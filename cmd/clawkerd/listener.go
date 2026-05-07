package main

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// startClawkerdListener binds the ClawkerdService listener on
// consts.DefaultClawkerdPort, registers the server impl, and starts
// grpc.Serve in a goroutine. mTLS is required and the peer cert's CN
// is pinned to consts.ContainerCP — sole legitimate caller is the
// CP. Without this pin, any other clawker-CA-signed cert (e.g.
// another agent's) would be accepted and could dispatch root-level
// ShellCommands (agent-to-agent privilege escalation).
//
// Returns the running grpc.Server so main can GracefulStop on
// shutdown. The underlying net.Listener is owned by the goroutine
// that runs Serve and is closed by Stop / GracefulStop.
func startClawkerdListener(boot *bootstrap, register *registerCoordinator, log *logger.Logger) (*grpc.Server, error) {
	tlsCfg, err := buildListenerTLSConfig(boot.CertPEM, boot.KeyPEM, boot.CACertPEM)
	if err != nil {
		return nil, fmt.Errorf("listener TLS config: %w", err)
	}

	addr := fmt.Sprintf(":%d", consts.DefaultClawkerdPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}

	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    consts.ClawkerdKeepaliveServerPingInterval,
			Timeout: consts.ClawkerdKeepalivePingTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             consts.ClawkerdKeepaliveMinClientPing,
			PermitWithoutStream: true,
		}),
	)
	clawkerdv1.RegisterClawkerdServiceServer(srv, &clawkerdServer{log: log, register: register})

	go func() {
		if serveErr := srv.Serve(lis); serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			log.Error().Err(serveErr).
				Str("event", "clawkerd_listener_serve_failed").
				Msg("grpc.Serve returned non-stop error")
		}
	}()

	log.Info().
		Str("event", "clawkerd_listener_started").
		Str("addr", addr).
		Msg("clawkerd listener accepting CP-only mTLS")

	return srv, nil
}

// buildListenerTLSConfig returns the *tls.Config for the clawkerd
// gRPC listener. ServerCert is the per-agent leaf the CLI minted —
// the leaf carries BOTH ServerAuth (used here, so CP-side chain
// verify accepts the cert as a server cert) and ClientAuth (held for
// any future agent→CP dial; clawkerd has no outbound RPC in this
// branch — see cmd/clawkerd/CLAUDE.md). See internal/auth/agent_cert.go
// for the dual-EKU rationale; without ServerAuth here every
// CP→clawkerd dial fails with "incompatible key usage".
//
// ClientCAs is the clawker CA bundle (so the CP's client cert chains
// validate). ClientAuth requires a verified peer cert.
// VerifyPeerCertificate then pins the peer CN to consts.ContainerCP
// AND asserts ClientAuth EKU on the peer cert (defense in depth — Go's
// TLS layer already enforces ClientAuth on client certs, but the
// app-layer assertion documents the dependency at the call site).
func buildListenerTLSConfig(certPEM, keyPEM, caPEM []byte) (*tls.Config, error) {
	leaf, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("agent leaf keypair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("CA PEM did not parse")
	}
	return &tls.Config{
		Certificates:          []tls.Certificate{leaf},
		ClientCAs:             pool,
		ClientAuth:            tls.RequireAndVerifyClientCert,
		MinVersion:            tls.VersionTLS13,
		VerifyPeerCertificate: pinPeerCNToCP,
	}, nil
}

// pinPeerCNToCP rejects any verified peer whose cert CN does not
// equal consts.ContainerCP, and additionally asserts the peer cert
// carries the ClientAuth EKU. Runs after the standard chain validation
// so verifiedChains is populated by the TLS stack.
//
// The ClientAuth EKU assertion is defense in depth: tls.Config with
// ClientAuth=RequireAndVerifyClientCert already enforces ClientAuth
// at the TLS layer (Go's default chain verify for client certs uses
// KeyUsages=[ClientAuth]). Re-asserting here documents the dependency
// at the call site so a future refactor that loosens TLS verification
// (e.g. to support a test that disables verify, or a switch to
// VerifyClientCertIfGiven) still fails closed at the application layer.
func pinPeerCNToCP(_ [][]byte, verifiedChains [][]*x509.Certificate) error {
	if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
		return errors.New("clawkerd listener: no verified peer chain")
	}
	leaf := verifiedChains[0][0]
	if subtle.ConstantTimeCompare([]byte(leaf.Subject.CommonName), []byte(consts.ContainerCP)) != 1 {
		return errors.New("clawkerd listener: peer CN not authorized")
	}
	if !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageClientAuth) {
		return errors.New("clawkerd listener: peer cert missing ClientAuth EKU")
	}
	return nil
}

// clawkerdServer is the ClawkerdServiceServer impl. The embedded
// Unimplemented*Server is the standard gRPC pattern for forward
// compatibility — any RPC added to the proto without a matching
// method here returns codes.Unimplemented instead of failing
// compilation.
//
// register is the CP-driven Register coordinator. It's shared across
// every Session a clawkerd serves for its container's lifetime so a
// CP retry that re-sends RegisterRequired short-circuits to the
// recorded outcome instead of burning the (single-use) Hydra
// assertion JWT a second time.
type clawkerdServer struct {
	clawkerdv1.UnimplementedClawkerdServiceServer
	log      *logger.Logger
	register *registerCoordinator
}

// Session is the bidi command-dispatch channel from CP to clawkerd.
// All per-stream state lives in runSession; this method just hands
// off and lets the helper own the lifecycle.
func (s *clawkerdServer) Session(stream clawkerdv1.ClawkerdService_SessionServer) error {
	return runSession(stream, s.log, s.register)
}

// clawker-cp is the containerized clawker control plane binary. It runs
// inside the privileged clawker-cp container as the replacement for the
// previous "sleep infinity" + docker-exec-ebpf-manager pattern.
//
// Responsibilities in v1:
//
//   - Load the eBPF programs once and keep their kernel links alive for
//     the process lifetime (fixes the hot-reload pinning bug that could
//     previously strip enforcement from other running containers during
//     firewall rule reloads).
//   - Serve ControlPlaneService over gRPC on a Unix domain socket with
//     mTLS + JWT authz, so the host-side clawker CLI can drive firewall
//     lifecycle operations without docker-exec'ing anything.
//   - Serve OIDC endpoints (/token, /.well-known/openid-configuration,
//     /keys) on a second UDS with mTLS so callers can obtain JWTs via
//     the client_credentials grant.
//   - Run Manager.Close + OIDC shutdown + gRPC GracefulStop on SIGTERM/SIGINT.
//
// The CP does not speak TCP in v1. All callers are local to the host and
// go through file-permission-gated Unix sockets. When the multi-caller
// follow-up lands (clawkerd, webui, etc.), a TCP listener with the same
// auth layer is added and clients plug in as additional OIDC client
// registrations. v1's auth shape is the final shape.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	v1 "github.com/schmitthub/clawker/internal/clawkerd/protocol/v1"
	"github.com/schmitthub/clawker/internal/controlplane"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/ebpf"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Default bind paths inside the clawker-cp container. These are mapped
// from <firewallDataDir> on the host via a single bind mount, so the
// client side reads its certs from the same physical directory the
// server writes them to.
const (
	defaultRunDir       = "/var/run/clawker-cp"
	defaultGRPCSocket   = "cp.sock"
	defaultOIDCSocket   = "cp-oidc.sock"
	defaultReadyFile    = "cp-ready"
	defaultShutdownWait = 5 * time.Second
)

func main() {
	runDir := flag.String("run-dir", defaultRunDir,
		"directory where the gRPC and OIDC UDS listeners bind, and where "+
			"cp-ready / cp-ca / cp-certs live. bind-mounted from the host "+
			"firewall data directory.")
	flag.Parse()

	// Host and container mount the same directory to different paths —
	// the host uses <firewallDataDir>, the container uses /var/run/clawker-cp.
	// ca.go wants the directory for cp-ca.{pem,key} and cp-certs/ —
	// pass the container-side path.
	if err := run(*runDir); err != nil {
		fmt.Fprintf(os.Stderr, "clawker-cp: %v\n", err)
		os.Exit(1)
	}
}

func run(runDir string) error {
	// Logger first, so subsequent errors show up structured in
	// `docker logs clawker-cp`. Using NewWriter(os.Stderr) so the
	// container runtime captures our JSON lines without us owning a
	// log file inside the container.
	log := logger.NewWriter(os.Stderr).
		With("component", "clawker-cp")
	defer log.Close()

	log.Info().
		Str("run_dir", runDir).
		Msg("clawker-cp starting")

	// Make sure the run dir exists — the host typically creates it when
	// the firewall data dir is set up, but `os.MkdirAll` is idempotent
	// and tolerates pre-existing directories so a reboot doesn't fail.
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("create run dir: %w", err)
	}

	// 1. Load (or generate) CA, server cert, CLI client cert, OIDC signing key.
	tlsMaterial, err := controlplane.LoadOrGenerateTLSMaterial(runDir)
	if err != nil {
		return fmt.Errorf("tls material: %w", err)
	}
	log.Info().Msg("tls material loaded")

	// 2. Load eBPF programs — this is the privileged operation that
	//    justifies the CP running as root in a CAP_BPF container.
	mgr := ebpf.NewManager(log)
	if err := mgr.Load(); err != nil {
		return fmt.Errorf("ebpf load: %w", err)
	}
	defer func() {
		if err := mgr.Close(); err != nil {
			log.Warn().Err(err).Msg("ebpf close returned error")
		}
	}()
	log.Info().Msg("ebpf programs loaded")

	// 3. Build the OIDC JWT issuer + verifier. The verifier is what the
	//    gRPC authz interceptor uses to check incoming bearer tokens.
	issuer := controlplane.NewTokenIssuer(tlsMaterial.OIDCSigningKey)

	// 4. Wire up both listeners + servers.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grpcServer, grpcLis, err := buildGRPCServer(ctx, log, runDir, tlsMaterial, issuer, mgr)
	if err != nil {
		return fmt.Errorf("build grpc server: %w", err)
	}
	defer grpcServer.Stop()

	oidcServer, oidcLis, err := buildOIDCServer(runDir, tlsMaterial, issuer)
	if err != nil {
		_ = grpcLis.Close()
		return fmt.Errorf("build oidc server: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), defaultShutdownWait)
		defer shutdownCancel()
		_ = oidcServer.Shutdown(shutdownCtx)
	}()

	// 5. Start serving in goroutines so we can block on a signal + clean shutdown.
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		log.Info().
			Str("addr", grpcLis.Addr().String()).
			Msg("gRPC listener serving")
		if err := grpcServer.Serve(grpcLis); err != nil {
			errCh <- fmt.Errorf("grpc serve: %w", err)
		}
	}()
	go func() {
		defer wg.Done()
		log.Info().
			Str("addr", oidcLis.Addr().String()).
			Msg("OIDC listener serving")
		if err := controlplane.ServeTLSOnListener(oidcServer, oidcLis); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("oidc serve: %w", err)
		}
	}()

	// 6. Signal readiness so the host-side firewall manager can progress
	//    past its EnsureRunning gate.
	if err := writeReadyFile(runDir); err != nil {
		return fmt.Errorf("write ready file: %w", err)
	}
	log.Info().Msg("clawker-cp ready")

	// 7. Block on SIGTERM/SIGINT OR a fatal listener error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case sig := <-sigCh:
		log.Info().Stringer("signal", sig).Msg("shutdown signal received")
	case err := <-errCh:
		log.Error().Err(err).Msg("listener failed")
		return err
	}

	// 8. Graceful shutdown: stop accepting new connections, give in-flight
	//    RPCs a moment to finish, then tear down.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		grpcServer.GracefulStop()
	}()
	select {
	case <-shutdownDone:
	case <-time.After(defaultShutdownWait):
		log.Warn().Msg("gRPC graceful stop timed out; forcing")
		grpcServer.Stop()
	}

	_ = removeReadyFile(runDir)
	wg.Wait()
	log.Info().Msg("clawker-cp stopped")
	return nil
}

// buildGRPCServer constructs the CP's gRPC server with mTLS credentials,
// the authz interceptor, and both the cherry-picked AgentReportingService
// (via controlplane.NewServer) and the new ControlPlaneService.
func buildGRPCServer(
	ctx context.Context,
	log *logger.Logger,
	runDir string,
	tlsMat *controlplane.TLSMaterial,
	issuer *controlplane.TokenIssuer,
	mgr *ebpf.Manager,
) (*grpc.Server, net.Listener, error) {
	_ = ctx // reserved for future use (e.g., token lifecycle background tasks)

	tlsCfg := serverTLSConfig(tlsMat)
	serverOpts := []grpc.ServerOption{
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.UnaryInterceptor(controlplane.AuthUnaryInterceptor(issuer.Verifier())),
		grpc.StreamInterceptor(controlplane.AuthStreamInterceptor(issuer.Verifier())),
	}

	// Construct the cherry-picked controlplane.Server. It registers
	// AgentReportingService on the underlying grpc.Server; we register
	// ControlPlaneService on top.
	cpServer := controlplane.NewServer(controlplane.Config{
		Log:           log,
		ServerOptions: serverOpts,
		// Secret / InitSpec / DockerClient / Cfg are only used by the
		// AgentReportingService handler, which is registered but has no
		// reachable callers in v1 (no TCP listener, no clawkerd).
		// Leaving them zero keeps v1 minimal; the multi-caller PR wires
		// them up when clawkerd lands.
	})

	handler := controlplane.NewControlPlaneHandler(mgr, log)
	v1.RegisterControlPlaneServiceServer(cpServer.GRPCServer(), handler)

	socketPath := filepath.Join(runDir, defaultGRPCSocket)
	lis, err := controlplane.ListenUnix(socketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("grpc uds listen: %w", err)
	}
	return cpServer.GRPCServer(), lis, nil
}

// buildOIDCServer wires the zitadel-compatible /token + /.well-known +
// /keys HTTP handlers onto an http.Server backed by a UDS listener.
func buildOIDCServer(
	runDir string,
	tlsMat *controlplane.TLSMaterial,
	issuer *controlplane.TokenIssuer,
) (*http.Server, net.Listener, error) {
	mux := controlplane.NewOIDCMux(issuer)
	tlsCfg := serverTLSConfig(tlsMat)
	srv := controlplane.NewTLSHTTPServer(mux, tlsCfg)

	socketPath := filepath.Join(runDir, defaultOIDCSocket)
	lis, err := controlplane.ListenUnix(socketPath)
	if err != nil {
		return nil, nil, fmt.Errorf("oidc uds listen: %w", err)
	}
	return srv, lis, nil
}

// serverTLSConfig assembles a *tls.Config that both listeners share.
// RequireAndVerifyClientCert means every incoming connection must present
// an mTLS client cert signed by cp-ca — this is the authentication layer
// that backs up the JWT authorization layer.
func serverTLSConfig(tlsMat *controlplane.TLSMaterial) *tls.Config {
	caPool := x509.NewCertPool()
	caPool.AddCert(tlsMat.CACert)

	return &tls.Config{
		Certificates: []tls.Certificate{
			{
				Certificate: [][]byte{tlsMat.ServerCertDER},
				PrivateKey:  tlsMat.ServerKey,
				Leaf:        tlsMat.ServerCert,
			},
		},
		ClientCAs:  caPool,
		ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS13,
	}
}

// writeReadyFile creates `<runDir>/cp-ready` to signal the host-side
// firewall manager's EnsureRunning gate that the CP has finished
// initialization and is serving. The file is removed in the shutdown
// path so a restart with the same run directory doesn't confuse poll
// loops by inheriting a stale ready marker.
func writeReadyFile(runDir string) error {
	path := filepath.Join(runDir, defaultReadyFile)
	// Write a minimal body so `cat cp-ready` in a debug session is
	// informative rather than ambiguously empty. Use a nonce so hosts
	// polling the file can distinguish a stale marker from a fresh one
	// even within the same process.
	nonce, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return err
	}
	body := fmt.Sprintf("pid=%d ts=%d nonce=%s\n",
		os.Getpid(), time.Now().Unix(), nonce.String())
	return os.WriteFile(path, []byte(body), 0o644)
}

func removeReadyFile(runDir string) error {
	return os.Remove(filepath.Join(runDir, defaultReadyFile))
}

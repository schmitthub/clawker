package controlplane_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	agentv1 "github.com/schmitthub/clawker/api/agent/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane"
	"github.com/schmitthub/clawker/internal/controlplane/agent"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/controlplane/agentslots"
	cpmocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

// startMTLSAgentServer mirrors startMTLSServer but for the agent
// listener: same TLS material the production CP wires onto cp.AgentPort
// (server cert signed by CLI CA, RequireAndVerifyClientCert, CLI CA pool
// for client verification) plus the agent-scope AuthInterceptor. The
// inspector is wired with a closure-backed stub so we can exercise the
// TLS layer without needing a real Docker daemon.
func startMTLSAgentServer(t *testing.T, introspector *cpmocks.IntrospectorMock) (string, *x509.CertPool) {
	t.Helper()

	require.NoError(t, auth.EnsureAuthMaterial())

	serverCertPath, err := consts.AuthServerCertPath()
	require.NoError(t, err)
	serverKeyPath, err := consts.AuthServerKeyPath()
	require.NoError(t, err)
	serverCert, err := tls.LoadX509KeyPair(serverCertPath, serverKeyPath)
	require.NoError(t, err)

	caCert, err := auth.CACert()
	require.NoError(t, err)
	caCertPool := x509.NewCertPool()
	caCertPool.AddCert(caCert)

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS13,
	}

	log := logger.Nop()
	interceptor := controlplane.NewAuthInterceptor(introspector, controlplane.AgentMethodScopes(), log)

	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.ChainUnaryInterceptor(interceptor.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(interceptor.StreamInterceptor()),
	)

	// Real handler so a successful TLS+token client is allowed to invoke
	// Register and observe a post-handshake (PermissionDenied) failure
	// from the slot lookup — the point being that the channel is open.
	slots := agentslots.NewRegistry(time.Now, time.Hour, log)
	t.Cleanup(slots.Stop)
	reg := agentregistry.NewRegistry(log)
	inspector := agentInspectorFn(func(_ context.Context, _ string) (agent.ContainerInfo, error) {
		return agent.ContainerInfo{}, nil
	})
	handler := agent.NewHandler(slots, reg, inspector, log)
	agentv1.RegisterAgentServiceServer(srv, handler)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("agent grpc serve: %v", err)
		}
	}()
	t.Cleanup(srv.Stop)

	return lis.Addr().String(), caCertPool
}

// agentInspectorFn satisfies agent.ContainerInspector with a closure so
// the TLS-layer tests don't need to spin up the moq-generated mock from
// agent/mocks (which would create import noise here).
type agentInspectorFn func(ctx context.Context, id string) (agent.ContainerInfo, error)

func (f agentInspectorFn) Inspect(ctx context.Context, id string) (agent.ContainerInfo, error) {
	return f(ctx, id)
}

// allowAllAgentIntrospector mirrors allowAllIntrospector but issues a
// token with the agent self-register scope so any RPC that survives the
// TLS handshake also passes the AuthInterceptor's scope check, exposing
// the underlying handler behavior.
func allowAllAgentIntrospector() *cpmocks.IntrospectorMock {
	return &cpmocks.IntrospectorMock{
		IntrospectFunc: func(_ context.Context, _, _ string) (*controlplane.IntrospectionResult, error) {
			return &controlplane.IntrospectionResult{
				Active:   true,
				Scope:    consts.ScopeAgentSelfRegister,
				ClientID: consts.ClientIDAgent,
				Sub:      consts.ClientIDAgent,
			}, nil
		},
	}
}

// --- mTLS connection tests for the agent listener ---

// Tests that a client presenting no client cert is rejected by the
// agent listener. Catches a regression to RequestClientCert that would
// silently let unauthenticated peers reach AgentService.Register.
func TestMTLSAgent_NoClientCert_Rejected(t *testing.T) {
	testenv.New(t)
	addr, caCertPool := startMTLSAgentServer(t, allowAllAgentIntrospector())

	tlsCfg := &tls.Config{
		RootCAs:    caCertPool,
		ServerName: consts.ContainerCP,
		MinVersion: tls.VersionTLS13,
	}

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	client := agentv1.NewAgentServiceClient(conn)
	ctx := withBearer(context.Background(), "agent-token")
	_, err = client.Register(ctx, &agentv1.RegisterRequest{
		AgentName:    "clawker.example",
		CodeVerifier: "verifier-not-used-handshake-fails-first",
	})
	require.Error(t, err, "agent listener must reject connections without a client cert")
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// Tests that a client presenting a client cert signed by an UNTRUSTED
// CA is rejected at the TLS handshake. RequireAndVerifyClientCert +
// ClientCAs containing only the CLI CA is the load-bearing config —
// any swap to a different verification mode would let arbitrary CAs in.
func TestMTLSAgent_UntrustedCAClientCert_Rejected(t *testing.T) {
	testenv.New(t)
	addr, caCertPool := startMTLSAgentServer(t, allowAllAgentIntrospector())

	clientCert := mintCertFromFreshCA(t)

	tlsCfg := &tls.Config{
		RootCAs:      caCertPool, // trust the server cert (CLI CA-signed)
		Certificates: []tls.Certificate{clientCert},
		ServerName:   consts.ContainerCP,
		MinVersion:   tls.VersionTLS13,
	}

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	client := agentv1.NewAgentServiceClient(conn)
	ctx := withBearer(context.Background(), "agent-token")
	_, err = client.Register(ctx, &agentv1.RegisterRequest{
		AgentName:    "clawker.example",
		CodeVerifier: "verifier-not-used-handshake-fails-first",
	})
	require.Error(t, err, "agent listener must reject client certs not signed by the CLI CA")
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// Tests that a client presenting a CLI-CA-signed client cert clears
// the TLS handshake. Register itself returns PermissionDenied because
// no slot is registered — that's the agent handler's fail-closed
// contract for any unknown agent and it confirms the request reached
// the handler (i.e. the channel was authenticated, the auth token
// passed scope check, and the call was dispatched).
func TestMTLSAgent_ValidCLIClientCert_HandshakeSucceeds(t *testing.T) {
	testenv.New(t)
	addr, caCertPool := startMTLSAgentServer(t, allowAllAgentIntrospector())

	clientCert, err := auth.LoadClientCert()
	require.NoError(t, err)

	tlsCfg := &tls.Config{
		RootCAs:      caCertPool,
		Certificates: []tls.Certificate{clientCert},
		ServerName:   consts.ContainerCP,
		MinVersion:   tls.VersionTLS13,
	}

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	client := agentv1.NewAgentServiceClient(conn)
	ctx := withBearer(context.Background(), "agent-token")
	_, err = client.Register(ctx, &agentv1.RegisterRequest{
		AgentName:    "clawker.unregistered-agent",
		CodeVerifier: "00000000000000000000000000000000000000000000",
	})
	require.Error(t, err, "Register must fail post-handshake (unknown slot)")
	assert.Equal(t, codes.PermissionDenied, status.Code(err),
		"failure must be PermissionDenied (handler-level), not Unavailable (TLS-level): got %v", err)
}

// Tests that a plaintext (non-TLS) connection to the agent listener
// is rejected. A regression that drops mTLS entirely would surface
// here as a successful plaintext connection.
func TestMTLSAgent_NoTLS_Rejected(t *testing.T) {
	testenv.New(t)
	addr, _ := startMTLSAgentServer(t, allowAllAgentIntrospector())

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	client := agentv1.NewAgentServiceClient(conn)
	ctx := withBearer(context.Background(), "agent-token")
	_, err = client.Register(ctx, &agentv1.RegisterRequest{
		AgentName:    "clawker.example",
		CodeVerifier: "verifier-not-used-handshake-fails-first",
	})
	require.Error(t, err, "agent listener must reject plaintext connections")
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// mintCertFromFreshCA generates a self-contained CA + leaf cert pair so
// the leaf is signed by a CA the server does NOT trust. The server's
// ClientCAs pool only contains the CLI CA — this leaf must be rejected
// at handshake.
func mintCertFromFreshCA(t *testing.T) tls.Certificate {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "untrusted-test-CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: leafSerial,
		Subject:      pkix.Name{CommonName: "untrusted-test-client"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)

	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER})

	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	return pair
}

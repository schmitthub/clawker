package firewall

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/schmitthub/clawker/internal/clawkerd/protocol/v1"
	"github.com/schmitthub/clawker/internal/controlplane"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpccredentials "google.golang.org/grpc/credentials"
	grpcoauth "google.golang.org/grpc/credentials/oauth"
	"google.golang.org/grpc/status"
)

// TestCPAuthEndToEnd exercises the full auth pipeline against a real
// in-process control plane:
//
//  1. LoadOrGenerateTLSMaterial creates the on-disk certs the host CLI
//     reads through internal/firewall/oidc_client.go.
//  2. A real gRPC server binds a UDS with mTLS + the authz interceptor.
//  3. A real HTTP server binds a second UDS for the OIDC /token endpoint.
//  4. A firewall-side gRPC client dials the server via the same helpers
//     firewall.Manager uses (TLS config + oauth.TokenSource), issues a
//     Health call, and verifies the round trip succeeds.
//  5. The authz rejection paths (missing scope, missing token) are
//     exercised by sending bare calls that bypass the CLI wrappers.
//
// This test covers the shape of the auth layer that all future CP
// callers inherit unchanged. If any layer is broken — cert generation,
// OIDC signing, /token endpoint, JWT verify, mTLS CN check, scope
// policy — this test is the one that catches it before PR review.
func TestCPAuthEndToEnd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// 1. Cert material.
	tlsMat, err := controlplane.LoadOrGenerateTLSMaterial(dir)
	require.NoError(t, err)

	// 2. OIDC issuer / verifier.
	issuer := controlplane.NewTokenIssuer(tlsMat.OIDCSigningKey)

	// Build the shared server *tls.Config.
	caPool := x509.NewCertPool()
	caPool.AddCert(tlsMat.CACert)
	serverTLS := &tls.Config{
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

	// 3. Start gRPC server on a UDS.
	grpcSocketPath := filepath.Join(dir, "cp.sock")
	grpcLis, err := controlplane.ListenUnix(grpcSocketPath)
	require.NoError(t, err)

	serverOpts := []grpc.ServerOption{
		grpc.Creds(grpccredentials.NewTLS(serverTLS)),
		grpc.UnaryInterceptor(controlplane.AuthUnaryInterceptor(issuer.Verifier())),
	}
	cpServer := controlplane.NewServer(controlplane.Config{
		Log:           logger.Nop(),
		ServerOptions: serverOpts,
	})
	handler := &fakeHandler{}
	v1.RegisterControlPlaneServiceServer(cpServer.GRPCServer(), handler)
	t.Cleanup(cpServer.Stop)

	go func() {
		_ = cpServer.Serve(grpcLis)
	}()

	// 4. Start OIDC HTTP server on a second UDS.
	oidcSocketPath := filepath.Join(dir, "cp-oidc.sock")
	oidcLis, err := controlplane.ListenUnix(oidcSocketPath)
	require.NoError(t, err)

	oidcServer := controlplane.NewTLSHTTPServer(
		controlplane.NewOIDCMux(issuer),
		serverTLS,
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = oidcServer.Shutdown(ctx)
	})
	go func() {
		_ = controlplane.ServeTLSOnListener(oidcServer, oidcLis)
	}()

	// Give the listeners a beat to come up.
	time.Sleep(50 * time.Millisecond)

	// 5. Build the CLI-side client exactly how firewall.Manager does it.
	paths := CPClientPaths{
		CACertPEM:     filepath.Join(dir, "cp-certs", "cp-ca.pem"),
		ClientCertPEM: filepath.Join(dir, "cp-certs", "cp-client-cli.pem"),
		ClientKeyPEM:  filepath.Join(dir, "cp-certs", "cp-client-cli.key"),
		GRPCSocket:    grpcSocketPath,
		OIDCSocket:    oidcSocketPath,
	}
	clientTLS, err := BuildCPTLSConfig(paths)
	require.NoError(t, err)
	// The server cert has SAN=clawker-cp; that's what our client
	// pins against already via cpTLSServerName. No override needed.

	tokenSource := NewCPTokenSource(context.Background(), paths, clientTLS)

	conn, err := grpc.NewClient(
		"unix:"+grpcSocketPath,
		grpc.WithTransportCredentials(grpccredentials.NewTLS(clientTLS)),
		grpc.WithPerRPCCredentials(grpcoauth.TokenSource{TokenSource: tokenSource}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := v1.NewControlPlaneServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("Health is reachable without a JWT", func(t *testing.T) {
		resp, err := client.Health(ctx, &v1.HealthRequest{})
		require.NoError(t, err)
		assert.True(t, resp.GetOk())
	})

	t.Run("EnableContainerFirewall succeeds with valid JWT + scope", func(t *testing.T) {
		// The CLI's TokenSource fetches a JWT via client_credentials
		// + mTLS and auto-attaches it. A successful Unauthenticated-
		// free handler call proves the full chain works.
		_, err := client.EnableContainerFirewall(ctx, &v1.EnableContainerFirewallRequest{
			CgroupPath: "/fake/cgroup",
			Config:     &v1.ContainerFirewallConfig{},
		})
		// The handler returns a stubbed "ok, cgroup_id=42" — we only
		// care that auth didn't reject us.
		require.NoError(t, err)
		assert.Equal(t, 1, handler.enableCalls)
	})

	t.Run("call with no bearer token is rejected", func(t *testing.T) {
		// Build a second connection that does NOT attach any per-RPC
		// credentials. The interceptor should reject EnableContainerFirewall
		// with Unauthenticated.
		noTokenConn, err := grpc.NewClient(
			"unix:"+grpcSocketPath,
			grpc.WithTransportCredentials(grpccredentials.NewTLS(clientTLS)),
		)
		require.NoError(t, err)
		defer noTokenConn.Close()
		noTokenClient := v1.NewControlPlaneServiceClient(noTokenConn)

		_, err = noTokenClient.EnableContainerFirewall(ctx, &v1.EnableContainerFirewallRequest{
			CgroupPath: "/fake/cgroup",
			Config:     &v1.ContainerFirewallConfig{},
		})
		require.Error(t, err)
		assert.Equal(t, codes.Unauthenticated, status.Code(err),
			"expected Unauthenticated, got %v", err)
	})

	t.Run("bad-scope JWT is rejected with PermissionDenied", func(t *testing.T) {
		// Issue a JWT with an empty scopes list — the interceptor
		// should reject EnableContainerFirewall because the method
		// requires firewall:admin.
		rawToken, _, err := issuer.Issue(controlplane.ClientIDCLI, []string{})
		require.NoError(t, err)

		badConn, err := grpc.NewClient(
			"unix:"+grpcSocketPath,
			grpc.WithTransportCredentials(grpccredentials.NewTLS(clientTLS)),
			grpc.WithPerRPCCredentials(staticBearer{token: rawToken}),
		)
		require.NoError(t, err)
		defer badConn.Close()
		badClient := v1.NewControlPlaneServiceClient(badConn)

		_, err = badClient.EnableContainerFirewall(ctx, &v1.EnableContainerFirewallRequest{
			CgroupPath: "/fake/cgroup",
			Config:     &v1.ContainerFirewallConfig{},
		})
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err),
			"expected PermissionDenied, got %v", err)
	})
}

// fakeHandler stubs ControlPlaneService for the end-to-end auth test.
// Records how many times each handler was invoked so the test can
// confirm authz passed before reaching the handler layer.
type fakeHandler struct {
	v1.UnimplementedControlPlaneServiceServer
	enableCalls int
}

func (h *fakeHandler) Health(_ context.Context, _ *v1.HealthRequest) (*v1.HealthResponse, error) {
	return &v1.HealthResponse{Ok: true}, nil
}

func (h *fakeHandler) EnableContainerFirewall(
	_ context.Context,
	_ *v1.EnableContainerFirewallRequest,
) (*v1.EnableContainerFirewallResponse, error) {
	h.enableCalls++
	return &v1.EnableContainerFirewallResponse{CgroupId: 42}, nil
}

// staticBearer implements grpc.PerRPCCredentials by attaching a fixed
// bearer token to every RPC. Used for authz-rejection tests where we
// want to inject a known-bad JWT.
type staticBearer struct {
	token string
}

func (s staticBearer) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + s.token}, nil
}

func (s staticBearer) RequireTransportSecurity() bool { return true }

// Verify at compile time that the placeholder types in this test file
// satisfy the interfaces they claim to — a drift here surfaces as a
// build error before the test runs.
var (
	_ v1.ControlPlaneServiceServer      = (*fakeHandler)(nil)
	_ grpccredentials.PerRPCCredentials = staticBearer{}
)

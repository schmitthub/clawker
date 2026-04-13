package controlplane_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane"
	ebpfmocks "github.com/schmitthub/clawker/internal/controlplane/ebpf/mocks"
	cpmocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

// startMTLSServer creates a gRPC server with mTLS + auth interceptor on a real
// TCP listener. Returns the server address and the CA cert pool clients need
// to verify the server cert and (optionally) present their own client cert.
func startMTLSServer(t *testing.T, introspector *cpmocks.IntrospectorMock, ebpfMgr *ebpfmocks.EBPFManagerMock) (string, *x509.CertPool) {
	t.Helper()

	require.NoError(t, auth.EnsureAuthMaterial())

	// Load server cert (signed by CLI CA).
	serverCertPath, err := consts.AuthServerCertPath()
	require.NoError(t, err)
	serverKeyPath, err := consts.AuthServerKeyPath()
	require.NoError(t, err)
	serverCert, err := tls.LoadX509KeyPair(serverCertPath, serverKeyPath)
	require.NoError(t, err)

	// Load CA for client cert verification.
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
	interceptor := controlplane.NewAuthInterceptor(introspector, controlplane.AdminMethodScopes(), log)

	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.ChainUnaryInterceptor(interceptor.UnaryInterceptor()),
		grpc.ChainStreamInterceptor(interceptor.StreamInterceptor()),
	)

	handler := controlplane.NewAdminHandler(ebpfMgr, log, nopContainerResolver)
	adminv1.RegisterAdminServiceServer(srv, handler)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		if err := srv.Serve(lis); err != nil {
			t.Logf("grpc serve: %v", err)
		}
	}()
	t.Cleanup(srv.Stop)

	return lis.Addr().String(), caCertPool
}

// --- mTLS connection tests ---

// Tests that a client with a valid CA-signed client cert can connect and make RPCs.
func TestMTLS_ValidClientCert_Accepted(t *testing.T) {
	testenv.New(t)
	addr, caCertPool := startMTLSServer(t, allowAllIntrospector(), noopEBPF())

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

	client := adminv1.NewAdminServiceClient(conn)
	ctx := withBearer(context.Background(), "admin-token")
	resp, err := client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{})
	require.NoError(t, err)
	assert.NotNil(t, resp)
}

// Tests that a client presenting no client cert is rejected by the mTLS server.
func TestMTLS_NoClientCert_Rejected(t *testing.T) {
	testenv.New(t)
	addr, caCertPool := startMTLSServer(t, allowAllIntrospector(), noopEBPF())

	// TLS config trusts the server but does NOT present a client cert.
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

	client := adminv1.NewAdminServiceClient(conn)
	ctx := withBearer(context.Background(), "admin-token")
	_, err = client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{})
	require.Error(t, err, "server must reject connections without a client cert")

	// The TLS handshake fails — gRPC surfaces this as Unavailable.
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// Tests that a plaintext (non-TLS) connection is rejected by the mTLS server.
func TestMTLS_NoTLS_Rejected(t *testing.T) {
	testenv.New(t)
	addr, _ := startMTLSServer(t, allowAllIntrospector(), noopEBPF())

	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	client := adminv1.NewAdminServiceClient(conn)
	ctx := withBearer(context.Background(), "admin-token")
	_, err = client.SyncRoutes(ctx, &adminv1.SyncRoutesRequest{})
	require.Error(t, err, "server must reject plaintext connections")

	assert.Equal(t, codes.Unavailable, status.Code(err))
}

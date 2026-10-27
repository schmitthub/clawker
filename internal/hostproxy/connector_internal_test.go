package hostproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	hostproxyv1 "github.com/schmitthub/clawker/api/hostproxy/v1"
	"github.com/schmitthub/clawker/controlplane/hostgateway"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

// cpTLS returns the CP's server TLS config and the host proxy's client
// TLS config from the real auth material.
func cpTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	testenv.New(t)
	require.NoError(t, auth.EnsureAuthMaterial())
	caCert, err := auth.CACert()
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	certPath, err := consts.AuthServerCertPath()
	require.NoError(t, err)
	keyPath, err := consts.AuthServerKeyPath()
	require.NoError(t, err)
	serverCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)
	hpCert, err := auth.LoadHostProxyClientCert()
	require.NoError(t, err)
	var server tls.Config
	server.Certificates = []tls.Certificate{serverCert}
	server.ClientAuth = tls.RequireAndVerifyClientCert
	server.ClientCAs = pool
	server.MinVersion = tls.VersionTLS13
	var client tls.Config
	client.RootCAs = pool
	client.ServerName = consts.ContainerCP
	client.Certificates = []tls.Certificate{hpCert}
	client.MinVersion = tls.VersionTLS13
	return &server, &client
}

// serveCP serves a fresh gateway on addr and returns it with a stop func.
func serveCP(t *testing.T, addr string, tlsCfg *tls.Config) (*hostgateway.Gateway, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	gw := hostgateway.New(logger.Nop())
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	hostproxyv1.RegisterHostProxyServiceServer(srv, gw)
	go func() {
		if serveErr := srv.Serve(lis); serveErr != nil {
			t.Logf("serve: %v", serveErr)
		}
	}()
	t.Cleanup(srv.Stop)
	return gw, srv.Stop
}

// TestConnector_ServesActionsAndReconnects: the host proxy dials out to
// the CP and serves the actions the CP sends. After a CP restart it
// reconnects by itself.
func TestConnector_ServesActionsAndReconnects(t *testing.T) {
	serverTLS, clientTLS := cpTLS(t)
	addr := net.JoinHostPort(consts.Localhost, "0")
	lis, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	addr = lis.Addr().String()
	require.NoError(t, lis.Close())

	gw, stop := serveCP(t, addr, serverTLS)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	require.NoError(t, err)
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Logf("close: %v", closeErr)
		}
	})
	actions, opened := newTestActions(t, "")
	c := NewConnector(hostproxyv1.NewHostProxyServiceClient(conn), actions, logger.Nop())
	c.minBackoff = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	agent := hostgateway.Identity{Project: "p", Agent: "a", ContainerID: "ctr"}
	openVia := func(gw *hostgateway.Gateway, u string) func() bool {
		return func() bool { return gw.OpenURL(context.Background(), agent, u) == nil }
	}
	require.Eventually(t, openVia(gw, "https://first.test/"), 10*time.Second, 20*time.Millisecond)
	assert.Contains(t, *opened, "https://first.test/")

	stop()
	gw2, _ := serveCP(t, addr, serverTLS)
	require.Eventually(t, openVia(gw2, "https://second.test/"), 10*time.Second, 50*time.Millisecond,
		"the host proxy must reconnect after a CP restart")
	assert.Contains(t, *opened, "https://second.test/")
}

// TestConnector_RunEndsWithContext: cancelling the daemon context stops
// the connector, also while the CP is unreachable.
func TestConnector_RunEndsWithContext(t *testing.T) {
	var tlsCfg tls.Config
	tlsCfg.MinVersion = tls.VersionTLS13
	conn, err := grpc.NewClient("passthrough:///"+net.JoinHostPort(consts.Localhost, "1"),
		grpc.WithTransportCredentials(credentials.NewTLS(&tlsCfg)))
	require.NoError(t, err)
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Logf("close: %v", closeErr)
		}
	})
	actions, _ := newTestActions(t, "")
	c := NewConnector(hostproxyv1.NewHostProxyServiceClient(conn), actions, logger.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

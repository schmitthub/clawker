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
// TLS config from the auth material on disk. Called again after a
// rotation, it returns the new identity.
func cpTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
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

// freeAddr reserves and releases a loopback address for a CP server.
func freeAddr(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", net.JoinHostPort(consts.Localhost, "0"))
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())
	return addr
}

// diskDialer dials addr with the host proxy identity on disk at call time,
// as adminclient.DialHostProxy does.
func diskDialer(t *testing.T, addr string) Dialer {
	t.Helper()
	return func() (*grpc.ClientConn, error) {
		_, clientTLS := cpTLS(t)
		return grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	}
}

// runConnector starts c and stops it at test end.
func runConnector(t *testing.T, c *Connector) {
	t.Helper()
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
}

func testAgent() hostgateway.Identity {
	return hostgateway.Identity{Project: "p", Agent: "a", ContainerID: "ctr"}
}

func openVia(gw *hostgateway.Gateway, u string) func() bool {
	return func() bool { return gw.OpenURL(context.Background(), testAgent(), u) == nil }
}

// TestConnector_ServesActionsAndReconnects: the host proxy dials out to
// the CP and serves the actions the CP sends. After a CP restart it
// reconnects by itself.
func TestConnector_ServesActionsAndReconnects(t *testing.T) {
	testenv.New(t)
	require.NoError(t, auth.EnsureAuthMaterial())
	serverTLS, _ := cpTLS(t)
	addr := freeAddr(t)
	gw, stop := serveCP(t, addr, serverTLS)

	actions, opened := newTestActions(t, "")
	runConnector(t, NewConnector(diskDialer(t, addr), actions, logger.Nop()))

	require.Eventually(t, openVia(gw, "https://first.test/"), 10*time.Second, 20*time.Millisecond)
	assert.Contains(t, *opened, "https://first.test/")

	stop()
	gw2, _ := serveCP(t, addr, serverTLS)
	require.Eventually(t, openVia(gw2, "https://second.test/"), 10*time.Second, 50*time.Millisecond,
		"the host proxy must reconnect after a CP restart")
	assert.Contains(t, *opened, "https://second.test/")
}

// TestConnector_ReloadsIdentityOnReconnect: `clawker auth rotate --force`
// replaces the CA and the host proxy leaf. The connector dials again for
// each attempt, so the restarted CP, which trusts only the new CA,
// accepts the reconnect without a daemon restart.
func TestConnector_ReloadsIdentityOnReconnect(t *testing.T) {
	testenv.New(t)
	require.NoError(t, auth.EnsureAuthMaterial())
	serverTLS, _ := cpTLS(t)
	addr := freeAddr(t)
	gw, stop := serveCP(t, addr, serverTLS)

	actions, opened := newTestActions(t, "")
	runConnector(t, NewConnector(diskDialer(t, addr), actions, logger.Nop()))
	require.Eventually(t, openVia(gw, "https://before.test/"), 10*time.Second, 20*time.Millisecond)

	require.NoError(t, auth.RotateAuthMaterial(true))
	stop()
	newServerTLS, _ := cpTLS(t)
	gw2, _ := serveCP(t, addr, newServerTLS)
	require.Eventually(t, openVia(gw2, "https://after.test/"), 10*time.Second, 50*time.Millisecond,
		"the host proxy must reconnect with the rotated identity")
	assert.Contains(t, *opened, "https://after.test/")
}

// TestConnector_BlockedBrowserDoesNotStallReconnect: a stuck browser
// opener ends with the stream context, so the connector reconnects
// instead of waiting on it.
func TestConnector_BlockedBrowserDoesNotStallReconnect(t *testing.T) {
	testenv.New(t)
	require.NoError(t, auth.EnsureAuthMaterial())
	serverTLS, _ := cpTLS(t)
	addr := freeAddr(t)
	gw, stop := serveCP(t, addr, serverTLS)

	actions, _ := newTestActions(t, "")
	released := make(chan struct{}, 1)
	actions.browserFunc = func(ctx context.Context, u string) error {
		if u != "https://stuck.test/" {
			return nil
		}
		<-ctx.Done()
		released <- struct{}{}
		return ctx.Err()
	}
	runConnector(t, NewConnector(diskDialer(t, addr), actions, logger.Nop()))
	require.Eventually(t, openVia(gw, "https://ready.test/"), 10*time.Second, 20*time.Millisecond)

	// The CP gives up quickly; the host action stays blocked on the browser.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	require.Error(t, gw.OpenURL(ctx, testAgent(), "https://stuck.test/"))

	stop()
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("stream end did not cancel the blocked browser action")
	}
	gw2, _ := serveCP(t, addr, serverTLS)
	require.Eventually(t, openVia(gw2, "https://again.test/"), 10*time.Second, 50*time.Millisecond,
		"the host proxy must reconnect after the blocked action")
}

// TestConnector_EnforcesCPDeadline: the action context ends at the
// deadline the CP sends, while the stream stays up, so a stalled helper
// cannot outlive the call the CP already gave up on.
func TestConnector_EnforcesCPDeadline(t *testing.T) {
	testenv.New(t)
	require.NoError(t, auth.EnsureAuthMaterial())
	serverTLS, _ := cpTLS(t)
	addr := freeAddr(t)
	gw, _ := serveCP(t, addr, serverTLS)

	actions, _ := newTestActions(t, "")
	ended := make(chan error, 1)
	actions.browserFunc = func(ctx context.Context, u string) error {
		if u != "https://slow.test/" {
			return nil
		}
		<-ctx.Done()
		ended <- ctx.Err()
		return ctx.Err()
	}
	runConnector(t, NewConnector(diskDialer(t, addr), actions, logger.Nop()))
	require.Eventually(t, openVia(gw, "https://ready.test/"), 10*time.Second, 20*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	require.Error(t, gw.OpenURL(ctx, testAgent(), "https://slow.test/"))

	select {
	case err := <-ended:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the host action outlived the CP deadline")
	}
}

// TestConnector_RunEndsWithContext: cancelling the daemon context stops
// the connector, also while the CP is unreachable.
func TestConnector_RunEndsWithContext(t *testing.T) {
	var tlsCfg tls.Config
	tlsCfg.MinVersion = tls.VersionTLS13
	dial := func() (*grpc.ClientConn, error) {
		return grpc.NewClient("passthrough:///"+net.JoinHostPort(consts.Localhost, "1"),
			grpc.WithTransportCredentials(credentials.NewTLS(&tlsCfg)))
	}
	actions, _ := newTestActions(t, "")
	c := NewConnector(dial, actions, logger.Nop())
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

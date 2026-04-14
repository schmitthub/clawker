package factory

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

func TestNew(t *testing.T) {
	f := New("1.0.0")

	if f.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got '%s'", f.Version)
	}
	if f.IOStreams == nil {
		t.Error("expected IOStreams to be non-nil")
	}
	if f.TUI == nil {
		t.Error("expected TUI to be non-nil")
	}
}

// TestCacheableState covers INV-B2-005: the closure must treat
// Ready/Connecting/Idle as cacheable (no rebuild), and
// TransientFailure/Shutdown as triggers for rebuild.
func TestCacheableState(t *testing.T) {
	cases := []struct {
		state connectivity.State
		want  bool
	}{
		{connectivity.Ready, true},
		{connectivity.Connecting, true},
		{connectivity.Idle, true},
		{connectivity.TransientFailure, false},
		{connectivity.Shutdown, false},
	}
	for _, c := range cases {
		if got := cacheableState(c.state); got != c.want {
			t.Errorf("cacheableState(%s) = %v, want %v", c.state, got, c.want)
		}
	}
}

// newTestFactory builds a Factory whose Config/Logger/Client closures
// succeed without touching the filesystem or Docker daemon. The caller
// is expected to swap ensureRunning + dialAdmin seams for the specific
// behavior under test.
func newTestFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	cfg := configmocks.NewBlankConfig()
	return &cmdutil.Factory{
		Config: func() (config.Config, error) { return cfg, nil },
		Logger: func() (*logger.Logger, error) { return logger.Nop(), nil },
		// ensureRunning seam ignores the returned *docker.Client, so
		// nil is safe here — no real Docker daemon required.
		Client: func(_ context.Context) (*docker.Client, error) { return nil, nil },
	}
}

// newFakeConn returns an unconnected *grpc.ClientConn in the Idle
// state. grpc.NewClient does not dial until the first RPC, so this is
// synchronous and safe under test. Callers may Close() it to force the
// Shutdown state.
func newFakeConn(t *testing.T) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///test", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	return conn
}

// withSeams swaps ensureRunning + dialAdmin for the duration of a
// test. The t.Cleanup restores originals so parallel tests don't
// bleed state.
func withSeams(t *testing.T, er func(context.Context, *docker.Client, config.Config, *logger.Logger) error,
	da func(context.Context, int, int, ...grpc.DialOption) (adminv1.AdminServiceClient, *grpc.ClientConn, error)) {
	t.Helper()
	origER := ensureRunning
	origDA := dialAdmin
	ensureRunning = er
	dialAdmin = da
	t.Cleanup(func() {
		ensureRunning = origER
		dialAdmin = origDA
	})
}

// TestAdminClient_CachesOnSuccessiveCalls covers INV-B2-005 assertion
// (1): ensureRunning fires exactly once across multiple calls when the
// connection is cacheable (Idle after first dial).
func TestAdminClient_CachesOnSuccessiveCalls(t *testing.T) {
	var ensureCalls int32
	var dialCalls int32
	conn := newFakeConn(t)
	t.Cleanup(func() { _ = conn.Close() })

	withSeams(t,
		func(ctx context.Context, _ *docker.Client, _ config.Config, _ *logger.Logger) error {
			atomic.AddInt32(&ensureCalls, 1)
			return nil
		},
		func(_ context.Context, _, _ int, _ ...grpc.DialOption) (adminv1.AdminServiceClient, *grpc.ClientConn, error) {
			atomic.AddInt32(&dialCalls, 1)
			return adminv1.NewAdminServiceClient(conn), conn, nil
		},
	)

	f := newTestFactory(t)
	getClient := adminClientFunc(f)

	ctx := context.Background()
	c1, err := getClient(ctx)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	c2, err := getClient(ctx)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	c3, err := getClient(ctx)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}

	if got := atomic.LoadInt32(&ensureCalls); got != 1 {
		t.Errorf("ensureRunning called %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&dialCalls); got != 1 {
		t.Errorf("dialAdmin called %d times, want 1", got)
	}
	if c1 != c2 || c2 != c3 {
		t.Error("expected same client instance across cached calls")
	}
	if state := conn.GetState(); state != connectivity.Idle {
		t.Errorf("conn state = %s, want Idle (cacheable)", state)
	}
}

// TestAdminClient_RebuildsAfterShutdown covers INV-B2-005 assertion
// (2): when the cached grpc.ClientConn transitions to Shutdown, the
// closure must tear it down and rebuild (ensureRunning + dialAdmin
// fire again).
func TestAdminClient_RebuildsAfterShutdown(t *testing.T) {
	var ensureCalls int32
	var dialCalls int32
	// Each dial returns a fresh conn so the test can distinguish
	// cached-vs-rebuilt clients by identity.
	conns := make([]*grpc.ClientConn, 0, 2)

	withSeams(t,
		func(ctx context.Context, _ *docker.Client, _ config.Config, _ *logger.Logger) error {
			atomic.AddInt32(&ensureCalls, 1)
			return nil
		},
		func(_ context.Context, _, _ int, _ ...grpc.DialOption) (adminv1.AdminServiceClient, *grpc.ClientConn, error) {
			atomic.AddInt32(&dialCalls, 1)
			c := newFakeConn(t)
			conns = append(conns, c)
			return adminv1.NewAdminServiceClient(c), c, nil
		},
	)
	t.Cleanup(func() {
		for _, c := range conns {
			_ = c.Close()
		}
	})

	f := newTestFactory(t)
	getClient := adminClientFunc(f)

	ctx := context.Background()
	c1, err := getClient(ctx)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Force the cached conn into Shutdown. grpc.ClientConn.Close
	// transitions state to Shutdown synchronously.
	if err := conns[0].Close(); err != nil {
		t.Fatalf("close first conn: %v", err)
	}
	if state := conns[0].GetState(); state != connectivity.Shutdown {
		t.Fatalf("conn state after Close = %s, want Shutdown", state)
	}

	c2, err := getClient(ctx)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if got := atomic.LoadInt32(&ensureCalls); got != 2 {
		t.Errorf("ensureRunning called %d times, want 2 (rebuild)", got)
	}
	if got := atomic.LoadInt32(&dialCalls); got != 2 {
		t.Errorf("dialAdmin called %d times, want 2 (rebuild)", got)
	}
	if c1 == c2 {
		t.Error("expected different client instance after rebuild")
	}
}

// TestAdminClientKeepaliveParams covers INV-B2-005 assertion (3) part
// A: the constant the closure passes to grpc.WithKeepaliveParams
// matches the CP server-side config (Time=30s, Timeout=10s,
// PermitWithoutStream=false). grpc.DialOption is opaque so we can't
// read the params back out of a captured option — instead we assert
// on the exported constant + separately assert the closure applies
// exactly one dial option (part B).
func TestAdminClientKeepaliveParams(t *testing.T) {
	if adminClientKeepalive.Time != 30*time.Second {
		t.Errorf("adminClientKeepalive.Time = %s, want 30s", adminClientKeepalive.Time)
	}
	if adminClientKeepalive.Timeout != 10*time.Second {
		t.Errorf("adminClientKeepalive.Timeout = %s, want 10s", adminClientKeepalive.Timeout)
	}
	if adminClientKeepalive.PermitWithoutStream {
		t.Error("adminClientKeepalive.PermitWithoutStream = true, want false")
	}
}

// TestAdminClient_PassesKeepaliveToDial covers INV-B2-005 assertion (3)
// part B: the closure passes exactly one dial option to dialAdmin
// (the keepalive option). Combined with TestAdminClientKeepaliveParams
// this proves the closure applies the correct keepalive on every dial.
func TestAdminClient_PassesKeepaliveToDial(t *testing.T) {
	var capturedOpts []grpc.DialOption
	conn := newFakeConn(t)
	t.Cleanup(func() { _ = conn.Close() })

	withSeams(t,
		func(context.Context, *docker.Client, config.Config, *logger.Logger) error { return nil },
		func(_ context.Context, _, _ int, opts ...grpc.DialOption) (adminv1.AdminServiceClient, *grpc.ClientConn, error) {
			capturedOpts = opts
			return adminv1.NewAdminServiceClient(conn), conn, nil
		},
	)

	f := newTestFactory(t)
	if _, err := adminClientFunc(f)(context.Background()); err != nil {
		t.Fatalf("adminClient: %v", err)
	}

	if len(capturedOpts) != 1 {
		t.Errorf("dialAdmin received %d dial options, want 1 (keepalive only)", len(capturedOpts))
	}
}

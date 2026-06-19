package factory

import (
	"testing"
	"time"

	"google.golang.org/grpc/connectivity"
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

// TestCacheableState pins the package-level helper. Closure-behavior
// coverage (caching, rebuild on Shutdown, keepalive wiring) lives in
// the E2E harness, which runs the real manager.EnsureRunning +
// adminclient.Dial path — matches what the CLI ships with.
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

// TestAdminClientKeepaliveParams pins the keepalive constant.
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

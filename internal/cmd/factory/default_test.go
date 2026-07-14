package factory

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/connectivity"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
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

// TestRegisteredRootsFn pins the bundle-GC roots provider: every registered
// project root AND every worktree path must be listed — dropping the worktree
// loop would silently narrow the roots union, and a prune would collect cache
// entries only a worktree checkout's clawker.yaml declares.
func TestRegisteredRootsFn(t *testing.T) {
	pm := projectmocks.NewMockProjectManager()
	pm.ListFunc = func(context.Context) ([]project.ProjectEntry, error) {
		return []project.ProjectEntry{
			{Name: "alpha", Root: "/repos/alpha", Worktrees: map[string]project.WorktreeEntry{
				"feature": {Path: "/worktrees/alpha-feature", Branch: "feature"},
			}},
			{Name: "beta", Root: "/repos/beta", Worktrees: nil},
		}, nil
	}
	f := &cmdutil.Factory{} //nolint:exhaustruct // only ProjectManager is consulted
	f.ProjectManager = func() (project.ProjectManager, error) { return pm, nil }

	roots, err := registeredRootsFn(f)(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]bool{"/repos/alpha": true, "/worktrees/alpha-feature": true, "/repos/beta": true}
	if len(roots) != len(want) {
		t.Fatalf("expected %d roots, got %v", len(want), roots)
	}
	for _, r := range roots {
		if !want[r] {
			t.Errorf("unexpected root %q in %v", r, roots)
		}
	}
}

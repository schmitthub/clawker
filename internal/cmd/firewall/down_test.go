package firewall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/firewall"
	fwmocks "github.com/schmitthub/clawker/internal/firewall/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// downTestHarness wires up the pieces downRun touches: a Config that returns
// a tempdir PID file path, a FirewallManagerMock the test can program, and
// capture buffers for stdout/stderr so assertions can inspect user-facing
// output. Tests drive downRun directly rather than through Cobra so the
// assertions exercise the real branching logic.
type downTestHarness struct {
	pidFile string
	mock    *fwmocks.FirewallManagerMock
	opts    *DownOptions
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer
}

func newDownTestHarness(t *testing.T) *downTestHarness {
	t.Helper()
	ios, _, stdout, stderr := iostreams.Test()

	pidFile := filepath.Join(t.TempDir(), "firewall.pid")

	cfgMock := configmocks.NewBlankConfig()
	cfgMock.FirewallPIDFilePathFunc = func() (string, error) { return pidFile, nil }

	mock := &fwmocks.FirewallManagerMock{
		// Defaults: no orphans, clean stop.
		IsRunningFunc: func(_ context.Context) bool { return false },
		StopFunc:      func(_ context.Context) error { return nil },
	}

	h := &downTestHarness{
		pidFile: pidFile,
		mock:    mock,
		stdout:  stdout,
		stderr:  stderr,
	}
	h.opts = &DownOptions{
		IOStreams: ios,
		Config:    func() (config.Config, error) { return cfgMock, nil },
		Firewall: func(_ context.Context) (firewall.FirewallManager, error) {
			return mock, nil
		},
	}
	return h
}

// writePIDFile stamps the harness PID file with the given PID. Used to make
// IsDaemonRunning report true for the daemon-running path.
func (h *downTestHarness) writePIDFile(t *testing.T, pid int) {
	t.Helper()
	require.NoError(t, os.WriteFile(h.pidFile, []byte(strconv.Itoa(pid)), 0o644))
}

// TestDownRun_DaemonNotRunning_NoOrphans is the baseline case: no PID file
// exists, the firewall stack is not running, and Stop() has nothing to do.
// Down should print the "not running" info message, still call Stop() as the
// belt-and-suspenders cleanup, and NOT print either of the success lines.
func TestDownRun_DaemonNotRunning_NoOrphans(t *testing.T) {
	h := newDownTestHarness(t)

	err := downRun(context.Background(), h.opts)
	require.NoError(t, err)

	// Belt-and-suspenders Stop() must run even when no daemon was found.
	require.Len(t, h.mock.StopCalls(), 1, "Stop must always run as cleanup backstop")

	out := h.stdout.String()
	assert.Contains(t, out, "Firewall daemon is not running")
	assert.NotContains(t, out, "Firewall stopped",
		"must not claim the daemon was stopped when it was never running")
	assert.NotContains(t, out, "Removed leftover firewall containers",
		"must not claim cleanup when IsRunning reported no orphans")
	assert.Empty(t, h.stderr.String(), "no warnings expected on the happy no-op path")
}

// TestDownRun_DaemonNotRunning_CleansOrphans is the regression guard for the
// early-return bug. Prior to the fix, if the daemon was absent (crashed, stale
// PID) the command would return before running Stop(), leaving envoy/coredns/
// ebpf-manager containers to collide with the next `firewall up`. This asserts
// both that Stop() runs AND that the orphan-cleanup success line surfaces so
// the user knows something was actually cleaned up.
func TestDownRun_DaemonNotRunning_CleansOrphans(t *testing.T) {
	h := newDownTestHarness(t)
	// Program the mock so IsRunning reports orphans exist.
	h.mock.IsRunningFunc = func(_ context.Context) bool { return true }

	err := downRun(context.Background(), h.opts)
	require.NoError(t, err)

	require.Len(t, h.mock.IsRunningCalls(), 1, "orphan detection must probe IsRunning before Stop")
	require.Len(t, h.mock.StopCalls(), 1, "Stop must run to clean up the orphans")

	out := h.stdout.String()
	assert.Contains(t, out, "Firewall daemon is not running")
	assert.Contains(t, out, "Removed leftover firewall containers",
		"the orphan-cleanup success line is the user's only signal that containers were freed")
	assert.NotContains(t, out, "Firewall stopped",
		"the full daemon-shutdown message is reserved for the daemon-was-running path")
}

// TestDownRun_DaemonNotRunning_StopError_SurfacesWarning is the regression
// guard for the swallowed `_ = fwMgr.Stop(ctx)`. Prior to the fix, real Docker
// errors were eaten entirely and the user saw a misleading green checkmark.
// Now the error must reach stderr with the warning icon.
func TestDownRun_DaemonNotRunning_StopError_SurfacesWarning(t *testing.T) {
	h := newDownTestHarness(t)
	stopErr := errors.New("docker daemon unreachable")
	h.mock.IsRunningFunc = func(_ context.Context) bool { return true }
	h.mock.StopFunc = func(_ context.Context) error { return stopErr }

	err := downRun(context.Background(), h.opts)
	require.NoError(t, err, "best-effort cleanup must not fail the command")

	errOut := h.stderr.String()
	assert.Contains(t, errOut, "firewall cleanup")
	assert.Contains(t, errOut, "docker daemon unreachable",
		"the real error must reach stderr — silently hiding it was the original bug")

	out := h.stdout.String()
	assert.NotContains(t, out, "Removed leftover firewall containers",
		"success line must not fire when cleanup errored")
}

// TestDownRun_FirewallFactoryError_SurfacesWarning covers the (rare) path
// where opts.Firewall(ctx) itself errors before we can call any methods. The
// user still needs to see the failure on stderr.
func TestDownRun_FirewallFactoryError_SurfacesWarning(t *testing.T) {
	h := newDownTestHarness(t)
	factoryErr := errors.New("factory broken")
	h.opts.Firewall = func(_ context.Context) (firewall.FirewallManager, error) {
		return nil, factoryErr
	}

	err := downRun(context.Background(), h.opts)
	require.NoError(t, err)

	errOut := h.stderr.String()
	assert.Contains(t, errOut, "firewall cleanup")
	assert.Contains(t, errOut, "factory broken")
	assert.Len(t, h.mock.StopCalls(), 0, "Stop must not be called when the factory itself errored")
}

// TestDownRun_DaemonRunning_CleanShutdown spawns a real child process so
// IsDaemonRunning observes a live PID, then exercises the full daemon-running
// path: SIGTERM, wait for exit, belt-and-suspenders cleanup, and the final
// "Firewall stopped" success line. Uses a real subprocess because
// IsDaemonRunning/StopDaemon/WaitForDaemonExit are not injectable at this
// layer — the process PID is the source of truth.
func TestDownRun_DaemonRunning_CleanShutdown(t *testing.T) {
	h := newDownTestHarness(t)

	// Spawn a long-lived child we can SIGTERM.
	child := exec.Command("sleep", "30")
	require.NoError(t, child.Start())
	// Reap the exit status in the background so the process table is clean
	// even if StopDaemon somehow misses.
	var reaped atomic.Bool
	go func() {
		_, _ = child.Process.Wait()
		reaped.Store(true)
	}()
	t.Cleanup(func() {
		if !reaped.Load() {
			_ = child.Process.Kill()
			_, _ = child.Process.Wait()
		}
	})

	h.writePIDFile(t, child.Process.Pid)

	err := downRun(context.Background(), h.opts)
	require.NoError(t, err)

	out := h.stdout.String()
	assert.Contains(t, out, "Firewall stopped",
		"the clean shutdown success line must fire when the daemon was actually running")
	assert.NotContains(t, out, "Firewall daemon is not running")
	assert.NotContains(t, out, "Removed leftover firewall containers")

	// Belt-and-suspenders Stop() runs even on the happy path (the daemon's
	// own Stop should have cleaned up, but we always follow up).
	require.Len(t, h.mock.StopCalls(), 1)
}

// TestNewCmdDown_RunFReceivesOptions is the Cobra wiring smoke test — a
// defense against future refactors that accidentally break the runF injection
// seam (which every other test in this file relies on).
func TestNewCmdDown_RunFReceivesOptions(t *testing.T) {
	f := newTestFactory(t)

	called := false
	cmd := NewCmdDown(f, func(_ context.Context, opts *DownOptions) error {
		called = true
		require.NotNil(t, opts)
		assert.NotNil(t, opts.IOStreams)
		return nil
	})

	cmd.SetArgs(nil)
	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}

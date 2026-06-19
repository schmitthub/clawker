package subprocess

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/logger"
)

func TestSubprocessManager_StartAndShutdown(t *testing.T) {
	log := logger.Nop()
	mgr := NewSubprocessManager(log)

	cmd := exec.Command("sleep", "60")
	require.NoError(t, mgr.Start("test-sleep", cmd))

	// Process should be running.
	proc := mgr.findProcess("test-sleep")
	require.NotNil(t, proc)
	assert.NotNil(t, proc.Cmd.Process)

	// Shutdown should terminate it.
	mgr.Shutdown(2 * time.Second)

	select {
	case <-proc.done:
		// exited
	case <-time.After(5 * time.Second):
		t.Fatal("subprocess did not exit after shutdown")
	}
}

func TestSubprocessManager_CrashDetection(t *testing.T) {
	log := logger.Nop()
	mgr := NewSubprocessManager(log)

	// Start a process that exits immediately with error.
	cmd := exec.Command("false")
	require.NoError(t, mgr.Start("crasher", cmd))

	select {
	case err := <-mgr.CrashChan():
		assert.Contains(t, err.Error(), "crasher")
	case <-time.After(5 * time.Second):
		t.Fatal("crash not detected")
	}
}

func TestSubprocessManager_WaitHealthy(t *testing.T) {
	log := logger.Nop()
	mgr := NewSubprocessManager(log)

	// Start a long-lived process.
	cmd := exec.Command("sleep", "60")
	require.NoError(t, mgr.Start("healthy-proc", cmd))
	defer mgr.Shutdown(2 * time.Second)

	// Start an HTTP server that returns 200.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := context.Background()
	err := mgr.WaitHealthy(ctx, "healthy-proc", HealthCheck{
		URL:      srv.URL + "/health",
		Interval: 50 * time.Millisecond,
		Timeout:  5 * time.Second,
	})
	assert.NoError(t, err)
}

func TestSubprocessManager_WaitHealthy_Timeout(t *testing.T) {
	log := logger.Nop()
	mgr := NewSubprocessManager(log)

	cmd := exec.Command("sleep", "60")
	require.NoError(t, mgr.Start("slow-proc", cmd))
	defer mgr.Shutdown(2 * time.Second)

	// Server always returns 503.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx := context.Background()
	err := mgr.WaitHealthy(ctx, "slow-proc", HealthCheck{
		URL:      srv.URL + "/health",
		Interval: 50 * time.Millisecond,
		Timeout:  200 * time.Millisecond,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not become healthy")
}

func TestSubprocessManager_ShutdownSuppressesCrashReport(t *testing.T) {
	log := logger.Nop()
	mgr := NewSubprocessManager(log)

	// Start a long-lived process.
	cmd := exec.Command("sleep", "60")
	require.NoError(t, mgr.Start("graceful", cmd))

	// Mark as shutting down BEFORE killing the process.
	mgr.BeginShutdown()

	// Kill the process — this simulates the signal-triggered shutdown path
	// where subprocesses exit after BeginShutdown but before Shutdown.
	proc := mgr.findProcess("graceful")
	require.NotNil(t, proc)
	require.NoError(t, proc.Cmd.Process.Kill())

	// Wait for the monitor goroutine to observe the exit.
	select {
	case <-proc.done:
	case <-time.After(5 * time.Second):
		t.Fatal("subprocess did not exit after kill")
	}

	// CrashChan must NOT receive the exit — it was expected.
	select {
	case err := <-mgr.CrashChan():
		t.Fatalf("expected no crash report during shutdown, got: %v", err)
	case <-time.After(100 * time.Millisecond):
		// Good — no crash reported.
	}
}

func TestSubprocessManager_CrashReportedBeforeShutdown(t *testing.T) {
	log := logger.Nop()
	mgr := NewSubprocessManager(log)

	// Start a process that exits immediately with error (no shutdown flag set).
	cmd := exec.Command("false")
	require.NoError(t, mgr.Start("crasher-pre-shutdown", cmd))

	// CrashChan MUST receive the exit — shutdown has not been initiated.
	select {
	case err := <-mgr.CrashChan():
		assert.Contains(t, err.Error(), "crasher-pre-shutdown")
		assert.Contains(t, err.Error(), "unexpectedly")
	case <-time.After(5 * time.Second):
		t.Fatal("crash not detected before shutdown")
	}
}

func TestSubprocessManager_ReverseShutdownOrder(t *testing.T) {
	log := logger.Nop()
	mgr := NewSubprocessManager(log)

	// Start two processes.
	cmd1 := exec.Command("sleep", "60")
	require.NoError(t, mgr.Start("first", cmd1))

	cmd2 := exec.Command("sleep", "60")
	require.NoError(t, mgr.Start("second", cmd2))

	proc1 := mgr.findProcess("first")
	proc2 := mgr.findProcess("second")

	// Shutdown should stop second before first (reverse order).
	mgr.Shutdown(2 * time.Second)

	// Both should have exited.
	select {
	case <-proc1.done:
	case <-time.After(5 * time.Second):
		t.Fatal("first did not exit")
	}
	select {
	case <-proc2.done:
	case <-time.After(5 * time.Second):
		t.Fatal("second did not exit")
	}
}

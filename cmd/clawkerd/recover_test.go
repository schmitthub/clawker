package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/schmitthub/clawker/internal/logger"
)

// withCapturedStderr swaps os.Stderr for a pipe, runs fn, and returns
// what fn wrote to stderr. Used to assert the recoverGoroutine
// stderr-mirror without polluting test runner output.
func withCapturedStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return string(<-done)
}

// TestRecoverGoroutine_PanicFiresOnPanicAndLogs pins the resilience
// contract: every long-lived clawkerd goroutine wraps with
// recoverGoroutine so a panic does not kill PID 1. The onPanic hook
// MUST fire (so reaper/sender peers can release waiters), a
// structured log line MUST be emitted, AND a stderr mirror MUST be
// emitted (survives lumberjack rotation failure — `docker logs`
// fallback for the only triage surface a degraded PID 1 has).
func TestRecoverGoroutine_PanicFiresOnPanicAndLogs(t *testing.T) {
	var buf bytes.Buffer
	var fired atomic.Bool

	stderr := withCapturedStderr(t, func() {
		defer recoverGoroutine(logger.NewWriter(&buf), "test_goroutine", func() {
			fired.Store(true)
		})
		panic("boom")
	})

	if !fired.Load() {
		t.Error("onPanic did not fire — peers selecting on Done/MainExited would deadlock")
	}
	out := buf.String()
	if !strings.Contains(out, `"event":"goroutine_panic"`) {
		t.Errorf("structured event missing; got: %s", out)
	}
	if !strings.Contains(out, `"goroutine":"test_goroutine"`) {
		t.Errorf("goroutine name missing from log; got: %s", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("panic value missing from log; got: %s", out)
	}
	if !strings.Contains(stderr, "goroutine_panic") || !strings.Contains(stderr, "test_goroutine") || !strings.Contains(stderr, "boom") {
		t.Errorf("stderr mirror missing required fields; got: %s", stderr)
	}
}

// TestRecoverGoroutine_NilOnPanicSafe pins that the nil-onPanic case
// (used by goroutines whose panic-recovery has no side effect to
// trigger) does not deadlock or panic the recovery itself.
func TestRecoverGoroutine_NilOnPanicSafe(t *testing.T) {
	var buf bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recoverGoroutine itself panicked with nil onPanic: %v", r)
		}
	}()
	stderr := withCapturedStderr(t, func() {
		defer recoverGoroutine(logger.NewWriter(&buf), "nil_hook", nil)
		panic("boom")
	})
	if !strings.Contains(buf.String(), "goroutine_panic") {
		t.Errorf("log missing; got: %s", buf.String())
	}
	if !strings.Contains(stderr, "goroutine_panic") {
		t.Errorf("stderr mirror missing; got: %s", stderr)
	}
}

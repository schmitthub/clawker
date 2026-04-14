package controlplane

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

// TestAgentWatcher_DrainCallback covers the drain-to-zero path's two
// callback outcomes together so the watcher's "drain fires after grace
// period" and "callback error propagates" contracts are exercised by
// the same invocation pattern.
func TestAgentWatcher_DrainCallback(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("stack stop failed")
	tests := []struct {
		name        string
		callbackErr error
		wantRunErr  error
		wantWrap    bool
	}{
		{name: "nil propagates nil", callbackErr: nil, wantRunErr: nil},
		{name: "error propagates wrapped", callbackErr: errBoom, wantRunErr: errBoom, wantWrap: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var drainCalled atomic.Int32
			listAgents := func(_ context.Context) (int, error) { return 0, nil }
			onDrain := func(_ context.Context) error {
				drainCalled.Add(1)
				return tc.callbackErr
			}

			w := NewAgentWatcher(logger.Nop(), listAgents, onDrain, AgentWatcherOptions{
				PollInterval:    10 * time.Millisecond,
				MissedThreshold: 2,
				GracePeriod:     50 * time.Millisecond,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			err := w.Run(ctx)
			switch {
			case tc.wantRunErr == nil:
				if err != nil {
					t.Fatalf("Run: got %v, want nil", err)
				}
			case tc.wantWrap:
				if !errors.Is(err, tc.wantRunErr) {
					t.Fatalf("Run: got %v, want %v wrapped", err, tc.wantRunErr)
				}
			}
			if drainCalled.Load() != 1 {
				t.Fatalf("drain callback: got %d invocations, want 1", drainCalled.Load())
			}
		})
	}
}

// TestAgentWatcher_DoesNotDrainBeforeGrace locks in the grace-period
// invariant: even if the agent count is zero from the very first poll,
// drain must not fire until wall-clock time exceeds the grace window.
func TestAgentWatcher_DoesNotDrainBeforeGrace(t *testing.T) {
	t.Parallel()

	var drainCalled atomic.Int32
	listAgents := func(_ context.Context) (int, error) { return 0, nil }
	onDrain := func(_ context.Context) error {
		drainCalled.Add(1)
		return nil
	}

	w := NewAgentWatcher(logger.Nop(), listAgents, onDrain, AgentWatcherOptions{
		PollInterval:    5 * time.Millisecond,
		MissedThreshold: 2,
		GracePeriod:     200 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := w.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run: got %v, want context.DeadlineExceeded (grace still in effect)", err)
	}
	if drainCalled.Load() != 0 {
		t.Fatalf("drain callback: got %d invocations, want 0 (within grace period)", drainCalled.Load())
	}
}

// TestAgentWatcher_NonZeroCountResetsMissStreak verifies that a
// non-zero poll result clears the missed counter. If an agent is still
// running, drain must not fire even after grace expires.
func TestAgentWatcher_NonZeroCountResetsMissStreak(t *testing.T) {
	t.Parallel()

	var drainCalled atomic.Int32
	var pollCount atomic.Int32
	listAgents := func(_ context.Context) (int, error) {
		pollCount.Add(1)
		return 1, nil
	}
	onDrain := func(_ context.Context) error {
		drainCalled.Add(1)
		return nil
	}

	w := NewAgentWatcher(logger.Nop(), listAgents, onDrain, AgentWatcherOptions{
		PollInterval:    5 * time.Millisecond,
		MissedThreshold: 2,
		GracePeriod:     20 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := w.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run: got %v, want context.DeadlineExceeded", err)
	}
	if drainCalled.Load() != 0 {
		t.Fatalf("drain callback: got %d invocations, want 0 (agent still present)", drainCalled.Load())
	}
	if pollCount.Load() < 3 {
		t.Fatalf("pollCount: got %d, want ≥ 3 (watcher kept polling)", pollCount.Load())
	}
}

// TestAgentWatcher_ListAgentsErrorResetsStreak verifies that a transient
// Docker error does not count toward the miss streak. A real Docker
// restart mid-session must not cause the CP to self-terminate.
func TestAgentWatcher_ListAgentsErrorResetsStreak(t *testing.T) {
	t.Parallel()

	var drainCalled atomic.Int32
	errBoom := errors.New("docker unreachable")
	var calls atomic.Int32
	listAgents := func(_ context.Context) (int, error) {
		n := calls.Add(1)
		if n == 1 {
			return 0, nil
		}
		return 0, errBoom
	}
	onDrain := func(_ context.Context) error {
		drainCalled.Add(1)
		return nil
	}

	w := NewAgentWatcher(logger.Nop(), listAgents, onDrain, AgentWatcherOptions{
		PollInterval:    5 * time.Millisecond,
		MissedThreshold: 2,
		GracePeriod:     10 * time.Millisecond,
		ListErrCeiling:  100, // far above the burst in this test
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	err := w.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run: got %v, want context.DeadlineExceeded", err)
	}
	if drainCalled.Load() != 0 {
		t.Fatalf("drain callback: got %d invocations, want 0 (errors must reset the streak)", drainCalled.Load())
	}
}

// TestAgentWatcher_ListErrCeiling_SurfacesError locks in the safety
// valve: if Docker is wedged long enough to exceed the ceiling, the
// watcher must return an error rather than stay blind indefinitely.
func TestAgentWatcher_ListErrCeiling_SurfacesError(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("docker wedged")
	listAgents := func(_ context.Context) (int, error) { return 0, errBoom }
	onDrain := func(_ context.Context) error {
		t.Fatal("drain must not fire when listAgents is failing")
		return nil
	}

	w := NewAgentWatcher(logger.Nop(), listAgents, onDrain, AgentWatcherOptions{
		PollInterval:    2 * time.Millisecond,
		MissedThreshold: 2,
		GracePeriod:     5 * time.Millisecond,
		ListErrCeiling:  3,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := w.Run(ctx)
	if !errors.Is(err, errBoom) {
		t.Fatalf("Run: got %v, want errBoom wrapped", err)
	}
	if !strings.Contains(err.Error(), "consecutive list failures") {
		t.Fatalf("Run error should mention ceiling, got %q", err.Error())
	}
}

// TestAgentWatcher_ContextCancellationReturnsError verifies that ctx
// cancellation takes precedence over drain and propagates the ctx error
// so shutdown flows can distinguish reasons.
func TestAgentWatcher_ContextCancellationReturnsError(t *testing.T) {
	t.Parallel()

	var drainCalled atomic.Int32
	listAgents := func(_ context.Context) (int, error) { return 1, nil }
	onDrain := func(_ context.Context) error {
		drainCalled.Add(1)
		return nil
	}

	w := NewAgentWatcher(logger.Nop(), listAgents, onDrain, AgentWatcherOptions{
		PollInterval:    5 * time.Millisecond,
		MissedThreshold: 2,
		GracePeriod:     10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	var runErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		runErr = w.Run(ctx)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	wg.Wait()

	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("Run: got %v, want context.Canceled", runErr)
	}
	if drainCalled.Load() != 0 {
		t.Fatal("drain callback should not fire on ctx cancel")
	}
}

// TestAgentWatcher_RunTwice_ReturnsError guarantees the at-most-once
// Run contract structurally — a second Run call must not spin up a
// competing poll loop.
func TestAgentWatcher_RunTwice_ReturnsError(t *testing.T) {
	t.Parallel()

	listAgents := func(_ context.Context) (int, error) { return 1, nil }
	onDrain := func(_ context.Context) error { return nil }

	w := NewAgentWatcher(logger.Nop(), listAgents, onDrain, AgentWatcherOptions{
		PollInterval: 5 * time.Millisecond,
		GracePeriod:  10 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	// Second call, fresh ctx — should refuse outright.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	err := w.Run(ctx2)
	if err == nil || !strings.Contains(err.Error(), "already called") {
		t.Fatalf("second Run: got %v, want already-called error", err)
	}
}

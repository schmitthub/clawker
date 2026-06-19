package ebpf

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/logger"
)

// TestDNSGCEscalator pins the "escalate once per crossing, reset on success"
// contract of the dns_cache GC degraded detector. The bug this guards against:
// a sweep that fails to reclaim must count toward the streak, and the
// dns_gc_degraded line must fire exactly once when the streak first reaches the
// threshold — not every tick after, and not at all if a success intervenes.
func TestDNSGCEscalator(t *testing.T) {
	t.Parallel()

	t.Run("fires once at threshold, then stays quiet", func(t *testing.T) {
		e := dnsGCEscalator{threshold: 3}
		// Two failures: below threshold, no escalation.
		assert.False(t, e.record(false))
		assert.False(t, e.record(false))
		// Third consecutive failure crosses the threshold exactly once.
		assert.True(t, e.record(false))
		assert.Equal(t, 3, e.failures)
		// Further failures in the same streak must NOT re-fire.
		assert.False(t, e.record(false))
		assert.False(t, e.record(false))
	})

	t.Run("a success resets the streak", func(t *testing.T) {
		e := dnsGCEscalator{threshold: 3}
		assert.False(t, e.record(false))
		assert.False(t, e.record(false))
		// Success before the threshold clears the streak.
		assert.False(t, e.record(true))
		assert.Equal(t, 0, e.failures)
		// The next failures start counting from zero again.
		assert.False(t, e.record(false))
		assert.False(t, e.record(false))
		assert.True(t, e.record(false))
	})

	t.Run("re-arms after a post-degraded success", func(t *testing.T) {
		e := dnsGCEscalator{threshold: 2}
		assert.False(t, e.record(false))
		assert.True(t, e.record(false)) // first crossing
		assert.False(t, e.record(true)) // recover
		assert.False(t, e.record(false))
		assert.True(t, e.record(false)) // second crossing fires again
	})
}

// TestDNSGCSweep pins the per-sweep CP-resilience contract that the GC
// goroutine depends on: a panicking sweep must be recovered and counted as a
// failure (so the escalator can trip) rather than tearing down the loop and
// stranding the dns_cache map unsupervised, and a sweep where GarbageCollectDNS
// reports it could not reclaim must also count as a failure. A clean sweep is a
// success regardless of how many entries it cleared. The escalator test covers
// the boolean folding; this covers how each sweep outcome becomes that boolean.
func TestDNSGCSweep(t *testing.T) {
	t.Parallel()

	t.Run("clean sweep that reclaimed entries is success", func(t *testing.T) {
		t.Parallel()
		ok := dnsGCSweep(func() (int, error) { return 3, nil }, logger.Nop())
		require.True(t, ok)
	})

	t.Run("clean sweep that reclaimed nothing is still success", func(t *testing.T) {
		t.Parallel()
		// "swept nothing because nothing had expired" must not count as failure,
		// or a healthy idle CP would escalate to dns_gc_degraded.
		ok := dnsGCSweep(func() (int, error) { return 0, nil }, logger.Nop())
		require.True(t, ok)
	})

	t.Run("GarbageCollectDNS error is a failed sweep", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		ok := dnsGCSweep(func() (int, error) { return 0, errors.New("wedged") }, log)
		require.False(t, ok, "an unreclaimable sweep must count toward escalation")
		require.Contains(t, buf.String(), "dns_gc_error",
			"a failed reclaim must emit the structured event for triage")
	})

	t.Run("panic is recovered, counted as failure, and logged", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		log := logger.NewWriter(&buf)
		// A panicking sweep must NOT propagate (which would kill the goroutine
		// and strand the dns_cache map) — the whole point of the recover.
		var ok bool
		require.NotPanics(t, func() {
			ok = dnsGCSweep(func() (int, error) { panic("boom") }, log)
		})
		require.False(t, ok, "a panicking sweep must count toward escalation")
		require.Contains(t, buf.String(), "dns_gc_panic",
			"a recovered panic must emit the structured event for triage")
	})
}

// TestDNSGCOptsDefaults pins that a zero-value DNSGCOpts falls back to the
// package defaults rather than a 0s ticker (which time.NewTicker panics on) or
// a threshold of 0 (which would fire dns_gc_degraded on the very first failed
// sweep). Construction must be safe with DNSGCOpts{} so the orchestrator can
// take production behavior without naming the consts.
func TestDNSGCOptsDefaults(t *testing.T) {
	t.Parallel()

	d := NewDNSGarbageCollector(&Manager{}, logger.Nop(), DNSGCOpts{})
	assert.Equal(t, dnsGCInterval, d.interval)
	assert.Equal(t, dnsGCDegradedThreshold, d.threshold)

	d2 := NewDNSGarbageCollector(&Manager{}, logger.Nop(), DNSGCOpts{Interval: time.Millisecond, DegradedThreshold: 2})
	assert.Equal(t, time.Millisecond, d2.interval)
	assert.Equal(t, 2, d2.threshold)
}

// TestDNSGarbageCollectorStartStop drives the real ticker loop and proves the
// load-bearing lifecycle contract: the loop sweeps on the interval, the
// returned stop cancels the loop AND joins any in-flight sweep before returning
// (so the BPF map fd can be torn down without a sweep racing it), and stop is
// sync.Once-guarded so the drain callback and the deferred path can both call
// it without a double-close.
func TestDNSGarbageCollectorStartStop(t *testing.T) {
	t.Parallel()

	t.Run("sweeps on the interval until stopped", func(t *testing.T) {
		t.Parallel()
		var sweeps atomic.Int64
		d := &DNSGarbageCollector{
			gc:        func() (int, error) { sweeps.Add(1); return 0, nil },
			log:       logger.Nop(),
			interval:  time.Millisecond,
			threshold: dnsGCDegradedThreshold,
		}
		stop := d.Start(context.Background())
		require.Eventually(t, func() bool { return sweeps.Load() >= 3 },
			2*time.Second, time.Millisecond, "the loop must sweep on its interval")
		stop()
		after := sweeps.Load()
		// No further sweeps once stopped.
		time.Sleep(20 * time.Millisecond)
		assert.Equal(t, after, sweeps.Load(), "no sweep may run after stop returns")
	})

	t.Run("stop joins an in-flight sweep before returning", func(t *testing.T) {
		t.Parallel()
		release := make(chan struct{})
		entered := make(chan struct{})
		var done atomic.Bool
		var enteredOnce atomic.Bool
		d := &DNSGarbageCollector{
			gc: func() (int, error) {
				if enteredOnce.CompareAndSwap(false, true) {
					close(entered)
				}
				<-release
				done.Store(true)
				return 0, nil
			},
			log:       logger.Nop(),
			interval:  time.Millisecond,
			threshold: dnsGCDegradedThreshold,
		}
		stop := d.Start(context.Background())
		<-entered // a sweep is now blocked inside gc

		stopReturned := make(chan struct{})
		go func() {
			stop()
			close(stopReturned)
		}()
		// stop must not return while the sweep is still in flight.
		select {
		case <-stopReturned:
			t.Fatal("stop returned before the in-flight sweep finished")
		case <-time.After(50 * time.Millisecond):
		}
		close(release) // let the sweep finish
		select {
		case <-stopReturned:
		case <-time.After(2 * time.Second):
			t.Fatal("stop did not return after the in-flight sweep finished")
		}
		assert.True(t, done.Load(), "stop must wait for the in-flight sweep to complete")
	})

	t.Run("ctx cancellation stops the loop", func(t *testing.T) {
		t.Parallel()
		var sweeps atomic.Int64
		d := &DNSGarbageCollector{
			gc:        func() (int, error) { sweeps.Add(1); return 0, nil },
			log:       logger.Nop(),
			interval:  time.Millisecond,
			threshold: dnsGCDegradedThreshold,
		}
		ctx, cancel := context.WithCancel(context.Background())
		stop := d.Start(ctx)
		require.Eventually(t, func() bool { return sweeps.Load() >= 1 },
			2*time.Second, time.Millisecond)
		cancel()
		require.Eventually(t, func() bool {
			before := sweeps.Load()
			time.Sleep(10 * time.Millisecond)
			return before == sweeps.Load()
		}, 2*time.Second, 10*time.Millisecond, "loop must quiesce after ctx cancel")
		// stop is still safe to call (idempotent) after ctx-driven shutdown.
		require.NotPanics(t, func() { stop() })
	})

	t.Run("stop is idempotent (sync.Once)", func(t *testing.T) {
		t.Parallel()
		d := &DNSGarbageCollector{
			gc:        func() (int, error) { return 0, nil },
			log:       logger.Nop(),
			interval:  time.Millisecond,
			threshold: dnsGCDegradedThreshold,
		}
		stop := d.Start(context.Background())
		require.NotPanics(t, func() {
			stop()
			stop()
			stop()
		}, "stop must be callable from both the drain body and a defer")
	})
}

package agentregistry

import (
	"context"
	"time"

	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/informer"
	"github.com/schmitthub/clawker/internal/logger"
)

// Subscribe panic-loop guardrails. A deterministic panic source (bad
// delta shape, latent bug) without backoff would spin recover →
// drainOnce → recover at line speed, filling the rotated log files
// (50MB × 3) in seconds and burying the actual root-cause line. Sleep
// between recoveries with exponential backoff capped at the ceiling,
// and after enough panics in a sliding window escalate to Error and
// terminate the consumer so the wrapping process surfaces the
// runaway loudly instead of silently spinning.
const (
	subscribePanicBackoffMin    = 100 * time.Millisecond
	subscribePanicBackoffMax    = 30 * time.Second
	subscribePanicWindow        = 5 * time.Minute
	subscribePanicWindowMaxHits = 100
)

// Subscribe wires the registry to container deltas published by the
// dockerevents feeder via the informer. The returned cleanup must be
// deferred by the caller; it cancels the informer subscription and
// waits for the consumer goroutine to drain.
//
// Eviction trigger:
//   - DeltaRemoved (Docker destroy/remove, i.e. `docker rm`) — the
//     container is gone for good and the row is orphaned.
//
// Stop/die/kill do NOT evict: the container still exists and may be
// `docker start`-ed back into life. The registry row survives so a
// subsequent restart finds its existing identity.
//
// Pause/unpause likewise are not eviction triggers: the agent process
// is alive, just frozen, and the existing mTLS connection remains valid.
//
// log is required (pass logger.Nop() in tests that don't care about the
// audit trail). A nil logger is replaced with logger.Nop() so callers
// can't accidentally trip a nil deref inside the panic recovery path.
func Subscribe(ctx context.Context, reg Registry, inf informer.Interface, log *logger.Logger) func() {
	if log == nil {
		log = logger.Nop()
	}
	_, ch, cancel := inf.Subscribe(informer.Filter{
		Kinds: []string{dockerevents.KindContainer},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Recover-and-resume so a panic in EvictByContainerID (or
		// any future per-delta hook) doesn't silently kill the
		// consumer. A dead consumer would leave registered agents
		// alive in the registry after their containers exit, and
		// stale Thumbprint entries would keep authorizing per-agent
		// RPCs against a container that is gone. Mirrors the recover
		// pattern in cmd/clawker-cp/main.go's informer stats
		// heartbeat goroutine, but loops back into the consumer so
		// subsequent deltas are still processed — the dropped delta
		// that triggered the panic is lost, but the next one is not.
		//
		// Backoff + circuit-breaker on consecutive panics so a
		// deterministic panic source can't hot-loop the recovery
		// path and bury real signal in the log.
		var panicTimes []time.Time
		var lastPanic time.Time
		backoff := subscribePanicBackoffMin
		for {
			if drainOnce(ctx, ch, reg, log) {
				return
			}
			now := time.Now()
			// Reset backoff if the previous panic was a long time ago —
			// rapid consecutive failures grow backoff; isolated ones don't.
			if !lastPanic.IsZero() && now.Sub(lastPanic) > 30*time.Second {
				backoff = subscribePanicBackoffMin
			}
			lastPanic = now
			panicTimes = append(panicTimes, now)
			// Slide the window so the count reflects only recent panics.
			cutoff := now.Add(-subscribePanicWindow)
			i := 0
			for i < len(panicTimes) && panicTimes[i].Before(cutoff) {
				i++
			}
			panicTimes = panicTimes[i:]
			if len(panicTimes) >= subscribePanicWindowMaxHits {
				log.Error().
					Int("panic_count", len(panicTimes)).
					Dur("window", subscribePanicWindow).
					Msg("agentregistry subscribe consumer: panic rate exceeded ceiling; terminating consumer")
				return
			}
			// Sleep with ctx-cancel awareness so a drain doesn't have
			// to wait out the backoff.
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < subscribePanicBackoffMax {
				backoff *= 2
				if backoff > subscribePanicBackoffMax {
					backoff = subscribePanicBackoffMax
				}
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

func handleDelta(d informer.Delta, reg Registry) {
	if d.Kind != informer.DeltaRemoved {
		return
	}
	// DeltaRemoved soft-deletes: After carries the resource with
	// Lifecycle=LifecycleGone. Before is set to the prior state if
	// the resource was previously visible. Either side gives us the
	// container ID we need.
	switch {
	case d.After != nil:
		reg.EvictByContainerID(d.After.ID)
	case d.Before != nil:
		reg.EvictByContainerID(d.Before.ID)
	}
}

// drainOnce runs the delta consumer until ctx is done, the channel is
// closed, or a panic in handleDelta unwinds. Returns true when the
// consumer is finished for good (ctx canceled or channel closed) and
// false when it should be restarted (panic recovered). Split out from
// Subscribe so the deferred recover has its own stack frame — defining
// it inline would require an immediately-invoked closure for the same
// effect and read worse.
func drainOnce(ctx context.Context, ch <-chan informer.Delta, reg Registry, log *logger.Logger) (terminate bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("agentregistry subscribe consumer panicked; resuming")
			terminate = false
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return true
		case d, ok := <-ch:
			if !ok {
				return true
			}
			handleDelta(d, reg)
		}
	}
}

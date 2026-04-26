package agentslots

import (
	"context"
	"time"

	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/informer"
	"github.com/schmitthub/clawker/internal/logger"
)

// Subscribe panic-loop guardrails — see agentregistry/subscribe.go for
// the same constants and rationale; kept in lock-step so both consumers
// behave identically under a deterministic panic source.
const (
	subscribePanicBackoffMin    = 100 * time.Millisecond
	subscribePanicBackoffMax    = 30 * time.Second
	subscribePanicWindow        = 5 * time.Minute
	subscribePanicWindowMaxHits = 100
)

// Subscribe wires the slot registry to container deltas published by the
// dockerevents feeder via the informer. The returned cleanup must be
// deferred by the caller; it cancels the informer subscription and
// waits for the consumer goroutine to drain.
//
// Eviction triggers mirror agentregistry's:
//   - DeltaRemoved (Docker destroy/remove) — container is gone, the
//     pending slot can never be consumed.
//   - DeltaUpdated where Lifecycle becomes "stopped" (Docker die / stop /
//     kill) — clawkerd will never start, so the slot is dead too.
//
// Pause/unpause are not eviction triggers: the agent process is alive,
// just frozen, and may eventually call AgentService.Connect.
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
		// Recover-and-resume so a panic in EvictByContainerID (or any
		// future per-delta hook) doesn't silently kill the consumer.
		// The TTL janitor would still sweep dead-container slots after
		// AgentSlotTTL elapses, so the correctness floor is bounded —
		// but a stuck consumer also defeats the integration point that
		// lets dockerevents drive both registries identically, leaving
		// pending slots lingering until expiry instead of being evicted
		// promptly on container exit. The loop reruns drainOnce so the
		// dropped delta is lost but subsequent deltas still drain.
		//
		// Backoff + circuit-breaker on consecutive panics so a
		// deterministic panic source can't hot-loop the recovery path
		// and bury real signal in the log (kept in sync with
		// agentregistry/subscribe.go).
		var panicTimes []time.Time
		var lastPanic time.Time
		backoff := subscribePanicBackoffMin
		for {
			if drainOnce(ctx, ch, reg, log) {
				return
			}
			now := time.Now()
			if !lastPanic.IsZero() && now.Sub(lastPanic) > 30*time.Second {
				backoff = subscribePanicBackoffMin
			}
			lastPanic = now
			panicTimes = append(panicTimes, now)
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
					Msg("agentslots subscribe consumer: panic rate exceeded ceiling; terminating consumer")
				return
			}
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
	switch d.Kind {
	case informer.DeltaRemoved:
		// DeltaRemoved soft-deletes: After carries the resource with
		// Lifecycle=LifecycleGone. Before is set to the prior state if
		// the resource was previously visible. Either side gives us
		// the container ID we need.
		switch {
		case d.After != nil:
			reg.EvictByContainerID(d.After.ID)
		case d.Before != nil:
			reg.EvictByContainerID(d.Before.ID)
		}
	case informer.DeltaUpdated:
		if d.After != nil && d.After.Lifecycle == dockerevents.LifecycleStopped {
			reg.EvictByContainerID(d.After.ID)
		}
	}
}

// drainOnce runs the delta consumer until ctx is done, the channel is
// closed, or a panic in handleDelta unwinds. Returns true when the
// consumer is finished for good (ctx canceled or channel closed) and
// false when it should be restarted (panic recovered). Split out from
// Subscribe so the deferred recover has its own stack frame.
func drainOnce(ctx context.Context, ch <-chan informer.Delta, reg Registry, log *logger.Logger) (terminate bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("agentslots subscribe consumer panicked; resuming")
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

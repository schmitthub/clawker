package agentregistry

import (
	"context"
	"time"

	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// Subscribe panic-loop guardrails. A deterministic panic source (bad
// event handler, latent bug) without backoff would spin recover →
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

// Subscribe wires the registry to dockerevents.ContainerRemoved
// events published on the Overseer bus. The returned cleanup must be
// deferred by the caller; it cancels the bus subscription and waits
// for the consumer goroutine to drain.
//
// Eviction trigger:
//   - dockerevents.ContainerRemoved (Docker destroy/remove, i.e.
//     `docker rm`) — the container is gone for good and the row is
//     orphaned.
//
// Stop/die/kill do NOT evict: the container still exists and may be
// `docker start`-ed back into life. The registry row survives so a
// subsequent restart finds its existing identity.
//
// log is required (pass logger.Nop() in tests that don't care about
// the audit trail). A nil logger is replaced with logger.Nop() so
// callers can't accidentally trip a nil deref inside the panic
// recovery path.
func Subscribe(ctx context.Context, reg Registry, bus *overseer.Overseer, log *logger.Logger) func() {
	if log == nil {
		log = logger.Nop()
	}
	sub, ok := overseer.Subscribe[dockerevents.ContainerRemoved](bus, "agentregistry")
	if !ok {
		log.Warn().Msg("agentregistry: bus closed before subscribe; consumer not started")
		return func() {}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Recover-and-resume so a panic in EvictByContainerID (or any
		// future per-event hook) doesn't silently kill the consumer.
		// A dead consumer would leave registered agents alive in the
		// registry after their containers exit, and stale Thumbprint
		// entries would keep authorizing per-agent RPCs against a
		// container that is gone.
		//
		// Backoff + circuit-breaker on consecutive panics so a
		// deterministic panic source can't hot-loop the recovery
		// path and bury real signal in the log.
		var panicTimes []time.Time
		var lastPanic time.Time
		backoff := subscribePanicBackoffMin
		for {
			if drainOnce(ctx, sub.C, reg, log) {
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
					Msg("agentregistry subscribe consumer: panic rate exceeded ceiling; terminating consumer")
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
		sub.Unsubscribe()
		<-done
	}
}

func handleEvent(ev dockerevents.ContainerRemoved, reg Registry, log *logger.Logger) {
	if ev.ID == "" {
		return
	}
	// Eviction is best-effort here: we cannot retry from a bus
	// consumer (the next event is already queued), so log the error
	// and proceed. The startup Reap heals stale rows that survived a
	// transient sqlite failure.
	if err := reg.EvictByContainerID(ev.ID); err != nil {
		log.Warn().
			Err(err).
			Str("container_id", ev.ID).
			Msg("agentregistry: evict-on-delete failed; row may persist until next reap")
	}
}

// drainOnce runs the typed-event consumer until ctx is done, the
// channel is closed, or a panic in handleEvent unwinds. Returns true
// when the consumer is finished for good (ctx canceled or channel
// closed) and false when it should be restarted (panic recovered).
// Split out from Subscribe so the deferred recover has its own stack
// frame.
func drainOnce(ctx context.Context, ch <-chan dockerevents.ContainerRemoved, reg Registry, log *logger.Logger) (terminate bool) {
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
		case ev, ok := <-ch:
			if !ok {
				return true
			}
			handleEvent(ev, reg, log)
		}
	}
}

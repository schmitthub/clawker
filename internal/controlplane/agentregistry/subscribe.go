package agentregistry

import (
	"context"
	"time"

	"github.com/moby/moby/api/types/events"

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
//
// subscribePanicWindowMaxHits is const because it sizes the
// fixed-capacity panic-time ring buffer (Go array length must be a
// compile-time constant). The other three are var-typed so panic-storm
// tests can shrink them without flooding real time; production callers
// must not mutate them.
const subscribePanicWindowMaxHits = 100

var (
	subscribePanicBackoffMin = 100 * time.Millisecond
	subscribePanicBackoffMax = 30 * time.Second
	subscribePanicWindow     = 5 * time.Minute
)

// subscribeEvictEscalationThreshold is the number of consecutive
// EvictByContainerID failures the consumer will log at Warn before
// escalating to a single Error. Sustained sqlite write failures (disk
// full, schema corruption, FS read-only after a host event) would
// otherwise leak orphan rows indefinitely on a long-running CP — the
// next reap only fires at CP restart, which may never come on a
// stable host. Escalation surfaces the situation to operators while
// the consumer keeps running so a transient blip recovers cleanly.
// Reset on the first successful evict.
var subscribeEvictEscalationThreshold = 5

// Subscribe wires the registry to dockerevents.DockerEvent envelopes
// matching `container/destroy` published on the Overseer bus. moby
// fires `destroy` for every `docker rm` on a container; `remove` is
// an image-event action and never fires for containers (verified vs
// live moby event stream — zero `container/remove` actions in
// observed history). The returned cleanup must be deferred by the
// caller; it cancels the bus subscription and waits for the consumer
// goroutine to drain.
//
// Eviction trigger:
//   - container/destroy (moby action=destroy) — the container is gone
//     from the daemon for good and the row is orphaned.
//
// Stop/die/kill/oom do NOT evict: the container still exists in the
// daemon and may be `docker start`-ed back into life. The registry
// row survives so a subsequent restart finds its existing identity.
//
// log is required (pass logger.Nop() in tests that don't care about
// the audit trail). A nil logger is replaced with logger.Nop() so
// callers can't accidentally trip a nil deref inside the panic
// recovery path.
func Subscribe(ctx context.Context, reg Registry, bus *overseer.Overseer, log *logger.Logger) func() {
	if log == nil {
		log = logger.Nop()
	}
	sub, ok := overseer.SubscribeFiltered(bus, "agentregistry", func(ev dockerevents.DockerEvent) bool {
		return ev.Type == events.ContainerEventType && ev.Action == events.ActionDestroy
	})
	if !ok {
		log.Warn().Msg("agentregistry: bus closed before subscribe; consumer not started")
		return func() {}
	}

	done := runConsumer(ctx, sub.C, reg, log)

	return func() {
		sub.Unsubscribe()
		<-done
	}
}

// runConsumer drives the destroyed-event channel until ctx cancels
// or the channel closes, calling EvictByContainerID with each
// event's container_id. Panic-loop guardrails: fixed-capacity ring
// buffer caps memory regardless of panic rate; exceeding the
// ceiling terminates the consumer.
func runConsumer(ctx context.Context, ch <-chan dockerevents.DockerEvent, reg Registry, log *logger.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var panicTimes [subscribePanicWindowMaxHits]time.Time
		var panicHead int
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
			panicTimes[panicHead] = now
			panicHead = (panicHead + 1) % len(panicTimes)
			cutoff := now.Add(-subscribePanicWindow)
			recent := 0
			for _, t := range panicTimes {
				if !t.IsZero() && t.After(cutoff) {
					recent++
				}
			}
			if recent >= subscribePanicWindowMaxHits {
				log.Error().
					Int("panic_count", recent).
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
	return done
}

// handleEvent evicts the row keyed by the event's container_id. Best-
// effort: we cannot retry from a bus consumer (the next event is
// already queued), so log the error and proceed. The startup Reap
// heals stale rows that survived a transient sqlite failure.
func handleEvent(ev dockerevents.DockerEvent, reg Registry, log *logger.Logger) error {
	cid := ev.Actor.ID
	if cid == "" {
		return nil
	}
	if err := reg.EvictByContainerID(cid); err != nil {
		log.Warn().
			Err(err).
			Str("container_id", cid).
			Msg("agentregistry: evict-on-delete failed; row may persist until next reap")
		return err
	}
	return nil
}

// drainOnce runs the typed-event consumer until ctx is done, the
// channel is closed, or a panic in handleEvent unwinds. Returns true
// when the consumer is finished for good (ctx canceled or channel
// closed) and false when it should be restarted (panic recovered).
// Split out from runConsumer so the deferred recover has its own
// stack frame.
//
// Tracks consecutive evict failures so a sustained sqlite write
// failure doesn't decay into a silent row leak: once the threshold
// is hit the consumer emits a single Error line and continues. The
// counter resets on the first successful evict so a transient blip
// recovers cleanly.
func drainOnce(ctx context.Context, ch <-chan dockerevents.DockerEvent, reg Registry, log *logger.Logger) (terminate bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("agentregistry subscribe consumer panicked; resuming")
			terminate = false
		}
	}()
	consecutiveEvictFailures := 0
	escalated := false
	for {
		select {
		case <-ctx.Done():
			return true
		case ev, ok := <-ch:
			if !ok {
				return true
			}
			if err := handleEvent(ev, reg, log); err != nil {
				consecutiveEvictFailures++
				if !escalated && consecutiveEvictFailures >= subscribeEvictEscalationThreshold {
					log.Error().
						Int("consecutive_failures", consecutiveEvictFailures).
						Msg("agentregistry: sustained evict failures; rows are leaking and will not heal until restart-time Reap")
					escalated = true
				}
			} else {
				consecutiveEvictFailures = 0
				escalated = false
			}
		}
	}
}

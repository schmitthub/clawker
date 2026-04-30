package agentdial

import (
	"context"
	"time"

	"github.com/moby/moby/api/types/events"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// Subscribe panic-loop guardrails. Mirrors agentregistry.Subscribe —
// see that package for the full rationale on rate-limiting recoveries
// so a deterministic panic source can't fill the log buffer.
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

// Subscribe wires the Dialer to the union of moby container actions
// that transition a container into running state — Start, Restart,
// UnPause. All three actions ride the single dockerevents.DockerEvent
// envelope; the SubscribeFiltered predicate selects them at the
// consumer boundary. A previously-not-running container reaching
// running state via any of those paths needs a CP→clawkerd dial. The
// Dialer's internal dedup map prevents double-dial of the same
// container_id when overlapping events deliver.
//
// ActionCreate is intentionally NOT in the predicate: a created-but-
// not-started container has no clawkerd listener, so dialing it
// always fails. The next ActionStart (or ActionRestart / ActionUnPause)
// republishes the envelope and Subscribe re-fires DialAgent then.
//
// The same DialAgent function is the call target the initial
// listAgentIDs poll uses at CP boot — one dial code path, multiple
// callers.
//
// Filter: only purpose=agent containers
// (consts.LabelPurpose=consts.PurposeAgent). Non-agent containers
// (CP itself, host proxy, hostproxytest) never start a clawkerd
// listener and shouldn't be dialed.
//
// log is required (pass logger.Nop() in tests). The returned cleanup
// must be deferred by the caller; it cancels the bus subscription
// and waits for the consumer goroutine to drain.
func Subscribe(ctx context.Context, dialer *Dialer, bus *overseer.Overseer, log *logger.Logger) func() {
	if log == nil {
		log = logger.Nop()
	}

	sub, ok := overseer.SubscribeFiltered(bus, "agentdial", func(ev dockerevents.DockerEvent) bool {
		if ev.Type != events.ContainerEventType {
			return false
		}
		switch ev.Action {
		case events.ActionStart, events.ActionRestart, events.ActionUnPause:
			return ev.Actor.Attributes[consts.LabelPurpose] == consts.PurposeAgent
		}
		return false
	})
	if !ok {
		log.Warn().Msg("agentdial: bus closed before subscribe; consumer not started")
		return func() {}
	}

	done := runConsumer(ctx, dialer, sub.C, log)

	return func() {
		sub.Unsubscribe()
		<-done
	}
}

// runConsumer drives the typed channel until ctx cancels or the
// channel closes, calling DialAgent with the container_id from each
// event. Panic-loop guardrails: fixed-capacity ring buffer caps
// memory regardless of panic rate; exceeding the ceiling terminates
// the consumer.
func runConsumer(ctx context.Context, dialer *Dialer, ch <-chan dockerevents.DockerEvent, log *logger.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var panicTimes [subscribePanicWindowMaxHits]time.Time
		var panicHead int
		var lastPanic time.Time
		backoff := subscribePanicBackoffMin
		for {
			if drainOnce(ctx, ch, dialer, log) {
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
					Msg("agentdial subscribe consumer: panic rate exceeded ceiling; terminating consumer")
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

// drainOnce runs the typed-event consumer until ctx is done, the
// channel is closed, or a panic in DialAgent unwinds. Mirrors
// agentregistry.drainOnce — deferred recover lives in its own stack
// frame so the inner select doesn't have to deal with recovery.
func drainOnce(ctx context.Context, ch <-chan dockerevents.DockerEvent, dialer *Dialer, log *logger.Logger) (terminate bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("agentdial subscribe consumer panicked; resuming")
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
			dialer.DialAgent(ctx, ev.Actor.ID)
		}
	}
}

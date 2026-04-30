package agentdial

import (
	"context"
	"time"

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
// that transition a container into running state — Started,
// Restarted, Unpaused. Each fires its own typed bus event in the
// post-refactor 1:1 vocabulary; a previously-not-running container
// reaching running state via any of those paths needs a CP→clawkerd
// dial, so we subscribe to all three. The Dialer's internal dedup
// map prevents double-dial of the same container_id when multiple
// inbound channels deliver overlapping events.
//
// ContainerCreated is intentionally NOT in the subscription set: a
// created-but-not-started container has no clawkerd listener, so
// dialing it always fails. The next ActionStart (or ActionRestart /
// ActionUnPause) republishes the appropriate typed event and the
// Subscribe path re-fires DialAgent then.
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
// must be deferred by the caller; it cancels the bus subscriptions
// and waits for every consumer goroutine to drain.
func Subscribe(ctx context.Context, dialer *Dialer, bus *overseer.Overseer, log *logger.Logger) func() {
	if log == nil {
		log = logger.Nop()
	}

	startedSub, ok := overseer.SubscribeFiltered(bus, "agentdial.started", agentPurposeFilter[dockerevents.ContainerStarted])
	if !ok {
		log.Warn().Msg("agentdial: bus closed before subscribe; consumer not started")
		return func() {}
	}
	restartedSub, ok := overseer.SubscribeFiltered(bus, "agentdial.restarted", agentPurposeFilter[dockerevents.ContainerRestarted])
	if !ok {
		startedSub.Unsubscribe()
		log.Warn().Msg("agentdial: bus closed before restart subscribe; consumer not started")
		return func() {}
	}
	unpausedSub, ok := overseer.SubscribeFiltered(bus, "agentdial.unpaused", agentPurposeFilter[dockerevents.ContainerUnpaused])
	if !ok {
		startedSub.Unsubscribe()
		restartedSub.Unsubscribe()
		log.Warn().Msg("agentdial: bus closed before unpause subscribe; consumer not started")
		return func() {}
	}

	doneStarted := runConsumer(ctx, dialer, startedSub.C, log.With("running_transition", "started"))
	doneRestarted := runConsumer(ctx, dialer, restartedSub.C, log.With("running_transition", "restarted"))
	doneUnpaused := runConsumer(ctx, dialer, unpausedSub.C, log.With("running_transition", "unpaused"))

	return func() {
		startedSub.Unsubscribe()
		restartedSub.Unsubscribe()
		unpausedSub.Unsubscribe()
		<-doneStarted
		<-doneRestarted
		<-doneUnpaused
	}
}

// runningEvent is the constraint for events that signal "container
// reached running state". Each implements Subscribe's union of
// dockerevents types via embedded ContainerEvent — every one exposes
// the container ID through Actor.ID.
type runningEvent interface {
	dockerevents.ContainerStarted | dockerevents.ContainerRestarted | dockerevents.ContainerUnpaused
}

// agentPurposeFilter returns a SubscribeFiltered predicate that
// matches only events whose Actor.Attributes carry the
// purpose=agent label. Generic over the running-event union so the
// same predicate logic backs all three subscriptions without
// per-type duplication.
func agentPurposeFilter[T runningEvent](ev T) bool {
	switch e := any(ev).(type) {
	case dockerevents.ContainerStarted:
		return e.Actor.Attributes[consts.LabelPurpose] == consts.PurposeAgent
	case dockerevents.ContainerRestarted:
		return e.Actor.Attributes[consts.LabelPurpose] == consts.PurposeAgent
	case dockerevents.ContainerUnpaused:
		return e.Actor.Attributes[consts.LabelPurpose] == consts.PurposeAgent
	default:
		return false
	}
}

// runConsumer drives one typed channel until ctx cancels or the
// channel closes, calling DialAgent with the container_id from each
// event. Panic-loop guardrails mirror the original implementation —
// a fixed-capacity ring buffer caps memory regardless of panic rate;
// exceeding the ceiling terminates this consumer (other typed
// consumers stay alive).
func runConsumer[T runningEvent](ctx context.Context, dialer *Dialer, ch <-chan T, log *logger.Logger) <-chan struct{} {
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
func drainOnce[T runningEvent](ctx context.Context, ch <-chan T, dialer *Dialer, log *logger.Logger) (terminate bool) {
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
			dialer.DialAgent(ctx, containerID(ev))
		}
	}
}

// containerID extracts the container ID from any running-transition
// event. The ContainerEvent base embeds events.Message so Actor.ID
// is the canonical path to the id; the per-action wrapper types
// expose it via the same promoted field. Generic switch keeps the
// extraction symmetrical with agentPurposeFilter.
func containerID[T runningEvent](ev T) string {
	switch e := any(ev).(type) {
	case dockerevents.ContainerStarted:
		return e.Actor.ID
	case dockerevents.ContainerRestarted:
		return e.Actor.ID
	case dockerevents.ContainerUnpaused:
		return e.Actor.ID
	default:
		return ""
	}
}

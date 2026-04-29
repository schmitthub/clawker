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
const (
	subscribePanicBackoffMin    = 100 * time.Millisecond
	subscribePanicBackoffMax    = 30 * time.Second
	subscribePanicWindow        = 5 * time.Minute
	subscribePanicWindowMaxHits = 100
)

// Subscribe wires the Dialer to dockerevents.ContainerStarted events
// so a purpose=agent container starting at runtime triggers a
// CP→clawkerd dial. The same DialAgent function is the call target
// the initial listAgentIDs poll uses at CP boot — one dial code path,
// two callers.
//
// We react to ContainerStarted only (covers Docker create/start/
// restart/unpause). A container in the "stopped" or "removed" state
// has no listener; the dialer's runDial loop already exits naturally
// when the underlying Session breaks, so eviction here would be
// redundant.
//
// Filter: only purpose=agent containers (consts.LabelPurpose=
// consts.PurposeAgent). Non-agent containers (CP itself, host proxy,
// hostproxytest) never start a clawkerd listener and shouldn't be
// dialed.
//
// log is required (pass logger.Nop() in tests). The returned cleanup
// must be deferred by the caller; it cancels the bus subscription
// and waits for the consumer goroutine to drain.
func Subscribe(ctx context.Context, dialer *Dialer, bus *overseer.Overseer, log *logger.Logger) func() {
	if log == nil {
		log = logger.Nop()
	}
	sub, ok := overseer.SubscribeFiltered(bus, "agentdial", func(ev dockerevents.ContainerStarted) bool {
		return ev.Labels[consts.LabelPurpose] == consts.PurposeAgent
	})
	if !ok {
		log.Warn().Msg("agentdial: bus closed before subscribe; consumer not started")
		return func() {}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var panicTimes []time.Time
		var lastPanic time.Time
		backoff := subscribePanicBackoffMin
		for {
			if drainOnce(ctx, sub.C, dialer, log) {
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

	return func() {
		sub.Unsubscribe()
		<-done
	}
}

// drainOnce runs the typed-event consumer until ctx is done, the
// channel is closed, or a panic in DialAgent unwinds. Mirrors
// agentregistry.drainOnce — deferred recover lives in its own stack
// frame so the inner select doesn't have to deal with recovery.
func drainOnce(ctx context.Context, ch <-chan dockerevents.ContainerStarted, dialer *Dialer, log *logger.Logger) (terminate bool) {
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
			dialer.DialAgent(ctx, ev.ID)
		}
	}
}

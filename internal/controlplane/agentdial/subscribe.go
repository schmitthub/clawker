package agentdial

import (
	"context"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/informer"
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

// Subscribe wires the Dialer to the informer's container deltas so a
// purpose=agent container starting at runtime triggers a CP→clawkerd
// dial. The same DialAgent function is the call target the initial
// listAgentIDs poll uses at CP boot — one dial code path, two
// callers.
//
// We react to "started" specifically (Lifecycle == "running"), NOT
// "created". A container in the "created" state has no PID, no
// listener, no clawker-net IP yet. Dialing then would fail; even
// with retry it would burn the budget against a container that
// hasn't actually started.
//
// Trigger conditions on the After resource (filtered to
// purpose=agent containers only):
//   - DeltaAdded with Lifecycle == "running" — first observation
//     after CP startup of a running container that snuck through
//     between listAgentIDs poll and Subscribe activation; the dedup
//     map on Dialer keeps this from double-dialing what initial
//     poll already grabbed.
//   - DeltaUpdated where Before.Lifecycle != "running" and
//     After.Lifecycle == "running" — runtime transition INTO
//     running. Covers fresh container starts AND restart-from-stopped.
//
// Ignored:
//   - DeltaUpdated where lifecycle stayed "running" (e.g. label
//     update, attr refresh) — already dialed.
//   - paused/unpaused — process is alive, Session unaffected.
//   - DeltaRemoved / Lifecycle="stopped" — eviction is a future
//     step (per-container cancel-by-id wiring); the dial goroutine
//     observes the broken stream when the container dies and exits
//     naturally.
//
// log is required (pass logger.Nop() in tests). The returned cleanup
// must be deferred by the caller; it cancels the informer
// subscription and waits for the consumer goroutine to drain.
func Subscribe(ctx context.Context, dialer *Dialer, inf informer.Interface, log *logger.Logger) func() {
	if log == nil {
		log = logger.Nop()
	}
	_, ch, cancel := inf.Subscribe(informer.Filter{
		Kinds: []string{dockerevents.KindContainer},
		Labels: informer.LabelSelector{
			Equals: map[string]string{consts.LabelPurpose: consts.PurposeAgent},
		},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		var panicTimes []time.Time
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
		cancel()
		<-done
	}
}

// handleDelta dispatches one delta to DialAgent if it represents a
// transition into "running". The informer filter has already narrowed
// to purpose=agent container kinds; we still re-check Lifecycle
// because the filter matches deltas where EITHER Before OR After
// matches (a stop transition would have Before.Lifecycle=="running"
// and pass the filter even though we don't want to dial).
func handleDelta(ctx context.Context, d informer.Delta, dialer *Dialer) {
	switch d.Kind {
	case informer.DeltaAdded:
		if d.After != nil && d.After.Lifecycle == dockerevents.LifecycleRunning {
			dialer.DialAgent(ctx, d.After.ID)
		}
	case informer.DeltaUpdated:
		if d.After == nil || d.After.Lifecycle != dockerevents.LifecycleRunning {
			return
		}
		// Only fire on transition INTO running. If Before was also
		// running, this is a label/attr refresh and the dial is
		// already in flight (or holding a Session) — dedup map
		// would skip anyway, but explicit guard keeps the log
		// quiet.
		if d.Before != nil && d.Before.Lifecycle == dockerevents.LifecycleRunning {
			return
		}
		dialer.DialAgent(ctx, d.After.ID)
	}
}

// drainOnce runs the delta consumer until ctx is done, the channel
// is closed, or a panic in handleDelta unwinds. Mirrors
// agentregistry.drainOnce — deferred recover lives in its own stack
// frame so the inner select doesn't have to deal with recovery.
func drainOnce(ctx context.Context, ch <-chan informer.Delta, dialer *Dialer, log *logger.Logger) (terminate bool) {
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
		case d, ok := <-ch:
			if !ok {
				return true
			}
			handleDelta(ctx, d, dialer)
		}
	}
}

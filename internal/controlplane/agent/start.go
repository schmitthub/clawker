package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moby/moby/api/types/events"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// ContainerLister enumerates every `purpose=agent` container ID currently
// known to the docker daemon. The implementation MUST include
// stopped/exited containers — a stopped container can be `docker
// start`-ed back into life, and its registry row should survive that
// transition. Only `docker rm` (destroy) means the container is
// genuinely gone and the row is orphaned.
type ContainerLister func(ctx context.Context) ([]string, error)

// StartDeps bundles the dependencies the umbrella `Start` procedure
// needs. Bus + Registry + Dialer + Docker lister + peer-IP resolver
// are all owned by the CP startup path; passing them as a single
// struct keeps the call site in cmd/clawker-cp/main.go a single
// function call. All fields are required.
type StartDeps struct {
	Registry     Registry
	DockerLister ContainerLister
	PeerLookup   ContainerByPeerIP
	Dialer       *Dialer
	Bus          *overseer.Overseer
	Log          *logger.Logger
}

// Start gathers the full agent worldview at CP boot and wires every
// ongoing agent-axis subscription. Called once from cmd/clawker-cp/main.go
// after the bus is up. Returns a cleanup func that unwinds in reverse
// order.
//
// Steps, in order:
//
//  1. Reap orphan registry rows. List every purpose=agent container
//     (All:true, includes stopped). For each registry row whose
//     container_id is missing from docker, evict. Heals the registry
//     against `docker rm`s that landed while CP was down.
//  2. Subscribe to dockerevents.DockerEvent for the evict path —
//     filter on container/destroy; consumer evicts the registry row.
//  3. Subscribe to dockerevents.DockerEvent for the dial path —
//     filter on container/start|restart|unpause with purpose=agent;
//     consumer calls dialer.DialAgent.
//
// The previously-fragmented exports (Reap, registry Subscribe, dial
// Subscribe) are now unexported helpers behind this single function.
func Start(ctx context.Context, deps StartDeps) (func(), error) {
	log := deps.Log
	if log == nil {
		log = logger.Nop()
	}
	if deps.Registry == nil {
		return nil, fmt.Errorf("agent.Start: Registry is required")
	}
	if deps.DockerLister == nil {
		return nil, fmt.Errorf("agent.Start: DockerLister is required")
	}
	if deps.Bus == nil {
		return nil, fmt.Errorf("agent.Start: Bus is required")
	}
	if deps.Dialer == nil {
		return nil, fmt.Errorf("agent.Start: Dialer is required")
	}
	if deps.PeerLookup == nil {
		return nil, fmt.Errorf("agent.Start: PeerLookup is required")
	}

	// Step 1: Reap orphan registry rows against the live docker view.
	if _, err := reapOrphans(ctx, deps.Registry, deps.DockerLister, log); err != nil {
		// Soft-fail: the dockerevents/destroy subscription will catch
		// future evictions, and the next CP restart re-runs reap. A
		// startup-time reap failure must not block the bus from coming
		// up — CP must always be reachable for containment. Publish a
		// typed event so worldview consumers (alerting, monitoring)
		// know the registry may contain ghost rows for containers
		// destroyed while CP was down.
		log.Warn().Err(err).Msg("agent: startup reap failed; continuing without orphan cleanup")
		overseer.Publish(deps.Bus, ReapDegraded{
			Reason: err.Error(),
			At:     time.Now(),
		})
	}

	// Step 2: container/destroy → evict registry row.
	evictCleanup, err := subscribeEvict(ctx, deps.Registry, deps.Bus, log)
	if err != nil {
		return nil, fmt.Errorf("agent.Start: subscribe evict: %w", err)
	}

	// Step 3: container/{die,stop,kill,oom,destroy} → cancel the
	// in-flight CP→clawkerd Session. The docker event is the source
	// of truth for "clawkerd is no longer serving"; without this,
	// the dialer would only notice on its next reconnect attempt
	// (outcomeContainerGone) and the doomed stream would linger.
	// Independent of the evict subscriber — evict reflects row
	// state (destroy only), cancel reflects connectivity state
	// (any exit transition).
	cancelCleanup, err := subscribeSessionCancel(ctx, deps.Dialer, deps.Bus, log)
	if err != nil {
		evictCleanup()
		return nil, fmt.Errorf("agent.Start: subscribe session cancel: %w", err)
	}

	// Step 4: container/start|restart|unpause → dial agent.
	dialCleanup, err := subscribeDial(ctx, deps.Dialer, deps.Bus, log)
	if err != nil {
		cancelCleanup()
		evictCleanup()
		return nil, fmt.Errorf("agent.Start: subscribe dial: %w", err)
	}

	cleanup := func() {
		// Reverse-order unwind so each subscriber drains before the
		// next stops fan-in.
		dialCleanup()
		cancelCleanup()
		evictCleanup()
	}
	return cleanup, nil
}

// --- Reap (private, replaces the old agentregistry.Reap export) ---

// reapListerMaxAttempts caps the bounded retry on transient docker
// daemon failures. The reap runs once at CP startup, so a brief
// daemon-restart window during boot must not skip the first sweep
// entirely (the dockerevents subscription only catches NEW destroys
// from that point forward).
const reapListerMaxAttempts = 3

// reapOrphans drops every registry row whose container_id is not
// present in the lister's snapshot.
func reapOrphans(ctx context.Context, reg Registry, lister ContainerLister, log *logger.Logger) (int, error) {
	ids, err := listWithRetry(ctx, lister, log)
	if err != nil {
		return 0, fmt.Errorf("listing containers: %w", err)
	}
	live := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		live[id] = struct{}{}
	}

	snap, err := reg.Snapshot()
	if err != nil {
		// Critical: abort reap rather than treat the (likely truncated
		// or nil) snap as authoritative. Iterating an empty result on a
		// query failure would evict every registered agent on the next
		// reap pass — see Registry.Snapshot contract.
		return 0, fmt.Errorf("snapshot registry: %w", err)
	}
	evicted := 0
	var evictErrs []error
	for _, e := range snap {
		if _, ok := live[e.ContainerID]; ok {
			continue
		}
		if err := reg.EvictByContainerID(e.ContainerID); err != nil {
			log.Error().
				Err(err).
				Str("container_id", e.ContainerID).
				Str("agent", e.AgentName.String()).
				Msg("agent: reap evict failed")
			evictErrs = append(evictErrs, fmt.Errorf("evict %s: %w", e.ContainerID, err))
			continue
		}
		evicted++
	}
	if evicted > 0 {
		log.Info().
			Int("evicted", evicted).
			Int("registry_size_before", len(snap)).
			Int("live_containers", len(live)).
			Msg("agent: reaped orphan rows")
	}
	if len(evictErrs) > 0 {
		return evicted, errors.Join(evictErrs...)
	}
	return evicted, nil
}

func listWithRetry(ctx context.Context, lister ContainerLister, log *logger.Logger) ([]string, error) {
	var lastErr error
	backoff := 100 * time.Millisecond
	for attempt := 1; attempt <= reapListerMaxAttempts; attempt++ {
		ids, err := lister(ctx)
		if err == nil {
			if attempt > 1 {
				log.Info().
					Int("attempt", attempt).
					Msg("agent: reap lister recovered after retry")
			}
			return ids, nil
		}
		lastErr = err
		if attempt == reapListerMaxAttempts {
			break
		}
		log.Warn().
			Err(err).
			Int("attempt", attempt).
			Dur("backoff", backoff).
			Msg("agent: reap lister failed; retrying")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return nil, lastErr
}

// --- Subscriptions ---

// Panic-loop guardrails shared by both subscribers. A deterministic
// panic source (bad event handler, latent bug) without backoff would
// spin recover → drainOnce → recover at line speed, filling the
// rotated log files in seconds. Sleep between recoveries with
// exponential backoff capped at the ceiling, and after enough panics
// in a sliding window terminate the consumer so the wrapping process
// surfaces the runaway loudly.
const consumerPanicWindowMaxHits = 100

var (
	consumerPanicBackoffMin = 100 * time.Millisecond
	consumerPanicBackoffMax = 30 * time.Second
	consumerPanicWindow     = 5 * time.Minute
)

// evictEscalationThreshold is the number of consecutive
// EvictByContainerID failures before escalation to a single Error log.
var evictEscalationThreshold = 5

// subscribeEvict wires the registry's evict path to dockerevents
// container/destroy envelopes. moby fires `destroy` for every
// `docker rm` on a container; `remove` is image-only and never fires
// for containers.
//
// Stop/die/kill/oom do NOT evict: the container still exists in the
// daemon and may be `docker start`-ed back into life. The registry
// row survives so a subsequent restart finds its existing identity.
// sessionCanceller is the narrow projection of the Dialer that the
// session-cancel subscriber needs. Defining it as an interface
// (rather than taking *Dialer directly) keeps the consumer testable
// without constructing a real dialer + grpc transport.
type sessionCanceller interface {
	CancelDial(containerID string)
}

func subscribeEvict(ctx context.Context, reg Registry, bus *overseer.Overseer, log *logger.Logger) (func(), error) {
	sub, ok := overseer.SubscribeFiltered(bus, "agent.evict", func(ev dockerevents.DockerEvent) bool {
		return ev.Type == events.ContainerEventType && ev.Action == events.ActionDestroy
	})
	if !ok {
		return nil, fmt.Errorf("bus closed before subscribe")
	}

	done := runEvictConsumer(ctx, sub.C, reg, log)
	return func() {
		sub.Unsubscribe()
		<-done
	}, nil
}

func runEvictConsumer(ctx context.Context, ch <-chan dockerevents.DockerEvent, reg Registry, log *logger.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		runWithBackoff(ctx, log, "agent.evict", func() bool {
			return drainEvictOnce(ctx, ch, reg, log)
		})
	}()
	return done
}

func drainEvictOnce(ctx context.Context, ch <-chan dockerevents.DockerEvent, reg Registry, log *logger.Logger) (terminate bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("agent.evict consumer panicked; resuming")
			terminate = false
		}
	}()
	consecutiveFailures := 0
	escalated := false
	for {
		select {
		case <-ctx.Done():
			return true
		case ev, ok := <-ch:
			if !ok {
				return true
			}
			cid := ev.Actor.ID
			if cid == "" {
				continue
			}
			if err := reg.EvictByContainerID(cid); err != nil {
				log.Warn().Err(err).Str("container_id", cid).
					Msg("agent.evict: evict-on-delete failed; row may persist until next reap")
				consecutiveFailures++
				if !escalated && consecutiveFailures >= evictEscalationThreshold {
					log.Error().
						Int("consecutive_failures", consecutiveFailures).
						Msg("agent.evict: sustained evict failures; rows are leaking and will not heal until restart-time Reap")
					escalated = true
				}
			} else {
				consecutiveFailures = 0
				escalated = false
			}
		}
	}
}

// sessionCancelEventPredicate matches every container-level docker
// event that means "clawkerd in this container can no longer serve":
// die/stop/kill/oom (process gone but container may be docker
// start-able) and destroy (container removed). The dial subscriber's
// counterpart fires on start/restart/unpause; together they keep
// the Session connection-state aligned with docker truth without
// going through the registry.
//
// Distinct from dialEventPredicate (which fires on
// start/restart/unpause) and from the evict predicate (destroy only,
// because evict semantics are tied to row deletion, not connectivity).
func sessionCancelEventPredicate(ev dockerevents.DockerEvent) bool {
	if ev.Type != events.ContainerEventType {
		return false
	}
	switch ev.Action {
	case events.ActionDie, events.ActionStop, events.ActionKill,
		events.ActionOOM, events.ActionDestroy:
		return true
	}
	return false
}

// subscribeSessionCancel wires CancelDial to docker container lifecycle
// events. The docker event is the source of truth for "clawkerd is
// no longer serving"; canceling at this layer keeps the dialer
// independent of registry state.
func subscribeSessionCancel(ctx context.Context, dialer sessionCanceller, bus *overseer.Overseer, log *logger.Logger) (func(), error) {
	if dialer == nil {
		return func() {}, nil
	}
	sub, ok := overseer.SubscribeFiltered(bus, "agent.session_cancel", sessionCancelEventPredicate)
	if !ok {
		return nil, fmt.Errorf("bus closed before subscribe")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		runWithBackoff(ctx, log, "agent.session_cancel", func() bool {
			return drainSessionCancelOnce(ctx, sub.C, dialer, log)
		})
	}()
	return func() {
		sub.Unsubscribe()
		<-done
	}, nil
}

func drainSessionCancelOnce(ctx context.Context, ch <-chan dockerevents.DockerEvent, dialer sessionCanceller, log *logger.Logger) (terminate bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("agent.session_cancel consumer panicked; resuming")
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
			cid := ev.Actor.ID
			if cid == "" {
				continue
			}
			// CancelDial is a no-op when no dial is in flight; safe
			// to call on every transition.
			dialer.CancelDial(cid)
		}
	}
}

// dialEventPredicate is the filter subscribeDial wires onto the
// dockerevents bus. Exposed as a named function (not an inline
// lambda) so tests can pin the exact predicate the production wiring
// uses without duplicating the logic.
//
// Dial actions: Start, Restart, UnPause — every transition that takes
// a container into running state. ActionCreate is intentionally
// excluded: a created-but-not-started container has no clawkerd
// listener, so dialing always fails. The next ActionStart re-fires.
//
// Scope: only purpose=agent containers. Non-agent containers (CP
// itself, host proxy) never start a clawkerd listener.
func dialEventPredicate(ev dockerevents.DockerEvent) bool {
	if ev.Type != events.ContainerEventType {
		return false
	}
	switch ev.Action {
	case events.ActionStart, events.ActionRestart, events.ActionUnPause:
		return ev.Actor.Attributes[consts.LabelPurpose] == consts.PurposeAgent
	}
	return false
}

// subscribeDial wires the Dialer to dialEventPredicate-matching
// dockerevents. The Dialer's internal dedup map prevents double-dial
// of the same container_id when overlapping events deliver.
func subscribeDial(ctx context.Context, dialer *Dialer, bus *overseer.Overseer, log *logger.Logger) (func(), error) {
	sub, ok := overseer.SubscribeFiltered(bus, "agent.dial", dialEventPredicate)
	if !ok {
		return nil, fmt.Errorf("bus closed before subscribe")
	}

	done := runDialConsumer(ctx, dialer, sub.C, log)
	return func() {
		sub.Unsubscribe()
		<-done
	}, nil
}

func runDialConsumer(ctx context.Context, dialer *Dialer, ch <-chan dockerevents.DockerEvent, log *logger.Logger) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		runWithBackoff(ctx, log, "agent.dial", func() bool {
			return drainDialOnce(ctx, ch, dialer, log)
		})
	}()
	return done
}

func drainDialOnce(ctx context.Context, ch <-chan dockerevents.DockerEvent, dialer *Dialer, log *logger.Logger) (terminate bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Msg("agent.dial consumer panicked; resuming")
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

// runWithBackoff drives the per-iteration drain function with the
// shared panic-loop guardrails. Returns when drain returns true
// (terminate signal) or panic rate exceeds the ceiling.
func runWithBackoff(ctx context.Context, log *logger.Logger, name string, drain func() bool) {
	var panicTimes [consumerPanicWindowMaxHits]time.Time
	var panicHead int
	var lastPanic time.Time
	backoff := consumerPanicBackoffMin
	for {
		if drain() {
			return
		}
		now := time.Now()
		if !lastPanic.IsZero() && now.Sub(lastPanic) > 30*time.Second {
			backoff = consumerPanicBackoffMin
		}
		lastPanic = now
		panicTimes[panicHead] = now
		panicHead = (panicHead + 1) % len(panicTimes)
		cutoff := now.Add(-consumerPanicWindow)
		recent := 0
		for _, t := range panicTimes {
			if !t.IsZero() && t.After(cutoff) {
				recent++
			}
		}
		if recent >= consumerPanicWindowMaxHits {
			log.Error().
				Str("consumer", name).
				Int("panic_count", recent).
				Dur("window", consumerPanicWindow).
				Msg("agent: consumer panic rate exceeded ceiling; terminating")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < consumerPanicBackoffMax {
			backoff *= 2
			if backoff > consumerPanicBackoffMax {
				backoff = consumerPanicBackoffMax
			}
		}
	}
}

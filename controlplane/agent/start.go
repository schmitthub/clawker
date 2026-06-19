package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moby/moby/api/types/events"

	"github.com/schmitthub/clawker/controlplane/dockerevents"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// ContainerListFunc enumerates every `purpose=agent` container ID currently
// known to the docker daemon. The implementation MUST include
// stopped/exited containers — a stopped container can be `docker
// start`-ed back into life, and its registry row should survive that
// transition. Only `docker rm` (destroy) means the container is
// genuinely gone and the row is orphaned.
type ContainerListFunc func(ctx context.Context) ([]string, error)

// StartDeps bundles the dependencies the umbrella `Start` procedure
// needs. The topics + Registry + Dialer + Docker lister + peer-IP
// resolver are all owned by the CP orchestrator; passing them as a
// single struct keeps the call site a single function call. All fields
// are required.
//
// DockerTopic is the dockerevents domain's Topic[DockerEvent]: the
// evict, session-cancel, and dial subscribers attach to it with their
// predicates folded into the handler closures. AgentTopic is this
// domain's own Topic[AgentEvent], used by the reap-degraded notification
// (the only AgentEvent Start itself publishes).
type StartDeps struct {
	Registry     Registry
	DockerLister ContainerListFunc
	PeerLookup   ContainerByPeerIP
	Dialer       *Dialer
	DockerTopic  *pubsub.Topic[dockerevents.DockerEvent]
	AgentTopic   *pubsub.Topic[AgentEvent]
	Log          *logger.Logger
}

// Start gathers the full agent worldview at CP boot and wires every
// ongoing agent-axis subscription onto the dockerevents topic. Called
// once from the orchestrator after the topics are up. Returns a cleanup
// func; the per-subscriber delivery goroutines are owned by the topic
// itself (the orchestrator's topic.Close() tears them down), so the
// returned cleanup is a no-op kept for call-site symmetry.
//
// Steps, in order:
//
//  1. Reap orphan registry rows. List every purpose=agent container
//     (All:true, includes stopped). For each registry row whose
//     container_id is missing from docker, evict. Heals the registry
//     against `docker rm`s that landed while CP was down.
//  2. Subscribe to the dockerevents topic for the evict path — predicate
//     container/destroy folded into the handler; consumer evicts the
//     registry row.
//  3. Subscribe to the dockerevents topic for the session-cancel path —
//     predicate container/{die,stop,kill,oom,destroy}; consumer calls
//     dialer.CancelDial so the in-flight Session tears down immediately.
//  4. Subscribe to the dockerevents topic for the dial path — predicate
//     container/start|restart|unpause with purpose=agent; consumer calls
//     dialer.DialAgent.
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
	if deps.DockerTopic == nil {
		return nil, fmt.Errorf("agent.Start: DockerTopic is required")
	}
	if deps.AgentTopic == nil {
		return nil, fmt.Errorf("agent.Start: AgentTopic is required")
	}
	if deps.Dialer == nil {
		return nil, fmt.Errorf("agent.Start: Dialer is required")
	}
	if deps.PeerLookup == nil {
		return nil, fmt.Errorf("agent.Start: PeerLookup is required")
	}

	// Step 1: Reap orphan registry rows against the live docker view.
	if _, err := reapOrphans(ctx, deps.Registry, deps.DockerLister, log); err != nil {
		// Soft-fail: the destroy subscription will catch future
		// evictions, and the next CP restart re-runs reap. A startup-time
		// reap failure must not block the bus from coming up — CP must
		// always be reachable for containment. Publish a typed AgentEvent
		// so worldview consumers (alerting, monitoring) know the registry
		// may contain ghost rows for containers destroyed while CP was
		// down.
		log.Warn().Err(err).Msg("agent: startup reap failed; continuing without orphan cleanup")
		publish(deps.AgentTopic, newAgentEvent(Agent{}, Message{
			Type:   RegistryEventType,
			Action: ActionReap,
			Reason: ReasonFailed,
			Detail: err.Error(),
		}))
	}

	// Step 2: container/destroy → evict registry row.
	subscribeEvict(ctx, deps.DockerTopic, deps.Registry, log)

	// Step 3: container/{die,stop,kill,oom,destroy} → cancel the
	// in-flight CP→clawkerd Session. The docker event is the source of
	// truth for "clawkerd is no longer serving"; without this, the
	// dialer would only notice on its next reconnect attempt
	// (outcomeContainerGone) and the doomed stream would linger.
	// Independent of the evict subscriber — evict reflects row state
	// (destroy only), cancel reflects connectivity state (any exit
	// transition).
	subscribeSessionCancel(ctx, deps.DockerTopic, deps.Dialer, log)

	// Step 4: container/start|restart|unpause → dial agent.
	subscribeDial(ctx, deps.DockerTopic, deps.Dialer, log)

	// Subscriptions live for the lifetime of the topic; the orchestrator
	// tears them down via topic.Close(). Cleanup is a no-op kept for
	// call-site symmetry.
	return func() {}, nil
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
func reapOrphans(ctx context.Context, reg Registry, lister ContainerListFunc, log *logger.Logger) (int, error) {
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

func listWithRetry(ctx context.Context, lister ContainerListFunc, log *logger.Logger) ([]string, error) {
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
//
// The pipe (controlplane/pubsub) owns delivery: each Subscribe handler
// runs on the topic's own per-subscriber drain goroutine, under recover,
// against a bounded drop-oldest buffer. So these handlers carry no
// goroutine/backoff/panic-loop machinery of their own — they fold the
// event predicate into an early-return guard and do the work inline. A
// panicking handler is contained by the pipe to its one event; the
// daemon (and eBPF supervision) survives.

// evictEscalationThreshold is the number of consecutive
// EvictByContainerID failures before escalation to a single Error log.
var evictEscalationThreshold = 5

// sessionCanceller is the narrow projection of the Dialer that the
// session-cancel subscriber needs. Defining it as an interface (rather
// than taking *Dialer directly) keeps the consumer testable without
// constructing a real dialer + grpc transport.
type sessionCanceller interface {
	CancelDial(containerID string)
}

// subscribeEvict wires the registry's evict path to dockerevents
// container/destroy envelopes. moby fires `destroy` for every
// `docker rm` on a container; `remove` is image-only and never fires
// for containers.
//
// Stop/die/kill/oom do NOT evict: the container still exists in the
// daemon and may be `docker start`-ed back into life. The registry
// row survives so a subsequent restart finds its existing identity.
//
// consecutiveFailures is closed over by the handler so sustained evict
// failures escalate to a single Error line; the pipe's serialized,
// single-goroutine-per-subscriber delivery makes the closure-local
// counter safe without a mutex.
func subscribeEvict(ctx context.Context, topic *pubsub.Topic[dockerevents.DockerEvent], reg Registry, log *logger.Logger) {
	consecutiveFailures := 0
	escalated := false
	topic.Subscribe(func(evt pubsub.Event[dockerevents.DockerEvent]) {
		if ctx.Err() != nil {
			return
		}
		ev := evt.Payload
		if ev.Type != events.ContainerEventType || ev.Action != events.ActionDestroy {
			return
		}
		cid := ev.Actor.ID
		if cid == "" {
			return
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
			return
		}
		consecutiveFailures = 0
		escalated = false
	})
}

// sessionCancelEvent reports whether a docker event means "clawkerd in
// this container can no longer serve": die/stop/kill/oom (process gone
// but container may be docker start-able) and destroy (container
// removed). The dial subscriber's counterpart fires on
// start/restart/unpause; together they keep the Session connection-state
// aligned with docker truth without going through the registry.
//
// Distinct from dialEvent (start/restart/unpause) and from the evict
// predicate (destroy only, because evict semantics are tied to row
// deletion, not connectivity).
func sessionCancelEvent(ev dockerevents.DockerEvent) bool {
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
// events. The docker event is the source of truth for "clawkerd is no
// longer serving"; canceling at this layer keeps the dialer independent
// of registry state. A nil dialer is a no-op (no subscription).
func subscribeSessionCancel(ctx context.Context, topic *pubsub.Topic[dockerevents.DockerEvent], dialer sessionCanceller, log *logger.Logger) {
	if dialer == nil {
		return
	}
	topic.Subscribe(func(evt pubsub.Event[dockerevents.DockerEvent]) {
		if ctx.Err() != nil {
			return
		}
		ev := evt.Payload
		if !sessionCancelEvent(ev) {
			return
		}
		cid := ev.Actor.ID
		if cid == "" {
			return
		}
		// CancelDial is a no-op when no dial is in flight; safe to call
		// on every transition.
		dialer.CancelDial(cid)
	})
}

// dialEvent reports whether a docker event should trigger a dial. Dial
// actions: Start, Restart, UnPause — every transition that takes a
// container into running state. ActionCreate is intentionally excluded:
// a created-but-not-started container has no clawkerd listener, so
// dialing always fails; the next ActionStart re-fires. Scope: only
// purpose=agent containers (CP itself, host proxy never run clawkerd).
func dialEvent(ev dockerevents.DockerEvent) bool {
	if ev.Type != events.ContainerEventType {
		return false
	}
	switch ev.Action {
	case events.ActionStart, events.ActionRestart, events.ActionUnPause:
		return ev.Actor.Attributes[consts.LabelPurpose] == consts.PurposeAgent
	}
	return false
}

// subscribeDial wires the Dialer to dialEvent-matching dockerevents. The
// Dialer's internal dedup map prevents double-dial of the same
// container_id when overlapping events deliver. The dial ctx threaded to
// DialAgent is the orchestrator's CP-lifetime ctx passed to Start.
func subscribeDial(ctx context.Context, topic *pubsub.Topic[dockerevents.DockerEvent], dialer *Dialer, log *logger.Logger) {
	topic.Subscribe(func(evt pubsub.Event[dockerevents.DockerEvent]) {
		if ctx.Err() != nil {
			return
		}
		ev := evt.Payload
		if !dialEvent(ev) {
			return
		}
		dialer.DialAgent(ctx, ev.Actor.ID)
	})
}

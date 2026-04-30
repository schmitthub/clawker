package dockerevents

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

// Resource Kind strings owned by this feeder. The bus dispatches by
// Go type, not by string — these constants exist for Docker filter
// vocabulary only.
const (
	KindContainer = "container"
	KindNetwork   = "network"
)

// parseExitCode coerces moby's stringly-typed exitCode attribute to
// int32 once at the dispatch boundary, with a Debug audit on
// malformed input so a moby contract change surfaces in logs without
// breaking dispatch. Used for the receive-side audit log; consumer-
// facing accessors (ContainerDied.ExitCode, ContainerStopped.ExitCode)
// re-parse at the consumer layer.
func (f *Feeder) parseExitCode(raw, containerID string) int32 {
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		f.log.Debug().
			Err(err).
			Str("event", "dockerevents_exit_code_parse_failed").
			Str("container_id", containerID).
			Str("raw", raw).
			Msg("dockerevents: malformed exitCode attribute from moby")
		return 0
	}
	return int32(n)
}

// publishContainerEvent fans a typed event onto the bus and logs a
// Warn line carrying container_id when Publish drops it. The bus's
// own drop-log in subscribe.go only carries event name + queue
// capacity — pairing with container_id at the producer site lets
// operators investigating "why didn't agentdial dial container X"
// trace a thread back through the timeline.
func (f *Feeder) publishContainerEvent(ev overseer.Event, containerID string) {
	if overseer.Publish(f.bus, ev) {
		return
	}
	f.log.Warn().
		Str("event", ev.EventName()).
		Str("container_id", containerID).
		Msg("dockerevents: publish dropped — bus full or closed")
}

// publishNetworkEvent is the network counterpart — drops carry both
// container_id (when present, e.g. connect/disconnect) and
// network_id so the dropped event can be reconstructed.
func (f *Feeder) publishNetworkEvent(ev overseer.Event, containerID, networkID string) {
	if overseer.Publish(f.bus, ev) {
		return
	}
	f.log.Warn().
		Str("event", ev.EventName()).
		Str("container_id", containerID).
		Str("network_id", networkID).
		Msg("dockerevents: publish dropped — bus full or closed")
}

// dispatch routes a single Docker event into typed Publish calls on
// the bus. dispatch runs on Run's single goroutine; managed-set
// mutations are goroutine-local and need no lock.
//
// Logging contract:
//   - `docker event received` — fires for every actionable message
//     read off the events stream, with the full event schema attached
//     as structured fields. Operator UAT view: "what did Docker tell
//     us right now."
//
// The receive log lives upstream of the action allowlist so noisy
// non-state events (exec_*, healthcheck probes, etc) don't dominate
// Loki. A missing publish on the bus under a receive line means the
// dispatch path filtered it; a missing receive line means Docker
// never sent it (or our type-filter rejected it on the wire).
func (f *Feeder) dispatch(ctx context.Context, ev events.Message) {
	if !shouldHandleAction(ev) {
		return
	}

	f.logEventReceived(ev)

	switch ev.Type {
	case events.ContainerEventType:
		f.dispatchContainer(ev)
	case events.NetworkEventType:
		f.dispatchNetwork(ctx, ev)
	}
}

// logEventReceived dumps the full Docker event payload as structured
// fields. Actor.Attributes is folded out two ways: as an
// `actor_attributes` JSON-encoded aggregate for full-fidelity replay,
// plus one `actor_attr.<k>` field per entry so operators can filter on
// individual attribute keys in Loki without a JSON parser.
func (f *Feeder) logEventReceived(ev events.Message) {
	e := f.log.Info().
		Str("source", "docker").
		Str("type", string(ev.Type)).
		Str("action", string(ev.Action)).
		Str("actor_id", ev.Actor.ID).
		Str("scope", ev.Scope).
		Int64("time", ev.Time).
		Int64("time_nano", ev.TimeNano)
	for k, v := range ev.Actor.Attributes {
		e = e.Str("actor_attr."+k, v)
	}
	if len(ev.Actor.Attributes) > 0 {
		if b, err := json.Marshal(ev.Actor.Attributes); err == nil {
			e = e.RawJSON("actor_attributes", b)
		}
	}
	e.Msgf("docker event received: %s/%s id=%s", ev.Type, ev.Action, short(ev.Actor.ID))
}

// shouldHandleAction filters out diagnostic / high-volume actions that
// have no realm-state value. Returns true if the event should reach
// the per-type dispatcher.
func shouldHandleAction(ev events.Message) bool {
	a := string(ev.Action)
	if strings.HasPrefix(a, "exec_") {
		return false
	}
	switch ev.Action {
	case events.ActionAttach, events.ActionDetach, events.ActionResize,
		events.ActionCopy, events.ActionArchivePath, events.ActionExtractToDir,
		events.ActionTop, events.ActionCommit, events.ActionExport,
		events.ActionUpdate, events.ActionCheckpoint, events.ActionPrune,
		events.ActionPush, events.ActionImport, events.ActionSave, events.ActionLoad,
		events.ActionMount, events.ActionUnmount:
		return false
	}
	return true
}

// dispatchContainer translates moby's container action into the
// matching typed bus event. 1:1 mapping — no buckets. The event
// envelope (ContainerEvent) embeds the full moby Message so
// consumers reach through to Action / Actor.Attributes / TimeNano
// without a parallel field schema.
//
// The exitCode parse log (parseExitCode at Debug) is run for actions
// that carry an exit code so a moby contract change surfaces in the
// receive-side audit; the published event's accessor re-parses for
// consumers (zero-cost on the dispatch path that already audits).
func (f *Feeder) dispatchContainer(ev events.Message) {
	id := ev.Actor.ID
	if id == "" {
		return
	}

	managed := f.isManaged(ev.Actor.Attributes)
	if !managed && !f.containers[id] {
		return
	}

	envelope := ContainerEvent{Message: ev}

	switch ev.Action {
	case events.ActionCreate:
		f.containers[id] = true
		f.publishContainerEvent(ContainerCreated{envelope}, id)

	case events.ActionStart:
		f.containers[id] = true
		f.publishContainerEvent(ContainerStarted{envelope}, id)

	case events.ActionRestart:
		f.containers[id] = true
		f.publishContainerEvent(ContainerRestarted{envelope}, id)

	case events.ActionPause:
		f.containers[id] = true
		f.publishContainerEvent(ContainerPaused{envelope}, id)

	case events.ActionUnPause:
		f.containers[id] = true
		f.publishContainerEvent(ContainerUnpaused{envelope}, id)

	case events.ActionDie:
		f.containers[id] = true
		// Audit-side parse so a moby contract change surfaces in logs.
		_ = f.parseExitCode(ev.Actor.Attributes["exitCode"], id)
		f.publishContainerEvent(ContainerDied{envelope}, id)

	case events.ActionStop:
		f.containers[id] = true
		_ = f.parseExitCode(ev.Actor.Attributes["exitCode"], id)
		f.publishContainerEvent(ContainerStopped{envelope}, id)

	case events.ActionKill:
		f.containers[id] = true
		f.publishContainerEvent(ContainerKilled{envelope}, id)

	case events.ActionOOM:
		f.containers[id] = true
		f.publishContainerEvent(ContainerOOM{envelope}, id)

	case events.ActionDestroy:
		delete(f.containers, id)
		f.publishContainerEvent(ContainerDestroyed{envelope}, id)

	case events.ActionRemove:
		delete(f.containers, id)
		f.publishContainerEvent(ContainerRemoved{envelope}, id)

	case events.ActionRename:
		f.containers[id] = true
		f.publishContainerEvent(ContainerRenamed{envelope}, id)

	default:
		// Action not in the lifecycle vocabulary (health_status without
		// state change, etc.). Track managed-set membership so downstream
		// events don't drop, but don't publish.
		f.containers[id] = true
	}
}

// dispatchNetwork handles network events. Network actor Attributes do
// NOT carry network labels (verified vs moby
// daemon/events.go::LogNetworkEventWithAttributes), so first-sight
// network events trigger NetworkInspect to read labels.
//
// Publishes 1:1 with moby's network actions for managed networks:
// NetworkCreated / NetworkDestroyed / NetworkConnected /
// NetworkDisconnected. Unmanaged networks drop after the inspect.
func (f *Feeder) dispatchNetwork(ctx context.Context, ev events.Message) {
	netID := ev.Actor.ID
	if netID == "" {
		return
	}

	envelope := NetworkEvent{Message: ev}

	switch ev.Action {
	case events.ActionCreate:
		f.tryNetworkInspect(ctx, netID)
		if !f.networks[netID] {
			return
		}
		f.publishNetworkEvent(NetworkCreated{envelope}, "", netID)

	case events.ActionDestroy, events.ActionRemove:
		// Drop any pending recheck flag — a destroyed network can never
		// become managed retroactively.
		wasManaged := f.networks[netID]
		delete(f.networksNeedRecheck, netID)
		delete(f.networks, netID)
		if wasManaged {
			f.publishNetworkEvent(NetworkDestroyed{envelope}, "", netID)
		}

	case events.ActionConnect, events.ActionDisconnect:
		// If the initial Create-time inspect failed for this network,
		// retry now — the connect/disconnect event proves the network
		// is alive in Docker, so the inspect is more likely to succeed.
		if f.networksNeedRecheck[netID] {
			f.tryNetworkInspect(ctx, netID)
		}
		if !f.networks[netID] {
			return
		}
		ctrID := ev.Actor.Attributes["container"]
		if ctrID == "" {
			return
		}
		if !f.containers[ctrID] {
			// Wait for the container event — without a managed
			// container we have no anchor for the edge. The next
			// reconcile pass would republish if the container is in
			// fact managed.
			return
		}
		if ev.Action == events.ActionConnect {
			f.publishNetworkEvent(NetworkConnected{envelope}, ctrID, netID)
		} else {
			f.publishNetworkEvent(NetworkDisconnected{envelope}, ctrID, netID)
		}
	}
}

// tryNetworkInspect runs NetworkInspect for a previously-unseen
// network ID. Records the inspection state into the managed-set
// (success → tracked-or-unmanaged-and-forgotten) or the recheck
// queue (failure → retry on the next event for this ID). A network
// whose first inspect fails is otherwise permanently invisible
// because subsequent connect/destroy events skip the lookup.
func (f *Feeder) tryNetworkInspect(ctx context.Context, netID string) {
	res, err := f.cli.NetworkInspect(ctx, netID, client.NetworkInspectOptions{})
	if err != nil {
		// Don't escalate ctx.Canceled (drain path); transient inspect
		// failures during shutdown are expected.
		if errors.Is(err, context.Canceled) {
			return
		}
		f.networksNeedRecheck[netID] = true
		f.log.Warn().
			Err(err).
			Str("network_id", short(netID)).
			Msg("network inspect failed; will retry on next event for this id")
		return
	}
	delete(f.networksNeedRecheck, netID)
	if !f.isManaged(res.Network.Labels) {
		// Definitive: this network exists and is not managed. No retry.
		return
	}
	f.networks[netID] = true
}

// containerEventFromState maps a moby ContainerState observed during
// reconcile to the typed bus event we publish for that state. Returns
// the event or nil for transient states we don't model.
//
// Reconcile is observation, not action: we synthesize an
// events.Message-shaped envelope (Action is the moby action that
// would have caused the observed state) so the published wrapper is
// indistinguishable from a stream-delivered event. ContainerCreated
// is NEVER fabricated for reconcile — a created-but-not-running
// container produces no published event (consistent with the wire-
// side decision: ActionCreate publishes ContainerCreated, NOT
// ContainerStarted).
func containerEventFromState(s container.ContainerState, id string, c container.Summary, at time.Time) overseer.Event {
	envelope := ContainerEvent{Message: events.Message{
		Type:   events.ContainerEventType,
		Action: containerActionFromState(s),
		Actor: events.Actor{
			ID:         id,
			Attributes: containerAttributesFromSummary(c),
		},
		Scope:    "local",
		Time:     at.Unix(),
		TimeNano: at.UnixNano(),
	}}

	switch s {
	case container.StateRunning:
		return ContainerStarted{envelope}
	case container.StatePaused:
		return ContainerPaused{envelope}
	case container.StateExited, container.StateDead:
		return ContainerDied{envelope}
	case container.StateRemoving:
		return ContainerDestroyed{envelope}
	}
	// StateCreated, StateRestarting → no synthetic event; the next
	// real moby action will redrive when the container transitions.
	return nil
}

// containerActionFromState picks the moby action that would have
// caused the observed state. Used only for synthetic reconcile
// events; matches what the live event stream would have published.
func containerActionFromState(s container.ContainerState) events.Action {
	switch s {
	case container.StateRunning:
		return events.ActionStart
	case container.StatePaused:
		return events.ActionPause
	case container.StateExited, container.StateDead:
		return events.ActionDie
	case container.StateRemoving:
		return events.ActionDestroy
	}
	return ""
}

// containerAttributesFromSummary builds an Actor.Attributes map from
// a container.Summary — name + image as engine-set keys plus the
// container's Labels. Keeps the synthetic reconcile envelope
// indistinguishable from a wire-delivered event.
func containerAttributesFromSummary(c container.Summary) map[string]string {
	attrs := make(map[string]string, len(c.Labels)+2)
	if len(c.Names) > 0 {
		attrs["name"] = strings.TrimPrefix(c.Names[0], "/")
	}
	if c.Image != "" {
		attrs["image"] = c.Image
	}
	for k, v := range c.Labels {
		attrs[k] = v
	}
	return attrs
}

// short truncates an id to Docker's 12-char short form for log
// readability. ID strings shorter than 12 chars pass through.
func short(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

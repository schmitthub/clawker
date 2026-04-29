package dockerevents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

// Resource Kind strings owned by this feeder. Other feeders use their
// own vocabulary; the bus dispatches by Go type, not by string.
// KindContainer is retained as a re-export shim for callers that still
// need a string label (e.g. Docker filters), but is no longer used to
// route bus messages.
const (
	KindContainer = "container"
	KindNetwork   = "network"
)

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

// dispatchContainer handles container events. The actor's Attributes
// carry the container's labels verbatim plus engine-set keys (image,
// name, exitCode on die — verified vs moby daemon/events.go
// LogContainerEventWithAttributes).
//
// Mapping from Docker action → typed event:
//   - destroy / remove → ContainerRemoved
//   - die / stop / kill → ContainerStopped
//   - create / start / restart / unpause → ContainerStarted
//   - pause → no event published in v1 (no consumer; agent can survive
//     a paused container window — see agentslots which does NOT evict
//     on pause)
//
// Other actions (rename, oom-without-die, health_status without state
// change) are not republished; the feeder is interested in lifecycle
// transitions, not diagnostic chatter.
func (f *Feeder) dispatchContainer(ev events.Message) {
	id := ev.Actor.ID
	if id == "" {
		return
	}

	managed := f.isManaged(ev.Actor.Attributes)
	if !managed && !f.containers[id] {
		return
	}

	at := time.Unix(0, ev.TimeNano)

	switch ev.Action {
	case events.ActionDestroy, events.ActionRemove:
		delete(f.containers, id)
		overseer.Publish(f.bus, ContainerRemoved{ID: id, At: at})
		return

	case events.ActionDie, events.ActionStop, events.ActionKill:
		f.containers[id] = true
		overseer.Publish(f.bus, ContainerStopped{
			ID:       id,
			ExitCode: ev.Actor.Attributes["exitCode"],
			OOM:      false,
			At:       at,
		})
		return

	case events.ActionOOM:
		// OOM may fire alongside or instead of die — record it as a
		// stop with OOM=true so consumers get the signal even if Docker
		// elides the die event.
		f.containers[id] = true
		overseer.Publish(f.bus, ContainerStopped{
			ID:       id,
			ExitCode: ev.Actor.Attributes["exitCode"],
			OOM:      true,
			At:       at,
		})
		return

	case events.ActionCreate, events.ActionStart, events.ActionRestart, events.ActionUnPause:
		f.containers[id] = true
		labels := stripEngineKeys(ev.Actor.Attributes, "image", "name", "exitCode")
		overseer.Publish(f.bus, ContainerStarted{
			ID:     id,
			Name:   ev.Actor.Attributes["name"],
			Image:  ev.Actor.Attributes["image"],
			Labels: labels,
			At:     at,
		})
		return
	}

	// Action not in the lifecycle vocabulary (rename, pause, health
	// status without a transition). Track managed-set membership so
	// downstream events don't drop, but don't publish.
	f.containers[id] = true
}

// dispatchNetwork handles network events. Network actor Attributes do
// NOT carry network labels (verified vs moby
// daemon/events.go::LogNetworkEventWithAttributes), so first-sight
// network events trigger NetworkInspect to read labels.
//
// In the typed-event world, this dispatcher only publishes
// NetworkAttached / NetworkDetached. Network create/destroy update
// internal bookkeeping but produce no bus event (no subscriber).
func (f *Feeder) dispatchNetwork(ctx context.Context, ev events.Message) {
	netID := ev.Actor.ID
	if netID == "" {
		return
	}

	switch ev.Action {
	case events.ActionCreate:
		f.tryNetworkInspect(ctx, netID)

	case events.ActionDestroy, events.ActionRemove:
		// Drop any pending recheck flag — a destroyed network can never
		// become managed retroactively.
		delete(f.networksNeedRecheck, netID)
		delete(f.networks, netID)

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
			// container we have no producer of ContainerStarted to
			// anchor the edge against. The next reconcile pass would
			// republish if the container is in fact managed.
			return
		}
		at := time.Unix(0, ev.TimeNano)
		if ev.Action == events.ActionConnect {
			overseer.Publish(f.bus, NetworkAttached{
				ContainerID: ctrID,
				NetworkID:   netID,
				At:          at,
			})
		} else {
			overseer.Publish(f.bus, NetworkDetached{
				ContainerID: ctrID,
				NetworkID:   netID,
				At:          at,
			})
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

// containerStatusFromState maps a moby ContainerState to the bus
// event we publish for that state during reconcile. Returns the event
// or nil for transient states we don't model (restarting).
func containerEventFromState(s container.ContainerState, id string, c container.Summary, at time.Time) overseer.Event {
	switch s {
	case container.StateCreated, container.StateRunning:
		var name string
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		return ContainerStarted{
			ID:     id,
			Name:   name,
			Image:  c.Image,
			Labels: c.Labels,
			At:     at,
		}
	case container.StatePaused:
		// Treat paused as still-running for worldview purposes — the
		// container exists, just frozen.
		var name string
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		return ContainerStarted{
			ID:     id,
			Name:   name,
			Image:  c.Image,
			Labels: c.Labels,
			At:     at,
		}
	case container.StateExited, container.StateDead, container.StateRemoving:
		return ContainerStopped{ID: id, At: at}
	}
	return nil
}

// stripEngineKeys returns a copy of attrs with the listed engine-set
// keys removed. Engine-set keys live alongside user labels in
// Actor.Attributes; a Resource's Labels should hold only true labels.
func stripEngineKeys(attrs map[string]string, keys ...string) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	skip := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		skip[k] = struct{}{}
	}
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		if _, drop := skip[k]; drop {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// short truncates an id to Docker's 12-char short form for log
// readability. ID strings shorter than 12 chars pass through.
func short(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

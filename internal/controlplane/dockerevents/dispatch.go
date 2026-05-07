package dockerevents

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

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
// breaking dispatch.
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

// publishDockerEvent fans the single DockerEvent envelope onto the
// bus and logs a Warn line carrying ctxID (container_id where
// available, network_id otherwise) when Publish drops it. The bus's
// own drop-log only carries event name + queue capacity — pairing
// with ctxID at the producer site lets operators investigating "why
// didn't subscriber X react to event Y" trace a thread back through
// the timeline.
func (f *Feeder) publishDockerEvent(ev DockerEvent, ctxID string) {
	if overseer.Publish(f.bus, ev) {
		return
	}
	f.log.Warn().
		Str("event", ev.EventName()).
		Str("ctx_id", ctxID).
		Msg("dockerevents: publish dropped — bus full or closed")
}

// dispatch routes a single Docker event into a Publish call on the
// bus. dispatch runs on Run's single goroutine; managed-set
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

// dispatchContainer publishes the single DockerEvent envelope for the
// incoming container event. Managed-set membership is the only
// goroutine-local state mutation; consumers express intent via
// SubscribeFiltered predicates on ev.Type + ev.Action.
//
// The exitCode parse log (parseExitCode at Debug) is run for actions
// that carry an exit code so a moby contract change surfaces in the
// receive-side audit.
func (f *Feeder) dispatchContainer(ev events.Message) {
	id := ev.Actor.ID
	if id == "" {
		return
	}

	managed := f.isManaged(ev.Actor.Attributes)
	if !managed && !f.containers[id] {
		return
	}

	switch ev.Action {
	case events.ActionDestroy:
		// moby fires `destroy` for `docker rm`. `events.ActionRemove`
		// is image-only and never reaches this switch for container
		// events.
		delete(f.containers, id)
	default:
		f.containers[id] = true
		if ev.Action == events.ActionDie || ev.Action == events.ActionStop {
			_ = f.parseExitCode(ev.Actor.Attributes["exitCode"], id)
		}
	}

	f.publishDockerEvent(DockerEvent{Message: ev}, id)
}

// dispatchNetwork handles network events. Network actor Attributes do
// NOT carry network labels (verified vs moby
// daemon/events.go::LogNetworkEventWithAttributes), so first-sight
// network events trigger NetworkInspect to read labels.
//
// Publishes a single DockerEvent envelope for managed networks;
// subscribers filter on ev.Type == NetworkEventType + ev.Action.
// Connect/Disconnect with no managed container anchor drop — the
// worldview has nothing to attribute the edge to.
func (f *Feeder) dispatchNetwork(ctx context.Context, ev events.Message) {
	netID := ev.Actor.ID
	if netID == "" {
		return
	}

	switch ev.Action {
	case events.ActionCreate:
		f.tryNetworkInspect(ctx, netID)
	case events.ActionDestroy, events.ActionRemove:
		// Drop any pending recheck flag — a destroyed network can
		// never become managed retroactively.
		delete(f.networksNeedRecheck, netID)
		// Note: don't drop f.networks[netID] until after the publish
		// below so a previously-managed destroy still propagates.
	case events.ActionConnect, events.ActionDisconnect:
		// If the initial Create-time inspect failed for this network,
		// retry now — the connect/disconnect event proves the network
		// is alive in Docker, so the inspect is more likely to
		// succeed.
		if f.networksNeedRecheck[netID] {
			f.tryNetworkInspect(ctx, netID)
		}
	}

	if !f.networks[netID] {
		return
	}

	if ev.Action == events.ActionConnect || ev.Action == events.ActionDisconnect {
		ctrID := ev.Actor.Attributes["container"]
		if ctrID == "" || !f.containers[ctrID] {
			return
		}
	}

	f.publishDockerEvent(DockerEvent{Message: ev}, ev.Actor.Attributes["container"])

	// Drop the destroyed network from managed-set after publish so
	// downstream connect/disconnect events can't retroactively reattach.
	if ev.Action == events.ActionDestroy || ev.Action == events.ActionRemove {
		delete(f.networks, netID)
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

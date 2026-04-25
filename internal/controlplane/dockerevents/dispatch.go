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

	"github.com/schmitthub/clawker/internal/controlplane/informer"
)

// dispatch routes a single Docker event into informer calls. dispatch
// runs on Run's single goroutine; managed-set mutations are
// goroutine-local and need no lock.
//
// Logging contract:
//   - `docker event received` — fires for every actionable message
//     read off the events stream, with the full event schema attached
//     as structured fields. Operator UAT view: "what did Docker tell
//     us right now."
//   - `informer published` — fires AFTER each informer write
//     (Upsert/Patch/Remove/Link/Unlink), describing exactly what
//     reached the realm model. Owned by the informer package, not
//     this one. Operator UAT view: "what did we record."
//
// The receive log lives upstream of the action allowlist so noisy
// non-state events (exec_*, healthcheck probes, etc) don't dominate
// Loki. A missing publish log under a receive line means the dispatch
// path filtered it; a missing receive line means Docker never sent it
// (or our type-filter rejected it on the wire).
func (f *Feeder) dispatch(ctx context.Context, ev events.Message) {
	if !shouldHandleAction(ev) {
		return
	}

	f.logEventReceived(ev)

	switch ev.Type {
	case events.ContainerEventType:
		f.dispatchContainer(ctx, ev)
	case events.NetworkEventType:
		f.dispatchNetwork(ctx, ev)
	case events.VolumeEventType:
		f.dispatchVolume(ctx, ev)
	case events.ImageEventType:
		f.dispatchImage(ctx, ev)
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

// publish helpers thinly proxy the informer write API and absorb
// expected shutdown errors so call sites stay terse. The publish log
// itself is owned by the informer (informer.logPublished) so every
// feeder gets it for free. Real failures (anything other than
// ctx.Canceled / informer.ErrClosed) surface as warn-level structured
// logs — silently dropping them would lie about the publish trail.
func (f *Feeder) publishUpsert(ctx context.Context, u informer.ResourceUpdate, t informer.Transition) {
	f.noteWriteErr(f.inf.Upsert(ctx, u, t), u.Kind, u.ID)
}

func (f *Feeder) publishRemove(ctx context.Context, key informer.Key, t informer.Transition) {
	f.noteWriteErr(f.inf.Remove(ctx, key, t), key.Kind, key.ID)
}

func (f *Feeder) publishLink(ctx context.Context, rel informer.Relation) {
	f.noteWriteErr(f.inf.LinkRelation(ctx, rel), rel.Kind, rel.From.ID+"->"+rel.To.ID)
}

func (f *Feeder) publishUnlink(ctx context.Context, from, to informer.Key, kind string) {
	f.noteWriteErr(f.inf.UnlinkRelation(ctx, from, to, kind), kind, from.ID+"->"+to.ID)
}

// noteWriteErr classifies an informer write error. Shutdown-class
// errors (ctx.Canceled, informer.ErrClosed) are silent — the drain
// path expects in-flight writes to land on a closing informer.
// informer.ErrNotStarted is a wiring bug (Run started before Start)
// and surfaces at error level. Everything else lands at warn with
// kind+id for triage.
func (f *Feeder) noteWriteErr(err error, kind, id string) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, informer.ErrClosed) {
		return
	}
	if errors.Is(err, informer.ErrNotStarted) {
		f.log.Error().Err(err).Str("kind", kind).Str("id", id).Msg("informer not started: feeder running before informer.Start")
		return
	}
	f.log.Warn().Err(err).Str("kind", kind).Str("id", id).Msg("informer write failed")
}

// shouldHandleAction filters out diagnostic / high-volume actions that
// have no realm-state value. Returns true if the event should reach
// the per-type dispatcher.
func shouldHandleAction(ev events.Message) bool {
	a := string(ev.Action)
	switch {
	case strings.HasPrefix(a, "exec_"):
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
func (f *Feeder) dispatchContainer(ctx context.Context, ev events.Message) {
	id := ev.Actor.ID
	if id == "" {
		return
	}

	managed := f.isManaged(ev.Actor.Attributes)
	if !managed && !f.containers[id] {
		return
	}

	labels := stripEngineKeys(ev.Actor.Attributes, "image", "name", "exitCode")

	t := informer.Transition{
		Source: transitionSource,
		Verb:   verbPrefix + "container." + string(ev.Action),
		At:     time.Unix(0, ev.TimeNano),
		Attrs:  map[string]string{"id": id},
	}

	switch ev.Action {
	case events.ActionDestroy, events.ActionRemove:
		delete(f.containers, id)
		f.publishRemove(ctx, informer.Key{Kind: KindContainer, ID: id}, t)
		return
	}

	lifecycle := containerLifecycleFromAction(ev.Action)
	attrs := map[string]string{}
	if img, ok := ev.Actor.Attributes["image"]; ok {
		attrs["image"] = img
	}
	if name, ok := ev.Actor.Attributes["name"]; ok {
		attrs["name"] = name
	}
	if code, ok := ev.Actor.Attributes["exitCode"]; ok {
		attrs["exit_code"] = code
	}
	if ev.Action == events.ActionOOM {
		attrs["oom"] = "true"
	}
	if hs, ok := healthStatusFrom(ev.Action); ok {
		attrs["health"] = hs
	}

	f.containers[id] = true
	f.publishUpsert(ctx, informer.ResourceUpdate{
		Kind:      KindContainer,
		ID:        id,
		Labels:    labels,
		Attrs:     attrs,
		Lifecycle: lifecycle,
	}, t)
}

// dispatchNetwork handles network events. Network actor Attributes do
// NOT carry network labels (verified vs moby
// daemon/events.go::LogNetworkEventWithAttributes), so first-sight
// network events trigger NetworkInspect to read labels.
func (f *Feeder) dispatchNetwork(ctx context.Context, ev events.Message) {
	netID := ev.Actor.ID
	if netID == "" {
		return
	}

	t := informer.Transition{
		Source: transitionSource,
		Verb:   verbPrefix + "network." + string(ev.Action),
		At:     time.Unix(0, ev.TimeNano),
		Attrs:  map[string]string{"network_id": netID},
	}

	switch ev.Action {
	case events.ActionCreate:
		// Inspect to learn whether this network is managed.
		res, err := f.cli.NetworkInspect(ctx, netID, client.NetworkInspectOptions{})
		if err != nil {
			f.log.Warn().Err(err).Str("network_id", short(netID)).Msg("network inspect failed; skipping create")
			return
		}
		if !f.isManaged(res.Network.Labels) {
			return
		}
		f.networks[netID] = true
		f.publishUpsert(ctx, informer.ResourceUpdate{
			Kind:   KindNetwork,
			ID:     netID,
			Labels: res.Network.Labels,
			Attrs: map[string]string{
				"name":   res.Network.Name,
				"driver": res.Network.Driver,
				"scope":  res.Network.Scope,
			},
			Lifecycle: informer.LifecycleLive,
		}, t)

	case events.ActionDestroy, events.ActionRemove:
		if !f.networks[netID] {
			return
		}
		delete(f.networks, netID)
		f.publishRemove(ctx, informer.Key{Kind: KindNetwork, ID: netID}, t)

	case events.ActionConnect, events.ActionDisconnect:
		if !f.networks[netID] {
			return
		}
		ctrID := ev.Actor.Attributes["container"]
		if ctrID == "" {
			return
		}
		if !f.containers[ctrID] {
			// Wait for the container event so relation lifetime tracks
			// the container's; informer would accept the orphan edge.
			return
		}
		from := informer.Key{Kind: KindContainer, ID: ctrID}
		to := informer.Key{Kind: KindNetwork, ID: netID}
		if ev.Action == events.ActionConnect {
			f.publishLink(ctx, informer.Relation{From: from, To: to, Kind: RelationAttachedTo})
		} else {
			f.publishUnlink(ctx, from, to, RelationAttachedTo)
		}
	}
}

// dispatchVolume handles volume events. Volume create events carry
// labels via moby's LogVolumeEvent; destroy events do NOT reliably
// carry user labels (especially via VolumesPrune), so destroys are
// gated on the in-memory volumeSet rather than re-checking labels.
// Mirrors the container path: untracked + unmanaged → drop silently.
func (f *Feeder) dispatchVolume(ctx context.Context, ev events.Message) {
	name := ev.Actor.ID
	if name == "" {
		return
	}

	managed := f.isManaged(ev.Actor.Attributes)
	if !managed && !f.volumes[name] {
		return
	}

	t := informer.Transition{
		Source: transitionSource,
		Verb:   verbPrefix + "volume." + string(ev.Action),
		At:     time.Unix(0, ev.TimeNano),
		Attrs:  map[string]string{"volume": name},
	}

	switch ev.Action {
	case events.ActionDestroy, events.ActionRemove:
		delete(f.volumes, name)
		f.publishRemove(ctx, informer.Key{Kind: KindVolume, ID: name}, t)
	default:
		f.volumes[name] = true
		f.publishUpsert(ctx, informer.ResourceUpdate{
			Kind:      KindVolume,
			ID:        name,
			Labels:    stripEngineKeys(ev.Actor.Attributes),
			Lifecycle: informer.LifecycleLive,
		}, t)
	}
}

// dispatchImage handles image events. Image actor Attributes carry
// image labels via LogImageEvent (moby
// daemon/images/image_events.go); engine-set keys are "name" and
// sometimes "tag".
func (f *Feeder) dispatchImage(ctx context.Context, ev events.Message) {
	id := ev.Actor.ID
	if id == "" {
		return
	}

	managed := f.isManaged(ev.Actor.Attributes)
	if !managed && !f.images[id] {
		return
	}

	t := informer.Transition{
		Source: transitionSource,
		Verb:   verbPrefix + "image." + string(ev.Action),
		At:     time.Unix(0, ev.TimeNano),
		Attrs:  map[string]string{"image": id},
	}

	switch ev.Action {
	case events.ActionDelete, events.ActionRemove:
		delete(f.images, id)
		f.publishRemove(ctx, informer.Key{Kind: KindImage, ID: id}, t)
	default:
		labels := stripEngineKeys(ev.Actor.Attributes, "name", "tag")
		attrs := map[string]string{}
		if name, ok := ev.Actor.Attributes["name"]; ok {
			attrs["name"] = name
		}
		f.images[id] = true
		f.publishUpsert(ctx, informer.ResourceUpdate{
			Kind:      KindImage,
			ID:        id,
			Labels:    labels,
			Attrs:     attrs,
			Lifecycle: informer.LifecycleLive,
		}, t)
	}
}

// containerLifecycleFromAction maps Docker actions to clawker
// lifecycle markers. Verbs not listed leave lifecycle unchanged
// (returns "").
func containerLifecycleFromAction(a events.Action) string {
	switch a {
	case events.ActionCreate:
		return "created"
	case events.ActionStart, events.ActionRestart, events.ActionUnPause:
		return "running"
	case events.ActionPause:
		return "paused"
	case events.ActionDie, events.ActionStop, events.ActionKill:
		return "stopped"
	}
	return ""
}

// containerLifecycleFromState maps a moby ContainerState to clawker
// lifecycle. Used by reconcile, where we read state from
// ContainerList summaries.
func containerLifecycleFromState(s container.ContainerState) string {
	switch s {
	case container.StateCreated:
		return "created"
	case container.StateRunning:
		return "running"
	case container.StatePaused:
		return "paused"
	case container.StateRestarting:
		return "restarting"
	case container.StateExited, container.StateDead, container.StateRemoving:
		return "stopped"
	}
	return ""
}

// containerAttrsFromSummary mirrors the engine-set keys we record on
// container resources so reconcile and event dispatch produce
// equivalent attribute maps.
func containerAttrsFromSummary(c container.Summary) map[string]string {
	attrs := map[string]string{
		"image":    c.Image,
		"image_id": c.ImageID,
	}
	if len(c.Names) > 0 {
		// Docker prefixes names with '/'; strip for ergonomics.
		attrs["name"] = strings.TrimPrefix(c.Names[0], "/")
	}
	return attrs
}

// healthStatusFrom maps an event action that carries a colon-prefixed
// health verdict ("health_status: healthy") to the trailing token.
// Returns ("", false) for non-health actions.
func healthStatusFrom(a events.Action) (string, bool) {
	s := string(a)
	if rest, ok := strings.CutPrefix(s, "health_status: "); ok {
		return rest, true
	}
	if s == string(events.ActionHealthStatus) {
		return "unknown", true
	}
	return "", false
}

// stripEngineKeys returns a copy of attrs with the listed engine-set
// keys removed. Engine-set keys live alongside user labels in
// Actor.Attributes; clawker's Resource.Labels should hold only true
// labels.
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

func joinTags(tags []string) string {
	return strings.Join(tags, ",")
}

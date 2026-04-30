package dockerevents

import (
	"strconv"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/rs/zerolog"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

// Event types published by the dockerevents feeder. Each moby
// container action gets its own Go type — no buckets, no fan-in. The
// dispatch layer is a transparent translation of moby's event
// vocabulary into typed bus messages so consumers can subscribe to
// the exact action they care about (start vs restart vs unpause vs
// create are all genuinely distinct lifecycle transitions, even when
// today's only consumer happens to react to the union).
//
// Composition strategy:
//
//   - ContainerEvent embeds moby's events.Message verbatim. The bus
//     envelope is a thin Go-typed wrapper around docker truth — no
//     parallel field schema to keep in sync, no engine-key vs
//     user-label flattening at the producer side. Consumers reach
//     through to Actor.Attributes (labels + engine-set keys),
//     Actor.ID (container id), Action, TimeNano as needed.
//   - Per-action types embed ContainerEvent. They contribute exactly
//     one thing the embedded base can't: a distinct reflect.TypeOf so
//     overseer.Subscribe[ContainerStarted] type-routes correctly.
//   - Action-specific accessors are method shims on the per-action
//     type (ExitCode, Signal, OldName) — they read the moby
//     Attributes on demand instead of duplicating fields into Go.
//
// NetworkEvent / per-action network types follow the same pattern.

// ContainerEvent is the bus envelope for any container action. Wraps
// moby's events.Message verbatim — Actor.ID, Actor.Attributes
// (labels + engine-set keys), Action, TimeNano are all available
// through promoted fields.
type ContainerEvent struct {
	events.Message
}

// OccurredAt converts moby's int64 nanosecond timestamp to time.Time.
// Promoted to every per-action subtype.
func (e ContainerEvent) OccurredAt() time.Time { return time.Unix(0, e.TimeNano) }

// MarshalZerologObject emits the canonical container-event log
// payload (id, name, image, action). Promoted to every subtype;
// subtypes that carry action-specific fields override and chain into
// this base. Subscribers reach through to the embedded moby Message
// for any other field — Actor.ID, Actor.Attributes, Action,
// TimeNano are all available directly. No accessor shims, because
// the moby Message IS the contract.
func (e ContainerEvent) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.Actor.ID).
		Str("name", e.Actor.Attributes["name"]).
		Str("image", e.Actor.Attributes["image"]).
		Str("action", string(e.Action))
}

// Labels returns a copy of the container's labels with moby's
// engine-set keys (image, name, exitCode, signal, oldName) stripped.
// Engine-set keys live alongside user labels in Actor.Attributes;
// callers reaching for "just the labels" filter them out. Allocated
// on demand — the embedded Attributes map remains untouched.
func (e ContainerEvent) Labels() map[string]string {
	return stripEngineKeys(e.Actor.Attributes,
		"image", "name", "exitCode", "signal", "oldName")
}

// ContainerCreated fires when moby reports action=create. Container
// is created but not running.
type ContainerCreated struct{ ContainerEvent }

func (e ContainerCreated) EventName() string { return "docker.container.created" }

// ContainerStarted fires when moby reports action=start. Container
// transitioned to running. NOT fired for create alone — a created
// container that never starts produces no ContainerStarted.
type ContainerStarted struct{ ContainerEvent }

func (e ContainerStarted) EventName() string { return "docker.container.started" }

func (e ContainerStarted) ApplyTo(s *overseer.State) {
	view := s.Containers[e.Actor.ID]
	view.ID = e.Actor.ID
	if name := e.Actor.Attributes["name"]; name != "" {
		view.Name = name
	}
	view.Status = overseer.ContainerStatusRunning
	view.Labels = e.Labels()
	view.UpdatedAt = e.OccurredAt()
	s.Containers[e.Actor.ID] = view
}

// ContainerRestarted fires when moby reports action=restart — an
// atomic stop+start. Distinct from a fresh start because the
// container regained running state via the restart path; consumers
// that care about restart count or restart-loop detection key on
// this type.
type ContainerRestarted struct{ ContainerEvent }

func (e ContainerRestarted) EventName() string { return "docker.container.restarted" }

func (e ContainerRestarted) ApplyTo(s *overseer.State) {
	view := s.Containers[e.Actor.ID]
	view.ID = e.Actor.ID
	if name := e.Actor.Attributes["name"]; name != "" {
		view.Name = name
	}
	view.Status = overseer.ContainerStatusRunning
	view.Labels = e.Labels()
	view.UpdatedAt = e.OccurredAt()
	s.Containers[e.Actor.ID] = view
}

// ContainerPaused fires when moby reports action=pause. Container
// processes are frozen but the container still exists.
type ContainerPaused struct{ ContainerEvent }

func (e ContainerPaused) EventName() string { return "docker.container.paused" }

// ContainerUnpaused fires when moby reports action=unpause. Frozen
// processes resumed. State.Containers status returns to running.
type ContainerUnpaused struct{ ContainerEvent }

func (e ContainerUnpaused) EventName() string { return "docker.container.unpaused" }

func (e ContainerUnpaused) ApplyTo(s *overseer.State) {
	view := s.Containers[e.Actor.ID]
	view.ID = e.Actor.ID
	view.Status = overseer.ContainerStatusRunning
	view.UpdatedAt = e.OccurredAt()
	s.Containers[e.Actor.ID] = view
}

// ContainerDied fires when moby reports action=die. Container's main
// process exited. ExitCode carries the exit status.
type ContainerDied struct{ ContainerEvent }

func (e ContainerDied) EventName() string { return "docker.container.died" }

// ExitCode reads moby's stringly-typed exit code from Actor.Attributes
// and parses to int32. Returns 0 on missing/malformed input.
func (e ContainerDied) ExitCode() int32 { return parseExitCodeAttr(e.Actor.Attributes["exitCode"]) }

func (e ContainerDied) MarshalZerologObject(z *zerolog.Event) {
	e.ContainerEvent.MarshalZerologObject(z)
	z.Int32("exit_code", e.ExitCode())
}

func (e ContainerDied) ApplyTo(s *overseer.State) {
	view := s.Containers[e.Actor.ID]
	view.ID = e.Actor.ID
	view.Status = overseer.ContainerStatusStopped
	view.UpdatedAt = e.OccurredAt()
	s.Containers[e.Actor.ID] = view
}

// ContainerStopped fires when moby reports action=stop — explicit
// `docker stop` (or equivalent API call). The daemon sends SIGTERM
// then SIGKILL after the stop timeout; this event marks the user-
// initiated stop, distinct from ContainerDied which is the process-
// exit observation.
type ContainerStopped struct{ ContainerEvent }

func (e ContainerStopped) EventName() string { return "docker.container.stopped" }

func (e ContainerStopped) ExitCode() int32 {
	return parseExitCodeAttr(e.Actor.Attributes["exitCode"])
}

func (e ContainerStopped) MarshalZerologObject(z *zerolog.Event) {
	e.ContainerEvent.MarshalZerologObject(z)
	z.Int32("exit_code", e.ExitCode())
}

func (e ContainerStopped) ApplyTo(s *overseer.State) {
	view := s.Containers[e.Actor.ID]
	view.ID = e.Actor.ID
	view.Status = overseer.ContainerStatusStopped
	view.UpdatedAt = e.OccurredAt()
	s.Containers[e.Actor.ID] = view
}

// ContainerKilled fires when moby reports action=kill — explicit
// `docker kill`. Signal carries the signal name (SIGKILL, SIGTERM,
// custom).
type ContainerKilled struct{ ContainerEvent }

func (e ContainerKilled) EventName() string { return "docker.container.killed" }

func (e ContainerKilled) Signal() string { return e.Actor.Attributes["signal"] }

func (e ContainerKilled) MarshalZerologObject(z *zerolog.Event) {
	e.ContainerEvent.MarshalZerologObject(z)
	z.Str("signal", e.Signal())
}

// ContainerOOM fires when moby reports action=oom. The kernel killed
// the container's main process due to OOM. May fire alongside or
// instead of die — consumers must dedup if they care about a single
// "container terminated" notification.
type ContainerOOM struct{ ContainerEvent }

func (e ContainerOOM) EventName() string { return "docker.container.oom" }

func (e ContainerOOM) ApplyTo(s *overseer.State) {
	view := s.Containers[e.Actor.ID]
	view.ID = e.Actor.ID
	view.Status = overseer.ContainerStatusStopped
	view.UpdatedAt = e.OccurredAt()
	s.Containers[e.Actor.ID] = view
}

// ContainerDestroyed fires when moby reports action=destroy. The
// container has been removed from the daemon — its ID is no longer
// resolvable. Subscribers (agentregistry) treat this as the eviction
// signal.
type ContainerDestroyed struct{ ContainerEvent }

func (e ContainerDestroyed) EventName() string { return "docker.container.destroyed" }

func (e ContainerDestroyed) ApplyTo(s *overseer.State) {
	delete(s.Containers, e.Actor.ID)
}

// ContainerRemoved fires when moby reports action=remove — the
// daemon's removal step (file system + state cleanup). Distinct from
// destroy in moby's event stream; in practice both fire for a
// removal so consumers may pick either.
type ContainerRemoved struct{ ContainerEvent }

func (e ContainerRemoved) EventName() string { return "docker.container.removed" }

func (e ContainerRemoved) ApplyTo(s *overseer.State) {
	delete(s.Containers, e.Actor.ID)
}

// ContainerRenamed fires when moby reports action=rename. OldName
// and NewName are exposed via accessors reading Actor.Attributes
// (oldName, name).
type ContainerRenamed struct{ ContainerEvent }

func (e ContainerRenamed) EventName() string { return "docker.container.renamed" }

// OldName / NewName accessors. moby uses the `oldName` engine key
// and reuses `name` for the new value.
func (e ContainerRenamed) OldName() string { return e.Actor.Attributes["oldName"] }
func (e ContainerRenamed) NewName() string { return e.Actor.Attributes["name"] }

func (e ContainerRenamed) MarshalZerologObject(z *zerolog.Event) {
	e.ContainerEvent.MarshalZerologObject(z)
	z.Str("old_name", e.OldName()).Str("new_name", e.NewName())
}

func (e ContainerRenamed) ApplyTo(s *overseer.State) {
	view := s.Containers[e.Actor.ID]
	view.ID = e.Actor.ID
	view.Name = e.NewName()
	view.UpdatedAt = e.OccurredAt()
	s.Containers[e.Actor.ID] = view
}

// NetworkEvent is the bus envelope for any network action. Same
// composition strategy as ContainerEvent — embeds moby's
// events.Message and provides accessors for fields specific to
// network events. Unlike container events, moby does NOT carry the
// network's labels in Actor.Attributes (verified vs moby
// daemon/events.go::LogNetworkEventWithAttributes), so the feeder
// pre-resolves "is this a managed network" via NetworkInspect at
// dispatch time and only publishes events for managed networks.
type NetworkEvent struct {
	events.Message
}

func (e NetworkEvent) OccurredAt() time.Time { return time.Unix(0, e.TimeNano) }

// MarshalZerologObject emits the canonical network-event log
// payload. Subscribers reach through to Actor.ID for the network ID,
// Actor.Attributes["name"] for the network name, and
// Actor.Attributes["container"] for the connected/disconnected
// container ID on connect/disconnect events.
func (e NetworkEvent) MarshalZerologObject(z *zerolog.Event) {
	z.Str("network_id", e.Actor.ID).
		Str("action", string(e.Action))
	if name := e.Actor.Attributes["name"]; name != "" {
		z.Str("network_name", name)
	}
	if cid := e.Actor.Attributes["container"]; cid != "" {
		z.Str("container_id", cid)
	}
}

// NetworkCreated fires when moby reports action=create on a managed
// network.
type NetworkCreated struct{ NetworkEvent }

func (e NetworkCreated) EventName() string { return "docker.network.created" }

// NetworkDestroyed fires when moby reports action=destroy on a
// managed network.
type NetworkDestroyed struct{ NetworkEvent }

func (e NetworkDestroyed) EventName() string { return "docker.network.destroyed" }

// NetworkConnected fires when moby reports action=connect — a
// container attached to a managed network.
type NetworkConnected struct{ NetworkEvent }

func (e NetworkConnected) EventName() string { return "docker.network.connected" }

// NetworkDisconnected fires when moby reports action=disconnect — a
// container detached from a managed network.
type NetworkDisconnected struct{ NetworkEvent }

func (e NetworkDisconnected) EventName() string { return "docker.network.disconnected" }

// stripEngineKeys returns a copy of attrs with the listed engine-set
// keys removed. Engine-set keys live alongside user labels in
// Actor.Attributes; callers reaching for "just the labels" filter
// them out.
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

// parseExitCodeAttr coerces moby's stringly-typed exitCode attribute
// to int32. Empty / malformed input yields 0 silently — accessor
// callers operate at the consumer layer and have no good place to
// surface a debug log; the dispatch boundary already audits exit
// code parse failures via Feeder.parseExitCode.
func parseExitCodeAttr(raw string) int32 {
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0
	}
	return int32(n)
}

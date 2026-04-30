package dockerevents

import (
	"time"

	"github.com/rs/zerolog"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

// Event types published by the dockerevents feeder. Each type is its
// own Go struct in this package — no shared "Lifecycle" string field
// is in scope here, so consumers and producers cannot collide on a
// shared vocabulary the way they did under the informer's
// ResourceUpdate (Y5).
//
// Container lifecycle decomposes into three terminal-or-transient
// events: Started (running), Stopped (died/stopped/killed), Removed
// (destroyed/removed from the daemon). Pause/resume are not modelled
// in v1 — agentregistry.Subscribe doesn't care about pause; if a
// consumer ever does, add ContainerPaused/Resumed.
//
// Network changes decompose into edge events only: NetworkAttached
// and NetworkDetached. Network create/destroy events are handled
// internally by the feeder (managed-set bookkeeping for filtering)
// but not republished — Overseer's worldview tracks containers and
// sessions, not networks as standalone entities.

// ContainerStarted fires when a managed container enters the running
// state — Docker create/start/restart/unpause actions, or a reconcile
// pass observing a container in StateRunning.
type ContainerStarted struct {
	ID     string
	Name   string
	Image  string
	Labels map[string]string
	At     time.Time
}

// EventName returns the canonical name for log lines.
func (e ContainerStarted) EventName() string { return "docker.container.started" }

// OccurredAt returns the event time.
func (e ContainerStarted) OccurredAt() time.Time { return e.At }

// MarshalZerologObject emits the type-specific log payload (container
// id/name/image) so NewLoggerHook lines carry the identity that made
// the event meaningful.
func (e ContainerStarted) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ID).
		Str("name", e.Name).
		Str("image", e.Image)
}

// ApplyTo updates the Overseer worldview to reflect this container as
// running. Labels are deep-copied so mutating the event's map after
// publish does not affect state.
func (e ContainerStarted) ApplyTo(s *overseer.State) {
	view := s.Containers[e.ID]
	view.ID = e.ID
	if e.Name != "" {
		view.Name = e.Name
	}
	view.Status = overseer.ContainerStatusRunning
	if e.Labels != nil {
		view.Labels = copyLabels(e.Labels)
	}
	view.UpdatedAt = e.At
	s.Containers[e.ID] = view
}

// ContainerStopped fires when a managed container leaves the running
// state without being removed — Docker die/stop/kill actions, or a
// reconcile pass observing exited/dead/removing.
type ContainerStopped struct {
	ID string
	// ExitCode is the parsed numeric exit code from moby's event Actor
	// attributes ("exitCode" key, reported as string on the wire). The
	// dispatch boundary parses once with strconv.ParseInt; consumers
	// receive a typed value rather than re-parsing per subscriber.
	// Zero on missing or unparseable input — dispatch logs the parse
	// failure at Debug so a moby contract change surfaces in logs
	// without breaking the dispatch loop.
	ExitCode int32
	OOM      bool
	At       time.Time
}

func (e ContainerStopped) EventName() string     { return "docker.container.stopped" }
func (e ContainerStopped) OccurredAt() time.Time { return e.At }
func (e ContainerStopped) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ID).
		Int32("exit_code", e.ExitCode).
		Bool("oom", e.OOM)
}
func (e ContainerStopped) ApplyTo(s *overseer.State) {
	view := s.Containers[e.ID]
	view.ID = e.ID
	view.Status = overseer.ContainerStatusStopped
	view.UpdatedAt = e.At
	s.Containers[e.ID] = view
}

// ContainerRemoved fires when a managed container is destroyed or
// removed from the daemon. Subscribers (agentregistry) treat this as
// the eviction signal.
type ContainerRemoved struct {
	ID string
	At time.Time
}

func (e ContainerRemoved) EventName() string     { return "docker.container.removed" }
func (e ContainerRemoved) OccurredAt() time.Time { return e.At }
func (e ContainerRemoved) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ID)
}
func (e ContainerRemoved) ApplyTo(s *overseer.State) {
	delete(s.Containers, e.ID)
}

// NetworkAttached fires when a managed container connects to a
// managed network. Both endpoints must be managed for the edge to
// publish — otherwise the event is suppressed at the dispatch boundary.
type NetworkAttached struct {
	ContainerID string
	NetworkID   string
	NetworkName string
	At          time.Time
}

func (e NetworkAttached) EventName() string     { return "docker.network.attached" }
func (e NetworkAttached) OccurredAt() time.Time { return e.At }
func (e NetworkAttached) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("network_id", e.NetworkID).
		Str("network_name", e.NetworkName)
}

// NetworkDetached fires when a managed container disconnects from a
// managed network.
type NetworkDetached struct {
	ContainerID string
	NetworkID   string
	At          time.Time
}

func (e NetworkDetached) EventName() string     { return "docker.network.detached" }
func (e NetworkDetached) OccurredAt() time.Time { return e.At }
func (e NetworkDetached) MarshalZerologObject(z *zerolog.Event) {
	z.Str("container_id", e.ContainerID).
		Str("network_id", e.NetworkID)
}

// copyLabels returns nil for nil, otherwise a fresh map with the same
// key/value pairs. Used by ApplyTo to keep the worldview from sharing
// pointers with caller-owned maps.
func copyLabels(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

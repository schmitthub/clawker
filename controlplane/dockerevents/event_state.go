package dockerevents

import (
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
)

// Status is the lifecycle of a container as observed by the
// dockerevents feeder. Distinct from the agent's session/registration
// axis — container lifecycle is "is the docker container running?",
// not "has CP attested its identity?".
type Status string

const (
	StatusUnknown Status = ""
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
)

// ContainerView is the Overseer's in-memory worldview of one container.
// Populated and mutated exclusively by dockerevents events implementing
// the unexported applier interface. Removed entirely when the container
// is destroyed (no soft-delete).
type ContainerView struct {
	ID        string
	Name      string
	Status    Status
	Labels    map[string]string
	UpdatedAt time.Time
}

const StoreKey = "containers"

type ContainerStore struct{ byID map[string]ContainerView }

func (s *ContainerStore) Clone() overseer.Projection { /* deep copy byID + Labels */ }

func Reduce(s *ContainerStore, e DockerEvent) {
	if e.Type != events.ContainerEventType {
		return
	}
	switch e.Action {
	case events.ActionStart, events.ActionRestart, events.ActionUnPause:
		view := s.byID[e.Actor.ID]
		view.ID = e.Actor.ID
		if name := e.Actor.Attributes["name"]; name != "" {
			view.Name = name
		}
		view.Status = StatusRunning
		view.Labels = stripEngineKeys(e.Actor.Attributes,
			"image", "name", "exitCode", "signal", "oldName", "execDuration")
		view.UpdatedAt = e.OccurredAt()
		s.byID[e.Actor.ID] = view

	case events.ActionDie, events.ActionStop, events.ActionKill, events.ActionOOM:
		view := s.byID[e.Actor.ID]
		view.ID = e.Actor.ID
		view.Status = StatusStopped
		view.UpdatedAt = e.OccurredAt()
		s.byID[e.Actor.ID] = view

	case events.ActionDestroy:
		// moby fires `destroy` for `docker rm` (verified vs live
		// stream — zero `container/remove` actions observed).
		// `events.ActionRemove` exists in the shared Action vocabulary
		// but is image-only (`docker rmi`) and never reaches this
		// switch for container events. ApplyTo is a projection, not
		// the wire vocabulary, so it MUST NOT branch on ActionRemove.
		delete(s.byID, e.Actor.ID)

	case events.ActionRename:
		view := s.byID[e.Actor.ID]
		view.ID = e.Actor.ID
		view.Name = e.Actor.Attributes["name"]
		view.UpdatedAt = e.OccurredAt()
		s.byID[e.Actor.ID] = view

		// Created / Paused / Unpaused-as-pure-edge and any unrecognised
		// action: pure pub/sub, no State change.
	}
}

// so consumers never type the raw key or assert by hand
func From(s overseer.Snapshot) (*ContainerStore, bool) {
	return overseer.Get[*ContainerStore](s, StoreKey)
}

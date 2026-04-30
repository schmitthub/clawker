package dockerevents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// reconcile rebuilds the managed-container and managed-network sets
// from authoritative Docker state and re-publishes the single
// DockerEvent envelope for every managed container plus a
// network/connect envelope for every container→managed-network edge.
// Called at startup and after every events stream reset.
//
// Reconcile is idempotent against the bus: ApplyTo on the embedded
// (Type, Action) pair sets Status by overwrite, not insert-only —
// re-publish on top of existing state is safe. Network events have
// no state projection in v1.
//
// Reconcile does NOT publish a destroy envelope for objects that were
// seen on a previous pass but are missing now. Removals come from the
// events stream — between the last list and the current one, those
// removals were either delivered as events (handled in dispatch) or
// will be replayed on the next stream open via Since= timestamp.
func (f *Feeder) reconcile(ctx context.Context) error {
	managedFilter := client.Filters{}.Add("label", f.opts.ManagedLabelKey+"="+f.opts.ManagedLabelValue)

	containers, cErr := f.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: managedFilter,
	})
	networks, nErr := f.cli.NetworkList(ctx, client.NetworkListOptions{Filters: managedFilter})

	if err := errors.Join(cErr, nErr); err != nil {
		return fmt.Errorf("dockerevents: list during reconcile: %w", err)
	}

	// Rebuild the managed-sets fresh. Stream deliveries that arrive
	// between this rebuild and runStream taking over are guarded by
	// the Since= anchor — they will redeliver and pass through
	// dispatch which patches the sets.
	f.containers = make(map[string]bool, len(containers.Items))
	f.networks = make(map[string]bool, len(networks.Items))

	now := time.Now()

	// Containers first so the network-attachment loop can gate edges
	// on f.containers membership.
	for _, c := range containers.Items {
		f.containers[c.ID] = true
		action := containerActionFromState(c.State)
		if action == "" {
			// StateCreated / StateRestarting → no synthetic publish;
			// the next real moby action will redrive when the
			// container transitions.
			continue
		}
		envelope := DockerEvent{Message: events.Message{
			Type:   events.ContainerEventType,
			Action: action,
			Actor: events.Actor{
				ID:         c.ID,
				Attributes: containerAttributesFromSummary(c),
			},
			Scope:    "local",
			Time:     now.Unix(),
			TimeNano: now.UnixNano(),
		}}
		f.publishDockerEvent(envelope, c.ID)
	}

	for _, n := range networks.Items {
		f.networks[n.ID] = true
	}

	// Container→network edges. Orphan edges to unmanaged networks
	// add no value, so we gate on f.networks (fully populated above).
	// Reconcile synthesizes a network/connect envelope so subscribers
	// can't tell stream-delivered from observed events apart.
	for _, c := range containers.Items {
		if c.NetworkSettings == nil {
			continue
		}
		for _, ep := range c.NetworkSettings.Networks {
			if ep == nil || ep.NetworkID == "" {
				continue
			}
			if !f.networks[ep.NetworkID] {
				continue
			}
			envelope := DockerEvent{Message: events.Message{
				Type:   events.NetworkEventType,
				Action: events.ActionConnect,
				Actor: events.Actor{
					ID: ep.NetworkID,
					Attributes: map[string]string{
						"container": c.ID,
					},
				},
				Scope:    "local",
				Time:     now.Unix(),
				TimeNano: now.UnixNano(),
			}}
			f.publishDockerEvent(envelope, c.ID)
		}
	}

	f.log.Info().
		Int("containers", len(f.containers)).
		Int("networks", len(f.networks)).
		Msg("reconcile complete")

	return nil
}

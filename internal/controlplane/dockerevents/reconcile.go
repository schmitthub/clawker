package dockerevents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moby/moby/client"
)

// reconcile rebuilds the managed-container and managed-network sets
// from authoritative Docker state and re-publishes a typed lifecycle
// event for every managed container plus a NetworkAttached event for
// every container→managed-network edge. Called at startup and after
// every events stream reset.
//
// Reconcile is idempotent against the bus:
//   - ContainerStarted/Stopped applier hooks set Status by overwrite,
//     not insert-only — re-publish on top of existing state is safe.
//   - NetworkAttached has no applier hook in v1 (Overseer doesn't
//     project network edges into State); subscribers are responsible
//     for their own dedup if they care.
//
// Reconcile does NOT publish ContainerRemoved for objects that were
// seen on a previous pass but are missing now. Removals come from the
// events stream — between the last list and the current one, those
// removals were either delivered as events (handled in dispatch) or
// will be replayed on the next stream open via Since= timestamp.
//
// Volume/image listing was dropped along with their event publishes;
// Overseer doesn't track them and the listing roundtrip was pure
// overhead.
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
	// the Since= anchor — they will redeliver and pass through dispatch
	// which patches the sets.
	f.containers = make(map[string]bool, len(containers.Items))
	f.networks = make(map[string]bool, len(networks.Items))

	now := time.Now()

	// Containers first so the network-attachment loop can gate edges
	// on f.containers membership.
	for _, c := range containers.Items {
		f.containers[c.ID] = true
		if ev := containerEventFromState(c.State, c.ID, c, now); ev != nil {
			f.publishContainerEvent(ev, c.ID)
		}
	}

	for _, n := range networks.Items {
		f.networks[n.ID] = true
	}

	// Container→network edges. Orphan edges to unmanaged networks add
	// no value, so we gate on f.networks (fully populated above).
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
			f.publishNetworkEvent(NetworkAttached{
				ContainerID: c.ID,
				NetworkID:   ep.NetworkID,
				At:          now,
			}, c.ID, ep.NetworkID)
		}
	}

	f.log.Info().
		Int("containers", len(f.containers)).
		Int("networks", len(f.networks)).
		Msg("reconcile complete")

	return nil
}

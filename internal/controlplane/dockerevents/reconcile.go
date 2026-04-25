package dockerevents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/controlplane/informer"
)

// reconcile rebuilds the four managed sets from authoritative Docker
// state and re-upserts every managed object into the informer. Called
// at startup and after every events stream reset.
//
// Reconcile is idempotent against the informer:
//   - Upsert merges Labels/Attrs key-by-key, refreshes LastSeen, and
//     records a Transition with verb "reconcile". A resource that was
//     already known emits a DeltaUpdated; a new one emits DeltaAdded.
//   - LinkRelation refreshes existing edges; new edges produce a
//     RelationAdded delta.
//
// Reconcile does NOT remove objects that were seen on a previous pass
// but are missing now. Removals come from the events stream
// (destroy/delete) — between the last list and the current one, those
// removals were either delivered as events (handled in dispatch) or
// will be replayed on the next stream open via Since= timestamp.
func (f *Feeder) reconcile(ctx context.Context) error {
	managedFilter := client.Filters{}.Add("label", f.opts.ManagedLabelKey+"="+f.opts.ManagedLabelValue)

	containers, cErr := f.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: managedFilter,
	})
	networks, nErr := f.cli.NetworkList(ctx, client.NetworkListOptions{Filters: managedFilter})
	volumes, vErr := f.cli.VolumeList(ctx, client.VolumeListOptions{Filters: managedFilter})
	images, iErr := f.cli.ImageList(ctx, client.ImageListOptions{Filters: managedFilter})

	if err := errors.Join(cErr, nErr, vErr, iErr); err != nil {
		return fmt.Errorf("dockerevents: list during reconcile: %w", err)
	}

	// Rebuild the managed-sets fresh. Stream deliveries that arrive
	// between this rebuild and runStream taking over are guarded by
	// the Since= anchor — they will redeliver and pass through dispatch
	// which patches the sets.
	f.containers = make(map[string]bool, len(containers.Items))
	f.networks = make(map[string]bool, len(networks.Items))
	f.volumes = make(map[string]bool, len(volumes.Items))
	f.images = make(map[string]bool, len(images.Items))

	now := time.Now()
	t := informer.Transition{Source: transitionSource, Verb: verbPrefix + "reconcile", At: now}

	for _, c := range containers.Items {
		f.containers[c.ID] = true
		_ = f.inf.Upsert(ctx, informer.ResourceUpdate{
			Kind:      KindContainer,
			ID:        c.ID,
			Labels:    c.Labels,
			Attrs:     containerAttrsFromSummary(c),
			Lifecycle: containerLifecycleFromState(c.State),
		}, t)

		// Network attachments — only available via the container summary
		// (events don't carry mount/network data). LinkRelation only if
		// the network is also managed; orphan edges to unmanaged networks
		// add no value.
		if c.NetworkSettings != nil {
			for _, ep := range c.NetworkSettings.Networks {
				if ep == nil || ep.NetworkID == "" {
					continue
				}
				if !f.networks[ep.NetworkID] {
					continue
				}
				_ = f.inf.LinkRelation(ctx, informer.Relation{
					From: informer.Key{Kind: KindContainer, ID: c.ID},
					To:   informer.Key{Kind: KindNetwork, ID: ep.NetworkID},
					Kind: RelationAttachedTo,
				})
			}
		}
	}

	for _, n := range networks.Items {
		f.networks[n.ID] = true
		_ = f.inf.Upsert(ctx, informer.ResourceUpdate{
			Kind:   KindNetwork,
			ID:     n.ID,
			Labels: n.Labels,
			Attrs: map[string]string{
				"name":   n.Name,
				"driver": n.Driver,
				"scope":  n.Scope,
			},
			Lifecycle: informer.LifecycleLive,
		}, t)
	}

	for _, v := range volumes.Items {
		f.volumes[v.Name] = true
		_ = f.inf.Upsert(ctx, informer.ResourceUpdate{
			Kind:   KindVolume,
			ID:     v.Name,
			Labels: v.Labels,
			Attrs: map[string]string{
				"driver":     v.Driver,
				"mountpoint": v.Mountpoint,
			},
			Lifecycle: informer.LifecycleLive,
		}, t)
	}

	for _, im := range images.Items {
		f.images[im.ID] = true
		_ = f.inf.Upsert(ctx, informer.ResourceUpdate{
			Kind:   KindImage,
			ID:     im.ID,
			Labels: im.Labels,
			Attrs: map[string]string{
				"repo_tags": joinTags(im.RepoTags),
			},
			Lifecycle: informer.LifecycleLive,
		}, t)
	}

	// Reconcile pass complete — replay container→network edges only
	// AFTER the network set is populated so the LinkRelation guard
	// above sees the right set membership. The first container loop
	// above checked f.networks which was just populated; if any
	// container appeared in containers.Items before the network it
	// attaches to (containers come first in the rebuild order),
	// LinkRelation was skipped. Rerun the attachment pass once
	// networks are seeded.
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
			_ = f.inf.LinkRelation(ctx, informer.Relation{
				From: informer.Key{Kind: KindContainer, ID: c.ID},
				To:   informer.Key{Kind: KindNetwork, ID: ep.NetworkID},
				Kind: RelationAttachedTo,
			})
		}
	}

	f.log.Info().
		Int("containers", len(f.containers)).
		Int("networks", len(f.networks)).
		Int("volumes", len(f.volumes)).
		Int("images", len(f.images)).
		Msg("reconcile complete")

	return nil
}

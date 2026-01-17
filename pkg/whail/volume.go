package whail

import (
	"context"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

// VolumeCreate creates a new volume with managed labels automatically applied.
// The provided labels are merged with the engine's configured labels.
func (e *Engine) VolumeCreate(ctx context.Context, options client.VolumeCreateOptions, extraLabels ...map[string]string) (client.VolumeCreateResult, error) {
	labels := e.volumeLabels(extraLabels...)

	// Merge labels into options instead of ignoring them
	if options.Labels == nil {
		options.Labels = labels
	} else {
		options.Labels = MergeLabels(options.Labels, labels)
	}

	result, err := e.APIClient.VolumeCreate(ctx, options)
	if err != nil {
		return client.VolumeCreateResult{}, ErrVolumeCreateFailed(options.Name, err)
	}
	return result, nil
}

// VolumeRemove removes a volume.
// Only removes managed volumes.
func (e *Engine) VolumeRemove(ctx context.Context, volumeID string, force bool) (client.VolumeRemoveResult, error) {
	isManaged, err := e.IsVolumeManaged(ctx, volumeID)
	if err != nil {
		return client.VolumeRemoveResult{}, ErrVolumeRemoveFailed(volumeID, err)
	}
	if !isManaged {
		return client.VolumeRemoveResult{}, ErrVolumeNotFound(volumeID, nil)
	}
	result, err := e.APIClient.VolumeRemove(ctx, volumeID, client.VolumeRemoveOptions{Force: force})
	if err != nil {
		return client.VolumeRemoveResult{}, ErrVolumeRemoveFailed(volumeID, err)
	}
	return result, nil
}

// VolumeInspect inspects a volume.
// Only inspects managed volumes.
func (e *Engine) VolumeInspect(ctx context.Context, volumeID string) (client.VolumeInspectResult, error) {
	isManaged, err := e.IsVolumeManaged(ctx, volumeID)
	if err != nil {
		return client.VolumeInspectResult{}, ErrVolumeInspectFailed(volumeID, err)
	}
	if !isManaged {
		return client.VolumeInspectResult{}, ErrVolumeNotFound(volumeID, nil)
	}
	result, err := e.APIClient.VolumeInspect(ctx, volumeID, client.VolumeInspectOptions{})
	if err != nil {
		return client.VolumeInspectResult{}, ErrVolumeInspectFailed(volumeID, err)
	}
	return result, nil
}

// VolumeExists checks if a volume exists.
func (e *Engine) VolumeExists(ctx context.Context, volumeID string) (bool, error) {
	_, err := e.APIClient.VolumeInspect(ctx, volumeID, client.VolumeInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// VolumeList lists volumes matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) VolumeList(ctx context.Context, extraFilters ...map[string]string) (client.VolumeListResult, error) {
	f := e.newManagedFilter()
	for _, labels := range extraFilters {
		for k, v := range labels {
			f = f.Add("label", k+"="+v)
		}
	}
	result, err := e.APIClient.VolumeList(ctx, client.VolumeListOptions{Filters: f})
	if err != nil {
		return client.VolumeListResult{}, err
	}
	return result, nil
}

// VolumeListAll lists all managed volumes.
func (e *Engine) VolumeListAll(ctx context.Context) (client.VolumeListResult, error) {
	return e.VolumeList(ctx)
}

// IsVolumeManaged checks if a volume has the managed label.
func (e *Engine) IsVolumeManaged(ctx context.Context, name string) (bool, error) {
	result, err := e.APIClient.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	val, ok := result.Volume.Labels[e.managedLabelKey]
	return ok && val == e.managedLabelValue, nil
}

// VolumesPrune removes all unused managed volumes.
// The managed label filter is automatically injected to ensure only
// managed volumes are affected.
// If all is true, prunes all unused volumes including named ones.
// If all is false, only prunes anonymous volumes (Docker's default behavior).
func (e *Engine) VolumesPrune(ctx context.Context, all bool) (client.VolumePruneResult, error) {
	f := e.newManagedFilter()
	result, err := e.APIClient.VolumePrune(ctx, client.VolumePruneOptions{All: all, Filters: f})
	if err != nil {
		return client.VolumePruneResult{}, ErrVolumesPruneFailed(err)
	}
	return result, nil
}

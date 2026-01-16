package whail

import (
	"context"

	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

// VolumeCreate creates a new volume with managed labels automatically applied.
// The provided labels are merged with the engine's configured labels.
func (e *Engine) VolumeCreate(ctx context.Context, options volume.CreateOptions, extraLabels ...map[string]string) (volume.Volume, error) {
	labels := e.volumeLabels(extraLabels...)

	// Merge labels into options instead of ignoring them
	if options.Labels == nil {
		options.Labels = labels
	} else {
		options.Labels = MergeLabels(options.Labels, labels)
	}

	vol, err := e.APIClient.VolumeCreate(ctx, options)
	if err != nil {
		return volume.Volume{}, ErrVolumeCreateFailed(options.Name, err)
	}
	return vol, nil
}

// VolumeRemove removes a volume.
// Only removes managed volumes.
func (e *Engine) VolumeRemove(ctx context.Context, volumeID string, force bool) error {
	isManaged, err := e.IsVolumeManaged(ctx, volumeID)
	if err != nil {
		return ErrVolumeRemoveFailed(volumeID, err)
	}
	if !isManaged {
		return ErrVolumeNotFound(volumeID, nil)
	}
	return e.APIClient.VolumeRemove(ctx, volumeID, force)
}

// VolumeInspect inspects a volume.
// Only inspects managed volumes.
func (e *Engine) VolumeInspect(ctx context.Context, volumeID string) (volume.Volume, error) {
	isManaged, err := e.IsVolumeManaged(ctx, volumeID)
	if err != nil {
		return volume.Volume{}, ErrVolumeInspectFailed(volumeID, err)
	}
	if !isManaged {
		return volume.Volume{}, ErrVolumeNotFound(volumeID, nil)
	}
	return e.APIClient.VolumeInspect(ctx, volumeID)
}

// VolumeExists checks if a volume exists.
func (e *Engine) VolumeExists(ctx context.Context, volumeID string) (bool, error) {
	_, err := e.APIClient.VolumeInspect(ctx, volumeID)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// VolumeList lists volumes matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) VolumeList(ctx context.Context, extraFilters ...map[string]string) (volume.ListResponse, error) {
	f := e.newManagedFilter()
	for _, labels := range extraFilters {
		for k, v := range labels {
			f.Add("label", k+"="+v)
		}
	}
	return e.APIClient.VolumeList(ctx, volume.ListOptions{Filters: f})
}

// VolumeListAll lists all managed volumes.
func (e *Engine) VolumeListAll(ctx context.Context) (volume.ListResponse, error) {
	return e.VolumeList(ctx)
}

// IsVolumeManaged checks if a volume has the managed label.
func (e *Engine) IsVolumeManaged(ctx context.Context, name string) (bool, error) {
	vol, err := e.APIClient.VolumeInspect(ctx, name)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}

	val, ok := vol.Labels[e.managedLabelKey]
	return ok && val == e.managedLabelValue, nil
}

// VolumesPrune removes all unused managed volumes.
// The managed label filter is automatically injected to ensure only
// managed volumes are affected.
// VolumesPrune removes all unused managed volumes.
// The managed label filter is automatically injected to ensure only
// managed volumes are affected.
// If all is true, prunes all unused volumes including named ones.
// If all is false, only prunes anonymous volumes (Docker's default behavior).
func (e *Engine) VolumesPrune(ctx context.Context, all bool) (volume.PruneReport, error) {
	f := e.newManagedFilter()
	if all {
		f.Add("all", "true")
	}
	report, err := e.APIClient.VolumesPrune(ctx, f)
	if err != nil {
		return volume.PruneReport{}, ErrVolumesPruneFailed(err)
	}
	return report, nil
}

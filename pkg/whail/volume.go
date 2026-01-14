package whail

import (
	"context"

	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

// VolumeCreate creates a new volume with managed labels automatically applied.
// The provided labels are merged with the engine's configured labels.
func (e *Engine) VolumeCreate(ctx context.Context, name string, extraLabels ...map[string]string) (volume.Volume, error) {
	labels := e.volumeLabels(extraLabels...)

	vol, err := e.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   name,
		Labels: labels,
	})
	if err != nil {
		return volume.Volume{}, ErrVolumeCreateFailed(name, err)
	}
	return vol, nil
}

// VolumeRemove removes a volume.
func (e *Engine) VolumeRemove(ctx context.Context, name string, force bool) error {
	return e.cli.VolumeRemove(ctx, name, force)
}

// VolumeInspect inspects a volume.
func (e *Engine) VolumeInspect(ctx context.Context, name string) (volume.Volume, error) {
	return e.cli.VolumeInspect(ctx, name)
}

// VolumeExists checks if a volume exists.
func (e *Engine) VolumeExists(ctx context.Context, name string) (bool, error) {
	_, err := e.cli.VolumeInspect(ctx, name)
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
	return e.cli.VolumeList(ctx, volume.ListOptions{Filters: f})
}

// VolumeListAll lists all managed volumes.
func (e *Engine) VolumeListAll(ctx context.Context) (volume.ListResponse, error) {
	return e.VolumeList(ctx)
}

// VolumeListByLabels lists volumes matching additional label filters.
// The managed label filter is automatically injected.
func (e *Engine) VolumeListByLabels(ctx context.Context, labels map[string]string) (volume.ListResponse, error) {
	return e.VolumeList(ctx, labels)
}

// IsVolumeManaged checks if a volume has the managed label.
func (e *Engine) IsVolumeManaged(ctx context.Context, name string) (bool, error) {
	vol, err := e.cli.VolumeInspect(ctx, name)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}

	val, ok := vol.Labels[e.managedLabelKey]
	return ok && val == e.managedLabelValue, nil
}

package whail

import (
	"context"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
)

// ImageBuild builds an image from a build context.
// Labels are applied via the build options.
func (e *Engine) ImageBuild(ctx context.Context, buildContext io.Reader, options types.ImageBuildOptions) (types.ImageBuildResponse, error) {
	// Merge labels: base managed + config + user-provided
	options.Labels = MergeLabels(
		e.imageLabels(),
		options.Labels,
	)

	resp, err := e.APIClient.ImageBuild(ctx, buildContext, options)
	if err != nil {
		return types.ImageBuildResponse{}, ErrImageBuildFailed(err)
	}
	return resp, nil
}

// ImageRemove removes an image.
func (e *Engine) ImageRemove(ctx context.Context, imageID string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
	isManaged, err := e.isManagedImage(ctx, imageID)
	if err != nil || !isManaged {
		return nil, ErrImageNotFound(imageID, err)
	}
	return e.APIClient.ImageRemove(ctx, imageID, options)
}

// ImageList lists images matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) ImageList(ctx context.Context, options image.ListOptions) ([]image.Summary, error) {
	f := e.newManagedFilter()
	return e.APIClient.ImageList(ctx, image.ListOptions{Filters: f})
}

// ImageInspect inspects an image.
func (e *Engine) ImageInspect(ctx context.Context, imageRef string) (types.ImageInspect, error) {
	isManaged, err := e.isManagedImage(ctx, imageRef)
	if err != nil || !isManaged {
		return types.ImageInspect{}, ErrImageNotFound(imageRef, err)
	}
	info, _, err := e.APIClient.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		return types.ImageInspect{}, ErrImageNotFound(imageRef, err)
	}
	return info, nil
}

// isManagedImage checks if an image has the managed label.
func (e *Engine) isManagedImage(ctx context.Context, imageRef string) (bool, error) {
	info, _, err := e.APIClient.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		return false, ErrImageNotFound(imageRef, err)
	}
	return e.isManagedLabelPresent(info.Config.Labels), nil
}

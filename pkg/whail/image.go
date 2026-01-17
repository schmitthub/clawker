package whail

import (
	"context"
	"io"

	"github.com/moby/moby/client"
)

// ImageBuild builds an image from a build context.
// Labels are applied via the build options.
func (e *Engine) ImageBuild(ctx context.Context, buildContext io.Reader, options client.ImageBuildOptions) (client.ImageBuildResult, error) {
	// Merge labels: base managed + config + user-provided
	// TODO: will this mutation be problematic if options is reused?
	options.Labels = MergeLabels(
		e.imageLabels(),
		options.Labels,
	)

	resp, err := e.APIClient.ImageBuild(ctx, buildContext, options)
	if err != nil {
		return client.ImageBuildResult{}, ErrImageBuildFailed(err)
	}
	return resp, nil
}

// ImageRemove removes an image.
func (e *Engine) ImageRemove(ctx context.Context, imageID string, options client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
	isManaged, err := e.isManagedImage(ctx, imageID)
	if err != nil || !isManaged {
		return client.ImageRemoveResult{}, ErrImageNotFound(imageID, err)
	}
	result, err := e.APIClient.ImageRemove(ctx, imageID, options)
	if err != nil {
		return client.ImageRemoveResult{}, ErrImageNotFound(imageID, err)
	}
	return result, nil
}

// ImageList lists images matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) ImageList(ctx context.Context, options client.ImageListOptions) (client.ImageListResult, error) {
	// TODO: this seems sloppy. overwriting users filters without merging them. prob need a copy to preserve options
	options.Filters = e.newManagedFilter()
	result, err := e.APIClient.ImageList(ctx, options)
	if err != nil {
		return client.ImageListResult{}, err
	}
	return result, nil
}

// ImageInspect inspects an image.
func (e *Engine) ImageInspect(ctx context.Context, imageRef string) (client.ImageInspectResult, error) {
	isManaged, err := e.isManagedImage(ctx, imageRef)
	if err != nil || !isManaged {
		return client.ImageInspectResult{}, ErrImageNotFound(imageRef, err)
	}
	result, err := e.APIClient.ImageInspect(ctx, imageRef)
	if err != nil {
		return client.ImageInspectResult{}, ErrImageNotFound(imageRef, err)
	}
	return result, nil
}

// isManagedImage checks if an image has the managed label.
func (e *Engine) isManagedImage(ctx context.Context, imageRef string) (bool, error) {
	result, err := e.APIClient.ImageInspect(ctx, imageRef)
	if err != nil {
		return false, ErrImageNotFound(imageRef, err)
	}
	return e.isManagedLabelPresent(result.Config.Labels), nil
}

// ImagesPrune removes all unused managed images.
// The managed label filter is automatically injected to ensure only
// managed images are affected.
// The dangling parameter controls whether to only remove dangling images (untagged)
// or all unused images.
func (e *Engine) ImagesPrune(ctx context.Context, dangling bool) (client.ImagePruneResult, error) {
	f := e.newManagedFilter()
	// dangling=true means only remove images without tags
	// dangling=false means remove all unused images
	// Note: Docker defaults to dangling=true, so we must explicitly set false
	if dangling {
		f = f.Add("dangling", "true")
	} else {
		f = f.Add("dangling", "false")
	}
	result, err := e.APIClient.ImagePrune(ctx, client.ImagePruneOptions{Filters: f})
	if err != nil {
		return client.ImagePruneResult{}, ErrImagesPruneFailed(err)
	}
	return result, nil
}

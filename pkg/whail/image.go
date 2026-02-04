package whail

import (
	"context"
	"io"

	"github.com/moby/moby/client"
)

// ImageBuild builds an image from a build context.
// Labels are applied via the build options.
func (e *Engine) ImageBuild(ctx context.Context, buildContext io.Reader, options client.ImageBuildOptions) (client.ImageBuildResult, error) {
	// Copy options to avoid mutating caller's struct
	optsCopy := options
	optsCopy.Labels = MergeLabels(
		e.imageLabels(),
		options.Labels,
	)
	// Ensure managed label cannot be overridden by caller labels.
	optsCopy.Labels[e.managedLabelKey] = e.managedLabelValue

	resp, err := e.APIClient.ImageBuild(ctx, buildContext, optsCopy)
	if err != nil {
		return client.ImageBuildResult{}, ErrImageBuildFailed(err)
	}
	return resp, nil
}

// ImageBuildKit builds an image using BuildKit via the configured closure.
// Labels are merged identically to ImageBuild — managed labels are injected
// and cannot be overridden by caller-supplied labels.
//
// Returns ErrBuildKitNotConfigured if BuildKitImageBuilder is nil.
func (e *Engine) ImageBuildKit(ctx context.Context, opts ImageBuildKitOptions) error {
	if e.BuildKitImageBuilder == nil {
		return ErrBuildKitNotConfigured()
	}

	// Copy options to avoid mutating caller's struct
	optsCopy := opts
	optsCopy.Labels = MergeLabels(
		e.imageLabels(),
		opts.Labels,
	)
	// Ensure managed label cannot be overridden by caller labels.
	optsCopy.Labels[e.managedLabelKey] = e.managedLabelValue

	return e.BuildKitImageBuilder(ctx, optsCopy)
}

// ImageTag adds a tag to an existing managed image.
// The source image must have the managed label or the operation is rejected.
func (e *Engine) ImageTag(ctx context.Context, opts ImageTagOptions) (ImageTagResult, error) {
	if _, err := e.isManagedImage(ctx, opts.Source); err != nil {
		return ImageTagResult{}, ErrImageNotFound(opts.Source, err)
	}
	return e.APIClient.ImageTag(ctx, opts)
}

// ImageRemove removes an image.
func (e *Engine) ImageRemove(ctx context.Context, imageID string, options client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
	isManaged, err := e.isManagedImage(ctx, imageID)
	if err != nil || !isManaged {
		return client.ImageRemoveResult{}, ErrImageNotFound(imageID, err)
	}
	result, err := e.APIClient.ImageRemove(ctx, imageID, options)
	if err != nil {
		return client.ImageRemoveResult{}, ErrImageRemoveFailed(imageID, err)
	}
	return result, nil
}

// ImageList lists images matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) ImageList(ctx context.Context, options client.ImageListOptions) (client.ImageListResult, error) {
	options.Filters = e.injectManagedFilter(options.Filters)
	result, err := e.APIClient.ImageList(ctx, options)
	if err != nil {
		return client.ImageListResult{}, ErrImageListFailed(err)
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

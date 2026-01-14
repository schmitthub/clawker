package whail

import (
	"context"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

// ImageBuild builds an image from a build context.
// Labels are applied via the build options.
func (e *Engine) ImageBuild(ctx context.Context, buildContext io.Reader, options types.ImageBuildOptions) (types.ImageBuildResponse, error) {
	// Merge labels: base managed + config + user-provided
	options.Labels = MergeLabels(
		e.imageLabels(),
		options.Labels,
	)

	resp, err := e.cli.ImageBuild(ctx, buildContext, options)
	if err != nil {
		return types.ImageBuildResponse{}, ErrImageBuildFailed(err)
	}
	return resp, nil
}

// ImagePull pulls an image from a registry.
// Note: Pulled images cannot have labels applied; labels are only for built images.
func (e *Engine) ImagePull(ctx context.Context, imageRef string) (io.ReadCloser, error) {
	reader, err := e.cli.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return nil, ErrImageNotFound(imageRef, err)
	}
	return reader, nil
}

// ImageRemove removes an image.
func (e *Engine) ImageRemove(ctx context.Context, imageID string, force bool) error {
	_, err := e.cli.ImageRemove(ctx, imageID, image.RemoveOptions{Force: force})
	return err
}

// ImageList lists images matching the filter.
// The managed label filter is automatically injected.
// Note: This only returns images that were built with managed labels.
func (e *Engine) ImageList(ctx context.Context) ([]image.Summary, error) {
	f := e.newManagedFilter()
	return e.cli.ImageList(ctx, image.ListOptions{Filters: f})
}

// ImageListAll lists all images (not filtered by managed label).
// Use this when you need to see all available images, not just managed ones.
func (e *Engine) ImageListAll(ctx context.Context) ([]image.Summary, error) {
	return e.cli.ImageList(ctx, image.ListOptions{})
}

// ImageListByLabels lists images matching additional label filters.
// The managed label filter is automatically injected.
func (e *Engine) ImageListByLabels(ctx context.Context, labels map[string]string) ([]image.Summary, error) {
	f := e.newManagedFilter()
	for k, v := range labels {
		f.Add("label", k+"="+v)
	}
	return e.cli.ImageList(ctx, image.ListOptions{Filters: f})
}

// ImageExists checks if an image exists locally.
func (e *Engine) ImageExists(ctx context.Context, imageRef string) (bool, error) {
	_, _, err := e.cli.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ImageInspect inspects an image.
func (e *Engine) ImageInspect(ctx context.Context, imageRef string) (types.ImageInspect, error) {
	info, _, err := e.cli.ImageInspectWithRaw(ctx, imageRef)
	return info, err
}

// IsImageManaged checks if an image has the managed label.
func (e *Engine) IsImageManaged(ctx context.Context, imageRef string) (bool, error) {
	info, _, err := e.cli.ImageInspectWithRaw(ctx, imageRef)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}

	val, ok := info.Config.Labels[e.managedLabelKey]
	return ok && val == e.managedLabelValue, nil
}

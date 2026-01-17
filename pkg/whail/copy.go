package whail

import (
	"context"

	"github.com/moby/moby/client"
)

// CopyToContainer copies content to a container.
// The content should be a tar archive.
// Only copies to managed containers.
func (e *Engine) CopyToContainer(ctx context.Context, containerID string, opts client.CopyToContainerOptions) (client.CopyToContainerResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.CopyToContainerResult{}, ErrCopyToContainerFailed(containerID, err)
	}
	if !isManaged {
		return client.CopyToContainerResult{}, ErrContainerNotFound(containerID)
	}
	result, err := e.APIClient.CopyToContainer(ctx, containerID, opts)
	if err != nil {
		return client.CopyToContainerResult{}, ErrCopyToContainerFailed(containerID, err)
	}
	return result, nil
}

// CopyFromContainer copies content from a container.
// Returns a tar archive reader and file stat info.
// The caller is responsible for closing the reader.
// Only copies from managed containers.
func (e *Engine) CopyFromContainer(ctx context.Context, containerID string, opts client.CopyFromContainerOptions) (client.CopyFromContainerResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.CopyFromContainerResult{}, ErrCopyFromContainerFailed(containerID, err)
	}
	if !isManaged {
		return client.CopyFromContainerResult{}, ErrContainerNotFound(containerID)
	}
	result, err := e.APIClient.CopyFromContainer(ctx, containerID, opts)
	if err != nil {
		return client.CopyFromContainerResult{}, ErrCopyFromContainerFailed(containerID, err)
	}
	return result, nil
}

// ContainerStatPath returns stat info for a path inside a container.
// Only works on managed containers.
func (e *Engine) ContainerStatPath(ctx context.Context, containerID string, opts client.ContainerStatPathOptions) (client.ContainerStatPathResult, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return client.ContainerStatPathResult{}, ErrContainerStatPathFailed(containerID, err)
	}
	if !isManaged {
		return client.ContainerStatPathResult{}, ErrContainerNotFound(containerID)
	}
	result, err := e.APIClient.ContainerStatPath(ctx, containerID, opts)
	if err != nil {
		return client.ContainerStatPathResult{}, ErrContainerStatPathFailed(containerID, err)
	}
	return result, nil
}

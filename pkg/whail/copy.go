package whail

import (
	"context"
	"io"

	"github.com/docker/docker/api/types/container"
)

// CopyToContainer copies content to a container.
// The content should be a tar archive.
// Only copies to managed containers.
func (e *Engine) CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader, opts container.CopyToContainerOptions) error {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return ErrCopyToContainerFailed(containerID, err)
	}
	if !isManaged {
		return ErrContainerNotFound(containerID)
	}
	if err := e.APIClient.CopyToContainer(ctx, containerID, dstPath, content, opts); err != nil {
		return ErrCopyToContainerFailed(containerID, err)
	}
	return nil
}

// CopyFromContainer copies content from a container.
// Returns a tar archive reader and file stat info.
// The caller is responsible for closing the reader.
// Only copies from managed containers.
func (e *Engine) CopyFromContainer(ctx context.Context, containerID, srcPath string) (io.ReadCloser, container.PathStat, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return nil, container.PathStat{}, ErrCopyFromContainerFailed(containerID, err)
	}
	if !isManaged {
		return nil, container.PathStat{}, ErrContainerNotFound(containerID)
	}
	reader, stat, err := e.APIClient.CopyFromContainer(ctx, containerID, srcPath)
	if err != nil {
		return nil, container.PathStat{}, ErrCopyFromContainerFailed(containerID, err)
	}
	return reader, stat, nil
}

// ContainerStatPath returns stat info for a path inside a container.
// Only works on managed containers.
func (e *Engine) ContainerStatPath(ctx context.Context, containerID, path string) (container.PathStat, error) {
	isManaged, err := e.IsContainerManaged(ctx, containerID)
	if err != nil {
		return container.PathStat{}, ErrContainerStatPathFailed(containerID, err)
	}
	if !isManaged {
		return container.PathStat{}, ErrContainerNotFound(containerID)
	}
	stat, err := e.APIClient.ContainerStatPath(ctx, containerID, path)
	if err != nil {
		return container.PathStat{}, ErrContainerStatPathFailed(containerID, err)
	}
	return stat, nil
}

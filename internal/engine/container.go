package engine

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
	"github.com/schmitthub/clawker/pkg/logger"
)

// ContainerManager handles container lifecycle operations
type ContainerManager struct {
	engine *Engine
}

// NewContainerManager creates a new container manager
func NewContainerManager(engine *Engine) *ContainerManager {
	return &ContainerManager{engine: engine}
}

// ContainerConfig holds configuration for container creation
type ContainerConfig struct {
	Name         string
	Image        string
	Mounts       []mount.Mount
	Env          []string
	WorkingDir   string
	Entrypoint   []string
	Cmd          []string
	Tty          bool
	OpenStdin    bool
	AttachStdin  bool
	AttachStdout bool
	AttachStderr bool
	CapAdd       []string
	NetworkMode  string
	User         string
	Labels       map[string]string
	PortBindings nat.PortMap
	ExposedPorts nat.PortSet
}

// Create creates a new container without starting it.
// Use Start() to start the container after attaching.
func (cm *ContainerManager) Create(ctx context.Context, cfg ContainerConfig) (string, error) {
	// Create container config
	containerConfig := &container.Config{
		Image:        cfg.Image,
		Env:          cfg.Env,
		WorkingDir:   cfg.WorkingDir,
		Entrypoint:   cfg.Entrypoint,
		Cmd:          cfg.Cmd,
		Tty:          cfg.Tty,
		OpenStdin:    cfg.OpenStdin,
		AttachStdin:  cfg.AttachStdin,
		AttachStdout: cfg.AttachStdout,
		AttachStderr: cfg.AttachStderr,
		User:         cfg.User,
		Labels:       cfg.Labels,
		ExposedPorts: cfg.ExposedPorts,
	}

	// Create host config
	hostConfig := &container.HostConfig{
		Mounts:       cfg.Mounts,
		NetworkMode:  container.NetworkMode(cfg.NetworkMode),
		CapAdd:       cfg.CapAdd,
		PortBindings: cfg.PortBindings,
	}

	// Create container
	resp, err := cm.engine.ContainerCreate(ctx, containerConfig, hostConfig, cfg.Name)
	if err != nil {
		return "", err
	}

	logger.Debug().
		Str("container", cfg.Name).
		Str("id", resp.ID[:12]).
		Msg("container created")

	return resp.ID, nil
}

// Start starts a created container
func (cm *ContainerManager) Start(ctx context.Context, containerID string) error {
	return cm.engine.ContainerStart(ctx, containerID)
}

// CreateAndStart creates a new container and starts it
func (cm *ContainerManager) CreateAndStart(ctx context.Context, cfg ContainerConfig) (string, error) {
	containerID, err := cm.Create(ctx, cfg)
	if err != nil {
		return "", err
	}

	// Start container
	if err := cm.Start(ctx, containerID); err != nil {
		// Clean up on failure
		cm.engine.ContainerRemove(ctx, containerID, true)
		return "", err
	}

	logger.Info().
		Str("container", cfg.Name).
		Str("id", containerID[:12]).
		Msg("container started")

	return containerID, nil
}

// FindOrCreate finds an existing container or creates a new one
func (cm *ContainerManager) FindOrCreate(ctx context.Context, cfg ContainerConfig) (string, bool, error) {
	// Check for existing container
	existing, err := cm.engine.FindContainerByName(ctx, cfg.Name)
	if err != nil {
		return "", false, err
	}

	if existing != nil {
		logger.Debug().
			Str("container", cfg.Name).
			Str("state", existing.State).
			Msg("found existing container")

		// If running, return it
		if existing.State == "running" {
			return existing.ID, false, nil
		}

		// If stopped, start it
		if existing.State == "exited" || existing.State == "created" {
			if err := cm.engine.ContainerStart(ctx, existing.ID); err != nil {
				return "", false, err
			}
			logger.Info().Str("container", cfg.Name).Msg("started existing container")
			return existing.ID, false, nil
		}

		// Remove containers in other states and recreate
		logger.Debug().
			Str("container", cfg.Name).
			Str("state", existing.State).
			Msg("removing container in unexpected state")
		if err := cm.engine.ContainerRemove(ctx, existing.ID, true); err != nil {
			return "", false, err
		}
	}

	// Create new container
	id, err := cm.CreateAndStart(ctx, cfg)
	if err != nil {
		return "", false, err
	}

	return id, true, nil
}

// Stop stops a container
func (cm *ContainerManager) Stop(ctx context.Context, containerID string, timeout int) error {
	return cm.engine.ContainerStop(ctx, containerID, &timeout)
}

// Remove removes a container
func (cm *ContainerManager) Remove(ctx context.Context, containerID string, force bool) error {
	return cm.engine.ContainerRemove(ctx, containerID, force)
}

// Attach attaches to a container's TTY
func (cm *ContainerManager) Attach(ctx context.Context, containerID string) (types.HijackedResponse, error) {
	return cm.engine.ContainerAttach(ctx, containerID, container.AttachOptions{
		Stream: true,
		Stdin:  true,
		Stdout: true,
		Stderr: true,
	})
}

// Wait waits for a container to exit and returns the exit code
func (cm *ContainerManager) Wait(ctx context.Context, containerID string) (int64, error) {
	statusCh, errCh := cm.engine.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)

	select {
	case err := <-errCh:
		return -1, err
	case status := <-statusCh:
		return status.StatusCode, nil
	}
}

// Logs streams container logs
func (cm *ContainerManager) Logs(ctx context.Context, containerID string, follow bool, tail string) (io.ReadCloser, error) {
	return cm.engine.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tail,
		Timestamps: false,
	})
}

// Exec executes a command in a running container
func (cm *ContainerManager) Exec(ctx context.Context, containerID string, cmd []string, tty bool) (types.HijackedResponse, string, error) {
	// Create exec instance
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          tty,
	}

	resp, err := cm.engine.ContainerExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return types.HijackedResponse{}, "", fmt.Errorf("failed to create exec: %w", err)
	}

	// Attach to exec
	hijacked, err := cm.engine.ContainerExecAttach(ctx, resp.ID, container.ExecStartOptions{
		Tty: tty,
	})
	if err != nil {
		return types.HijackedResponse{}, "", fmt.Errorf("failed to attach to exec: %w", err)
	}

	return hijacked, resp.ID, nil
}

// Resize resizes a container's TTY
func (cm *ContainerManager) Resize(ctx context.Context, containerID string, height, width uint) error {
	return cm.engine.ContainerResize(ctx, containerID, height, width)
}

// ResizeExec resizes an exec instance's TTY
func (cm *ContainerManager) ResizeExec(ctx context.Context, execID string, height, width uint) error {
	return cm.engine.ContainerExecResize(ctx, execID, height, width)
}

// IsRunning checks if a container is running
func (cm *ContainerManager) IsRunning(ctx context.Context, containerID string) (bool, error) {
	info, err := cm.engine.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, err
	}
	return info.State.Running, nil
}

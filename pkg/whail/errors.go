package whail

import (
	"fmt"
	"strings"
)

// DockerError represents a user-friendly Docker error with remediation steps.
// It wraps underlying Docker SDK errors with context and actionable guidance.
type DockerError struct {
	Op        string   // Operation that failed (e.g., "connect", "build", "run")
	Err       error    // Underlying error
	Message   string   // Human-readable message
	NextSteps []string // Suggested remediation steps
}

func (e *DockerError) Error() string {
	return e.Message
}

func (e *DockerError) Unwrap() error {
	return e.Err
}

// FormatUserError formats the error for display to users with next steps.
func (e *DockerError) FormatUserError() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Error: %s\n", e.Message))

	if e.Err != nil {
		sb.WriteString(fmt.Sprintf("  Details: %s\n", e.Err.Error()))
	}

	if len(e.NextSteps) > 0 {
		sb.WriteString("\nNext Steps:\n")
		for i, step := range e.NextSteps {
			sb.WriteString(fmt.Sprintf("  %d. %s\n", i+1, step))
		}
	}

	return sb.String()
}

// Common error constructors

// ErrDockerNotRunning returns an error for when Docker daemon is not accessible.
func ErrDockerNotRunning(err error) *DockerError {
	return &DockerError{
		Op:      "connect",
		Err:     err,
		Message: "Cannot connect to Docker daemon",
		NextSteps: []string{
			"Ensure Docker is installed",
			"Start Docker Desktop (macOS/Windows) or run 'sudo systemctl start docker' (Linux)",
			"Check if Docker socket is accessible: ls -la /var/run/docker.sock",
			"Verify your user is in the docker group: groups $USER",
		},
	}
}

// ErrImageNotFound returns an error for when an image cannot be found.
func ErrImageNotFound(image string, err error) *DockerError {
	return &DockerError{
		Op:      "pull",
		Err:     err,
		Message: fmt.Sprintf("Image '%s' not found", image),
		NextSteps: []string{
			"Check the image name and tag are correct",
			"Verify you have network access to the registry",
			"Try pulling manually: docker pull " + image,
		},
	}
}

// ErrImageBuildFailed returns an error for when image build fails.
func ErrImageBuildFailed(err error) *DockerError {
	return &DockerError{
		Op:      "build",
		Err:     err,
		Message: "Failed to build Docker image",
		NextSteps: []string{
			"Check the Dockerfile syntax",
			"Verify all referenced files exist in the build context",
			"Review the build output for specific errors",
			"Try building manually: docker build -t test .",
		},
	}
}

// ErrContainerNotFound returns an error for when a container cannot be found.
func ErrContainerNotFound(name string) *DockerError {
	return &DockerError{
		Op:      "find",
		Err:     nil,
		Message: fmt.Sprintf("Container '%s' not found", name),
		NextSteps: []string{
			"Check if the container was started",
			"Check running containers: docker ps",
			"Check all containers: docker ps -a",
		},
	}
}

// ErrContainerStartFailed returns an error for when a container fails to start.
func ErrContainerStartFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "start",
		Err:     err,
		Message: fmt.Sprintf("Failed to start container '%s'", name),
		NextSteps: []string{
			"Check container logs: docker logs " + name,
			"Verify the image is valid",
			"Check for port conflicts",
			"Try removing and recreating the container",
		},
	}
}

// ErrContainerCreateFailed returns an error for when container creation fails.
func ErrContainerCreateFailed(err error) *DockerError {
	return &DockerError{
		Op:      "create",
		Err:     err,
		Message: "Failed to create container",
		NextSteps: []string{
			"Check if the image exists",
			"Verify volume mount paths are valid",
			"Check for conflicting container names",
			"Review Docker daemon logs for details",
		},
	}
}

// ErrContainerRemoveFailed returns an error for when container removal fails.
func ErrContainerRemoveFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "remove",
		Err:     err,
		Message: fmt.Sprintf("Failed to remove container '%s'", name),
		NextSteps: []string{
			"Check if the container exists",
			"Verify the container is not running",
			"Check for dependent resources",
			"Review Docker daemon logs for details",
		},
	}
}

// ErrVolumeCreateFailed returns an error for when volume creation fails.
func ErrVolumeCreateFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "volume_create",
		Err:     err,
		Message: fmt.Sprintf("Failed to create volume '%s'", name),
		NextSteps: []string{
			"Check Docker daemon is running",
			"Verify disk space is available",
			"Check for conflicting volume names: docker volume ls",
		},
	}
}

// ErrVolumeCopyFailed returns an error for when copying to a volume fails.
func ErrVolumeCopyFailed(err error) *DockerError {
	return &DockerError{
		Op:      "volume_copy",
		Err:     err,
		Message: "Failed to copy files to volume",
		NextSteps: []string{
			"Check source directory exists and is readable",
			"Verify disk space is available",
		},
	}
}

// ErrAttachFailed returns an error for when attaching to a container fails.
func ErrAttachFailed(err error) *DockerError {
	return &DockerError{
		Op:      "attach",
		Err:     err,
		Message: "Failed to attach to container",
		NextSteps: []string{
			"Verify the container is running",
			"Check if the container has a TTY allocated",
		},
	}
}

// ErrNetworkError returns an error for network-related failures.
func ErrNetworkError(err error) *DockerError {
	return &DockerError{
		Op:      "network",
		Err:     err,
		Message: "Network operation failed",
		NextSteps: []string{
			"Check Docker network configuration",
			"Verify firewall settings",
			"Try restarting Docker daemon",
		},
	}
}

func ErrNetworkNotFound(name string, err error) *DockerError {
	return &DockerError{
		Op:      "network_find",
		Err:     err,
		Message: fmt.Sprintf("Network '%s' not found", name),
		NextSteps: []string{
			"Check the network name is correct",
			"List existing networks: docker network ls",
		},
	}
}

// ErrNetworkCreateFailed returns an error for when network creation fails.
func ErrNetworkCreateFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "network_create",
		Err:     err,
		Message: fmt.Sprintf("Failed to create network '%s'", name),
		NextSteps: []string{
			"Check Docker daemon is running",
			"Verify no conflicting networks exist: docker network ls",
			"Check if network with same name exists: docker network inspect " + name,
		},
	}
}

// ErrNetworkRemoveFailed returns an error for when network removal fails.
func ErrNetworkRemoveFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "network_remove",
		Err:     err,
		Message: fmt.Sprintf("Failed to remove network '%s'", name),
		NextSteps: []string{
			"Check if the network is in use by containers",
			"Stop containers using this network first",
			"Verify the network exists: docker network ls",
		},
	}
}

// ErrContainerStopFailed returns an error for when a container fails to stop.
func ErrContainerStopFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "stop",
		Err:     err,
		Message: fmt.Sprintf("Failed to stop container '%s'", name),
		NextSteps: []string{
			"Check if the container is running: docker ps",
			"Try force stopping: docker stop -f " + name,
			"Check container logs: docker logs " + name,
		},
	}
}

// ErrContainerInspectFailed returns an error for when container inspection fails.
func ErrContainerInspectFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "inspect",
		Err:     err,
		Message: fmt.Sprintf("Failed to inspect container '%s'", name),
		NextSteps: []string{
			"Check if the container exists: docker ps -a",
			"Verify Docker daemon is running",
		},
	}
}

// ErrContainerLogsFailed returns an error for when fetching container logs fails.
func ErrContainerLogsFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "logs",
		Err:     err,
		Message: fmt.Sprintf("Failed to get logs for container '%s'", name),
		NextSteps: []string{
			"Check if the container exists: docker ps -a",
			"Verify the container has been started at least once",
		},
	}
}

// ErrVolumeNotFound returns an error for when a volume cannot be found.
func ErrVolumeNotFound(name string, err error) *DockerError {
	return &DockerError{
		Op:      "volume_find",
		Err:     err,
		Message: fmt.Sprintf("Volume '%s' not found", name),
		NextSteps: []string{
			"Check the volume name is correct",
			"List existing volumes: docker volume ls",
		},
	}
}

// ErrVolumeRemoveFailed returns an error for when volume removal fails.
func ErrVolumeRemoveFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "volume_remove",
		Err:     err,
		Message: fmt.Sprintf("Failed to remove volume '%s'", name),
		NextSteps: []string{
			"Check if the volume is in use by a container",
			"Stop containers using this volume first",
			"Try force removal: docker volume rm -f " + name,
		},
	}
}

// ErrVolumeInspectFailed returns an error for when volume inspection fails.
func ErrVolumeInspectFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "volume_inspect",
		Err:     err,
		Message: fmt.Sprintf("Failed to inspect volume '%s'", name),
		NextSteps: []string{
			"Check if the volume exists: docker volume ls",
			"Verify Docker daemon is running",
		},
	}
}

// ErrContainerKillFailed returns an error for when killing a container fails.
func ErrContainerKillFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "kill",
		Err:     err,
		Message: fmt.Sprintf("Failed to kill container '%s'", name),
		NextSteps: []string{
			"Check if the container is running: docker ps",
			"Try stopping gracefully: docker stop " + name,
		},
	}
}

// ErrContainerRestartFailed returns an error for when restarting a container fails.
func ErrContainerRestartFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "restart",
		Err:     err,
		Message: fmt.Sprintf("Failed to restart container '%s'", name),
		NextSteps: []string{
			"Check if the container exists: docker ps -a",
			"Check container logs: docker logs " + name,
		},
	}
}

// ErrContainerPauseFailed returns an error for when pausing a container fails.
func ErrContainerPauseFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "pause",
		Err:     err,
		Message: fmt.Sprintf("Failed to pause container '%s'", name),
		NextSteps: []string{
			"Check if the container is running: docker ps",
			"Verify the container is not already paused",
		},
	}
}

// ErrContainerUnpauseFailed returns an error for when unpausing a container fails.
func ErrContainerUnpauseFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "unpause",
		Err:     err,
		Message: fmt.Sprintf("Failed to unpause container '%s'", name),
		NextSteps: []string{
			"Check if the container is paused: docker ps",
		},
	}
}

// ErrContainerRenameFailed returns an error for when renaming a container fails.
func ErrContainerRenameFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "rename",
		Err:     err,
		Message: fmt.Sprintf("Failed to rename container '%s'", name),
		NextSteps: []string{
			"Check if the container exists: docker ps -a",
			"Verify the new name is not already in use",
		},
	}
}

// ErrContainerTopFailed returns an error for when getting container processes fails.
func ErrContainerTopFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "top",
		Err:     err,
		Message: fmt.Sprintf("Failed to get processes for container '%s'", name),
		NextSteps: []string{
			"Check if the container is running: docker ps",
			"Verify the container has processes running",
		},
	}
}

// ErrContainerStatsFailed returns an error for when getting container stats fails.
func ErrContainerStatsFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "stats",
		Err:     err,
		Message: fmt.Sprintf("Failed to get stats for container '%s'", name),
		NextSteps: []string{
			"Check if the container is running: docker ps",
		},
	}
}

// ErrContainerUpdateFailed returns an error for when updating container config fails.
func ErrContainerUpdateFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "update",
		Err:     err,
		Message: fmt.Sprintf("Failed to update container '%s'", name),
		NextSteps: []string{
			"Check if the container exists: docker ps -a",
			"Verify the update configuration is valid",
		},
	}
}

// ErrCopyToContainerFailed returns an error for when copying to a container fails.
func ErrCopyToContainerFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "copy_to",
		Err:     err,
		Message: fmt.Sprintf("Failed to copy files to container '%s'", name),
		NextSteps: []string{
			"Check if the container exists: docker ps -a",
			"Verify the destination path is valid",
			"Check if the source file exists",
		},
	}
}

// ErrCopyFromContainerFailed returns an error for when copying from a container fails.
func ErrCopyFromContainerFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "copy_from",
		Err:     err,
		Message: fmt.Sprintf("Failed to copy files from container '%s'", name),
		NextSteps: []string{
			"Check if the container exists: docker ps -a",
			"Verify the source path exists in the container",
		},
	}
}

// ErrContainerStatPathFailed returns an error for when stat'ing a path in a container fails.
func ErrContainerStatPathFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "stat_path",
		Err:     err,
		Message: fmt.Sprintf("Failed to stat path in container '%s'", name),
		NextSteps: []string{
			"Check if the container exists: docker ps -a",
			"Verify the path exists in the container",
		},
	}
}

// ErrContainerExecFailed returns an error for when exec operations fail.
func ErrContainerExecFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "exec",
		Err:     err,
		Message: fmt.Sprintf("Failed to execute command in container '%s'", name),
		NextSteps: []string{
			"Check if the container is running: docker ps",
			"Verify the command exists in the container",
		},
	}
}

// ErrContainerResizeFailed returns an error for when resizing a container TTY fails.
func ErrContainerResizeFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "resize",
		Err:     err,
		Message: fmt.Sprintf("Failed to resize TTY for container '%s'", name),
		NextSteps: []string{
			"Check if the container is running: docker ps",
			"Verify the container has a TTY attached",
		},
	}
}

// ErrContainerWaitFailed returns an error for when waiting on a container fails.
func ErrContainerWaitFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "wait",
		Err:     err,
		Message: fmt.Sprintf("Failed to wait for container '%s'", name),
		NextSteps: []string{
			"Check if the container exists: docker ps -a",
			"Verify Docker daemon is running",
		},
	}
}

// ErrContainerListFailed returns an error for when listing containers fails.
func ErrContainerListFailed(err error) *DockerError {
	return &DockerError{
		Op:      "list",
		Err:     err,
		Message: "Failed to list containers",
		NextSteps: []string{
			"Check if Docker daemon is running",
			"Verify Docker socket is accessible",
		},
	}
}

// ErrExecAttachFailed returns an error for when attaching to an exec instance fails.
func ErrExecAttachFailed(execID string, err error) *DockerError {
	return &DockerError{
		Op:      "exec_attach",
		Err:     err,
		Message: fmt.Sprintf("Failed to attach to exec instance '%s'", execID),
		NextSteps: []string{
			"Check if the exec instance is still valid",
			"Verify the container is still running",
		},
	}
}

// ErrExecResizeFailed returns an error for when resizing an exec instance TTY fails.
func ErrExecResizeFailed(execID string, err error) *DockerError {
	return &DockerError{
		Op:      "exec_resize",
		Err:     err,
		Message: fmt.Sprintf("Failed to resize exec TTY '%s'", execID),
		NextSteps: []string{
			"Check if the exec instance is still valid",
			"Verify the exec has a TTY attached",
		},
	}
}

// ErrVolumesPruneFailed returns an error for when pruning volumes fails.
func ErrVolumesPruneFailed(err error) *DockerError {
	return &DockerError{
		Op:      "volumes_prune",
		Err:     err,
		Message: "Failed to prune volumes",
		NextSteps: []string{
			"Check if Docker daemon is running",
			"Verify no volumes are in use",
		},
	}
}

// ErrNetworksPruneFailed returns an error for when pruning networks fails.
func ErrNetworksPruneFailed(err error) *DockerError {
	return &DockerError{
		Op:      "networks_prune",
		Err:     err,
		Message: "Failed to prune networks",
		NextSteps: []string{
			"Check if Docker daemon is running",
			"Verify no networks are in use",
		},
	}
}

// ErrImagesPruneFailed returns an error for when pruning images fails.
func ErrImagesPruneFailed(err error) *DockerError {
	return &DockerError{
		Op:      "images_prune",
		Err:     err,
		Message: "Failed to prune images",
		NextSteps: []string{
			"Check if Docker daemon is running",
			"Verify no images are in use",
		},
	}
}

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

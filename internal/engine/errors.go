package engine

import (
	"fmt"
	"strings"
)

// DockerError represents a user-friendly Docker error with remediation steps
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

// FormatUserError formats the error for display to users
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

// ErrDockerNotRunning returns an error for when Docker daemon is not accessible
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

// ErrImageNotFound returns an error for when an image cannot be found
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

// ErrImageBuildFailed returns an error for when image build fails
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

// ErrContainerNotFound returns an error for when a container cannot be found
func ErrContainerNotFound(name string) *DockerError {
	return &DockerError{
		Op:      "find",
		Err:     nil,
		Message: fmt.Sprintf("Container '%s' not found", name),
		NextSteps: []string{
			"Run 'claucker up' to start a new container",
			"Check running containers: docker ps",
			"Check all containers: docker ps -a",
		},
	}
}

// ErrContainerStartFailed returns an error for when a container fails to start
func ErrContainerStartFailed(name string, err error) *DockerError {
	return &DockerError{
		Op:      "start",
		Err:     err,
		Message: fmt.Sprintf("Failed to start container '%s'", name),
		NextSteps: []string{
			"Check container logs: docker logs " + name,
			"Verify the image is valid",
			"Check for port conflicts",
			"Try removing and recreating: claucker down --clean && claucker up",
		},
	}
}

// ErrContainerCreateFailed returns an error for when container creation fails
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

// ErrVolumeCreateFailed returns an error for when volume creation fails
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

// ErrVolumeCopyFailed returns an error for when copying to a volume fails
func ErrVolumeCopyFailed(err error) *DockerError {
	return &DockerError{
		Op:      "volume_copy",
		Err:     err,
		Message: "Failed to copy files to volume",
		NextSteps: []string{
			"Check source directory exists and is readable",
			"Verify disk space is available",
			"Check .clauckerignore for excluded files",
		},
	}
}

// ErrAttachFailed returns an error for when attaching to a container fails
func ErrAttachFailed(err error) *DockerError {
	return &DockerError{
		Op:      "attach",
		Err:     err,
		Message: "Failed to attach to container",
		NextSteps: []string{
			"Verify the container is running",
			"Check if the container has a TTY allocated",
			"Try reconnecting: claucker up",
		},
	}
}

// ErrNetworkError returns an error for network-related failures
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

// ErrNetworkCreateFailed returns an error for when network creation fails
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

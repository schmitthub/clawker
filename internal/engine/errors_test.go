package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestDockerError(t *testing.T) {
	underlying := errors.New("connection refused")
	err := &DockerError{
		Op:      "connect",
		Err:     underlying,
		Message: "Cannot connect to Docker daemon",
		NextSteps: []string{
			"Ensure Docker is running",
			"Check permissions",
		},
	}

	// Test Error() method
	if err.Error() != "Cannot connect to Docker daemon" {
		t.Errorf("Error() = %q, want %q", err.Error(), "Cannot connect to Docker daemon")
	}

	// Test Unwrap() method
	if err.Unwrap() != underlying {
		t.Error("Unwrap() should return underlying error")
	}
}

func TestDockerErrorFormatUserError(t *testing.T) {
	err := &DockerError{
		Op:      "connect",
		Err:     errors.New("connection refused"),
		Message: "Cannot connect to Docker daemon",
		NextSteps: []string{
			"Ensure Docker is running",
			"Check permissions",
		},
	}

	formatted := err.FormatUserError()

	// Should contain the error message
	if !strings.Contains(formatted, "Cannot connect to Docker daemon") {
		t.Error("FormatUserError() should contain the error message")
	}

	// Should contain details
	if !strings.Contains(formatted, "connection refused") {
		t.Error("FormatUserError() should contain underlying error details")
	}

	// Should contain next steps
	if !strings.Contains(formatted, "Next Steps:") {
		t.Error("FormatUserError() should contain 'Next Steps:' section")
	}
	if !strings.Contains(formatted, "Ensure Docker is running") {
		t.Error("FormatUserError() should contain first next step")
	}
	if !strings.Contains(formatted, "Check permissions") {
		t.Error("FormatUserError() should contain second next step")
	}
}

func TestDockerErrorFormatUserErrorNoNextSteps(t *testing.T) {
	err := &DockerError{
		Op:      "connect",
		Err:     nil,
		Message: "Simple error",
	}

	formatted := err.FormatUserError()

	if strings.Contains(formatted, "Next Steps:") {
		t.Error("FormatUserError() should not contain 'Next Steps:' when empty")
	}
}

func TestErrDockerNotRunning(t *testing.T) {
	underlying := errors.New("connection refused")
	err := ErrDockerNotRunning(underlying)

	if err.Op != "connect" {
		t.Errorf("Op = %q, want %q", err.Op, "connect")
	}
	if err.Err != underlying {
		t.Error("Err should be the underlying error")
	}
	if !strings.Contains(err.Message, "Docker daemon") {
		t.Errorf("Message should mention Docker daemon, got %q", err.Message)
	}
	if len(err.NextSteps) == 0 {
		t.Error("NextSteps should not be empty")
	}

	// Check that next steps include helpful information
	hasInstall := false
	hasStart := false
	for _, step := range err.NextSteps {
		if strings.Contains(strings.ToLower(step), "install") {
			hasInstall = true
		}
		if strings.Contains(strings.ToLower(step), "start") {
			hasStart = true
		}
	}
	if !hasInstall {
		t.Error("NextSteps should mention installation")
	}
	if !hasStart {
		t.Error("NextSteps should mention starting Docker")
	}
}

func TestErrImageNotFound(t *testing.T) {
	underlying := errors.New("not found")
	err := ErrImageNotFound("myimage:latest", underlying)

	if err.Op != "pull" {
		t.Errorf("Op = %q, want %q", err.Op, "pull")
	}
	if !strings.Contains(err.Message, "myimage:latest") {
		t.Errorf("Message should contain image name, got %q", err.Message)
	}
	if len(err.NextSteps) == 0 {
		t.Error("NextSteps should not be empty")
	}
}

func TestErrImageBuildFailed(t *testing.T) {
	underlying := errors.New("build failed")
	err := ErrImageBuildFailed(underlying)

	if err.Op != "build" {
		t.Errorf("Op = %q, want %q", err.Op, "build")
	}
	if !strings.Contains(err.Message, "build") {
		t.Errorf("Message should mention build, got %q", err.Message)
	}
}

func TestErrContainerNotFound(t *testing.T) {
	err := ErrContainerNotFound("my-container")

	if err.Op != "find" {
		t.Errorf("Op = %q, want %q", err.Op, "find")
	}
	if !strings.Contains(err.Message, "my-container") {
		t.Errorf("Message should contain container name, got %q", err.Message)
	}
	if err.Err != nil {
		t.Error("Err should be nil for not found")
	}
}

func TestErrContainerStartFailed(t *testing.T) {
	underlying := errors.New("port conflict")
	err := ErrContainerStartFailed("my-container", underlying)

	if err.Op != "start" {
		t.Errorf("Op = %q, want %q", err.Op, "start")
	}
	if !strings.Contains(err.Message, "my-container") {
		t.Errorf("Message should contain container name, got %q", err.Message)
	}
}

func TestErrContainerCreateFailed(t *testing.T) {
	underlying := errors.New("invalid config")
	err := ErrContainerCreateFailed(underlying)

	if err.Op != "create" {
		t.Errorf("Op = %q, want %q", err.Op, "create")
	}
}

func TestErrVolumeCreateFailed(t *testing.T) {
	underlying := errors.New("disk full")
	err := ErrVolumeCreateFailed("my-volume", underlying)

	if err.Op != "volume_create" {
		t.Errorf("Op = %q, want %q", err.Op, "volume_create")
	}
	if !strings.Contains(err.Message, "my-volume") {
		t.Errorf("Message should contain volume name, got %q", err.Message)
	}
}

func TestErrVolumeCopyFailed(t *testing.T) {
	underlying := errors.New("permission denied")
	err := ErrVolumeCopyFailed(underlying)

	if err.Op != "volume_copy" {
		t.Errorf("Op = %q, want %q", err.Op, "volume_copy")
	}
}

func TestErrAttachFailed(t *testing.T) {
	underlying := errors.New("not running")
	err := ErrAttachFailed(underlying)

	if err.Op != "attach" {
		t.Errorf("Op = %q, want %q", err.Op, "attach")
	}
}

func TestErrNetworkError(t *testing.T) {
	underlying := errors.New("network unreachable")
	err := ErrNetworkError(underlying)

	if err.Op != "network" {
		t.Errorf("Op = %q, want %q", err.Op, "network")
	}
}

func TestDockerErrorChaining(t *testing.T) {
	underlying := errors.New("original error")
	err := ErrDockerNotRunning(underlying)

	// Test errors.Is compatibility
	if !errors.Is(err, err) {
		t.Error("errors.Is should work with DockerError")
	}

	// Test errors.Unwrap compatibility
	unwrapped := errors.Unwrap(err)
	if unwrapped != underlying {
		t.Error("errors.Unwrap should return underlying error")
	}
}

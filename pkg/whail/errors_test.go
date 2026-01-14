package whail

import (
	"errors"
	"strings"
	"testing"
)

func TestDockerError_Error(t *testing.T) {
	err := &DockerError{
		Op:      "test",
		Message: "test error message",
	}

	if err.Error() != "test error message" {
		t.Errorf("Error() = %q, want %q", err.Error(), "test error message")
	}
}

func TestDockerError_Unwrap(t *testing.T) {
	underlying := errors.New("underlying error")
	err := &DockerError{
		Op:  "test",
		Err: underlying,
	}

	if !errors.Is(err, underlying) {
		t.Error("Unwrap() should return underlying error")
	}
}

func TestDockerError_FormatUserError(t *testing.T) {
	tests := []struct {
		name      string
		err       *DockerError
		wantParts []string
	}{
		{
			name: "basic error",
			err: &DockerError{
				Message: "Something failed",
			},
			wantParts: []string{"Error: Something failed"},
		},
		{
			name: "with underlying error",
			err: &DockerError{
				Message: "Something failed",
				Err:     errors.New("connection refused"),
			},
			wantParts: []string{"Error: Something failed", "Details: connection refused"},
		},
		{
			name: "with next steps",
			err: &DockerError{
				Message: "Something failed",
				NextSteps: []string{
					"Try this first",
					"Try this second",
				},
			},
			wantParts: []string{
				"Error: Something failed",
				"Next Steps:",
				"1. Try this first",
				"2. Try this second",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.FormatUserError()
			for _, part := range tt.wantParts {
				if !strings.Contains(got, part) {
					t.Errorf("FormatUserError() missing %q, got:\n%s", part, got)
				}
			}
		})
	}
}

func TestErrDockerNotRunning(t *testing.T) {
	underlying := errors.New("connection refused")
	err := ErrDockerNotRunning(underlying)

	if err.Op != "connect" {
		t.Errorf("Op = %q, want %q", err.Op, "connect")
	}
	if !errors.Is(err, underlying) {
		t.Error("should wrap underlying error")
	}
	if len(err.NextSteps) == 0 {
		t.Error("should have next steps")
	}
}

func TestErrImageNotFound(t *testing.T) {
	err := ErrImageNotFound("myimage:latest", nil)

	if err.Op != "pull" {
		t.Errorf("Op = %q, want %q", err.Op, "pull")
	}
	if !strings.Contains(err.Message, "myimage:latest") {
		t.Errorf("Message should contain image name, got: %s", err.Message)
	}
}

func TestErrContainerNotFound(t *testing.T) {
	err := ErrContainerNotFound("mycontainer")

	if err.Op != "find" {
		t.Errorf("Op = %q, want %q", err.Op, "find")
	}
	if !strings.Contains(err.Message, "mycontainer") {
		t.Errorf("Message should contain container name, got: %s", err.Message)
	}
}

func TestErrContainerStartFailed(t *testing.T) {
	underlying := errors.New("port already in use")
	err := ErrContainerStartFailed("mycontainer", underlying)

	if err.Op != "start" {
		t.Errorf("Op = %q, want %q", err.Op, "start")
	}
	if !strings.Contains(err.Message, "mycontainer") {
		t.Errorf("Message should contain container name, got: %s", err.Message)
	}
	if !errors.Is(err, underlying) {
		t.Error("should wrap underlying error")
	}
}

func TestErrVolumeCreateFailed(t *testing.T) {
	underlying := errors.New("disk full")
	err := ErrVolumeCreateFailed("myvolume", underlying)

	if err.Op != "volume_create" {
		t.Errorf("Op = %q, want %q", err.Op, "volume_create")
	}
	if !strings.Contains(err.Message, "myvolume") {
		t.Errorf("Message should contain volume name, got: %s", err.Message)
	}
}

func TestErrNetworkCreateFailed(t *testing.T) {
	underlying := errors.New("network exists")
	err := ErrNetworkCreateFailed("mynetwork", underlying)

	if err.Op != "network_create" {
		t.Errorf("Op = %q, want %q", err.Op, "network_create")
	}
	if !strings.Contains(err.Message, "mynetwork") {
		t.Errorf("Message should contain network name, got: %s", err.Message)
	}
}

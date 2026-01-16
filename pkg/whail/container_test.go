package whail

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

// Test container helper functions
//
// Standardized Cleanup Pattern:
// - All test functions accept (ctx context.Context, t *testing.T, name string)
// - setupFunc creates the container and returns the container ID
// - cleanupFunc is deferred immediately after setup for consistent teardown
// - Helper functions use t.Helper() to properly attribute errors to the calling test
// - Container names are explicit in test structs (no hardcoded strings in test bodies)

// setupManagedContainer creates a managed container for testing.
func setupManagedContainer(ctx context.Context, t *testing.T, name string, extraLabels ...map[string]string) string {
	t.Helper()
	resp, err := testEngine.ContainerCreate(
		ctx,
		&container.Config{
			Image:  testImageTag,
			Labels: testEngine.containerLabels(extraLabels...),
			Cmd:    []string{"sleep", "300"},
		},
		nil,
		nil,
		nil,
		name,
		extraLabels...,
	)
	if err != nil {
		t.Fatalf("Failed to create managed container %q: %v", name, err)
	}
	return resp.ID
}

// setupUnmanagedContainer creates an unmanaged container for testing.
// Uses unmanagedTag (image without managed labels) to avoid label inheritance.
func setupUnmanagedContainer(ctx context.Context, t *testing.T, name string, labels map[string]string) string {
	t.Helper()
	resp, err := testEngine.APIClient.ContainerCreate(
		ctx,
		&container.Config{
			Image:  unmanagedTag, // Use unmanaged image to avoid inheriting managed labels
			Labels: labels,
			Cmd:    []string{"sleep", "300"},
		},
		nil,
		nil,
		nil,
		name,
	)
	if err != nil {
		t.Fatalf("Failed to create unmanaged container %q: %v", name, err)
	}
	return resp.ID
}

// cleanupManagedContainer removes a managed container.
func cleanupManagedContainer(ctx context.Context, t *testing.T, containerID string) {
	t.Helper()
	if err := testEngine.ContainerRemove(ctx, containerID, true); err != nil {
		t.Logf("Warning: Failed to cleanup managed container %q: %v", containerID, err)
	}
}

// cleanupUnmanagedContainer removes an unmanaged container.
func cleanupUnmanagedContainer(ctx context.Context, t *testing.T, containerID string) {
	t.Helper()
	if err := testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		t.Logf("Warning: Failed to cleanup unmanaged container %q: %v", containerID, err)
	}
}

// generateContainerName creates a unique container name for testing.
func generateContainerName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func TestContainerCreate(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		extraLabels   map[string]string
		shouldErr     bool
	}{
		{
			name:          "should create container with managed labels",
			containerName: generateContainerName("test-container-create"),
			extraLabels:   map[string]string{"test.label": "value"},
			shouldErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			resp, err := testEngine.ContainerCreate(
				ctx,
				&container.Config{
					Image: testImageTag,
					Cmd:   []string{"sleep", "300"},
				},
				nil,
				nil,
				nil,
				tt.containerName,
				tt.extraLabels,
			)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerCreate failed: %v", err)
			}

			// Verify container was created
			if resp.ID == "" {
				t.Fatalf("Expected container ID, got empty string")
			}

			// Cleanup
			defer testEngine.APIClient.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})

			// Verify labels applied
			inspect, err := testEngine.APIClient.ContainerInspect(ctx, resp.ID)
			if err != nil {
				t.Fatalf("Failed to inspect created container: %v", err)
			}

			// Check managed label
			containerLabels := testEngine.containerLabels(tt.extraLabels)
			for k, v := range containerLabels {
				if inspect.Config.Labels[k] != v {
					t.Errorf("Expected label %q=%q, got %q", k, v, inspect.Config.Labels[k])
				}
			}
		})
	}
}

func TestContainerStart(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should start managed container",
			containerName: generateContainerName("test-container-start-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupManagedContainer(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not start unmanaged container",
			containerName: generateContainerName("test-container-start-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			err := testEngine.ContainerStart(ctx, containerID, container.StartOptions{})
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerStart failed: %v", err)
			}
		})
	}
}

func TestContainerStop(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should stop managed container",
			containerName: generateContainerName("test-container-stop-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				// Start the container so we can stop it
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for stop test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not stop unmanaged container",
			containerName: generateContainerName("test-container-stop-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				// Start the container
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for stop test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			err := testEngine.ContainerStop(ctx, containerID, nil)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerStop failed: %v", err)
			}
		})
	}
}

func TestContainerRemove(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should remove managed container",
			containerName: generateContainerName("test-container-remove-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupManagedContainer(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				// No cleanup needed - we're testing removal
			},
			shouldErr: false,
		},
		{
			name:          "should not remove unmanaged container",
			containerName: generateContainerName("test-container-remove-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			err := testEngine.ContainerRemove(ctx, containerID, true)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerRemove failed: %v", err)
			}

			// Verify removal
			_, err = testEngine.APIClient.ContainerInspect(ctx, containerID)
			if err == nil {
				t.Fatalf("Expected container to be removed, but it still exists")
			}
		})
	}
}

func TestContainerInspect(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should inspect managed container",
			containerName: generateContainerName("test-container-inspect-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupManagedContainer(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not inspect unmanaged container",
			containerName: generateContainerName("test-container-inspect-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			info, err := testEngine.ContainerInspect(ctx, containerID)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerInspect failed: %v", err)
			}

			if info.ID != containerID {
				t.Errorf("Expected container ID %q, got %q", containerID, info.ID)
			}
		})
	}
}

func TestContainerList(t *testing.T) {
	tests := []struct {
		name          string
		setupFunc     func(ctx context.Context, t *testing.T) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldBeFound bool
	}{
		{
			name: "should return managed containers",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateContainerName("test-container-list-managed")
				return setupManagedContainer(ctx, t, name, map[string]string{"test.filter": "managed"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldBeFound: true,
		},
		{
			name: "should not return unmanaged containers",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateContainerName("test-container-list-unmanaged")
				return setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldBeFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t)
			defer tt.cleanupFunc(ctx, t, containerID)

			containers, err := testEngine.ContainerList(ctx, container.ListOptions{All: true})
			if err != nil {
				t.Fatalf("ContainerList failed: %v", err)
			}

			found := false
			for _, c := range containers {
				if c.ID == containerID {
					found = true
					break
				}
			}

			if found != tt.shouldBeFound {
				t.Errorf("Expected container %q to be found: %v, but got: %v", containerID, tt.shouldBeFound, found)
			}
		})
	}
}

func TestContainerLogs(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should get logs for managed container",
			containerName: generateContainerName("test-container-logs-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				// Start the container to generate logs
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for logs test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not get logs for unmanaged container",
			containerName: generateContainerName("test-container-logs-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				// Start the container
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for logs test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			reader, err := testEngine.ContainerLogs(ctx, containerID, container.LogsOptions{
				ShowStdout: true,
				ShowStderr: true,
			})
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerLogs failed: %v", err)
			}
			defer reader.Close()
		})
	}
}

func TestIsContainerManaged(t *testing.T) {
	tests := []struct {
		name        string
		setupFunc   func(ctx context.Context, t *testing.T) string
		cleanupFunc func(ctx context.Context, t *testing.T, containerID string)
		expected    bool
	}{
		{
			name: "should return true for managed container",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateContainerName("test-container-managed-check")
				return setupManagedContainer(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			expected: true,
		},
		{
			name: "should return false for unmanaged container",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateContainerName("test-container-unmanaged-check")
				return setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			expected: false,
		},
		{
			name: "should return false for non-existing container",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				return "nonexistent-container-id"
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t)
			defer tt.cleanupFunc(ctx, t, containerID)

			isManaged, err := testEngine.IsContainerManaged(ctx, containerID)
			if err != nil && tt.expected {
				t.Fatalf("IsContainerManaged failed: %v", err)
			}

			if isManaged != tt.expected {
				t.Errorf("Expected isManaged=%v, got %v", tt.expected, isManaged)
			}
		})
	}
}

func TestContainerKill(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should kill managed container",
			containerName: generateContainerName("test-container-kill-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for kill test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not kill unmanaged container",
			containerName: generateContainerName("test-container-kill-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for kill test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerKill(ctx, containerID, "SIGKILL")
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			err := testEngine.ContainerKill(ctx, containerID, "SIGKILL")
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerKill failed: %v", err)
			}
		})
	}
}

func TestContainerPauseUnpause(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should pause and unpause managed container",
			containerName: generateContainerName("test-container-pause-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for pause test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerUnpause(ctx, containerID)
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not pause unmanaged container",
			containerName: generateContainerName("test-container-pause-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for pause test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			// Test pause
			err := testEngine.ContainerPause(ctx, containerID)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("ContainerPause: Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerPause failed: %v", err)
			}

			// Verify paused
			info, err := testEngine.APIClient.ContainerInspect(ctx, containerID)
			if err != nil {
				t.Fatalf("Failed to inspect container: %v", err)
			}
			if !info.State.Paused {
				t.Errorf("Expected container to be paused")
			}

			// Test unpause
			err = testEngine.ContainerUnpause(ctx, containerID)
			if err != nil {
				t.Fatalf("ContainerUnpause failed: %v", err)
			}

			// Verify unpaused
			info, err = testEngine.APIClient.ContainerInspect(ctx, containerID)
			if err != nil {
				t.Fatalf("Failed to inspect container: %v", err)
			}
			if info.State.Paused {
				t.Errorf("Expected container to be unpaused")
			}
		})
	}
}

func TestContainerRestart(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should restart managed container",
			containerName: generateContainerName("test-container-restart-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for restart test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not restart unmanaged container",
			containerName: generateContainerName("test-container-restart-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for restart test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			timeout := 5
			err := testEngine.ContainerRestart(ctx, containerID, &timeout)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerRestart failed: %v", err)
			}

			// Verify container is running after restart
			info, err := testEngine.APIClient.ContainerInspect(ctx, containerID)
			if err != nil {
				t.Fatalf("Failed to inspect container: %v", err)
			}
			if !info.State.Running {
				t.Errorf("Expected container to be running after restart")
			}
		})
	}
}

func TestContainerRename(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		newName       string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID, cleanupName string)
		shouldErr     bool
	}{
		{
			name:          "should rename managed container",
			containerName: generateContainerName("test-container-rename-managed"),
			newName:       generateContainerName("test-container-renamed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupManagedContainer(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID, cleanupName string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not rename unmanaged container",
			containerName: generateContainerName("test-container-rename-unmanaged"),
			newName:       generateContainerName("test-container-renamed-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID, cleanupName string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			cleanupName := tt.containerName
			if !tt.shouldErr {
				cleanupName = tt.newName
			}
			defer tt.cleanupFunc(ctx, t, containerID, cleanupName)

			err := testEngine.ContainerRename(ctx, containerID, tt.newName)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerRename failed: %v", err)
			}

			// Verify new name
			info, err := testEngine.APIClient.ContainerInspect(ctx, containerID)
			if err != nil {
				t.Fatalf("Failed to inspect container: %v", err)
			}
			if info.Name != "/"+tt.newName {
				t.Errorf("Expected container name %q, got %q", "/"+tt.newName, info.Name)
			}
		})
	}
}

func TestContainerTop(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should get top for managed container",
			containerName: generateContainerName("test-container-top-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for top test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not get top for unmanaged container",
			containerName: generateContainerName("test-container-top-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for top test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			top, err := testEngine.ContainerTop(ctx, containerID, nil)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerTop failed: %v", err)
			}

			// Verify we got process info
			if len(top.Titles) == 0 {
				t.Errorf("Expected process titles, got empty")
			}
		})
	}
}

func TestContainerStats(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should get stats for managed container",
			containerName: generateContainerName("test-container-stats-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for stats test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not get stats for unmanaged container",
			containerName: generateContainerName("test-container-stats-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for stats test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			// Test non-streaming stats (one-shot)
			reader, err := testEngine.ContainerStats(ctx, containerID, false)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerStats failed: %v", err)
			}
			defer reader.Close()
		})
	}
}

func TestContainerStatsOneShot(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should get one-shot stats for managed container",
			containerName: generateContainerName("test-container-stats-oneshot-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for stats test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not get one-shot stats for unmanaged container",
			containerName: generateContainerName("test-container-stats-oneshot-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for stats test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			stats, err := testEngine.ContainerStatsOneShot(ctx, containerID)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerStatsOneShot failed: %v", err)
			}
			defer stats.Body.Close()
		})
	}
}

func TestContainerUpdate(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should update managed container",
			containerName: generateContainerName("test-container-update-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for update test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not update unmanaged container",
			containerName: generateContainerName("test-container-update-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for update test: %v", err)
				}
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			// Update with minimal config change
			_, err := testEngine.ContainerUpdate(ctx, containerID, container.UpdateConfig{})
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerUpdate failed: %v", err)
			}
		})
	}
}

func TestContainerListAll(t *testing.T) {
	ctx := context.Background()

	// Create a managed container (stopped)
	name := generateContainerName("test-container-listall")
	containerID := setupManagedContainer(ctx, t, name)
	defer testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})

	// List all (including stopped)
	containers, err := testEngine.ContainerListAll(ctx)
	if err != nil {
		t.Fatalf("ContainerListAll failed: %v", err)
	}

	// Verify our container is in the list
	found := false
	for _, c := range containers {
		if c.ID == containerID {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Expected to find container %q in list", containerID)
	}
}

func TestContainerListRunning(t *testing.T) {
	ctx := context.Background()

	// Create and start a managed container
	name := generateContainerName("test-container-listrunning")
	containerID := setupManagedContainer(ctx, t, name)
	defer func() {
		testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
		testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
	}()

	if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	// List running only
	containers, err := testEngine.ContainerListRunning(ctx)
	if err != nil {
		t.Fatalf("ContainerListRunning failed: %v", err)
	}

	// Verify our container is in the list
	found := false
	for _, c := range containers {
		if c.ID == containerID {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Expected to find running container %q in list", containerID)
	}
}

func TestContainerListByLabels(t *testing.T) {
	ctx := context.Background()

	// Create a managed container with extra labels
	name := generateContainerName("test-container-listbylabels")
	extraLabels := map[string]string{"test.filter.label": "unique-value"}
	containerID := setupManagedContainer(ctx, t, name, extraLabels)
	defer testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})

	// List by the extra label
	containers, err := testEngine.ContainerListByLabels(ctx, extraLabels, true)
	if err != nil {
		t.Fatalf("ContainerListByLabels failed: %v", err)
	}

	// Verify our container is in the list
	found := false
	for _, c := range containers {
		if c.ID == containerID {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("Expected to find container %q with label filter", containerID)
	}
}

func TestFindContainerByName(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should find managed container by name",
			containerName: generateContainerName("test-find-by-name-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupManagedContainer(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not find unmanaged container by name",
			containerName: generateContainerName("test-find-by-name-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
		{
			name:          "should return error for non-existent container",
			containerName: "nonexistent-container-name",
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return "" // No container to setup
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {},
			shouldErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID != "" {
				defer tt.cleanupFunc(ctx, t, containerID)
			}

			found, err := testEngine.FindContainerByName(ctx, tt.containerName)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("FindContainerByName failed: %v", err)
			}

			if found.ID != containerID {
				t.Errorf("Expected container ID %q, got %q", containerID, found.ID)
			}
		})
	}
}

func TestContainerKillDefaultSignal(t *testing.T) {
	ctx := context.Background()

	// Create and start a managed container
	name := generateContainerName("test-container-kill-default")
	containerID := setupManagedContainer(ctx, t, name)
	defer testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})

	if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	// Test with empty signal (should default to SIGKILL)
	err := testEngine.ContainerKill(ctx, containerID, "")
	if err != nil {
		t.Fatalf("ContainerKill with empty signal failed: %v", err)
	}
}

func TestContainerRestartNilTimeout(t *testing.T) {
	ctx := context.Background()

	// Create and start a managed container
	name := generateContainerName("test-container-restart-nil")
	containerID := setupManagedContainer(ctx, t, name)
	defer func() {
		testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
		testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
	}()

	if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	// Test with nil timeout (should use Docker default)
	err := testEngine.ContainerRestart(ctx, containerID, nil)
	if err != nil {
		t.Fatalf("ContainerRestart with nil timeout failed: %v", err)
	}

	// Verify container is running after restart
	info, err := testEngine.APIClient.ContainerInspect(ctx, containerID)
	if err != nil {
		t.Fatalf("Failed to inspect container: %v", err)
	}
	if !info.State.Running {
		t.Errorf("Expected container to be running after restart")
	}
}

func TestContainerWait(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
		checkNilWait  bool // whether to check that waitCh is nil (unmanaged case)
	}{
		{
			name:          "should wait for managed container to exit",
			containerName: generateContainerName("test-container-wait-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				// Create a container that exits immediately
				resp, err := testEngine.ContainerCreate(
					ctx,
					&container.Config{
						Image:  testImageTag,
						Labels: testEngine.containerLabels(),
						Cmd:    []string{"true"}, // Exits immediately with 0
					},
					nil,
					nil,
					nil,
					name,
				)
				if err != nil {
					t.Fatalf("Failed to create managed container: %v", err)
				}
				return resp.ID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr:    false,
			checkNilWait: false,
		},
		{
			name:          "should return error for unmanaged container",
			containerName: generateContainerName("test-container-wait-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr:    true,
			checkNilWait: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			// Start container for the managed case
			if !tt.shouldErr {
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container: %v", err)
				}
			}

			waitCh, errCh := testEngine.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)

			if tt.shouldErr {
				// For unmanaged containers, waitCh should be nil
				if tt.checkNilWait && waitCh != nil {
					t.Errorf("Expected nil wait channel for unmanaged container, got non-nil")
				}
				// Error channel should contain an error
				select {
				case err := <-errCh:
					if err == nil {
						t.Fatalf("Expected error in error channel but got nil")
					}
					// Verify it's the right error type
					var dockerErr *DockerError
					if ok := isDockerError(err, &dockerErr); !ok {
						t.Errorf("Expected *DockerError, got %T", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("Timeout waiting for error channel")
				}
				return
			}

			// Wait for container to exit
			select {
			case result := <-waitCh:
				if result.StatusCode != 0 {
					t.Errorf("Expected exit code 0, got %d", result.StatusCode)
				}
			case err := <-errCh:
				if err != nil {
					t.Fatalf("ContainerWait failed: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("Timeout waiting for container to exit")
			}
		})
	}
}

// isDockerError checks if an error is a DockerError and extracts it.
func isDockerError(err error, target **DockerError) bool {
	var dockerErr *DockerError
	if ok := isErrorType(err, &dockerErr); ok {
		*target = dockerErr
		return true
	}
	return false
}

// isErrorType is a helper for errors.As without importing errors package.
func isErrorType(err error, target interface{}) bool {
	if err == nil {
		return false
	}
	// Check if the error matches directly
	if de, ok := err.(*DockerError); ok {
		if t, ok := target.(**DockerError); ok {
			*t = de
			return true
		}
	}
	return false
}

func TestContainerAttach(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should attach to managed container",
			containerName: generateContainerName("test-container-attach-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				// Create a container with stdin/tty enabled for attach
				resp, err := testEngine.ContainerCreate(
					ctx,
					&container.Config{
						Image:       testImageTag,
						Labels:      testEngine.containerLabels(),
						Cmd:         []string{"sleep", "300"},
						OpenStdin:   true,
						Tty:         true,
						StdinOnce:   false,
						AttachStdin: true,
					},
					nil,
					nil,
					nil,
					name,
				)
				if err != nil {
					t.Fatalf("Failed to create managed container: %v", err)
				}
				return resp.ID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerStop(ctx, containerID, container.StopOptions{})
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: false,
		},
		{
			name:          "should not attach to unmanaged container",
			containerName: generateContainerName("test-container-attach-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				return containerID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, containerID string) {
				testEngine.APIClient.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			containerID := tt.setupFunc(ctx, t, tt.containerName)
			if containerID == "" {
				t.Fatalf("Setup failed: container ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, containerID)

			// Start container for attach
			if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
				t.Fatalf("Failed to start container: %v", err)
			}

			// Test ContainerAttach
			attachOpts := container.AttachOptions{
				Stream: true,
				Stdin:  true,
				Stdout: true,
				Stderr: true,
			}
			resp, err := testEngine.ContainerAttach(ctx, containerID, attachOpts)

			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("ContainerAttach failed: %v", err)
			}
			defer resp.Close()

			// Verify we got a valid response
			if resp.Conn == nil {
				t.Errorf("Expected non-nil connection in attach response")
			}
		})
	}
}

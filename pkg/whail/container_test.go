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

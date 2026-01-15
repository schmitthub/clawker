package whail

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

// generateCopyContainerName creates a unique container name for copy tests.
func generateCopyContainerName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// createTarContent creates a tar archive with a single file.
func createTarContent(filename, content string) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)

	hdr := &tar.Header{
		Name: filename,
		Mode: 0644,
		Size: int64(len(content)),
	}

	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}

	if _, err := tw.Write([]byte(content)); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf, nil
}

func TestCopyToContainer(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should copy to managed container",
			containerName: generateCopyContainerName("test-copy-to-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for copy test: %v", err)
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
			name:          "should not copy to unmanaged container",
			containerName: generateCopyContainerName("test-copy-to-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for copy test: %v", err)
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

			// Create tar content to copy
			tarContent, err := createTarContent("testfile.txt", "test content")
			if err != nil {
				t.Fatalf("Failed to create tar content: %v", err)
			}

			err = testEngine.CopyToContainer(ctx, containerID, "/tmp", tarContent, container.CopyToContainerOptions{})
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("CopyToContainer failed: %v", err)
			}
		})
	}
}

func TestCopyFromContainer(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should copy from managed container",
			containerName: generateCopyContainerName("test-copy-from-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for copy test: %v", err)
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
			name:          "should not copy from unmanaged container",
			containerName: generateCopyContainerName("test-copy-from-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for copy test: %v", err)
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

			// Copy /etc/hostname which should exist in alpine
			reader, stat, err := testEngine.CopyFromContainer(ctx, containerID, "/etc/hostname")
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("CopyFromContainer failed: %v", err)
			}
			defer reader.Close()

			// Verify we got valid stat info
			if stat.Name == "" {
				t.Errorf("Expected non-empty stat name")
			}

			// Read some content to verify the reader works
			content, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("Failed to read from container: %v", err)
			}
			if len(content) == 0 {
				t.Errorf("Expected non-empty content")
			}
		})
	}
}

func TestContainerStatPath(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		path          string
		setupFunc     func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc   func(ctx context.Context, t *testing.T, containerID string)
		shouldErr     bool
	}{
		{
			name:          "should stat path in managed container",
			containerName: generateCopyContainerName("test-stat-path-managed"),
			path:          "/etc/hostname",
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupManagedContainer(ctx, t, name)
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for stat test: %v", err)
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
			name:          "should not stat path in unmanaged container",
			containerName: generateCopyContainerName("test-stat-path-unmanaged"),
			path:          "/etc/hostname",
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				containerID := setupUnmanagedContainer(ctx, t, name, map[string]string{"other.label": "value"})
				if err := testEngine.APIClient.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
					t.Fatalf("Failed to start container for stat test: %v", err)
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

			stat, err := testEngine.ContainerStatPath(ctx, containerID, tt.path)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContainerStatPath failed: %v", err)
			}

			// Verify we got valid stat info
			if stat.Name == "" {
				t.Errorf("Expected non-empty stat name")
			}
		})
	}
}

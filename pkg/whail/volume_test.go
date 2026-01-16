package whail

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/docker/docker/api/types/volume"
)

// Test volume helper functions
//
// Standardized Cleanup Pattern:
// - All test functions accept (ctx context.Context, t *testing.T, name string)
// - setupFunc creates the volume and returns the volume name
// - cleanupFunc is deferred immediately after setup for consistent teardown
// - Helper functions use t.Helper() to properly attribute errors to the calling test
// - Volume names are explicit in test structs (no hardcoded strings in test bodies)

// setupManagedVolume creates a managed volume for testing.
func setupManagedVolume(ctx context.Context, t *testing.T, name string, extraLabels ...map[string]string) string {
	t.Helper()
	vol, err := testEngine.VolumeCreate(ctx, volume.CreateOptions{Name: name}, extraLabels...)
	if err != nil {
		t.Fatalf("Failed to create managed volume %q: %v", name, err)
	}
	return vol.Name
}

// setupUnmanagedVolume creates an unmanaged volume for testing.
func setupUnmanagedVolume(ctx context.Context, t *testing.T, name string, labels map[string]string) string {
	t.Helper()
	vol, err := testEngine.APIClient.VolumeCreate(ctx, volume.CreateOptions{
		Name:   name,
		Labels: labels,
	})
	if err != nil {
		t.Fatalf("Failed to create unmanaged volume %q: %v", name, err)
	}
	return vol.Name
}

// cleanupManagedVolume removes a managed volume.
func cleanupManagedVolume(ctx context.Context, t *testing.T, name string) {
	t.Helper()
	if err := testEngine.VolumeRemove(ctx, name, true); err != nil {
		t.Logf("Warning: Failed to cleanup managed volume %q: %v", name, err)
	}
}

// cleanupUnmanagedVolume removes an unmanaged volume.
func cleanupUnmanagedVolume(ctx context.Context, t *testing.T, name string) {
	t.Helper()
	if err := testEngine.APIClient.VolumeRemove(ctx, name, true); err != nil {
		t.Logf("Warning: Failed to cleanup unmanaged volume %q: %v", name, err)
	}
}

// generateVolumeName creates a unique volume name for testing.
func generateVolumeName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func TestVolumeCreate(t *testing.T) {
	tests := []struct {
		name        string
		volumeName  string
		extraLabels map[string]string
		shouldErr   bool
	}{
		{
			name:        "should create volume with managed labels",
			volumeName:  generateVolumeName("test-volume-create"),
			extraLabels: map[string]string{"test.label": "value"},
			shouldErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			vol, err := testEngine.VolumeCreate(ctx, volume.CreateOptions{Name: tt.volumeName}, tt.extraLabels)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("VolumeCreate failed: %v", err)
			}

			// Verify volume was created
			if vol.Name == "" {
				t.Fatalf("Expected volume name, got empty string")
			}

			// Cleanup
			defer testEngine.APIClient.VolumeRemove(ctx, vol.Name, true)

			// Verify labels applied
			inspect, err := testEngine.APIClient.VolumeInspect(ctx, vol.Name)
			if err != nil {
				t.Fatalf("Failed to inspect created volume: %v", err)
			}

			// Check managed label
			volumeLabels := testEngine.volumeLabels(tt.extraLabels)
			for k, v := range volumeLabels {
				if inspect.Labels[k] != v {
					t.Errorf("Expected label %q=%q, got %q", k, v, inspect.Labels[k])
				}
			}
		})
	}
}

func TestVolumeRemove(t *testing.T) {
	tests := []struct {
		name        string
		volumeName  string
		setupFunc   func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc func(ctx context.Context, t *testing.T, name string)
		shouldErr   bool
	}{
		{
			name:       "should remove managed volume",
			volumeName: generateVolumeName("test-volume-remove-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupManagedVolume(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {
				// No cleanup needed - we're testing removal
			},
			shouldErr: false,
		},
		{
			name:       "should not remove unmanaged volume",
			volumeName: generateVolumeName("test-volume-remove-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupUnmanagedVolume(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {
				testEngine.APIClient.VolumeRemove(ctx, name, true)
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			volumeName := tt.setupFunc(ctx, t, tt.volumeName)
			if volumeName == "" {
				t.Fatalf("Setup failed: volume name is empty")
			}
			defer tt.cleanupFunc(ctx, t, volumeName)

			err := testEngine.VolumeRemove(ctx, volumeName, true)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("VolumeRemove failed: %v", err)
			}

			// Verify removal
			exists, err := testEngine.VolumeExists(ctx, volumeName)
			if err != nil {
				t.Fatalf("Failed to check volume existence: %v", err)
			}
			if exists {
				t.Fatalf("Expected volume to be removed, but it still exists")
			}
		})
	}
}

func TestVolumeInspect(t *testing.T) {
	tests := []struct {
		name        string
		volumeName  string
		setupFunc   func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc func(ctx context.Context, t *testing.T, name string)
		shouldErr   bool
	}{
		{
			name:       "should inspect managed volume",
			volumeName: generateVolumeName("test-volume-inspect-managed"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupManagedVolume(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {
				testEngine.VolumeRemove(ctx, name, true)
			},
			shouldErr: false,
		},
		{
			name:       "should not inspect unmanaged volume",
			volumeName: generateVolumeName("test-volume-inspect-unmanaged"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				return setupUnmanagedVolume(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {
				testEngine.APIClient.VolumeRemove(ctx, name, true)
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			volumeName := tt.setupFunc(ctx, t, tt.volumeName)
			if volumeName == "" {
				t.Fatalf("Setup failed: volume name is empty")
			}
			defer tt.cleanupFunc(ctx, t, volumeName)

			info, err := testEngine.VolumeInspect(ctx, volumeName)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("VolumeInspect failed: %v", err)
			}

			if info.Name != volumeName {
				t.Errorf("Expected volume name %q, got %q", volumeName, info.Name)
			}
		})
	}
}

func TestVolumeExists(t *testing.T) {
	tests := []struct {
		name        string
		volumeName  string
		setupFunc   func(ctx context.Context, t *testing.T, name string)
		cleanupFunc func(ctx context.Context, t *testing.T, name string)
		expected    bool
	}{
		{
			name:       "should return true for existing volume",
			volumeName: generateVolumeName("test-volume-exists"),
			setupFunc: func(ctx context.Context, t *testing.T, name string) {
				setupManagedVolume(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {
				testEngine.VolumeRemove(ctx, name, true)
			},
			expected: true,
		},
		{
			name:        "should return false for non-existing volume",
			volumeName:  "test-volume-does-not-exist",
			setupFunc:   func(ctx context.Context, t *testing.T, name string) {},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			tt.setupFunc(ctx, t, tt.volumeName)
			defer tt.cleanupFunc(ctx, t, tt.volumeName)

			exists, err := testEngine.VolumeExists(ctx, tt.volumeName)
			if err != nil {
				t.Fatalf("VolumeExists failed: %v", err)
			}

			if exists != tt.expected {
				t.Errorf("Expected exists=%v, got %v", tt.expected, exists)
			}
		})
	}
}

func TestVolumeList(t *testing.T) {
	tests := []struct {
		name          string
		setupFunc     func(ctx context.Context, t *testing.T) string
		cleanupFunc   func(ctx context.Context, t *testing.T, volumeName string)
		extraFilters  map[string]string
		shouldBeFound bool
	}{
		{
			name: "should return managed volumes",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateVolumeName("test-volume-list-managed")
				return setupManagedVolume(ctx, t, name, map[string]string{"test.filter": "managed"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, volumeName string) {
				testEngine.VolumeRemove(ctx, volumeName, true)
			},
			extraFilters:  map[string]string{"test.filter": "managed"},
			shouldBeFound: true,
		},
		{
			name: "should not return unmanaged volumes",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateVolumeName("test-volume-list-unmanaged")
				return setupUnmanagedVolume(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, volumeName string) {
				testEngine.APIClient.VolumeRemove(ctx, volumeName, true)
			},
			extraFilters:  map[string]string{},
			shouldBeFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			volumeName := tt.setupFunc(ctx, t)
			defer tt.cleanupFunc(ctx, t, volumeName)

			resp, err := testEngine.VolumeList(ctx, tt.extraFilters)
			if err != nil {
				t.Fatalf("VolumeList failed: %v", err)
			}

			found := false
			for _, vol := range resp.Volumes {
				if vol.Name == volumeName {
					found = true
					break
				}
			}

			if found != tt.shouldBeFound {
				t.Errorf("Expected volume %q to be found: %v, but got: %v", volumeName, tt.shouldBeFound, found)
			}
		})
	}
}

func TestIsVolumeManaged(t *testing.T) {
	tests := []struct {
		name        string
		setupFunc   func(ctx context.Context, t *testing.T) string
		cleanupFunc func(ctx context.Context, t *testing.T, volumeName string)
		expected    bool
	}{
		{
			name: "should return true for managed volume",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateVolumeName("test-volume-managed-check")
				return setupManagedVolume(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, volumeName string) {
				testEngine.VolumeRemove(ctx, volumeName, true)
			},
			expected: true,
		},
		{
			name: "should return false for unmanaged volume",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateVolumeName("test-volume-unmanaged-check")
				return setupUnmanagedVolume(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, volumeName string) {
				testEngine.APIClient.VolumeRemove(ctx, volumeName, true)
			},
			expected: false,
		},
		{
			name: "should return false for non-existing volume",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				return "nonexistent-volume"
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, volumeName string) {},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			volumeName := tt.setupFunc(ctx, t)
			defer tt.cleanupFunc(ctx, t, volumeName)

			isManaged, err := testEngine.IsVolumeManaged(ctx, volumeName)
			if err != nil && tt.expected {
				t.Fatalf("IsVolumeManaged failed: %v", err)
			}

			if isManaged != tt.expected {
				t.Errorf("Expected isManaged=%v, got %v", tt.expected, isManaged)
			}
		})
	}
}


func TestVolumesPrune(t *testing.T) {
	tests := []struct {
		name                string
		setupFunc           func(ctx context.Context, t *testing.T) string
		cleanupFunc         func(ctx context.Context, t *testing.T, volumeName string)
		shouldBeRemoved     bool
		skipUnmanagedCleanup bool
	}{
		{
			name: "should prune managed volumes",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateVolumeName("test-volume-prune-managed")
				return setupManagedVolume(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, volumeName string) {
				// Volume should be pruned, but try cleanup anyway in case test fails
				testEngine.APIClient.VolumeRemove(ctx, volumeName, true)
			},
			shouldBeRemoved: true,
		},
		{
			name: "should not prune unmanaged volumes",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateVolumeName("test-volume-prune-unmanaged")
				return setupUnmanagedVolume(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, volumeName string) {
				testEngine.APIClient.VolumeRemove(ctx, volumeName, true)
			},
			shouldBeRemoved: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			volumeName := tt.setupFunc(ctx, t)
			defer tt.cleanupFunc(ctx, t, volumeName)

			// Verify volume exists before prune
			exists, err := testEngine.VolumeExists(ctx, volumeName)
			if err != nil {
				t.Fatalf("Failed to check if volume exists before prune: %v", err)
			}
			if !exists {
				t.Fatalf("Volume should exist before prune")
			}

			// Prune volumes (all=true to include named volumes)
			_, err = testEngine.VolumesPrune(ctx, true)
			if err != nil {
				t.Fatalf("VolumesPrune failed: %v", err)
			}

			// Check if volume still exists
			exists, err = testEngine.VolumeExists(ctx, volumeName)
			if err != nil {
				t.Fatalf("Failed to check if volume exists after prune: %v", err)
			}

			if tt.shouldBeRemoved && exists {
				t.Errorf("Expected managed volume %q to be pruned, but it still exists", volumeName)
			}
			if !tt.shouldBeRemoved && !exists {
				t.Errorf("Expected unmanaged volume %q to NOT be pruned, but it was removed", volumeName)
			}
		})
	}
}

package whail

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/docker/docker/api/types/network"
)

// Test network helper functions
//
// Standardized Cleanup Pattern:
// - All test functions accept (ctx context.Context, t *testing.T, name string)
// - setupFunc creates the network and returns the network ID
// - cleanupFunc is deferred immediately after setup for consistent teardown
// - Helper functions (setupManagedNetwork, cleanupManagedNetwork, etc.) use t.Helper()
//   to properly attribute errors to the calling test
// - Network names are explicit in test structs (no hardcoded strings in test bodies)

// setupManagedNetwork creates a managed network for testing.
func setupManagedNetwork(ctx context.Context, t *testing.T, name string, extraLabels ...map[string]string) string {
	t.Helper()
	resp, err := testEngine.NetworkCreate(ctx, name, network.CreateOptions{}, extraLabels...)
	if err != nil {
		t.Fatalf("Failed to create managed network %q: %v", name, err)
	}
	return resp.ID
}

// setupUnmanagedNetwork creates an unmanaged network for testing.
func setupUnmanagedNetwork(ctx context.Context, t *testing.T, name string, labels map[string]string) string {
	t.Helper()
	resp, err := testEngine.APIClient.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: labels,
	})
	if err != nil {
		t.Fatalf("Failed to create unmanaged network %q: %v", name, err)
	}
	return resp.ID
}

// cleanupManagedNetwork removes a managed network.
func cleanupManagedNetwork(ctx context.Context, t *testing.T, name string) {
	t.Helper()
	if err := testEngine.NetworkRemove(ctx, name); err != nil {
		t.Logf("Warning: Failed to cleanup managed network %q: %v", name, err)
	}
}

// cleanupUnmanagedNetwork removes an unmanaged network.
func cleanupUnmanagedNetwork(ctx context.Context, t *testing.T, name string) {
	t.Helper()
	if err := testEngine.APIClient.NetworkRemove(ctx, name); err != nil {
		t.Logf("Warning: Failed to cleanup unmanaged network %q: %v", name, err)
	}
}

// generateNetworkName creates a unique network name for testing.
func generateNetworkName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func TestNetworkCreate(t *testing.T) {
	tests := []struct {
		name        string
		networkName string
		extraLabels map[string]string
		shouldErr   bool
	}{
		{
			name:        "should create network with managed labels",
			networkName: "test-network-create",
			extraLabels: map[string]string{"test.label": "value"},
			shouldErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			resp, err := testEngine.NetworkCreate(ctx, tt.networkName, network.CreateOptions{}, tt.extraLabels)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("NetworkCreate failed: %v", err)
			}

			// Verify network was created
			if resp.ID == "" {
				t.Fatalf("Expected network ID, got empty string")
			}

			// Verify labels applied
			inspect, err := testEngine.APIClient.NetworkInspect(ctx, tt.networkName, network.InspectOptions{})
			if err != nil {
				t.Fatalf("Failed to inspect created network: %v", err)
			}

			// Cleanup
			defer testEngine.APIClient.NetworkRemove(ctx, tt.networkName)

			// Check managed label
			networkLabels := testEngine.networkLabels(tt.extraLabels)
			for k, v := range networkLabels {
				if inspect.Labels[k] != v {
					t.Errorf("Expected label %q=%q, got %q", k, v, inspect.Labels[k])
				}
			}
		})
	}
}

func TestNetworkRemove(t *testing.T) {
	tests := []struct {
		name        string
		networkName string
		setupFunc   func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc func(ctx context.Context, t *testing.T, name string)
		shouldErr   bool
	}{
		{
			name:        "should remove managed network",
			networkName: "test-network-remove-managed",
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				resp, err := testEngine.NetworkCreate(ctx, name, network.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create network: %v", err)
				}
				return resp.ID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {},
			shouldErr:   false,
		},
		{
			name:        "should not remove unmanaged network",
			networkName: "test-network-remove-unmanaged",
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				// Create unmanaged network directly with API client
				resp, err := testEngine.APIClient.NetworkCreate(ctx, name, network.CreateOptions{
					Driver: "bridge",
					Labels: map[string]string{"other.label": "value"},
				})
				if err != nil {
					t.Fatalf("Failed to create unmanaged network: %v", err)
				}
				return resp.ID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {
				// Cleanup unmanaged network
				testEngine.APIClient.NetworkRemove(ctx, name)
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			networkID := tt.setupFunc(ctx, t, tt.networkName)
			if networkID == "" {
				t.Fatalf("Setup failed: network ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, tt.networkName)

			err := testEngine.NetworkRemove(ctx, tt.networkName)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("NetworkRemove failed: %v", err)
			}

			// Verify removal
			exists, err := testEngine.NetworkExists(ctx, tt.networkName)
			if err != nil {
				t.Fatalf("Failed to check network existence: %v", err)
			}
			if exists {
				t.Fatalf("Expected network to be removed, but it still exists")
			}
		})
	}
}

func TestNetworkInspect(t *testing.T) {
	tests := []struct {
		name        string
		networkName string
		setupFunc   func(ctx context.Context, t *testing.T, name string) string
		cleanupFunc func(ctx context.Context, t *testing.T, name string)
		shouldErr   bool
	}{
		{
			name:        "should inspect managed network",
			networkName: "test-network-inspect-managed",
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				resp, err := testEngine.NetworkCreate(ctx, name, network.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create network: %v", err)
				}
				return resp.ID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {
				testEngine.NetworkRemove(ctx, name)
			},
			shouldErr: false,
		},
		{
			name:        "should not inspect unmanaged network",
			networkName: "test-network-inspect-unmanaged",
			setupFunc: func(ctx context.Context, t *testing.T, name string) string {
				resp, err := testEngine.APIClient.NetworkCreate(ctx, name, network.CreateOptions{
					Driver: "bridge",
					Labels: map[string]string{"other.label": "value"},
				})
				if err != nil {
					t.Fatalf("Failed to create unmanaged network: %v", err)
				}
				return resp.ID
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {
				testEngine.APIClient.NetworkRemove(ctx, name)
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			networkID := tt.setupFunc(ctx, t, tt.networkName)
			if networkID == "" {
				t.Fatalf("Setup failed: network ID is empty")
			}
			defer tt.cleanupFunc(ctx, t, tt.networkName)

			info, err := testEngine.NetworkInspect(ctx, tt.networkName, network.InspectOptions{})
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("NetworkInspect failed: %v", err)
			}

			if info.ID != networkID {
				t.Errorf("Expected network ID %q, got %q", networkID, info.ID)
			}
		})
	}
}

func TestNetworkExists(t *testing.T) {
	tests := []struct {
		name        string
		networkName string
		setupFunc   func(ctx context.Context, t *testing.T, name string)
		cleanupFunc func(ctx context.Context, t *testing.T, name string)
		expected    bool
	}{
		{
			name:        "should return true for existing network",
			networkName: "test-network-exists",
			setupFunc: func(ctx context.Context, t *testing.T, name string) {
				_, err := testEngine.NetworkCreate(ctx, name, network.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create network: %v", err)
				}
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {
				testEngine.NetworkRemove(ctx, name)
			},
			expected: true,
		},
		{
			name:        "should return false for non-existing network",
			networkName: "test-network-does-not-exist",
			setupFunc:   func(ctx context.Context, t *testing.T, name string) {},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			tt.setupFunc(ctx, t, tt.networkName)
			defer tt.cleanupFunc(ctx, t, tt.networkName)

			exists, err := testEngine.NetworkExists(ctx, tt.networkName)
			if err != nil {
				t.Fatalf("NetworkExists failed: %v", err)
			}

			if exists != tt.expected {
				t.Errorf("Expected exists=%v, got %v", tt.expected, exists)
			}
		})
	}
}

func TestNetworkList(t *testing.T) {
	tests := []struct {
		name          string
		setupFunc     func(ctx context.Context, t *testing.T) string
		cleanupFunc   func(ctx context.Context, t *testing.T, networkName string)
		extraFilters  map[string]string
		shouldBeFound bool
	}{
		{
			name: "should return managed networks",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				_, err := testEngine.NetworkCreate(ctx, "test-network-list-managed", network.CreateOptions{}, map[string]string{"test.filter": "managed"})
				if err != nil {
					t.Fatalf("Failed to create network: %v", err)
				}
				return "test-network-list-managed"
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, networkName string) {
				testEngine.NetworkRemove(ctx, networkName)
			},
			extraFilters:  map[string]string{"test.filter": "managed"},
			shouldBeFound: true,
		},
		{
			name: "should not return unmanaged networks",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				_, err := testEngine.APIClient.NetworkCreate(ctx, "test-network-list-unmanaged", network.CreateOptions{
					Driver: "bridge",
					Labels: map[string]string{"other.label": "value"},
				})
				if err != nil {
					t.Fatalf("Failed to create unmanaged network: %v", err)
				}
				return "test-network-list-unmanaged"
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, networkName string) {
				testEngine.APIClient.NetworkRemove(ctx, networkName)
			},
			extraFilters:  map[string]string{},
			shouldBeFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			networkName := tt.setupFunc(ctx, t)
			defer tt.cleanupFunc(ctx, t, networkName)

			networks, err := testEngine.NetworkList(ctx, tt.extraFilters)
			if err != nil {
				t.Fatalf("NetworkList failed: %v", err)
			}

			found := false
			for _, net := range networks {
				if net.Name == networkName {
					found = true
					break
				}
			}

			if found != tt.shouldBeFound {
				t.Errorf("Expected network %q to be found: %v, but got: %v", networkName, tt.shouldBeFound, found)
			}
		})
	}
}

func TestEnsureNetwork(t *testing.T) {
	tests := []struct {
		name        string
		networkName string
		setupFunc   func(ctx context.Context, t *testing.T, name string)
		cleanupFunc func(ctx context.Context, t *testing.T, name string)
		shouldErr   bool
	}{
		{
			name:        "should create network if it doesn't exist",
			networkName: "test-ensure-network-new",
			setupFunc:   func(ctx context.Context, t *testing.T, name string) {},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {
				testEngine.NetworkRemove(ctx, name)
			},
			shouldErr: false,
		},
		{
			name:        "should return existing network if it exists",
			networkName: "test-ensure-network-existing",
			setupFunc: func(ctx context.Context, t *testing.T, name string) {
				_, err := testEngine.NetworkCreate(ctx, name, network.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create network: %v", err)
				}
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, name string) {
				testEngine.NetworkRemove(ctx, name)
			},
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			tt.setupFunc(ctx, t, tt.networkName)
			defer tt.cleanupFunc(ctx, t, tt.networkName)

			networkID, err := testEngine.EnsureNetwork(ctx, tt.networkName, network.CreateOptions{}, false)
			if tt.shouldErr {
				if err == nil {
					t.Fatalf("Expected error but got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("EnsureNetwork failed: %v", err)
			}

			if networkID == "" {
				t.Fatalf("Expected network ID, got empty string")
			}

			// Verify network exists
			exists, err := testEngine.NetworkExists(ctx, tt.networkName)
			if err != nil {
				t.Fatalf("Failed to check network existence: %v", err)
			}
			if !exists {
				t.Fatalf("Expected network to exist")
			}
		})
	}
}

func TestIsNetworkManaged(t *testing.T) {
	tests := []struct {
		name        string
		setupFunc   func(ctx context.Context, t *testing.T) string
		cleanupFunc func(ctx context.Context, t *testing.T, networkName string)
		expected    bool
	}{
		{
			name: "should return true for managed network",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				_, err := testEngine.NetworkCreate(ctx, "test-network-managed-check", network.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create network: %v", err)
				}
				return "test-network-managed-check"
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, networkName string) {
				testEngine.NetworkRemove(ctx, networkName)
			},
			expected: true,
		},
		{
			name: "should return false for unmanaged network",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				_, err := testEngine.APIClient.NetworkCreate(ctx, "test-network-unmanaged-check", network.CreateOptions{
					Driver: "bridge",
					Labels: map[string]string{"other.label": "value"},
				})
				if err != nil {
					t.Fatalf("Failed to create unmanaged network: %v", err)
				}
				return "test-network-unmanaged-check"
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, networkName string) {
				testEngine.APIClient.NetworkRemove(ctx, networkName)
			},
			expected: false,
		},
		{
			name: "should return false for non-existing network",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				return "test-network-nonexistent"
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, networkName string) {},
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			networkName := tt.setupFunc(ctx, t)
			defer tt.cleanupFunc(ctx, t, networkName)

			isManaged, err := testEngine.IsNetworkManaged(ctx, networkName)
			if err != nil && tt.expected {
				t.Fatalf("IsNetworkManaged failed: %v", err)
			}

			if isManaged != tt.expected {
				t.Errorf("Expected isManaged=%v, got %v", tt.expected, isManaged)
			}
		})
	}
}


func TestNetworksPrune(t *testing.T) {
	tests := []struct {
		name            string
		setupFunc       func(ctx context.Context, t *testing.T) string
		cleanupFunc     func(ctx context.Context, t *testing.T, networkName string)
		shouldBeRemoved bool
	}{
		{
			name: "should prune managed networks",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateNetworkName("test-network-prune-managed")
				return setupManagedNetwork(ctx, t, name)
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, networkName string) {
				// Network should be pruned, but try cleanup anyway in case test fails
				testEngine.APIClient.NetworkRemove(ctx, networkName)
			},
			shouldBeRemoved: true,
		},
		{
			name: "should not prune unmanaged networks",
			setupFunc: func(ctx context.Context, t *testing.T) string {
				name := generateNetworkName("test-network-prune-unmanaged")
				return setupUnmanagedNetwork(ctx, t, name, map[string]string{"other.label": "value"})
			},
			cleanupFunc: func(ctx context.Context, t *testing.T, networkName string) {
				testEngine.APIClient.NetworkRemove(ctx, networkName)
			},
			shouldBeRemoved: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			networkName := tt.setupFunc(ctx, t)
			defer tt.cleanupFunc(ctx, t, networkName)

			// Verify network exists before prune
			exists, err := testEngine.NetworkExists(ctx, networkName)
			if err != nil {
				t.Fatalf("Failed to check if network exists before prune: %v", err)
			}
			if !exists {
				t.Fatalf("Network should exist before prune")
			}

			// Prune networks
			_, err = testEngine.NetworksPrune(ctx)
			if err != nil {
				t.Fatalf("NetworksPrune failed: %v", err)
			}

			// Check if network still exists
			exists, err = testEngine.NetworkExists(ctx, networkName)
			if err != nil {
				t.Fatalf("Failed to check if network exists after prune: %v", err)
			}

			if tt.shouldBeRemoved && exists {
				t.Errorf("Expected managed network %q to be pruned, but it still exists", networkName)
			}
			if !tt.shouldBeRemoved && !exists {
				t.Errorf("Expected unmanaged network %q to NOT be pruned, but it was removed", networkName)
			}
		})
	}
}

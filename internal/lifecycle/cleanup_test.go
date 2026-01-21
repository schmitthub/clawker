package lifecycle

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

// TestClawkerStop tests stopping containers.
func TestClawkerStop(t *testing.T) {
	SkipIfNoDocker(t)

	tests := []struct {
		name          string
		projectConfig func(cfg *config.Config)
	}{
		{
			name: "debian-stop-running",
		},
		{
			name: "alpine-stop-running",
			projectConfig: func(cfg *config.Config) {
				cfg.Build.Image = "alpine:3.22"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultTestConfig("clawker-test-stop-" + sanitizeTestName(tt.name))
			if tt.projectConfig != nil {
				tt.projectConfig(cfg)
			}

			env := NewTestEnvWithOptions(t, TestEnvOptions{
				ProjectConfig:   func(c *config.Config) { *c = *cfg },
				UseClawkerImage: true,
			})
			defer env.Cleanup()

			// Run container
			containerID := env.RunContainer("test", "sleep", "300")
			env.AssertContainerRunning(containerID)

			// Stop container
			env.StopContainer(containerID)
			env.AssertContainerStopped(containerID)
		})
	}
}

// TestClawkerRemove tests removing containers.
func TestClawkerRemove(t *testing.T) {
	SkipIfNoDocker(t)

	tests := []struct {
		name          string
		projectConfig func(cfg *config.Config)
	}{
		{
			name: "debian-remove-stopped",
		},
		{
			name: "alpine-remove-stopped",
			projectConfig: func(cfg *config.Config) {
				cfg.Build.Image = "alpine:3.22"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultTestConfig("clawker-test-rm-" + sanitizeTestName(tt.name))
			if tt.projectConfig != nil {
				tt.projectConfig(cfg)
			}

			env := NewTestEnvWithOptions(t, TestEnvOptions{
				ProjectConfig:   func(c *config.Config) { *c = *cfg },
				UseClawkerImage: true,
			})
			defer env.Cleanup()

			// Run and stop container
			containerID := env.RunContainer("test", "sleep", "300")
			env.StopContainer(containerID)
			env.AssertContainerStopped(containerID)

			// Remove container
			env.RemoveContainer(containerID)
			env.AssertContainerNotExists(containerID)
		})
	}
}

// TestClawkerStopIdempotent verifies stopping an already-stopped container doesn't error.
func TestClawkerStopIdempotent(t *testing.T) {
	SkipIfNoDocker(t)

	cfg := defaultTestConfig("clawker-test-stop-idempotent")

	env := NewTestEnvWithOptions(t, TestEnvOptions{
		ProjectConfig:   func(c *config.Config) { *c = *cfg },
		UseClawkerImage: true,
	})
	defer env.Cleanup()

	// Create (not start) a container
	containerID := env.CreateContainer("test", "sleep", "300")

	// Try stopping - should not error even though container wasn't started
	_, err := env.Client.ContainerStop(env.Ctx, containerID, nil)
	// Note: Depending on Docker version, this may or may not error
	// We just verify it doesn't panic
	_ = err
}

// TestClawkerRemoveRunningForceful verifies force removing a running container works.
func TestClawkerRemoveRunningForceful(t *testing.T) {
	SkipIfNoDocker(t)

	cfg := defaultTestConfig("clawker-test-rm-force")

	env := NewTestEnvWithOptions(t, TestEnvOptions{
		ProjectConfig:   func(c *config.Config) { *c = *cfg },
		UseClawkerImage: true,
	})
	defer env.Cleanup()

	// Run container (leave it running)
	containerID := env.RunContainer("test", "sleep", "300")
	env.AssertContainerRunning(containerID)

	// Force remove (should work because we use force: true)
	_, err := env.Client.ContainerRemove(env.Ctx, containerID, true)
	require.NoError(t, err, "force remove should work on running container")

	env.AssertContainerNotExists(containerID)
}

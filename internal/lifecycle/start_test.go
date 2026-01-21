package lifecycle

import (
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContainerStartLifecycle tests the container start operation.
// This is the critical test that verifies containers don't exit immediately after start.
func TestContainerStartLifecycle(t *testing.T) {
	SkipIfNoDocker(t)

	tests := []struct {
		name          string
		command       []string
		expectRunning bool // Should container still be running after start?
		waitDuration  time.Duration
	}{
		{
			name:          "start stopped container - does not exit immediately",
			command:       []string{"sleep", "300"},
			expectRunning: true,
			waitDuration:  5 * time.Second,
		},
		{
			name:          "start container with short-lived command",
			command:       []string{"sh", "-c", "echo hello && sleep 2"},
			expectRunning: false, // Will exit after 2 seconds
			waitDuration:  5 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := NewTestEnv(t)
			defer env.Cleanup()

			agentName := GenerateAgentName("start")
			containerName := env.ContainerName(agentName)

			// Create container (stopped state)
			createOpts := whail.ContainerCreateOptions{
				Config: &container.Config{
					Image: env.ImageTag,
					Cmd:   tt.command,
				},
				Name: containerName,
				ExtraLabels: whail.Labels{
					{docker.LabelProject: env.ProjectName},
					{docker.LabelAgent: agentName},
				},
			}

			resp, err := env.Client.ContainerCreate(env.Ctx, createOpts)
			require.NoError(t, err)
			containerID := resp.ID

			// Verify container is created (not running)
			info, err := env.Client.APIClient.ContainerInspect(env.Ctx, containerID, client.ContainerInspectOptions{})
			require.NoError(t, err)
			assert.False(t, info.Container.State.Running, "container should not be running after create")

			// Start the container
			_, err = env.Client.ContainerStart(env.Ctx, whail.ContainerStartOptions{
				ContainerID: containerID,
			})
			require.NoError(t, err)

			// Wait the specified duration
			time.Sleep(tt.waitDuration)

			// Check state
			info, err = env.Client.APIClient.ContainerInspect(env.Ctx, containerID, client.ContainerInspectOptions{})
			require.NoError(t, err)

			if tt.expectRunning {
				assert.True(t, info.Container.State.Running, "container should still be running after %v", tt.waitDuration)
				// Stop for cleanup
				_, _ = env.Client.ContainerStop(env.Ctx, containerID, nil)
			} else {
				// Container may have already exited, which is expected for short-lived commands
				t.Logf("Container state: running=%v, exitCode=%d", info.Container.State.Running, info.Container.State.ExitCode)
			}

			// Cleanup
			_, _ = env.Client.ContainerRemove(env.Ctx, containerID, true)
		})
	}
}

// TestStartStoppedContainerDoesNotExitImmediately is the critical regression test.
// It specifically tests the issue where containers exit immediately after start.
func TestStartStoppedContainerDoesNotExitImmediately(t *testing.T) {
	SkipIfNoDocker(t)

	env := NewTestEnv(t)
	defer env.Cleanup()

	agentName := GenerateAgentName("starttest")
	containerName := env.ContainerName(agentName)

	// Create container
	createOpts := whail.ContainerCreateOptions{
		Config: &container.Config{
			Image: env.ImageTag,
			Cmd:   []string{"sleep", "300"},
		},
		Name: containerName,
		ExtraLabels: whail.Labels{
			{docker.LabelProject: env.ProjectName},
			{docker.LabelAgent: agentName},
		},
	}

	resp, err := env.Client.ContainerCreate(env.Ctx, createOpts)
	require.NoError(t, err)
	containerID := resp.ID
	defer func() {
		_, _ = env.Client.ContainerRemove(env.Ctx, containerID, true)
	}()

	// Start container
	_, err = env.Client.ContainerStart(env.Ctx, whail.ContainerStartOptions{
		ContainerID: containerID,
	})
	require.NoError(t, err)

	// Give it time to potentially crash
	time.Sleep(2 * time.Second)

	// Verify running
	info, err := env.Client.APIClient.ContainerInspect(env.Ctx, containerID, client.ContainerInspectOptions{})
	require.NoError(t, err)
	require.True(t, info.Container.State.Running, "container should still be running after 2 seconds")

	// Stop and verify stopped
	_, err = env.Client.ContainerStop(env.Ctx, containerID, nil)
	require.NoError(t, err)

	err = env.WaitForContainerState(containerID, ContainerStateStopped, 30*time.Second)
	require.NoError(t, err)

	// Start AGAIN - this is the key test
	_, err = env.Client.ContainerStart(env.Ctx, whail.ContainerStartOptions{
		ContainerID: containerID,
	})
	require.NoError(t, err)

	// Verify running again
	time.Sleep(2 * time.Second)
	info, err = env.Client.APIClient.ContainerInspect(env.Ctx, containerID, client.ContainerInspectOptions{})
	require.NoError(t, err)
	assert.True(t, info.Container.State.Running, "container should be running after restart")

	// Log final state
	env.LogContainerState(containerID)

	// Cleanup
	_, _ = env.Client.ContainerStop(env.Ctx, containerID, nil)
}

// TestMultipleStartStopCycles verifies containers can be started and stopped multiple times.
func TestMultipleStartStopCycles(t *testing.T) {
	SkipIfNoDocker(t)

	env := NewTestEnv(t)
	defer env.Cleanup()

	agentName := GenerateAgentName("cycles")
	containerName := env.ContainerName(agentName)

	// Create container
	createOpts := whail.ContainerCreateOptions{
		Config: &container.Config{
			Image: env.ImageTag,
			Cmd:   []string{"sleep", "300"},
		},
		Name: containerName,
		ExtraLabels: whail.Labels{
			{docker.LabelProject: env.ProjectName},
			{docker.LabelAgent: agentName},
		},
	}

	resp, err := env.Client.ContainerCreate(env.Ctx, createOpts)
	require.NoError(t, err)
	containerID := resp.ID
	defer func() {
		_, _ = env.Client.ContainerRemove(env.Ctx, containerID, true)
	}()

	// Run 3 start/stop cycles
	for i := 0; i < 3; i++ {
		t.Logf("Cycle %d: starting container", i+1)

		// Start
		_, err = env.Client.ContainerStart(env.Ctx, whail.ContainerStartOptions{
			ContainerID: containerID,
		})
		require.NoError(t, err, "cycle %d: start should not error", i+1)

		// Verify running
		time.Sleep(1 * time.Second)
		env.AssertContainerRunning(containerID)

		t.Logf("Cycle %d: stopping container", i+1)

		// Stop
		_, err = env.Client.ContainerStop(env.Ctx, containerID, nil)
		require.NoError(t, err, "cycle %d: stop should not error", i+1)

		// Wait for stopped state
		err = env.WaitForContainerState(containerID, ContainerStateStopped, 30*time.Second)
		require.NoError(t, err, "cycle %d: should reach stopped state", i+1)

		env.AssertContainerNotRunning(containerID)
	}

	t.Log("All 3 start/stop cycles completed successfully")
}

// ============================================================================
// Clawker Image Tests - Test with real clawker images including init scripts
// ============================================================================

// TestClawkerStart tests starting containers with real clawker images.
func TestClawkerStart(t *testing.T) {
	SkipIfNoDocker(t)

	tests := []struct {
		name            string
		projectConfig   func(cfg *config.Config)
		stopStartCycles int
		verify          func(t *testing.T, env *TestEnv, containerID string)
	}{
		{
			name:            "debian-single-cycle",
			stopStartCycles: 1,
			verify: func(t *testing.T, env *TestEnv, containerID string) {
				env.VerifyContainerLabels(containerID)
			},
		},
		{
			name:            "debian-multiple-cycles",
			stopStartCycles: 3,
			verify: func(t *testing.T, env *TestEnv, containerID string) {
				env.VerifyContainerLabels(containerID)
			},
		},
		{
			name: "alpine-single-cycle",
			projectConfig: func(cfg *config.Config) {
				cfg.Build.Image = "alpine:3.22"
			},
			stopStartCycles: 1,
			verify: func(t *testing.T, env *TestEnv, containerID string) {
				env.VerifyContainerLabels(containerID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultTestConfig("clawker-test-start-" + sanitizeTestName(tt.name))
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

			for i := 0; i < tt.stopStartCycles; i++ {
				// Stop container
				env.StopContainer(containerID)
				env.AssertContainerStopped(containerID)

				// Start container
				env.StartContainer(containerID)
				time.Sleep(500 * time.Millisecond) // Give it time to start
				env.AssertContainerRunning(containerID)

				// Run custom verification
				if tt.verify != nil {
					tt.verify(t, env, containerID)
				}
			}

			// Cleanup
			env.StopContainer(containerID)
			env.RemoveContainer(containerID)
		})
	}
}

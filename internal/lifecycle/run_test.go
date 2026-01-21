package lifecycle

import (
	"context"
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

// TestContainerRunLifecycle tests the full container run lifecycle.
// These tests verify that containers:
// - Start successfully without immediate exit
// - Have correct labels applied
// - Can be stopped and removed cleanly
func TestContainerRunLifecycle(t *testing.T) {
	SkipIfNoDocker(t)

	tests := []struct {
		name          string
		command       []string
		expectRunning bool  // Should container still be running after start?
		expectExit    int64 // Expected exit code if not running
		timeout       time.Duration
	}{
		{
			name:          "sleep command stays running",
			command:       []string{"sleep", "300"},
			expectRunning: true,
			timeout:       30 * time.Second,
		},
		{
			name:          "echo command exits cleanly",
			command:       []string{"echo", "hello"},
			expectRunning: false,
			expectExit:    0,
			timeout:       30 * time.Second,
		},
		{
			name:          "sh script exits cleanly",
			command:       []string{"sh", "-c", "echo started; sleep 1; echo done"},
			expectRunning: false,
			expectExit:    0,
			timeout:       30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := NewTestEnv(t)
			defer env.Cleanup()

			agentName := GenerateAgentName("run")
			containerName := env.ContainerName(agentName)

			// Create container with test command
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
			require.NoError(t, err, "container create should not error")
			require.NotEmpty(t, resp.ID, "container ID should not be empty")

			containerID := resp.ID

			// Start container
			_, err = env.Client.ContainerStart(env.Ctx, whail.ContainerStartOptions{
				ContainerID: containerID,
			})
			require.NoError(t, err, "container start should not error")

			// Verify container has correct labels
			info, err := env.Client.APIClient.ContainerInspect(env.Ctx, containerID, client.ContainerInspectOptions{})
			require.NoError(t, err, "container inspect should not error")

			assert.Equal(t, docker.ManagedLabelValue, info.Container.Config.Labels[docker.LabelManaged], "should have managed label")
			assert.Equal(t, env.ProjectName, info.Container.Config.Labels[docker.LabelProject], "should have project label")
			assert.Equal(t, agentName, info.Container.Config.Labels[docker.LabelAgent], "should have agent label")

			if tt.expectRunning {
				// Give container a moment to potentially crash
				time.Sleep(2 * time.Second)

				// Verify still running
				info, err := env.Client.APIClient.ContainerInspect(env.Ctx, containerID, client.ContainerInspectOptions{})
				require.NoError(t, err)
				assert.True(t, info.Container.State.Running, "container should still be running after 2 seconds")

				// Stop for cleanup
				_, err = env.Client.ContainerStop(env.Ctx, containerID, nil)
				require.NoError(t, err)
			} else {
				// Wait for container to exit
				ctx, cancel := context.WithTimeout(env.Ctx, tt.timeout)
				defer cancel()

				waitResult := env.Client.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
				select {
				case status := <-waitResult.Result:
					assert.Equal(t, tt.expectExit, status.StatusCode, "unexpected exit code")
				case err := <-waitResult.Error:
					require.NoError(t, err, "wait should not error")
				case <-ctx.Done():
					t.Fatal("timeout waiting for container to exit")
				}
			}

			// Cleanup: remove container
			_, err = env.Client.ContainerRemove(env.Ctx, containerID, true)
			require.NoError(t, err, "container remove should not error")
		})
	}
}

// TestContainerDoesNotExitImmediately verifies that containers with long-running
// commands don't exit immediately after start. This is a critical regression test.
func TestContainerDoesNotExitImmediately(t *testing.T) {
	SkipIfNoDocker(t)

	env := NewTestEnv(t)
	defer env.Cleanup()

	agentName := GenerateAgentName("noexit")
	containerName := env.ContainerName(agentName)

	// Create container with sleep command
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

	// Start container
	_, err = env.Client.ContainerStart(env.Ctx, whail.ContainerStartOptions{
		ContainerID: containerID,
	})
	require.NoError(t, err)

	// Wait 5 seconds - container should still be running
	time.Sleep(5 * time.Second)

	// Check state
	info, err := env.Client.APIClient.ContainerInspect(env.Ctx, containerID, client.ContainerInspectOptions{})
	require.NoError(t, err)

	assert.True(t, info.Container.State.Running, "container should still be running after 5 seconds")
	assert.Equal(t, 0, info.Container.State.ExitCode, "exit code should be 0 while running")

	// Log state for debugging
	env.LogContainerState(containerID)

	// Cleanup
	_, _ = env.Client.ContainerStop(env.Ctx, containerID, nil)
	_, _ = env.Client.ContainerRemove(env.Ctx, containerID, true)
}

// TestContainerLabelsApplied verifies that all clawker labels are correctly applied.
func TestContainerLabelsApplied(t *testing.T) {
	SkipIfNoDocker(t)

	env := NewTestEnv(t)
	defer env.Cleanup()

	agentName := GenerateAgentName("labels")
	containerName := env.ContainerName(agentName)

	createOpts := whail.ContainerCreateOptions{
		Config: &container.Config{
			Image: env.ImageTag,
			Cmd:   []string{"sleep", "10"},
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

	// Verify labels via our helper
	assert.True(t, env.ContainerIsManaged(containerID), "container should be managed")
	assert.True(t, env.ContainerHasLabel(containerID, docker.LabelProject, env.ProjectName), "should have correct project label")
	assert.True(t, env.ContainerHasLabel(containerID, docker.LabelAgent, agentName), "should have correct agent label")
}

// TestContainerStopAndRestart verifies stop/restart cycle works correctly.
func TestContainerStopAndRestart(t *testing.T) {
	SkipIfNoDocker(t)

	env := NewTestEnv(t)
	defer env.Cleanup()

	agentName := GenerateAgentName("stopstart")
	containerName := env.ContainerName(agentName)

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

	// Start
	_, err = env.Client.ContainerStart(env.Ctx, whail.ContainerStartOptions{
		ContainerID: containerID,
	})
	require.NoError(t, err)

	// Verify running
	env.AssertContainerRunning(containerID)

	// Stop
	_, err = env.Client.ContainerStop(env.Ctx, containerID, nil)
	require.NoError(t, err)

	// Verify stopped
	err = env.WaitForContainerState(containerID, ContainerStateStopped, 30*time.Second)
	require.NoError(t, err, "container should reach stopped state")

	// Restart
	_, err = env.Client.ContainerStart(env.Ctx, whail.ContainerStartOptions{
		ContainerID: containerID,
	})
	require.NoError(t, err)

	// Give it a moment
	time.Sleep(2 * time.Second)

	// Verify running again
	env.AssertContainerRunning(containerID)

	// Stop for cleanup
	_, _ = env.Client.ContainerStop(env.Ctx, containerID, nil)
}

// ============================================================================
// Clawker Image Tests - Test with real clawker images including init scripts
// ============================================================================

// TestClawkerRun tests running containers built with real clawker images.
// These tests exercise the full container lifecycle with entrypoint scripts,
// firewall initialization, etc.
func TestClawkerRun(t *testing.T) {
	SkipIfNoDocker(t)

	tests := []struct {
		name          string
		projectConfig func(cfg *config.Config)
		cmd           []string       // Container command override
		verify        func(t *testing.T, env *TestEnv, containerID string)
	}{
		{
			name: "debian-detached",
			cmd:  []string{"sleep", "300"},
			verify: func(t *testing.T, env *TestEnv, containerID string) {
				env.VerifyContainerLabels(containerID)
			},
		},
		{
			name: "debian-firewall-enabled",
			// Default config has firewall enabled
			cmd: []string{"sleep", "300"},
			verify: func(t *testing.T, env *TestEnv, containerID string) {
				env.VerifyContainerLabels(containerID)
				// Note: Actual firewall verification would need network access tests
			},
		},
		{
			name: "debian-firewall-disabled",
			projectConfig: func(cfg *config.Config) {
				cfg.Security.Firewall = &config.FirewallConfig{Enable: false}
				cfg.Security.CapAdd = nil // No NET_ADMIN needed
			},
			cmd: []string{"sleep", "300"},
			verify: func(t *testing.T, env *TestEnv, containerID string) {
				env.VerifyContainerLabels(containerID)
			},
		},
		{
			name: "alpine-detached",
			projectConfig: func(cfg *config.Config) {
				cfg.Build.Image = "alpine:3.22"
			},
			cmd: []string{"sleep", "300"},
			verify: func(t *testing.T, env *TestEnv, containerID string) {
				env.VerifyContainerLabels(containerID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultTestConfig("clawker-test-run-" + sanitizeTestName(tt.name))
			if tt.projectConfig != nil {
				tt.projectConfig(cfg)
			}

			env := NewTestEnvWithOptions(t, TestEnvOptions{
				ProjectConfig:   func(c *config.Config) { *c = *cfg },
				UseClawkerImage: true,
			})
			defer env.Cleanup()

			// Run container
			containerID := env.RunContainer("test", tt.cmd...)
			defer func() {
				env.StopContainer(containerID)
				env.RemoveContainer(containerID)
			}()

			// Verify container is running
			env.AssertContainerRunning(containerID)

			// Run custom verification
			if tt.verify != nil {
				tt.verify(t, env, containerID)
			}
		})
	}
}

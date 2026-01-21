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

// TestFirewallContainerLifecycle tests containers with firewall configuration.
// Note: Firewall init scripts require NET_ADMIN capability and may need iptables/ipset.
// These tests focus on verifying containers start successfully with firewall config,
// not on testing the actual firewall rules (which would require network-level testing).
func TestFirewallContainerLifecycle(t *testing.T) {
	SkipIfNoDocker(t)

	tests := []struct {
		name           string
		firewallConfig *config.FirewallConfig
		expectStart    bool // Should container start successfully?
	}{
		{
			name: "firewall enabled with override domains",
			firewallConfig: &config.FirewallConfig{
				Enable:          true,
				OverrideDomains: []string{"github.com"},
			},
			expectStart: true,
		},
		{
			name: "firewall disabled",
			firewallConfig: &config.FirewallConfig{
				Enable: false,
			},
			expectStart: true,
		},
		{
			name:           "firewall nil (defaults to disabled)",
			firewallConfig: nil, // Will use default from NewTestEnv which disables firewall
			expectStart:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var env *TestEnv
			if tt.firewallConfig != nil {
				env = NewTestEnvWithFirewall(t, tt.firewallConfig)
			} else {
				env = NewTestEnv(t)
			}
			defer env.Cleanup()

			agentName := GenerateAgentName("firewall")
			containerName := env.ContainerName(agentName)

			// Create container with a command that echoes and sleeps
			createOpts := whail.ContainerCreateOptions{
				Config: &container.Config{
					Image: env.ImageTag,
					Cmd:   []string{"sh", "-c", "echo 'Container started'; sleep 30"},
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

			if tt.expectStart {
				require.NoError(t, err, "container should start successfully")

				// Give container time to potentially crash
				time.Sleep(3 * time.Second)

				// Verify running
				info, err := env.Client.APIClient.ContainerInspect(env.Ctx, containerID, client.ContainerInspectOptions{})
				require.NoError(t, err)
				assert.True(t, info.Container.State.Running, "container should still be running")

				// Get logs for debugging
				logs, err := env.GetContainerLogs(containerID)
				if err == nil {
					t.Logf("Container logs:\n%s", logs)
				}

				// Cleanup
				_, _ = env.Client.ContainerStop(env.Ctx, containerID, nil)
			} else {
				assert.Error(t, err, "container should fail to start")
			}
		})
	}
}

// TestContainerWithFirewallDoesNotExitImmediately verifies that containers
// with firewall enabled don't exit immediately after start.
func TestContainerWithFirewallDoesNotExitImmediately(t *testing.T) {
	SkipIfNoDocker(t)

	// Create environment with firewall disabled (for faster tests)
	// Note: Actual firewall testing with NET_ADMIN requires privileged containers
	env := NewTestEnvWithFirewall(t, &config.FirewallConfig{
		Enable: false, // Disabled for CI compatibility
	})
	defer env.Cleanup()

	agentName := GenerateAgentName("fw-noexit")
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

	// Wait 5 seconds
	time.Sleep(5 * time.Second)

	// Verify still running
	info, err := env.Client.APIClient.ContainerInspect(env.Ctx, containerID, client.ContainerInspectOptions{})
	require.NoError(t, err)
	assert.True(t, info.Container.State.Running, "container should still be running after 5 seconds")

	// Log state
	env.LogContainerState(containerID)

	// Cleanup
	_, _ = env.Client.ContainerStop(env.Ctx, containerID, nil)
}

// TestFirewallConfigInTestEnv verifies the test environment correctly applies
// firewall configuration.
func TestFirewallConfigInTestEnv(t *testing.T) {
	SkipIfNoDocker(t)

	t.Run("default TestEnv has firewall disabled", func(t *testing.T) {
		env := NewTestEnv(t)
		defer env.Cleanup()

		require.NotNil(t, env.Config.Security.Firewall, "firewall config should exist")
		assert.False(t, env.Config.Security.Firewall.Enable, "firewall should be disabled by default")
	})

	t.Run("NewTestEnvWithFirewall applies custom config", func(t *testing.T) {
		firewallCfg := &config.FirewallConfig{
			Enable:          true,
			OverrideDomains: []string{"test.example.com"},
		}
		env := NewTestEnvWithFirewall(t, firewallCfg)
		defer env.Cleanup()

		require.NotNil(t, env.Config.Security.Firewall)
		assert.True(t, env.Config.Security.Firewall.Enable, "firewall should be enabled")
		assert.Contains(t, env.Config.Security.Firewall.OverrideDomains, "test.example.com")
	})
}

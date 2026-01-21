package lifecycle

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

// TestClawkerExec tests command execution in containers.
// These tests verify that:
// - Commands can be executed in running containers
// - Output is correctly captured
// - Different exec options work (user, env, workdir)
func TestClawkerExec(t *testing.T) {
	SkipIfNoDocker(t)

	tests := []struct {
		name          string
		projectConfig func(cfg *config.Config)
		execCmd       []string
		execOpts      ExecOptions
		wantOutput    string
		wantErr       bool
	}{
		{
			name:       "debian-echo",
			execCmd:    []string{"echo", "hello"},
			wantOutput: "hello",
		},
		{
			name:       "debian-whoami-default-user",
			execCmd:    []string{"whoami"},
			wantOutput: "claude", // Default user in clawker images
		},
		{
			name:       "debian-exec-as-root",
			execCmd:    []string{"whoami"},
			execOpts:   ExecOptions{User: "root"},
			wantOutput: "root",
		},
		{
			name:       "debian-exec-with-env",
			execCmd:    []string{"sh", "-c", "echo $FOO"},
			execOpts:   ExecOptions{Env: []string{"FOO=bar"}},
			wantOutput: "bar",
		},
		{
			name:       "debian-exec-with-workdir",
			execCmd:    []string{"pwd"},
			execOpts:   ExecOptions{WorkDir: "/tmp"},
			wantOutput: "/tmp",
		},
		{
			name: "alpine-echo",
			projectConfig: func(cfg *config.Config) {
				cfg.Build.Image = "alpine:3.22"
			},
			execCmd:    []string{"echo", "alpine-test"},
			wantOutput: "alpine-test",
		},
		{
			name:    "debian-command-not-found",
			execCmd: []string{"nonexistent-command-12345"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultTestConfig("clawker-test-exec-" + sanitizeTestName(tt.name))
			if tt.projectConfig != nil {
				tt.projectConfig(cfg)
			}

			env := NewTestEnvWithOptions(t, TestEnvOptions{
				ProjectConfig:   func(c *config.Config) { *c = *cfg },
				UseClawkerImage: true,
			})
			defer env.Cleanup()

			// Run a container with sleep command
			agentName := "test"
			containerID := env.RunContainer(agentName, "sleep", "300")
			defer func() {
				env.StopContainer(containerID)
				env.RemoveContainer(containerID)
			}()

			// Execute command
			output, err := env.ExecInContainerWithOpts(containerID, tt.execCmd, tt.execOpts)
			if tt.wantErr {
				require.Error(t, err, "expected error but got none")
			} else {
				require.NoError(t, err, "exec should succeed")
				require.Contains(t, output, tt.wantOutput, "output should contain expected string")
			}
		})
	}
}

// TestExecInStoppedContainer verifies that exec fails on stopped containers.
func TestExecInStoppedContainer(t *testing.T) {
	SkipIfNoDocker(t)

	cfg := defaultTestConfig("clawker-test-exec-stopped")

	env := NewTestEnvWithOptions(t, TestEnvOptions{
		ProjectConfig:   func(c *config.Config) { *c = *cfg },
		UseClawkerImage: true,
	})
	defer env.Cleanup()

	// Create and start container
	containerID := env.RunContainer("test", "sleep", "300")

	// Stop container
	env.StopContainer(containerID)
	env.AssertContainerStopped(containerID)

	// Try to exec - should fail
	_, err := env.ExecInContainer(containerID, "echo", "test")
	require.Error(t, err, "exec should fail on stopped container")
}

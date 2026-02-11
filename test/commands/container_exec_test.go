package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/cmd/container/exec"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/schmitthub/clawker/test/harness/builders"
	"github.com/stretchr/testify/require"
)

// TestContainerExec_BasicCommands tests executing commands in an already-running container.
// This validates that exec works correctly and that commands are executed properly.
func TestContainerExec_BasicCommands(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("exec-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "exec-test"); err != nil {
			t.Logf("WARNING: cleanup failed for exec-test: %v", err)
		}
	}()

	// Generate unique container name
	agentName := "test-exec-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start a container that stays running via whail (auto-injects labels)
	resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"}, // Sleep for 5 minutes
			Labels: map[string]string{
				"com.clawker.project": "exec-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = dockerClient.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID})
	require.NoError(t, err, "failed to start container")

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, dockerClient, resp.ID)
	require.NoError(t, err, "container did not start")

	// Define test cases for exec commands
	tests := []struct {
		name       string
		cmd        []string
		verifyFunc func(t *testing.T, output string)
	}{
		{
			name: "whoami",
			cmd:  []string{"whoami"},
			verifyFunc: func(t *testing.T, output string) {
				// Alpine container runs as root by default
				require.Contains(t, strings.TrimSpace(output), "root", "whoami should return root")
			},
		},
		{
			name: "env",
			cmd:  []string{"env"},
			verifyFunc: func(t *testing.T, output string) {
				// Should contain PATH variable at minimum
				require.Contains(t, output, "PATH=", "expected PATH in environment")
			},
		},
		{
			name: "ls root",
			cmd:  []string{"ls", "/"},
			verifyFunc: func(t *testing.T, output string) {
				require.Contains(t, output, "bin", "expected bin in ls output")
				require.Contains(t, output, "etc", "expected etc in ls output")
			},
		},
		{
			name: "echo test",
			cmd:  []string{"echo", "exec-test-output"},
			verifyFunc: func(t *testing.T, output string) {
				require.Contains(t, output, "exec-test-output", "expected echo output")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ios := harness.NewTestFactory(t, h)
			f.Client = func(ctx context.Context) (*docker.Client, error) {
				return dockerClient, nil
			}

			// Build exec command args: container name, then command
			cmdArgs := append([]string{containerName}, tt.cmd...)

			cmd := exec.NewCmdExec(f, nil)
			cmd.SetArgs(cmdArgs)

			err := cmd.Execute()
			require.NoError(t, err, "exec command failed: stderr=%s", ios.ErrBuf.String())

			// Verify output from IOStreams
			tt.verifyFunc(t, ios.OutBuf.String())
		})
	}
}

// TestContainerExec_WithAgent tests the --agent flag for exec commands.
func TestContainerExec_WithAgent(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("exec-agent-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "exec-agent-test"); err != nil {
			t.Logf("WARNING: cleanup failed for exec-agent-test: %v", err)
		}
	}()

	agentName := "test-agent-exec-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start container via whail (auto-injects labels)
	resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				"com.clawker.project": "exec-agent-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = dockerClient.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID})
	require.NoError(t, err, "failed to start container")

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, dockerClient, resp.ID)
	require.NoError(t, err, "container did not start")

	// Test exec with --agent flag
	f, ios := harness.NewTestFactory(t, h)
	f.Client = func(ctx context.Context) (*docker.Client, error) {
		return dockerClient, nil
	}

	cmd := exec.NewCmdExec(f, nil)
	cmd.SetArgs([]string{
		"--agent", agentName,
		"echo", "agent-exec-works",
	})

	err = cmd.Execute()
	require.NoError(t, err, "exec with --agent failed: stderr=%s", ios.ErrBuf.String())
	require.Contains(t, ios.OutBuf.String(), "agent-exec-works", "expected echo output")
}

// TestContainerExec_EnvFlag tests passing environment variables via exec.
func TestContainerExec_EnvFlag(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("exec-env-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "exec-env-test"); err != nil {
			t.Logf("WARNING: cleanup failed for exec-env-test: %v", err)
		}
	}()

	agentName := "test-exec-env-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start container via whail (auto-injects labels)
	resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				"com.clawker.project": "exec-env-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = dockerClient.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID})
	require.NoError(t, err, "failed to start container")

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, dockerClient, resp.ID)
	require.NoError(t, err, "container did not start")

	// Test exec with -e flag to set environment variable
	f, ios := harness.NewTestFactory(t, h)

	cmd := exec.NewCmdExec(f, nil)
	f.Client = func(ctx context.Context) (*docker.Client, error) {
		return dockerClient, nil
	}
	cmd.SetArgs([]string{
		"-e", "TEST_VAR=custom_value",
		containerName,
		"sh", "-c", "echo $TEST_VAR",
	})

	err = cmd.Execute()
	require.NoError(t, err, "exec with -e failed: stderr=%s", ios.ErrBuf.String())
	require.Contains(t, ios.OutBuf.String(), "custom_value", "expected custom env var in output")
}

// TestContainerExec_ErrorCases tests error scenarios for the exec command.
// These tests verify that clear, useful error messages are provided when things fail.
func TestContainerExec_ErrorCases(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("exec-error-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "exec-error-test"); err != nil {
			t.Logf("WARNING: cleanup failed for exec-error-test: %v", err)
		}
	}()

	t.Run("command not found", func(t *testing.T) {
		agentName := "test-err-notfound-" + time.Now().Format("150405.000000")
		containerName := h.ContainerName(agentName)

		// Create and start a container via whail (auto-injects labels)
		resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
			Name: containerName,
			Config: &container.Config{
				Image: "alpine:latest",
				Cmd:   []string{"sleep", "300"},
				Labels: map[string]string{
					"com.clawker.project": "exec-error-test",
					"com.clawker.agent":   agentName,
				},
			},
		})
		require.NoError(t, err, "failed to create container")
		_, err = dockerClient.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID})
		require.NoError(t, err, "failed to start container")

		// Wait for container to be running
		readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		err = harness.WaitForContainerRunning(readyCtx, dockerClient, resp.ID)
		require.NoError(t, err, "container did not start")

		// Try to exec a command that doesn't exist
		f, _ := harness.NewTestFactory(t, h)
		f.Client = func(ctx context.Context) (*docker.Client, error) {
			return dockerClient, nil
		}

		cmd := exec.NewCmdExec(f, nil)
		cmd.SetArgs([]string{
			containerName,
			"notacommand123doesnotexist",
		})

		err = cmd.Execute()
		// The exec should fail with a non-zero exit code or error
		require.Error(t, err, "expected error for non-existent command")
	})

	t.Run("exec on stopped container", func(t *testing.T) {
		agentName := "test-err-stopped-" + time.Now().Format("150405.000000")
		containerName := h.ContainerName(agentName)

		// Create a container but don't start it (via whail for labels)
		_, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
			Name: containerName,
			Config: &container.Config{
				Image: "alpine:latest",
				Cmd:   []string{"sleep", "300"},
				Labels: map[string]string{
					"com.clawker.project": "exec-error-test",
					"com.clawker.agent":   agentName,
				},
			},
		})
		require.NoError(t, err, "failed to create container")
		// Deliberately NOT starting the container

		// Try to exec into the stopped container
		f, ios := harness.NewTestFactory(t, h)
		f.Client = func(ctx context.Context) (*docker.Client, error) {
			return dockerClient, nil
		}

		cmd := exec.NewCmdExec(f, nil)
		cmd.SetArgs([]string{
			containerName,
			"ls",
		})

		err = cmd.Execute()
		// Should fail because container is not running
		require.Error(t, err, "expected error when execing into stopped container")
		// Error message should indicate the container isn't running
		errMsg := err.Error() + ios.ErrBuf.String()
		require.True(t,
			strings.Contains(errMsg, "not running") ||
				strings.Contains(errMsg, "is not running") ||
				strings.Contains(errMsg, "Container") && strings.Contains(errMsg, "running"),
			"error should mention container is not running, got: %s", errMsg)
	})
}

// TestContainerExec_ScriptExecution tests running scripts via exec command.
// This verifies that scripts created in the container can be executed via exec.
func TestContainerExec_ScriptExecution(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("exec-script-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "exec-script-test"); err != nil {
			t.Logf("WARNING: cleanup failed for exec-script-test: %v", err)
		}
	}()

	agentName := "test-script-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start a container via whail (auto-injects labels)
	resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				"com.clawker.project": "exec-script-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")
	_, err = dockerClient.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID})
	require.NoError(t, err, "failed to start container")

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, dockerClient, resp.ID)
	require.NoError(t, err, "container did not start")

	// Create a test script in the container via exec (same interface on whail)
	createScriptCmd := []string{"sh", "-c", `cat > /tmp/test-script.sh << 'SCRIPT'
#!/bin/sh
echo "script-execution-test-output"
echo "args: $@"
SCRIPT
chmod +x /tmp/test-script.sh`}

	execConfig := docker.ExecCreateOptions{
		Cmd:          createScriptCmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	execResp, err := dockerClient.ExecCreate(ctx, resp.ID, execConfig)
	require.NoError(t, err)
	_, err = dockerClient.ExecStart(ctx, execResp.ID, docker.ExecStartOptions{})
	require.NoError(t, err)

	// Give script creation time to complete
	time.Sleep(200 * time.Millisecond)

	tests := []struct {
		name         string
		useAgentFlag bool
		scriptArgs   []string
		checkOutput  func(t *testing.T, stdout string)
	}{
		{
			name:         "run script via --agent flag",
			useAgentFlag: true,
			scriptArgs:   []string{"/tmp/test-script.sh", "arg1", "arg2"},
			checkOutput: func(t *testing.T, stdout string) {
				require.Contains(t, stdout, "script-execution-test-output")
				require.Contains(t, stdout, "args: arg1 arg2")
			},
		},
		{
			name:         "run script via container name",
			useAgentFlag: false,
			scriptArgs:   []string{"/tmp/test-script.sh", "hello", "world"},
			checkOutput: func(t *testing.T, stdout string) {
				require.Contains(t, stdout, "script-execution-test-output")
				require.Contains(t, stdout, "args: hello world")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ios := harness.NewTestFactory(t, h)
			f.Client = func(ctx context.Context) (*docker.Client, error) {
				return dockerClient, nil
			}

			var cmdArgs []string
			if tt.useAgentFlag {
				cmdArgs = []string{"--agent", agentName}
				cmdArgs = append(cmdArgs, tt.scriptArgs...)
			} else {
				cmdArgs = []string{containerName}
				cmdArgs = append(cmdArgs, tt.scriptArgs...)
			}

			cmd := exec.NewCmdExec(f, nil)
			cmd.SetArgs(cmdArgs)

			err := cmd.Execute()
			require.NoError(t, err, "exec command failed: stderr=%s", ios.ErrBuf.String())

			tt.checkOutput(t, ios.OutBuf.String())
		})
	}
}

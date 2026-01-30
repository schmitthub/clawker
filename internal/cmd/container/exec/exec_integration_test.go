//go:build integration

package exec

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestExecIntegration_BasicCommands tests executing commands in an already-running container.
// This validates that exec works correctly and that commands are executed properly.
func TestExecIntegration_BasicCommands(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("exec-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := testutil.NewTestClient(t)
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()
	defer testutil.CleanupProjectResources(ctx, dockerClient, "exec-test")

	// Generate unique container name
	agentName := "test-exec-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start a container that stays running
	resp, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"}, // Sleep for 5 minutes
			Labels: map[string]string{
				"com.clawker.managed": "true",
				"com.clawker.project": "exec-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = rawClient.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "failed to start container")

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = testutil.WaitForContainerRunning(readyCtx, rawClient, resp.ID)
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
			ios := iostreams.NewTestIOStreams()
			f := &cmdutil.Factory{
				WorkDir:   h.ProjectDir,
				IOStreams: ios.IOStreams,
			}

			// Build exec command args: container name, then command
			cmdArgs := append([]string{containerName}, tt.cmd...)

			cmd := NewCmdExec(f, nil)
			cmd.SetArgs(cmdArgs)

			err := cmd.Execute()
			require.NoError(t, err, "exec command failed: stderr=%s", ios.ErrBuf.String())

			// Verify output from IOStreams
			tt.verifyFunc(t, ios.OutBuf.String())
		})
	}
}

// TestExecIntegration_WithAgent tests the --agent flag for exec commands.
func TestExecIntegration_WithAgent(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("exec-agent-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := testutil.NewTestClient(t)
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()
	defer testutil.CleanupProjectResources(ctx, dockerClient, "exec-agent-test")

	agentName := "test-agent-exec-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start container
	resp, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				"com.clawker.managed": "true",
				"com.clawker.project": "exec-agent-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = rawClient.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "failed to start container")

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = testutil.WaitForContainerRunning(readyCtx, rawClient, resp.ID)
	require.NoError(t, err, "container did not start")

	// Test exec with --agent flag
	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	cmd := NewCmdExec(f, nil)
	cmd.SetArgs([]string{
		"--agent", agentName,
		"echo", "agent-exec-works",
	})

	err = cmd.Execute()
	require.NoError(t, err, "exec with --agent failed: stderr=%s", ios.ErrBuf.String())
	require.Contains(t, ios.OutBuf.String(), "agent-exec-works", "expected echo output")
}

// TestExecIntegration_EnvFlag tests passing environment variables via exec.
func TestExecIntegration_EnvFlag(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("exec-env-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := testutil.NewTestClient(t)
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()
	defer testutil.CleanupProjectResources(ctx, dockerClient, "exec-env-test")

	agentName := "test-exec-env-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start container
	resp, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				"com.clawker.managed": "true",
				"com.clawker.project": "exec-env-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = rawClient.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "failed to start container")

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = testutil.WaitForContainerRunning(readyCtx, rawClient, resp.ID)
	require.NoError(t, err, "container did not start")

	// Test exec with -e flag to set environment variable
	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	cmd := NewCmdExec(f, nil)
	cmd.SetArgs([]string{
		"-e", "TEST_VAR=custom_value",
		containerName,
		"sh", "-c", "echo $TEST_VAR",
	})

	err = cmd.Execute()
	require.NoError(t, err, "exec with -e failed: stderr=%s", ios.ErrBuf.String())
	require.Contains(t, ios.OutBuf.String(), "custom_value", "expected custom env var in output")
}

// TestExecIntegration_WorkdirFlag tests executing commands in a specific directory.
func TestExecIntegration_WorkdirFlag(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("exec-workdir-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := testutil.NewTestClient(t)
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()
	defer testutil.CleanupProjectResources(ctx, dockerClient, "exec-workdir-test")

	agentName := "test-exec-wd-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start container
	resp, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				"com.clawker.managed": "true",
				"com.clawker.project": "exec-workdir-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = rawClient.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "failed to start container")

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = testutil.WaitForContainerRunning(readyCtx, rawClient, resp.ID)
	require.NoError(t, err, "container did not start")

	// Test exec with -w flag to set working directory
	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	cmd := NewCmdExec(f, nil)
	cmd.SetArgs([]string{
		"-w", "/tmp",
		containerName,
		"pwd",
	})

	err = cmd.Execute()
	require.NoError(t, err, "exec with -w failed: stderr=%s", ios.ErrBuf.String())
	require.Contains(t, ios.OutBuf.String(), "/tmp", "expected /tmp as working directory")
}

// TestExecIntegration_ErrorCases tests error scenarios for the exec command.
// These tests verify that clear, useful error messages are provided when things fail.
func TestExecIntegration_ErrorCases(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("exec-error-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := testutil.NewTestClient(t)
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()
	defer testutil.CleanupProjectResources(ctx, dockerClient, "exec-error-test")

	t.Run("command not found", func(t *testing.T) {
		agentName := "test-err-notfound-" + time.Now().Format("150405.000000")
		containerName := h.ContainerName(agentName)

		// Create and start a container
		resp, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
			Name: containerName,
			Config: &container.Config{
				Image: "alpine:latest",
				Cmd:   []string{"sleep", "300"},
				Labels: testutil.AddClawkerLabels(map[string]string{
					testutil.TestLabel: testutil.TestLabelValue,
				}, "exec-error-test", agentName),
			},
		})
		require.NoError(t, err, "failed to create container")
		_, err = rawClient.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
		require.NoError(t, err, "failed to start container")

		// Wait for container to be running
		readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		err = testutil.WaitForContainerRunning(readyCtx, rawClient, resp.ID)
		require.NoError(t, err, "container did not start")

		// Try to exec a command that doesn't exist
		ios := iostreams.NewTestIOStreams()
		f := &cmdutil.Factory{
			WorkDir:   h.ProjectDir,
			IOStreams: ios.IOStreams,
		}

		cmd := NewCmdExec(f, nil)
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

		// Create a container but don't start it
		_, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
			Name: containerName,
			Config: &container.Config{
				Image: "alpine:latest",
				Cmd:   []string{"sleep", "300"},
				Labels: testutil.AddClawkerLabels(map[string]string{
					testutil.TestLabel: testutil.TestLabelValue,
				}, "exec-error-test", agentName),
			},
		})
		require.NoError(t, err, "failed to create container")
		// Deliberately NOT starting the container

		// Try to exec into the stopped container
		ios := iostreams.NewTestIOStreams()
		f := &cmdutil.Factory{
			WorkDir:   h.ProjectDir,
			IOStreams: ios.IOStreams,
		}

		cmd := NewCmdExec(f, nil)
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

// TestExecIntegration_ScriptExecution tests running scripts via exec command.
// This verifies that scripts created in the container can be executed via exec.
func TestExecIntegration_ScriptExecution(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("exec-script-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := testutil.NewTestClient(t)
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()
	defer testutil.CleanupProjectResources(ctx, dockerClient, "exec-script-test")

	agentName := "test-script-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start a container
	resp, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: testutil.AddClawkerLabels(map[string]string{
				testutil.TestLabel: testutil.TestLabelValue,
			}, "exec-script-test", agentName),
		},
	})
	require.NoError(t, err, "failed to create container")
	_, err = rawClient.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "failed to start container")

	// Wait for container to be running
	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = testutil.WaitForContainerRunning(readyCtx, rawClient, resp.ID)
	require.NoError(t, err, "container did not start")

	// Create a test script in the container
	createScriptCmd := []string{"sh", "-c", `cat > /tmp/test-script.sh << 'SCRIPT'
#!/bin/sh
echo "script-execution-test-output"
echo "args: $@"
SCRIPT
chmod +x /tmp/test-script.sh`}

	execConfig := client.ExecCreateOptions{
		Cmd:          createScriptCmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	execResp, err := rawClient.ExecCreate(ctx, resp.ID, execConfig)
	require.NoError(t, err)
	_, err = rawClient.ExecStart(ctx, execResp.ID, client.ExecStartOptions{})
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
			ios := iostreams.NewTestIOStreams()
			f := &cmdutil.Factory{
				WorkDir:   h.ProjectDir,
				IOStreams: ios.IOStreams,
			}

			var cmdArgs []string
			if tt.useAgentFlag {
				cmdArgs = []string{"--agent", agentName}
				cmdArgs = append(cmdArgs, tt.scriptArgs...)
			} else {
				cmdArgs = []string{containerName}
				cmdArgs = append(cmdArgs, tt.scriptArgs...)
			}

			cmd := NewCmdExec(f, nil)
			cmd.SetArgs(cmdArgs)

			err := cmd.Execute()
			require.NoError(t, err, "exec command failed: stderr=%s", ios.ErrBuf.String())

			tt.checkOutput(t, ios.OutBuf.String())
		})
	}
}

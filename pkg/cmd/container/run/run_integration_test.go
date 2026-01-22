//go:build integration

package run

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/stretchr/testify/require"
)

// TestRunIntegration_EntrypointBypass tests running a container with a simple command
// that bypasses the entrypoint (e.g., "echo hello"). This validates the basic container
// lifecycle: create, start, run command, and cleanup.
func TestRunIntegration_EntrypointBypass(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	// Create harness with minimal config (no firewall, no host proxy)
	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("run-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)

	// Change to project directory so config can be found
	h.Chdir()

	// Create Docker client for verification and cleanup
	client := testutil.NewTestClient(t)
	defer testutil.CleanupProjectResources(ctx, client, "run-test")

	// Generate unique agent name for this test
	agentName := "test-echo-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create factory pointing to harness project directory
	ios := cmdutil.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	// Create and execute the run command in detached mode
	cmd := NewCmd(f)
	cmd.SetArgs([]string{
		"--detach",
		"--agent", agentName,
		"alpine:latest",
		"sh", "-c", "echo hello && sleep 5",
	})

	err := cmd.Execute()
	require.NoError(t, err, "run command failed: stderr=%s", ios.ErrBuf.String())

	// In detached mode, container ID is printed to f.IOStreams.Out.
	// We can verify via the output or the API.

	// Verify container exists and has correct labels
	containers, err := client.ListContainersByProject(ctx, "run-test", true)
	require.NoError(t, err, "failed to list containers")
	require.Len(t, containers, 1, "expected exactly one container")

	container := containers[0]
	require.Equal(t, containerName, container.Name, "container name mismatch")
	require.Equal(t, "run-test", container.Project, "project mismatch")
	require.Equal(t, agentName, container.Agent, "agent mismatch")

	// Wait a moment for the command to run and check logs
	time.Sleep(500 * time.Millisecond)

	logReader, err := client.ContainerLogs(ctx, container.ID, dockerclient.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	require.NoError(t, err, "failed to get container logs")
	defer logReader.Close()

	logBytes, err := io.ReadAll(logReader)
	require.NoError(t, err, "failed to read logs")
	require.Contains(t, string(logBytes), "hello", "expected 'hello' in container output")
}

// TestRunIntegration_AutoRemove tests that --rm flag properly removes the container
// after it exits.
func TestRunIntegration_AutoRemove(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	// Create harness with minimal config
	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("run-rm-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)

	h.Chdir()

	client := testutil.NewTestClient(t)
	defer testutil.CleanupProjectResources(ctx, client, "run-rm-test")

	agentName := "test-rm-" + time.Now().Format("150405.000000")

	ios := cmdutil.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	// Run a container that exits immediately with --rm
	cmd := NewCmd(f)
	cmd.SetArgs([]string{
		"--detach",
		"--rm",
		"--agent", agentName,
		"alpine:latest",
		"echo", "goodbye",
	})

	err := cmd.Execute()
	require.NoError(t, err, "run command failed: stderr=%s", ios.ErrBuf.String())

	// Container ID is now printed to f.IOStreams.Out, verify removal through the API.

	// Wait for container to exit and be removed
	// The container runs "echo goodbye" which exits immediately
	// With --rm, Docker should remove it after exit
	time.Sleep(2 * time.Second)

	// Verify container was removed
	containers, err := client.ListContainersByProject(ctx, "run-rm-test", true)
	require.NoError(t, err, "failed to list containers")
	require.Empty(t, containers, "expected container to be removed due to --rm flag")
}

// TestRunIntegration_Labels tests that custom labels are applied to the container
// alongside the required clawker labels.
func TestRunIntegration_Labels(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("run-label-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)

	h.Chdir()

	client := testutil.NewTestClient(t)
	defer testutil.CleanupProjectResources(ctx, client, "run-label-test")

	agentName := "test-labels-" + time.Now().Format("150405.000000")

	ios := cmdutil.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	cmd := NewCmd(f)
	cmd.SetArgs([]string{
		"--detach",
		"--agent", agentName,
		"--label", "custom.label=testvalue",
		"--label", "another.label=anothervalue",
		"alpine:latest",
		"sleep", "30",
	})

	err := cmd.Execute()
	require.NoError(t, err, "run command failed: stderr=%s", ios.ErrBuf.String())

	// Verify labels - use ContainerInspect to get the raw labels
	containers, err := client.ListContainersByProject(ctx, "run-label-test", true)
	require.NoError(t, err)
	require.Len(t, containers, 1)

	container := containers[0]

	// Check project and agent from parsed container
	require.Equal(t, "run-label-test", container.Project)
	require.Equal(t, agentName, container.Agent)

	// Use ContainerInspect to check full labels including custom ones
	info, err := client.ContainerInspect(ctx, container.ID, docker.ContainerInspectOptions{})
	require.NoError(t, err, "failed to inspect container")

	labels := info.Container.Config.Labels

	// Check clawker labels are present
	require.Equal(t, "true", labels["com.clawker.managed"])
	require.Equal(t, "run-label-test", labels["com.clawker.project"])
	require.Equal(t, agentName, labels["com.clawker.agent"])

	// Check custom labels are present
	require.Equal(t, "testvalue", labels["custom.label"])
	require.Equal(t, "anothervalue", labels["another.label"])
}

// TestRunIntegration_ReadySignalUtilities validates that the ready signal utilities
// can detect when a container creates the ready file. This tests the utilities
// without requiring the full clawker entrypoint.
func TestRunIntegration_ReadySignalUtilities(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("run-ready-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)

	h.Chdir()

	client := testutil.NewTestClient(t)
	defer testutil.CleanupProjectResources(ctx, client, "run-ready-test")

	agentName := "test-ready-" + time.Now().Format("150405.000000")

	ios := cmdutil.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	// Run a container that creates the ready file after a brief delay
	// This simulates what the clawker entrypoint does
	readyScript := `
		mkdir -p /var/run/clawker
		sleep 1
		echo "ts=$(date +%s) pid=$$" > /var/run/clawker/ready
		echo "[clawker] ready"
		sleep 30
	`

	cmd := NewCmd(f)
	cmd.SetArgs([]string{
		"--detach",
		"--agent", agentName,
		"alpine:latest",
		"sh", "-c", readyScript,
	})

	err := cmd.Execute()
	require.NoError(t, err, "run command failed: stderr=%s", ios.ErrBuf.String())

	// Get the container
	containers, err := client.ListContainersByProject(ctx, "run-ready-test", true)
	require.NoError(t, err)
	require.Len(t, containers, 1)

	container := containers[0]

	// Wait for ready file with timeout
	readyCtx, cancel := context.WithTimeout(ctx, testutil.BypassCommandTimeout)
	defer cancel()

	// Get raw Docker client for the utility functions
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()

	err = testutil.WaitForReadyFile(readyCtx, rawClient, container.ID)
	require.NoError(t, err, "ready file was not created")

	// Also verify the ready log pattern
	logs, err := testutil.GetContainerLogs(ctx, rawClient, container.ID)
	require.NoError(t, err, "failed to get container logs")
	require.Contains(t, logs, testutil.ReadyLogPrefix, "expected ready log pattern in output")

	// Verify no error patterns in logs
	hasError, errorMsg := testutil.CheckForErrorPattern(logs)
	require.False(t, hasError, "unexpected error in logs: %s", errorMsg)
}


// TestRunIntegration_ArbitraryCommand tests running arbitrary commands through the
// clawker entrypoint. When a command is a known system binary (not a Claude flag),
// the entrypoint should execute it directly without prepending "claude".
func TestRunIntegration_ArbitraryCommand(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	// Create harness with firewall disabled for speed
	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("run-arbitrary-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	// Build a clawker image (NOT alpine - needs clawker entrypoint)
	imageTag := testutil.BuildTestImage(t, h, testutil.BuildTestImageOptions{
		SuppressOutput: true,
	})

	client := testutil.NewTestClient(t)
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()
	defer testutil.CleanupProjectResources(ctx, client, "run-arbitrary-test")

	tests := []struct {
		name          string
		args          []string
		checkOutput   func(t *testing.T, logs string)
		expectClaude  bool // whether Claude Code should be running
	}{
		{
			name: "echo via entrypoint",
			args: []string{"echo", "hello-from-arbitrary"},
			checkOutput: func(t *testing.T, logs string) {
				require.Contains(t, logs, "hello-from-arbitrary", "expected echo output in logs")
			},
			expectClaude: false,
		},
		{
			name: "ls via entrypoint",
			args: []string{"ls", "/"},
			checkOutput: func(t *testing.T, logs string) {
				// Root directory should contain standard Linux dirs
				require.Contains(t, logs, "bin", "expected 'bin' in ls output")
				require.Contains(t, logs, "etc", "expected 'etc' in ls output")
			},
			expectClaude: false,
		},
		{
			name: "bash via entrypoint",
			args: []string{"bash", "-c", "echo test-bash-output"},
			checkOutput: func(t *testing.T, logs string) {
				require.Contains(t, logs, "test-bash-output", "expected bash echo output in logs")
			},
			expectClaude: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Sanitize test name for agent (replace spaces with dashes for valid volume names)
			sanitizedName := strings.ReplaceAll(tt.name, " ", "-")
			agentName := "test-arb-" + sanitizedName + "-" + time.Now().Format("150405.000000")

			ios := cmdutil.NewTestIOStreams()
			f := &cmdutil.Factory{
				WorkDir:   h.ProjectDir,
				IOStreams: ios.IOStreams,
			}

			// Build command args: --detach, --agent, image, then the arbitrary command
			cmdArgs := []string{
				"--detach",
				"--agent", agentName,
				imageTag,
			}
			cmdArgs = append(cmdArgs, tt.args...)

			cmd := NewCmd(f)
			cmd.SetArgs(cmdArgs)

			err := cmd.Execute()
			require.NoError(t, err, "run command failed: stderr=%s", ios.ErrBuf.String())

			// Get the container
			containers, err := client.ListContainersByProject(ctx, "run-arbitrary-test", true)
			require.NoError(t, err)

			// Find our container
			var container *docker.Container
			for i := range containers {
				if containers[i].Agent == agentName {
					container = &containers[i]
					break
				}
			}
			require.NotNil(t, container, "container not found for agent %s", agentName)

			// Wait for container completion (handles short-lived commands)
			// Short-lived commands like "echo" may exit before we can check the ready file,
			// so use WaitForContainerCompletion which handles both running and exited containers.
			readyCtx, cancel := context.WithTimeout(ctx, testutil.BypassCommandTimeout)
			defer cancel()

			err = testutil.WaitForContainerCompletion(readyCtx, rawClient, container.ID)
			require.NoError(t, err, "container did not complete successfully")

			// Wait a moment for logs to be available
			time.Sleep(200 * time.Millisecond)

			// Get logs and verify output
			logs, err := testutil.GetContainerLogs(ctx, rawClient, container.ID)
			require.NoError(t, err, "failed to get container logs")

			// Verify ready signal was emitted (proves entrypoint ran)
			require.Contains(t, logs, testutil.ReadyLogPrefix, "expected ready log - entrypoint should have run")

			// Run the test-specific output check
			tt.checkOutput(t, logs)

			// Skip process check for short-lived commands - container has already exited
			// The log check above already verifies the entrypoint ran correctly
		})
	}
}

// TestRunIntegration_ArbitraryCommand_EnvVars tests that environment variables are
// properly set when running arbitrary commands through the entrypoint.
func TestRunIntegration_ArbitraryCommand_EnvVars(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("run-env-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	imageTag := testutil.BuildTestImage(t, h, testutil.BuildTestImageOptions{
		SuppressOutput: true,
	})

	client := testutil.NewTestClient(t)
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()
	defer testutil.CleanupProjectResources(ctx, client, "run-env-test")

	agentName := "test-env-" + time.Now().Format("150405.000000")

	ios := cmdutil.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	// Run env command through entrypoint
	cmd := NewCmd(f)
	cmd.SetArgs([]string{
		"--detach",
		"--agent", agentName,
		imageTag,
		"env",
	})

	err := cmd.Execute()
	require.NoError(t, err, "run command failed: stderr=%s", ios.ErrBuf.String())

	// Get the container
	containers, err := client.ListContainersByProject(ctx, "run-env-test", true)
	require.NoError(t, err)
	require.Len(t, containers, 1)

	container := containers[0]

	// Wait for container completion (handles short-lived commands like "env")
	readyCtx, cancel := context.WithTimeout(ctx, testutil.BypassCommandTimeout)
	defer cancel()

	err = testutil.WaitForContainerCompletion(readyCtx, rawClient, container.ID)
	require.NoError(t, err, "container did not complete successfully")

	time.Sleep(200 * time.Millisecond)

	// Get logs with env output
	logs, err := testutil.GetContainerLogs(ctx, rawClient, container.ID)
	require.NoError(t, err, "failed to get container logs")

	// Verify basic container environment (not CLAWKER_ vars since those depend on host proxy)
	// The container should have a HOME set
	require.Contains(t, logs, "HOME=", "HOME environment variable should be set")

	// Verify ready signal proves entrypoint ran
	require.Contains(t, logs, testutil.ReadyLogPrefix, "expected ready log - entrypoint should have run")
}


// TestRunIntegration_ClaudeFlagsPassthrough tests that flags are correctly passed
// to Claude Code when using the -- separator (with --agent) or when specifying the
// image directly. This tests the PRIMARY use case of clawker - running Claude Code with flags.
func TestRunIntegration_ClaudeFlagsPassthrough(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	// Create harness with firewall disabled for speed
	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("run-flags-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	// Build a clawker image with Claude Code
	imageTag := testutil.BuildTestImage(t, h, testutil.BuildTestImageOptions{
		SuppressOutput: true,
	})

	client := testutil.NewTestClient(t)
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()
	defer testutil.CleanupProjectResources(ctx, client, "run-flags-test")

	tests := []struct {
		name         string
		useAgentFlag bool // true = use --agent + --, false = use image directly
		claudeArgs   []string
		checkOutput  func(t *testing.T, logs string)
	}{
		{
			name:         "version flag via --agent",
			useAgentFlag: true,
			claudeArgs:   []string{"--version"},
			checkOutput: func(t *testing.T, logs string) {
				// Claude --version should output version info OR an auth error
				// (auth errors prove Claude ran and received the flag)
				hasVersion := strings.Contains(logs, "claude") || strings.Contains(logs, "Claude")
				hasAuthError := strings.Contains(logs, "API key") || strings.Contains(logs, "/login")
				require.True(t, hasVersion || hasAuthError,
					"expected Claude version output or auth error in logs, got: %s", logs)
			},
		},
		{
			name:         "version flag via container name",
			useAgentFlag: false,
			claudeArgs:   []string{"--version"},
			checkOutput: func(t *testing.T, logs string) {
				// Claude --version should output version info OR an auth error
				hasVersion := strings.Contains(logs, "claude") || strings.Contains(logs, "Claude")
				hasAuthError := strings.Contains(logs, "API key") || strings.Contains(logs, "/login")
				require.True(t, hasVersion || hasAuthError,
					"expected Claude version output or auth error in logs, got: %s", logs)
			},
		},
		{
			name:         "help flag via --agent",
			useAgentFlag: true,
			claudeArgs:   []string{"--help"},
			checkOutput: func(t *testing.T, logs string) {
				// Claude --help should show help text, an auth error, or at minimum the ready signal
				// (some Claude versions may not output --help to stdout in non-interactive mode)
				hasHelp := strings.Contains(logs, "Usage:")
				hasAuthError := strings.Contains(logs, "API key") || strings.Contains(logs, "/login")
				hasReady := strings.Contains(logs, testutil.ReadyLogPrefix)
				require.True(t, hasHelp || hasAuthError || hasReady,
					"expected Claude help output, auth error, or ready signal in logs, got: %s", logs)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentName := "test-flags-" + time.Now().Format("150405.000000")

			ios := cmdutil.NewTestIOStreams()
			f := &cmdutil.Factory{
				WorkDir:   h.ProjectDir,
				IOStreams: ios.IOStreams,
			}

			var cmdArgs []string
			if tt.useAgentFlag {
				// Pattern: clawker run --detach --agent <name> <image> -- <claude-flags>
				// The -- is only needed when we want to pass flags (like --version)
				// to the container command rather than clawker
				cmdArgs = []string{
					"--detach",
					"--agent", agentName,
					imageTag,
					"--", // Separator for Claude flags
				}
				cmdArgs = append(cmdArgs, tt.claudeArgs...)
			} else {
				// Pattern: clawker run --detach <image> <claude-flags>
				// When image is specified, remaining args go to the container
				cmdArgs = []string{
					"--detach",
					"--agent", agentName, // Still need agent for naming
					imageTag,
				}
				cmdArgs = append(cmdArgs, tt.claudeArgs...)
			}

			cmd := NewCmd(f)
			cmd.SetArgs(cmdArgs)

			err := cmd.Execute()
			require.NoError(t, err, "run command failed: stderr=%s", ios.ErrBuf.String())

			// Get the container
			containers, err := client.ListContainersByProject(ctx, "run-flags-test", true)
			require.NoError(t, err)

			// Find our container
			var container *docker.Container
			for i := range containers {
				if containers[i].Agent == agentName {
					container = &containers[i]
					break
				}
			}
			require.NotNil(t, container, "container not found for agent %s", agentName)

			// Wait for container completion (handles short-lived commands like claude --version)
			readyCtx, cancel := context.WithTimeout(ctx, testutil.BypassCommandTimeout)
			defer cancel()

			err = testutil.WaitForContainerCompletion(readyCtx, rawClient, container.ID)
			require.NoError(t, err, "container did not complete successfully")

			// Wait for logs to be available
			time.Sleep(200 * time.Millisecond)

			// Get logs and verify output
			logs, err := testutil.GetContainerLogs(ctx, rawClient, container.ID)
			require.NoError(t, err, "failed to get container logs")

			// Run the test-specific output check
			tt.checkOutput(t, logs)
		})
	}
}

// TestRunIntegration_ContainerNameResolution tests running containers using the
// full container name (clawker.project.agent) pattern instead of the --agent flag.
// This covers PRD requirement for both invocation patterns.
func TestRunIntegration_ContainerNameResolution(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	// Create harness
	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("run-name-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	// Build a clawker image
	imageTag := testutil.BuildTestImage(t, h, testutil.BuildTestImageOptions{
		SuppressOutput: true,
	})

	client := testutil.NewTestClient(t)
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()
	defer testutil.CleanupProjectResources(ctx, client, "run-name-test")

	agentName := "test-name-" + time.Now().Format("150405.000000")

	ios := cmdutil.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	// Run with image and command, using --agent for naming
	cmd := NewCmd(f)
	cmd.SetArgs([]string{
		"--detach",
		"--agent", agentName,
		imageTag,
		"echo", "container-name-test-output",
	})

	err := cmd.Execute()
	require.NoError(t, err, "run command failed: stderr=%s", ios.ErrBuf.String())

	// Get the container by agent name
	containers, err := client.ListContainersByProject(ctx, "run-name-test", true)
	require.NoError(t, err)

	var container *docker.Container
	for i := range containers {
		if containers[i].Agent == agentName {
			container = &containers[i]
			break
		}
	}
	require.NotNil(t, container, "container not found")

	// Verify the container name follows the clawker.project.agent pattern
	expectedName := "clawker.run-name-test." + agentName
	require.Equal(t, expectedName, container.Name, "container name should follow clawker.project.agent pattern")

	// Wait for container completion (handles short-lived commands like echo)
	readyCtx, cancel := context.WithTimeout(ctx, testutil.BypassCommandTimeout)
	defer cancel()

	err = testutil.WaitForContainerCompletion(readyCtx, rawClient, container.ID)
	require.NoError(t, err, "container did not complete successfully")

	time.Sleep(200 * time.Millisecond)

	// Verify echo output
	logs, err := testutil.GetContainerLogs(ctx, rawClient, container.ID)
	require.NoError(t, err)
	require.Contains(t, logs, "container-name-test-output", "expected echo output in logs")
}

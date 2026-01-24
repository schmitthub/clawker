//go:build integration

package run

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testutil"
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
	ios := iostreams.NewTestIOStreams()
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

	ios := iostreams.NewTestIOStreams()
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

	ios := iostreams.NewTestIOStreams()
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

	ios := iostreams.NewTestIOStreams()
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

// TestRunIntegration_ArbitraryCommand tests running arbitrary commands in containers.
// This verifies the Docker client integration: commands are passed through correctly
// to the container.
func TestRunIntegration_ArbitraryCommand(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("run-arbitrary-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	client := testutil.NewTestClient(t)
	defer testutil.CleanupProjectResources(ctx, client, "run-arbitrary-test")

	tests := []struct {
		name        string
		args        []string
		checkOutput func(t *testing.T, logs string)
	}{
		{
			name: "echo command",
			args: []string{"echo", "hello-from-arbitrary"},
			checkOutput: func(t *testing.T, logs string) {
				require.Contains(t, logs, "hello-from-arbitrary", "expected echo output in logs")
			},
		},
		{
			name: "ls command",
			args: []string{"ls", "/"},
			checkOutput: func(t *testing.T, logs string) {
				// Root directory should contain standard Linux dirs
				require.Contains(t, logs, "bin", "expected 'bin' in ls output")
				require.Contains(t, logs, "etc", "expected 'etc' in ls output")
			},
		},
		{
			name: "sh command",
			args: []string{"sh", "-c", "echo test-shell-output"},
			checkOutput: func(t *testing.T, logs string) {
				require.Contains(t, logs, "test-shell-output", "expected shell echo output in logs")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sanitizedName := strings.ReplaceAll(tt.name, " ", "-")
			agentName := "test-arb-" + sanitizedName + "-" + time.Now().Format("150405.000000")

			ios := iostreams.NewTestIOStreams()
			f := &cmdutil.Factory{
				WorkDir:   h.ProjectDir,
				IOStreams: ios.IOStreams,
			}

			// Build command args: --detach, --agent, alpine, then the command
			cmdArgs := []string{
				"--detach",
				"--agent", agentName,
				"alpine:latest",
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

			// Wait for container to complete (short-lived commands)
			rawClient := testutil.NewRawDockerClient(t)
			defer rawClient.Close()

			readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			err = testutil.WaitForContainerExit(readyCtx, rawClient, container.ID)
			require.NoError(t, err, "container did not complete")

			// Wait a moment for logs to be available
			time.Sleep(200 * time.Millisecond)

			// Get logs and verify output
			logs, err := testutil.GetContainerLogs(ctx, rawClient, container.ID)
			require.NoError(t, err, "failed to get container logs")

			// Run the test-specific output check
			tt.checkOutput(t, logs)
		})
	}
}

// TestRunIntegration_ArbitraryCommand_EnvVars tests that environment variables are
// properly passed to containers via the -e flag.
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

	client := testutil.NewTestClient(t)
	defer testutil.CleanupProjectResources(ctx, client, "run-env-test")

	agentName := "test-env-" + time.Now().Format("150405.000000")

	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	// Run env command with a custom environment variable
	cmd := NewCmd(f)
	cmd.SetArgs([]string{
		"--detach",
		"--agent", agentName,
		"-e", "TEST_VAR=test_value_123",
		"alpine:latest",
		"env",
	})

	err := cmd.Execute()
	require.NoError(t, err, "run command failed: stderr=%s", ios.ErrBuf.String())

	// Get the container
	containers, err := client.ListContainersByProject(ctx, "run-env-test", true)
	require.NoError(t, err)
	require.Len(t, containers, 1)

	container := containers[0]

	// Wait for container completion
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()

	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err = testutil.WaitForContainerExit(readyCtx, rawClient, container.ID)
	require.NoError(t, err, "container did not complete")

	time.Sleep(200 * time.Millisecond)

	// Get logs with env output
	logs, err := testutil.GetContainerLogs(ctx, rawClient, container.ID)
	require.NoError(t, err, "failed to get container logs")

	// Verify our custom environment variable was set
	require.Contains(t, logs, "TEST_VAR=test_value_123", "custom environment variable should be set")

	// Verify container has basic environment (HOME, PATH, etc.)
	require.Contains(t, logs, "PATH=", "PATH environment variable should be set")
}

// TODO: TestRunIntegration_ClaudeFlagsPassthrough - requires clawker-built image with Claude Code
// This test should be implemented as part of E2E test infrastructure that builds and tests
// the full clawker entrypoint with Claude Code integration.
// Tracked in: https://github.com/schmitthub/clawker/issues/XXX

// TestRunIntegration_ContainerNameResolution tests that container names follow the
// clawker.project.agent naming convention.
func TestRunIntegration_ContainerNameResolution(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("run-name-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	client := testutil.NewTestClient(t)
	defer testutil.CleanupProjectResources(ctx, client, "run-name-test")

	agentName := "test-name-" + time.Now().Format("150405.000000")

	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	// Run with --agent flag and verify naming convention
	cmd := NewCmd(f)
	cmd.SetArgs([]string{
		"--detach",
		"--agent", agentName,
		"alpine:latest",
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

	// Verify labels are correct
	info, err := client.ContainerInspect(ctx, container.ID, docker.ContainerInspectOptions{})
	require.NoError(t, err)

	labels := info.Container.Config.Labels
	require.Equal(t, "true", labels["com.clawker.managed"], "managed label missing")
	require.Equal(t, "run-name-test", labels["com.clawker.project"], "project label mismatch")
	require.Equal(t, agentName, labels["com.clawker.agent"], "agent label mismatch")

	// Wait for container completion and verify output
	rawClient := testutil.NewRawDockerClient(t)
	defer rawClient.Close()

	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err = testutil.WaitForContainerExit(readyCtx, rawClient, container.ID)
	require.NoError(t, err, "container did not complete")

	time.Sleep(200 * time.Millisecond)

	logs, err := testutil.GetContainerLogs(ctx, rawClient, container.ID)
	require.NoError(t, err)
	require.Contains(t, logs, "container-name-test-output", "expected echo output in logs")
}

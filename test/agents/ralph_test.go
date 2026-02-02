package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/ralph"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/schmitthub/clawker/test/harness/builders"
	"github.com/stretchr/testify/require"
)

// TestRalphIntegration_SessionCreatedImmediately verifies that when ralph run
// starts, the session file is created immediately before the first exec completes.
// This was a bug where the session was only saved after the first loop finished,
// causing "ralph status" to show "No session found" during long-running loops.
func TestRalphIntegration_SessionCreatedImmediately(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	// Setup temp directories for session store
	tempDir := t.TempDir()
	storeDir := filepath.Join(tempDir, "ralph")

	// Create a harness for test container
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("ralph-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	rawClient := harness.NewRawDockerClient(t)
	defer rawClient.Close()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "ralph-test"); err != nil {
			t.Logf("WARNING: cleanup failed for ralph-test: %v", err)
		}
	}()

	// Generate unique container name
	agentName := "test-ralph-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start container
	resp, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				"com.clawker.managed": "true",
				"com.clawker.project": "ralph-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = rawClient.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err, "failed to start container")

	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, rawClient, resp.ID)
	require.NoError(t, err, "container did not start")

	// Create runner with custom store directory
	store := ralph.NewSessionStore(storeDir)
	history := ralph.NewHistoryStore(storeDir)
	wrappedClient := &docker.Client{Engine: dockerClient.Engine}
	runner := ralph.NewRunnerWith(wrappedClient, store, history)

	// Channel to signal when Run has started
	runStarted := make(chan struct{})

	// Run ralph in a goroutine
	errCh := make(chan error, 1)
	go func() {
		close(runStarted)
		_, err := runner.Run(ctx, ralph.LoopOptions{
			ContainerName: containerName,
			Project:       "ralph-test",
			Agent:         agentName,
			Prompt:        "echo hello", // Simple command that exits quickly
			MaxLoops:      1,
			Timeout:       30 * time.Second,
		})
		errCh <- err
	}()

	// Wait for Run to start
	<-runStarted

	// Give it a moment to create the session
	time.Sleep(500 * time.Millisecond)

	// Verify session file exists BEFORE the exec could complete
	sessionPath := filepath.Join(storeDir, "sessions", "ralph-test."+agentName+".json")
	_, err = os.Stat(sessionPath)
	require.NoError(t, err, "session file should exist immediately after Run starts")

	// Verify we can load the session
	session, err := store.LoadSession("ralph-test", agentName)
	require.NoError(t, err, "should be able to load session")
	require.NotNil(t, session, "session should not be nil")
	require.Equal(t, "ralph-test", session.Project)
	require.Equal(t, agentName, session.Agent)
}

// TestRalphIntegration_ExecCaptureTimeout verifies that the ExecCapture function
// properly respects context cancellation and doesn't hang forever.
func TestRalphIntegration_ExecCaptureTimeout(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	tempDir := t.TempDir()
	storeDir := filepath.Join(tempDir, "ralph")

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("ralph-timeout-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	rawClient := harness.NewRawDockerClient(t)
	defer rawClient.Close()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "ralph-timeout-test"); err != nil {
			t.Logf("WARNING: cleanup failed for ralph-timeout-test: %v", err)
		}
	}()

	agentName := "test-timeout-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create container
	resp, err := rawClient.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				"com.clawker.managed": "true",
				"com.clawker.project": "ralph-timeout-test",
				"com.clawker.agent":   agentName,
			},
		},
	})
	require.NoError(t, err)

	_, err = rawClient.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	require.NoError(t, err)

	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, rawClient, resp.ID)
	require.NoError(t, err)

	store := ralph.NewSessionStore(storeDir)
	history := ralph.NewHistoryStore(storeDir)
	wrappedClient := &docker.Client{Engine: dockerClient.Engine}
	runner := ralph.NewRunnerWith(wrappedClient, store, history)

	// Test that ExecCapture with a short timeout doesn't hang
	execCtx, execCancel := context.WithTimeout(ctx, 2*time.Second)
	defer execCancel()

	start := time.Now()
	// This command takes 30 seconds but our context times out in 2 seconds
	output, exitCode, err := runner.ExecCapture(execCtx, containerName, []string{"sleep", "30"}, nil)
	elapsed := time.Since(start)

	// Should complete in roughly 2 seconds (the timeout), not 30 seconds
	require.Less(t, elapsed, 10*time.Second, "ExecCapture should respect context timeout")

	// Should have a timeout error
	require.Error(t, err, "expected timeout error")
	require.Contains(t, err.Error(), "timed out", "error should mention timeout")

	// Exit code should be -1 for timeout
	require.Equal(t, -1, exitCode, "exit code should be -1 for timeout")

	// Output may have partial data, that's fine
	_ = output
}

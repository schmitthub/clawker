package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/schmitthub/clawker/test/harness/builders"
	"github.com/stretchr/testify/require"
)

// TestLoopIntegration_SessionCreatedImmediately verifies that when loop run
// starts, the session file is created immediately before the first exec completes.
// This was a bug where the session was only saved after the first loop finished,
// causing "loop status" to show "No session found" during long-running loops.
func TestLoopIntegration_SessionCreatedImmediately(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	// Setup temp directories for session store
	tempDir := t.TempDir()
	storeDir := filepath.Join(tempDir, "loop")

	// Create a harness for test container
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("loop-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "loop-test"); err != nil {
			t.Logf("WARNING: cleanup failed for loop-test: %v", err)
		}
	}()

	// Generate unique container name
	agentName := "test-loop-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create and start container — whail auto-injects managed + test labels
	resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				docker.LabelProject: "loop-test",
				docker.LabelAgent:   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = dockerClient.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID})
	require.NoError(t, err, "failed to start container")

	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, dockerClient, resp.ID)
	require.NoError(t, err, "container did not start")

	// Create runner with custom store directory
	store := loop.NewSessionStore(storeDir)
	history := loop.NewHistoryStore(storeDir)
	wrappedClient := &docker.Client{Engine: dockerClient.Engine}
	runner := loop.NewRunnerWith(wrappedClient, store, history)

	// Channel to signal when Run has started
	runStarted := make(chan struct{})

	// Run loop in a goroutine
	errCh := make(chan error, 1)
	go func() {
		close(runStarted)
		_, err := runner.Run(ctx, loop.Options{
			ContainerName: containerName,
			Project:       "loop-test",
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
	sessionPath := filepath.Join(storeDir, "sessions", "loop-test."+agentName+".json")
	_, err = os.Stat(sessionPath)
	require.NoError(t, err, "session file should exist immediately after Run starts")

	// Verify we can load the session
	session, err := store.LoadSession("loop-test", agentName)
	require.NoError(t, err, "should be able to load session")
	require.NotNil(t, session, "session should not be nil")
	require.Equal(t, "loop-test", session.Project)
	require.Equal(t, agentName, session.Agent)
}

// TestLoopIntegration_ExecCaptureTimeout verifies that the ExecCapture function
// properly respects context cancellation and doesn't hang forever.
func TestLoopIntegration_ExecCaptureTimeout(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	tempDir := t.TempDir()
	storeDir := filepath.Join(tempDir, "loop")

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("loop-timeout-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, "loop-timeout-test"); err != nil {
			t.Logf("WARNING: cleanup failed for loop-timeout-test: %v", err)
		}
	}()

	agentName := "test-timeout-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Create container — whail auto-injects managed + test labels
	resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				docker.LabelProject: "loop-timeout-test",
				docker.LabelAgent:   agentName,
			},
		},
	})
	require.NoError(t, err)

	_, err = dockerClient.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID})
	require.NoError(t, err)

	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = harness.WaitForContainerRunning(readyCtx, dockerClient, resp.ID)
	require.NoError(t, err)

	store := loop.NewSessionStore(storeDir)
	history := loop.NewHistoryStore(storeDir)
	wrappedClient := &docker.Client{Engine: dockerClient.Engine}
	runner := loop.NewRunnerWith(wrappedClient, store, history)

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

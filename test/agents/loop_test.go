package agents

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/schmitthub/clawker/test/harness/builders"
	"github.com/stretchr/testify/assert"
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

// ---------------------------------------------------------------------------
// Helper: createLoopTestContainer creates a running Alpine container wired for
// loop integration tests. Returns the container ID, container name, and a
// cleanup function. The container runs "sleep 300" so it stays alive for exec.
// ---------------------------------------------------------------------------

func createLoopTestContainer(t *testing.T, ctx context.Context, project, agentName string) (
	containerID, containerName string, dockerClient *docker.Client, cleanup func(),
) {
	t.Helper()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient = harness.NewTestClient(t)
	containerName = h.ContainerName(agentName)

	resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				docker.LabelProject: project,
				docker.LabelAgent:   agentName,
			},
		},
	})
	require.NoError(t, err, "failed to create container")

	_, err = dockerClient.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID})
	require.NoError(t, err, "failed to start container")

	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	require.NoError(t, harness.WaitForContainerRunning(readyCtx, dockerClient, resp.ID), "container did not start")

	cleanup = func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, project); err != nil {
			t.Logf("WARNING: cleanup failed for %s: %v", project, err)
		}
	}
	containerID = resp.ID
	return
}

// newTestRunner creates a Runner with temp-dir stores suitable for testing.
func newTestRunner(t *testing.T, dockerClient *docker.Client) (*loop.Runner, *loop.SessionStore, *loop.HistoryStore) {
	t.Helper()
	tempDir := t.TempDir()
	storeDir := filepath.Join(tempDir, "loop")
	store := loop.NewSessionStore(storeDir)
	history := loop.NewHistoryStore(storeDir)
	wrappedClient := &docker.Client{Engine: dockerClient.Engine}
	return loop.NewRunnerWith(wrappedClient, store, history), store, history
}

// ---------------------------------------------------------------------------
// Runner-level integration tests
// ---------------------------------------------------------------------------

// TestLoopIntegration_RunSingleIteration verifies that Runner.Run executes
// a single loop iteration, produces a result, and persists the session. The
// exec command outputs a LOOP_STATUS block so the circuit breaker is satisfied.
func TestLoopIntegration_RunSingleIteration(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-single-test"
	agentName := "test-single-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()
	_ = containerID

	runner, store, _ := newTestRunner(t, dockerClient)

	// The prompt is a shell command that prints a LOOP_STATUS block.
	// Runner.ExecCapture runs ["claude", "-p", prompt] but since claude is not
	// installed in the alpine container, the command will fail with a non-zero
	// exit code. We still verify the runner machinery handles this gracefully.
	//
	// For a true integration path we exec a simple echo command instead.
	// We inject a small script that outputs a parseable LOOP_STATUS block.
	// Write a helper script into the container that mimics claude -p output.
	script := `#!/bin/sh
echo "---LOOP_STATUS---"
echo "STATUS: COMPLETE"
echo "TASKS_COMPLETED_THIS_LOOP: 1"
echo "FILES_MODIFIED: 2"
echo "TESTS_STATUS: ALL_PASSING"
echo "WORK_TYPE: IMPLEMENTATION"
echo "EXIT_SIGNAL: true"
echo "---END_LOOP_STATUS---"
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	// Now run the loop — it will exec "claude -p <prompt>" which will run our script.
	result, err := runner.Run(ctx, loop.Options{
		ContainerName: containerName,
		Project:       project,
		Agent:         agentName,
		Prompt:        "test prompt",
		MaxLoops:      1,
		Timeout:       30 * time.Second,
		LoopDelaySeconds: 0,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.LoopsCompleted, "should complete exactly 1 loop")

	// Verify session was persisted
	session, err := store.LoadSession(project, agentName)
	require.NoError(t, err)
	require.NotNil(t, session, "session should be persisted")
	assert.Equal(t, project, session.Project)
	assert.Equal(t, agentName, session.Agent)
	assert.Equal(t, 1, session.LoopsCompleted)
}

// TestLoopIntegration_SessionPersistenceAcrossIterations verifies that session
// data accumulates correctly across multiple loop iterations.
func TestLoopIntegration_SessionPersistenceAcrossIterations(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-persist-test"
	agentName := "test-persist-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, store, history := newTestRunner(t, dockerClient)

	// Write a fake claude script that outputs different LOOP_STATUS blocks.
	// The script uses a counter file to track invocations.
	script := `#!/bin/sh
COUNTER_FILE=/tmp/loop-counter
if [ ! -f "$COUNTER_FILE" ]; then
  echo 1 > "$COUNTER_FILE"
else
  COUNT=$(cat "$COUNTER_FILE")
  COUNT=$((COUNT + 1))
  echo $COUNT > "$COUNTER_FILE"
fi
COUNT=$(cat "$COUNTER_FILE")

echo "---LOOP_STATUS---"
echo "STATUS: IN_PROGRESS"
echo "TASKS_COMPLETED_THIS_LOOP: 1"
echo "FILES_MODIFIED: 2"
echo "TESTS_STATUS: ALL_PASSING"
echo "WORK_TYPE: IMPLEMENTATION"
echo "EXIT_SIGNAL: false"
echo "---END_LOOP_STATUS---"
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	// Run 3 iterations
	result, err := runner.Run(ctx, loop.Options{
		ContainerName:   containerName,
		Project:         project,
		Agent:           agentName,
		Prompt:          "test prompt",
		MaxLoops:        3,
		Timeout:         30 * time.Second,
		LoopDelaySeconds: 0,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 3, result.LoopsCompleted, "should complete 3 loops")
	assert.Equal(t, "max loops reached", result.ExitReason)

	// Verify session state accumulated
	session, err := store.LoadSession(project, agentName)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, 3, session.LoopsCompleted)
	// Each loop reported 1 task completed, so total should be 3
	assert.Equal(t, 3, session.TotalTasksCompleted, "tasks should accumulate across loops")
	// Each loop reported 2 files modified, so total should be 6
	assert.Equal(t, 6, session.TotalFilesModified, "files should accumulate across loops")
	assert.Equal(t, 0, session.NoProgressCount, "should have progress each loop")

	// Verify history has entries
	sessionHistory, err := history.LoadSessionHistory(project, agentName)
	require.NoError(t, err)
	require.NotNil(t, sessionHistory)
	assert.Greater(t, len(sessionHistory.Entries), 0, "history should have entries")
}

// TestLoopIntegration_CircuitBreakerTripsOnStagnation verifies the circuit
// breaker trips when the agent makes no progress for StagnationThreshold loops.
func TestLoopIntegration_CircuitBreakerTripsOnStagnation(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-stagnation-test"
	agentName := "test-stagnation-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, store, _ := newTestRunner(t, dockerClient)

	// Write a fake claude script that outputs a LOOP_STATUS with no progress.
	// tasks_completed: 0 and files_modified: 0 means no progress.
	script := `#!/bin/sh
echo "---LOOP_STATUS---"
echo "STATUS: IN_PROGRESS"
echo "TASKS_COMPLETED_THIS_LOOP: 0"
echo "FILES_MODIFIED: 0"
echo "TESTS_STATUS: NOT_RUN"
echo "WORK_TYPE: IMPLEMENTATION"
echo "EXIT_SIGNAL: false"
echo "---END_LOOP_STATUS---"
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	// Run with a low stagnation threshold
	result, err := runner.Run(ctx, loop.Options{
		ContainerName:       containerName,
		Project:             project,
		Agent:               agentName,
		Prompt:              "test prompt",
		MaxLoops:            20, // High enough to never hit
		StagnationThreshold: 3,  // Trip after 3 no-progress loops
		Timeout:             30 * time.Second,
		LoopDelaySeconds:    0,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Circuit breaker should have tripped before MaxLoops
	assert.Less(t, result.LoopsCompleted, 20, "should trip before max loops")
	assert.Contains(t, result.ExitReason, "stagnation", "exit reason should mention stagnation")
	assert.Error(t, result.Error, "should have an error when circuit trips")

	// Verify circuit state was persisted as tripped
	circuitState, err := store.LoadCircuitState(project, agentName)
	require.NoError(t, err)
	require.NotNil(t, circuitState, "circuit state should be persisted")
	assert.True(t, circuitState.Tripped, "circuit should be tripped")
	assert.NotEmpty(t, circuitState.TripReason)
	assert.NotNil(t, circuitState.TrippedAt)
}

// TestLoopIntegration_CircuitBreakerBlocksRerun verifies that a tripped circuit
// breaker prevents new runs until reset.
func TestLoopIntegration_CircuitBreakerBlocksRerun(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-blocked-test"
	agentName := "test-blocked-" + time.Now().Format("150405.000000")
	_, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, store, _ := newTestRunner(t, dockerClient)

	// Manually save a tripped circuit state
	now := time.Now()
	err := store.SaveCircuitState(&loop.CircuitState{
		Project:    project,
		Agent:      agentName,
		Tripped:    true,
		TripReason: "test: manually tripped",
		TrippedAt:  &now,
	})
	require.NoError(t, err)

	// Attempt to run — should fail immediately
	result, err := runner.Run(ctx, loop.Options{
		ContainerName: containerName,
		Project:       project,
		Agent:         agentName,
		Prompt:        "test",
		MaxLoops:      1,
		Timeout:       10 * time.Second,
	})
	require.NoError(t, err) // Run returns nil error — the error is in Result
	require.NotNil(t, result)
	assert.Equal(t, 0, result.LoopsCompleted, "no loops should run when circuit is tripped")
	assert.Contains(t, result.ExitReason, "circuit already tripped")
	assert.Error(t, result.Error)
}

// TestLoopIntegration_ResetCircuitAllowsRerun verifies that the --reset-circuit
// option clears a tripped circuit breaker and allows the loop to proceed.
func TestLoopIntegration_ResetCircuitAllowsRerun(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-reset-test"
	agentName := "test-reset-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, store, _ := newTestRunner(t, dockerClient)

	// Pre-trip the circuit
	now := time.Now()
	err := store.SaveCircuitState(&loop.CircuitState{
		Project:    project,
		Agent:      agentName,
		Tripped:    true,
		TripReason: "test: pre-tripped",
		TrippedAt:  &now,
	})
	require.NoError(t, err)

	// Write a completion script so the loop exits cleanly
	script := `#!/bin/sh
echo "---LOOP_STATUS---"
echo "STATUS: COMPLETE"
echo "TASKS_COMPLETED_THIS_LOOP: 1"
echo "FILES_MODIFIED: 1"
echo "TESTS_STATUS: ALL_PASSING"
echo "WORK_TYPE: IMPLEMENTATION"
echo "EXIT_SIGNAL: true"
echo "---END_LOOP_STATUS---"
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	// Run with --reset-circuit
	result, err := runner.Run(ctx, loop.Options{
		ContainerName:   containerName,
		Project:         project,
		Agent:           agentName,
		Prompt:          "test",
		MaxLoops:        1,
		Timeout:         30 * time.Second,
		ResetCircuit:    true,
		LoopDelaySeconds: 0,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.LoopsCompleted, "should run after circuit reset")

	// Circuit state should no longer be tripped
	circuitState, err := store.LoadCircuitState(project, agentName)
	require.NoError(t, err)
	// After reset + successful run, circuit state file may not exist or be clean
	if circuitState != nil {
		assert.False(t, circuitState.Tripped, "circuit should not be tripped after reset")
	}
}

// TestLoopIntegration_CompletionDetection verifies that the loop exits when the
// agent signals completion via exit_signal: true in the LOOP_STATUS block.
func TestLoopIntegration_CompletionDetection(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-complete-test"
	agentName := "test-complete-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, _, _ := newTestRunner(t, dockerClient)

	// Write a fake claude that signals completion on the 2nd invocation.
	script := `#!/bin/sh
COUNTER_FILE=/tmp/loop-counter
if [ ! -f "$COUNTER_FILE" ]; then
  echo 1 > "$COUNTER_FILE"
  COUNT=1
else
  COUNT=$(cat "$COUNTER_FILE")
  COUNT=$((COUNT + 1))
  echo $COUNT > "$COUNTER_FILE"
fi

if [ "$COUNT" -ge 2 ]; then
  echo "---LOOP_STATUS---"
  echo "STATUS: COMPLETE"
  echo "TASKS_COMPLETED_THIS_LOOP: 2"
  echo "FILES_MODIFIED: 3"
  echo "TESTS_STATUS: ALL_PASSING"
  echo "WORK_TYPE: IMPLEMENTATION"
  echo "EXIT_SIGNAL: true"
  echo "---END_LOOP_STATUS---"
else
  echo "---LOOP_STATUS---"
  echo "STATUS: IN_PROGRESS"
  echo "TASKS_COMPLETED_THIS_LOOP: 1"
  echo "FILES_MODIFIED: 1"
  echo "TESTS_STATUS: ALL_PASSING"
  echo "WORK_TYPE: IMPLEMENTATION"
  echo "EXIT_SIGNAL: false"
  echo "---END_LOOP_STATUS---"
fi
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	result, err := runner.Run(ctx, loop.Options{
		ContainerName:   containerName,
		Project:         project,
		Agent:           agentName,
		Prompt:          "complete the task",
		MaxLoops:        10,
		Timeout:         30 * time.Second,
		LoopDelaySeconds: 0,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2, result.LoopsCompleted, "should complete after 2 loops (completion on 2nd)")
	assert.Contains(t, result.ExitReason, "completion", "should exit due to completion signal")
	assert.Nil(t, result.Error, "completion is not an error")
	require.NotNil(t, result.FinalStatus)
	assert.True(t, result.FinalStatus.ExitSignal, "final status should have exit_signal=true")
}

// TestLoopIntegration_LOOPSTATUSParsing verifies end-to-end LOOP_STATUS block
// parsing from actual docker exec output through the analyzer.
func TestLoopIntegration_LOOPSTATUSParsing(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-status-parse-test"
	agentName := "test-parse-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, _, _ := newTestRunner(t, dockerClient)

	// Write a rich LOOP_STATUS block to verify all fields parse correctly.
	script := `#!/bin/sh
echo "Some preceding output from the agent..."
echo "Working on task: fix authentication"
echo ""
echo "---LOOP_STATUS---"
echo "STATUS: IN_PROGRESS"
echo "TASKS_COMPLETED_THIS_LOOP: 3"
echo "FILES_MODIFIED: 7"
echo "TESTS_STATUS: FAILING"
echo "WORK_TYPE: TESTING"
echo "RECOMMENDATION: Need to fix auth test mocks"
echo "EXIT_SIGNAL: false"
echo "---END_LOOP_STATUS---"
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	// Track what the callbacks receive
	var capturedStatus *loop.Status
	result, err := runner.Run(ctx, loop.Options{
		ContainerName:   containerName,
		Project:         project,
		Agent:           agentName,
		Prompt:          "fix tests",
		MaxLoops:        1,
		Timeout:         30 * time.Second,
		LoopDelaySeconds: 0,
		OnLoopEnd: func(loopNum int, status *loop.Status, loopErr error) {
			capturedStatus = status
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify the LOOP_STATUS was fully parsed
	require.NotNil(t, result.FinalStatus, "FinalStatus should be populated from LOOP_STATUS block")
	assert.Equal(t, "IN_PROGRESS", result.FinalStatus.Status)
	assert.Equal(t, 3, result.FinalStatus.TasksCompleted)
	assert.Equal(t, 7, result.FinalStatus.FilesModified)
	assert.Equal(t, "FAILING", result.FinalStatus.TestsStatus)
	assert.Equal(t, "TESTING", result.FinalStatus.WorkType)
	assert.Equal(t, "Need to fix auth test mocks", result.FinalStatus.Recommendation)
	assert.False(t, result.FinalStatus.ExitSignal)

	// The OnLoopEnd callback should also receive the parsed status
	require.NotNil(t, capturedStatus, "OnLoopEnd should receive parsed status")
	assert.Equal(t, "IN_PROGRESS", capturedStatus.Status)
	assert.Equal(t, 3, capturedStatus.TasksCompleted)
}

// TestLoopIntegration_NoLOOPSTATUS_CountsAsNoProgress verifies that when the
// agent output contains no LOOP_STATUS block, it counts as no progress toward
// the stagnation threshold.
func TestLoopIntegration_NoLOOPSTATUS_CountsAsNoProgress(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-nostatus-test"
	agentName := "test-nostatus-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, store, _ := newTestRunner(t, dockerClient)

	// Write a claude script that outputs NO LOOP_STATUS block.
	script := `#!/bin/sh
echo "I did some work but forgot the status block"
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	result, err := runner.Run(ctx, loop.Options{
		ContainerName:       containerName,
		Project:             project,
		Agent:               agentName,
		Prompt:              "test",
		MaxLoops:            10,
		StagnationThreshold: 3,
		Timeout:             30 * time.Second,
		LoopDelaySeconds:    0,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should trip after StagnationThreshold loops with no progress
	assert.LessOrEqual(t, result.LoopsCompleted, 4, "should trip around stagnation threshold")
	assert.Contains(t, result.ExitReason, "stagnation")

	// Session should show no-progress count
	session, err := store.LoadSession(project, agentName)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Greater(t, session.NoProgressCount, 0, "session should track no-progress count")
}

// TestLoopIntegration_ContextCancellation verifies that Runner.Run exits
// promptly when the context is cancelled mid-loop.
func TestLoopIntegration_ContextCancellation(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-cancel-test"
	agentName := "test-cancel-" + time.Now().Format("150405.000000")

	bgCtx := context.Background()
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, bgCtx, project, agentName)
	defer cleanup()

	runner, _, _ := newTestRunner(t, dockerClient)

	// Write a slow script so the loop doesn't finish before we cancel.
	script := `#!/bin/sh
echo "starting long work..."
sleep 60
echo "---LOOP_STATUS---"
echo "STATUS: IN_PROGRESS"
echo "TASKS_COMPLETED_THIS_LOOP: 0"
echo "FILES_MODIFIED: 0"
echo "---END_LOOP_STATUS---"
`
	writeScriptToContainer(t, bgCtx, dockerClient, containerID, "/usr/local/bin/claude", script)

	ctx, cancel := context.WithCancel(bgCtx)
	// Cancel after a brief delay
	go func() {
		time.Sleep(3 * time.Second)
		cancel()
	}()

	start := time.Now()
	result, err := runner.Run(ctx, loop.Options{
		ContainerName:   containerName,
		Project:         project,
		Agent:           agentName,
		Prompt:          "test",
		MaxLoops:        5,
		Timeout:         120 * time.Second,
		LoopDelaySeconds: 0,
	})
	elapsed := time.Since(start)

	// Should exit within a few seconds of the cancel (not 120s timeout)
	assert.Less(t, elapsed, 30*time.Second, "should exit promptly after context cancellation")

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.ExitReason, "context cancelled")
}

// TestLoopIntegration_OnOutputCallback verifies that output chunks from the
// agent are delivered via the OnOutput callback during execution.
func TestLoopIntegration_OnOutputCallback(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-output-test"
	agentName := "test-output-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, _, _ := newTestRunner(t, dockerClient)

	script := `#!/bin/sh
echo "OUTPUT_LINE_1: hello from the agent"
echo "OUTPUT_LINE_2: working on task"
echo "---LOOP_STATUS---"
echo "STATUS: COMPLETE"
echo "TASKS_COMPLETED_THIS_LOOP: 1"
echo "FILES_MODIFIED: 1"
echo "EXIT_SIGNAL: true"
echo "---END_LOOP_STATUS---"
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	var outputChunks []string
	result, err := runner.Run(ctx, loop.Options{
		ContainerName:   containerName,
		Project:         project,
		Agent:           agentName,
		Prompt:          "test",
		MaxLoops:        1,
		Timeout:         30 * time.Second,
		LoopDelaySeconds: 0,
		OnOutput: func(chunk []byte) {
			outputChunks = append(outputChunks, string(chunk))
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify we received output via callback
	combined := strings.Join(outputChunks, "")
	assert.Contains(t, combined, "OUTPUT_LINE_1", "should receive agent output via callback")
	assert.Contains(t, combined, "OUTPUT_LINE_2", "should receive all output lines")
}

// TestLoopIntegration_HookInjection verifies that InjectLoopHooks properly
// writes hook configuration and scripts into a container.
func TestLoopIntegration_HookInjection(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-hooks-test"
	agentName := "test-hooks-" + time.Now().Format("150405.000000")

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, project); err != nil {
			t.Logf("WARNING: cleanup failed for %s: %v", project, err)
		}
	}()

	containerName := h.ContainerName(agentName)

	// Create container (NOT started — InjectLoopHooks works on created containers)
	resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				docker.LabelProject: project,
				docker.LabelAgent:   agentName,
			},
		},
	})
	require.NoError(t, err)

	// Inject default hooks
	copyFn := newCopyFn(dockerClient)
	err = injectLoopHooksForTest(ctx, resp.ID, "", copyFn)
	require.NoError(t, err, "InjectLoopHooks should succeed")

	// Start the container so we can exec into it to verify files
	_, err = dockerClient.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID})
	require.NoError(t, err)

	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	require.NoError(t, harness.WaitForContainerRunning(readyCtx, dockerClient, resp.ID))

	// Verify settings.json was injected with hooks
	ctr := &harness.RunningContainer{ID: resp.ID, Name: containerName}
	settingsResult, err := ctr.Exec(ctx, dockerClient, "cat", "/home/claude/.claude/settings.json")
	require.NoError(t, err, "settings.json should exist")
	assert.Equal(t, 0, settingsResult.ExitCode, "cat settings.json should succeed")

	// Parse settings.json and verify hooks structure
	var settings struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	err = json.Unmarshal([]byte(settingsResult.CleanOutput()), &settings)
	require.NoError(t, err, "settings.json should be valid JSON")
	require.NotNil(t, settings.Hooks, "settings.json should contain hooks key")
	assert.Contains(t, settings.Hooks, "Stop", "hooks should include Stop event")
	assert.Contains(t, settings.Hooks, "SessionStart", "hooks should include SessionStart event")

	// Verify stop-check script was injected
	scriptResult, err := ctr.Exec(ctx, dockerClient, "cat", loop.StopCheckScriptPath)
	require.NoError(t, err, "stop-check script should exist")
	assert.Equal(t, 0, scriptResult.ExitCode, "cat stop-check.js should succeed")
	assert.Contains(t, scriptResult.CleanOutput(), "LOOP_STATUS", "stop-check script should reference LOOP_STATUS")

	// Verify the hook script directory exists
	dirResult, err := ctr.Exec(ctx, dockerClient, "ls", loop.HookScriptDir)
	require.NoError(t, err)
	assert.Equal(t, 0, dirResult.ExitCode, "hook script directory should exist")
}

// TestLoopIntegration_CustomHooksFile verifies that when a custom hooks file
// is provided, it replaces the default hooks entirely.
func TestLoopIntegration_CustomHooksFile(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-custom-hooks-test"
	agentName := "test-custom-hooks-" + time.Now().Format("150405.000000")

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	dockerClient := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, project); err != nil {
			t.Logf("WARNING: cleanup failed for %s: %v", project, err)
		}
	}()

	containerName := h.ContainerName(agentName)

	// Write a custom hooks JSON file
	customHooks := `{
  "PreToolUse": [
    {
      "matcher": ".*",
      "hooks": [
        {"type": "command", "command": "echo custom-hook-fired"}
      ]
    }
  ]
}`
	customHooksPath := filepath.Join(t.TempDir(), "custom-hooks.json")
	require.NoError(t, os.WriteFile(customHooksPath, []byte(customHooks), 0644))

	// Create container
	resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"sleep", "300"},
			Labels: map[string]string{
				docker.LabelProject: project,
				docker.LabelAgent:   agentName,
			},
		},
	})
	require.NoError(t, err)

	// Inject custom hooks
	copyFn := newCopyFn(dockerClient)
	err = injectLoopHooksForTest(ctx, resp.ID, customHooksPath, copyFn)
	require.NoError(t, err, "InjectLoopHooks with custom file should succeed")

	// Start and verify
	_, err = dockerClient.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: resp.ID})
	require.NoError(t, err)

	readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	require.NoError(t, harness.WaitForContainerRunning(readyCtx, dockerClient, resp.ID))

	// Verify settings.json contains custom hooks, NOT default ones
	ctr := &harness.RunningContainer{ID: resp.ID, Name: containerName}
	settingsResult, err := ctr.Exec(ctx, dockerClient, "cat", "/home/claude/.claude/settings.json")
	require.NoError(t, err)
	assert.Equal(t, 0, settingsResult.ExitCode)

	var settings struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	err = json.Unmarshal([]byte(settingsResult.CleanOutput()), &settings)
	require.NoError(t, err)
	require.NotNil(t, settings.Hooks)
	assert.Contains(t, settings.Hooks, "PreToolUse", "should have custom PreToolUse hook")
	assert.NotContains(t, settings.Hooks, "Stop", "should NOT have default Stop hook (custom replaces)")

	// Verify stop-check.js was NOT injected (custom hooks don't get default scripts)
	scriptResult, err := ctr.Exec(ctx, dockerClient, "ls", loop.StopCheckScriptPath)
	require.NoError(t, err)
	assert.NotEqual(t, 0, scriptResult.ExitCode, "stop-check.js should NOT exist with custom hooks")
}

// TestLoopIntegration_SessionExpiration verifies that expired sessions are
// cleaned up and a fresh session is started.
func TestLoopIntegration_SessionExpiration(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-expire-test"
	agentName := "test-expire-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, store, _ := newTestRunner(t, dockerClient)

	// Pre-create an expired session (started 48 hours ago)
	oldSession := &loop.Session{
		Project:        project,
		Agent:          agentName,
		StartedAt:      time.Now().Add(-48 * time.Hour),
		UpdatedAt:      time.Now().Add(-48 * time.Hour),
		LoopsCompleted: 10,
		Status:         "IN_PROGRESS",
	}
	require.NoError(t, store.SaveSession(oldSession))

	// Write a completion script
	script := `#!/bin/sh
echo "---LOOP_STATUS---"
echo "STATUS: COMPLETE"
echo "TASKS_COMPLETED_THIS_LOOP: 1"
echo "FILES_MODIFIED: 1"
echo "EXIT_SIGNAL: true"
echo "---END_LOOP_STATUS---"
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	result, err := runner.Run(ctx, loop.Options{
		ContainerName:          containerName,
		Project:                project,
		Agent:                  agentName,
		Prompt:                 "test",
		MaxLoops:               1,
		Timeout:                30 * time.Second,
		SessionExpirationHours: 24, // 24h expiration, session is 48h old
		LoopDelaySeconds:       0,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// The old session should have been replaced with a fresh one
	session, err := store.LoadSession(project, agentName)
	require.NoError(t, err)
	require.NotNil(t, session)
	// New session starts with fresh counters
	assert.Equal(t, 1, session.LoopsCompleted, "fresh session should have 1 loop (just ran)")
	assert.True(t, session.StartedAt.After(time.Now().Add(-1*time.Minute)),
		"fresh session should have been created recently, not 48h ago")
}

// TestLoopIntegration_NamingFormat verifies that auto-generated agent names
// follow the expected loop-<adjective>-<noun> format.
func TestLoopIntegration_NamingFormat(t *testing.T) {
	// This doesn't need Docker — it's a pure unit test included here for
	// completeness of the loop integration test suite.
	names := make(map[string]bool)
	for i := 0; i < 100; i++ {
		name := loop.GenerateAgentName()

		// Format: loop-<adjective>-<noun>
		assert.True(t, strings.HasPrefix(name, "loop-"), "name should start with loop-: %s", name)

		parts := strings.SplitN(name, "-", 3)
		assert.Equal(t, 3, len(parts), "name should have 3 parts (loop-adjective-noun): %s", name)

		// Should be unique (with very high probability over 100 names)
		assert.False(t, names[name], "name collision: %s", name)
		names[name] = true
	}
}

// TestLoopIntegration_RateLimiter verifies that the rate limiter state is
// persisted in the session and respected across loop iterations.
func TestLoopIntegration_RateLimiter(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-ratelimit-test"
	agentName := "test-ratelimit-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, store, _ := newTestRunner(t, dockerClient)

	script := `#!/bin/sh
echo "---LOOP_STATUS---"
echo "STATUS: IN_PROGRESS"
echo "TASKS_COMPLETED_THIS_LOOP: 1"
echo "FILES_MODIFIED: 1"
echo "EXIT_SIGNAL: false"
echo "---END_LOOP_STATUS---"
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	result, err := runner.Run(ctx, loop.Options{
		ContainerName:   containerName,
		Project:         project,
		Agent:           agentName,
		Prompt:          "test",
		MaxLoops:        2,
		Timeout:         30 * time.Second,
		CallsPerHour:    100,
		LoopDelaySeconds: 0,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify rate limit state was persisted in session
	session, err := store.LoadSession(project, agentName)
	require.NoError(t, err)
	require.NotNil(t, session)
	require.NotNil(t, session.RateLimitState, "rate limit state should be persisted in session")
	assert.Greater(t, session.RateLimitState.Calls, 0, "rate limit should track calls")
}

// TestLoopIntegration_HistoryTracking verifies that session and circuit history
// entries are recorded during loop execution.
func TestLoopIntegration_HistoryTracking(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-history-test"
	agentName := "test-history-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, _, history := newTestRunner(t, dockerClient)

	script := `#!/bin/sh
echo "---LOOP_STATUS---"
echo "STATUS: IN_PROGRESS"
echo "TASKS_COMPLETED_THIS_LOOP: 1"
echo "FILES_MODIFIED: 1"
echo "EXIT_SIGNAL: false"
echo "---END_LOOP_STATUS---"
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	_, err := runner.Run(ctx, loop.Options{
		ContainerName:   containerName,
		Project:         project,
		Agent:           agentName,
		Prompt:          "test",
		MaxLoops:        2,
		Timeout:         30 * time.Second,
		LoopDelaySeconds: 0,
	})
	require.NoError(t, err)

	// Verify session history was recorded
	sessionHistory, err := history.LoadSessionHistory(project, agentName)
	require.NoError(t, err)
	require.NotNil(t, sessionHistory, "session history should exist")
	assert.Greater(t, len(sessionHistory.Entries), 0, "should have history entries")

	// Should have a "created" entry and "updated" entries
	var hasCreated, hasUpdated bool
	for _, entry := range sessionHistory.Entries {
		if entry.Event == "created" {
			hasCreated = true
		}
		if entry.Event == "updated" {
			hasUpdated = true
		}
	}
	assert.True(t, hasCreated, "history should have 'created' entry")
	assert.True(t, hasUpdated, "history should have 'updated' entries")
}

// TestLoopIntegration_WorkDirPersisted verifies that the working directory is
// stored in the session for concurrency detection.
func TestLoopIntegration_WorkDirPersisted(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-workdir-test"
	agentName := "test-workdir-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, store, _ := newTestRunner(t, dockerClient)

	script := `#!/bin/sh
echo "---LOOP_STATUS---"
echo "STATUS: COMPLETE"
echo "TASKS_COMPLETED_THIS_LOOP: 1"
echo "FILES_MODIFIED: 1"
echo "EXIT_SIGNAL: true"
echo "---END_LOOP_STATUS---"
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	testWorkDir := "/test/work/dir"
	_, err := runner.Run(ctx, loop.Options{
		ContainerName:   containerName,
		Project:         project,
		Agent:           agentName,
		Prompt:          "test",
		MaxLoops:        1,
		Timeout:         30 * time.Second,
		WorkDir:         testWorkDir,
		LoopDelaySeconds: 0,
	})
	require.NoError(t, err)

	session, err := store.LoadSession(project, agentName)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, testWorkDir, session.WorkDir, "session should store the working directory")
}

// TestLoopIntegration_SameErrorTrip verifies the circuit breaker trips when the
// same error signature appears too many times.
func TestLoopIntegration_SameErrorTrip(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-same-error-test"
	agentName := "test-same-error-" + time.Now().Format("150405.000000")
	containerID, containerName, dockerClient, cleanup := createLoopTestContainer(t, ctx, project, agentName)
	defer cleanup()

	runner, _, _ := newTestRunner(t, dockerClient)

	// Script that always exits with error (non-zero exit code)
	script := `#!/bin/sh
echo "Error: connection refused to api.example.com"
exit 1
`
	writeScriptToContainer(t, ctx, dockerClient, containerID, "/usr/local/bin/claude", script)

	result, err := runner.Run(ctx, loop.Options{
		ContainerName:       containerName,
		Project:             project,
		Agent:               agentName,
		Prompt:              "test",
		MaxLoops:            20,
		StagnationThreshold: 10, // High so stagnation doesn't trip first
		SameErrorThreshold:  3,  // Trip after 3 same errors
		Timeout:             30 * time.Second,
		LoopDelaySeconds:    0,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Non-zero exit code doesn't cause loopErr (ExecCapture returns nil error for
	// non-zero exit codes). The output contains "Error: ..." which errorPatternRe
	// matches, extracting a consistent error signature. Without a LOOP_STATUS block,
	// each loop counts as no-progress. The same-error circuit breaker should trip
	// after SameErrorThreshold consecutive identical error signatures.
	assert.LessOrEqual(t, result.LoopsCompleted, 5, "should trip around same-error threshold")
	assert.Contains(t, result.ExitReason, "stagnation", "should exit due to circuit breaker trip")
}

// TestLoopIntegration_ListSessions verifies that sessions can be listed per project.
func TestLoopIntegration_ListSessions(t *testing.T) {
	// Pure unit test — doesn't need Docker
	tempDir := t.TempDir()
	storeDir := filepath.Join(tempDir, "loop")
	store := loop.NewSessionStore(storeDir)

	project := "list-test"

	// Create multiple sessions for the same project
	for i, agent := range []string{"agent-1", "agent-2", "agent-3"} {
		session := loop.NewSession(project, agent, "test prompt", "/test/dir")
		session.LoopsCompleted = i + 1
		require.NoError(t, store.SaveSession(session))
	}

	// Create a session for a different project
	otherSession := loop.NewSession("other-project", "agent-x", "test", "/other")
	require.NoError(t, store.SaveSession(otherSession))

	// List sessions for our project
	sessions, err := store.ListSessions(project)
	require.NoError(t, err)
	assert.Len(t, sessions, 3, "should find 3 sessions for project")

	// Verify all belong to the correct project
	for _, s := range sessions {
		assert.Equal(t, project, s.Project)
	}
}

// TestLoopIntegration_PromptInstructions verifies that BuildSystemPrompt produces
// parseable LOOP_STATUS instructions.
func TestLoopIntegration_PromptInstructions(t *testing.T) {
	// Pure unit test — doesn't need Docker
	prompt := loop.BuildSystemPrompt("")
	assert.Contains(t, prompt, "---LOOP_STATUS---", "system prompt should include LOOP_STATUS marker")
	assert.Contains(t, prompt, "---END_LOOP_STATUS---", "system prompt should include end marker")
	assert.Contains(t, prompt, "EXIT_SIGNAL", "system prompt should document EXIT_SIGNAL field")

	// Verify the example in the prompt is parseable
	status := loop.ParseStatus(prompt)
	require.NotNil(t, status, "the LOOP_STATUS example in the prompt should be parseable")

	// With additional instructions
	additional := "Always use TypeScript for new files."
	promptWithExtra := loop.BuildSystemPrompt(additional)
	assert.Contains(t, promptWithExtra, "---LOOP_STATUS---")
	assert.Contains(t, promptWithExtra, additional)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeScriptToContainer writes a shell script to a path inside the container
// by executing sh -c "cat > path && chmod +x path" via docker exec.
func writeScriptToContainer(t *testing.T, ctx context.Context, client *docker.Client, containerID, path, script string) {
	t.Helper()

	dir := filepath.Dir(path)
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s && chmod +x %s", dir, path, path)

	execResp, err := client.ExecCreate(ctx, containerID, docker.ExecCreateOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"sh", "-c", cmd},
	})
	require.NoError(t, err, "failed to create exec for script injection")

	hijacked, err := client.ExecAttach(ctx, execResp.ID, docker.ExecAttachOptions{TTY: false})
	require.NoError(t, err, "failed to attach for script injection")

	_, err = hijacked.Conn.Write([]byte(script))
	require.NoError(t, err, "failed to write script content")
	hijacked.CloseWrite()
	hijacked.Close()

	// Wait for exec to complete
	time.Sleep(500 * time.Millisecond)

	// Verify the script was written
	inspectCtx, inspectCancel := context.WithTimeout(ctx, 5*time.Second)
	defer inspectCancel()
	inspectResp, err := client.ExecInspect(inspectCtx, execResp.ID, docker.ExecInspectOptions{})
	require.NoError(t, err, "failed to inspect exec")
	require.Equal(t, 0, inspectResp.ExitCode, "script injection should succeed (exit code 0)")
}

// injectLoopHooksForTest is a test-accessible wrapper around the shared
// InjectLoopHooks function. It imports the shared package indirectly to avoid
// a dependency cycle — tests in test/agents/ can import internal packages.
func injectLoopHooksForTest(ctx context.Context, containerID, hooksFile string, copyFn func(ctx context.Context, containerID, destPath string, content io.Reader) error) error {
	hooks, hookFiles, err := loop.ResolveHooks(hooksFile)
	if err != nil {
		return err
	}

	settingsJSON, err := hooks.MarshalSettingsJSON()
	if err != nil {
		return fmt.Errorf("marshaling hook settings: %w", err)
	}

	settingsTar, err := buildTestSettingsTar(settingsJSON)
	if err != nil {
		return err
	}

	if err := copyFn(ctx, containerID, "/home/claude/.claude", settingsTar); err != nil {
		return fmt.Errorf("injecting settings.json: %w", err)
	}

	if len(hookFiles) > 0 {
		scriptsTar, err := buildTestHookFilesTar(hookFiles)
		if err != nil {
			return err
		}
		if err := copyFn(ctx, containerID, "/", scriptsTar); err != nil {
			return fmt.Errorf("injecting hook scripts: %w", err)
		}
	}

	return nil
}

// newCopyFn creates a CopyToContainerFn wrapping a docker.Client.
func newCopyFn(client *docker.Client) func(ctx context.Context, containerID, destPath string, content io.Reader) error {
	return func(ctx context.Context, containerID, destPath string, content io.Reader) error {
		_, err := client.CopyToContainer(ctx, containerID, docker.CopyToContainerOptions{
			DestinationPath: destPath,
			Content:         content,
		})
		return err
	}
}

// buildTestSettingsTar creates a tar archive containing settings.json.
func buildTestSettingsTar(content []byte) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	hdr := &tar.Header{
		Name: "settings.json",
		Mode: 0o644,
		Size: int64(len(content)),
		Uid:  1001,
		Gid:  1001,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	if _, err := tw.Write(content); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}

// buildTestHookFilesTar creates a tar archive containing hook scripts at
// their absolute container paths.
func buildTestHookFilesTar(files map[string][]byte) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	dirs := make(map[string]bool)
	for path, content := range files {
		dir := filepath.Dir(path)
		if dir != "/" && dir != "." && !dirs[dir] {
			if err := tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     dir[1:] + "/",
				Mode:     0o755,
			}); err != nil {
				return nil, err
			}
			dirs[dir] = true
		}

		if err := tw.WriteHeader(&tar.Header{
			Name: path[1:],
			Mode: 0o755,
			Size: int64(len(content)),
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(content); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}

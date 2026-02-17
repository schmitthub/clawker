package agents

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	loopshared "github.com/schmitthub/clawker/internal/cmd/loop/shared"
	"github.com/schmitthub/clawker/internal/config/configtest"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/schmitthub/clawker/test/harness/builders"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// NDJSON stream-json helpers
// ---------------------------------------------------------------------------

// loopStatusBlock builds assistant text containing a LOOP_STATUS block.
func loopStatusBlock(status string, exitSignal bool, tasksCompleted, filesModified int) string {
	signal := "false"
	if exitSignal {
		signal = "true"
	}
	return fmt.Sprintf(`Here is my work summary.

---LOOP_STATUS---
STATUS: %s
TASKS_COMPLETED_THIS_LOOP: %d
FILES_MODIFIED: %d
COMPLETION_INDICATORS: 0
TESTS_STATUS: NOT_RUN
WORK_TYPE: IMPLEMENTATION
RECOMMENDATION: continue
EXIT_SIGNAL: %s
---END_LOOP_STATUS---
`, status, tasksCompleted, filesModified, signal)
}

// ndjsonOutput builds NDJSON stream-json output (stream_event + assistant + result).
func ndjsonOutput(text string) string {
	streamEvent, _ := json.Marshal(map[string]interface{}{
		"type":       "stream_event",
		"session_id": "test-session",
		"event": map[string]interface{}{
			"type": "content_block_delta",
			"delta": map[string]interface{}{
				"type": "text_delta",
				"text": "token",
			},
		},
	})
	assistant, _ := json.Marshal(map[string]interface{}{
		"type":       "assistant",
		"session_id": "test-session",
		"message": map[string]interface{}{
			"id":          "msg-1",
			"role":        "assistant",
			"model":       "test-model",
			"stop_reason": "end_turn",
			"content": []map[string]interface{}{
				{"type": "text", "text": text},
			},
		},
	})
	result, _ := json.Marshal(map[string]interface{}{
		"type":            "result",
		"subtype":         "success",
		"session_id":      "test-session",
		"is_error":        false,
		"duration_ms":     1000,
		"duration_api_ms": 900,
		"num_turns":       1,
		"total_cost_usd":  0.01,
	})
	return string(streamEvent) + "\n" + string(assistant) + "\n" + string(result) + "\n"
}

// ---------------------------------------------------------------------------
// Container creation helpers for per-iteration Runner
// ---------------------------------------------------------------------------

// containerCmd returns a shell command that outputs the given NDJSON to stdout.
// Uses base64 encoding to avoid shell escaping issues.
func containerCmd(ndjson string) []string {
	encoded := base64.StdEncoding.EncodeToString([]byte(ndjson))
	return []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d", encoded)}
}

// makeCreateContainer returns a CreateContainer callback that creates Alpine
// containers outputting the given NDJSON each iteration.
func makeCreateContainer(t *testing.T, dockerClient *docker.Client, project, agent, output string) func(context.Context) (*loopshared.ContainerStartConfig, error) {
	t.Helper()
	iteration := 0
	return func(ctx context.Context) (*loopshared.ContainerStartConfig, error) {
		iteration++
		name := fmt.Sprintf("loop-iter-%s-%d-%d", agent, iteration, time.Now().UnixNano())
		resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
			Name: name,
			Config: &container.Config{
				Image: "alpine:latest",
				Cmd:   containerCmd(output),
				Labels: map[string]string{
					docker.LabelProject: project,
					docker.LabelAgent:   agent,
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("creating test container: %w", err)
		}
		containerID := resp.ID
		return &loopshared.ContainerStartConfig{
			ContainerID: containerID,
			Cleanup: func() {
				_ = dockerClient.RemoveContainerWithVolumes(context.Background(), containerID, true)
			},
		}, nil
	}
}

// makeMultiCreateContainer returns a CreateContainer callback that uses
// different NDJSON output for each iteration (cycling back to the last if
// there are more iterations than outputs).
func makeMultiCreateContainer(t *testing.T, dockerClient *docker.Client, project, agent string, outputs []string) func(context.Context) (*loopshared.ContainerStartConfig, error) {
	t.Helper()
	iteration := 0
	return func(ctx context.Context) (*loopshared.ContainerStartConfig, error) {
		idx := iteration
		if idx >= len(outputs) {
			idx = len(outputs) - 1
		}
		iteration++
		name := fmt.Sprintf("loop-iter-%s-%d-%d", agent, iteration, time.Now().UnixNano())
		resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
			Name: name,
			Config: &container.Config{
				Image: "alpine:latest",
				Cmd:   containerCmd(outputs[idx]),
				Labels: map[string]string{
					docker.LabelProject: project,
					docker.LabelAgent:   agent,
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("creating test container: %w", err)
		}
		containerID := resp.ID
		return &loopshared.ContainerStartConfig{
			ContainerID: containerID,
			Cleanup: func() {
				_ = dockerClient.RemoveContainerWithVolumes(context.Background(), containerID, true)
			},
		}, nil
	}
}

// makeSlowCreateContainer returns a CreateContainer callback that creates a
// container which sleeps for the given duration before exiting. Used to test
// context cancellation during container execution.
func makeSlowCreateContainer(t *testing.T, dockerClient *docker.Client, project, agent string, sleepSeconds int) func(context.Context) (*loopshared.ContainerStartConfig, error) {
	t.Helper()
	iteration := 0
	return func(ctx context.Context) (*loopshared.ContainerStartConfig, error) {
		iteration++
		name := fmt.Sprintf("loop-iter-%s-%d-%d", agent, iteration, time.Now().UnixNano())
		resp, err := dockerClient.ContainerCreate(ctx, whail.ContainerCreateOptions{
			Name: name,
			Config: &container.Config{
				Image: "alpine:latest",
				Cmd:   []string{"sh", "-c", fmt.Sprintf("sleep %d", sleepSeconds)},
				Labels: map[string]string{
					docker.LabelProject: project,
					docker.LabelAgent:   agent,
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("creating test container: %w", err)
		}
		containerID := resp.ID
		return &loopshared.ContainerStartConfig{
			ContainerID: containerID,
			Cleanup: func() {
				_ = dockerClient.RemoveContainerWithVolumes(context.Background(), containerID, true)
			},
		}, nil
	}
}

// newTestRunner creates a Runner with temp-dir stores suitable for testing.
func newTestRunner(t *testing.T, dockerClient *docker.Client) (*loopshared.Runner, *loopshared.SessionStore, *loopshared.HistoryStore) {
	t.Helper()
	tmpDir := t.TempDir()
	storeDir := filepath.Join(tmpDir, "loop")
	store := loopshared.NewSessionStore(storeDir)
	history := loopshared.NewHistoryStore(storeDir)
	return loopshared.NewRunnerWith(dockerClient, store, history), store, history
}

// projectCleanup registers a t.Cleanup that removes all resources for a project.
func projectCleanup(t *testing.T, dockerClient *docker.Client, project string) {
	t.Helper()
	t.Cleanup(func() {
		if err := harness.CleanupProjectResources(context.Background(), dockerClient, project); err != nil {
			t.Logf("WARNING: cleanup failed for %s: %v", project, err)
		}
	})
}

// testProject creates a *config.Project for tests.
func testProject(name string) *configtest.ProjectBuilder {
	return configtest.NewProjectBuilder().WithProject(name)
}

// ---------------------------------------------------------------------------
// Runner integration tests
// ---------------------------------------------------------------------------

// TestLoopIntegration_RunSingleIteration verifies that Runner.Run executes
// a single loop iteration with per-iteration container creation, produces a
// result, and persists the session.
func TestLoopIntegration_RunSingleIteration(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-single-test"
	agent := "test-single-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	text := loopStatusBlock("COMPLETE", true, 1, 2)
	output := ndjsonOutput(text)
	runner, store, _ := newTestRunner(t, dockerClient)

	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "test prompt",
		MaxLoops:            1,
		Timeout:             60 * time.Second,
		LoopDelaySeconds:    0,
		StagnationThreshold: 10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.LoopsCompleted, "should complete exactly 1 loop")
	assert.Contains(t, result.ExitReason, "completion")

	// Verify session was persisted
	session, err := store.LoadSession(project, agent)
	require.NoError(t, err)
	require.NotNil(t, session, "session should be persisted")
	assert.Equal(t, project, session.Project)
	assert.Equal(t, agent, session.Agent)
	assert.Equal(t, 1, session.LoopsCompleted)
}

// TestLoopIntegration_SessionPersistenceAcrossIterations verifies that session
// data accumulates correctly across multiple per-iteration containers.
func TestLoopIntegration_SessionPersistenceAcrossIterations(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-persist-test"
	agent := "test-persist-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	// IN_PROGRESS with progress each loop — no exit signal
	text := loopStatusBlock("IN_PROGRESS", false, 1, 2)
	output := ndjsonOutput(text)
	runner, store, history := newTestRunner(t, dockerClient)

	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "test prompt",
		MaxLoops:            3,
		Timeout:             60 * time.Second,
		LoopDelaySeconds:    1,
		StagnationThreshold: 10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 3, result.LoopsCompleted, "should complete 3 loops")
	assert.Equal(t, "max loops reached", result.ExitReason)

	// Verify session state accumulated
	session, err := store.LoadSession(project, agent)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, 3, session.LoopsCompleted)
	assert.Equal(t, 3, session.TotalTasksCompleted, "tasks should accumulate across loops")
	assert.Equal(t, 6, session.TotalFilesModified, "files should accumulate across loops")
	assert.Equal(t, 0, session.NoProgressCount, "should have progress each loop")

	// Verify history has entries
	sessionHistory, err := history.LoadSessionHistory(project, agent)
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
	agent := "test-stagnation-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	// No progress: 0 tasks, 0 files
	text := loopStatusBlock("IN_PROGRESS", false, 0, 0)
	output := ndjsonOutput(text)
	runner, store, _ := newTestRunner(t, dockerClient)

	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "test prompt",
		MaxLoops:            20,
		StagnationThreshold: 3,
		Timeout:             60 * time.Second,
		LoopDelaySeconds:    1,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Less(t, result.LoopsCompleted, 20, "should trip before max loops")
	assert.Contains(t, result.ExitReason, "stagnation", "exit reason should mention stagnation")
	assert.Error(t, result.Error, "should have an error when circuit trips")

	// Verify circuit state was persisted as tripped
	circuitState, err := store.LoadCircuitState(project, agent)
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
	agent := "test-blocked-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, store, _ := newTestRunner(t, dockerClient)

	// Pre-trip the circuit
	now := time.Now()
	err := store.SaveCircuitState(&loopshared.CircuitState{
		Project:    project,
		Agent:      agent,
		Tripped:    true,
		TripReason: "test: manually tripped",
		TrippedAt:  &now,
	})
	require.NoError(t, err)

	// Attempt to run — should fail immediately (no container needed)
	text := loopStatusBlock("COMPLETE", true, 1, 1)
	output := ndjsonOutput(text)
	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer: makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:      testProject(project).Build(),
		Agent:           agent,
		Prompt:          "test",
		MaxLoops:        1,
		Timeout:         10 * time.Second,
	})
	require.NoError(t, err)
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
	agent := "test-reset-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, store, _ := newTestRunner(t, dockerClient)

	// Pre-trip the circuit
	now := time.Now()
	err := store.SaveCircuitState(&loopshared.CircuitState{
		Project:    project,
		Agent:      agent,
		Tripped:    true,
		TripReason: "test: pre-tripped",
		TrippedAt:  &now,
	})
	require.NoError(t, err)

	// Completion output so the loop exits cleanly after reset
	text := loopStatusBlock("COMPLETE", true, 1, 1)
	output := ndjsonOutput(text)

	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "test",
		MaxLoops:            1,
		Timeout:             60 * time.Second,
		ResetCircuit:        true,
		LoopDelaySeconds:    0,
		StagnationThreshold: 10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.LoopsCompleted, "should run after circuit reset")

	// Circuit state should no longer be tripped
	circuitState, err := store.LoadCircuitState(project, agent)
	require.NoError(t, err)
	if circuitState != nil {
		assert.False(t, circuitState.Tripped, "circuit should not be tripped after reset")
	}
}

// TestLoopIntegration_CompletionDetection verifies that the loop exits when the
// agent signals completion via EXIT_SIGNAL: true in the LOOP_STATUS block.
func TestLoopIntegration_CompletionDetection(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-complete-test"
	agent := "test-complete-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, _, _ := newTestRunner(t, dockerClient)

	// First iteration: IN_PROGRESS. Second: COMPLETE with exit signal.
	outputs := []string{
		ndjsonOutput(loopStatusBlock("IN_PROGRESS", false, 1, 1)),
		ndjsonOutput(loopStatusBlock("COMPLETE", true, 2, 3)),
	}

	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeMultiCreateContainer(t, dockerClient, project, agent, outputs),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "complete the task",
		MaxLoops:            10,
		Timeout:             60 * time.Second,
		LoopDelaySeconds:    1,
		StagnationThreshold: 10,
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
// parsing from real Docker container output through the stream-json pipeline.
func TestLoopIntegration_LOOPSTATUSParsing(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-status-parse-test"
	agent := "test-parse-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, _, _ := newTestRunner(t, dockerClient)

	// Rich LOOP_STATUS with all fields
	richText := `Some preceding output from the agent...
Working on task: fix authentication

---LOOP_STATUS---
STATUS: IN_PROGRESS
TASKS_COMPLETED_THIS_LOOP: 3
FILES_MODIFIED: 7
TESTS_STATUS: FAILING
WORK_TYPE: TESTING
RECOMMENDATION: Need to fix auth test mocks
EXIT_SIGNAL: false
---END_LOOP_STATUS---
`
	output := ndjsonOutput(richText)

	var capturedStatus *loopshared.Status
	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "fix tests",
		MaxLoops:            1,
		Timeout:             60 * time.Second,
		LoopDelaySeconds:    0,
		StagnationThreshold: 10,
		OnLoopEnd: func(_ int, status *loopshared.Status, _ *loopshared.ResultEvent, _ error) {
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
	agent := "test-nostatus-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, store, _ := newTestRunner(t, dockerClient)

	// No LOOP_STATUS in the output
	output := ndjsonOutput("I did some work but forgot the status block")

	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "test",
		MaxLoops:            10,
		StagnationThreshold: 3,
		Timeout:             60 * time.Second,
		LoopDelaySeconds:    1,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.LessOrEqual(t, result.LoopsCompleted, 4, "should trip around stagnation threshold")
	assert.Contains(t, result.ExitReason, "stagnation")

	// Session should show no-progress count
	session, err := store.LoadSession(project, agent)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Greater(t, session.NoProgressCount, 0, "session should track no-progress count")
}

// TestLoopIntegration_ContextCancellation verifies that Runner.Run exits
// promptly when the context is cancelled mid-loop.
func TestLoopIntegration_ContextCancellation(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-cancel-test"
	agent := "test-cancel-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, _, _ := newTestRunner(t, dockerClient)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a brief delay
	go func() {
		time.Sleep(3 * time.Second)
		cancel()
	}()

	start := time.Now()
	result, err := runner.Run(ctx, loopshared.Options{
		// Slow container: sleeps 300s so context cancellation is tested mid-exec
		CreateContainer:     makeSlowCreateContainer(t, dockerClient, project, agent, 300),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "test",
		MaxLoops:            5,
		Timeout:             120 * time.Second,
		StagnationThreshold: 100,
		LoopDelaySeconds:    0,
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
	agent := "test-output-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, _, _ := newTestRunner(t, dockerClient)

	text := loopStatusBlock("COMPLETE", true, 1, 1)
	output := ndjsonOutput(text)

	var outputReceived bool
	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "test",
		MaxLoops:            1,
		Timeout:             60 * time.Second,
		LoopDelaySeconds:    0,
		StagnationThreshold: 10,
		OnOutput: func(chunk []byte) {
			if len(chunk) > 0 {
				outputReceived = true
			}
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, outputReceived, "OnOutput should receive stream_event data")
}

// TestLoopIntegration_SessionExpiration verifies that expired sessions are
// cleaned up and a fresh session is started.
func TestLoopIntegration_SessionExpiration(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-expire-test"
	agent := "test-expire-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, store, _ := newTestRunner(t, dockerClient)

	// Pre-create an expired session (started 48 hours ago)
	oldSession := &loopshared.Session{
		Project:        project,
		Agent:          agent,
		StartedAt:      time.Now().Add(-48 * time.Hour),
		UpdatedAt:      time.Now().Add(-48 * time.Hour),
		LoopsCompleted: 10,
		Status:         "IN_PROGRESS",
	}
	require.NoError(t, store.SaveSession(oldSession))

	text := loopStatusBlock("COMPLETE", true, 1, 1)
	output := ndjsonOutput(text)

	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:        makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:             testProject(project).Build(),
		Agent:                  agent,
		Prompt:                 "test",
		MaxLoops:               1,
		Timeout:                60 * time.Second,
		SessionExpirationHours: 24, // 24h expiration, session is 48h old
		LoopDelaySeconds:       0,
		StagnationThreshold:    10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// The old session should have been replaced with a fresh one
	session, err := store.LoadSession(project, agent)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, 1, session.LoopsCompleted, "fresh session should have 1 loop (just ran)")
	assert.True(t, session.StartedAt.After(time.Now().Add(-1*time.Minute)),
		"fresh session should have been created recently, not 48h ago")
}

// TestLoopIntegration_RateLimiter verifies that the rate limiter state is
// persisted in the session and respected across loop iterations.
func TestLoopIntegration_RateLimiter(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-ratelimit-test"
	agent := "test-ratelimit-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, store, _ := newTestRunner(t, dockerClient)

	text := loopStatusBlock("IN_PROGRESS", false, 1, 1)
	output := ndjsonOutput(text)

	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "test",
		MaxLoops:            2,
		Timeout:             60 * time.Second,
		CallsPerHour:        100,
		LoopDelaySeconds:    1,
		StagnationThreshold: 10,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify rate limit state was persisted in session
	session, err := store.LoadSession(project, agent)
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
	agent := "test-history-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, _, history := newTestRunner(t, dockerClient)

	text := loopStatusBlock("IN_PROGRESS", false, 1, 1)
	output := ndjsonOutput(text)

	_, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "test",
		MaxLoops:            2,
		Timeout:             60 * time.Second,
		LoopDelaySeconds:    1,
		StagnationThreshold: 10,
	})
	require.NoError(t, err)

	// Verify session history was recorded
	sessionHistory, err := history.LoadSessionHistory(project, agent)
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
	agent := "test-workdir-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, store, _ := newTestRunner(t, dockerClient)

	text := loopStatusBlock("COMPLETE", true, 1, 1)
	output := ndjsonOutput(text)

	testWorkDir := "/test/work/dir"
	_, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "test",
		MaxLoops:            1,
		Timeout:             60 * time.Second,
		WorkDir:             testWorkDir,
		LoopDelaySeconds:    0,
		StagnationThreshold: 10,
	})
	require.NoError(t, err)

	session, err := store.LoadSession(project, agent)
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
	agent := "test-same-error-" + time.Now().Format("150405.000000")
	dockerClient := harness.NewTestClient(t)
	projectCleanup(t, dockerClient, project)

	runner, _, _ := newTestRunner(t, dockerClient)

	// Output with an error pattern in the assistant text but no LOOP_STATUS
	errorText := "Error: connection refused to api.example.com\nFailed to complete the task."
	output := ndjsonOutput(errorText)

	result, err := runner.Run(ctx, loopshared.Options{
		CreateContainer:     makeCreateContainer(t, dockerClient, project, agent, output),
		ProjectCfg:          testProject(project).Build(),
		Agent:               agent,
		Prompt:              "test",
		MaxLoops:            20,
		StagnationThreshold: 10,
		SameErrorThreshold:  3,
		Timeout:             60 * time.Second,
		LoopDelaySeconds:    1,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.LessOrEqual(t, result.LoopsCompleted, 5, "should trip around same-error threshold")
	assert.Contains(t, result.ExitReason, "stagnation", "should exit due to circuit breaker trip")
}

// ---------------------------------------------------------------------------
// Hook injection tests
// ---------------------------------------------------------------------------

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
	projectCleanup(t, dockerClient, project)

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
	scriptResult, err := ctr.Exec(ctx, dockerClient, "cat", loopshared.StopCheckScriptPath)
	require.NoError(t, err, "stop-check script should exist")
	assert.Equal(t, 0, scriptResult.ExitCode, "cat stop-check.js should succeed")
	assert.Contains(t, scriptResult.CleanOutput(), "LOOP_STATUS", "stop-check script should reference LOOP_STATUS")

	// Verify the hook script directory exists
	dirResult, err := ctr.Exec(ctx, dockerClient, "ls", loopshared.HookScriptDir)
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
	projectCleanup(t, dockerClient, project)

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
	scriptResult, err := ctr.Exec(ctx, dockerClient, "ls", loopshared.StopCheckScriptPath)
	require.NoError(t, err)
	assert.NotEqual(t, 0, scriptResult.ExitCode, "stop-check.js should NOT exist with custom hooks")
}

// ---------------------------------------------------------------------------
// Pure unit tests (no Docker)
// ---------------------------------------------------------------------------

// TestLoopIntegration_NamingFormat verifies that auto-generated agent names
// follow the expected loop-<adjective>-<noun> format.
func TestLoopIntegration_NamingFormat(t *testing.T) {
	names := make(map[string]bool)
	for i := 0; i < 100; i++ {
		name := loopshared.GenerateAgentName()

		assert.True(t, strings.HasPrefix(name, "loop-"), "name should start with loop-: %s", name)

		parts := strings.SplitN(name, "-", 3)
		assert.Equal(t, 3, len(parts), "name should have 3 parts (loop-adjective-noun): %s", name)

		assert.False(t, names[name], "name collision: %s", name)
		names[name] = true
	}
}

// TestLoopIntegration_ListSessions verifies that sessions can be listed per project.
func TestLoopIntegration_ListSessions(t *testing.T) {
	tempDir := t.TempDir()
	storeDir := filepath.Join(tempDir, "loop")
	store := loopshared.NewSessionStore(storeDir)

	project := "list-test"

	for i, agent := range []string{"agent-1", "agent-2", "agent-3"} {
		session := loopshared.NewSession(project, agent, "test prompt", "/test/dir")
		session.LoopsCompleted = i + 1
		require.NoError(t, store.SaveSession(session))
	}

	// Create a session for a different project
	otherSession := loopshared.NewSession("other-project", "agent-x", "test", "/other")
	require.NoError(t, store.SaveSession(otherSession))

	// List sessions for our project
	sessions, err := store.ListSessions(project)
	require.NoError(t, err)
	assert.Len(t, sessions, 3, "should find 3 sessions for project")

	for _, s := range sessions {
		assert.Equal(t, project, s.Project)
	}
}

// TestLoopIntegration_PromptInstructions verifies that BuildSystemPrompt produces
// parseable LOOP_STATUS instructions.
func TestLoopIntegration_PromptInstructions(t *testing.T) {
	prompt := loopshared.BuildSystemPrompt("")
	assert.Contains(t, prompt, "---LOOP_STATUS---", "system prompt should include LOOP_STATUS marker")
	assert.Contains(t, prompt, "---END_LOOP_STATUS---", "system prompt should include end marker")
	assert.Contains(t, prompt, "EXIT_SIGNAL", "system prompt should document EXIT_SIGNAL field")

	// Verify the example in the prompt is parseable
	status := loopshared.ParseStatus(prompt)
	require.NotNil(t, status, "the LOOP_STATUS example in the prompt should be parseable")

	// With additional instructions
	additional := "Always use TypeScript for new files."
	promptWithExtra := loopshared.BuildSystemPrompt(additional)
	assert.Contains(t, promptWithExtra, "---LOOP_STATUS---")
	assert.Contains(t, promptWithExtra, additional)
}

// ---------------------------------------------------------------------------
// Hook test helpers
// ---------------------------------------------------------------------------

// injectLoopHooksForTest is a test-accessible wrapper around the shared
// InjectLoopHooks function.
func injectLoopHooksForTest(ctx context.Context, containerID, hooksFile string, copyFn func(ctx context.Context, containerID, destPath string, content io.Reader) error) error {
	hooks, hookFiles, err := loopshared.ResolveHooks(hooksFile)
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

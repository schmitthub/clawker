package shared_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/loop"
)

// loopStatusOutput builds a fake Claude output with a LOOP_STATUS block.
func loopStatusOutput(status string, exitSignal bool, tasksCompleted, filesModified int) string {
	signal := "false"
	if exitSignal {
		signal = "true"
	}
	return fmt.Sprintf(`Here is my work summary.

---LOOP_STATUS---
STATUS: %s
TASKS_COMPLETED: %d
FILES_MODIFIED: %d
COMPLETION_INDICATORS: 0
TESTS_STATUS: NOT_RUN
WORK_TYPE: IMPLEMENTATION
RECOMMENDATION: continue
EXIT_SIGNAL: %s
---END_LOOP_STATUS---
`, status, tasksCompleted, filesModified, signal)
}

// setupExecFakes wires the minimal Docker API fakes needed for Runner.ExecCapture:
// FindContainerByName (ContainerList), ExecCreate (ContainerInspect + ExecCreate),
// ExecAttach (with stdcopy-framed output), ExecInspect.
func setupExecFakes(fake *dockertest.FakeClient, containerName, output string, exitCode int) {
	fake.SetupFindContainer(containerName, dockertest.RunningContainerFixture("testproj", "testagent"))
	fake.SetupExecCreate("exec-123")
	fake.SetupExecAttachWithOutput(output)
	fake.SetupExecInspect(exitCode)
}

// newTestRunner creates a Runner with temp stores and returns it along with
// the store and history for assertions.
func newTestRunner(t *testing.T, client *dockertest.FakeClient) (*loop.Runner, *loop.SessionStore, *loop.HistoryStore) {
	t.Helper()
	tmpDir := t.TempDir()
	store := loop.NewSessionStore(filepath.Join(tmpDir, "sessions"))
	history := loop.NewHistoryStore(filepath.Join(tmpDir, "history"))
	runner := loop.NewRunnerWith(client.Client, store, history)
	return runner, store, history
}

func TestRunnerRun_SingleLoopCompletion(t *testing.T) {
	fake := dockertest.NewFakeClient()
	containerName := "clawker.testproj.testagent"
	output := loopStatusOutput("COMPLETE", true, 5, 3)
	setupExecFakes(fake, containerName, output, 0)

	runner, store, _ := newTestRunner(t, fake)

	result, err := runner.Run(context.Background(), loop.Options{
		ContainerName: containerName,
		ProjectCfg:    "testproj",
		Agent:         "testagent",
		Prompt:        "implement the feature",
		MaxLoops:      10,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.LoopsCompleted)
	assert.Equal(t, "agent signaled completion", result.ExitReason)
	assert.Nil(t, result.Error)
	require.NotNil(t, result.FinalStatus)
	assert.True(t, result.FinalStatus.ExitSignal)

	// Session should be persisted with updated state
	session, loadErr := store.LoadSession("testproj", "testagent")
	require.NoError(t, loadErr)
	require.NotNil(t, session)
	assert.Equal(t, 1, session.LoopsCompleted)
}

func TestRunnerRun_MaxLoopsReached(t *testing.T) {
	fake := dockertest.NewFakeClient()
	containerName := "clawker.testproj.testagent"
	// Output that always says IN_PROGRESS with some progress (no exit signal)
	output := loopStatusOutput("IN_PROGRESS", false, 1, 1)
	setupExecFakes(fake, containerName, output, 0)

	runner, _, _ := newTestRunner(t, fake)

	result, err := runner.Run(context.Background(), loop.Options{
		ContainerName:       containerName,
		ProjectCfg:          "testproj",
		Agent:               "testagent",
		Prompt:              "do some work",
		MaxLoops:            2,
		StagnationThreshold: 10, // High so we don't trip circuit
		LoopDelaySeconds:    1,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2, result.LoopsCompleted)
	assert.Equal(t, "max loops reached", result.ExitReason)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "reached maximum loops")
}

func TestRunnerRun_CircuitBreakerTrips(t *testing.T) {
	fake := dockertest.NewFakeClient()
	containerName := "clawker.testproj.testagent"
	// Output with no progress (0 tasks, 0 files, no exit signal) — triggers stagnation
	output := loopStatusOutput("IN_PROGRESS", false, 0, 0)
	setupExecFakes(fake, containerName, output, 0)

	runner, _, _ := newTestRunner(t, fake)

	result, err := runner.Run(context.Background(), loop.Options{
		ContainerName:       containerName,
		ProjectCfg:          "testproj",
		Agent:               "testagent",
		Prompt:              "do some work",
		MaxLoops:            10,
		StagnationThreshold: 2, // Trip after 2 loops without progress
		LoopDelaySeconds:    1,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.LessOrEqual(t, result.LoopsCompleted, 3, "should trip before many loops")
	assert.Contains(t, result.ExitReason, "stagnation")
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "circuit breaker tripped")
}

func TestRunnerRun_ContextCancellation(t *testing.T) {
	fake := dockertest.NewFakeClient()
	containerName := "clawker.testproj.testagent"
	output := loopStatusOutput("IN_PROGRESS", false, 1, 1)
	setupExecFakes(fake, containerName, output, 0)

	runner, _, _ := newTestRunner(t, fake)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after first OnLoopEnd callback
	var loopsRan int
	result, err := runner.Run(ctx, loop.Options{
		ContainerName:       containerName,
		ProjectCfg:          "testproj",
		Agent:               "testagent",
		Prompt:              "do some work",
		MaxLoops:            100,
		StagnationThreshold: 100,
		LoopDelaySeconds:    1,
		OnLoopEnd: func(_ int, _ *loop.Status, _ error) {
			loopsRan++
			if loopsRan >= 1 {
				cancel()
			}
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "context cancelled", result.ExitReason)
	require.Error(t, result.Error)
	assert.ErrorIs(t, result.Error, context.Canceled)
}

func TestRunnerRun_CallbacksFired(t *testing.T) {
	fake := dockertest.NewFakeClient()
	containerName := "clawker.testproj.testagent"
	output := loopStatusOutput("COMPLETE", true, 2, 1)
	setupExecFakes(fake, containerName, output, 0)

	runner, _, _ := newTestRunner(t, fake)

	var startLoops, endLoops []int
	var outputReceived bool

	result, err := runner.Run(context.Background(), loop.Options{
		ContainerName: containerName,
		ProjectCfg:    "testproj",
		Agent:         "testagent",
		Prompt:        "do it",
		MaxLoops:      5,
		OnLoopStart: func(loopNum int) {
			startLoops = append(startLoops, loopNum)
		},
		OnLoopEnd: func(loopNum int, _ *loop.Status, _ error) {
			endLoops = append(endLoops, loopNum)
		},
		OnOutput: func(chunk []byte) {
			if len(chunk) > 0 {
				outputReceived = true
			}
		},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []int{1}, startLoops, "OnLoopStart should fire once")
	assert.Equal(t, []int{1}, endLoops, "OnLoopEnd should fire once")
	assert.True(t, outputReceived, "OnOutput should receive data")
}

func TestRunnerRun_PreCancelledContext(t *testing.T) {
	fake := dockertest.NewFakeClient()
	containerName := "clawker.testproj.testagent"
	// No exec fakes needed — context is pre-cancelled so Run exits immediately
	runner, _, _ := newTestRunner(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Pre-cancel

	result, err := runner.Run(ctx, loop.Options{
		ContainerName: containerName,
		ProjectCfg:    "testproj",
		Agent:         "testagent",
		Prompt:        "do it",
		MaxLoops:      5,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "context cancelled", result.ExitReason)
}

func TestRunnerRun_RepeatedErrorHistoryEntry(t *testing.T) {
	fake := dockertest.NewFakeClient()
	containerName := "clawker.testproj.testagent"

	// Output with an error signature that will repeat every loop
	output := `Error: compilation failed
` + loopStatusOutput("IN_PROGRESS", false, 1, 1)
	setupExecFakes(fake, containerName, output, 0)

	runner, _, history := newTestRunner(t, fake)

	// Run enough loops for same-error count to reach 3 (threshold for repeated_error event).
	// Same-error threshold is 5 by default so the circuit won't trip yet.
	result, err := runner.Run(context.Background(), loop.Options{
		ContainerName:       containerName,
		ProjectCfg:          "testproj",
		Agent:               "testagent",
		Prompt:              "do work",
		MaxLoops:            4,
		StagnationThreshold: 100, // High to avoid stagnation trip
		LoopDelaySeconds:    1,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.GreaterOrEqual(t, result.LoopsCompleted, 3)

	// Check history for a repeated_error entry
	hist, histErr := history.LoadSessionHistory("testproj", "testagent")
	require.NoError(t, histErr)

	var foundRepeatedError bool
	for _, entry := range hist.Entries {
		if entry.Event == "repeated_error" {
			foundRepeatedError = true
			assert.NotEmpty(t, entry.Error, "repeated_error entry should include the error signature")
			break
		}
	}
	assert.True(t, foundRepeatedError, "should find a repeated_error history entry after 3+ same-error loops")
}

func TestRunnerRun_CircuitAlreadyTripped(t *testing.T) {
	fake := dockertest.NewFakeClient()
	containerName := "clawker.testproj.testagent"
	runner, store, _ := newTestRunner(t, fake)

	// Pre-trip the circuit
	now := time.Now()
	err := store.SaveCircuitState(&loop.CircuitState{
		Project:    "testproj",
		Agent:      "testagent",
		Tripped:    true,
		TripReason: "previous stagnation",
		TrippedAt:  &now,
	})
	require.NoError(t, err)

	result, runErr := runner.Run(context.Background(), loop.Options{
		ContainerName: containerName,
		ProjectCfg:    "testproj",
		Agent:         "testagent",
		Prompt:        "do it",
		MaxLoops:      5,
	})

	require.NoError(t, runErr)
	require.NotNil(t, result)
	assert.Contains(t, result.ExitReason, "circuit already tripped")
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "previous stagnation")
	assert.Equal(t, 0, result.LoopsCompleted, "no loops should run when circuit is tripped")
}

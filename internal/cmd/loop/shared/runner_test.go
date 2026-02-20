package shared_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmd/loop/shared"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/logger/loggertest"
)

// loopStatusText builds the assistant text containing a LOOP_STATUS block.
func loopStatusText(status string, exitSignal bool, tasksCompleted, filesModified int) string {
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

// streamJSONLines builds NDJSON stream-json output: a stream_event delta,
// an assistant event with the given text, and a result event.
func streamJSONLines(text string) string {
	// Stream event (text delta for OnOutput callback)
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

	// Assistant event with the full text
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

	// Result event
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

	return shared.ReadyLogPrefix + "\n" + string(streamEvent) + "\n" + string(assistant) + "\n" + string(result) + "\n"
}

// setupContainerFakes wires the Docker API fakes needed for Runner.StartContainer:
// ContainerAttach (with stdcopy-framed NDJSON output), ContainerStart, ContainerWait.
func setupContainerFakes(fake *dockertest.FakeClient, output string, exitCode int64) {
	fake.SetupContainerAttachWithOutput(output)
	fake.SetupContainerStart()
	fake.SetupContainerWait(exitCode)
}

// newTestRunner creates a Runner with temp stores and returns it along with
// the store and history for assertions.
func newTestRunner(t *testing.T, client *dockertest.FakeClient) (*shared.Runner, *shared.SessionStore, *shared.HistoryStore) {
	t.Helper()
	tmpDir := t.TempDir()
	store := shared.NewSessionStore(filepath.Join(tmpDir, "sessions"))
	history := shared.NewHistoryStore(filepath.Join(tmpDir, "history"))
	runner := shared.NewRunnerWith(client.Client, store, history)
	return runner, store, history
}

// makeCreateContainer returns a CreateContainer callback that always returns
// the given container ID with a no-op cleanup function.
func makeCreateContainer(containerID string) func(context.Context) (*shared.ContainerStartConfig, error) {
	return func(_ context.Context) (*shared.ContainerStartConfig, error) {
		return &shared.ContainerStartConfig{
			ContainerID: containerID,
			Cleanup:     func() {},
		}, nil
	}
}

func TestRunnerRun_SingleLoopCompletion(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	text := loopStatusText("COMPLETE", true, 5, 3)
	output := streamJSONLines(text)
	setupContainerFakes(fake, output, 0)

	runner, store, _ := newTestRunner(t, fake)

	result, err := runner.Run(context.Background(), shared.Options{
		CreateContainer: makeCreateContainer("container-123"),
		ProjectCfg:      &config.Project{Project: "testproj"},
		Agent:           "testagent",
		Prompt:          "implement the feature",
		MaxLoops:        10,
		Logger:          loggertest.NewNop(),
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	// Output that always says IN_PROGRESS with some progress (no exit signal)
	text := loopStatusText("IN_PROGRESS", false, 1, 1)
	output := streamJSONLines(text)
	setupContainerFakes(fake, output, 0)

	runner, _, _ := newTestRunner(t, fake)

	result, err := runner.Run(context.Background(), shared.Options{
		CreateContainer:     makeCreateContainer("container-123"),
		ProjectCfg:          &config.Project{Project: "testproj"},
		Agent:               "testagent",
		Prompt:              "do some work",
		MaxLoops:            2,
		StagnationThreshold: 10, // High so we don't trip circuit
		LoopDelaySeconds:    1,
		Logger:              loggertest.NewNop(),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2, result.LoopsCompleted)
	assert.Equal(t, "max loops reached", result.ExitReason)
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "reached maximum loops")
}

func TestRunnerRun_CircuitBreakerTrips(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	// Output with no progress (0 tasks, 0 files, no exit signal) — triggers stagnation
	text := loopStatusText("IN_PROGRESS", false, 0, 0)
	output := streamJSONLines(text)
	setupContainerFakes(fake, output, 0)

	runner, _, _ := newTestRunner(t, fake)

	result, err := runner.Run(context.Background(), shared.Options{
		CreateContainer:     makeCreateContainer("container-123"),
		ProjectCfg:          &config.Project{Project: "testproj"},
		Agent:               "testagent",
		Prompt:              "do some work",
		MaxLoops:            10,
		StagnationThreshold: 2, // Trip after 2 loops without progress
		LoopDelaySeconds:    1,
		Logger:              loggertest.NewNop(),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.LessOrEqual(t, result.LoopsCompleted, 3, "should trip before many loops")
	assert.Contains(t, result.ExitReason, "stagnation")
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "circuit breaker tripped")
}

func TestRunnerRun_ContextCancellation(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	text := loopStatusText("IN_PROGRESS", false, 1, 1)
	output := streamJSONLines(text)
	setupContainerFakes(fake, output, 0)

	runner, _, _ := newTestRunner(t, fake)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after first OnLoopEnd callback
	var loopsRan int
	result, err := runner.Run(ctx, shared.Options{
		CreateContainer:     makeCreateContainer("container-123"),
		ProjectCfg:          &config.Project{Project: "testproj"},
		Agent:               "testagent",
		Prompt:              "do some work",
		MaxLoops:            100,
		StagnationThreshold: 100,
		LoopDelaySeconds:    1,
		Logger:              loggertest.NewNop(),
		OnLoopEnd: func(_ int, _ *shared.Status, _ *shared.ResultEvent, _ error) {
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	text := loopStatusText("COMPLETE", true, 2, 1)
	output := streamJSONLines(text)
	setupContainerFakes(fake, output, 0)

	runner, _, _ := newTestRunner(t, fake)

	var startLoops, endLoops []int
	var outputReceived bool

	result, err := runner.Run(context.Background(), shared.Options{
		CreateContainer: makeCreateContainer("container-123"),
		ProjectCfg:      &config.Project{Project: "testproj"},
		Agent:           "testagent",
		Prompt:          "do it",
		MaxLoops:        5,
		Logger:          loggertest.NewNop(),
		OnLoopStart: func(loopNum int) {
			startLoops = append(startLoops, loopNum)
		},
		OnLoopEnd: func(loopNum int, _ *shared.Status, _ *shared.ResultEvent, _ error) {
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
	assert.True(t, outputReceived, "OnOutput should receive stream_event data")
}

func TestRunnerRun_PreCancelledContext(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	// No container fakes needed — context is pre-cancelled so Run exits immediately
	runner, _, _ := newTestRunner(t, fake)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Pre-cancel

	result, err := runner.Run(ctx, shared.Options{
		CreateContainer: makeCreateContainer("container-123"),
		ProjectCfg:      &config.Project{Project: "testproj"},
		Agent:           "testagent",
		Prompt:          "do it",
		MaxLoops:        5,
		Logger:          loggertest.NewNop(),
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "context cancelled", result.ExitReason)
}

func TestRunnerRun_RepeatedErrorHistoryEntry(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())

	// Output with an error signature that will repeat every loop
	text := "Error: compilation failed\n" + loopStatusText("IN_PROGRESS", false, 1, 1)
	output := streamJSONLines(text)
	setupContainerFakes(fake, output, 0)

	runner, _, history := newTestRunner(t, fake)

	// Run enough loops for same-error count to reach 3 (threshold for repeated_error event).
	// Same-error threshold is 5 by default so the circuit won't trip yet.
	result, err := runner.Run(context.Background(), shared.Options{
		CreateContainer:     makeCreateContainer("container-123"),
		ProjectCfg:          &config.Project{Project: "testproj"},
		Agent:               "testagent",
		Prompt:              "do work",
		MaxLoops:            4,
		StagnationThreshold: 100, // High to avoid stagnation trip
		LoopDelaySeconds:    1,
		Logger:              loggertest.NewNop(),
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
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	runner, store, _ := newTestRunner(t, fake)

	// Pre-trip the circuit
	now := time.Now()
	err := store.SaveCircuitState(&shared.CircuitState{
		Project:    "testproj",
		Agent:      "testagent",
		Tripped:    true,
		TripReason: "previous stagnation",
		TrippedAt:  &now,
	})
	require.NoError(t, err)

	result, runErr := runner.Run(context.Background(), shared.Options{
		CreateContainer: makeCreateContainer("container-123"),
		ProjectCfg:      &config.Project{Project: "testproj"},
		Agent:           "testagent",
		Prompt:          "do it",
		MaxLoops:        5,
		Logger:          loggertest.NewNop(),
	})

	require.NoError(t, runErr)
	require.NotNil(t, result)
	assert.Contains(t, result.ExitReason, "circuit already tripped")
	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "previous stagnation")
	assert.Equal(t, 0, result.LoopsCompleted, "no loops should run when circuit is tripped")
}

package whailtest_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordedBuildEvent_Delay(t *testing.T) {
	e := whailtest.RecordedBuildEvent{DelayMs: 150}
	assert.Equal(t, 150*time.Millisecond, e.Delay())
}

func TestRecordedBuildEvent_Delay_Zero(t *testing.T) {
	e := whailtest.RecordedBuildEvent{DelayMs: 0}
	assert.Equal(t, time.Duration(0), e.Delay())
}

func TestRecordedBuildScenario_FlatEvents(t *testing.T) {
	scenario := &whailtest.RecordedBuildScenario{
		Name: "test",
		Events: []whailtest.RecordedBuildEvent{
			{DelayMs: 10, Event: whail.BuildProgressEvent{StepID: "a", StepName: "step A"}},
			{DelayMs: 20, Event: whail.BuildProgressEvent{StepID: "b", StepName: "step B"}},
		},
	}

	flat := scenario.FlatEvents()
	require.Len(t, flat, 2)
	assert.Equal(t, "a", flat[0].StepID)
	assert.Equal(t, "b", flat[1].StepID)
}

func TestRecordedBuildScenario_FlatEvents_Empty(t *testing.T) {
	scenario := &whailtest.RecordedBuildScenario{Name: "empty"}
	flat := scenario.FlatEvents()
	assert.Empty(t, flat)
}

func TestRecordedScenarioFromEvents(t *testing.T) {
	events := []whail.BuildProgressEvent{
		{StepID: "1", StepName: "step 1", Status: whail.BuildStepRunning},
		{StepID: "1", StepName: "step 1", Status: whail.BuildStepComplete},
	}

	scenario := whailtest.RecordedScenarioFromEvents("test", "desc", events, 50*time.Millisecond)

	assert.Equal(t, "test", scenario.Name)
	assert.Equal(t, "desc", scenario.Description)
	require.Len(t, scenario.Events, 2)
	assert.Equal(t, int64(50), scenario.Events[0].DelayMs)
	assert.Equal(t, int64(50), scenario.Events[1].DelayMs)
	assert.Equal(t, "1", scenario.Events[0].Event.StepID)
}

func TestRecordedScenarioFromEventsWithTiming(t *testing.T) {
	events := []whail.BuildProgressEvent{
		{StepID: "1", StepName: "[internal] load Dockerfile", Status: whail.BuildStepComplete},
		{StepID: "2", StepName: "[stage-0 1/1] RUN echo hello", Status: whail.BuildStepRunning},
		{StepID: "2", StepName: "[stage-0 1/1] RUN echo hello", LogLine: "hello"},
		{StepID: "2", StepName: "[stage-0 1/1] RUN echo hello", Status: whail.BuildStepComplete},
	}

	scenario := whailtest.RecordedScenarioFromEventsWithTiming(
		"test", "desc", events,
		10*time.Millisecond,  // internal
		50*time.Millisecond,  // running
		20*time.Millisecond,  // log
		100*time.Millisecond, // complete
	)

	require.Len(t, scenario.Events, 4)
	assert.Equal(t, int64(10), scenario.Events[0].DelayMs)  // internal
	assert.Equal(t, int64(50), scenario.Events[1].DelayMs)  // running
	assert.Equal(t, int64(20), scenario.Events[2].DelayMs)  // log
	assert.Equal(t, int64(100), scenario.Events[3].DelayMs) // complete
}

func TestSaveAndLoadRecordedScenario_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")

	original := &whailtest.RecordedBuildScenario{
		Name:        "round-trip",
		Description: "Tests JSON round-trip",
		Events: []whailtest.RecordedBuildEvent{
			{
				DelayMs: 100,
				Event: whail.BuildProgressEvent{
					StepID:     "sha256:abc",
					StepName:   "[stage-0 1/2] FROM node:20",
					StepIndex:  0,
					TotalSteps: 2,
					Status:     whail.BuildStepRunning,
				},
			},
			{
				DelayMs: 50,
				Event: whail.BuildProgressEvent{
					StepID:     "sha256:abc",
					StepName:   "[stage-0 1/2] FROM node:20",
					Status:     whail.BuildStepComplete,
					StepIndex:  0,
					TotalSteps: 2,
				},
			},
			{
				DelayMs: 10,
				Event: whail.BuildProgressEvent{
					StepID:   "sha256:def",
					StepName: "[stage-0 2/2] RUN npm install",
					Status:   whail.BuildStepError,
					Error:    "exit code 1",
					LogLine:  "npm ERR!",
				},
			},
		},
	}

	err := whailtest.SaveRecordedScenario(path, original)
	require.NoError(t, err)

	loaded, err := whailtest.LoadRecordedScenario(path)
	require.NoError(t, err)

	assert.Equal(t, original.Name, loaded.Name)
	assert.Equal(t, original.Description, loaded.Description)
	require.Len(t, loaded.Events, len(original.Events))

	for i, orig := range original.Events {
		got := loaded.Events[i]
		assert.Equal(t, orig.DelayMs, got.DelayMs, "event %d delay", i)
		assert.Equal(t, orig.Event.StepID, got.Event.StepID, "event %d StepID", i)
		assert.Equal(t, orig.Event.StepName, got.Event.StepName, "event %d StepName", i)
		assert.Equal(t, orig.Event.Status, got.Event.Status, "event %d Status", i)
		assert.Equal(t, orig.Event.LogLine, got.Event.LogLine, "event %d LogLine", i)
		assert.Equal(t, orig.Event.Error, got.Event.Error, "event %d Error", i)
		assert.Equal(t, orig.Event.Cached, got.Event.Cached, "event %d Cached", i)
	}
}

func TestSaveRecordedScenario_CreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "scenario.json")

	scenario := &whailtest.RecordedBuildScenario{Name: "nested"}
	err := whailtest.SaveRecordedScenario(path, scenario)
	require.NoError(t, err)

	_, err = os.Stat(path)
	require.NoError(t, err)
}

func TestLoadRecordedScenario_NotFound(t *testing.T) {
	_, err := whailtest.LoadRecordedScenario("/nonexistent/path.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read recorded scenario")
}

func TestLoadRecordedScenario_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("{invalid"), 0644))

	_, err := whailtest.LoadRecordedScenario(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse recorded scenario")
}

// ---------------------------------------------------------------------------
// EventRecorder tests (Part 2)
// ---------------------------------------------------------------------------

func TestEventRecorder_CapturesEvents(t *testing.T) {
	recorder := whailtest.NewEventRecorder("test", "captures events", nil)
	onProgress := recorder.OnProgress()

	onProgress(whail.BuildProgressEvent{StepID: "1", StepName: "step 1", Status: whail.BuildStepRunning})
	onProgress(whail.BuildProgressEvent{StepID: "1", StepName: "step 1", Status: whail.BuildStepComplete})

	scenario := recorder.Scenario()
	assert.Equal(t, "test", scenario.Name)
	assert.Equal(t, "captures events", scenario.Description)
	require.Len(t, scenario.Events, 2)
	assert.Equal(t, whail.BuildStepRunning, scenario.Events[0].Event.Status)
	assert.Equal(t, whail.BuildStepComplete, scenario.Events[1].Event.Status)
}

func TestEventRecorder_FirstEventZeroDelay(t *testing.T) {
	recorder := whailtest.NewEventRecorder("test", "", nil)
	onProgress := recorder.OnProgress()

	onProgress(whail.BuildProgressEvent{StepID: "1"})

	scenario := recorder.Scenario()
	require.Len(t, scenario.Events, 1)
	assert.Equal(t, int64(0), scenario.Events[0].DelayMs,
		"first event should have zero delay")
}

func TestEventRecorder_NonNegativeDelays(t *testing.T) {
	recorder := whailtest.NewEventRecorder("test", "", nil)
	onProgress := recorder.OnProgress()

	for i := range 10 {
		onProgress(whail.BuildProgressEvent{StepID: whailtest.StepDigest(i)})
	}

	scenario := recorder.Scenario()
	for i, e := range scenario.Events {
		assert.GreaterOrEqual(t, e.DelayMs, int64(0),
			"event %d should have non-negative delay", i)
	}
}

func TestEventRecorder_ForwardsToInner(t *testing.T) {
	var received []whail.BuildProgressEvent
	inner := func(event whail.BuildProgressEvent) {
		received = append(received, event)
	}

	recorder := whailtest.NewEventRecorder("test", "", inner)
	onProgress := recorder.OnProgress()

	onProgress(whail.BuildProgressEvent{StepID: "a"})
	onProgress(whail.BuildProgressEvent{StepID: "b"})

	require.Len(t, received, 2)
	assert.Equal(t, "a", received[0].StepID)
	assert.Equal(t, "b", received[1].StepID)
}

func TestEventRecorder_NilInner(t *testing.T) {
	recorder := whailtest.NewEventRecorder("test", "", nil)
	onProgress := recorder.OnProgress()

	// Should not panic with nil inner.
	onProgress(whail.BuildProgressEvent{StepID: "1"})

	scenario := recorder.Scenario()
	require.Len(t, scenario.Events, 1)
}

func TestEventRecorder_ScenarioCopiesEvents(t *testing.T) {
	recorder := whailtest.NewEventRecorder("test", "", nil)
	onProgress := recorder.OnProgress()

	onProgress(whail.BuildProgressEvent{StepID: "1"})

	s1 := recorder.Scenario()
	onProgress(whail.BuildProgressEvent{StepID: "2"})
	s2 := recorder.Scenario()

	assert.Len(t, s1.Events, 1, "first snapshot should not be mutated")
	assert.Len(t, s2.Events, 2, "second snapshot should include new event")
}

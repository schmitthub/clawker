package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventCollector collects BuildProgressEvents in a thread-safe manner.
type eventCollector struct {
	mu     sync.Mutex
	events []whail.BuildProgressEvent
}

func (c *eventCollector) collect(event whail.BuildProgressEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
}

func (c *eventCollector) all() []whail.BuildProgressEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]whail.BuildProgressEvent{}, c.events...)
}

func buildLegacyStream(events ...buildEvent) []byte {
	var buf bytes.Buffer
	for _, e := range events {
		data, _ := json.Marshal(e)
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

func TestProcessBuildOutputWithProgress_StepParsing(t *testing.T) {
	stream := buildLegacyStream(
		buildEvent{Stream: "Step 1/3 : FROM node:20-slim\n"},
		buildEvent{Stream: " ---> abc123\n"},
		buildEvent{Stream: "Step 2/3 : RUN apt-get update\n"},
		buildEvent{Stream: " ---> Running in def456\n"},
		buildEvent{Stream: "reading package lists...\n"},
		buildEvent{Stream: "Step 3/3 : COPY . /app\n"},
		buildEvent{Stream: " ---> ghi789\n"},
	)

	collector := &eventCollector{}
	client := &Client{Engine: clawkerEngine(whailtest.NewFakeAPIClient())}
	err := client.processBuildOutputWithProgress(bytes.NewReader(stream), collector.collect)
	require.NoError(t, err)

	events := collector.all()

	// Find step status events (non-log events)
	var steps []whail.BuildProgressEvent
	for _, e := range events {
		if e.LogLine == "" {
			steps = append(steps, e)
		}
	}

	// Should have: running(0), complete(0), running(1), complete(1), running(2), complete(2)
	require.GreaterOrEqual(t, len(steps), 6, "expected at least 6 step events, got %d", len(steps))

	// First step starts running
	assert.Equal(t, "step-0", steps[0].StepID)
	assert.Equal(t, whail.BuildStepRunning, steps[0].Status)
	assert.Equal(t, "FROM node:20-slim", steps[0].StepName)
	assert.Equal(t, 0, steps[0].StepIndex)
	assert.Equal(t, 3, steps[0].TotalSteps)
}

func TestProcessBuildOutputWithProgress_CacheHit(t *testing.T) {
	stream := buildLegacyStream(
		buildEvent{Stream: "Step 1/2 : FROM node:20-slim\n"},
		buildEvent{Stream: " ---> Using cache\n"},
		buildEvent{Stream: "Step 2/2 : RUN echo hello\n"},
		buildEvent{Stream: "hello\n"},
	)

	collector := &eventCollector{}
	client := &Client{Engine: clawkerEngine(whailtest.NewFakeAPIClient())}
	err := client.processBuildOutputWithProgress(bytes.NewReader(stream), collector.collect)
	require.NoError(t, err)

	events := collector.all()

	// Find the cached event
	var cached bool
	for _, e := range events {
		if e.Status == whail.BuildStepCached {
			cached = true
			assert.True(t, e.Cached)
			break
		}
	}
	assert.True(t, cached, "expected at least one cached step event")
}

func TestProcessBuildOutputWithProgress_Error(t *testing.T) {
	stream := buildLegacyStream(
		buildEvent{Stream: "Step 1/2 : FROM node:20-slim\n"},
		buildEvent{Stream: " ---> abc123\n"},
		buildEvent{Stream: "Step 2/2 : RUN exit 1\n"},
		buildEvent{Error: "The command '/bin/sh -c exit 1' returned a non-zero code: 1"},
	)

	collector := &eventCollector{}
	client := &Client{Engine: clawkerEngine(whailtest.NewFakeAPIClient())}
	err := client.processBuildOutputWithProgress(bytes.NewReader(stream), collector.collect)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit 1")

	events := collector.all()

	// Should have an error event
	var errEvent *whail.BuildProgressEvent
	for i, e := range events {
		if e.Status == whail.BuildStepError {
			errEvent = &events[i]
			break
		}
	}
	require.NotNil(t, errEvent, "expected an error step event")
	assert.Contains(t, errEvent.Error, "exit 1")
}

func TestProcessBuildOutputWithProgress_LogLines(t *testing.T) {
	stream := buildLegacyStream(
		buildEvent{Stream: "Step 1/1 : RUN echo hello && echo world\n"},
		buildEvent{Stream: "hello\n"},
		buildEvent{Stream: "world\n"},
	)

	collector := &eventCollector{}
	client := &Client{Engine: clawkerEngine(whailtest.NewFakeAPIClient())}
	err := client.processBuildOutputWithProgress(bytes.NewReader(stream), collector.collect)
	require.NoError(t, err)

	events := collector.all()

	var logs []string
	for _, e := range events {
		if e.LogLine != "" {
			logs = append(logs, e.LogLine)
		}
	}
	assert.Contains(t, logs, "hello")
	assert.Contains(t, logs, "world")
}

func TestBuildImage_OnProgressThreadedToBuildKit(t *testing.T) {
	fake := whailtest.NewFakeAPIClient()
	engine := clawkerEngine(fake)
	client := &Client{Engine: engine}

	capture := &whailtest.BuildKitCapture{}
	engine.BuildKitImageBuilder = whailtest.FakeBuildKitBuilder(capture)

	var called bool
	progressFn := func(event whail.BuildProgressEvent) {
		called = true
	}

	ctx := context.Background()
	err := client.BuildImage(ctx, bytes.NewReader(nil), BuildImageOpts{
		Tags:            []string{"test:latest"},
		BuildKitEnabled: true,
		ContextDir:      "/tmp/build",
		SuppressOutput:  true,
		OnProgress:      progressFn,
	})
	require.NoError(t, err)

	// Verify OnProgress was forwarded to BuildKit options
	assert.NotNil(t, capture.Opts.OnProgress, "expected OnProgress to be forwarded to BuildKit")
	_ = called // The fake builder doesn't call OnProgress, so called stays false.
}

func TestProcessBuildOutputWithProgress_MultiStep(t *testing.T) {
	// Verify a multi-step build produces correct step indices and completion
	stream := buildLegacyStream(
		buildEvent{Stream: "Step 1/3 : FROM alpine\n"},
		buildEvent{Stream: " ---> abc123\n"},
		buildEvent{Stream: "Step 2/3 : RUN echo hello\n"},
		buildEvent{Stream: "hello\n"},
		buildEvent{Stream: "Step 3/3 : CMD echo done\n"},
	)

	collector := &eventCollector{}
	client := &Client{Engine: clawkerEngine(whailtest.NewFakeAPIClient())}
	err := client.processBuildOutputWithProgress(bytes.NewReader(stream), collector.collect)
	require.NoError(t, err)

	events := collector.all()
	require.NotEmpty(t, events)

	// Verify all three step IDs appear
	stepIDs := make(map[string]bool)
	for _, e := range events {
		if e.StepID != "" {
			stepIDs[e.StepID] = true
		}
	}
	assert.True(t, stepIDs["step-0"], "expected step-0")
	assert.True(t, stepIDs["step-1"], "expected step-1")
	assert.True(t, stepIDs["step-2"], "expected step-2")

	// Last event should be complete for the final step
	lastEvent := events[len(events)-1]
	assert.Equal(t, whail.BuildStepComplete, lastEvent.Status)
	assert.Equal(t, "step-2", lastEvent.StepID)
}

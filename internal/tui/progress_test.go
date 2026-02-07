package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Ring buffer tests
// ---------------------------------------------------------------------------

func TestRingBuffer_Basic(t *testing.T) {
	rb := newRingBuffer(3)

	assert.Nil(t, rb.Lines())
	assert.Equal(t, 0, rb.Count())

	rb.Push("line1")
	rb.Push("line2")
	assert.Equal(t, []string{"line1", "line2"}, rb.Lines())
	assert.Equal(t, 2, rb.Count())

	rb.Push("line3")
	assert.Equal(t, []string{"line1", "line2", "line3"}, rb.Lines())
	assert.Equal(t, 3, rb.Count())
}

func TestRingBuffer_Overflow(t *testing.T) {
	rb := newRingBuffer(3)

	rb.Push("a")
	rb.Push("b")
	rb.Push("c")
	rb.Push("d") // evicts "a"

	lines := rb.Lines()
	assert.Equal(t, []string{"b", "c", "d"}, lines)
	assert.Equal(t, 4, rb.Count())

	rb.Push("e") // evicts "b"
	rb.Push("f") // evicts "c"
	lines = rb.Lines()
	assert.Equal(t, []string{"d", "e", "f"}, lines)
	assert.Equal(t, 6, rb.Count())
}

func TestRingBuffer_SingleCapacity(t *testing.T) {
	rb := newRingBuffer(1)

	rb.Push("first")
	assert.Equal(t, []string{"first"}, rb.Lines())

	rb.Push("second")
	assert.Equal(t, []string{"second"}, rb.Lines())
	assert.Equal(t, 2, rb.Count())
}

// ---------------------------------------------------------------------------
// Visible steps sliding window tests
// ---------------------------------------------------------------------------

func TestVisibleSteps_FewSteps(t *testing.T) {
	steps := []*progressStep{
		{name: "step 1", status: StepComplete},
		{name: "step 2", status: StepRunning},
	}

	visible, hidden := visibleProgressSteps(steps, defaultMaxVisible, nil)
	assert.Len(t, visible, 2)
	assert.Equal(t, 0, hidden)
}

func TestVisibleSteps_SlidingWindow(t *testing.T) {
	steps := make([]*progressStep, 8)
	for i := range 6 {
		steps[i] = &progressStep{name: "completed step", status: StepComplete}
	}
	steps[6] = &progressStep{name: "running step", status: StepRunning}
	steps[7] = &progressStep{name: "pending step", status: StepPending}

	visible, hidden := visibleProgressSteps(steps, defaultMaxVisible, nil)
	assert.Len(t, visible, defaultMaxVisible)
	assert.Equal(t, 3, hidden) // 3 completed steps hidden (steps 0, 1, 2)
}

func TestVisibleSteps_InternalStepsExcluded(t *testing.T) {
	isInternal := func(name string) bool { return strings.HasPrefix(name, "[internal]") }
	steps := []*progressStep{
		{name: "[internal] load build definition", status: StepComplete},
		{name: "[internal] load .dockerignore", status: StepComplete},
		{name: "[stage-2 1/7] FROM node:20", status: StepComplete},
		{name: "[stage-2 2/7] RUN apt-get", status: StepRunning},
	}

	visible, hidden := visibleProgressSteps(steps, defaultMaxVisible, isInternal)
	assert.Len(t, visible, 2) // only non-internal steps
	assert.Equal(t, 0, hidden)
	assert.Equal(t, "[stage-2 1/7] FROM node:20", visible[0].name)
}

// ---------------------------------------------------------------------------
// Plain mode tests (channel-based)
// ---------------------------------------------------------------------------

func sendProgressSteps(ch chan<- ProgressStep, steps ...ProgressStep) {
	for _, s := range steps {
		ch <- s
	}
	close(ch) // channel closure = done signal
}

// testDisplayConfig returns a ProgressDisplayConfig wired with build-like callbacks for tests.
func testDisplayConfig() ProgressDisplayConfig {
	return ProgressDisplayConfig{
		Title:    "Building myproject",
		Subtitle: "myproject:latest",
		IsInternal: func(name string) bool {
			return strings.HasPrefix(name, "[internal]")
		},
	}
}

func TestPlainMode_Header(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch)

	cfg := testDisplayConfig()
	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "Building myproject")
	assert.Contains(t, output, "myproject:latest")
}

func TestPlainMode_StepTransitions(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepRunning},
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "[run]")
	assert.Contains(t, output, "[ok]")
}

func TestPlainMode_CachedStep(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepCached, Cached: true},
	)

	cfg := testDisplayConfig()
	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "cached")
}

func TestPlainMode_ErrorStep(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "RUN exit 1", Status: StepError, Error: "exit code 1"},
	)

	cfg := testDisplayConfig()
	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "[fail]")
	assert.Contains(t, output, "exit code 1")
}

func TestPlainMode_InternalStepsHidden(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "[internal] load build definition", Status: StepComplete},
		ProgressStep{ID: "s2", Name: "[stage-2 1/7] FROM node:20", Status: StepRunning},
	)

	cfg := testDisplayConfig()
	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)

	output := tio.ErrBuf.String()
	assert.NotContains(t, output, "[internal]")
	assert.Contains(t, output, "FROM node:20")
}

func TestPlainMode_NoDuplicateRunLines(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "RUN npm install", Status: StepRunning},
		// BuildKit sends repeated Running events for the same step.
		ProgressStep{ID: "s1", Name: "RUN npm install", Status: StepRunning},
		ProgressStep{ID: "s1", Name: "RUN npm install", Status: StepRunning},
		ProgressStep{ID: "s1", Name: "RUN npm install", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)

	output := tio.ErrBuf.String()
	// Should have exactly one [run] line (not three).
	assert.Equal(t, 1, strings.Count(output, "[run]"))
}

func TestPlainMode_PendingNotRendered(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "COPY . /workspace", Status: StepPending},
	)

	cfg := testDisplayConfig()
	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)

	output := tio.ErrBuf.String()
	assert.NotContains(t, output, "COPY")
	assert.NotContains(t, output, "[run]")
}

func TestPlainMode_Summary_Success(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "Built myproject:latest")
}

func TestPlainMode_Summary_Cached(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepCached, Cached: true},
		ProgressStep{ID: "s2", Name: "RUN npm install", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "1/2 cached")
}

func TestPlainMode_LogEventsIgnored(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "RUN npm install", Status: StepRunning},
		ProgressStep{ID: "s1", LogLine: "added 512 packages"},
		ProgressStep{ID: "s1", Name: "RUN npm install", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)

	output := tio.ErrBuf.String()
	assert.NotContains(t, output, "512 packages")
}

// ---------------------------------------------------------------------------
// TTY model unit tests (no BubbleTea program)
// ---------------------------------------------------------------------------

func newTestProgressModel(t *testing.T) (progressModel, *iostreams.TestIOStreams) {
	t.Helper()
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10) // channel not used in direct model tests
	cfg := ProgressDisplayConfig{
		Title:    "Building myproject",
		Subtitle: "myproject:latest",
		ParseGroup: func(name string) string {
			if !strings.HasPrefix(name, "[") {
				return ""
			}
			end := strings.Index(name, "]")
			if end < 0 {
				return ""
			}
			inner := name[1:end]
			if sp := strings.IndexByte(inner, ' '); sp > 0 {
				return inner[:sp]
			}
			return inner
		},
		IsInternal: func(name string) bool {
			return strings.HasPrefix(name, "[internal]")
		},
	}
	m := newProgressModel(tio.IOStreams, cfg, ch)
	return m, tio
}

func TestProgressModel_ProcessEvent_NewStep(t *testing.T) {
	m, _ := newTestProgressModel(t)

	m.processEvent(ProgressStep{
		ID:     "s1",
		Name:   "FROM node:20",
		Status: StepRunning,
	})

	assert.Len(t, m.steps, 1)
	assert.Equal(t, "FROM node:20", m.steps[0].name)
	assert.Equal(t, StepRunning, m.steps[0].status)
}

func TestProgressModel_ProcessEvent_UpdateStep(t *testing.T) {
	m, _ := newTestProgressModel(t)

	m.processEvent(ProgressStep{ID: "s1", Name: "RUN npm install", Status: StepRunning})
	m.processEvent(ProgressStep{ID: "s1", Status: StepComplete})

	assert.Len(t, m.steps, 1)
	assert.Equal(t, StepComplete, m.steps[0].status)
	assert.False(t, m.steps[0].endTime.IsZero())
}

func TestProgressModel_ProcessEvent_LogLine(t *testing.T) {
	m, _ := newTestProgressModel(t)

	m.processEvent(ProgressStep{ID: "s1", Name: "RUN npm install", Status: StepRunning})
	m.processEvent(ProgressStep{ID: "s1", LogLine: "added 512 packages"})
	m.processEvent(ProgressStep{ID: "s1", LogLine: "done in 2.1s"})

	assert.Equal(t, 2, m.logBuf.Count())
	lines := m.logBuf.Lines()
	assert.Equal(t, "added 512 packages", lines[0])
	assert.Equal(t, "done in 2.1s", lines[1])
}

func TestProgressModel_ProcessEvent_GroupDetection(t *testing.T) {
	m, _ := newTestProgressModel(t)

	m.processEvent(ProgressStep{ID: "s1", Name: "[stage-2 1/3] FROM node:20", Status: StepRunning})
	m.processEvent(ProgressStep{ID: "s2", Name: "[builder 1/2] FROM golang:1.21", Status: StepRunning})

	assert.Equal(t, "stage-2", m.steps[0].group)
	assert.Equal(t, "builder", m.steps[1].group)
}

func TestProgressModel_View_Header(t *testing.T) {
	m, _ := newTestProgressModel(t)
	m.width = 60

	output := m.View()
	assert.Contains(t, output, "Building myproject")
	assert.Contains(t, output, "myproject:latest")
}

func TestProgressModel_View_Steps(t *testing.T) {
	m, _ := newTestProgressModel(t)
	m.width = 80

	m.processEvent(ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepComplete})
	m.steps[0].endTime = m.steps[0].startTime.Add(200 * time.Millisecond)
	m.processEvent(ProgressStep{ID: "s2", Name: "RUN npm install", Status: StepRunning})
	m.processEvent(ProgressStep{ID: "s3", Name: "COPY . /workspace", Status: StepPending})

	output := m.View()
	assert.Contains(t, output, "FROM node:20")
	assert.Contains(t, output, "RUN npm install")
	assert.Contains(t, output, "COPY . /workspace")
}

func TestProgressModel_View_Viewport(t *testing.T) {
	m, _ := newTestProgressModel(t)
	m.width = 60

	m.processEvent(ProgressStep{ID: "s1", Name: "RUN npm install", Status: StepRunning})
	m.processEvent(ProgressStep{ID: "s1", LogLine: "npm warn deprecated rimraf@3.0.2"})
	m.processEvent(ProgressStep{ID: "s1", LogLine: "added 512 packages in 2.5s"})

	output := m.View()
	assert.Contains(t, output, "rimraf@3.0.2")
	assert.Contains(t, output, "512 packages")
	// Viewport borders
	assert.Contains(t, output, "┌")
	assert.Contains(t, output, "└")
}

func TestProgressModel_View_SlidingWindow(t *testing.T) {
	m, _ := newTestProgressModel(t)
	m.width = 80

	// Add more steps than defaultMaxVisible.
	for i := range defaultMaxVisible + 3 {
		id := "s" + string(rune('A'+i))
		name := "step " + string(rune('A'+i))
		m.processEvent(ProgressStep{ID: id, Name: name, Status: StepComplete})
		m.steps[i].endTime = m.steps[i].startTime.Add(100 * time.Millisecond)
	}

	output := m.View()
	// Should show collapsed count for hidden steps.
	assert.Contains(t, output, "steps completed")
	// Last steps should be visible.
	lastStep := m.steps[len(m.steps)-1]
	assert.Contains(t, output, lastStep.name)
}

func TestProgressModel_View_GroupHeadings(t *testing.T) {
	m, _ := newTestProgressModel(t)
	m.width = 80

	m.processEvent(ProgressStep{ID: "s1", Name: "[stage-2 1/3] FROM node:20", Status: StepComplete})
	m.steps[0].endTime = m.steps[0].startTime
	m.processEvent(ProgressStep{ID: "s2", Name: "[builder 1/2] FROM golang:1.21", Status: StepRunning})

	output := m.View()
	// Both group names should appear as headings.
	assert.Contains(t, output, "stage-2")
	assert.Contains(t, output, "builder")
}

func TestProgressModel_View_Finished(t *testing.T) {
	m, _ := newTestProgressModel(t)

	// Add some completed steps before finishing.
	m.processEvent(ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepComplete})
	m.steps[0].startTime = time.Now().Add(-2 * time.Second)
	m.steps[0].endTime = time.Now()
	m.width = 80
	m.finished = true

	output := m.View()
	// viewFinished renders a static snapshot — not empty.
	assert.NotEmpty(t, output)
	assert.Contains(t, output, "Building myproject")
	assert.Contains(t, output, "✓")
	assert.Contains(t, output, "FROM node:20")
	// Viewport persists in finished view.
	assert.Contains(t, output, "┌")
	assert.Contains(t, output, "└")
}

func TestProgressModel_View_InternalStepsHidden(t *testing.T) {
	m, _ := newTestProgressModel(t)
	m.width = 80

	m.processEvent(ProgressStep{ID: "s1", Name: "[internal] load build definition", Status: StepComplete})
	m.processEvent(ProgressStep{ID: "s2", Name: "[stage-2 1/3] FROM node:20", Status: StepRunning})

	output := m.View()
	assert.NotContains(t, output, "[internal]")
	assert.Contains(t, output, "FROM node:20")
}

// ---------------------------------------------------------------------------
// Summary rendering tests
// ---------------------------------------------------------------------------

func TestRenderProgressSummary_Success(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	cfg := testDisplayConfig()
	steps := []*progressStep{
		{name: "FROM node:20", status: StepComplete},
		{name: "RUN npm install", status: StepComplete},
	}

	renderProgressSummary(tio.IOStreams, &cfg, steps, time.Now())

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "Built myproject:latest")
}

func TestRenderProgressSummary_Error(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	cfg := testDisplayConfig()
	steps := []*progressStep{
		{name: "RUN exit 1", status: StepError, errMsg: "exit code 1"},
	}

	renderProgressSummary(tio.IOStreams, &cfg, steps, time.Now())

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "failed")
}

func TestRenderProgressSummary_Cached(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	cfg := testDisplayConfig()
	steps := []*progressStep{
		{name: "FROM node:20", status: StepCached, cached: true},
		{name: "RUN npm install", status: StepComplete},
		{name: "[internal] load build definition", status: StepComplete}, // excluded
	}

	renderProgressSummary(tio.IOStreams, &cfg, steps, time.Now())

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "1/2 cached")
}

// ---------------------------------------------------------------------------
// DefaultFormatDuration tests
// ---------------------------------------------------------------------------

func TestDefaultFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0.0s"},
		{200 * time.Millisecond, "0.2s"},
		{4100 * time.Millisecond, "4.1s"},
		{59 * time.Second, "59.0s"},
		{72 * time.Second, "1m 12s"},
		{3661 * time.Second, "1h 1m"},
		{-1 * time.Second, "0.0s"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := defaultFormatDuration(tt.d)
			require.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// Lifecycle hook integration tests
// ---------------------------------------------------------------------------

func TestPlainMode_NilHook_Unchanged(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	// OnLifecycle is nil — should behave exactly as before.
	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "Built myproject:latest")
}

func TestPlainMode_Hook_AbortSkipsSummary(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	cfg.OnLifecycle = func(component, event string) HookResult {
		assert.Equal(t, "progress", component)
		assert.Equal(t, "before_complete", event)
		return HookResult{Continue: false, Message: "user quit"}
	}

	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.EqualError(t, result.Err, "user quit")

	output := tio.ErrBuf.String()
	// Summary should NOT be rendered when hook aborts.
	assert.NotContains(t, output, "Built myproject:latest")
}

func TestPlainMode_Hook_ErrorPropagates(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepComplete},
	)

	hookErr := fmt.Errorf("hook crashed")
	cfg := testDisplayConfig()
	cfg.OnLifecycle = func(_, _ string) HookResult {
		return HookResult{Continue: false, Err: hookErr}
	}

	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.ErrorIs(t, result.Err, hookErr)
}

func TestPlainMode_Hook_ContinueRendersSummary(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	ch := make(chan ProgressStep, 10)

	hookCalled := false
	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	cfg.OnLifecycle = func(_, _ string) HookResult {
		hookCalled = true
		return HookResult{Continue: true}
	}

	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.NoError(t, result.Err)
	assert.True(t, hookCalled)

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "Built myproject:latest")
}

package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
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
// Stage tree tests
// ---------------------------------------------------------------------------

func TestStageState_AllPending(t *testing.T) {
	s := &stageNode{
		name: "builder",
		steps: []*progressStep{
			{status: StepPending},
			{status: StepPending},
		},
	}
	assert.Equal(t, StepPending, s.stageState())
}

func TestStageState_HasRunning(t *testing.T) {
	s := &stageNode{
		name: "builder",
		steps: []*progressStep{
			{status: StepComplete},
			{status: StepRunning},
			{status: StepPending},
		},
	}
	assert.Equal(t, StepRunning, s.stageState())
}

func TestStageState_AllComplete(t *testing.T) {
	s := &stageNode{
		name: "builder",
		steps: []*progressStep{
			{status: StepComplete},
			{status: StepCached},
		},
	}
	assert.Equal(t, StepComplete, s.stageState())
}

func TestStageState_HasError(t *testing.T) {
	// Error takes precedence over everything.
	s := &stageNode{
		name: "builder",
		steps: []*progressStep{
			{status: StepComplete},
			{status: StepError},
			{status: StepRunning},
		},
	}
	assert.Equal(t, StepError, s.stageState())
}

func TestStageState_CompleteAndPending(t *testing.T) {
	// Some complete, rest pending — no running/error → Complete (stage has progress).
	s := &stageNode{
		name: "builder",
		steps: []*progressStep{
			{status: StepComplete},
			{status: StepPending},
		},
	}
	assert.Equal(t, StepComplete, s.stageState())
}

func TestBuildStageTree_SingleGroup(t *testing.T) {
	steps := []*progressStep{
		{name: "[builder 1/3] FROM golang", group: "builder", status: StepComplete},
		{name: "[builder 2/3] COPY go.mod", group: "builder", status: StepRunning},
		{name: "[builder 3/3] RUN go build", group: "builder", status: StepPending},
	}

	tree := buildStageTree(steps, nil)
	require.Len(t, tree.stages, 1)
	assert.Equal(t, "builder", tree.stages[0].name)
	assert.Len(t, tree.stages[0].steps, 3)
	assert.Empty(t, tree.ungrouped)
}

func TestBuildStageTree_MultipleGroups(t *testing.T) {
	steps := []*progressStep{
		{name: "[builder 1/2] FROM golang", group: "builder", status: StepComplete},
		{name: "[builder 2/2] RUN go build", group: "builder", status: StepComplete},
		{name: "[assets 1/2] FROM node", group: "assets", status: StepComplete},
		{name: "[assets 2/2] RUN npm build", group: "assets", status: StepRunning},
		{name: "[runtime 1/1] FROM alpine", group: "runtime", status: StepPending},
	}

	tree := buildStageTree(steps, nil)
	require.Len(t, tree.stages, 3)
	assert.Equal(t, "builder", tree.stages[0].name)
	assert.Len(t, tree.stages[0].steps, 2)
	assert.Equal(t, "assets", tree.stages[1].name)
	assert.Len(t, tree.stages[1].steps, 2)
	assert.Equal(t, "runtime", tree.stages[2].name)
	assert.Len(t, tree.stages[2].steps, 1)
	assert.Empty(t, tree.ungrouped)
}

func TestBuildStageTree_InterleavedGroups(t *testing.T) {
	// BuildKit interleaves stages: stage-2 → builder-a → stage-2 → builder-a.
	steps := []*progressStep{
		{name: "[stage-2 1/3] FROM node", group: "stage-2", status: StepComplete},
		{name: "[builder-a 1/2] FROM golang", group: "builder-a", status: StepComplete},
		{name: "[stage-2 2/3] COPY .", group: "stage-2", status: StepComplete},
		{name: "[builder-a 2/2] RUN go build", group: "builder-a", status: StepComplete},
		{name: "[stage-2 3/3] RUN npm install", group: "stage-2", status: StepRunning},
	}

	tree := buildStageTree(steps, nil)
	require.Len(t, tree.stages, 2)
	// Ordered by first appearance.
	assert.Equal(t, "stage-2", tree.stages[0].name)
	assert.Len(t, tree.stages[0].steps, 3)
	assert.Equal(t, "builder-a", tree.stages[1].name)
	assert.Len(t, tree.stages[1].steps, 2)
}

func TestBuildStageTree_InternalFiltered(t *testing.T) {
	isInternal := func(name string) bool { return strings.HasPrefix(name, "[internal]") }
	steps := []*progressStep{
		{name: "[internal] load build definition", group: "", status: StepComplete},
		{name: "[internal] load .dockerignore", group: "", status: StepComplete},
		{name: "[stage-2 1/3] FROM node:20", group: "stage-2", status: StepRunning},
	}

	tree := buildStageTree(steps, isInternal)
	require.Len(t, tree.stages, 1)
	assert.Equal(t, "stage-2", tree.stages[0].name)
	assert.Empty(t, tree.ungrouped, "internal steps should be filtered, not placed in ungrouped")
}

func TestBuildStageTree_UngroupedSteps(t *testing.T) {
	steps := []*progressStep{
		{name: "COPY . /workspace", group: "", status: StepComplete},
		{name: "[builder 1/2] FROM golang", group: "builder", status: StepRunning},
		{name: "RUN echo hello", group: "", status: StepPending},
	}

	tree := buildStageTree(steps, nil)
	require.Len(t, tree.stages, 1)
	assert.Equal(t, "builder", tree.stages[0].name)
	require.Len(t, tree.ungrouped, 2)
	assert.Equal(t, "COPY . /workspace", tree.ungrouped[0].name)
	assert.Equal(t, "RUN echo hello", tree.ungrouped[1].name)
}

func TestBuildStageTree_Empty(t *testing.T) {
	tree := buildStageTree(nil, nil)
	assert.Empty(t, tree.stages)
	assert.Empty(t, tree.ungrouped)
}

func TestBuildStageTree_AllInternal(t *testing.T) {
	isInternal := func(name string) bool { return true }
	steps := []*progressStep{
		{name: "[internal] step 1", group: "", status: StepComplete},
		{name: "[internal] step 2", group: "", status: StepComplete},
	}

	tree := buildStageTree(steps, isInternal)
	assert.Empty(t, tree.stages)
	assert.Empty(t, tree.ungrouped)
}

// ---------------------------------------------------------------------------
// Tree rendering tests
// ---------------------------------------------------------------------------

// noColorScheme returns a ColorScheme that doesn't apply colors.
func noColorScheme() *iostreams.ColorScheme {
	tio := iostreamstest.New()
	return tio.IOStreams.ColorScheme()
}

func TestRenderCollapsedStage_Complete(t *testing.T) {
	cs := noColorScheme()
	stage := &stageNode{
		name: "builder",
		steps: []*progressStep{
			{status: StepComplete},
			{status: StepComplete},
			{status: StepCached},
		},
	}

	var buf strings.Builder
	renderCollapsedStage(&buf, cs, stage)

	output := buf.String()
	assert.Contains(t, output, "✓")
	assert.Contains(t, output, "builder")
	assert.Contains(t, output, "── 3 steps")
}

func TestRenderCollapsedStage_SingleStep(t *testing.T) {
	cs := noColorScheme()
	stage := &stageNode{
		name: "helper",
		steps: []*progressStep{
			{status: StepCached},
		},
	}

	var buf strings.Builder
	renderCollapsedStage(&buf, cs, stage)

	output := buf.String()
	assert.Contains(t, output, "── 1 step")
	assert.NotContains(t, output, "steps") // singular
}

func TestRenderCollapsedStage_Error(t *testing.T) {
	cs := noColorScheme()
	stage := &stageNode{
		name: "builder",
		steps: []*progressStep{
			{status: StepComplete},
			{status: StepError},
		},
	}

	var buf strings.Builder
	renderCollapsedStage(&buf, cs, stage)

	output := buf.String()
	assert.Contains(t, output, "✗")
	assert.Contains(t, output, "builder")
}

func TestRenderCollapsedStage_Pending(t *testing.T) {
	cs := noColorScheme()
	stage := &stageNode{
		name: "runtime",
		steps: []*progressStep{
			{status: StepPending},
			{status: StepPending},
		},
	}

	var buf strings.Builder
	renderCollapsedStage(&buf, cs, stage)

	output := buf.String()
	assert.Contains(t, output, "○")
	assert.Contains(t, output, "runtime")
}

func TestRenderTreeStepLine_WithConnector(t *testing.T) {
	cs := noColorScheme()
	cfg := &ProgressDisplayConfig{}
	step := &progressStep{
		name:      "RUN npm install",
		status:    StepRunning,
		startTime: time.Now(),
	}

	var buf strings.Builder
	renderTreeStepLine(&buf, cs, cfg, step, treeMid, 80)

	output := buf.String()
	assert.Contains(t, output, "├─")
	assert.Contains(t, output, "●")
	assert.Contains(t, output, "RUN npm install")
}

func TestRenderTreeStepLine_LastConnector(t *testing.T) {
	cs := noColorScheme()
	cfg := &ProgressDisplayConfig{}
	step := &progressStep{
		name:   "COPY . /app",
		status: StepPending,
	}

	var buf strings.Builder
	renderTreeStepLine(&buf, cs, cfg, step, treeLast, 80)

	output := buf.String()
	assert.Contains(t, output, "└─")
	assert.Contains(t, output, "○")
	assert.Contains(t, output, "COPY . /app")
}

func TestRenderTreeLogLines(t *testing.T) {
	cs := noColorScheme()
	step := &progressStep{
		name:   "RUN npm install",
		status: StepRunning,
		logBuf: newRingBuffer(3),
	}
	step.logBuf.Push("installing dependencies...")
	step.logBuf.Push("npm WARN deprecated rimraf@3.0.2")

	var buf strings.Builder
	renderTreeLogLines(&buf, cs, step, false, 80)

	output := buf.String()
	assert.Contains(t, output, "⎿")
	assert.Contains(t, output, "installing dependencies...")
	assert.Contains(t, output, "npm WARN deprecated")
	// Non-last: pipe continuation.
	assert.Contains(t, output, "│")
}

func TestRenderTreeLogLines_NilLogBuf(t *testing.T) {
	cs := noColorScheme()
	step := &progressStep{name: "step", status: StepRunning}

	var buf strings.Builder
	renderTreeLogLines(&buf, cs, step, false, 80)
	assert.Empty(t, buf.String())
}

func TestRenderStageChildren_FewSteps(t *testing.T) {
	cs := noColorScheme()
	cfg := &ProgressDisplayConfig{MaxVisible: 5}
	now := time.Now()
	stage := &stageNode{
		name: "builder",
		steps: []*progressStep{
			{name: "apt-get update", status: StepComplete, startTime: now, endTime: now.Add(800 * time.Millisecond)},
			{name: "npm install", status: StepRunning, startTime: now},
			{name: "npm run build", status: StepPending},
		},
	}

	var buf strings.Builder
	lines := renderStageChildren(&buf, cs, cfg, stage, 5, 80)

	output := buf.String()
	assert.Equal(t, 3, lines)
	// First two get ├─, last gets └─.
	assert.Contains(t, output, "├─")
	assert.Contains(t, output, "└─")
	assert.Contains(t, output, "apt-get update")
	assert.Contains(t, output, "npm install")
	assert.Contains(t, output, "npm run build")
}

func TestRenderStageChildren_WindowedWithCollapse(t *testing.T) {
	cs := noColorScheme()
	cfg := &ProgressDisplayConfig{MaxVisible: 3}
	now := time.Now()

	steps := make([]*progressStep, 10)
	for i := range steps {
		steps[i] = &progressStep{
			name:      fmt.Sprintf("step %d", i+1),
			status:    StepPending,
			startTime: now,
		}
	}
	// First 3 complete, step 4 running, rest pending.
	steps[0].status = StepComplete
	steps[0].endTime = now.Add(time.Second)
	steps[1].status = StepComplete
	steps[1].endTime = now.Add(time.Second)
	steps[2].status = StepComplete
	steps[2].endTime = now.Add(time.Second)
	steps[3].status = StepRunning

	stage := &stageNode{name: "builder", steps: steps}

	var buf strings.Builder
	lines := renderStageChildren(&buf, cs, cfg, stage, 3, 80)

	output := buf.String()
	// Should show collapsed header for completed steps before window.
	assert.Contains(t, output, "steps completed")
	// Should show collapsed footer for pending steps after window.
	assert.Contains(t, output, "more steps")
	// Running step should be visible.
	assert.Contains(t, output, "step 4")
	assert.True(t, lines >= 3, "should have at least window + collapse lines")
}

func TestRenderStageNode_ActiveExpanded(t *testing.T) {
	cs := noColorScheme()
	cfg := &ProgressDisplayConfig{MaxVisible: 5}
	now := time.Now()
	stage := &stageNode{
		name: "builder",
		steps: []*progressStep{
			{name: "FROM golang", status: StepComplete, startTime: now, endTime: now.Add(time.Second)},
			{name: "COPY go.mod", status: StepRunning, startTime: now},
			{name: "RUN go build", status: StepPending},
		},
	}

	var buf strings.Builder
	lines := renderStageNode(&buf, cs, cfg, stage, 5, 80)

	output := buf.String()
	// Heading + 3 children = 4 lines.
	assert.Equal(t, 4, lines)
	assert.Contains(t, output, "● builder")
	assert.Contains(t, output, "├─")
	assert.Contains(t, output, "└─")
}

func TestRenderStageNode_Collapsed(t *testing.T) {
	cs := noColorScheme()
	cfg := &ProgressDisplayConfig{MaxVisible: 5}
	now := time.Now()
	stage := &stageNode{
		name: "builder",
		steps: []*progressStep{
			{name: "FROM golang", status: StepComplete, startTime: now, endTime: now.Add(time.Second)},
			{name: "RUN go build", status: StepComplete, startTime: now, endTime: now.Add(2 * time.Second)},
		},
	}

	var buf strings.Builder
	lines := renderStageNode(&buf, cs, cfg, stage, 5, 80)

	output := buf.String()
	assert.Equal(t, 1, lines) // Single collapsed line.
	assert.Contains(t, output, "✓")
	assert.Contains(t, output, "builder")
	assert.Contains(t, output, "── 2 steps")
}

func TestRenderTreeSection_MultiStage(t *testing.T) {
	cs := noColorScheme()
	cfg := &ProgressDisplayConfig{MaxVisible: 5}
	now := time.Now()

	tree := stageTree{
		stages: []*stageNode{
			{
				name: "builder",
				steps: []*progressStep{
					{name: "FROM golang", status: StepComplete, startTime: now, endTime: now.Add(time.Second)},
					{name: "RUN go build", status: StepComplete, startTime: now, endTime: now.Add(2 * time.Second)},
				},
			},
			{
				name: "runtime",
				steps: []*progressStep{
					{name: "FROM alpine", status: StepComplete, startTime: now, endTime: now.Add(time.Second)},
					{name: "COPY /app", status: StepRunning, startTime: now},
					{name: "ENTRYPOINT", status: StepPending},
				},
			},
		},
	}

	var buf strings.Builder
	hw := 0
	renderTreeSection(&buf, cs, cfg, tree, &hw, 80)

	output := buf.String()
	// builder is collapsed.
	assert.Contains(t, output, "builder")
	assert.Contains(t, output, "── 2 steps")
	// runtime is expanded (has running step).
	assert.Contains(t, output, "● runtime")
	assert.Contains(t, output, "FROM alpine")
	assert.Contains(t, output, "COPY /app")
	assert.Contains(t, output, "ENTRYPOINT")
}

func TestRenderTreeSection_HighWaterPadding(t *testing.T) {
	cs := noColorScheme()
	cfg := &ProgressDisplayConfig{MaxVisible: 5}
	now := time.Now()

	// First render: expanded stage (4 lines).
	tree1 := stageTree{
		stages: []*stageNode{
			{
				name: "builder",
				steps: []*progressStep{
					{name: "FROM golang", status: StepComplete, startTime: now, endTime: now.Add(time.Second)},
					{name: "COPY go.mod", status: StepRunning, startTime: now},
					{name: "RUN go build", status: StepPending},
				},
			},
		},
	}

	var buf1 strings.Builder
	hw := 0
	renderTreeSection(&buf1, cs, cfg, tree1, &hw, 80)
	lines1 := strings.Count(buf1.String(), "\n")

	// Second render: collapsed stage (1 line) — should pad to high water.
	tree2 := stageTree{
		stages: []*stageNode{
			{
				name: "builder",
				steps: []*progressStep{
					{name: "FROM golang", status: StepComplete, startTime: now, endTime: now.Add(time.Second)},
					{name: "COPY go.mod", status: StepComplete, startTime: now, endTime: now.Add(time.Second)},
					{name: "RUN go build", status: StepComplete, startTime: now, endTime: now.Add(time.Second)},
				},
			},
		},
	}

	var buf2 strings.Builder
	renderTreeSection(&buf2, cs, cfg, tree2, &hw, 80)
	lines2 := strings.Count(buf2.String(), "\n")

	assert.GreaterOrEqual(t, lines2, lines1, "collapsed frame should be padded to high-water mark")
}

func TestRenderStageChildren_WithLogLines(t *testing.T) {
	cs := noColorScheme()
	cfg := &ProgressDisplayConfig{MaxVisible: 5}
	now := time.Now()

	logBuf := newRingBuffer(3)
	logBuf.Push("installing dependencies...")
	logBuf.Push("npm WARN deprecated rimraf@3.0.2")

	stage := &stageNode{
		name: "builder",
		steps: []*progressStep{
			{name: "FROM node", status: StepComplete, startTime: now, endTime: now.Add(time.Second)},
			{name: "npm install", status: StepRunning, startTime: now, logBuf: logBuf},
			{name: "npm run build", status: StepPending},
		},
	}

	var buf strings.Builder
	lines := renderStageChildren(&buf, cs, cfg, stage, 5, 80)

	output := buf.String()
	// 3 step lines + 2 log lines = 5.
	assert.Equal(t, 5, lines)
	assert.Contains(t, output, "⎿")
	assert.Contains(t, output, "installing dependencies...")
	assert.Contains(t, output, "npm WARN deprecated")
}

// ---------------------------------------------------------------------------
// Frame height stability tests (BubbleTea inline renderer — tree-based)
// ---------------------------------------------------------------------------

// TestViewFrameHeight_StableAcrossGroupCollapse verifies that View() and
// viewFinished() produce the same number of lines. BubbleTea's inline renderer
// tracks line count between frames — if the final frame is shorter, old lines
// remain as artifacts and the cursor position is wrong.
func TestViewFrameHeight_StableAcrossGroupCollapse(t *testing.T) {
	tio := iostreamstest.New()
	cfg := ProgressDisplayConfig{
		Title:      "Building test",
		Subtitle:   "test:latest",
		MaxVisible: 5,
		LogLines:   3,
		ParseGroup: func(name string) string {
			if len(name) > 1 && name[0] == '[' {
				if sp := strings.Index(name, " "); sp > 1 {
					return name[1:sp]
				}
			}
			return ""
		},
	}

	ch := make(chan ProgressStep)
	close(ch)
	m := newProgressModel(tio.IOStreams, cfg, ch)

	// 3 stages, 3 steps each.
	// Mid-build: builder done (collapsed), assets has running step (expanded), runtime pending (collapsed).
	m.steps = []*progressStep{
		{name: "[builder 1/3] FROM golang", status: StepComplete, group: "builder"},
		{name: "[builder 2/3] COPY go.mod", status: StepComplete, group: "builder"},
		{name: "[builder 3/3] RUN go build", status: StepComplete, group: "builder"},
		{name: "[assets 1/3] FROM node:20-slim", status: StepComplete, group: "assets"},
		{name: "[assets 2/3] COPY package.json", status: StepComplete, group: "assets"},
		{name: "[assets 3/3] RUN npm run build", status: StepRunning, group: "assets"},
		{name: "[runtime 1/3] FROM alpine:3.19", status: StepPending, group: "runtime"},
		{name: "[runtime 2/3] COPY --from=builder /bin/app", status: StepPending, group: "runtime"},
		{name: "[runtime 3/3] ENTRYPOINT", status: StepPending, group: "runtime"},
	}

	// Simulate frame sequence: as groups complete, View() line count must not shrink.
	frames := []struct {
		name  string
		setup func()
	}{
		{"assets running + runtime pending", func() {
			// builder: collapsed (all complete)
			// assets: expanded (has running step) — heading + 3 children
			// runtime: collapsed (all pending)
		}},
		{"all complete", func() {
			for _, s := range m.steps {
				s.status = StepComplete
			}
			// All 3 groups collapse: 3 collapsed headings.
			// Without high-water mark, this frame is shorter → BubbleTea cursor breaks.
		}},
		{"finished", func() {
			m.finished = true
			// viewFinished() must match previous frame height.
		}},
	}

	var prevLines int
	for _, frame := range frames {
		frame.setup()
		view := m.View()
		lines := strings.Count(view, "\n")
		if prevLines > 0 {
			assert.GreaterOrEqual(t, lines, prevLines,
				"frame %q shrank from %d to %d lines\n%s", frame.name, prevLines, lines, view)
		}
		prevLines = lines
	}
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
		Title:          "Building myproject",
		Subtitle:       "myproject:latest",
		CompletionVerb: "Built",
		IsInternal: func(name string) bool {
			return strings.HasPrefix(name, "[internal]")
		},
	}
}

func TestPlainMode_Header(t *testing.T) {
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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

func newTestProgressModel(t *testing.T) (progressModel, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()
	ch := make(chan ProgressStep, 10) // channel not used in direct model tests
	cfg := ProgressDisplayConfig{
		Title:          "Building myproject",
		Subtitle:       "myproject:latest",
		CompletionVerb: "Built",
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

	// Log lines are routed to per-step buffers.
	require.NotNil(t, m.steps[0].logBuf)
	assert.Equal(t, 2, m.steps[0].logBuf.Count())
	lines := m.steps[0].logBuf.Lines()
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

func TestProgressModel_View_InlineLogLines(t *testing.T) {
	m, _ := newTestProgressModel(t)
	m.width = 60

	m.processEvent(ProgressStep{ID: "s1", Name: "[stage-2 1/3] RUN npm install", Status: StepRunning})
	m.processEvent(ProgressStep{ID: "s1", LogLine: "npm warn deprecated rimraf@3.0.2"})
	m.processEvent(ProgressStep{ID: "s1", LogLine: "added 512 packages in 2.5s"})

	output := m.View()
	// Log lines appear inline under the running step with ⎿ connector.
	assert.Contains(t, output, "rimraf@3.0.2")
	assert.Contains(t, output, "512 packages")
	assert.Contains(t, output, "⎿")
}

func TestProgressModel_View_TreeStages(t *testing.T) {
	m, _ := newTestProgressModel(t)
	m.width = 80

	m.processEvent(ProgressStep{ID: "s1", Name: "[stage-2 1/3] FROM node:20", Status: StepComplete})
	m.steps[0].endTime = m.steps[0].startTime
	m.processEvent(ProgressStep{ID: "s2", Name: "[builder 1/2] FROM golang:1.21", Status: StepRunning})

	output := m.View()
	// stage-2 has only complete steps → collapsed.
	assert.Contains(t, output, "stage-2")
	assert.Contains(t, output, "── 1 step")
	// builder has running step → expanded with tree connectors.
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
	assert.Contains(t, output, "●") // completed step = green dot
	assert.Contains(t, output, "FROM node:20")
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
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
	cfg := testDisplayConfig()
	steps := []*progressStep{
		{name: "RUN exit 1", status: StepError, errMsg: "exit code 1"},
	}

	renderProgressSummary(tio.IOStreams, &cfg, steps, time.Now())

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "failed")
}

func TestRenderProgressSummary_Cached(t *testing.T) {
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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
	tio := iostreamstest.New()
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

func TestPlainMode_Hook_AbortNoMessage(t *testing.T) {
	tio := iostreamstest.New()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	cfg.OnLifecycle = func(_, _ string) HookResult {
		return HookResult{Continue: false} // no Err, no Message
	}

	result := runProgressPlain(tio.IOStreams, cfg, ch)
	assert.Error(t, result.Err)
	assert.Contains(t, result.Err.Error(), "aborted by lifecycle hook")
}

func TestHandleHookResult_Continue(t *testing.T) {
	r := handleHookResult(HookResult{Continue: true})
	assert.NoError(t, r.Err)
}

func TestHandleHookResult_AbortWithErr(t *testing.T) {
	err := fmt.Errorf("hook failed")
	r := handleHookResult(HookResult{Continue: false, Err: err})
	assert.ErrorIs(t, r.Err, err)
}

func TestHandleHookResult_AbortWithMessage(t *testing.T) {
	r := handleHookResult(HookResult{Continue: false, Message: "custom reason"})
	assert.Error(t, r.Err)
	assert.Contains(t, r.Err.Error(), "custom reason")
}

func TestHandleHookResult_AbortEmpty(t *testing.T) {
	r := handleHookResult(HookResult{Continue: false})
	assert.Error(t, r.Err)
	assert.Contains(t, r.Err.Error(), "aborted by lifecycle hook")
}

func TestPlainMode_Hook_ContinueRendersSummary(t *testing.T) {
	tio := iostreamstest.New()
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

// ---------------------------------------------------------------------------
// RunProgress mode selection tests
// ---------------------------------------------------------------------------

func TestRunProgress_PlainModeForced(t *testing.T) {
	tio := iostreamstest.New()
	tio.SetInteractive(true) // TTY available, but "plain" overrides
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepRunning},
		ProgressStep{ID: "s1", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	result := RunProgress(tio.IOStreams, "plain", cfg, ch)
	assert.NoError(t, result.Err)
	// Plain mode writes to stderr line by line.
	output := tio.ErrBuf.String()
	assert.Contains(t, output, "[ok]")
}

func TestRunProgress_AutoFallsBackToPlain(t *testing.T) {
	tio := iostreamstest.New() // non-TTY by default
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepRunning},
		ProgressStep{ID: "s1", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	result := RunProgress(tio.IOStreams, "auto", cfg, ch)
	assert.NoError(t, result.Err)
	output := tio.ErrBuf.String()
	assert.Contains(t, output, "[ok]")
}

func TestRunProgress_UnknownModeFallsToAuto(t *testing.T) {
	tio := iostreamstest.New() // non-TTY → plain
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "RUN build", Status: StepRunning},
		ProgressStep{ID: "s1", Status: StepComplete},
	)

	cfg := testDisplayConfig()
	result := RunProgress(tio.IOStreams, "nonexistent", cfg, ch)
	assert.NoError(t, result.Err)
	output := tio.ErrBuf.String()
	assert.Contains(t, output, "[ok]")
}

func TestRunProgress_EmptyChannel(t *testing.T) {
	tio := iostreamstest.New()
	ch := make(chan ProgressStep)
	close(ch) // immediately closed = no events

	cfg := testDisplayConfig()
	result := RunProgress(tio.IOStreams, "plain", cfg, ch)
	assert.NoError(t, result.Err)
}

func TestRunProgress_ZeroValueConfig(t *testing.T) {
	tio := iostreamstest.New()
	ch := make(chan ProgressStep, 10)

	go sendProgressSteps(ch,
		ProgressStep{ID: "s1", Name: "step 1", Status: StepRunning},
		ProgressStep{ID: "s1", Status: StepComplete},
	)

	// Zero-value config should use sensible defaults without panicking.
	cfg := ProgressDisplayConfig{}
	result := RunProgress(tio.IOStreams, "plain", cfg, ch)
	assert.NoError(t, result.Err)
}

// ---------------------------------------------------------------------------
// processEvent edge case tests
// ---------------------------------------------------------------------------

func TestProgressModel_ProcessEvent_EmptyID(t *testing.T) {
	m, _ := newTestProgressModel(t)

	// Empty ID event should not create a step.
	m.processEvent(ProgressStep{ID: "", Name: "orphan", Status: StepRunning})
	assert.Empty(t, m.steps)
}

func TestProgressModel_ProcessEvent_UnknownIDLogLine(t *testing.T) {
	m, _ := newTestProgressModel(t)

	// Log line for an ID that hasn't been seen as a step yet.
	m.processEvent(ProgressStep{ID: "unknown", LogLine: "output line"})
	// Should either ignore or create a step — must not panic.
	// The ID-not-found path should be safe.
}

func TestProgressModel_ProcessEvent_EventAfterCompletion(t *testing.T) {
	m, _ := newTestProgressModel(t)

	m.processEvent(ProgressStep{ID: "s1", Name: "RUN build", Status: StepRunning})
	m.processEvent(ProgressStep{ID: "s1", Status: StepComplete})

	// Late event for the same step after completion.
	m.processEvent(ProgressStep{ID: "s1", LogLine: "straggler log"})
	// Must not panic; step should still be in final state.
	assert.Equal(t, StepComplete, m.steps[0].status)
}

func TestProgressModel_ProcessEvent_CachedStep(t *testing.T) {
	m, _ := newTestProgressModel(t)

	m.processEvent(ProgressStep{ID: "s1", Name: "FROM node:20", Status: StepCached, Cached: true})
	require.Len(t, m.steps, 1)
	assert.Equal(t, StepCached, m.steps[0].status)
	assert.True(t, m.steps[0].cached)
}

func TestProgressModel_ProcessEvent_ErrorStep(t *testing.T) {
	m, _ := newTestProgressModel(t)

	m.processEvent(ProgressStep{ID: "s1", Name: "RUN npm install", Status: StepRunning})
	m.processEvent(ProgressStep{ID: "s1", Status: StepError, Error: "exit code 1"})

	require.Len(t, m.steps, 1)
	assert.Equal(t, StepError, m.steps[0].status)
	assert.Equal(t, "exit code 1", m.steps[0].errMsg)
}

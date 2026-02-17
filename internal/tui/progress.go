package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// ProgressStepStatus represents the state of a progress step.
type ProgressStepStatus int

const (
	StepPending ProgressStepStatus = iota
	StepRunning
	StepComplete
	StepCached
	StepError
)

// ProgressStep represents a single progress update from the pipeline.
// The caller sends these on a channel consumed by RunProgress.
type ProgressStep struct {
	ID      string
	Name    string
	Status  ProgressStepStatus
	LogLine string
	Cached  bool
	Error   string
}

// ProgressDisplayConfig configures the progress display.
// Domain-specific logic flows in through callbacks — the display itself
// has zero knowledge of what is being tracked.
type ProgressDisplayConfig struct {
	Title    string // e.g., "Building"
	Subtitle string // e.g., image tag

	CompletionVerb string // Success summary verb (e.g., "Built", "Deployed"). Default: "Completed"

	MaxVisible int // per-stage child window size (default: 5)
	LogLines   int // per-step log buffer capacity (default: 3)

	// Callbacks — all optional. nil = passthrough / no-op.
	IsInternal     func(string) bool          // filter function (nil = show all)
	CleanName      func(string) string        // name cleaning (nil = passthrough)
	ParseGroup     func(string) string        // group/stage detection (nil = no groups)
	FormatDuration func(time.Duration) string // duration formatting (nil = default)

	// Lifecycle hook — called at key moments. nil = no-op.
	OnLifecycle LifecycleHook

	// AltScreen enables the alternate screen buffer for TTY mode.
	// When true, progress output is rendered in the alt screen and cleared
	// when the display finishes — useful for clean handoff to a container TTY.
	AltScreen bool
}

// ProgressResult contains the outcome of a progress display.
type ProgressResult struct {
	Err error // only set if the progress display itself errors
}

// ---------------------------------------------------------------------------
// Configuration defaults
// ---------------------------------------------------------------------------

const (
	defaultMaxVisible = 5
	defaultLogLines   = 3
)

func (cfg *ProgressDisplayConfig) maxVisible() int {
	if cfg.MaxVisible > 0 {
		return cfg.MaxVisible
	}
	return defaultMaxVisible
}

func (cfg *ProgressDisplayConfig) logLines() int {
	if cfg.LogLines > 0 {
		return cfg.LogLines
	}
	return defaultLogLines
}

func (cfg *ProgressDisplayConfig) isInternal(name string) bool {
	if cfg.IsInternal == nil {
		return false
	}
	return cfg.IsInternal(name)
}

func (cfg *ProgressDisplayConfig) cleanName(name string) string {
	if cfg.CleanName == nil {
		return name
	}
	return cfg.CleanName(name)
}

func (cfg *ProgressDisplayConfig) parseGroup(name string) string {
	if cfg.ParseGroup == nil {
		return ""
	}
	return cfg.ParseGroup(name)
}

func (cfg *ProgressDisplayConfig) formatDuration(d time.Duration) string {
	if cfg.FormatDuration == nil {
		return defaultFormatDuration(d)
	}
	return cfg.FormatDuration(d)
}

func (cfg *ProgressDisplayConfig) completionVerb() string {
	if cfg.CompletionVerb != "" {
		return cfg.CompletionVerb
	}
	return "Completed"
}

func (cfg *ProgressDisplayConfig) fireHook(component, event string) HookResult {
	if cfg.OnLifecycle == nil {
		return HookResult{Continue: true}
	}
	return cfg.OnLifecycle(component, event)
}

// defaultFormatDuration provides a compact duration format when no custom formatter is set.
func defaultFormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := d.Seconds()
	switch {
	case secs < 60:
		return fmt.Sprintf("%.1fs", secs)
	case secs < 3600:
		m := int(secs) / 60
		s := int(secs) % 60
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		h := int(secs) / 3600
		m := (int(secs) % 3600) / 60
		return fmt.Sprintf("%dh %dm", h, m)
	}
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// RunProgress runs a progress display, consuming steps from ch until it is closed.
// It selects TTY (BubbleTea) or plain mode based on the terminal and mode setting.
// The mode parameter can be "auto", "plain", or "tty".
// Channel closure signals completion — the caller closes ch when done.
func RunProgress(ios *iostreams.IOStreams, mode string, cfg ProgressDisplayConfig, ch <-chan ProgressStep) ProgressResult {
	ttyMode := ios.IsStderrTTY()
	switch mode {
	case "tty":
		ttyMode = true
	case "plain":
		ttyMode = false
	}

	if ttyMode {
		return runProgressTTY(ios, cfg, ch)
	}
	return runProgressPlain(ios, cfg, ch)
}

// ---------------------------------------------------------------------------
// Internal types
// ---------------------------------------------------------------------------

// progressStep tracks state for a single step.
type progressStep struct {
	id        string
	name      string
	status    ProgressStepStatus
	cached    bool
	errMsg    string
	startTime time.Time
	endTime   time.Time
	group     string      // parsed group name (e.g., stage name)
	logBuf    *ringBuffer // per-step log buffer (nil until first log received)
}

// Tree connector constants for tree-based progress rendering.
const (
	treeMid  = "├─" // middle child
	treeLast = "└─" // last child
	treePipe = "│"  // vertical continuation
	treeLog  = "⎿"  // log sub-output
)

// stageNode represents a build stage (group) in the display tree.
type stageNode struct {
	name  string
	steps []*progressStep
}

// stageState returns the aggregate state: Error > Running > Complete > Pending.
func (s *stageNode) stageState() ProgressStepStatus {
	hasRunning := false
	hasComplete := false
	for _, step := range s.steps {
		switch step.status {
		case StepError:
			return StepError
		case StepRunning:
			hasRunning = true
		case StepComplete, StepCached:
			hasComplete = true
		}
	}
	if hasRunning {
		return StepRunning
	}
	if hasComplete {
		return StepComplete
	}
	return StepPending
}

// stageTree is the result of buildStageTree.
type stageTree struct {
	stages    []*stageNode    // ordered by first appearance
	ungrouped []*progressStep // steps with no group
}

// buildStageTree groups steps into stages, ordered by first appearance.
// Internal steps are filtered out. Steps with no group go to ungrouped.
func buildStageTree(steps []*progressStep, isInternal func(string) bool) stageTree {
	var tree stageTree
	stageIndex := make(map[string]int) // group name → index in tree.stages

	for _, s := range steps {
		if isInternal != nil && isInternal(s.name) {
			continue
		}
		if s.group == "" {
			tree.ungrouped = append(tree.ungrouped, s)
			continue
		}
		idx, exists := stageIndex[s.group]
		if !exists {
			idx = len(tree.stages)
			stageIndex[s.group] = idx
			tree.stages = append(tree.stages, &stageNode{name: s.group})
		}
		tree.stages[idx].steps = append(tree.stages[idx].steps, s)
	}

	return tree
}

// ringBuffer is a fixed-size circular buffer for log lines.
type ringBuffer struct {
	lines    []string
	capacity int
	head     int
	count    int
	full     bool
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{
		lines:    make([]string, capacity),
		capacity: capacity,
	}
}

func (rb *ringBuffer) Push(line string) {
	rb.lines[rb.head] = line
	rb.head = (rb.head + 1) % rb.capacity
	if rb.head == 0 && rb.count >= rb.capacity {
		rb.full = true
	}
	rb.count++
}

func (rb *ringBuffer) Lines() []string {
	if rb.count == 0 {
		return nil
	}
	n := rb.count
	if n > rb.capacity {
		n = rb.capacity
	}
	result := make([]string, n)
	start := 0
	if rb.full || rb.count > rb.capacity {
		start = rb.head
	}
	for i := range n {
		result[i] = rb.lines[(start+i)%rb.capacity]
	}
	return result
}

func (rb *ringBuffer) Count() int {
	return rb.count
}

// ---------------------------------------------------------------------------
// BubbleTea messages
// ---------------------------------------------------------------------------

type progressStepMsg ProgressStep

type progressChannelClosedMsg struct{}

func waitForProgressStep(ch <-chan ProgressStep) tea.Cmd {
	return func() tea.Msg {
		step, ok := <-ch
		if !ok {
			return progressChannelClosedMsg{}
		}
		return progressStepMsg(step)
	}
}

// ---------------------------------------------------------------------------
// BubbleTea model — TTY progress display
// ---------------------------------------------------------------------------

type progressModel struct {
	ios *iostreams.IOStreams
	cs  *iostreams.ColorScheme
	cfg ProgressDisplayConfig

	steps     []*progressStep
	stepIndex map[string]int
	startTime time.Time

	finished    bool
	interrupted bool // true if user pressed Ctrl+C

	width int

	// stepHighWater tracks the maximum line count rendered by the step section.
	// Pointer so View() (value receiver) can update it through dereference.
	// renderTreeSection pads to this value to prevent BubbleTea inline frame shrinkage.
	stepHighWater *int

	eventCh <-chan ProgressStep
}

func newProgressModel(ios *iostreams.IOStreams, cfg ProgressDisplayConfig, eventCh <-chan ProgressStep) progressModel {
	return progressModel{
		ios:           ios,
		cs:            ios.ColorScheme(),
		cfg:           cfg,
		stepIndex:     make(map[string]int),
		startTime:     time.Now(),
		width:         ios.TerminalWidth(),
		stepHighWater: new(int),
		eventCh:       eventCh,
	}
}

func (m progressModel) Init() tea.Cmd {
	return waitForProgressStep(m.eventCh)
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.interrupted = true
			m.finished = true
			return m, tea.Quit
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case progressStepMsg:
		step := ProgressStep(msg)
		m.processEvent(step)
		return m, waitForProgressStep(m.eventCh)

	case progressChannelClosedMsg:
		m.finished = true
		return m, tea.Quit
	}

	return m, nil
}

func (m *progressModel) processEvent(step ProgressStep) {
	// Route log lines to per-step buffers.
	if step.LogLine != "" {
		if idx, exists := m.stepIndex[step.ID]; exists {
			s := m.steps[idx]
			if s.logBuf == nil {
				s.logBuf = newRingBuffer(m.cfg.logLines())
			}
			s.logBuf.Push(step.LogLine)
		}
		// Log-only events (no Name, no Status change) stop here.
		if step.Name == "" {
			return
		}
	}

	if step.ID == "" {
		return
	}

	idx, exists := m.stepIndex[step.ID]
	if !exists {
		idx = len(m.steps)
		m.stepIndex[step.ID] = idx
		m.steps = append(m.steps, &progressStep{
			id:        step.ID,
			name:      step.Name,
			status:    step.Status,
			cached:    step.Cached,
			errMsg:    step.Error,
			startTime: time.Now(),
			group:     m.cfg.parseGroup(step.Name),
		})
	} else {
		s := m.steps[idx]
		if step.Name != "" {
			s.name = step.Name
			s.group = m.cfg.parseGroup(step.Name)
		}
		s.status = step.Status
		s.cached = step.Cached
		if step.Error != "" {
			s.errMsg = step.Error
		}
		if step.Status == StepComplete || step.Status == StepCached || step.Status == StepError {
			s.endTime = time.Now()
		}
	}
}

// View renders the progress display. Used for both live updates and the final
// BubbleTea frame — the tree rendering shows status icons without animation.
func (m progressModel) View() string {
	cs := m.cs
	width := m.width
	if width < 40 {
		width = 40
	}

	var buf strings.Builder

	// Header
	renderProgressHeader(&buf, cs, &m.cfg, width)
	buf.WriteByte('\n')

	// Tree-based step display.
	tree := buildStageTree(m.steps, m.cfg.isInternal)
	renderTreeSection(&buf, cs, &m.cfg, tree, m.stepHighWater, width)

	return buf.String()
}

// ---------------------------------------------------------------------------
// TTY render helpers
// ---------------------------------------------------------------------------

func renderProgressHeader(buf *strings.Builder, cs *iostreams.ColorScheme, cfg *ProgressDisplayConfig, width int) {
	title := fmt.Sprintf("  ━━ %s ", cfg.Title)
	subtitle := fmt.Sprintf(" %s ━━", cfg.Subtitle)

	titleRendered := cs.Bold(cs.Primary(title))
	subtitleRendered := cs.Muted(subtitle)

	titleWidth := text.CountVisibleWidth(titleRendered)
	subtitleWidth := text.CountVisibleWidth(subtitleRendered)
	fillWidth := width - titleWidth - subtitleWidth
	if fillWidth < 3 {
		fillWidth = 3
	}
	fill := cs.Muted(strings.Repeat("━", fillWidth))

	buf.WriteString(titleRendered)
	buf.WriteString(fill)
	buf.WriteString(subtitleRendered)
	buf.WriteByte('\n')
}

func renderProgressStepLine(buf *strings.Builder, cs *iostreams.ColorScheme, cfg *ProgressDisplayConfig, step *progressStep, width int) {
	icon, name, duration := stepLineParts(cs, cfg, step)
	renderStepLineWithPrefix(buf, "  ", icon, name, duration, width)
}

// renderStepLineWithPrefix renders a single step line with the given prefix.
// Shared layout logic for both flat and tree-based step lines.
func renderStepLineWithPrefix(buf *strings.Builder, prefix, icon, name, duration string, width int) {
	prefixWidth := text.CountVisibleWidth(prefix)
	durationWidth := text.CountVisibleWidth(duration)
	maxNameWidth := width - prefixWidth - durationWidth - 3 // icon + space + margin
	if maxNameWidth > 0 {
		name = text.Truncate(name, maxNameWidth)
	}

	buf.WriteString(prefix)
	buf.WriteString(icon)
	buf.WriteByte(' ')
	buf.WriteString(name)
	if duration != "" {
		nameWidth := text.CountVisibleWidth(name)
		pad := width - prefixWidth - 2 - nameWidth - durationWidth // 2 = icon+space
		if pad < 2 {
			pad = 2
		}
		buf.WriteString(strings.Repeat(" ", pad))
		buf.WriteString(duration)
	}
	buf.WriteByte('\n')
}

// stepLineParts returns the icon, display name, and duration string for a step.
func stepLineParts(cs *iostreams.ColorScheme, cfg *ProgressDisplayConfig, step *progressStep) (icon, name, duration string) {
	switch step.status {
	case StepComplete:
		icon = cs.Green("●")
		name = cfg.cleanName(step.name)
		d := step.endTime.Sub(step.startTime)
		duration = cs.Muted(cfg.formatDuration(d))
	case StepCached:
		icon = cs.Green("●")
		name = cfg.cleanName(step.name)
		duration = cs.Muted("cached")
	case StepRunning:
		icon = cs.Muted("●")
		name = cfg.cleanName(step.name)
		d := time.Since(step.startTime)
		duration = cs.Muted(cfg.formatDuration(d))
	case StepPending:
		icon = cs.Muted("○")
		name = cs.Muted(cfg.cleanName(step.name))
	case StepError:
		icon = cs.Error("●")
		name = cfg.cleanName(step.name)
		d := step.endTime.Sub(step.startTime)
		duration = cs.Muted(cfg.formatDuration(d))
	}
	return icon, name, duration
}

// ---------------------------------------------------------------------------
// Tree-based rendering
// ---------------------------------------------------------------------------

// stageIcon returns the status icon for a stage heading.
func stageIcon(cs *iostreams.ColorScheme, state ProgressStepStatus) string {
	switch state {
	case StepComplete, StepCached:
		return cs.Green("✓")
	case StepRunning:
		return cs.Muted("●")
	case StepError:
		return cs.Error("✗")
	default:
		return cs.Muted("○")
	}
}

// renderCollapsedStage writes a single collapsed stage line: "  ✓ name ── N steps".
func renderCollapsedStage(buf *strings.Builder, cs *iostreams.ColorScheme, stage *stageNode) {
	icon := stageIcon(cs, stage.stageState())
	noun := "steps"
	if len(stage.steps) == 1 {
		noun = "step"
	}
	label := fmt.Sprintf("── %d %s", len(stage.steps), noun)
	buf.WriteString(fmt.Sprintf("  %s %s %s\n", icon, cs.Bold(stage.name), cs.Muted(label)))
}

// renderTreeStepLine writes a step line with a tree connector prefix.
// connector is treeMid or treeLast; continuation is treePipe or "  " for vertical lines below.
func renderTreeStepLine(buf *strings.Builder, cs *iostreams.ColorScheme, cfg *ProgressDisplayConfig, step *progressStep, connector string, width int) {
	icon, name, duration := stepLineParts(cs, cfg, step)
	// Prefix: "    ├─ " or "    └─ " (4 indent + connector + space)
	prefix := "    " + connector + " "
	renderStepLineWithPrefix(buf, prefix, icon, name, duration, width)
}

// renderTreeLogLines writes inline log lines below a running step with tree connectors.
func renderTreeLogLines(buf *strings.Builder, cs *iostreams.ColorScheme, step *progressStep, isLast bool, width int) {
	if step.logBuf == nil {
		return
	}
	lines := step.logBuf.Lines()
	if len(lines) == 0 {
		return
	}

	pipe := treePipe
	if isLast {
		pipe = " "
	}

	innerWidth := width - 10 // "    │  ⎿ " or "       ⎿ " = ~9-10 chars
	if innerWidth < 10 {
		innerWidth = 10
	}

	for i, line := range lines {
		line = text.Truncate(line, innerWidth)
		if i == 0 {
			buf.WriteString(fmt.Sprintf("    %s  %s %s\n", cs.Muted(pipe), cs.Muted(treeLog), line))
		} else {
			buf.WriteString(fmt.Sprintf("    %s    %s\n", cs.Muted(pipe), line))
		}
	}
}

// renderStageChildren writes the children of an active stage with tree connectors.
// If children exceed maxVisible, a centered window is shown around the running step.
func renderStageChildren(buf *strings.Builder, cs *iostreams.ColorScheme, cfg *ProgressDisplayConfig, stage *stageNode, maxVisible int, width int) int {
	steps := stage.steps
	lines := 0

	if len(steps) <= maxVisible {
		// Show all children directly.
		for i, step := range steps {
			isLast := i == len(steps)-1
			connector := treeMid
			if isLast {
				connector = treeLast
			}
			renderTreeStepLine(buf, cs, cfg, step, connector, width)
			lines++
			if step.status == StepRunning {
				renderTreeLogLines(buf, cs, step, isLast, width)
				if step.logBuf != nil {
					n := len(step.logBuf.Lines())
					lines += n
				}
			}
		}
		return lines
	}

	// Centered window: find first running step.
	runIdx := -1
	for i, step := range steps {
		if step.status == StepRunning {
			runIdx = i
			break
		}
	}
	if runIdx < 0 {
		runIdx = 0
	}

	// Center window on running step.
	half := maxVisible / 2
	winStart := runIdx - half
	winEnd := winStart + maxVisible
	if winStart < 0 {
		winStart = 0
		winEnd = maxVisible
	}
	if winEnd > len(steps) {
		winEnd = len(steps)
		winStart = winEnd - maxVisible
		if winStart < 0 {
			winStart = 0
		}
	}

	// Collapsed header for completed steps before window.
	completedBefore := 0
	for i := 0; i < winStart; i++ {
		if steps[i].status == StepComplete || steps[i].status == StepCached {
			completedBefore++
		}
	}
	if winStart > 0 {
		noun := "steps"
		if completedBefore == 1 {
			noun = "step"
		}
		label := fmt.Sprintf("✓ %d %s completed", completedBefore, noun)
		buf.WriteString(fmt.Sprintf("    %s %s\n", treeMid, cs.Muted(label)))
		lines++
	}

	// Window steps.
	pendingAfter := len(steps) - winEnd
	for i := winStart; i < winEnd; i++ {
		isLast := i == len(steps)-1 && pendingAfter == 0
		connector := treeMid
		if isLast {
			connector = treeLast
		}
		renderTreeStepLine(buf, cs, cfg, steps[i], connector, width)
		lines++
		if steps[i].status == StepRunning {
			renderTreeLogLines(buf, cs, steps[i], isLast, width)
			if steps[i].logBuf != nil {
				n := len(steps[i].logBuf.Lines())
				lines += n
			}
		}
	}

	// Collapsed footer for pending steps after window.
	if pendingAfter > 0 {
		noun := "steps"
		if pendingAfter == 1 {
			noun = "step"
		}
		label := fmt.Sprintf("○ %d more %s", pendingAfter, noun)
		buf.WriteString(fmt.Sprintf("    %s %s\n", treeLast, cs.Muted(label)))
		lines++
	}

	return lines
}

// renderStageNode writes a single stage: collapsed if complete/error/pending, expanded if active.
func renderStageNode(buf *strings.Builder, cs *iostreams.ColorScheme, cfg *ProgressDisplayConfig, stage *stageNode, maxVisible int, width int) int {
	state := stage.stageState()
	lines := 0

	switch state {
	case StepRunning:
		// Active stage: heading + expanded children.
		icon := stageIcon(cs, state)
		buf.WriteString(fmt.Sprintf("  %s %s\n", icon, cs.Bold(stage.name)))
		lines++
		lines += renderStageChildren(buf, cs, cfg, stage, maxVisible, width)
	default:
		// Collapsed stage (complete, pending, error).
		renderCollapsedStage(buf, cs, stage)
		lines++
	}

	return lines
}

// renderTreeSection writes the full tree display: stages + ungrouped steps.
// It tracks the maximum line count via highWater for stable frame height.
func renderTreeSection(buf *strings.Builder, cs *iostreams.ColorScheme, cfg *ProgressDisplayConfig, tree stageTree, highWater *int, width int) {
	lines := 0

	// Ungrouped steps first (if any).
	for _, step := range tree.ungrouped {
		renderProgressStepLine(buf, cs, cfg, step, width)
		lines++
	}

	// Stage nodes.
	for _, stage := range tree.stages {
		lines += renderStageNode(buf, cs, cfg, stage, cfg.maxVisible(), width)
	}

	// Update high-water mark and pad.
	if lines > *highWater {
		*highWater = lines
	}
	for range *highWater - lines {
		buf.WriteByte('\n')
	}
}

// ---------------------------------------------------------------------------
// TTY mode — BubbleTea
// ---------------------------------------------------------------------------

func runProgressTTY(ios *iostreams.IOStreams, cfg ProgressDisplayConfig, eventCh <-chan ProgressStep) ProgressResult {
	model := newProgressModel(ios, cfg, eventCh)
	var opts []ProgramOption
	if cfg.AltScreen {
		opts = append(opts, WithAltScreen(true))
	}
	finalModel, err := RunProgram(ios, model, opts...)
	if err != nil {
		return ProgressResult{Err: fmt.Errorf("display error: %w", err)}
	}

	m, ok := finalModel.(progressModel)
	if !ok {
		return ProgressResult{Err: fmt.Errorf("unexpected model type")}
	}

	if m.interrupted {
		return ProgressResult{Err: context.Canceled}
	}

	// Fire lifecycle hook — display is still visible, stdin is free.
	if result := handleHookResult(cfg.fireHook("progress", "before_complete")); result.Err != nil {
		return result
	}

	renderProgressSummary(ios, &cfg, m.steps, m.startTime)
	return ProgressResult{}
}

// handleHookResult converts a HookResult into a ProgressResult.
// When Continue is false, an error is always produced — either from the hook's
// Err field, its Message, or a default "aborted by lifecycle hook" message.
func handleHookResult(hr HookResult) ProgressResult {
	if hr.Continue {
		return ProgressResult{}
	}
	if hr.Err != nil {
		return ProgressResult{Err: hr.Err}
	}
	msg := hr.Message
	if msg == "" {
		msg = "aborted by lifecycle hook"
	}
	return ProgressResult{Err: fmt.Errorf("%s", msg)}
}

// ---------------------------------------------------------------------------
// Plain mode
// ---------------------------------------------------------------------------

func runProgressPlain(ios *iostreams.IOStreams, cfg ProgressDisplayConfig, eventCh <-chan ProgressStep) ProgressResult {
	cs := ios.ColorScheme()
	width := ios.TerminalWidth() - 20
	if width < 40 {
		width = 40
	}
	startTime := time.Now()

	// Header.
	fmt.Fprintf(ios.ErrOut, "%s %s (%s)\n", cs.Primary("━━"), cfg.Title, cfg.Subtitle)

	steps := make(map[string]*progressStep)
	var orderedSteps []*progressStep

	for step := range eventCh {
		// Skip log-only events in plain mode.
		if step.ID == "" || (step.LogLine != "" && step.Name == "") {
			continue
		}

		s, exists := steps[step.ID]
		if !exists {
			s = &progressStep{
				id:        step.ID,
				name:      step.Name,
				status:    step.Status,
				cached:    step.Cached,
				errMsg:    step.Error,
				startTime: time.Now(),
			}
			steps[step.ID] = s
			orderedSteps = append(orderedSteps, s)
			if step.Status != StepPending {
				renderPlainProgressStepLine(ios, cs, &cfg, s, width)
			}
		} else {
			prevStatus := s.status
			if step.Name != "" {
				s.name = step.Name
			}
			s.status = step.Status
			s.cached = step.Cached
			if step.Error != "" {
				s.errMsg = step.Error
			}
			if step.Status == StepComplete || step.Status == StepCached || step.Status == StepError {
				s.endTime = time.Now()
			}
			if step.Status != prevStatus {
				renderPlainProgressStepLine(ios, cs, &cfg, s, width)
			}
		}
	}

	// Fire lifecycle hook.
	if result := handleHookResult(cfg.fireHook("progress", "before_complete")); result.Err != nil {
		return result
	}

	renderProgressSummary(ios, &cfg, orderedSteps, startTime)
	return ProgressResult{}
}

func renderPlainProgressStepLine(ios *iostreams.IOStreams, cs *iostreams.ColorScheme, cfg *ProgressDisplayConfig, step *progressStep, width int) {
	if cfg.isInternal(step.name) {
		return
	}
	name := cfg.cleanName(step.name)
	if width > 0 {
		runes := []rune(name)
		if len(runes) > width {
			name = string(runes[:width-1]) + "…"
		}
	}
	switch step.status {
	case StepRunning:
		fmt.Fprintf(ios.ErrOut, "%s  %s\n", cs.Cyan("[run]"), name)
	case StepComplete:
		d := step.endTime.Sub(step.startTime)
		fmt.Fprintf(ios.ErrOut, "%s   %s (%s)\n", cs.Success("[ok]"), name, cfg.formatDuration(d))
	case StepCached:
		fmt.Fprintf(ios.ErrOut, "%s   %s (cached)\n", cs.Success("[ok]"), name)
	case StepError:
		fmt.Fprintf(ios.ErrOut, "%s %s: %s\n", cs.Error("[fail]"), name, step.errMsg)
	}
}

// ---------------------------------------------------------------------------
// Shared summary
// ---------------------------------------------------------------------------

func renderProgressSummary(ios *iostreams.IOStreams, cfg *ProgressDisplayConfig, steps []*progressStep, startTime time.Time) {
	cs := ios.ColorScheme()
	elapsed := time.Since(startTime)

	// Detect failure from step statuses.
	hasError := false
	for _, step := range steps {
		if step.status == StepError {
			hasError = true
			break
		}
	}

	if hasError {
		fmt.Fprintf(ios.ErrOut, "\n%s %s failed (%s)\n", cs.FailureIcon(), cfg.Title, cfg.formatDuration(elapsed))
		return
	}

	cachedCount := 0
	visibleCount := 0
	for _, step := range steps {
		if cfg.isInternal(step.name) {
			continue
		}
		visibleCount++
		if step.cached || step.status == StepCached {
			cachedCount++
		}
	}

	summary := fmt.Sprintf("%s %s", cfg.completionVerb(), cfg.Subtitle)
	if cachedCount > 0 {
		summary += fmt.Sprintf(" (%d/%d cached)", cachedCount, visibleCount)
	}
	fmt.Fprintf(ios.ErrOut, "%s %s %s\n", cs.SuccessIcon(), summary, cs.Muted(cfg.formatDuration(elapsed)))
}

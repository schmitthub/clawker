package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// ProgressStepStatus represents the state of a progress step.
type ProgressStepStatus int

const (
	StepPending  ProgressStepStatus = iota
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

	MaxVisible int // sliding window size (default: 5)
	LogLines   int // viewport height (default: 3)

	// Callbacks — all optional. nil = passthrough / no-op.
	IsInternal     func(string) bool         // filter function (nil = show all)
	CleanName      func(string) string       // name cleaning (nil = passthrough)
	ParseGroup     func(string) string       // group/stage detection (nil = no groups)
	FormatDuration func(time.Duration) string // duration formatting (nil = default)

	// Lifecycle hook — called at key moments. nil = no-op.
	OnLifecycle LifecycleHook
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

func (cfg ProgressDisplayConfig) maxVisible() int {
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
	group     string // parsed group name (e.g., stage name)
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
	logBuf    *ringBuffer
	startTime time.Time

	finished    bool
	interrupted bool // true if user pressed Ctrl+C

	spinner spinner.Model
	width   int

	eventCh <-chan ProgressStep
}

func newProgressModel(ios *iostreams.IOStreams, cfg ProgressDisplayConfig, eventCh <-chan ProgressStep) progressModel {
	s := spinner.New()
	s.Spinner = spinner.Spinner{
		Frames: []string{"●", "○"},
		FPS:    150 * time.Millisecond,
	}
	s.Style = BrandOrangeStyle

	return progressModel{
		ios:       ios,
		cs:        ios.ColorScheme(),
		cfg:       cfg,
		stepIndex: make(map[string]int),
		logBuf:    newRingBuffer(cfg.logLines()),
		startTime: time.Now(),
		spinner:   s,
		width:     ios.TerminalWidth(),
		eventCh:   eventCh,
	}
}

func (m progressModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		waitForProgressStep(m.eventCh),
	)
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

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

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
	if step.LogLine != "" {
		m.logBuf.Push(step.LogLine)
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

// View renders the fixed-height progress display.
func (m progressModel) View() string {
	if m.finished {
		return m.viewFinished()
	}

	cs := m.cs
	width := m.width
	if width < 40 {
		width = 40
	}

	var buf strings.Builder

	// Header
	renderProgressHeader(&buf, cs, &m.cfg, width)
	buf.WriteByte('\n')

	// Visible steps with sliding window
	maxVis := m.cfg.maxVisible()
	visible, hiddenCount := visibleProgressSteps(m.steps, maxVis, m.cfg.isInternal)

	if hiddenCount > 0 {
		icon := cs.Green("✓")
		label := fmt.Sprintf("%d steps completed", hiddenCount)
		buf.WriteString(fmt.Sprintf("  %s %s\n", icon, cs.Muted(label)))
	}

	lastGroup := ""
	for _, step := range visible {
		// Group heading when group changes.
		if step.group != "" && step.group != lastGroup {
			heading := cs.Bold(cs.BrandOrange(step.group))
			buf.WriteString(fmt.Sprintf("  ─ %s\n", heading))
		}
		lastGroup = step.group
		renderProgressStepLine(&buf, cs, &m.cfg, step, m.spinner.View(), width)
	}

	buf.WriteByte('\n')

	// Output viewport
	renderProgressViewport(&buf, cs, &m.cfg, m.logBuf, m.steps, width)

	return buf.String()
}

// viewFinished renders a static final snapshot for BubbleTea's last frame.
// Shows header + all visible steps with final status icons + log viewport — no spinner animation.
func (m progressModel) viewFinished() string {
	cs := m.cs
	width := m.width
	if width < 40 {
		width = 40
	}

	var buf strings.Builder

	// Header
	renderProgressHeader(&buf, cs, &m.cfg, width)
	buf.WriteByte('\n')

	// All visible steps — pass empty spinnerView so running steps show no animation.
	maxVis := m.cfg.maxVisible()
	visible, hiddenCount := visibleProgressSteps(m.steps, maxVis, m.cfg.isInternal)

	if hiddenCount > 0 {
		icon := cs.Green("✓")
		label := fmt.Sprintf("%d steps completed", hiddenCount)
		buf.WriteString(fmt.Sprintf("  %s %s\n", icon, cs.Muted(label)))
	}

	lastGroup := ""
	for _, step := range visible {
		if step.group != "" && step.group != lastGroup {
			heading := cs.Bold(cs.BrandOrange(step.group))
			buf.WriteString(fmt.Sprintf("  ─ %s\n", heading))
		}
		lastGroup = step.group
		renderProgressStepLine(&buf, cs, &m.cfg, step, "", width)
	}

	buf.WriteByte('\n')

	// Output viewport — preserve log lines so they're visible after exit.
	renderProgressViewport(&buf, cs, &m.cfg, m.logBuf, m.steps, width)

	return buf.String()
}

// ---------------------------------------------------------------------------
// TTY render helpers
// ---------------------------------------------------------------------------

func renderProgressHeader(buf *strings.Builder, cs *iostreams.ColorScheme, cfg *ProgressDisplayConfig, width int) {
	title := fmt.Sprintf("  ━━ %s ", cfg.Title)
	subtitle := fmt.Sprintf(" %s ━━", cfg.Subtitle)

	titleRendered := cs.Bold(cs.BrandOrange(title))
	subtitleRendered := cs.Muted(subtitle)

	titleWidth := CountVisibleWidth(titleRendered)
	subtitleWidth := CountVisibleWidth(subtitleRendered)
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

func renderProgressStepLine(buf *strings.Builder, cs *iostreams.ColorScheme, cfg *ProgressDisplayConfig, step *progressStep, spinnerView string, width int) {
	var icon, name, duration string

	switch step.status {
	case StepComplete:
		icon = cs.Green("✓")
		name = cfg.cleanName(step.name)
		d := step.endTime.Sub(step.startTime)
		duration = cs.Muted(cfg.formatDuration(d))
	case StepCached:
		icon = cs.Green("✓")
		name = cfg.cleanName(step.name)
		duration = cs.Muted("cached")
	case StepRunning:
		icon = spinnerView
		name = cfg.cleanName(step.name)
		d := time.Since(step.startTime)
		duration = cs.Muted(cfg.formatDuration(d))
	case StepPending:
		icon = cs.Muted("○")
		name = cs.Muted(cfg.cleanName(step.name))
	case StepError:
		icon = cs.Error("✗")
		name = cfg.cleanName(step.name)
		d := step.endTime.Sub(step.startTime)
		duration = cs.Muted(cfg.formatDuration(d))
	}

	durationWidth := CountVisibleWidth(duration)
	maxNameWidth := width - durationWidth - 6
	if maxNameWidth > 0 {
		name = Truncate(name, maxNameWidth)
	}

	buf.WriteString("  ")
	buf.WriteString(icon)
	buf.WriteByte(' ')
	buf.WriteString(name)
	if duration != "" {
		nameWidth := CountVisibleWidth(name)
		pad := width - 4 - nameWidth - durationWidth
		if pad < 2 {
			pad = 2
		}
		buf.WriteString(strings.Repeat(" ", pad))
		buf.WriteString(duration)
	}
	buf.WriteByte('\n')
}

func renderProgressViewport(buf *strings.Builder, cs *iostreams.ColorScheme, cfg *ProgressDisplayConfig, logBuf *ringBuffer, steps []*progressStep, width int) {
	lines := logBuf.Lines()
	totalCount := logBuf.Count()
	logHeight := cfg.logLines()

	// Find the active step's short name for the title.
	title := ""
	for _, step := range steps {
		if step.status == StepRunning {
			title = step.name
			if idx := strings.Index(title, " "); idx > 0 {
				title = title[idx+1:]
			}
			break
		}
	}

	innerWidth := width - 6
	if innerWidth < 20 {
		innerWidth = 20
	}

	// Top border with title.
	topLeft := "  ┌"
	topRight := "┐"
	if title != "" {
		title = Truncate(title, innerWidth-4)
		titleStr := " " + title + " "
		fillLen := innerWidth - CountVisibleWidth(titleStr)
		if fillLen < 0 {
			fillLen = 0
		}
		borderLine := topLeft + titleStr + strings.Repeat("─", fillLen) + topRight
		buf.WriteString(cs.Muted(borderLine))
	} else {
		buf.WriteString(cs.Muted(topLeft + strings.Repeat("─", innerWidth) + topRight))
	}
	buf.WriteByte('\n')

	// Content lines.
	for _, line := range lines {
		line = Truncate(line, innerWidth)
		padLen := innerWidth - CountVisibleWidth(line)
		if padLen < 0 {
			padLen = 0
		}
		buf.WriteString(cs.Muted("  │ "))
		buf.WriteString(line)
		buf.WriteString(strings.Repeat(" ", padLen))
		buf.WriteString(cs.Muted(" │"))
		buf.WriteByte('\n')
	}

	// Pad empty viewport lines.
	for range logHeight - len(lines) {
		buf.WriteString(cs.Muted("  │ "))
		buf.WriteString(strings.Repeat(" ", innerWidth))
		buf.WriteString(cs.Muted(" │"))
		buf.WriteByte('\n')
	}

	// Bottom border with counter.
	counter := ""
	if totalCount > len(lines) {
		counter = fmt.Sprintf(" %d of %d lines ", len(lines), totalCount)
	}
	counterWidth := CountVisibleWidth(counter)
	bottomFillLen := innerWidth - counterWidth
	if bottomFillLen < 0 {
		bottomFillLen = 0
	}
	bottomLine := "  └" + strings.Repeat("─", bottomFillLen) + counter + "┘"
	buf.WriteString(cs.Muted(bottomLine))
	buf.WriteByte('\n')
}

// visibleProgressSteps returns the steps to display in the sliding window and the count
// of hidden completed steps. Internal steps are excluded via the isInternal callback.
func visibleProgressSteps(steps []*progressStep, maxVisible int, isInternal func(string) bool) (visible []*progressStep, hiddenCount int) {
	// Filter out internal steps.
	var filtered []*progressStep
	for _, s := range steps {
		if isInternal != nil && isInternal(s.name) {
			continue
		}
		filtered = append(filtered, s)
	}

	if len(filtered) <= maxVisible {
		return filtered, 0
	}

	// Show the last maxVisible steps, but always include any running step.
	// Count completed steps that will be hidden.
	cutoff := len(filtered) - maxVisible
	for _, s := range filtered[:cutoff] {
		if s.status == StepComplete || s.status == StepCached {
			hiddenCount++
		}
	}

	return filtered[cutoff:], hiddenCount
}

// ---------------------------------------------------------------------------
// TTY mode — BubbleTea
// ---------------------------------------------------------------------------

func runProgressTTY(ios *iostreams.IOStreams, cfg ProgressDisplayConfig, eventCh <-chan ProgressStep) ProgressResult {
	model := newProgressModel(ios, cfg, eventCh)
	finalModel, err := RunProgram(ios, model)
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
	if result := cfg.fireHook("progress", "before_complete"); !result.Continue {
		if result.Err != nil {
			return ProgressResult{Err: result.Err}
		}
		if result.Message != "" {
			return ProgressResult{Err: fmt.Errorf("%s", result.Message)}
		}
		return ProgressResult{}
	}

	renderProgressSummary(ios, &cfg, m.steps, m.startTime)
	return ProgressResult{}
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
	fmt.Fprintf(ios.ErrOut, "%s %s (%s)\n", cs.BrandOrange("━━"), cfg.Title, cfg.Subtitle)

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
	if result := cfg.fireHook("progress", "before_complete"); !result.Continue {
		if result.Err != nil {
			return ProgressResult{Err: result.Err}
		}
		if result.Message != "" {
			return ProgressResult{Err: fmt.Errorf("%s", result.Message)}
		}
		return ProgressResult{}
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
		fmt.Fprintf(ios.ErrOut, "[run]  %s\n", name)
	case StepComplete:
		d := step.endTime.Sub(step.startTime)
		fmt.Fprintf(ios.ErrOut, "[ok]   %s (%s)\n", name, cfg.formatDuration(d))
	case StepCached:
		fmt.Fprintf(ios.ErrOut, "[ok]   %s (cached)\n", name)
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

	summary := fmt.Sprintf("Built %s", cfg.Subtitle)
	if cachedCount > 0 {
		summary += fmt.Sprintf(" (%d/%d cached)", cachedCount, visibleCount)
	}
	fmt.Fprintf(ios.ErrOut, "%s %s %s\n", cs.SuccessIcon(), summary, cs.Muted(cfg.formatDuration(elapsed)))
}

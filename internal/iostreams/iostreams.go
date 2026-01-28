// Package cmdutil provides utilities for command-line applications.
package iostreams

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/briandowns/spinner"
	"github.com/schmitthub/clawker/internal/logger"
	"golang.org/x/term"
)

// IOStreams provides access to standard input/output/error streams.
// It follows the GitHub CLI pattern for testable I/O.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer

	// isInputTTY caches whether stdin is a terminal.
	// -1 = unchecked, 0 = false, 1 = true
	isInputTTY int

	// isOutputTTY caches whether stdout is a terminal.
	isOutputTTY int

	// isStderrTTY caches whether stderr is a terminal.
	isStderrTTY int

	// colorEnabled controls color output.
	// -1 = auto (detect from TTY), 0 = disabled, 1 = enabled
	colorEnabled int

	// terminalTheme is the detected terminal theme: "light", "dark", or "none"
	terminalTheme string

	// Progress indicator state
	progressIndicatorEnabled bool
	progressIndicator        *spinner.Spinner
	progressIndicatorMu      sync.Mutex
	spinnerDisabled          bool

	// Pager state
	pagerCommand string
	pagerWriter  *pagerWriter
	origOut      io.Writer

	// Alternate screen buffer state
	alternateScreenEnabled bool
	alternateScreenActive  bool

	// neverPrompt disables all interactive prompts (e.g., for CI)
	neverPrompt bool

	// Terminal size cache
	termWidthCache  int
	termHeightCache int
	termSizeCached  bool
}

// NewIOStreams creates an IOStreams connected to standard streams.
func NewIOStreams() *IOStreams {
	ios := &IOStreams{
		In:            os.Stdin,
		Out:           os.Stdout,
		ErrOut:        os.Stderr,
		isInputTTY:    -1,
		isOutputTTY:   -1,
		isStderrTTY:   -1,
		colorEnabled:  -1, // Auto-detect
		terminalTheme: "", // Detect on first use
	}

	// Progress enabled when both stdout and stderr are TTYs
	if ios.IsOutputTTY() && ios.IsStderrTTY() {
		ios.progressIndicatorEnabled = true
	}

	// Check for spinner disabled env var
	if os.Getenv("CLAWKER_SPINNER_DISABLED") != "" {
		ios.spinnerDisabled = true
	}

	return ios
}

// IsInputTTY returns true if stdin is a terminal.
func (s *IOStreams) IsInputTTY() bool {
	if s.isInputTTY == -1 {
		if f, ok := s.In.(*os.File); ok {
			s.isInputTTY = boolToInt(term.IsTerminal(int(f.Fd())))
		} else {
			s.isInputTTY = 0
		}
	}
	return s.isInputTTY == 1
}

// IsOutputTTY returns true if stdout is a terminal.
func (s *IOStreams) IsOutputTTY() bool {
	if s.isOutputTTY == -1 {
		if f, ok := s.Out.(*os.File); ok {
			s.isOutputTTY = boolToInt(term.IsTerminal(int(f.Fd())))
		} else {
			s.isOutputTTY = 0
		}
	}
	return s.isOutputTTY == 1
}

// IsInteractive returns true if both stdin and stdout are terminals.
// When false, commands should behave as if --yes was passed (for CI).
func (s *IOStreams) IsInteractive() bool {
	return s.IsInputTTY() && s.IsOutputTTY()
}

// IsStderrTTY returns true if stderr is a terminal.
func (s *IOStreams) IsStderrTTY() bool {
	if s.isStderrTTY == -1 {
		if f, ok := s.ErrOut.(*os.File); ok {
			s.isStderrTTY = boolToInt(term.IsTerminal(int(f.Fd())))
		} else {
			s.isStderrTTY = 0
		}
	}
	return s.isStderrTTY == 1
}

// ColorEnabled returns whether color output is enabled.
// Returns true if:
// - Explicitly enabled via SetColorEnabled(true)
// - Auto-detect mode and stdout is a TTY
func (s *IOStreams) ColorEnabled() bool {
	if s.colorEnabled == -1 {
		// Auto-detect based on TTY
		return s.IsOutputTTY()
	}
	return s.colorEnabled == 1
}

// SetColorEnabled explicitly enables or disables color output.
func (s *IOStreams) SetColorEnabled(enabled bool) {
	s.colorEnabled = boolToInt(enabled)
}

// DetectTerminalTheme attempts to detect the terminal's color theme.
// Sets terminalTheme to "light", "dark", or "none".
func (s *IOStreams) DetectTerminalTheme() {
	if !s.IsOutputTTY() {
		s.terminalTheme = "none"
		return
	}

	// Check common environment variables for theme hints
	colorfgbg := os.Getenv("COLORFGBG")
	if colorfgbg != "" {
		// COLORFGBG format: "fg;bg" or "fg;ignored;bg"
		parts := strings.Split(colorfgbg, ";")
		var bg string
		if len(parts) >= 2 {
			bg = parts[len(parts)-1]
		}
		// 0-6 are "dark" colors, 7-15 are "light" colors (roughly)
		if bg == "0" || bg == "1" || bg == "2" || bg == "3" ||
			bg == "4" || bg == "5" || bg == "6" || bg == "8" {
			s.terminalTheme = "dark"
			return
		}
		if bg == "7" || bg == "15" {
			s.terminalTheme = "light"
			return
		}
	}

	// Check terminal emulator-specific variables
	if os.Getenv("TERM_PROGRAM") == "Apple_Terminal" {
		// Apple Terminal is light by default
		s.terminalTheme = "light"
		return
	}

	// Default to dark theme (most common for developer terminals)
	s.terminalTheme = "dark"
}

// TerminalTheme returns the detected or set terminal theme.
// Returns "light", "dark", or "none".
func (s *IOStreams) TerminalTheme() string {
	if s.terminalTheme == "" {
		s.DetectTerminalTheme()
	}
	return s.terminalTheme
}

// ColorScheme returns a ColorScheme configured for this IOStreams.
func (s *IOStreams) ColorScheme() *ColorScheme {
	return NewColorScheme(s.ColorEnabled(), s.TerminalTheme())
}

// TerminalWidth returns the width of the terminal in columns.
// Returns 80 as a default if detection fails.
func (s *IOStreams) TerminalWidth() int {
	w, _ := s.TerminalSize()
	return w
}

// TerminalSize returns the width and height of the terminal.
// Returns (80, 24) as defaults if detection fails.
func (s *IOStreams) TerminalSize() (width, height int) {
	if s.termSizeCached {
		return s.termWidthCache, s.termHeightCache
	}

	// Default fallback values
	width, height = 80, 24

	// Try to get size from stdout
	if f, ok := s.Out.(*os.File); ok {
		w, h, err := term.GetSize(int(f.Fd()))
		if err == nil && w > 0 && h > 0 {
			width, height = w, h
		}
	}

	// Try stdin as fallback
	if width == 80 && height == 24 {
		if f, ok := s.In.(*os.File); ok {
			w, h, err := term.GetSize(int(f.Fd()))
			if err == nil && w > 0 && h > 0 {
				width, height = w, h
			}
		}
	}

	s.termWidthCache = width
	s.termHeightCache = height
	s.termSizeCached = true

	return width, height
}

// InvalidateTerminalSizeCache clears the cached terminal size.
// Call this after a window resize event.
func (s *IOStreams) InvalidateTerminalSizeCache() {
	s.termSizeCached = false
}

// StartProgressIndicator starts a spinner on stderr.
func (s *IOStreams) StartProgressIndicator() {
	s.StartProgressIndicatorWithLabel("")
}

// StartProgressIndicatorWithLabel starts a spinner with a label on stderr.
func (s *IOStreams) StartProgressIndicatorWithLabel(label string) {
	if !s.progressIndicatorEnabled {
		return
	}

	s.progressIndicatorMu.Lock()
	defer s.progressIndicatorMu.Unlock()

	// Check spinnerDisabled inside mutex for thread safety
	if s.spinnerDisabled {
		s.startTextualProgressIndicatorLocked(label)
		return
	}

	// If spinner already running, just update the prefix
	if s.progressIndicator != nil {
		if label == "" {
			s.progressIndicator.Prefix = ""
		} else {
			s.progressIndicator.Prefix = label + " "
		}
		return
	}

	// Create new spinner
	// CharSets[11] is braille: ⣾ ⣷ ⣽ ⣻ ⡿
	// Note: spinner.WithColor silently ignores invalid colors per library design.
	// "fgCyan" is a verified valid color (see TestProgressIndicator_ColorIsValid).
	sp := spinner.New(spinner.CharSets[11], 120*time.Millisecond,
		spinner.WithWriter(s.ErrOut),
		spinner.WithColor("fgCyan"))
	if label != "" {
		sp.Prefix = label + " "
	}

	sp.Start()
	s.progressIndicator = sp
}

// startTextualProgressIndicatorLocked prints a one-time text message instead of animated spinner.
// Caller must hold progressIndicatorMu.
func (s *IOStreams) startTextualProgressIndicatorLocked(label string) {
	// Default label when spinner disabled
	if label == "" {
		label = "Working..."
	}

	// Add ellipsis if not present
	if !strings.HasSuffix(label, "...") {
		label = label + "..."
	}

	fmt.Fprintf(s.ErrOut, "%s\n", s.ColorScheme().Cyan(label))
}

// StopProgressIndicator stops the spinner.
func (s *IOStreams) StopProgressIndicator() {
	s.progressIndicatorMu.Lock()
	defer s.progressIndicatorMu.Unlock()

	if s.progressIndicator == nil {
		return
	}

	s.progressIndicator.Stop()
	s.progressIndicator = nil
}

// GetSpinnerDisabled returns whether the animated spinner is disabled.
func (s *IOStreams) GetSpinnerDisabled() bool {
	return s.spinnerDisabled
}

// SetSpinnerDisabled sets whether the animated spinner is disabled.
func (s *IOStreams) SetSpinnerDisabled(v bool) {
	s.spinnerDisabled = v
}

// RunWithProgress runs a function while showing a spinner.
// The spinner is automatically stopped when the function returns.
func (s *IOStreams) RunWithProgress(label string, fn func() error) error {
	s.StartProgressIndicatorWithLabel(label)
	defer s.StopProgressIndicator()
	return fn()
}

// SetPager sets the pager command to use for output.
// If empty, paging is disabled.
func (s *IOStreams) SetPager(cmd string) {
	s.pagerCommand = cmd
}

// GetPager returns the configured pager command.
// Returns the effective pager (from env vars if not explicitly set).
func (s *IOStreams) GetPager() string {
	if s.pagerCommand != "" {
		return s.pagerCommand
	}
	return getPagerCommand()
}

// StartPager starts piping stdout through a pager.
// If stdout is not a TTY, this is a no-op.
func (s *IOStreams) StartPager() error {
	if !s.IsOutputTTY() {
		return nil
	}

	pagerCmd := s.GetPager()
	if pagerCmd == "" {
		return nil
	}

	pw, err := newPagerWriter(pagerCmd, s.Out)
	if err != nil {
		return err
	}
	if pw == nil {
		return nil
	}

	s.origOut = s.Out
	s.pagerWriter = pw
	s.Out = pw

	return nil
}

// StopPager stops the pager and restores the original stdout.
func (s *IOStreams) StopPager() {
	if s.pagerWriter == nil {
		return
	}

	if err := s.pagerWriter.Close(); err != nil {
		// Log but don't fail - pager errors are non-fatal
		// Ignore "broken pipe" - expected if user quit pager early
		if !strings.Contains(err.Error(), "broken pipe") {
			logger.Debug().Err(err).Msg("pager close error")
		}
	}
	s.Out = s.origOut
	s.pagerWriter = nil
	s.origOut = nil
}

// SetAlternateScreenBufferEnabled enables or disables alternate screen buffer usage.
func (s *IOStreams) SetAlternateScreenBufferEnabled(enabled bool) {
	s.alternateScreenEnabled = enabled
}

// StartAlternateScreenBuffer switches to the terminal's alternate screen buffer.
// This is commonly used for full-screen terminal applications.
func (s *IOStreams) StartAlternateScreenBuffer() {
	if !s.alternateScreenEnabled || !s.IsOutputTTY() {
		return
	}
	if s.alternateScreenActive {
		return
	}

	// ANSI escape sequence to switch to alternate screen and hide cursor
	fmt.Fprint(s.Out, "\x1b[?1049h\x1b[?25l")
	s.alternateScreenActive = true
}

// StopAlternateScreenBuffer switches back to the main screen buffer.
func (s *IOStreams) StopAlternateScreenBuffer() {
	if !s.alternateScreenActive {
		return
	}

	// ANSI escape sequence to switch back to main screen, show cursor, and reset
	fmt.Fprint(s.Out, "\x1b[?1049l\x1b[?25h\x1b[0m\x1b(B")
	s.alternateScreenActive = false
}

// RefreshScreen clears the screen and moves cursor to home position.
func (s *IOStreams) RefreshScreen() {
	if !s.IsOutputTTY() {
		return
	}
	// Clear screen and move cursor to home
	fmt.Fprint(s.Out, "\x1b[2J\x1b[H")
}

// CanPrompt returns whether interactive prompts should be shown.
// Returns false if stdin/stdout are not TTYs, or if NeverPrompt is set.
func (s *IOStreams) CanPrompt() bool {
	if s.neverPrompt {
		return false
	}
	return s.IsInteractive()
}

// SetNeverPrompt disables all interactive prompts.
// Useful for CI environments or scripted usage.
func (s *IOStreams) SetNeverPrompt(never bool) {
	s.neverPrompt = never
}

// GetNeverPrompt returns whether prompts are disabled.
func (s *IOStreams) GetNeverPrompt() bool {
	return s.neverPrompt
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestIOStreams creates IOStreams for testing with bytes.Buffer.
// Returns the streams and separate buffers for stdout and stderr.
type TestIOStreams struct {
	*IOStreams
	InBuf  *testBuffer
	OutBuf *testBuffer
	ErrBuf *testBuffer
}

// testBuffer wraps a byte slice for use in tests.
type testBuffer struct {
	data []byte
}

func (b *testBuffer) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func (b *testBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *testBuffer) String() string {
	return string(b.data)
}

func (b *testBuffer) Reset() {
	b.data = nil
}

// SetInput sets the input data for the test buffer.
func (b *testBuffer) SetInput(s string) {
	b.data = []byte(s)
}

// NewTestIOStreams creates IOStreams for testing.
func NewTestIOStreams() *TestIOStreams {
	in := &testBuffer{}
	out := &testBuffer{}
	errOut := &testBuffer{}

	return &TestIOStreams{
		IOStreams: &IOStreams{
			In:           in,
			Out:          out,
			ErrOut:       errOut,
			isInputTTY:   0, // Tests are non-interactive by default
			isOutputTTY:  0,
			isStderrTTY:  0,
			colorEnabled: 0, // Colors disabled in tests by default
		},
		InBuf:  in,
		OutBuf: out,
		ErrBuf: errOut,
	}
}

// SetInteractive allows tests to simulate interactive mode.
func (t *TestIOStreams) SetInteractive(interactive bool) {
	if interactive {
		t.IOStreams.isInputTTY = 1
		t.IOStreams.isOutputTTY = 1
		t.IOStreams.isStderrTTY = 1
	} else {
		t.IOStreams.isInputTTY = 0
		t.IOStreams.isOutputTTY = 0
		t.IOStreams.isStderrTTY = 0
	}
}

// SetColorEnabled allows tests to control color output.
func (t *TestIOStreams) SetColorEnabled(enabled bool) {
	t.IOStreams.SetColorEnabled(enabled)
}

// SetTerminalSize allows tests to simulate terminal size.
func (t *TestIOStreams) SetTerminalSize(width, height int) {
	t.IOStreams.termWidthCache = width
	t.IOStreams.termHeightCache = height
	t.IOStreams.termSizeCached = true
}

// SetProgressEnabled allows tests to enable/disable progress indicator.
func (t *TestIOStreams) SetProgressEnabled(enabled bool) {
	t.IOStreams.progressIndicatorEnabled = enabled
}

// SetSpinnerDisabled allows tests to disable the animated spinner.
func (t *TestIOStreams) SetSpinnerDisabled(disabled bool) {
	t.IOStreams.spinnerDisabled = disabled
}

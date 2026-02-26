// Big credit to the GitHub CLI project for the IOStreams pattern and Factory design.
package iostreams

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"

	"github.com/google/shlex"
	"github.com/mattn/go-colorable"
	interm "github.com/schmitthub/clawker/internal/term"
	termocks "github.com/schmitthub/clawker/internal/term/mocks"
)

const DefaultWidth = 80

type fileWriter interface {
	io.Writer
	Fd() uintptr
}

type fileReader interface {
	io.ReadCloser
	Fd() uintptr
}

// term describes terminal capabilities. Unexported — commands access
// terminal info through IOStreams methods, never directly.
type term interface {
	IsTTY() bool
	IsColorEnabled() bool
	Is256ColorSupported() bool
	IsTrueColorSupported() bool
	Size() (int, int, error)
}

// IOStreams provides access to standard input/output/error streams.
// It follows the GitHub CLI pattern for testable I/O.
type IOStreams struct {
	In     fileReader
	Out    fileWriter
	ErrOut fileWriter
	term   term

	alternateScreenBufferEnabled bool
	alternateScreenBufferActive  bool
	alternateScreenBufferMu      sync.Mutex

	colorOverride bool

	// isInputTTY caches whether stdin is a terminal.
	isInputTTY bool

	// isStdoutTTY caches whether stdout is a terminal.
	isStdoutTTY bool

	// isStderrTTY caches whether stderr is a terminal.
	isStderrTTY bool

	// colorEnabled controls color output.
	colorEnabled bool

	// terminalTheme is the detected terminal theme: "light", "dark", or "none"
	terminalTheme string

	// Spinner state
	progressIndicatorEnabled bool
	activeSpinner            *spinnerRunner
	spinnerMu                sync.Mutex
	spinnerDisabled          bool

	// Pager state
	pagerCommand string
	pagerProcess *os.Process

	// neverPrompt disables all interactive prompts (e.g., for CI)
	neverPrompt bool
}

// System creates an IOStreams wired to the real system terminal.
// Reads terminal capabilities from the host environment via term.FromEnv().
// The factory calls this, then may layer clawker config overrides.
func System() *IOStreams {

	terminal := interm.FromEnv()

	var stdout fileWriter = os.Stdout
	if colorableStdout := colorable.NewColorable(os.Stdout); colorableStdout != os.Stdout {
		// Ensure that the file descriptor of the original stdout is preserved.
		stdout = &fdWriter{
			fd:     os.Stdout.Fd(),
			Writer: colorableStdout,
		}
	}

	var stderr fileWriter = os.Stderr
	// On Windows with no virtual terminal processing support, translate ANSI escape
	// sequences to console syscalls.
	if colorableStderr := colorable.NewColorable(os.Stderr); colorableStderr != os.Stderr {
		// Ensure that the file descriptor of the original stderr is preserved.
		stderr = &fdWriter{
			fd:     os.Stderr.Fd(),
			Writer: colorableStderr,
		}
	}

	io := &IOStreams{
		In:           os.Stdin,
		Out:          stdout,
		ErrOut:       stderr,
		pagerCommand: os.Getenv("PAGER"),
		term:         &terminal,
	}

	stdoutIsTTY := io.IsStdoutTTY()
	stderrIsTTY := io.IsStderrTTY()

	// Progress indicator requires both stdout and stderr TTY
	if stdoutIsTTY && stderrIsTTY {
		io.progressIndicatorEnabled = true
	}

	if stdoutIsTTY && hasAlternateScreenBuffer(terminal.IsTrueColorSupported()) {
		io.alternateScreenBufferEnabled = true
	}

	// Detect terminal theme for color scheme selection
	if io.IsStdoutTTY() {
		io.DetectTerminalTheme()
	}

	return io
}

func Test() (*IOStreams, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	io := &IOStreams{
		In: &fdReader{
			fd:         0,
			ReadCloser: io.NopCloser(in),
		},
		Out:    &fdWriter{fd: 1, Writer: out},
		ErrOut: &fdWriter{fd: 2, Writer: errOut},
		term:   &termocks.FakeTerm{},
	}
	io.SetStdinTTY(false)
	io.SetStdoutTTY(false)
	io.SetStderrTTY(false)
	return io, in, out, errOut
}

func (s *IOStreams) StartAlternateScreenBuffer() {
	if s.alternateScreenBufferEnabled {
		s.alternateScreenBufferMu.Lock()
		defer s.alternateScreenBufferMu.Unlock()

		if _, err := fmt.Fprint(s.Out, "\x1b[?1049h"); err == nil {
			s.alternateScreenBufferActive = true

			ch := make(chan os.Signal, 1)
			signal.Notify(ch, os.Interrupt)

			go func() {
				<-ch
				s.StopAlternateScreenBuffer()

				os.Exit(1)
			}()
		}
	}
}

func (s *IOStreams) StopAlternateScreenBuffer() {
	s.alternateScreenBufferMu.Lock()
	defer s.alternateScreenBufferMu.Unlock()

	if s.alternateScreenBufferActive {
		fmt.Fprint(s.Out, "\x1b[?1049l")
		s.alternateScreenBufferActive = false
	}
}

func (s *IOStreams) RefreshScreen() {
	if s.IsStdoutTTY() {
		// Move cursor to 0,0
		fmt.Fprint(s.Out, "\x1b[0;0H")
		// Clear from cursor to bottom of screen
		fmt.Fprint(s.Out, "\x1b[J")
	}
}

// TerminalWidth returns the width of the terminal that controls the process
func (s *IOStreams) TerminalWidth() int {
	w, _, err := s.term.Size()
	if err == nil && w > 0 {
		return w
	}
	return DefaultWidth
}

// IsInputTTY returns true if stdin is a terminal.
func (s *IOStreams) IsInputTTY() bool {
	if !s.isInputTTY {
		if f, ok := s.In.(*os.File); ok {
			s.isInputTTY = interm.IsTerminalFd(int(f.Fd()))
		} else {
			s.isInputTTY = false
		}
	}
	return s.isInputTTY
}

// IsStdoutTTY returns true if stdout is a terminal.
func (s *IOStreams) IsStdoutTTY() bool {
	if !s.isStdoutTTY {
		if f, ok := s.Out.(*os.File); ok {
			s.isStdoutTTY = interm.IsTerminalFd(int(f.Fd()))
		} else {
			s.isStdoutTTY = false
		}
	}
	return s.isStdoutTTY
}

// IsInteractive returns true if both stdin and stdout are terminals.
// When false, commands should behave as if --yes was passed (for CI).
func (s *IOStreams) IsInteractive() bool {
	return s.IsInputTTY() && s.IsStdoutTTY()
}

// IsStderrTTY returns true if stderr is a terminal.
func (s *IOStreams) IsStderrTTY() bool {
	if !s.isStderrTTY {
		if f, ok := s.ErrOut.(*os.File); ok {
			s.isStderrTTY = interm.IsTerminalFd(int(f.Fd()))
		} else {
			s.isStderrTTY = false
		}
	}
	return s.isStderrTTY
}

// ColorEnabled returns whether color output is enabled.
// Returns true if:
// - Explicitly enabled via SetColorEnabled(true)
// - Auto-detect mode and stdout is a TTY
func (s *IOStreams) ColorEnabled() bool {
	if s.colorOverride {
		return s.colorEnabled
	}
	return s.term.IsColorEnabled()
}

// SetColorEnabled explicitly enables or disables color output.
func (s *IOStreams) SetColorEnabled(enabled bool) {
	s.colorOverride = true
	s.colorEnabled = enabled
}

// Is256ColorSupported returns whether the host terminal supports 256 colors.
func (s *IOStreams) Is256ColorSupported() bool {
	return s.term != nil && s.term.Is256ColorSupported()
}

// IsTrueColorSupported returns whether the host terminal supports 24-bit truecolor.
func (s *IOStreams) IsTrueColorSupported() bool {
	return s.term != nil && s.term.IsTrueColorSupported()
}

// DetectTerminalTheme attempts to detect the terminal's color theme.
// Sets terminalTheme to "light", "dark", or "none".
func (s *IOStreams) DetectTerminalTheme() {
	if !s.IsStdoutTTY() {
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

// StartProgressIndicator starts a spinner on stderr.
//
// Deprecated: Use StartSpinner instead.
func (s *IOStreams) StartProgressIndicator() {
	s.StartSpinner("")
}

// StartProgressIndicatorWithLabel starts a spinner with a label on stderr.
//
// Deprecated: Use StartSpinner instead.
func (s *IOStreams) StartProgressIndicatorWithLabel(label string) {
	s.StartSpinner(label)
}

// StopProgressIndicator stops the spinner.
//
// Deprecated: Use StopSpinner instead.
func (s *IOStreams) StopProgressIndicator() {
	s.StopSpinner()
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
//
// Deprecated: Use RunWithSpinner instead.
func (s *IOStreams) RunWithProgress(label string, fn func() error) error {
	return s.RunWithSpinner(label, fn)
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

func (s *IOStreams) StartPager() error {
	if s.pagerCommand == "" || s.pagerCommand == "cat" || !s.IsStdoutTTY() {
		return nil
	}

	pagerArgs, err := shlex.Split(s.pagerCommand)
	if err != nil {
		return err
	}

	pagerEnv := os.Environ()
	for i := len(pagerEnv) - 1; i >= 0; i-- {
		if strings.HasPrefix(pagerEnv[i], "PAGER=") {
			pagerEnv = append(pagerEnv[0:i], pagerEnv[i+1:]...)
		}
	}
	if _, ok := os.LookupEnv("LESS"); !ok {
		pagerEnv = append(pagerEnv, "LESS=FRX")
	}
	if _, ok := os.LookupEnv("LV"); !ok {
		pagerEnv = append(pagerEnv, "LV=-c")
	}

	pagerExe, err := exec.LookPath(pagerArgs[0])
	if err != nil {
		return err
	}
	pagerCmd := exec.Command(pagerExe, pagerArgs[1:]...)
	pagerCmd.Env = pagerEnv
	pagerCmd.Stdout = s.Out
	pagerCmd.Stderr = s.ErrOut
	pagedOut, err := pagerCmd.StdinPipe()
	if err != nil {
		return err
	}
	s.Out = &fdWriteCloser{
		fd:          s.Out.Fd(),
		WriteCloser: &pagerWriter{pagedOut},
	}
	err = pagerCmd.Start()
	if err != nil {
		return err
	}
	s.pagerProcess = pagerCmd.Process
	return nil
}

func (s *IOStreams) StopPager() {
	if s.pagerProcess == nil {
		return
	}

	// if a pager was started, we're guaranteed to have a WriteCloser
	_ = s.Out.(io.WriteCloser).Close()
	_, _ = s.pagerProcess.Wait()
	s.pagerProcess = nil
}

// SetAlternateScreenBufferEnabled enables or disables alternate screen buffer usage.
func (s *IOStreams) SetAlternateScreenBufferEnabled(enabled bool) {
	s.alternateScreenBufferEnabled = enabled
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

// SetStdinTTY sets whether stdin is a terminal (for testing).
func (s *IOStreams) SetStdinTTY(isTTY bool) { s.isInputTTY = isTTY }

// SetStdoutTTY sets whether stdout is a terminal (for testing).
func (s *IOStreams) SetStdoutTTY(isTTY bool) { s.isStdoutTTY = isTTY }

// SetStderrTTY sets whether stderr is a terminal (for testing).
func (s *IOStreams) SetStderrTTY(isTTY bool) { s.isStderrTTY = isTTY }

// SetProgressIndicatorEnabled enables or disables the progress indicator.
func (s *IOStreams) SetProgressIndicatorEnabled(enabled bool) {
	s.progressIndicatorEnabled = enabled
}

// fdReader represents a wrapped stdin ReadCloser that preserves the original file descriptor
type fdReader struct {
	io.ReadCloser
	fd uintptr
}

func (r *fdReader) Fd() uintptr {
	return r.fd
}

// fdWriter represents a wrapped stdout Writer that preserves the original file descriptor
type fdWriter struct {
	io.Writer
	fd uintptr
}

func (w *fdWriter) Fd() uintptr {
	return w.fd
}

// fdWriteCloser represents a wrapped stdout Writer that preserves the original file descriptor
type fdWriteCloser struct {
	io.WriteCloser
	fd uintptr
}

func (w *fdWriteCloser) Fd() uintptr {
	return w.fd
}

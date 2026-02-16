package iostreams

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// SpinnerType defines the visual style of the spinner animation.
type SpinnerType int

const (
	SpinnerBraille SpinnerType = iota // ⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏
	SpinnerDots                       // ⣾⣽⣻⢿⡿⣟⣯⣷
	SpinnerLine                       // -\|/
	SpinnerPulse                      // ●○
	SpinnerGlobe                      // 🌍🌎🌏
	SpinnerMoon                       // 🌑🌒🌓🌔🌕🌖🌗🌘
)

// SpinnerFrames returns the frame characters for a given spinner type.
func SpinnerFrames(t SpinnerType) []string {
	switch t {
	case SpinnerBraille:
		return []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	case SpinnerDots:
		return []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}
	case SpinnerLine:
		return []string{"-", "\\", "|", "/"}
	case SpinnerPulse:
		return []string{"●", "○"}
	case SpinnerGlobe:
		return []string{"🌍", "🌎", "🌏"}
	case SpinnerMoon:
		return []string{"🌑", "🌒", "🌓", "🌔", "🌕", "🌖", "🌗", "🌘"}
	default:
		// Fall back to braille for unknown types
		return []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	}
}

// SpinnerFrame returns the rendered frame string for a given type, tick, label, and color scheme.
// This is a pure function with no side effects, used by the iostreams goroutine spinner.
// The tui SpinnerModel uses bubbles/spinner directly but maintains visual consistency
// through shared CyanStyle.
func SpinnerFrame(t SpinnerType, tick int, label string, cs *ColorScheme) string {
	frames := SpinnerFrames(t)
	frame := frames[tick%len(frames)]

	// Apply color to the spinner character
	styledFrame := cs.Cyan(frame)

	if label == "" {
		return styledFrame
	}
	return styledFrame + " " + label
}

// spinnerRunner manages an animated spinner goroutine.
type spinnerRunner struct {
	spinnerType SpinnerType
	label       string
	cs          *ColorScheme
	writer      io.Writer
	done        chan struct{}
	stopped     chan struct{} // closed when goroutine exits
	tick        int
	mu          sync.Mutex
	stopOnce    sync.Once
}

func newSpinnerRunner(t SpinnerType, label string, cs *ColorScheme, writer io.Writer) *spinnerRunner {
	return &spinnerRunner{
		spinnerType: t,
		label:       label,
		cs:          cs,
		writer:      writer,
		done:        make(chan struct{}),
		stopped:     make(chan struct{}),
	}
}

// Start begins the spinner animation in a background goroutine.
func (r *spinnerRunner) Start() {
	go func() {
		defer close(r.stopped)
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-r.done:
				return
			case <-ticker.C:
				r.mu.Lock()
				frame := SpinnerFrame(r.spinnerType, r.tick, r.label, r.cs)
				r.tick++
				r.mu.Unlock()

				// \r to overwrite current line, clear to end of line.
				// Exit on write error (terminal disconnected, pipe closed)
				// to avoid a hot error loop.
				if _, err := fmt.Fprintf(r.writer, "\r\033[K%s", frame); err != nil {
					return
				}
			}
		}
	}()
}

// Stop halts the spinner animation and clears the line.
// Safe to call multiple times — uses sync.Once internally.
func (r *spinnerRunner) Stop() {
	r.stopOnce.Do(func() {
		close(r.done)
		<-r.stopped // wait for goroutine to exit before clearing
		fmt.Fprintf(r.writer, "\r\033[K")
	})
}

// SetLabel updates the spinner label while it's running.
func (r *spinnerRunner) SetLabel(label string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.label = label
}

// StartSpinner starts an animated spinner on stderr with the default braille style.
// If label is empty, uses a default "Working..." label in text fallback mode.
// Does nothing if progress indicators are disabled (non-TTY environment).
func (s *IOStreams) StartSpinner(label string) {
	s.StartSpinnerWithType(SpinnerBraille, label)
}

// StartSpinnerWithType starts an animated spinner with the specified style.
// In animated mode (TTY), calling again while a spinner is running updates the label.
// In text fallback mode (non-TTY or spinnerDisabled), each call prints a new status line.
func (s *IOStreams) StartSpinnerWithType(t SpinnerType, label string) {
	if !s.progressIndicatorEnabled {
		return
	}

	s.spinnerMu.Lock()
	defer s.spinnerMu.Unlock()

	if s.spinnerDisabled {
		s.startTextualSpinnerLocked(label)
		return
	}

	// If spinner already running, just update the label
	if s.activeSpinner != nil {
		s.activeSpinner.SetLabel(label)
		return
	}

	sp := newSpinnerRunner(t, label, s.ColorScheme(), s.ErrOut)
	sp.Start()
	s.activeSpinner = sp
}

// startTextualSpinnerLocked prints a one-time text message instead of an animated spinner.
// Caller must hold spinnerMu.
func (s *IOStreams) startTextualSpinnerLocked(label string) {
	if label == "" {
		label = "Working..."
	}

	if !strings.HasSuffix(label, "...") {
		label = label + "..."
	}

	fmt.Fprintf(s.ErrOut, "%s\n", s.ColorScheme().Cyan(label))
}

// StopSpinner stops the active spinner animation and clears the line.
// Safe to call even if no spinner is running.
func (s *IOStreams) StopSpinner() {
	s.spinnerMu.Lock()
	defer s.spinnerMu.Unlock()

	if s.activeSpinner == nil {
		return
	}

	s.activeSpinner.Stop()
	s.activeSpinner = nil
}

// RunWithSpinner runs a function while showing a spinner.
// The spinner is automatically stopped when the function returns.
func (s *IOStreams) RunWithSpinner(label string, fn func() error) error {
	s.StartSpinner(label)
	defer s.StopSpinner()
	return fn()
}

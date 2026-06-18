package docker

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/signals"
	"github.com/schmitthub/clawker/internal/term"
)

// visualResetSequence undoes the terminal input/visual modes an in-container TUI
// (Claude Code, vim, htop, …) turns on but never gets to turn off when the session
// ends abruptly — a Ctrl-P+Q detach (the container keeps running) or a kill. Every
// sequence here is an idempotent DECRST/SGR reset: none destroys on-screen content
// or moves the cursor, so Restore emits them unconditionally. Disabling a mode that
// was never enabled is a no-op, so we do not track which the container set.
//   - \x1b[?25h   : Show cursor
//   - \x1b[?1000l : Disable mouse button-event tracking
//   - \x1b[?1002l : Disable mouse cell-motion tracking
//   - \x1b[?1003l : Disable mouse all-motion tracking
//   - \x1b[?1006l : Disable SGR (1006) mouse encoding
//   - \x1b[?1004l : Disable focus in/out reporting
//   - \x1b[?2004l : Disable bracketed paste
//   - \x1b[0m     : Reset text attributes
//   - \x1b(B      : Select ASCII character set
//
// Without the mouse resets, a detached session leaves the host terminal echoing raw
// SGR mouse reports (e.g. "<35;95;29M") on every pointer move.
const visualResetSequence = "\x1b[?25h" +
	"\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l" +
	"\x1b[?1004l" +
	"\x1b[?2004l" +
	"\x1b[0m\x1b(B"

// altScreenLeaveSeq leaves the alternate screen buffer. restoreSequence prepends it
// ONLY when the container left us in the alt buffer (see PTYHandler.containerInAltScreen)
// — a blind leave issues a DECRC cursor-restore that yanks the cursor to a stale
// saved position and squashes plain primary-screen output (init progress, command
// results) the moment the session exits. This is the one reset that is NOT idempotent,
// which is why it alone is gated on tracked state.
const altScreenLeaveSeq = "\x1b[?1049l"

// restoreSequence is the full terminal reset Restore writes: visualResetSequence
// always, with altScreenLeaveSeq prepended only when the container left us in the
// alternate buffer. See those consts for why each is or isn't gated.
func restoreSequence(containerInAltScreen bool) string {
	if containerInAltScreen {
		return altScreenLeaveSeq + visualResetSequence
	}
	return visualResetSequence
}

// PTYHandler manages the pseudo-terminal connection to a container
type PTYHandler struct {
	stdin   *os.File
	stdout  *os.File
	stderr  *os.File
	log     *logger.Logger
	rawMode *term.RawMode

	// containerInAltScreen records whether the container's output stream left
	// the terminal in the alternate screen buffer — an alt-screen enter with no
	// matching leave (e.g. an in-container TUI like Claude Code killed before it
	// could emit its own leave). Set by the output-copy scanner, read by Restore.
	// atomic because the scanner runs in the output goroutine while Restore runs
	// on the caller's goroutine.
	containerInAltScreen atomic.Bool

	// mu protects concurrent access
	mu sync.Mutex
}

// NewPTYHandler creates a new PTY handler.
// When log is nil, a Nop logger is used.
func NewPTYHandler(log *logger.Logger) *PTYHandler {
	if log == nil {
		log = logger.Nop()
	}
	return &PTYHandler{
		stdin:   os.Stdin,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		log:     log,
		rawMode: term.NewRawModeStdin(),
	}
}

// Setup prepares the terminal for PTY interaction
func (p *PTYHandler) Setup() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.rawMode.IsTerminal() {
		p.log.Debug().Msg("stdin is not a terminal, skipping raw mode")
		return nil
	}

	if err := p.rawMode.Enable(); err != nil {
		return err
	}

	p.log.Debug().Msg("terminal set to raw mode")
	return nil
}

// Restore returns the terminal to its original state.
// It first resets the terminal visual state (alternate screen, cursor visibility,
// text attributes) before restoring termios settings.
func (p *PTYHandler) Restore() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Reset visual state BEFORE restoring termios
	p.resetVisualStateUnlocked()

	if err := p.rawMode.Restore(); err != nil {
		return err
	}

	p.log.Debug().Msg("terminal restored")
	return nil
}

// resetVisualStateUnlocked sends reset sequences without locking.
// Must be called with p.mu held.
func (p *PTYHandler) resetVisualStateUnlocked() {
	if !p.rawMode.IsTerminal() {
		return
	}
	// Only force-leave the alternate screen buffer when the container actually
	// left us in it. Emitting the leave blindly squashes plain primary-screen
	// output on exit (the DECRC cursor-restore that ?1049l performs).
	seq := restoreSequence(p.containerInAltScreen.Load())
	if _, err := p.stdout.WriteString(seq); err != nil {
		p.log.Warn().Err(err).Msg("failed to write terminal reset sequence")
	}
}

// Stream handles bidirectional I/O between local terminal and container
func (p *PTYHandler) Stream(ctx context.Context, hijacked HijackedResponse) error {
	// NOTE: caller owns hijacked.Close() — do not close here to avoid double-close.

	outputDone := make(chan struct{})
	errCh := make(chan error, 2)

	// Copy container output to stdout
	go func() {
		_, err := io.Copy(newAltScreenTrackingWriter(p.stdout, &p.containerInAltScreen), hijacked.Reader)
		if err != nil && err != io.EOF && !isClosedConnectionError(err) {
			errCh <- err
		}
		close(outputDone)
	}()

	// Copy stdin to container input
	go func() {
		_, err := io.Copy(hijacked.Conn, p.stdin)
		if err != nil && err != io.EOF && !isClosedConnectionError(err) {
			errCh <- err
		}
		// Close write side when stdin is done
		hijacked.CloseWrite()
	}()

	// Wait for context cancellation, error, or output completion
	// NOTE: We don't wait for stdin copy because it may be blocked on stdin.Read()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-outputDone:
		// Output is complete, container has exited
		return nil
	}
}

// StreamWithResize handles bidirectional I/O with terminal resize support
func (p *PTYHandler) StreamWithResize(
	ctx context.Context,
	hijacked HijackedResponse,
	resizeFunc func(height, width uint) error,
) error {
	// NOTE: caller owns hijacked.Close() — do not close here to avoid double-close.

	outputDone := make(chan struct{})
	errCh := make(chan error, 2)

	// Initial resize and set up SIGWINCH monitoring
	if p.rawMode.IsTerminal() {
		width, height, err := p.rawMode.GetSize()
		if err != nil {
			p.log.Debug().Err(err).Msg("failed to get initial terminal size")
		} else if resizeFunc != nil {
			// Docker CLI's +1/-1 trick: resize to artificial size first, then actual
			// This forces a size change event to trigger TUI redraw on re-attach
			// See: docker/cli/cli/command/container/attach.go resizeTTY()
			if err := resizeFunc(uint(height+1), uint(width+1)); err != nil {
				p.log.Debug().Err(err).Msg("failed to set artificial container TTY size")
			}
			if err := resizeFunc(uint(height), uint(width)); err != nil {
				p.log.Debug().Err(err).Msg("failed to set actual container TTY size")
			}
		}

		// Start monitoring for window resize events (SIGWINCH)
		resizeHandler := signals.NewResizeHandler(resizeFunc, p.GetSize)
		resizeHandler.Start()
		defer resizeHandler.Stop()
	}

	// Copy container output to stdout
	go func() {
		_, err := io.Copy(newAltScreenTrackingWriter(p.stdout, &p.containerInAltScreen), hijacked.Reader)
		if err != nil && err != io.EOF && !isClosedConnectionError(err) {
			p.log.Debug().Err(err).Msg("error copying container output")
			errCh <- err
		}
		close(outputDone)
	}()

	// Copy stdin to container input
	go func() {
		_, err := io.Copy(hijacked.Conn, p.stdin)
		if err != nil && err != io.EOF && !isClosedConnectionError(err) {
			p.log.Debug().Err(err).Msg("error copying stdin to container")
			errCh <- err
		}
		hijacked.CloseWrite()
	}()

	// Wait for context cancellation, error, or output completion
	// NOTE: We don't wait for stdin copy because it may be blocked on stdin.Read()
	// In raw mode, Ctrl+C doesn't generate SIGINT - it's passed to the container.
	// When the container exits, output closes but stdin may still be blocked.
	// Returning immediately when output is done allows proper terminal restoration.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-outputDone:
		// Output is complete, container has exited
		// Don't wait for stdin - it may be blocked on Read()
		return nil
	}
}

// GetSize returns the current terminal size
func (p *PTYHandler) GetSize() (width, height int, err error) {
	return p.rawMode.GetSize()
}

// IsTerminal returns true if stdin is a terminal
func (p *PTYHandler) IsTerminal() bool {
	return p.rawMode.IsTerminal()
}

// isClosedConnectionError checks if the error is a "use of closed network connection" error.
// This happens when the hijacked Docker socket connection is closed (e.g., container exit)
// while a goroutine is still reading from it. This is expected behavior, not an error.
func isClosedConnectionError(err error) bool {
	if err == nil {
		return false
	}
	// NOTE: net.ErrClosed is not always wrapped by the hijacked connection, so
	// we fall back to string matching. This is fragile but matches the Go stdlib
	// internal error string from net/fd_posix.go ("use of closed network connection").
	return strings.Contains(err.Error(), "use of closed network connection")
}

// alt-screen DEC private-mode sequences. Entering any of these switches the
// terminal to the alternate screen buffer; the matching `l` form leaves it.
// Modern TUIs use 1049; 1047/47 are older variants tracked for completeness.
var (
	altScreenEnterSeqs = [][]byte{[]byte("\x1b[?1049h"), []byte("\x1b[?1047h"), []byte("\x1b[?47h")}
	altScreenLeaveSeqs = [][]byte{[]byte("\x1b[?1049l"), []byte("\x1b[?1047l"), []byte("\x1b[?47l")}
)

// altScreenMaxSeqLen is the byte length of the longest tracked sequence
// (\x1b[?1049h / \x1b[?1049l). The scanner retains this many bytes minus one
// between writes so a sequence split across two reads is still matched.
const altScreenMaxSeqLen = 8

// altScreenTrackingWriter forwards every byte to w unchanged while watching the
// stream for alt-screen enter/leave sequences, publishing the latest state to
// flag. The container's own output is the source of truth for whether the
// terminal was left in the alt buffer, so Restore can decide whether a corrective
// leave is needed. Not safe for concurrent use — the output copy runs in a single
// goroutine; flag is atomic only to publish across to Restore's goroutine.
type altScreenTrackingWriter struct {
	w     io.Writer
	flag  *atomic.Bool
	carry []byte // tail of the previous write that may prefix a split sequence
}

func newAltScreenTrackingWriter(w io.Writer, flag *atomic.Bool) *altScreenTrackingWriter {
	return &altScreenTrackingWriter{w: w, flag: flag}
}

func (a *altScreenTrackingWriter) Write(p []byte) (int, error) {
	a.scan(p)
	return a.w.Write(p)
}

// scan updates flag from the alt-screen toggles in p, honoring a sequence split
// across the previous write via the retained carry.
func (a *altScreenTrackingWriter) scan(p []byte) {
	a.carry = append(a.carry, p...)
	buf := a.carry

	enter := lastIndexOfAny(buf, altScreenEnterSeqs)
	leave := lastIndexOfAny(buf, altScreenLeaveSeqs)
	switch {
	case enter > leave:
		a.flag.Store(true)
	case leave > enter:
		a.flag.Store(false)
	}

	// Retain only a tail long enough to complete a sequence that begins at the
	// very end of this write. The rightmost retained toggle is by construction
	// the most recent one seen, so re-scanning it on the next write re-applies
	// the same state — it can never override a newer toggle, which always sits
	// further right.
	if keep := altScreenMaxSeqLen - 1; len(buf) > keep {
		n := copy(a.carry, buf[len(buf)-keep:])
		a.carry = a.carry[:n]
	}
}

// lastIndexOfAny returns the highest start index of any seq in b, or -1 if none
// are present.
func lastIndexOfAny(b []byte, seqs [][]byte) int {
	last := -1
	for _, s := range seqs {
		if i := bytes.LastIndex(b, s); i > last {
			last = i
		}
	}
	return last
}

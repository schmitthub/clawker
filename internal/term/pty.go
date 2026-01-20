package term

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/pkg/logger"
)

// resetSequence contains ANSI escape sequences to reset terminal visual state.
// - \x1b[?1049l : Leave alternate screen buffer
// - \x1b[?25h  : Show cursor
// - \x1b[0m    : Reset text attributes
// - \x1b(B    : Select ASCII character set
const resetSequence = "\x1b[?1049l\x1b[?25h\x1b[0m\x1b(B"

// PTYHandler manages the pseudo-terminal connection to a container
type PTYHandler struct {
	stdin   *os.File
	stdout  *os.File
	stderr  *os.File
	rawMode *RawMode

	// mu protects concurrent access
	mu sync.Mutex
}

// NewPTYHandler creates a new PTY handler
func NewPTYHandler() *PTYHandler {
	return &PTYHandler{
		stdin:   os.Stdin,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		rawMode: NewRawModeStdin(),
	}
}

// Setup prepares the terminal for PTY interaction
func (p *PTYHandler) Setup() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.rawMode.IsTerminal() {
		logger.Debug().Msg("stdin is not a terminal, skipping raw mode")
		return nil
	}

	if err := p.rawMode.Enable(); err != nil {
		return err
	}

	logger.Debug().Msg("terminal set to raw mode")
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

	logger.Debug().Msg("terminal restored")
	return nil
}

// resetVisualStateUnlocked sends reset sequences without locking.
// Must be called with p.mu held.
func (p *PTYHandler) resetVisualStateUnlocked() {
	if !p.rawMode.IsTerminal() {
		return
	}
	if _, err := p.stdout.WriteString(resetSequence); err != nil {
		logger.Warn().Err(err).Msg("failed to write terminal reset sequence")
	}
}

// Stream handles bidirectional I/O between local terminal and container
func (p *PTYHandler) Stream(ctx context.Context, hijacked client.HijackedResponse) error {
	defer hijacked.Close()

	outputDone := make(chan struct{})
	errCh := make(chan error, 2)

	// Copy container output to stdout
	go func() {
		_, err := io.Copy(p.stdout, hijacked.Reader)
		if err != nil && err != io.EOF {
			errCh <- err
		}
		close(outputDone)
	}()

	// Copy stdin to container input
	go func() {
		_, err := io.Copy(hijacked.Conn, p.stdin)
		if err != nil && err != io.EOF {
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
	hijacked client.HijackedResponse,
	resizeFunc func(height, width uint) error,
) error {
	defer hijacked.Close()

	outputDone := make(chan struct{})
	errCh := make(chan error, 2)

	// Initial resize and set up SIGWINCH monitoring
	if p.rawMode.IsTerminal() {
		width, height, err := p.rawMode.GetSize()
		if err != nil {
			logger.Debug().Err(err).Msg("failed to get initial terminal size")
		} else if resizeFunc != nil {
			// Docker CLI's +1/-1 trick: resize to artificial size first, then actual
			// This forces a size change event to trigger TUI redraw on re-attach
			// See: docker/cli/cli/command/container/attach.go resizeTTY()
			if err := resizeFunc(uint(height+1), uint(width+1)); err != nil {
				logger.Debug().Err(err).Msg("failed to set artificial container TTY size")
			}
			if err := resizeFunc(uint(height), uint(width)); err != nil {
				logger.Debug().Err(err).Msg("failed to set actual container TTY size")
			}
		}

		// Start monitoring for window resize events (SIGWINCH)
		resizeHandler := NewResizeHandler(resizeFunc, p.GetSize)
		resizeHandler.Start()
		defer resizeHandler.Stop()
	}

	// Copy container output to stdout
	go func() {
		_, err := io.Copy(p.stdout, hijacked.Reader)
		if err != nil && err != io.EOF {
			logger.Debug().Err(err).Msg("error copying container output")
			errCh <- err
		}
		close(outputDone)
	}()

	// Copy stdin to container input
	go func() {
		_, err := io.Copy(hijacked.Conn, p.stdin)
		if err != nil && err != io.EOF {
			logger.Debug().Err(err).Msg("error copying stdin to container")
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

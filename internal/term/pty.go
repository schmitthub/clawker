package term

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/pkg/logger"
)

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

// Restore returns the terminal to its original state
func (p *PTYHandler) Restore() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.rawMode.Restore(); err != nil {
		return err
	}

	logger.Debug().Msg("terminal restored")
	return nil
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
		if err == nil && resizeFunc != nil {
			resizeFunc(uint(height), uint(width))
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

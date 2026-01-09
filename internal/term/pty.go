package term

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/schmitthub/claucker/pkg/logger"
	"github.com/docker/docker/api/types"
)

// PTYHandler manages the pseudo-terminal connection to a container
type PTYHandler struct {
	stdin   *os.File
	stdout  *os.File
	stderr  *os.File
	rawMode *RawMode

	// done signals when streaming is complete
	done chan struct{}

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
		done:    make(chan struct{}),
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
func (p *PTYHandler) Stream(ctx context.Context, hijacked types.HijackedResponse) error {
	defer hijacked.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	// Copy container output to stdout
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(p.stdout, hijacked.Reader)
		if err != nil && err != io.EOF {
			errCh <- err
		}
	}()

	// Copy stdin to container input
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(hijacked.Conn, p.stdin)
		if err != nil && err != io.EOF {
			errCh <- err
		}
		// Close write side when stdin is done
		hijacked.CloseWrite()
	}()

	// Wait for context cancellation or completion
	go func() {
		wg.Wait()
		close(p.done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-p.done:
		return nil
	}
}

// StreamWithResize handles bidirectional I/O with terminal resize support
func (p *PTYHandler) StreamWithResize(
	ctx context.Context,
	hijacked types.HijackedResponse,
	resizeFunc func(height, width uint) error,
) error {
	defer hijacked.Close()

	var wg sync.WaitGroup
	errCh := make(chan error, 3)

	// Initial resize
	if p.rawMode.IsTerminal() {
		width, height, err := p.rawMode.GetSize()
		if err == nil && resizeFunc != nil {
			resizeFunc(uint(height), uint(width))
		}
	}

	// Copy container output to stdout
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(p.stdout, hijacked.Reader)
		if err != nil && err != io.EOF {
			logger.Debug().Err(err).Msg("error copying container output")
			errCh <- err
		}
	}()

	// Copy stdin to container input
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(hijacked.Conn, p.stdin)
		if err != nil && err != io.EOF {
			logger.Debug().Err(err).Msg("error copying stdin to container")
			errCh <- err
		}
		hijacked.CloseWrite()
	}()

	// Wait for completion
	go func() {
		wg.Wait()
		close(p.done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-p.done:
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

// Done returns a channel that's closed when streaming is complete
func (p *PTYHandler) Done() <-chan struct{} {
	return p.done
}

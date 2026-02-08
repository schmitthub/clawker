// Package signals provides OS signal utilities for graceful shutdown and
// terminal resize propagation. This is a leaf package — stdlib only, no
// internal imports, no logging.
package signals

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// SetupSignalContext creates a context that's canceled on SIGINT/SIGTERM.
func SetupSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case <-sigChan:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigChan)
	}()

	return ctx, cancel
}

// ResizeHandler manages terminal resize signals (SIGWINCH).
// It takes closures for resize and size-query operations so the caller
// decides *what* to resize and *how* to measure — keeping this package
// free of terminal or Docker imports.
type ResizeHandler struct {
	sigChan    chan os.Signal
	resizeFunc func(height, width uint) error // called with (height, width) — note: swapped from getSize's (width, height)
	getSize    func() (width, height int, err error)
	done       chan struct{}
	stopOnce   sync.Once
}

// NewResizeHandler creates a new resize handler.
//
//   - resizeFunc is called with (height, width) whenever SIGWINCH arrives.
//   - getSize returns the current terminal dimensions (width, height).
func NewResizeHandler(resizeFunc func(height, width uint) error, getSize func() (width, height int, err error)) *ResizeHandler {
	return &ResizeHandler{
		sigChan:    make(chan os.Signal, 1),
		resizeFunc: resizeFunc,
		getSize:    getSize,
		done:       make(chan struct{}),
	}
}

// Start begins listening for resize signals.
func (h *ResizeHandler) Start() {
	signal.Notify(h.sigChan, syscall.SIGWINCH)

	go h.handle()
}

// Stop stops listening for resize signals. Safe to call multiple times.
func (h *ResizeHandler) Stop() {
	h.stopOnce.Do(func() {
		signal.Stop(h.sigChan)
		close(h.done)
	})
}

// handle processes resize signals.
func (h *ResizeHandler) handle() {
	defer func() {
		// Recover from panics (e.g. send on closed channel) to avoid
		// crashing the host process — resize is best-effort.
		recover() //nolint:revive // intentionally discarding recovered value
	}()
	for {
		select {
		case <-h.done:
			return
		case <-h.sigChan:
			h.doResize()
		}
	}
}

// doResize performs the actual resize operation.
func (h *ResizeHandler) doResize() {
	if h.getSize == nil || h.resizeFunc == nil {
		return
	}

	width, height, err := h.getSize()
	if err != nil {
		return
	}

	_ = h.resizeFunc(uint(height), uint(width))
}

// TriggerResize manually triggers a resize operation.
func (h *ResizeHandler) TriggerResize() {
	h.doResize()
}

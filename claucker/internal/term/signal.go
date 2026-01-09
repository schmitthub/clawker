package term

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/claucker/claucker/pkg/logger"
)

// SignalHandler manages signal handling for terminal sessions
type SignalHandler struct {
	sigChan    chan os.Signal
	cancelFunc context.CancelFunc
	cleanup    func()
}

// NewSignalHandler creates a new signal handler
func NewSignalHandler(cancel context.CancelFunc, cleanup func()) *SignalHandler {
	return &SignalHandler{
		sigChan:    make(chan os.Signal, 1),
		cancelFunc: cancel,
		cleanup:    cleanup,
	}
}

// Start begins listening for signals
func (h *SignalHandler) Start() {
	signal.Notify(h.sigChan,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGHUP,
	)

	go h.handle()
}

// Stop stops listening for signals
func (h *SignalHandler) Stop() {
	signal.Stop(h.sigChan)
	close(h.sigChan)
}

// handle processes incoming signals
func (h *SignalHandler) handle() {
	for sig := range h.sigChan {
		logger.Debug().
			Str("signal", sig.String()).
			Msg("received signal")

		switch sig {
		case syscall.SIGINT, syscall.SIGTERM:
			// Run cleanup before canceling
			if h.cleanup != nil {
				h.cleanup()
			}
			// Cancel the context
			if h.cancelFunc != nil {
				h.cancelFunc()
			}
			return

		case syscall.SIGHUP:
			// Terminal hung up - cleanup and exit
			if h.cleanup != nil {
				h.cleanup()
			}
			if h.cancelFunc != nil {
				h.cancelFunc()
			}
			return
		}
	}
}

// ResizeHandler manages terminal resize signals (SIGWINCH)
type ResizeHandler struct {
	sigChan    chan os.Signal
	resizeFunc func(height, width uint) error
	getSize    func() (width, height int, err error)
	done       chan struct{}
}

// NewResizeHandler creates a new resize handler
func NewResizeHandler(resizeFunc func(height, width uint) error, getSize func() (width, height int, err error)) *ResizeHandler {
	return &ResizeHandler{
		sigChan:    make(chan os.Signal, 1),
		resizeFunc: resizeFunc,
		getSize:    getSize,
		done:       make(chan struct{}),
	}
}

// Start begins listening for resize signals
func (h *ResizeHandler) Start() {
	signal.Notify(h.sigChan, syscall.SIGWINCH)

	go h.handle()
}

// Stop stops listening for resize signals
func (h *ResizeHandler) Stop() {
	signal.Stop(h.sigChan)
	close(h.done)
}

// handle processes resize signals
func (h *ResizeHandler) handle() {
	for {
		select {
		case <-h.done:
			return
		case <-h.sigChan:
			h.doResize()
		}
	}
}

// doResize performs the actual resize operation
func (h *ResizeHandler) doResize() {
	if h.getSize == nil || h.resizeFunc == nil {
		return
	}

	width, height, err := h.getSize()
	if err != nil {
		logger.Debug().Err(err).Msg("failed to get terminal size")
		return
	}

	if err := h.resizeFunc(uint(height), uint(width)); err != nil {
		logger.Debug().Err(err).Msg("failed to resize container TTY")
	}
}

// TriggerResize manually triggers a resize operation
func (h *ResizeHandler) TriggerResize() {
	h.doResize()
}

// SetupSignalContext creates a context that's canceled on SIGINT/SIGTERM
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

// WaitForSignal blocks until a signal is received or context is done
func WaitForSignal(ctx context.Context, signals ...os.Signal) os.Signal {
	sigChan := make(chan os.Signal, 1)
	if len(signals) == 0 {
		signals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}
	}
	signal.Notify(sigChan, signals...)
	defer signal.Stop(sigChan)

	select {
	case sig := <-sigChan:
		return sig
	case <-ctx.Done():
		return nil
	}
}

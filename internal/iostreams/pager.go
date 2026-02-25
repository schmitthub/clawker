package iostreams

import (
	"errors"
	"io"
	"os"
	"runtime"
	"syscall"
)

// getPagerCommand returns the pager command to use.
// Order of precedence: CLAWKER_PAGER > PAGER > platform default
func getPagerCommand() string {
	// Check CLAWKER_PAGER first
	if pager := os.Getenv("CLAWKER_PAGER"); pager != "" {
		return pager
	}

	// Check standard PAGER variable
	if pager := os.Getenv("PAGER"); pager != "" {
		return pager
	}

	// Platform-specific defaults
	if runtime.GOOS == "windows" {
		return "more"
	}
	return "less -R"
}

// pagerWriter implements a WriteCloser that wraps all EPIPE errors in an ErrClosedPagerPipe type.
type pagerWriter struct {
	io.WriteCloser
}

func (w *pagerWriter) Write(d []byte) (int, error) {
	n, err := w.WriteCloser.Write(d)
	if err != nil && (errors.Is(err, io.ErrClosedPipe) || isEpipeError(err)) {
		return n, &ErrClosedPagerPipe{err}
	}
	return n, err
}

func isEpipeError(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}

// ErrClosedPagerPipe is the error returned when writing to a pager that has been closed.
type ErrClosedPagerPipe struct {
	error
}

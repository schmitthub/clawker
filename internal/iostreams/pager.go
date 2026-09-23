// Adapted from the GitHub CLI (https://github.com/cli/cli),
// Copyright (c) 2019 GitHub Inc., MIT License.

package iostreams

import (
	"errors"
	"io"
	"os"
	"runtime"
	"syscall"

	"github.com/schmitthub/clawker/internal/consts"
)

// getPagerCommand returns the pager command to use.
// Order of precedence: consts.EnvPager > PAGER > platform default
func getPagerCommand() string {
	// Check the clawker-specific pager override first
	if pager := os.Getenv(consts.EnvPager); pager != "" {
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

// pagerWriter is a WriteCloser to the pager's stdin. When the user quits
// the pager before the end, the pipe closes; the remaining output is
// dropped, not reported as a write failure.
type pagerWriter struct {
	io.WriteCloser
}

func (w *pagerWriter) Write(d []byte) (int, error) {
	n, err := w.WriteCloser.Write(d)
	if err != nil && (errors.Is(err, io.ErrClosedPipe) || isEpipeError(err)) {
		return len(d), nil
	}
	return n, err
}

func isEpipeError(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}

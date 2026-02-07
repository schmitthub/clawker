package cmdutil

import (
	"errors"
	"fmt"
)

// ExitError represents a container exit with a non-zero status code.
// Commands should return this instead of calling os.Exit() directly,
// allowing deferred cleanup to run. The root command handles os.Exit().
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

// FlagError indicates bad flags or arguments. When Main() encounters this error
// type, it prints the error message followed by the command's usage string.
type FlagError struct {
	err error
}

func (e *FlagError) Error() string { return e.err.Error() }
func (e *FlagError) Unwrap() error { return e.err }

// FlagErrorf creates a FlagError with a formatted message.
func FlagErrorf(format string, args ...any) error {
	return &FlagError{err: fmt.Errorf(format, args...)}
}

// FlagErrorWrap wraps an existing error as a FlagError.
func FlagErrorWrap(err error) error {
	return &FlagError{err: err}
}

// SilentError signals that the error has already been displayed to the user.
// Main() will exit non-zero but not print anything additional.
var SilentError = errors.New("SilentError")

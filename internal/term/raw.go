package term

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// RawMode manages putting the terminal into raw mode
type RawMode struct {
	fd       int
	oldState *term.State
	isRaw    bool
}

// NewRawMode creates a new RawMode manager for the given file descriptor
func NewRawMode(fd int) *RawMode {
	return &RawMode{
		fd:    fd,
		isRaw: false,
	}
}

// NewRawModeStdin creates a RawMode manager for stdin
func NewRawModeStdin() *RawMode {
	return NewRawMode(int(os.Stdin.Fd()))
}

// Enable puts the terminal into raw mode
func (r *RawMode) Enable() error {
	if r.isRaw {
		return nil
	}

	oldState, err := term.MakeRaw(r.fd)
	if err != nil {
		return err
	}

	r.oldState = oldState
	r.isRaw = true
	return nil
}

// Restore returns the terminal to its original state
func (r *RawMode) Restore() error {
	if !r.isRaw || r.oldState == nil {
		return nil
	}

	err := term.Restore(r.fd, r.oldState)
	if err == nil {
		r.isRaw = false
	}
	return err
}

// IsRaw returns true if the terminal is currently in raw mode
func (r *RawMode) IsRaw() bool {
	return r.isRaw
}

// IsTerminal checks if the file descriptor is a terminal
func (r *RawMode) IsTerminal() bool {
	return term.IsTerminal(r.fd)
}

// GetSize returns the current terminal size
func (r *RawMode) GetSize() (width, height int, err error) {
	return term.GetSize(r.fd)
}

// IsTerminalFd checks if a file descriptor is a terminal
func IsTerminalFd(fd int) bool {
	return term.IsTerminal(fd)
}

// IsStdinTerminal checks if stdin is a terminal
func IsStdinTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// IsStdoutTerminal checks if stdout is a terminal
func IsStdoutTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// GetStdinSize returns the size of the stdin terminal
func GetStdinSize() (width, height int, err error) {
	return term.GetSize(int(os.Stdin.Fd()))
}

// GetTerminalSize returns the terminal size for the given file descriptor.
// This is the canonical wrapper for x/term.GetSize — use this instead of
// importing golang.org/x/term directly.
func GetTerminalSize(fd int) (width, height int, err error) {
	return term.GetSize(fd)
}

// ReadPassword reads a line from the terminal with echo disabled, returning
// it without the trailing newline. The canonical wrapper for
// x/term.ReadPassword — use this instead of importing golang.org/x/term
// directly.
//
// The descriptor must be a terminal: echo is a property of the line
// discipline, so there is nothing to suppress on a pipe or a file and the
// call fails rather than reading a secret that was never hidden.
func ReadPassword(fd int) ([]byte, error) {
	secret, err := term.ReadPassword(fd)
	if err != nil {
		return nil, fmt.Errorf("reading from the terminal: %w", err)
	}
	return secret, nil
}

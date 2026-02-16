// Package loggertest provides test doubles for the logger package.
// TestLogger captures log output for assertions in tests.
// *TestLogger satisfies iostreams.Logger via explicit method implementations.
package loggertest

import (
	"bytes"

	"github.com/rs/zerolog"
)

// TestLogger is a test double that satisfies iostreams.Logger.
// It delegates to a private zerolog.Logger without embedding it,
// exposing only the 4 interface methods — not zerolog's full API surface.
type TestLogger struct {
	logger zerolog.Logger
	buf    *bytes.Buffer
}

// New creates a test logger that captures all output to a buffer.
func New() *TestLogger {
	buf := &bytes.Buffer{}
	return &TestLogger{
		logger: zerolog.New(buf),
		buf:    buf,
	}
}

// NewNop creates a test logger that discards all output.
func NewNop() *TestLogger {
	return &TestLogger{
		logger: zerolog.Nop(),
		buf:    &bytes.Buffer{},
	}
}

// Debug returns a debug-level zerolog.Event.
func (tl *TestLogger) Debug() *zerolog.Event { return tl.logger.Debug() }

// Info returns an info-level zerolog.Event.
func (tl *TestLogger) Info() *zerolog.Event { return tl.logger.Info() }

// Warn returns a warn-level zerolog.Event.
func (tl *TestLogger) Warn() *zerolog.Event { return tl.logger.Warn() }

// Error returns an error-level zerolog.Event.
func (tl *TestLogger) Error() *zerolog.Event { return tl.logger.Error() }

// Output returns captured log output as a string.
func (tl *TestLogger) Output() string { return tl.buf.String() }

// Reset clears captured output.
func (tl *TestLogger) Reset() { tl.buf.Reset() }

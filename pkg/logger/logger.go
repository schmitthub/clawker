package logger

import (
	"io"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

var (
	// Log is the global logger instance
	Log zerolog.Logger

	// interactiveMode controls whether INFO logs are suppressed.
	// When true, INFO logs are suppressed to avoid TUI interference.
	interactiveMode bool
	interactiveMu   sync.RWMutex
)

// SetInteractiveMode enables or disables interactive mode.
// When enabled, INFO logs are suppressed to avoid interfering with TUI output.
// ERROR and WARN logs are still shown.
func SetInteractiveMode(enabled bool) {
	interactiveMu.Lock()
	defer interactiveMu.Unlock()
	interactiveMode = enabled
}

// Init initializes the global logger with the specified configuration
func Init(debug bool) {
	var output io.Writer

	// Use console writer for pretty output
	output = zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
		NoColor:    false,
	}

	// Set log level based on debug flag
	level := zerolog.InfoLevel
	if debug {
		level = zerolog.DebugLevel
	}

	Log = zerolog.New(output).
		Level(level).
		With().
		Timestamp().
		Logger()
}

// shouldSuppress returns true if logs should be suppressed (interactive mode, non-debug level)
func shouldSuppress() bool {
	interactiveMu.RLock()
	interactive := interactiveMode
	interactiveMu.RUnlock()
	return interactive && Log.GetLevel() != zerolog.DebugLevel
}

// Debug logs a debug message (never suppressed - used for debugging)
func Debug() *zerolog.Event {
	return Log.Debug()
}

// Info logs an info message (suppressed in interactive mode)
func Info() *zerolog.Event {
	if shouldSuppress() {
		nop := zerolog.Nop()
		return nop.Info()
	}
	return Log.Info()
}

// Warn logs a warning message (suppressed in interactive mode)
func Warn() *zerolog.Event {
	if shouldSuppress() {
		nop := zerolog.Nop()
		return nop.Warn()
	}
	return Log.Warn()
}

// Error logs an error message (suppressed in interactive mode)
func Error() *zerolog.Event {
	if shouldSuppress() {
		nop := zerolog.Nop()
		return nop.Error()
	}
	return Log.Error()
}

// Fatal logs a fatal message and exits (never suppressed - critical failures)
func Fatal() *zerolog.Event {
	return Log.Fatal()
}

// WithField returns a logger with an additional field
func WithField(key string, value interface{}) zerolog.Logger {
	return Log.With().Interface(key, value).Logger()
}

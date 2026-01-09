package logger

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

var (
	// Log is the global logger instance
	Log zerolog.Logger
)

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

// Debug logs a debug message
func Debug() *zerolog.Event {
	return Log.Debug()
}

// Info logs an info message
func Info() *zerolog.Event {
	return Log.Info()
}

// Warn logs a warning message
func Warn() *zerolog.Event {
	return Log.Warn()
}

// Error logs an error message
func Error() *zerolog.Event {
	return Log.Error()
}

// Fatal logs a fatal message and exits
func Fatal() *zerolog.Event {
	return Log.Fatal()
}

// WithField returns a logger with an additional field
func WithField(key string, value interface{}) zerolog.Logger {
	return Log.With().Interface(key, value).Logger()
}

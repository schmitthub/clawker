package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// Log is the global logger instance
	Log zerolog.Logger

	// fileWriter is the file output for logging (with rotation)
	fileWriter *lumberjack.Logger

	// fileOnlyLog is a cached logger that writes only to file (no console).
	// Used in interactive mode to avoid creating a new logger per log event.
	fileOnlyLog zerolog.Logger

	// interactiveMode controls whether console logs are suppressed.
	// When true, ALL console logs (INFO, WARN, ERROR) are suppressed to avoid TUI interference.
	// File logging (if enabled) is NOT affected by interactive mode.
	interactiveMode bool
	interactiveMu   sync.RWMutex

	// logContext holds project/agent context for log entries (optional, may be empty)
	logContext   logContextData
	logContextMu sync.RWMutex
)

// logContextData holds optional project and agent context for log entries.
type logContextData struct {
	Project string
	Agent   string
}

// SetContext sets project and agent context for all subsequent log entries.
// Pass empty strings to clear. Thread-safe.
func SetContext(project, agent string) {
	logContextMu.Lock()
	defer logContextMu.Unlock()
	logContext = logContextData{
		Project: project,
		Agent:   agent,
	}
}

// ClearContext clears the project/agent context.
func ClearContext() {
	SetContext("", "")
}

// getContext returns current context (thread-safe read).
func getContext() logContextData {
	logContextMu.RLock()
	defer logContextMu.RUnlock()
	return logContext
}

// addContext adds project/agent fields to an event if set.
func addContext(event *zerolog.Event) *zerolog.Event {
	ctx := getContext()
	if ctx.Project != "" {
		event = event.Str("project", ctx.Project)
	}
	if ctx.Agent != "" {
		event = event.Str("agent", ctx.Agent)
	}
	return event
}

// LoggingConfig holds configuration for file-based logging.
// This matches internal/config.LoggingConfig but is duplicated here
// to avoid circular imports.
type LoggingConfig struct {
	FileEnabled *bool
	MaxSizeMB   int
	MaxAgeDays  int
	MaxBackups  int
}

// IsFileEnabled returns whether file logging is enabled.
// Defaults to true if not explicitly set.
func (c *LoggingConfig) IsFileEnabled() bool {
	if c.FileEnabled == nil {
		return true // enabled by default
	}
	return *c.FileEnabled
}

// GetMaxSizeMB returns the max size in MB, defaulting to 50 if not set.
func (c *LoggingConfig) GetMaxSizeMB() int {
	if c.MaxSizeMB <= 0 {
		return 50
	}
	return c.MaxSizeMB
}

// GetMaxAgeDays returns the max age in days, defaulting to 7 if not set.
func (c *LoggingConfig) GetMaxAgeDays() int {
	if c.MaxAgeDays <= 0 {
		return 7
	}
	return c.MaxAgeDays
}

// GetMaxBackups returns the max backups, defaulting to 3 if not set.
func (c *LoggingConfig) GetMaxBackups() int {
	if c.MaxBackups <= 0 {
		return 3
	}
	return c.MaxBackups
}

// SetInteractiveMode enables or disables interactive mode.
// When enabled, ALL console logs (INFO, WARN, ERROR) are suppressed to avoid
// interfering with TUI output. Debug and Fatal are never suppressed on console.
// File logging (if enabled) is NOT affected by interactive mode.
func SetInteractiveMode(enabled bool) {
	interactiveMu.Lock()
	defer interactiveMu.Unlock()
	interactiveMode = enabled
}

// Init initializes the global logger with the specified configuration.
// This initializes console-only logging. Use InitWithFile for file logging.
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

// InitWithFile initializes the logger with optional file output.
// File logging captures all logs regardless of interactive mode.
// If logsDir is empty or cfg indicates file logging is disabled,
// this behaves like Init (console-only).
func InitWithFile(debug bool, logsDir string, cfg *LoggingConfig) error {
	// Set log level based on debug flag
	level := zerolog.InfoLevel
	if debug {
		level = zerolog.DebugLevel
	}

	// Console writer for human-readable output
	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
		NoColor:    false,
	}

	// Check if file logging should be enabled
	if logsDir == "" || cfg == nil || !cfg.IsFileEnabled() {
		// Console-only logging
		Log = zerolog.New(consoleWriter).
			Level(level).
			With().
			Timestamp().
			Logger()
		return nil
	}

	// Create logs directory
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	logPath := filepath.Join(logsDir, "clawker.log")

	// Configure lumberjack for rotation
	fileWriter = &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    cfg.GetMaxSizeMB(),  // MB
		MaxAge:     cfg.GetMaxAgeDays(), // days
		MaxBackups: cfg.GetMaxBackups(),
		LocalTime:  true,
		Compress:   false,
	}

	// Create a cached file-only logger for use in interactive mode.
	// This avoids allocating a new logger on each suppressed log event.
	fileOnlyLog = zerolog.New(fileWriter).
		Level(level).
		With().
		Timestamp().
		Logger()

	// Multi-writer: console + file
	// Console uses human-readable format, file uses JSON
	// Interactive mode filtering happens at the log function level (Info, Warn, Error)
	multi := io.MultiWriter(consoleWriter, fileWriter)

	Log = zerolog.New(multi).
		Level(level).
		With().
		Timestamp().
		Logger()

	return nil
}

// CloseFileWriter closes the file writer if it exists.
// Call this on program shutdown for clean log file closure.
func CloseFileWriter() error {
	if fileWriter != nil {
		err := fileWriter.Close()
		fileWriter = nil // Prevent double-close and writes to closed file
		return err
	}
	return nil
}

// GetLogFilePath returns the path to the current log file, or empty string if file logging is disabled.
func GetLogFilePath() string {
	if fileWriter != nil {
		return fileWriter.Filename
	}
	return ""
}

// shouldSuppress returns true if console logs should be suppressed (interactive mode, non-debug level)
func shouldSuppress() bool {
	interactiveMu.RLock()
	interactive := interactiveMode
	interactiveMu.RUnlock()
	return interactive && Log.GetLevel() != zerolog.DebugLevel
}

// Debug logs a debug message (never suppressed - used for debugging)
func Debug() *zerolog.Event {
	return addContext(Log.Debug())
}

// Info logs an info message (suppressed on console in interactive mode, still written to file)
func Info() *zerolog.Event {
	if shouldSuppress() {
		// In interactive mode, log to file only (if enabled)
		if fileWriter != nil {
			return addContext(fileOnlyLog.Info())
		}
		nop := zerolog.Nop()
		return nop.Info()
	}
	return addContext(Log.Info())
}

// Warn logs a warning message (suppressed on console in interactive mode, still written to file)
func Warn() *zerolog.Event {
	if shouldSuppress() {
		// In interactive mode, log to file only (if enabled)
		if fileWriter != nil {
			return addContext(fileOnlyLog.Warn())
		}
		nop := zerolog.Nop()
		return nop.Warn()
	}
	return addContext(Log.Warn())
}

// Error logs an error message (suppressed on console in interactive mode, still written to file)
func Error() *zerolog.Event {
	if shouldSuppress() {
		// In interactive mode, log to file only (if enabled)
		if fileWriter != nil {
			return addContext(fileOnlyLog.Error())
		}
		nop := zerolog.Nop()
		return nop.Error()
	}
	return addContext(Log.Error())
}

// Fatal logs a fatal message and exits (never suppressed - critical failures)
func Fatal() *zerolog.Event {
	return addContext(Log.Fatal())
}

// WithField returns a logger with an additional field
func WithField(key string, value interface{}) zerolog.Logger {
	return Log.With().Interface(key, value).Logger()
}

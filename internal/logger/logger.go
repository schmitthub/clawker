package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// Log is the global logger instance (file-only; nop before InitWithFile)
	Log zerolog.Logger

	// fileWriter is the file output for logging (with rotation)
	fileWriter *lumberjack.Logger

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

// Init initializes the global logger as a nop logger.
// This is the pre-file-logging placeholder — all log output is discarded
// until InitWithFile is called with a valid logs directory.
func Init() {
	Log = zerolog.Nop()
}

// InitWithFile initializes the logger with file-only output.
// Zerolog never writes to the console — user-visible output uses
// fmt.Fprintf to IOStreams (see code style guide).
// If logsDir is empty or cfg indicates file logging is disabled,
// the logger remains a nop.
func InitWithFile(debug bool, logsDir string, cfg *LoggingConfig) error {
	level := zerolog.InfoLevel
	if debug {
		level = zerolog.DebugLevel
	}

	if logsDir == "" || cfg == nil || !cfg.IsFileEnabled() {
		Log = zerolog.Nop()
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

	Log = zerolog.New(fileWriter).
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

// Debug logs a debug message (developer diagnostics, file-only)
func Debug() *zerolog.Event {
	return addContext(Log.Debug())
}

// Info logs an info message (file-only)
func Info() *zerolog.Event {
	return addContext(Log.Info())
}

// Warn logs a warning message (file-only)
func Warn() *zerolog.Event {
	return addContext(Log.Warn())
}

// Error logs an error message (file-only)
func Error() *zerolog.Event {
	return addContext(Log.Error())
}

// Fatal logs a fatal message and exits (file-only).
// NEVER use in Cobra hooks — return errors instead.
func Fatal() *zerolog.Event {
	return addContext(Log.Fatal())
}

// WithField returns a logger with an additional field
func WithField(key string, value interface{}) zerolog.Logger {
	return Log.With().Interface(key, value).Logger()
}

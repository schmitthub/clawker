package logger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/contrib/bridges/otelzerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// Log is the global logger instance (file-only; nop before Init/NewLogger)
	Log zerolog.Logger

	// fileWriter is the file output for logging (with rotation)
	fileWriter *lumberjack.Logger

	// loggerProvider is the OTEL log provider (nil when OTEL is not enabled)
	loggerProvider *sdklog.LoggerProvider

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
	Compress    *bool
}

// IsFileEnabled returns whether file logging is enabled.
// Defaults to true if not explicitly set.
func (c *LoggingConfig) IsFileEnabled() bool {
	if c.FileEnabled == nil {
		return true // enabled by default
	}
	return *c.FileEnabled
}

// IsCompressEnabled returns whether rotated log compression is enabled.
// Defaults to true if not explicitly set.
func (c *LoggingConfig) IsCompressEnabled() bool {
	if c.Compress == nil {
		return true
	}
	return *c.Compress
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

// OtelLogConfig configures the OTEL zerolog bridge.
type OtelLogConfig struct {
	Endpoint       string        // e.g. "localhost:4318"
	Insecure       bool          // default: true (local collector)
	Timeout        time.Duration // export timeout
	MaxQueueSize   int           // batch processor queue size
	ExportInterval time.Duration // batch export interval
}

// Options configures the logger via NewLogger.
type Options struct {
	LogsDir    string         // directory for log files
	FileConfig *LoggingConfig // file rotation settings
	OtelConfig *OtelLogConfig // nil = file-only, no OTEL bridge
}

// Init initializes the global logger as a nop logger.
// This is the pre-file-logging placeholder — all log output is discarded
// until InitWithFile or NewLogger is called.
func Init() {
	Log = zerolog.Nop()
}

// InitWithFile initializes the logger with file-only output.
//
// Deprecated: Use NewLogger() for new code. This is retained for backwards compatibility.
func InitWithFile(logsDir string, cfg *LoggingConfig) error {
	return NewLogger(&Options{
		LogsDir:    logsDir,
		FileConfig: cfg,
	})
}

// NewLogger initializes the global logger with file output and optional OTEL bridge.
//
// With OtelConfig nil: file-only logging via lumberjack.
// With OtelConfig set: file logging + OTEL hook that streams to the collector.
// OTEL SDK handles resilience natively — buffer, retry, drop on overflow.
//
// If opts is nil or file logging is disabled, the logger becomes a nop.
func NewLogger(opts *Options) error {
	if opts == nil || opts.LogsDir == "" || opts.FileConfig == nil || !opts.FileConfig.IsFileEnabled() {
		Log = zerolog.Nop()
		return nil
	}

	// Create logs directory
	if err := os.MkdirAll(opts.LogsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	logPath := filepath.Join(opts.LogsDir, "clawker.log")

	// Configure lumberjack for rotation
	fileWriter = &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    opts.FileConfig.GetMaxSizeMB(),
		MaxAge:     opts.FileConfig.GetMaxAgeDays(),
		MaxBackups: opts.FileConfig.GetMaxBackups(),
		LocalTime:  true,
		Compress:   opts.FileConfig.IsCompressEnabled(),
	}

	// Base logger writes to file
	logger := zerolog.New(fileWriter).
		Level(zerolog.DebugLevel).
		With().
		Timestamp().
		Logger()

	// Add OTEL hook if configured
	if opts.OtelConfig != nil {
		provider, err := createOtelProvider(opts.OtelConfig)
		if err != nil {
			// OTEL failure is non-fatal — log to file only
			logger.Warn().Err(err).Msg("OTEL bridge unavailable, continuing with file-only logging")
		} else {
			loggerProvider = provider
			hook := otelzerolog.NewHook("clawker",
				otelzerolog.WithLoggerProvider(provider),
			)
			logger = logger.Hook(hook)
		}
	}

	Log = logger
	return nil
}

// createOtelProvider creates an OTLP HTTP log exporter and batch processor.
func createOtelProvider(cfg *OtelLogConfig) (*sdklog.LoggerProvider, error) {
	// Redirect OTEL SDK internal errors to the file logger instead of stderr.
	// The closure captures Log by reference — at invocation time (async error),
	// Log is already the file-backed logger set by NewLogger.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		Log.Warn().Err(err).Msg("otel sdk error")
	}))

	ctx := context.Background()

	exporterOpts := []otlploghttp.Option{
		otlploghttp.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		exporterOpts = append(exporterOpts, otlploghttp.WithInsecure())
	}
	if cfg.Timeout > 0 {
		exporterOpts = append(exporterOpts, otlploghttp.WithTimeout(cfg.Timeout))
	}

	exporter, err := otlploghttp.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP log exporter: %w", err)
	}

	processorOpts := []sdklog.BatchProcessorOption{}
	if cfg.MaxQueueSize > 0 {
		processorOpts = append(processorOpts, sdklog.WithMaxQueueSize(cfg.MaxQueueSize))
	}
	if cfg.ExportInterval > 0 {
		processorOpts = append(processorOpts, sdklog.WithExportInterval(cfg.ExportInterval))
	}

	processor := sdklog.NewBatchProcessor(exporter, processorOpts...)
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(processor))

	return provider, nil
}

// Close shuts down the logger, flushing any pending OTEL logs and closing the file writer.
// Call this on program shutdown for clean resource cleanup.
func Close() error {
	var firstErr error

	// Flush OTEL provider first (may have pending batches)
	if loggerProvider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := loggerProvider.Shutdown(ctx); err != nil {
			firstErr = fmt.Errorf("failed to shutdown OTEL provider: %w", err)
		}
		loggerProvider = nil
	}

	// Close file writer
	if fileWriter != nil {
		if err := fileWriter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		fileWriter = nil
	}

	return firstErr
}

// CloseFileWriter closes the file writer if it exists.
//
// Deprecated: Use Close() which handles both file + OTEL shutdown.
func CloseFileWriter() error {
	return Close()
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

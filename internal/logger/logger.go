package logger

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Logger wraps zerolog with file rotation and optional OTEL export.
// Create with New or Nop. Safe for concurrent use after construction.
type Logger struct {
	zl       zerolog.Logger
	fw       *lumberjack.Logger
	provider *sdklog.LoggerProvider

	mu     sync.Mutex // guards Close
	closed bool
}

// Options configures the logger.
type Options struct {
	// LogsDir is the directory for log files. Required for file logging.
	LogsDir string

	// Filename overrides the log file name within LogsDir.
	// Defaults to "clawker.log" when empty.
	Filename string

	// File rotation settings.
	MaxSizeMB  int  // default: 50
	MaxAgeDays int  // default: 7
	MaxBackups int  // default: 3
	Compress   bool // default: true

	// Otel configures the OTEL zerolog bridge. Nil disables OTEL export.
	Otel *OtelOptions
}

// OtelOptions configures the OTLP HTTP log exporter.
type OtelOptions struct {
	Endpoint       string        // e.g. "localhost:4318"
	Insecure       bool          // default: true (local collector)
	Timeout        time.Duration // export timeout
	MaxQueueSize   int           // batch processor queue size
	ExportInterval time.Duration // batch export interval

	// mTLS configuration. When all three paths are non-empty, the
	// exporter presents a client certificate during the TLS handshake
	// and pins the server's CA. This is how the clawker-cp daemon
	// pushes to the monitoring stack's CP-only OTLP receiver — agents
	// on clawker-net cannot present a CLI-signed cert and so the
	// handshake fails, regardless of whether they reached the port.
	//
	// CACertFile is the PEM bundle the exporter trusts for the server
	// cert. ClientCertFile + ClientKeyFile are the exporter's own
	// keypair. If any one of the three is set, all three must be set;
	// Insecure is ignored when these are populated.
	CACertFile     string
	ClientCertFile string
	ClientKeyFile  string
}

func (o *Options) maxSizeMB() int {
	if o.MaxSizeMB <= 0 {
		return 50
	}
	return o.MaxSizeMB
}

func (o *Options) maxAgeDays() int {
	if o.MaxAgeDays <= 0 {
		return 7
	}
	return o.MaxAgeDays
}

func (o *Options) maxBackups() int {
	if o.MaxBackups <= 0 {
		return 3
	}
	return o.MaxBackups
}

// Nop returns a logger that discards all output.
func Nop() *Logger {
	return &Logger{zl: zerolog.Nop()}
}

// NewWriter creates a logger that writes structured JSON to the given
// io.Writer. No file rotation, no OTEL bridge — this is the constructor
// for containerized daemons (clawker-cp, clawkerd) that want their
// structured logs captured by the container runtime's stdout/stderr
// collection so `docker logs <container>` shows them.
//
// Use logger.New when you want file-based logging with rotation (that's
// the CLI/host-side path). Use NewWriter inside containers where the
// container runtime owns log lifecycle.
func NewWriter(w io.Writer) *Logger {
	zl := zerolog.New(w).
		Level(zerolog.DebugLevel).
		With().
		Timestamp().
		Logger()
	return &Logger{zl: zl}
}

// New creates a logger with file output and optional OTEL bridge.
//
// OTEL failure is non-fatal: the logger falls back to file-only and
// the OTEL warning is written to the log file.
func New(opts Options) (*Logger, error) {
	if opts.LogsDir == "" {
		return nil, fmt.Errorf("logger: LogsDir is required")
	}

	if err := os.MkdirAll(opts.LogsDir, 0o755); err != nil {
		return nil, fmt.Errorf("logger: create logs directory: %w", err)
	}

	filename := opts.Filename
	if filename == "" {
		filename = "clawker.log"
	}

	fw := &lumberjack.Logger{
		Filename:   filepath.Join(opts.LogsDir, filename),
		MaxSize:    opts.maxSizeMB(),
		MaxAge:     opts.maxAgeDays(),
		MaxBackups: opts.maxBackups(),
		LocalTime:  true,
		Compress:   opts.Compress,
	}

	// Default writer is the lumberjack file. When OTEL is configured
	// we tee through a custom writer that parses zerolog's JSON output
	// and re-emits each record as an OTEL log.Record with all
	// structured fields preserved (otelzerolog's bridge can't read
	// fields off a zerolog.Event — see otel_writer.go).
	var writer io.Writer = fw
	l := &Logger{fw: fw}

	if opts.Otel != nil {
		fallbackZL := zerolog.New(fw)
		provider, err := newOtelProvider(opts.Otel, fallbackZL)
		if err != nil {
			// Non-fatal: continue file-only. Surface the warning to BOTH
			// the file logger AND stderr — if the file writer is also
			// broken the user would otherwise have no signal that
			// logging is degraded.
			fallbackZL.Warn().Err(err).Msg("OTEL bridge unavailable, continuing file-only")
			fmt.Fprintf(os.Stderr, "warning: OTEL bridge unavailable, continuing file-only: %v\n", err)
		} else {
			l.provider = provider
			otelW := newOtelLogWriter(provider.Logger("clawker"))
			writer = zerolog.MultiLevelWriter(fw, otelW)
		}
	}

	zl := zerolog.New(writer).
		Level(zerolog.DebugLevel).
		With().
		Timestamp().
		Logger()
	l.zl = zl

	return l, nil
}

// With returns a new Logger with additional context fields.
// Use this instead of per-call field injection for recurring context
// like project or agent.
//
//	projectLog := log.With("project", "foo", "agent", "bar")
//	projectLog.Info().Msg("started")
func (l *Logger) With(keyvals ...interface{}) *Logger {
	if len(keyvals)%2 != 0 {
		panic("logger.With: odd number of key-value arguments")
	}
	ctx := l.zl.With()
	for i := 0; i < len(keyvals); i += 2 {
		key, ok := keyvals[i].(string)
		if !ok {
			panic(fmt.Sprintf("logger.With: key at index %d is %T, want string", i, keyvals[i]))
		}
		ctx = ctx.Interface(key, keyvals[i+1])
	}
	return &Logger{
		zl:       ctx.Logger(),
		fw:       l.fw,
		provider: l.provider,
	}
}

// Debug logs at debug level.
func (l *Logger) Debug() *zerolog.Event { return l.zl.Debug() }

// Info logs at info level.
func (l *Logger) Info() *zerolog.Event { return l.zl.Info() }

// Warn logs at warn level.
func (l *Logger) Warn() *zerolog.Event { return l.zl.Warn() }

// Error logs at error level.
func (l *Logger) Error() *zerolog.Event { return l.zl.Error() }

// Fatal logs at fatal level and exits.
// Avoid in Cobra hooks — return errors instead.
func (l *Logger) Fatal() *zerolog.Event { return l.zl.Fatal() }

// Zerolog returns the underlying zerolog.Logger for interop with
// libraries that accept one directly.
func (l *Logger) Zerolog() zerolog.Logger { return l.zl }

// LogFilePath returns the path to the current log file,
// or empty string if this is a nop logger.
func (l *Logger) LogFilePath() string {
	if l.fw != nil {
		return l.fw.Filename
	}
	return ""
}

// Close flushes pending OTEL batches and closes the file writer.
// Safe to call multiple times. Safe to call on a Nop logger.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true

	var firstErr error

	if l.provider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := l.provider.Shutdown(ctx); err != nil {
			firstErr = fmt.Errorf("logger: shutdown OTEL provider: %w", err)
		}
	}

	if l.fw != nil {
		if err := l.fw.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("logger: close file writer: %w", err)
		}
	}

	return firstErr
}

// newOtelProvider creates an OTLP HTTP log exporter and batch processor.
func newOtelProvider(cfg *OtelOptions, fileLogger zerolog.Logger) (*sdklog.LoggerProvider, error) {
	// Route OTEL SDK internal errors to the file logger instead of stderr.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		fileLogger.Warn().Err(err).Msg("otel sdk error")
	}))

	exporterOpts := []otlploghttp.Option{
		otlploghttp.WithEndpoint(cfg.Endpoint),
	}

	switch {
	case cfg.ClientCertFile != "" || cfg.ClientKeyFile != "" || cfg.CACertFile != "":
		// All three required when any are set — partial config is a
		// configuration bug rather than a soft fallback.
		if cfg.ClientCertFile == "" || cfg.ClientKeyFile == "" || cfg.CACertFile == "" {
			return nil, fmt.Errorf("OTEL mTLS: ClientCertFile, ClientKeyFile, and CACertFile must all be set")
		}
		tlsCfg, err := buildOtelMTLSConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("OTEL mTLS config: %w", err)
		}
		exporterOpts = append(exporterOpts, otlploghttp.WithTLSClientConfig(tlsCfg))
	case cfg.Insecure:
		exporterOpts = append(exporterOpts, otlploghttp.WithInsecure())
	}

	if cfg.Timeout > 0 {
		exporterOpts = append(exporterOpts, otlploghttp.WithTimeout(cfg.Timeout))
	}

	exporter, err := otlploghttp.New(context.Background(), exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP log exporter: %w", err)
	}

	var processorOpts []sdklog.BatchProcessorOption
	if cfg.MaxQueueSize > 0 {
		processorOpts = append(processorOpts, sdklog.WithMaxQueueSize(cfg.MaxQueueSize))
	}
	if cfg.ExportInterval > 0 {
		processorOpts = append(processorOpts, sdklog.WithExportInterval(cfg.ExportInterval))
	}

	processor := sdklog.NewBatchProcessor(exporter, processorOpts...)
	return sdklog.NewLoggerProvider(sdklog.WithProcessor(processor)), nil
}

// buildOtelMTLSConfig loads the client keypair and trust roots for the
// OTLP exporter's mTLS handshake. The client cert is presented during
// the handshake; the receiver gates `require_client_certificate: true`
// on a CA bundle so only CLI-issued certs (currently the daemon's
// cp-client cert) connect.
func buildOtelMTLSConfig(cfg *OtelOptions) (*tls.Config, error) {
	clientCert, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load client keypair: %w", err)
	}
	caBytes, err := os.ReadFile(cfg.CACertFile)
	if err != nil {
		return nil, fmt.Errorf("read CA bundle: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("CA bundle %q contains no PEM blocks", cfg.CACertFile)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

package logger

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/schmitthub/clawker/internal/consts"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"google.golang.org/grpc/credentials"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Rotating file sink defaults.
const (
	defaultLogFileName   = "clawker.log"
	defaultLogMaxSizeMB  = 50
	defaultLogMaxAgeDays = 7
	defaultLogMaxBackups = 3
)

// Logger wraps zerolog with file rotation and optional OTEL export.
// Create with New or Nop. Safe for concurrent use after construction.
type Logger struct {
	zl       zerolog.Logger
	fw       *lumberjack.Logger
	provider *sdklog.LoggerProvider

	// base is the field-less root logger (sinks + timestamp, no With
	// fields). With rebuilds zl from base each call so a repeated key
	// (e.g. "component" set at each layer) collapses to one field
	// instead of stacking — zerolog itself never dedupes keys.
	base zerolog.Logger
	// fields is the accumulated With context, deduped by key with the
	// last value winning, in first-set order.
	fields []logField

	mu     sync.Mutex // guards Close
	closed bool
}

// logField is one accumulated With key/value pair.
type logField struct {
	key string
	val any
}

// mergeFields returns prev with keyvals applied: a key already present
// keeps its position and takes the new value (last wins); a new key is
// appended. The input slice is never mutated.
func mergeFields(prev []logField, keyvals []any) []logField {
	merged := make([]logField, len(prev))
	copy(merged, prev)
	for i := 0; i < len(keyvals); i += 2 {
		key, ok := keyvals[i].(string)
		if !ok {
			panic(fmt.Sprintf("logger.With: key at index %d is %T, want string", i, keyvals[i]))
		}
		val := keyvals[i+1]
		replaced := false
		for j := range merged {
			if merged[j].key == key {
				merged[j].val = val
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, logField{key: key, val: val})
		}
	}
	return merged
}

// Options configures the logger.
type Options struct {
	// LogsDir is the directory for log files. Required for file logging.
	LogsDir string

	// Filename overrides the log file name within LogsDir.
	// Defaults to defaultLogFileName when empty.
	Filename string

	// File rotation settings.
	MaxSizeMB  int  // default: 50
	MaxAgeDays int  // default: 7
	MaxBackups int  // default: 3
	Compress   bool // default: true

	// Otel configures the OTEL zerolog bridge. Nil disables OTEL export.
	Otel *OtelOptions

	// EchoStdout mirrors every record to os.Stdout in addition to the
	// file (and OTEL bridge if configured). Intended for containerized
	// daemons whose structured logs should also surface in
	// `docker logs <container>`; host-side CLI logging leaves this off.
	EchoStdout bool
}

// OtelOptions configures the OTLP/gRPC log exporter. The transport is
// gRPC because the collector's trusted-infra receiver speaks gRPC only
// — an HTTP exporter hits it with the wrong content-type and the
// receiver returns 415 Unsupported Media Type, silently dropping every
// record. See newOtelProvider for the receiver-side rationale.
type OtelOptions struct {
	Endpoint       string        // e.g. "localhost:4317"
	Insecure       bool          // default: true (local collector)
	Timeout        time.Duration // export timeout
	MaxQueueSize   int           // batch processor queue size
	ExportInterval time.Duration // batch export interval

	// ServiceName stamps `service.name` on the Resource attached to every
	// emitted log record. Required when the collector routes on this
	// attribute (routing/trusted, routing/untrusted in otel-config.yaml).
	// Empty leaves the SDK default ("unknown_service:<binary>") which is
	// dropped silently at the routing connector. Example caller: "clawker-cli"
	// (host CLI, set in internal/cmd/factory/default.go).
	ServiceName string

	// mTLS configuration. Two mutually-exclusive shapes:
	//
	//   - File-path triple (CACertFile + ClientCertFile + ClientKeyFile).
	//     The exporter reads PEM material from disk at New time. No
	//     in-tree consumer today — clawkercp uses the TLSConfig path
	//     below, clawker-cli runs Insecure=true on the untrusted otlp
	//     receiver (CLI leaves chain to the CLI root, not the infra
	//     intermediate, so they cannot complete the otlp/infra
	//     handshake), and Envoy/CoreDNS read their bind-mounted PEM via
	//     their own native config (not this struct). Shape preserved
	//     for any future on-disk-cert consumer. If any one of the three
	//     is set, all three must be set.
	//
	//   - In-process tls.Config. The exporter is given a fully-formed
	//     *tls.Config (typically built by
	//     internal/controlplane/otelcerts.Service.LoadTLSConfig with a
	//     GetClientCertificate hook that re-mints on every handshake).
	//     Used by the clawkercp daemon's own OTel exporter so the leaf
	//     never lands on disk and rotation matches the connection
	//     lifecycle.
	//
	// At most one shape may be set. Insecure is ignored when either is
	// populated.
	CACertFile     string
	ClientCertFile string
	ClientKeyFile  string

	// TLSConfig is the in-process counterpart to the file-path triple.
	// When non-nil, the exporter uses it directly and the file-path
	// fields are not consulted. Construction is the caller's
	// responsibility — see internal/controlplane/otelcerts for the
	// trusted-lane wiring used by clawkercp.
	TLSConfig *tls.Config
}

func (o *Options) maxSizeMB() int {
	if o.MaxSizeMB <= 0 {
		return defaultLogMaxSizeMB
	}
	return o.MaxSizeMB
}

func (o *Options) maxAgeDays() int {
	if o.MaxAgeDays <= 0 {
		return defaultLogMaxAgeDays
	}
	return o.MaxAgeDays
}

func (o *Options) maxBackups() int {
	if o.MaxBackups <= 0 {
		return defaultLogMaxBackups
	}
	return o.MaxBackups
}

// Nop returns a logger that discards all output.
func Nop() *Logger {
	nop := zerolog.Nop()
	return &Logger{zl: nop, base: nop}
}

// NewWriter creates a logger that writes structured JSON to the given
// io.Writer. No file rotation, no OTEL bridge.
//
// Use logger.New for file-based logging with rotation (the primary path
// for daemons and the CLI). Use NewWriter as a fallback when New fails —
// for example, writing to os.Stderr when the log directory is unavailable.
// Also useful in tests that capture output via a *bytes.Buffer.
func NewWriter(w io.Writer) *Logger {
	zl := zerolog.New(w).
		Level(zerolog.DebugLevel).
		With().
		Timestamp().
		Logger()
	return &Logger{zl: zl, base: zl}
}

// OtelOptionsFromEnv builds [OtelOptions] from the standard OTLP
// environment variables. It returns nil when no endpoint is configured —
// the logger then runs file-only and the caller needs no OTEL dependency
// at runtime.
//
// Per-signal OTEL_EXPORTER_OTLP_LOGS_ENDPOINT takes precedence over the
// generic OTEL_EXPORTER_OTLP_ENDPOINT (resolved by
// [consts.ResolveOTLPEndpoint]). Either may be a full URL
// (https://host.docker.internal:4319/v1/logs) or a bare authority
// (host.docker.internal:4319); the OTLP/gRPC exporter only needs
// host:port, so scheme/path are stripped during resolution.
//
// Default is TLS. Bare host:port → TLS. https:// → TLS. Only explicit
// http:// opts in to plaintext, so a misconfigured prod endpoint can't
// silently downgrade.
//
// mTLS material is NOT taken from env. A trusted-lane caller (the clawkercp
// daemon) wires the in-process [OtelOptions.TLSConfig] shape separately so
// the leaf never lands on disk; env-driven cert paths are deliberately not
// honored here. This helper only resolves the endpoint and plaintext flag.
func OtelOptionsFromEnv() *OtelOptions {
	endpoint, insecure := consts.ResolveOTLPEndpoint()
	if endpoint == "" {
		return nil
	}
	return &OtelOptions{
		Endpoint: endpoint,
		Insecure: insecure,
	}
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
		filename = defaultLogFileName
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
	// Compose the sinks bottom-up: file is always present; stdout is
	// added when EchoStdout is set so daemon logs surface in
	// `docker logs <container>`; OTEL bridge is appended last when
	// configured.
	sinks := []io.Writer{fw}
	if opts.EchoStdout {
		// Wrap os.Stdout so transient stdout failures (closed FD,
		// pipe break in a `docker logs` consumer) don't shadow the
		// success of the other sinks through MultiLevelWriter's
		// first-error-wins semantics. File + OTEL sinks stay
		// authoritative; stdout is best-effort triage output.
		sinks = append(sinks, absorbingWriter{w: os.Stdout})
	}
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
			sinks = append(sinks, newOtelLogWriter(provider.Logger("clawker")))
		}
	}

	var writer io.Writer
	if len(sinks) == 1 {
		writer = sinks[0]
	} else {
		writer = zerolog.MultiLevelWriter(sinks...)
	}

	zl := zerolog.New(writer).
		Level(zerolog.DebugLevel).
		With().
		Timestamp().
		Logger()
	l.zl = zl
	l.base = zl

	return l, nil
}

// With returns a new Logger with additional context fields.
// Use this instead of per-call field injection for recurring context
// like project or agent.
//
//	projectLog := log.With("project", "foo", "agent", "bar")
//	projectLog.Info().Msg("started")
func (l *Logger) With(keyvals ...any) *Logger {
	if len(keyvals)%2 != 0 {
		panic("logger.With: odd number of key-value arguments")
	}
	// Rebuild from the field-less base applying the deduped field set, so
	// a key set at multiple layers (e.g. "component") collapses to a
	// single field. Building incrementally off l.zl would stack duplicate
	// keys — zerolog does not dedupe.
	fields := mergeFields(l.fields, keyvals)
	ctx := l.base.With()
	for _, f := range fields {
		ctx = ctx.Interface(f.key, f.val)
	}
	return &Logger{
		zl:       ctx.Logger(),
		fw:       l.fw,
		provider: l.provider,
		base:     l.base,
		fields:   fields,
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
func (l *Logger) Close(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true

	var provErr, fwErr error

	if l.provider != nil {
		// ctx is the flush deadline. Shutdown honors it: a canceled or expired
		// ctx unwinds the final export (and the exporter's retry backoff) at once
		// rather than blocking on an unreachable collector. With a live ctx the
		// bound is OtelOptions.Timeout when set, else the OTLP SDK default export
		// timeout. Whatever Shutdown returns is the true outcome of the close —
		// report it. Interpreting a ctx cancellation it requested (e.g. a caller
		// that cancels for a fast exit) is the caller's job, not the logger's.
		if err := l.provider.Shutdown(ctx); err != nil {
			provErr = fmt.Errorf("logger: shutdown OTEL provider: %w", err)
		}
	}

	if l.fw != nil {
		if err := l.fw.Close(); err != nil {
			fwErr = fmt.Errorf("logger: close file writer: %w", err)
		}
	}

	return errors.Join(provErr, fwErr)
}

// absorbingWriter forwards writes to an inner writer but always
// returns a successful (n=len(p), err=nil) result. Composed inside
// MultiLevelWriter so a failing best-effort sink (stdout) can't
// shadow successful writes to the authoritative sinks (file, OTEL).
type absorbingWriter struct {
	w io.Writer
}

func (a absorbingWriter) Write(p []byte) (int, error) {
	_, _ = a.w.Write(p)
	return len(p), nil
}

// newOtelProvider creates an OTLP/gRPC log exporter and batch processor.
// gRPC is the chosen transport for the trusted infra lane because the
// collector's otlp/infra receiver declares grpc only — an HTTP exporter
// hits the gRPC server with a non-grpc content-type and the receiver
// returns 415 Unsupported Media Type, silently dropping every record.
func newOtelProvider(cfg *OtelOptions, fileLogger zerolog.Logger) (*sdklog.LoggerProvider, error) {
	// Route OTEL SDK internal errors to the file logger instead of stderr.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		fileLogger.Warn().Err(err).Msg("otel sdk error")
	}))

	exporterOpts := []otlploggrpc.Option{
		otlploggrpc.WithEndpoint(cfg.Endpoint),
	}

	hasPathTriple := cfg.ClientCertFile != "" || cfg.ClientKeyFile != "" || cfg.CACertFile != ""
	switch {
	case cfg.TLSConfig != nil && hasPathTriple:
		// Path triple and TLSConfig together are a wiring bug — the
		// caller has two trust anchors and we'd silently pick one. Fail
		// loud so the operator can resolve the conflict.
		return nil, fmt.Errorf("OTEL mTLS: TLSConfig and file-path triple are mutually exclusive")
	case cfg.TLSConfig != nil:
		// In-process tls.Config — typically minted by
		// internal/controlplane/otelcerts.Service.LoadTLSConfig with a
		// GetClientCertificate hook that re-mints per handshake.
		exporterOpts = append(exporterOpts, otlploggrpc.WithTLSCredentials(credentials.NewTLS(cfg.TLSConfig)))
	case hasPathTriple:
		// All three required when any are set — partial config is a
		// configuration bug rather than a soft fallback.
		if cfg.ClientCertFile == "" || cfg.ClientKeyFile == "" || cfg.CACertFile == "" {
			return nil, fmt.Errorf("OTEL mTLS: ClientCertFile, ClientKeyFile, and CACertFile must all be set")
		}
		tlsCfg, err := buildOtelMTLSConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("OTEL mTLS config: %w", err)
		}
		exporterOpts = append(exporterOpts, otlploggrpc.WithTLSCredentials(credentials.NewTLS(tlsCfg)))
	case cfg.Insecure:
		exporterOpts = append(exporterOpts, otlploggrpc.WithInsecure())
	}

	if cfg.Timeout > 0 {
		exporterOpts = append(exporterOpts, otlploggrpc.WithTimeout(cfg.Timeout))
	}

	exporter, err := otlploggrpc.New(context.Background(), exporterOpts...)
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

	providerOpts := []sdklog.LoggerProviderOption{sdklog.WithProcessor(processor)}
	if cfg.ServiceName != "" {
		res, err := sdkresource.Merge(sdkresource.Default(), sdkresource.NewSchemaless(
			semconv.ServiceName(cfg.ServiceName),
		))
		if err != nil {
			return nil, fmt.Errorf("build OTEL resource: %w", err)
		}
		providerOpts = append(providerOpts, sdklog.WithResource(res))
	}
	return sdklog.NewLoggerProvider(providerOpts...), nil
}

// buildOtelMTLSConfig loads the client keypair and trust roots for the
// OTLP exporter's mTLS handshake from the file-path triple in OtelOptions.
// The client cert is presented during the handshake; the receiver gates
// `require_client_certificate: true` on a CA bundle so only certs signed
// by the trusted CA connect.
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

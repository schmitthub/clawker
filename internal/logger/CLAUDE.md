# Logger Package

Zerolog-based file-only logging with optional OTEL bridge. Struct-based API — no global state. Zerolog never writes to the console — user-visible output uses `fmt.Fprintf` to IOStreams (see code style guide).

## Architecture

**File-only by default**: All log output goes to `cfg.LogsSubdir()/clawker.log` via lumberjack rotation with gzip compression. There is no console writer.

**Struct-based**: `*Logger` is a self-contained struct holding the zerolog instance, file writer, and OTEL provider. Created via `New(opts)` for production or `Nop()` for tests/disabled logging. No global state.

**Factory noun**: Wired as a lazy closure on `cmdutil.Factory.Logger`. Commands capture `f.Logger` on their Options struct and resolve it in the run function. Library packages accept `*logger.Logger` in constructors.

**Dual-destination**: When `OtelOptions` is provided, logs go to both the local file (lumberjack writer) and an OTEL collector via a custom `io.Writer` sink (`otelLogWriter`) that parses zerolog's JSON output and re-emits each record as an OTEL `log.Record` with all structured fields preserved. OTEL failure is non-fatal — if the provider cannot be created, logging falls back to file-only with a warning.

**User-visible output**: Commands use `fmt.Fprintf(ios.ErrOut, ...)` with `ios.ColorScheme()` for warnings/status, and return errors to `Main()` for centralized rendering. See `cli-output-style-guide` memory for per-scenario details.

## Types

### Logger

```go
type Logger struct {
    zl       zerolog.Logger          // underlying zerolog instance
    fw       *lumberjack.Logger      // file writer (nil for Nop)
    provider *sdklog.LoggerProvider  // OTEL provider (nil if not configured)
    mu       sync.Mutex              // guards Close
    closed   bool
}
```

### Options

```go
type Options struct {
    LogsDir    string       // directory for log files (required)
    Filename   string       // override log file name (default: "clawker.log")
    MaxSizeMB  int          // max log file size (default: 50)
    MaxAgeDays int          // max log age (default: 7)
    MaxBackups int          // max backup count (default: 3)
    Compress   bool         // gzip rotated logs (default: true)
    Otel       *OtelOptions // nil = file-only, no OTEL bridge
    EchoStdout bool         // mirror records to os.Stdout (container daemon path; off for CLI)
}
```

### OtelOptions

```go
type OtelOptions struct {
    Endpoint       string        // e.g. "localhost:4317" — gRPC, NOT HTTP
    Insecure       bool          // default: true (local collector)
    Timeout        time.Duration // export timeout
    MaxQueueSize   int           // batch processor queue size
    ExportInterval time.Duration // batch export interval

    // ServiceName stamps `service.name` on the OTEL Resource for every
    // emitted record. REQUIRED when the collector routes on this
    // attribute (routing/trusted, routing/untrusted in otel-config.yaml).
    // Leaving it empty produces the SDK default "unknown_service:<binary>",
    // which the routing connector drops silently — records export
    // successfully but never reach a backend. Canonical values:
    // "clawker-cli" (host CLI), "clawkercp" (control plane daemon).
    ServiceName string

    // mTLS material — two mutually-exclusive shapes; at most one may be
    // set. When either is wired, the exporter presents the leaf during the
    // gRPC handshake and pins the receiver's CA, and Insecure is ignored.
    //
    //   - File-path triple (CACertFile + ClientCertFile + ClientKeyFile):
    //     exporter reads PEM from disk at New time. No in-tree
    //     consumer today (CLI runs Insecure=true on the untrusted lane;
    //     CP uses TLSConfig; Envoy/CoreDNS read PEM via their own native
    //     config, not this struct). Shape preserved for future on-disk-
    //     cert consumers. If any one is set, all three must be set.
    //   - In-process TLSConfig: caller passes a fully-formed *tls.Config
    //     (typically built by internal/controlplane/otelcerts with a
    //     GetClientCertificate hook that re-mints per handshake). Used by
    //     clawkercp so the leaf never lands on disk and rotation matches
    //     the connection lifecycle. When non-nil, file-path fields are
    //     not consulted.
    CACertFile     string
    ClientCertFile string
    ClientKeyFile  string
    TLSConfig      *tls.Config
}
```

Transport is OTLP/gRPC, not OTLP/HTTP. Two distinct receivers exist on the collector: the unauthenticated `otlp` receiver (`OtelGRPCPort`, plaintext) which the host CLI logger targets, and the mTLS-gated `otlp/infra` receiver (`OtelInfraPort`, gRPC-only, infra-intermediate CA) which `clawkercp` targets via the in-process `TLSConfig` shape. They share the wire format but not the trust boundary — see `internal/monitor/CLAUDE.md` "OTEL Pipelines". Dialing the HTTP port with a gRPC exporter returns 415 and silently drops every record.

## Constructors

```go
func New(opts Options) (*Logger, error)  // File logging + optional OTEL bridge (CLI/host path)
func NewWriter(w io.Writer) *Logger       // Structured JSON to io.Writer, no rotation, no OTEL
func Nop() *Logger                        // Discards all output (tests, disabled logging)
```

`New` creates the log directory, configures lumberjack rotation, and optionally attaches the OTEL bridge. Returns error if `LogsDir` is empty or directory creation fails. OTEL failure is non-fatal (falls back to file-only).

`NewWriter` writes structured JSON to an arbitrary `io.Writer` with no file rotation and no OTEL bridge. Used in tests (passing `*bytes.Buffer`) and as the degraded fallback in `clawkercp` when `New` fails (writes to `os.Stderr`). Debug level by default.

**Note**: Both `clawkerd` and `clawkercp` use `New(...)` as their primary logger, not `NewWriter`. `clawkercp` sets `EchoStdout: true` to mirror records to stdout so `docker logs clawker-controlplane` shows them alongside the file/OTEL sinks. `clawkerd` uses `New(...)` writing to `/var/log/clawker/clawkerd.log` without `EchoStdout` — per-agent containers can be many and may run with `--rm` so `docker logs` is short-lived; an on-disk rotated file (50MB / 7d / 3 backups) gives an operator a stable place to triage individual-agent issues across container churn. Material is bounded by the container's writable layer — `--rm` or `docker rm` reclaims it. See `cmd/clawkerd/CLAUDE.md` for the full level taxonomy.

`Nop` returns a logger backed by `zerolog.Nop()` — zero allocation, no file I/O.

## Env-Driven OtelOptions

```go
func OtelOptionsFromEnv() *OtelOptions  // resolve endpoint + plaintext flag from OTLP env; nil when unconfigured
```

`OtelOptionsFromEnv` builds `*OtelOptions` from the standard OTLP endpoint env vars via `consts.ResolveOTLPEndpoint` (logs-signal `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` wins over the generic `OTEL_EXPORTER_OTLP_ENDPOINT`). Returns nil when no endpoint is set so the caller runs file-only. Secure by default: bare `host:port` and `https://` resolve to TLS; only an explicit `http://` opts in to plaintext, so a misconfigured prod endpoint cannot silently downgrade. mTLS material is NOT read from env — a trusted-lane caller (`clawkercp`) wires the in-process `OtelOptions.TLSConfig` shape separately so the leaf never lands on disk, and env-driven cert paths are deliberately not honored (an env-supplied CLI-root-direct leaf, which agent containers also hold, could otherwise forge `service.name=clawkercp` records on the trusted receiver).

## Methods

### Logging

```go
func (l *Logger) Debug() *zerolog.Event
func (l *Logger) Info()  *zerolog.Event
func (l *Logger) Warn()  *zerolog.Event
func (l *Logger) Error() *zerolog.Event
func (l *Logger) Fatal() *zerolog.Event  // NEVER use in Cobra hooks — return errors instead
```

### Context

```go
func (l *Logger) With(keyvals ...interface{}) *Logger
```

Returns a new `*Logger` with additional structured fields. Accepts alternating key/value pairs where keys must be strings. Panics on odd argument count or non-string key.

```go
projectLog := log.With("project", "foo", "agent", "bar")
projectLog.Info().Msg("started")
```

### Interop

```go
func (l *Logger) Zerolog() zerolog.Logger  // underlying zerolog.Logger for libraries
func (l *Logger) LogFilePath() string      // log file path (empty for Nop)
```

### Lifecycle

```go
func (l *Logger) Close(ctx context.Context) error  // flush OTEL (ctx is the flush deadline — a canceled/expired ctx unwinds the export immediately) + close file writer; returns the true shutdown outcome (does NOT swallow ctx errors — the caller interprets a cancellation it requested); safe to call multiple times
```

## Factory Integration

Commands access logger through `f.Logger` (Factory lazy noun):

```go
// In NewCmdFoo:
opts.Logger = f.Logger

// In fooRun:
log, err := opts.Logger()
if err != nil {
    return fmt.Errorf("initializing logger: %w", err)
}
log.Debug().Str("key", "val").Msg("diagnostic info")
```

Library packages accept `*logger.Logger` in constructors:

```go
func NewClient(ctx context.Context, cfg config.Config, log *logger.Logger, opts ...Option) (*Client, error)
```

Tests use `logger.Nop()`:

```go
f := &cmdutil.Factory{
    Logger: func() (*logger.Logger, error) { return logger.Nop(), nil },
}
```

## OTEL Resilience

The OTEL SDK handles resilience natively — no custom health checking is needed:
- `BatchProcessor` buffers in a ring buffer (configurable `MaxQueueSize`)
- Retries on transient failures (429, 503, 504)
- Buffer overflow drops oldest entries (no OOM, no blocking)
- Collector down at startup: buffer, retry, drop. Comes up later: auto-recovers.
- **Custom error handler**: `otel.SetErrorHandler()` redirects OTEL SDK internal errors to the file logger via `l.zl.Warn()` instead of stderr.

## Test Coverage

`logger_test.go` — tests for `New`, `Nop`, `Close` (idempotent), `With` context, `LogFilePath`, file output verification, no-console-output verification.

## Key Rules

- **Never** use `logger.Fatal()` in Cobra hooks — return errors instead
- Zerolog is for **file logging only** — never for user-visible output
- Commands access logger via `f.Logger` (Factory noun) — never import logger package directly for calling methods in command code
- Library packages accept `*logger.Logger` in constructors — never use globals
- Tests use `logger.Nop()` — no special test infrastructure needed
- Don't swallow `opts.Logger()` errors — always check and return them
- Log path: `cfg.LogsSubdir()/clawker.log`
- File rotation via lumberjack: 50MB size, 7 days age, 3 backups, gzip compression (defaults)
- OTEL bridge is optional — set `Otel` in `Options` to enable dual-destination logging

## Dependencies

`zerolog` (structured logging), `lumberjack` (rotation), `otlploggrpc` (OTLP/gRPC exporter), `otel/sdk/log` (LoggerProvider), `google.golang.org/grpc/credentials` (mTLS for the trusted-infra receiver).

# Logging Brainstorm (2026-02-15)

## Decisions Made
- **Compress rotated logs**: Enable lumberjack `Compress: true` by default. Active `clawker.log` stays plain text; only rotated backups get gzipped.
- **No user-facing config yet**: Compression is internal — developers/maintainers control it, not end users. No need to expose `compress` in `settings.yaml` right now.
- **Keep current rotation defaults**: 50MB max size, 3 backups, 7 days retention — unchanged.

## Dual-Destination Logging (Brainstorm)
- **Goal**: Always log to file, also stream to monitoring stack (Loki) if available.
- **Monitoring stack**: Docker Compose with otel-collector (4318), Loki (3100), Prometheus, Grafana.
- **Existing detection**: `docker.Client.IsMonitoringActive()` checks otel-collector container — but requires Docker client, not available at logger init time.
- **Loki ports**: `localhost:3100` (direct), `localhost:4318` (via otel-collector). Both port-mapped from Docker.
- **Approach options**:
  1. Simple HTTP ping at init (`localhost:3100/ready` or `localhost:4318`) + `io.MultiWriter` (zerolog native, no OTEL SDK)
  2. Lazy dual-writer — file first, add monitoring writer later when Docker client is available
- **Chosen approach**: JIT dual-writer — custom `io.Writer` wrapping lumberjack + monitoring writer. Background goroutine pings health endpoint every ~30s, updates `atomic.Bool`. `Write()` checks bool (near-zero cost), always writes to file, best-effort writes to monitoring.
- **Pattern**: `DualWriter` struct with `file io.Writer`, `monitor io.Writer`, `available atomic.Bool`. Never blocks, never errors on monitoring side.
- **NewLogger pattern**: `NewLogger(opts *LoggerOptions)` constructor sets global `Log`. Call sites unchanged. Tests inject mocks.
- **Option (b) confirmed**: Constructor sets global, no caller refactor needed.
- **OtelService interface**: `Connect()`, `Healthy()`, `Send()` — abstracts monitoring transport. Nil = no-op (graceful).
- **OTEL vs Loki direct**: Still open. OTEL collector aligns with pipeline, future-proof. Loki direct is simpler.
- **Construction site**: `initializeLogger()` in `internal/cmd/root/root.go` — already has settings + logsDir. OtelService built there too (just needs `localhost:4318`, no Docker client).
- **Lifecycle**: `internal/clawker/cmd.go` owns cleanup via `defer logger.Close()` (flush file + shutdown OtelService).

## Architecture Decisions

### Two-Layer Logger Strategy
- **Command layer (~30 files)**: NEW — uses `ios.Logger()` via IOStreams. Testable, DI-friendly.
- **Library layer (~28 files)**: LEGACY — keeps using `logger.Debug()` global. No changes. These are deep packages (docker, workspace, hostproxy, socketbridge, config) that don't have IOStreams and shouldn't.

### IOStreams Logger via Interface
- IOStreams defines a `Logger` interface + `LogEvent` interface (small, no external deps).
- IOStreams NEVER imports `internal/logger`. Zero coupling.
- `internal/logger` implements the interface (zerolog, lumberjack, otel).
- Factory wires it: `ios.SetLogger(logger.NewLogger(opts))` in `ioStreams()` helper.
- Command layer: `ios.Logger().Debug().Msg(...)`.
- Library layer: `logger.Debug().Msg(...)` (global, unchanged).
- Same underlying logger instance, two access paths, single file writer, single otel connection.

### Construction Site
- Factory's `ioStreams()` helper in `internal/cmd/factory/default.go`.
- Constructs `internal/logger` with settings + otel, assigns to IOStreams via interface.
- Factory is pure DI — `ioStreams()` constructs the IOStreams noun, logger is part of that noun.

### OTEL Integration via otelzerolog Bridge
- **Package**: `go.opentelemetry.io/contrib/bridges/otelzerolog` — official OTEL contrib.
- **How it works**: Provides a `zerolog.Writer` that converts zerolog events → OTLP LogRecords (severity mapping, fields→attributes, timestamps). Feeds into OTEL `LoggerProvider`.
- **Writer setup**: `zerolog.New(io.MultiWriter(lumberjack, otelzerolog.NewWriter(loggerProvider)))` — no custom DualWriter needed.
- **LoggerProvider**: Configured with `otlploghttp` exporter → `localhost:4318` (otel-collector).
- **No custom OtelService interface needed** — OTEL SDK's LoggerProvider is the abstraction.
- **No `internal/telemetry/` package needed** — OTEL SDK setup lives in `internal/logger`.
- **Health/availability**: OTEL SDK handles everything natively:
  - BatchProcessor buffers in ring buffer (configurable size, default 2048)
  - Exporter retries on transient failures (429, 503, 504)
  - Buffer overflow → oldest logs dropped (no OOM, no blocking)
  - App never blocked — exports in background goroutine
  - Collector down at startup → buffer, retry, drop. Comes up later → logs flow.
  - Collector dies mid-session → same graceful degradation, auto-recovers.
  - NO custom health checking, atomic.Bool, background ping, or DualWriter needed.
- **Config knobs**: WithTimeout, WithRetry, WithMaxQueueSize, WithExportInterval, WithExportMaxBatchSize — all values from config, not hardcoded.

### Config as Single Source of Truth
- `internal/config` owns ALL monitoring/otel constants and defaults.
- Defaults mirror current monitoring stack values (otel-collector 4318, Loki 3100, etc.).
- Overridable via `settings.yaml` otel section, then env vars (OTEL SDK reads env natively).
- Precedence: env vars > settings.yaml > config defaults.
- `internal/monitor` compose templates render with values from config (no hardcoded ports).
- `internal/logger` otel exporter reads config for collector endpoint.
- If a port/endpoint changes in settings, both compose stack and logger see the same value.
- **Refactor needed — three consumers of hardcoded OTEL values**:
  1. `internal/monitor/templates.go` — compose ports, service names
  2. `internal/bundler/assets/Dockerfile.tmpl` — container OTEL env vars (lines 274-286)
  3. `internal/logger` — host-side OTLP exporter endpoint
  All three must read from config. Config defaults mirror current hardcoded values.
- **Shutdown**: `loggerProvider.Shutdown(ctx)` flushes pending logs, called from `logger.Close()`.
- **Tests**: IOStreams tests use mock Logger interface. Logger tests can use `logtest.NewRecorder()` from OTEL SDK for verifying OTLP records.

### Lifecycle
- `defer logger.Close()` in `cmd.go` handles file + otel shutdown.

## Open Items
- User may have more logging tweaks to discuss.

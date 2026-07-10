# Monitor Command Package

Manage local observability stack (OpenTelemetry Collector + OpenSearch / OpenSearch Dashboards for logs + traces + Prometheus for metrics).

## Files

| File | Purpose |
|------|---------|
| `monitor.go` | `NewCmdMonitor(f)` — parent command |
| `init/init.go` | `NewCmdInit(f, runF)` — scaffold monitoring config files (resolves units, prints per-unit provenance, renders active set) |
| `up/up.go` | `NewCmdUp(f, runF)` — render + start observability stack (option-D: merges cwd projection into the host ledger, renders collector config over the seeded union, force-recreates the collector only when otel-config bytes changed) |
| `down/down.go` | `NewCmdDown(f, runF)` — stop observability stack |
| `status/status.go` | `NewCmdStatus(f, runF)` — show stack status |

## Key Symbols

```go
func NewCmdMonitor(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages.

## Subcommands

### monitor init

```go
type InitOptions struct {
    IOStreams *iostreams.IOStreams
    Config    func() (config.Config, error)
    Logger    func() (*logger.Logger, error)
    Force     bool
}
func NewCmdInit(f *cmdutil.Factory, runF func(context.Context, *InitOptions) error) *cobra.Command
```

Scaffolds monitoring stack config files (`compose.yaml`, `otel-config.yaml`, `prometheus.yaml`) plus the `opensearch-bootstrap/` asset tree (bootstrap.sh, component templates, ingest pipelines, index templates, ISM policies, datasources, Dashboards saved objects) in `cfg.MonitorSubdir()` via `monitor.WriteOpenSearchBootstrap`. Flags: `--force/-f` (overwrite existing).

### monitor up

```go
type UpOptions struct {
    IOStreams *iostreams.IOStreams
    Client    func(context.Context) (*docker.Client, error)
    Config    func() (config.Config, error)
    Logger    func() (*logger.Logger, error)
    Detach    bool
}
func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command
```

Starts monitoring stack via Docker Compose. Ensures the clawker network exists. The one-shot `clawker-opensearch-bootstrap` service runs first (after OpenSearch reaches `service_healthy`) and applies index templates / ISM policies / Dashboards saved objects; `otel-collector` and `prometheus` gate on its `service_completed_successfully` so they never start against an unprovisioned cluster. Flags: `--detach` (default: true).

### monitor down

```go
type DownOptions struct {
    IOStreams *iostreams.IOStreams
    Config    func() (config.Config, error)
    Logger    func() (*logger.Logger, error)
    Volumes   bool
}
func NewCmdDown(f *cmdutil.Factory, runF func(context.Context, *DownOptions) error) *cobra.Command
```

Stops monitoring stack via Docker Compose. Flags: `--volumes/-v` (remove named volumes). With `--volumes` it also deletes the seeded-unit ledger (`units-ledger.yaml`), since the REST state it tracked is wiped.

Monitoring-unit selection has no dedicated commands: a project selects extensions by name in `monitor.extensions` (clawker.yaml), and `monitor up` seeds them. The register-era `units/` commands (register/remove/list/enable/disable + the `settings.yaml monitoring.units` registry) are gone; `bundle list` shows monitoring-component provenance.

### monitor status

```go
type StatusOptions struct {
    IOStreams *iostreams.IOStreams
    Config    func() (config.Config, error)
    Logger    func() (*logger.Logger, error)
}
func NewCmdStatus(f *cmdutil.Factory, runF func(context.Context, *StatusOptions) error) *cobra.Command
```

Shows monitoring stack status (running/stopped), container details, and service URLs.

## Config Access Pattern

Subcommands use `config.Config` interface via `opts.Config()` (multi-return). Monitor directory resolved via `cfg.MonitorSubdir()`, network name via `cfg.ClawkerNetwork()`, in-cluster service URLs via `cfg.OpenSearchURL()` / `cfg.OpenSearchDashboardsURL()` / `cfg.PrometheusURL()` (zero-arg; returns clawker network hostnames for in-network consumers). Host-facing URLs printed to the user are formatted as `http://localhost:<port>` directly from `cfg.SettingsStore().Read().Monitoring` ports.

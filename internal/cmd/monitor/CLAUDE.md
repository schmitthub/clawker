# Monitor Command Package

Manage local observability stack (OpenTelemetry Collector + OpenSearch / OpenSearch Dashboards for logs + traces + Prometheus for metrics).

## Files

| File | Purpose |
|------|---------|
| `monitor.go` | `NewCmdMonitor(f)` — parent command |
| `init/init.go` | `NewCmdInit(f, runF)` — scaffold the base stack config files (floor only — zero extensions; projection is up/reload territory) |
| `up/up.go` | `NewCmdUp(f, runF)` — render + start observability stack (option-D: merges cwd projection into the host ledger, renders collector config over the seeded union; never restarts a running collector — warns and points at `monitor reload` when the rendered otel-config bytes changed) |
| `reload/reload.go` | `NewCmdReload(f, runF)` — explicit disruptive apply: re-render over the seeded union + this project's projection, stop+remove the collector, compose up, seed ledger |
| `shared/stack.go` | `PrepareStack`, `ComposeUp`, `RemoveCollector`, `CollectorRunning`, `RunComposeCmd` — stack plumbing shared by up/reload |
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

Scaffolds the BASE stack config files (`compose.yaml`, `otel-config.yaml`, `prometheus.yaml`) plus the `opensearch-bootstrap/` asset tree in `cfg.MonitorSubdir()` — floor only, zero monitoring extensions (init never resolves `monitor.extensions` and never touches the units ledger; projection is `monitor up`/`monitor reload` territory). Flags: `--force/-f` (overwrite existing).

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

Starts monitoring stack via Docker Compose. Ensures the clawker network exists. The one-shot `clawker-opensearch-bootstrap` service runs first (after OpenSearch reaches `service_healthy`) and applies index templates / ISM policies / Dashboards saved objects; `otel-collector` and `prometheus` gate on its `service_completed_successfully` so they never start against an unprovisioned cluster. `up` NEVER restarts a running collector — when the rendered otel-config bytes changed and the collector was already running, it warns and points at `monitor reload`. Flags: `--detach` (default: true).

### monitor reload

```go
type ReloadOptions struct {
    IOStreams     *iostreams.IOStreams
    Client        func(context.Context) (*docker.Client, error)
    Config        func() (config.Config, error)
    Logger        func() (*logger.Logger, error)
    BundleManager func() (*bundle.Manager, error)
}
func NewCmdReload(f *cmdutil.Factory, runF func(context.Context, *ReloadOptions) error) *cobra.Command
```

The explicit disruptive apply: re-renders the stack config over the seeded union + this project's projection, unconditionally stops and removes `otel-collector` (compose never recreates on bind-mount content change), composes the stack back up detached, then seeds the ledger. Use after editing `monitor.extensions` while the stack is running. No flags.

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

Monitoring-unit selection has no dedicated commands: a project selects extensions by name in `monitor.extensions` (clawker.yaml; NO default selection — every extension, including the floor claude-code unit, is an explicit opt-in), and `monitor up` seeds them (`monitor reload` applies an edit to a running stack). The register-era `units/` commands (register/remove/list/enable/disable + the `settings.yaml monitoring.units` registry) are gone; `bundle list` shows monitoring-component provenance.

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

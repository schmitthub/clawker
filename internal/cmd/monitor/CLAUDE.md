# Monitor Command Package

Manage local observability stack (OpenTelemetry, Jaeger, Prometheus, Grafana).

## Files

| File | Purpose |
|------|---------|
| `monitor.go` | `NewCmdMonitor(f)` — parent command |
| `init/init.go` | `NewCmdInit(f, runF)` — scaffold monitoring config files |
| `up/up.go` | `NewCmdUp(f, runF)` — start observability stack |
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
    Config    func() config.Provider
    Force     bool
}
func NewCmdInit(f *cmdutil.Factory, runF func(context.Context, *InitOptions) error) *cobra.Command
```

Scaffolds monitoring stack config files in `~/.clawker/monitor/`. Flags: `--force/-f` (overwrite existing).

### monitor up

```go
type UpOptions struct {
    IOStreams *iostreams.IOStreams
    Client    func(context.Context) (*docker.Client, error)
    Config    func() config.Provider
    Detach    bool
}
func NewCmdUp(f *cmdutil.Factory, runF func(context.Context, *UpOptions) error) *cobra.Command
```

Starts monitoring stack via Docker Compose. Ensures `clawker-net` network exists. Flags: `--detach` (default: true).

### monitor down

```go
type DownOptions struct {
    IOStreams *iostreams.IOStreams
    Volumes   bool
}
func NewCmdDown(f *cmdutil.Factory, runF func(context.Context, *DownOptions) error) *cobra.Command
```

Stops monitoring stack via Docker Compose. Flags: `--volumes/-v` (remove named volumes).

### monitor status

```go
type StatusOptions struct {
    IOStreams *iostreams.IOStreams
    Config    func() config.Provider
}
func NewCmdStatus(f *cmdutil.Factory, runF func(context.Context, *StatusOptions) error) *cobra.Command
```

Shows monitoring stack status (running/stopped), container details, and service URLs.

## Config Access Pattern

Subcommands use `config.Provider` gateway: `opts.Config().UserSettings().Monitoring` for URLs/ports.

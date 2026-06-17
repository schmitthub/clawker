# Factory Package

The factory wiring layer. Constructs a fully-wired `*cmdutil.Factory` with
real dependency implementations. Called exactly once at the CLI entry point.

## Domain: Clawker-Specific Configuration

**Responsibility**: Wire IOStreams from the standard terminal layer; does not override terminal env vars.

The factory `ioStreams()` helper calls `iostreams.System()` and returns the result unchanged. It does NOT handle standard terminal env vars — those belong in lower layers.

| Layer | Package | Responsibility | Env Vars |
|-------|---------|----------------|----------|
| Capabilities | `term` | What the terminal supports | `TERM`, `COLORTERM`, `NO_COLOR` |
| Behavior | `iostreams` | Terminal UX (theme, progress, paging) | `CLAWKER_PAGER`, `PAGER` |
| **App Config** | `factory` | Clawker-specific wiring | (none currently) |

The cascade: `term.FromEnv()` → `iostreams.System()` → `factory.ioStreams()`

## Key File

| File | Purpose |
|------|---------|
| `default.go` | `New(version)` — constructs Factory with all lazy closures |

## Usage

```go
// Entry point only (internal/clawker/cmd.go)
f := factory.New(build.Version)
rootCmd, err := root.NewCmdRoot(f, build.Version, build.Date)
```

**Tests NEVER import this package.** Tests construct minimal Factory structs:
```go
tio, _, _, _ := iostreams.Test()
f := &cmdutil.Factory{IOStreams: tio, TUI: tui.NewTUI(tio), Version: "1.0.0"}
```

## Extracted Helper Pattern

`New()` delegates to extracted helper functions for each Factory field:
- `ioStreams()` -- creates IOStreams via `iostreams.System()` (eager, no Config dependency)
- `tuiFunc(f)` -- creates TUI struct bound to IOStreams (eager, separate helper in `default.go`)
- `clientFunc(f)` -- returns lazy Docker client constructor; closes over `f.Config()` to pass `*config.Config` to `docker.NewClient`
- `projectRegistryFunc()` -- returns lazy `*project.Registry` constructor (`project.NewRegistry()`); the sole production constructor of registry storage, shared by Config, GitManager, ProjectManager, and commands via `f.ProjectRegistry`
- `configFunc(f)` -- returns lazy `config.Config` gateway constructor (lazy-loads project + settings stores; the registry is touched only through `f.ProjectRegistry().CurrentRoot()` for the walk-up anchor). Resolves the project root at the call site and passes it to `config.NewConfig(config.WithProjectRoot(root))` to bound project-config walk-up (empty root → walk-up disabled)
- `gitManagerFunc(f)` -- returns lazy git manager constructor; uses the project root from `f.ProjectRegistry().CurrentRoot()`
- `hostProxyFunc(f)` -- returns lazy host proxy manager constructor
- `adminClientFunc(f)` -- returns a lazy `adminv1.AdminServiceClient` constructor; closes over `f.Config()` only. Pure dial — does NOT bootstrap the CP (CP lifecycle lives in `controlPlaneFunc` / `cpboot.Manager`; CP is brought up by agent-container start flows and the explicit `clawker controlplane up` / `clawker firewall up` verbs). Reads `cp.AdminPort` / `cp.HydraPublicPort` from settings and calls `adminclient.Dial(ctx, adminPort, hydraPort, grpc.WithKeepaliveParams(...))` with mTLS + OAuth2 JWT; subsequent calls return the cached `grpc.ClientConn` unless it has entered `TransientFailure`/`Shutdown`, in which case the closure closes the conn and rebuilds. Admin commands invoked when the CP is down fail fast. No test seams — callers substitute via `AdminServiceClient` mocks at the Factory level (`cpmocks.AdminServiceClientMock`). No raw moby client.
- `controlPlaneFunc(f)` -- returns a `sync.Once`-cached `func() cpboot.Manager` that constructs a single `cpboot.NewManager(f.Client, f.Config, f.Logger)` per Factory. The `Manager` holds lazy Factory closures, not eagerly resolved Docker/Config/Logger values, so a caller that never touches the CP never resolves them. Consumed by the break-glass verbs in `internal/cmd/controlplane/` and intended for any future caller that needs to drive the CP lifecycle without hitting the AdminService.
- `socketBridgeFunc(f)` -- returns lazy `socketbridge.SocketBridgeManager` constructor (wraps `socketbridge.NewManager()`)
- `prompterFunc(f)` -- returns lazy prompter constructor
- `projectManagerFunc(f)` -- returns lazy `project.ProjectManager` constructor; resolves Config, Logger, and ProjectRegistry, then calls `project.NewProjectManager(log, nil, cfg.Project().Name, reg)`, passing the `clawker.yaml` `name:` override down as a primitive. The edge is strictly one-way (PM reads config; config never reads PM — its walk-up anchor comes from the shared registry facade), so there is no cycle
- `httpClientFunc()` -- returns a lazy `func() (*http.Client, error)` with a 30s timeout; shared by npm registry lookups (Claude Code version resolution), the update checker, and the changelog teaser. The `error` return is **reserved** (constructing a plain client is infallible today) so a future fallible transport — custom CA bundle, proxy resolution, auth round-tripper — can surface failures without a signature change; until then it is always nil.
- `cliStateFunc()` -- returns a lazy `func() (state.StateStore, error)` (`state.New()`, `sync.Once`-cached, no dependencies); the CLI runtime-state facade (update-check cache + changelog cursor) consumed by the background notifications in `Main`. The error is real — `state.New()` can fail on a disk/migration error.

Each helper is a standalone function in `default.go`, making the wiring easy to read and test.

All closures use `sync.Once` for lazy single-initialization within the `config.Config` gateway or within the helper closures themselves.

**Dependency ordering in `New()`**: Config is constructed first, then `loggerLazy(f)` (needs `f.Config()` for settings), then `ioStreams()` (no Config dependency), then IOStreams-dependent fields (TUI, Prompter).

## Logger Initialization

Logger initialization happens inside `loggerLazy(f)` (separate from `ioStreams()`):
1. Reads `cfg.SettingsStore().Read().Logging` for file/OTEL config
2. Calls `logger.New(opts)` with file config (rotation, compression) and optional OTEL config
3. Returns `logger.Nop()` if file logging is explicitly disabled via settings
4. Logger is a separate Factory lazy noun (`f.Logger`), not part of IOStreams

## Environment Variables

Standard terminal env vars (`TERM`, `COLORTERM`, `NO_COLOR`) are handled by `term.FromEnv()`. The factory itself reads no additional env vars; clawker-specific runtime behavior (e.g. spinner mode) is controlled programmatically via `IOStreams` setters or settings.

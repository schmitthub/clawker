# Factory Package

The factory wiring layer. Constructs a fully-wired `*cmdutil.Factory` with
real dependency implementations. Called exactly once at the CLI entry point.

## Domain: Clawker-Specific Configuration

**Responsibility**: Apply clawker-specific environment configuration on top of standard terminal behavior.

The factory `ioStreams()` helper calls `iostreams.System()` then applies clawker-specific config. It does NOT handle standard terminal env vars — those belong in lower layers.

| Layer | Package | Responsibility | Env Vars |
|-------|---------|----------------|----------|
| Capabilities | `term` | What the terminal supports | `TERM`, `COLORTERM`, `NO_COLOR` |
| Behavior | `iostreams` | Terminal UX (theme, progress, paging) | `CLAWKER_PAGER`, `PAGER` |
| **App Config** | `factory` | Clawker-specific preferences | `CLAWKER_SPINNER_DISABLED` |

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
tio := iostreamstest.New()
f := &cmdutil.Factory{IOStreams: tio.IOStreams, TUI: tui.NewTUI(tio.IOStreams), Version: "1.0.0"}
```

## Extracted Helper Pattern

`New()` delegates to extracted helper functions for each Factory field:
- `ioStreams(f)` -- creates IOStreams + initializes logger (eager, needs `f.Config()` for settings)
- `tui.NewTUI(ios)` -- creates TUI struct bound to IOStreams (eager, inline in `New()`)
- `clientFunc(f)` -- returns lazy Docker client constructor; closes over `f.Config()` to pass `*config.Config` to `docker.NewClient`
- `configFunc()` -- returns lazy `*config.Config` gateway constructor (the gateway itself uses `os.Getwd()` internally and lazy-loads Project, Settings, Resolution, Registry via `sync.Once`)
- `gitManagerFunc(f)` -- returns lazy git manager constructor; uses project root from `f.Config().Project.RootDir()`
- `hostProxyFunc()` -- returns lazy host proxy manager constructor
- `socketBridgeFunc()` -- returns lazy `socketbridge.SocketBridgeManager` constructor (wraps `socketbridge.NewManager()`)
- `prompterFunc(ios)` -- returns lazy prompter constructor

Each helper is a standalone function in `default.go`, making the wiring easy to read and test.

All closures use `sync.Once` for lazy single-initialization within the `config.Config` gateway or within the helper closures themselves.

**Dependency ordering in `New()`**: Config is constructed first, then `ioStreams(f)` (needs `f.Config().Settings` for logger init), then IOStreams-dependent fields (TUI, Prompter).

## Logger Initialization

Logger initialization happens inside `ioStreams(f)`:
1. Reads `f.Config().Settings` (Viper already resolved ENV > config > defaults)
2. Calls `logger.NewLogger()` with file config (rotation, compression) and optional OTEL config
3. Sets `ios.Logger = &logger.Log` (`*zerolog.Logger` satisfies `iostreams.Logger`)
4. Falls back to `logger.Init()` (nop) if `LogsDir()` fails

Previously, logger init lived in `root.go`'s `initializeLogger()` — now consolidated into the factory.

## Environment Variables

| Variable | Effect |
|----------|--------|
| `CLAWKER_SPINNER_DISABLED` | Static text instead of animated spinner |

Note: Standard terminal env vars (`TERM`, `COLORTERM`, `NO_COLOR`) are handled by `term.FromEnv()`. Factory handles only clawker-specific config.

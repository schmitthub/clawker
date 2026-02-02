# Factory Package

The factory wiring layer. Constructs a fully-wired `*cmdutil.Factory` with
real dependency implementations. Called exactly once at the CLI entry point.

## Key File

| File | Purpose |
|------|---------|
| `default.go` | `New(version, commit)` — constructs Factory with all lazy closures |

## Usage

```go
// Entry point only (internal/clawker/cmd.go)
f := factory.New(Version, Commit)
rootCmd := root.NewCmdRoot(f)
```

**Tests NEVER import this package.** Tests construct minimal Factory structs:
```go
tio := iostreams.NewTestIOStreams()
f := &cmdutil.Factory{IOStreams: tio.IOStreams, Version: "1.0.0"}
```

## Extracted Helper Pattern

`New()` delegates to extracted helper functions for each Factory field:
- `ioStreams()` -- creates IOStreams (eager)
- `workDirFunc()` -- returns lazy `func() string` closure
- `clientFunc(f)` -- returns lazy Docker client constructor; closes over `f.Config()` to pass `*config.Config` to `docker.NewClient`
- `configFunc(workDirFn)` -- returns lazy `*config.Config` gateway constructor (the gateway itself lazy-loads Project, Settings, Resolution, Registry internally via `sync.Once`)
- `hostProxyFunc()` -- returns lazy host proxy manager constructor
- `prompterFunc(ios)` -- returns lazy prompter constructor

Each helper is a standalone function in `default.go`, making the wiring easy to read and test.

All closures use `sync.Once` for lazy single-initialization within the `config.Config` gateway or within the helper closures themselves.

**Dependency ordering in `New()`**: `Config` must be assigned before `Client` because `clientFunc(f)` reads `f.Config()` at call time.

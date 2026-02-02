# Cmdutil Package

Lightweight shared CLI utilities: Factory struct (DI container), output helpers, argument validators.

Heavy command helpers have been extracted to dedicated packages:
- Image resolution → `internal/resolver/`
- Build utilities → `internal/build/`
- Project registration → `internal/project/`
- Container naming → `internal/docker/`

## Key Files

| File | Purpose |
|------|---------|
| `factory.go` | `Factory` — pure struct with closure fields (no methods, no construction logic) |
| `output.go` | `HandleError`, `PrintError`, `PrintNextSteps`, etc. (iostreams only) |
| `required.go` | `NoArgs`, `ExactArgs`, `AgentArgsValidator` (cobra only) |
| `project.go` | `ErrAborted` (stdlib only) |

## Factory (`factory.go`)

Pure dependency injection container struct. Closure fields are wired by `internal/cmd/factory/default.go`.

```go
type Factory struct {
    WorkDir, BuildOutputDir string
    Debug                   bool
    Version, Commit         string
    IOStreams                *iostreams.IOStreams

    // Closure fields (wired by factory constructor, lazy internally)
    Client      func(context.Context) (*docker.Client, error)
    CloseClient func()
    Config      func() (*config.Config, error)
    ResetConfig func()
    // ... 16 closure fields total
}
```

**Closure Fields:**
- `Client(ctx)` — Docker client (`*docker.Client`)
- `CloseClient()` — Close Docker client
- `ConfigLoader()` — Config loader (`*config.Loader`)
- `Config()` — Project config (`*config.Config`)
- `ResetConfig()` — Clear config cache
- `SettingsLoader()` — Settings loader (`*config.SettingsLoader`)
- `Settings()` — User settings (`*config.Settings`)
- `InvalidateSettingsCache()` — Clear settings cache
- `RegistryLoader()` — Project registry loader (`*config.RegistryLoader`)
- `Registry()` — Project registry (`*config.ProjectRegistry`)
- `Resolution()` — Project resolution (`*config.Resolution`)
- `HostProxy()` — Host proxy manager
- `EnsureHostProxy()` — Start host proxy if needed
- `StopHostProxy(ctx)` — Stop host proxy
- `HostProxyEnvVar()` — Proxy env var for containers
- `Prompter()` — Interactive prompter
- `BuildKitEnabled(ctx)` — Detect BuildKit support (env var > daemon ping > OS)

**Testing:** Construct minimal Factory structs directly:
```go
tio := iostreams.NewTestIOStreams()
f := &cmdutil.Factory{
    Version:  "1.0.0",
    Commit:   "abc123",
    IOStreams: tio.IOStreams,
}
```

## Error Handling (`output.go`)

- `HandleError(ios, err)` — format errors for users (duck-typed `FormatUserError()` interface)
- `PrintError(ios, format, args...)` — print error to stderr
- `PrintWarning(ios, format, args...)` — print warning to stderr
- `PrintNextSteps(ios, steps...)` — print next-steps guidance to stderr
- `PrintStatus(ios, quiet, format, args...)` — print status (suppressed with --quiet)
- `OutputJSON(ios, data)` — marshal to stdout as indented JSON
- `PrintHelpHint(ios, cmdPath)` — contextual help hint

### ExitError (`output.go`)

Type for propagating non-zero container exit codes through Cobra's error chain. Allows deferred cleanup (terminal restore, container removal) to run before `os.Exit()`.

```go
type ExitError struct { Code int }
func (e *ExitError) Error() string // "exit status <N>"
```

Commands return `&ExitError{Code: status}` instead of calling `os.Exit()` directly. The root command's `Execute()` checks for `ExitError` and calls `os.Exit(code)` after all defers have run. This is critical because `os.Exit()` does **not** run deferred functions — returning `ExitError` ensures terminal state is restored before exit.

## Argument Validators (`required.go`)

- `NoArgs` — error if any args provided
- `RequiresMinArgs(n)` — error if fewer than n args
- `RequiresMaxArgs(n)` — error if more than n args
- `RequiresRangeArgs(min, max)` — error if out of range
- `ExactArgs(n)` — error if not exactly n args
- `AgentArgsValidator(minArgs)` — `--agent` flag mutually exclusive with positional args
- `AgentArgsValidatorExact(n)` — same but requires exactly n args without `--agent`

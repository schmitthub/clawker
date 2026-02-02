# Cmdutil Package

Lightweight shared CLI utilities: Factory struct (DI container), output helpers, argument validators.

Heavy command helpers have been extracted to dedicated packages:
- Image resolution: `internal/docker/` (image_resolve.go)
- Build utilities: `internal/build/`
- Project registration: `internal/project/`
- Container naming/middleware: `internal/docker/`

## Key Files

| File | Purpose |
|------|---------|
| `factory.go` | `Factory` -- pure struct with closure fields (no methods, no construction logic) |
| `output.go` | `HandleError`, `PrintError`, `PrintNextSteps`, `PrintWarning`, `PrintStatus`, `OutputJSON`, `PrintHelpHint`, `ExitError` |
| `required.go` | `NoArgs`, `ExactArgs`, `RequiresMinArgs`, `RequiresMaxArgs`, `RequiresRangeArgs`, `AgentArgsValidator`, `AgentArgsValidatorExact` |
| `project.go` | `ErrAborted` sentinel (stdlib only) |

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
    // ... 18 closure fields total (see source for full list)
}
```

**Closure Fields:**
- `Client(ctx)`, `CloseClient()` -- Docker client lifecycle
- `ConfigLoader()`, `Config()`, `ResetConfig()` -- project config
- `SettingsLoader()`, `Settings()`, `InvalidateSettingsCache()` -- user settings
- `RegistryLoader()`, `Registry()`, `Resolution()` -- project registry
- `HostProxy()`, `EnsureHostProxy()`, `StopHostProxy(ctx)`, `HostProxyEnvVar()` -- host proxy
- `Prompter()` -- interactive prompter (`*prompter.Prompter`)
- `RuntimeEnv()` -- runtime environment variables
- `BuildKitEnabled(ctx)` -- detect BuildKit support (env var > daemon ping > OS)

**Testing:** Construct minimal Factory structs directly:
```go
tio := iostreams.NewTestIOStreams()
f := &cmdutil.Factory{
    Version:  "1.0.0",
    Commit:   "abc123",
    IOStreams: tio.IOStreams,
}
```

## Error Handling & Output (`output.go`)

`HandleError(ios, err)` -- format errors for users (duck-typed `FormatUserError()` interface)
`PrintError(ios, format, args...)` -- print "Error: ..." to stderr
`PrintWarning(ios, format, args...)` -- print "Warning: ..." to stderr
`PrintNextSteps(ios, steps...)` -- print numbered next-steps guidance to stderr
`PrintStatus(ios, quiet, format, args...)` -- print status message (suppressed with --quiet)
`OutputJSON(ios, data) error` -- marshal to stdout as indented JSON
`PrintHelpHint(ios, cmdPath)` -- print "Run '<cmd> --help' for more information" to stderr

### ExitError

Type for propagating non-zero container exit codes through Cobra's error chain. Allows deferred cleanup (terminal restore, container removal) to run before `os.Exit()`.

```go
type ExitError struct { Code int }
func (e *ExitError) Error() string // "exit status <N>"
```

Commands return `&ExitError{Code: status}` instead of calling `os.Exit()` directly. The root command's `Execute()` checks for `ExitError` and calls `os.Exit(code)` after all defers have run. Critical because `os.Exit()` does **not** run deferred functions.

## Argument Validators (`required.go`)

All return `cobra.PositionalArgs` (except `NoArgs` which is one directly).

**Standard validators:**
- `NoArgs` -- error if any args provided (also handles "unknown command" for parent commands)
- `ExactArgs(n)` -- error if not exactly n args
- `RequiresMinArgs(n)` -- error if fewer than n args
- `RequiresMaxArgs(n)` -- error if more than n args
- `RequiresRangeArgs(min, max)` -- error if outside [min, max] range

**Agent-aware validators** (for commands with `--agent` flag):
- `AgentArgsValidator(minArgs)` -- `--agent` mutually exclusive with positional args; requires minArgs without `--agent`
- `AgentArgsValidatorExact(n)` -- same but requires exactly n args without `--agent`

All validators include binary name, command path, and usage line in error messages.

## Sentinels (`project.go`)

`ErrAborted` -- returned when user cancels an interactive operation

## Tests

`required_test.go` -- unit tests for argument validators

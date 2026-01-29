# Cmdutil Package

Shared CLI utilities: Factory struct, project registration, name resolution, error handling.

## Key Files

| File | Purpose |
|------|---------|
| `factory.go` | `Factory` — pure struct with closure fields (no methods, no construction logic) |
| `register.go` | `RegisterProject` — shared helper for project registration |
| `resolve.go` | Image resolution, container/agent name resolution |
| `project.go` | Project utilities |

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

**Testing:** Construct minimal Factory structs directly:
```go
tio := iostreams.NewTestIOStreams()
f := &cmdutil.Factory{
    Version:  "1.0.0",
    Commit:   "abc123",
    IOStreams: tio.IOStreams,
}
```

## RegisterProject (`register.go`)

Shared helper used by both `project init` and `project register`:

```go
func RegisterProject(loader func() (*config.RegistryLoader, error), name, root string) error
```

## Name Resolution (`resolve.go`)

- `ResolveContainerName(resolution, agent)` — builds `clawker.project.agent` or `clawker.agent`
- `ResolveContainerNames(resolution, names)` — resolve multiple names
- `ResolveContainerNamesFromAgents(resolution, agents)` — resolve agent names to container names
- `ResolveImage` / `ResolveImageWithSource` / `ResolveAndValidateImage` — image resolution chain
- `FindProjectImage(ctx, client, project)` — find `clawker-<project>:latest` image
- `AgentArgsValidator` / `AgentArgsValidatorExact` — Cobra args validators

## Error Handling

- `HandleError(err)` — format Docker errors for users
- `PrintError(io, msg)` — print error to stderr
- `PrintNextSteps(io, steps)` — print next-steps guidance to stderr

### ExitError (`output.go`)

Type for propagating non-zero container exit codes through Cobra's error chain. Allows deferred cleanup (terminal restore, container removal) to run before `os.Exit()`.

```go
type ExitError struct { Code int }
func (e *ExitError) Error() string // "container exited with code <N>"
```

Commands return `&ExitError{Code: status}` instead of calling `os.Exit()` directly. The root command's `Execute()` checks for `ExitError` and calls `os.Exit(code)` after all defers have run. This is critical because `os.Exit()` does **not** run deferred functions — returning `ExitError` ensures terminal state is restored before exit.

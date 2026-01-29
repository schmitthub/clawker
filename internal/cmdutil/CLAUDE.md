# Cmdutil Package

Shared CLI utilities: Factory, project registration, name resolution, error handling.

## Key Files

| File | Purpose |
|------|---------|
| `factory.go` | `Factory` — lazy-loaded dependencies for all commands |
| `register.go` | `RegisterProject` — shared helper for project registration |
| `resolve.go` | Image resolution, container/agent name resolution |
| `project.go` | Project utilities |

## Factory (`factory.go`)

Central dependency provider with lazy initialization via `sync.Once`.

```go
type Factory struct {
    WorkDir       string
    IOStreams      *iostreams.IOStreams
    // ... version, debug fields

    // Lazy-loaded (each has a sync.Once + cached result + error)
    // Access via methods: Client(ctx), Config(), Settings(), etc.
}
```

**Lazy-loaded fields:**
- `Client(ctx)` — Docker client (`*docker.Client`)
- `Config()` — Project config (`*config.Config`)
- `ConfigLoader()` — Config loader (`*config.Loader`)
- `Settings()` — User settings (`*config.Settings`)
- `SettingsLoader()` — Settings loader (`*config.SettingsLoader`)
- `RegistryLoader()` — Project registry loader (`*config.RegistryLoader`)
- `Registry()` — Project registry (`*config.ProjectRegistry`)
- `Resolution()` — Project resolution (`*config.Resolution`)
- `HostProxy()` — Host proxy manager
- `Prompter()` — Interactive prompter

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

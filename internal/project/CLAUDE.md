# Project Package

Project registration in the user's project registry.

## Key Files

| File | Purpose |
|------|---------|
| `register.go` | `RegisterProject` — shared helper for project registration |

## RegisterProject (`register.go`)

```go
func RegisterProject(ios *iostreams.IOStreams, registryLoader config.Registry, workDir string, projectName string) (string, error)
```

Registers a project in `~/.local/clawker/projects.yaml`:
1. Calls `registryLoader.Register(projectName, workDir)` via the `config.Registry` interface
2. Returns the slug on success
3. On errors, prints warnings to stderr and returns the error

Used by `project init` and `project register` commands.

## Dependencies

Imports: `internal/config`, `internal/iostreams`, `internal/logger`

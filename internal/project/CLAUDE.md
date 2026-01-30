# Project Package

Project registration in the user's project registry.

## Key Files

| File | Purpose |
|------|---------|
| `register.go` | `RegisterProject` — shared helper for project registration |

## RegisterProject (`register.go`)

```go
func RegisterProject(ios *iostreams.IOStreams, registryLoader func() (*config.RegistryLoader, error), workDir string, projectName string) (string, error)
```

Registers a project in `~/.local/clawker/projects.yaml`:
1. Ensures settings file exists
2. Loads registry via provided loader function
3. Calls `rl.Register(projectName, workDir)`
4. Returns the slug on success

Used by `project init` and `project register` commands.

## Dependencies

Imports: `internal/config`, `internal/iostreams`, `internal/logger`

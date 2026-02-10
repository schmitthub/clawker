# Container Shared Package

Domain orchestration logic shared between container subcommands (`run/`, `create/`).

**Separation from `opts/`**: The `opts/` package holds CLI flag types to break import cycles. The `shared/` package holds domain logic that multiple subcommands call.

## API

### Container Init (`containerfs.go`)

One-time Claude config initialization for new containers. Called after `EnsureConfigVolumes` when the config volume was freshly created.

```go
import "github.com/schmitthub/clawker/internal/cmd/container/shared"

// Copy host config and/or credentials to config volume
err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
    ProjectName:      "myapp",
    AgentName:        "ralph",
    ContainerWorkDir: cfg.Workspace.RemotePath,
    ClaudeCode:       cfg.Agent.ClaudeCode,
    CopyToVolume:     client.CopyToVolume,
})

// Inject onboarding marker after ContainerCreate, before ContainerStart
err := shared.InjectOnboardingFile(ctx, shared.InjectOnboardingOpts{
    ContainerID:     containerID,
    CopyToContainer: shared.NewCopyToContainerFn(client),
})
```

### Image Rebuild (`image.go`)

Interactive rebuild flow for missing default images, shared between `run/` and `create/`.

```go
// Commands pass client.BuildDefaultImage as the BuildImage function.
err := shared.RebuildMissingDefaultImage(ctx, shared.RebuildMissingImageOpts{
    ImageRef:       resolvedImage.Reference,
    IOStreams:      ios,
    TUI:           opts.TUI,
    Prompter:       opts.Prompter,
    SettingsLoader: func() config.SettingsLoader { return cfgGateway.SettingsLoader() },
    BuildImage:     client.BuildDefaultImage,
    CommandVerb:    "run",
})
```

Non-interactive mode prints instructions and returns an error. Interactive mode prompts for rebuild confirmation, flavor selection, then builds with TUI progress display (or spinner fallback when TUI is nil).

### Types

| Type | Purpose |
|------|---------|
| `CopyToVolumeFn` | Function type matching `(*docker.Client).CopyToVolume` |
| `CopyToContainerFn` | Simplified function type for tar-to-container copy |
| `InitConfigOpts` | Project/agent names, `ContainerWorkDir`, `*config.ClaudeCodeConfig`, `CopyToVolumeFn` |
| `InjectOnboardingOpts` | Container ID, `CopyToContainerFn` |
| `RebuildMissingImageOpts` | Image ref, IOStreams, TUI, Prompter, SettingsLoader, BuildImage fn, CommandVerb |

### Functions

| Function | Description |
|----------|-------------|
| `InitContainerConfig(ctx, InitConfigOpts)` | Copy host Claude config (strategy=copy) and/or credentials (use_host_auth) to config volume |
| `InjectOnboardingFile(ctx, InjectOnboardingOpts)` | Write `~/.claude.json` onboarding marker to a created container |
| `RebuildMissingDefaultImage(ctx, RebuildMissingImageOpts)` | Interactive rebuild flow for missing default images with TUI progress |

## Dependencies

Imports: `internal/config`, `internal/containerfs`, `internal/docker`, `internal/logger`

## Testing

Unit tests in `shared/containerfs_test.go` — uses mock CopyToVolume/CopyToContainer function trackers.
Integration tests in `test/internals/containerfs_test.go` — exercises full pipeline with real Docker containers.

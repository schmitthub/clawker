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

### Container Initialization (`init.go`)

Progress-tracked container initialization, shared between `run` and `create`. Extracts duplicated init code from `run.go` and `create.go` into a single `ContainerInitializer` Factory noun.

```go
import "github.com/schmitthub/clawker/internal/cmd/container/shared"

// Construct from Factory (in NewCmdRun/NewCmdCreate)
initializer := shared.NewContainerInitializer(f)

// Run with pre-resolved parameters (after image resolution)
result, err := initializer.Run(ctx, shared.InitParams{
    Client:           client,
    Config:           cfg,
    ContainerOptions: containerOpts,
    Flags:            cmd.Flags(),
    Image:            containerOpts.Image,
    StartAfterCreate: opts.Detach,
})
// result.ContainerID, result.AgentName, result.Warnings
```

**Three-phase command structure** (run.go, create.go):
- Phase A: Pre-progress (synchronous) — config + Docker connect + image resolution (may trigger interactive prompts)
- Phase B: Progress-tracked — `Initializer.Run()` with TUI progress display (5 steps)
- Phase C: Post-progress — print deferred warnings, then detach output or attach-then-start

**Progress steps**: workspace, config (cached if volume exists), environment, container create, post-init (only when `agent.post_init` configured), start (detached only). Step count: 5-6 depending on config.

**Deferred warnings**: During progress goroutine, TUI owns the terminal — can't print. Warnings collected in `InitResult.Warnings` for Phase C printing.

### Types

| Type | Purpose |
|------|---------|
| `ContainerInitializer` | Factory noun for progress-tracked container init; captures IOStreams, TUI, GitManager, HostProxy |
| `InitParams` | Runtime values: Client, Config, ContainerOptions, Flags, Image, StartAfterCreate, AltScreen |
| `InitResult` | Outputs: ContainerID, AgentName, ContainerName, HostProxyRunning, Warnings |
| `CopyToVolumeFn` | Function type matching `(*docker.Client).CopyToVolume` |
| `CopyToContainerFn` | Simplified function type for tar-to-container copy |
| `InitConfigOpts` | Project/agent names, `ContainerWorkDir`, `*config.ClaudeCodeConfig`, `CopyToVolumeFn` |
| `InjectOnboardingOpts` | Container ID, `CopyToContainerFn` |
| `InjectPostInitOpts` | Container ID, Script content, `CopyToContainerFn` — injects `~/.clawker/post-init.sh` |
| `RebuildMissingImageOpts` | Image ref, IOStreams, TUI, Prompter, SettingsLoader, BuildImage fn, CommandVerb |

### Functions

| Function | Description |
|----------|-------------|
| `NewContainerInitializer(f)` | Construct from Factory — captures eager + lazy deps |
| `(*ContainerInitializer).Run(ctx, InitParams)` | Progress-tracked init: workspace, config, env, create, start |
| `InitContainerConfig(ctx, InitConfigOpts)` | Copy host Claude config (strategy=copy) and/or credentials (use_host_auth) to config volume |
| `InjectOnboardingFile(ctx, InjectOnboardingOpts)` | Write `~/.claude.json` onboarding marker to a created container |
| `InjectPostInitScript(ctx, InjectPostInitOpts)` | Write `~/.clawker/post-init.sh` to a created container; entrypoint runs it once on first start |
| `RebuildMissingDefaultImage(ctx, RebuildMissingImageOpts)` | Interactive rebuild flow for missing default images with TUI progress |
| `NewCopyToContainerFn(client *docker.Client) CopyToContainerFn` | Creates a `CopyToContainerFn` closure wrapping `docker.Client.CopyToContainer` |

## Dependencies

Imports: `internal/cmd/container/opts`, `internal/cmdutil`, `internal/config`, `internal/containerfs`, `internal/docker`, `internal/git`, `internal/hostproxy`, `internal/iostreams`, `internal/logger`, `internal/tui`, `internal/workspace`

## Testing

Unit tests in `shared/init_test.go` — `ContainerInitializer` tests using `dockertest.FakeClient`.
Unit tests in `shared/image_test.go` — `RebuildMissingDefaultImage` interactive flow, `persistDefaultImageSetting` edge cases, `progressStatus` mapping.
Unit tests in `shared/containerfs_test.go` — uses mock CopyToVolume/CopyToContainer function trackers.
Integration tests in `test/internals/containerfs_test.go` — exercises full pipeline with real Docker containers.

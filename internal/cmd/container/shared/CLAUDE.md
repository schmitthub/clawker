# Container Shared Package

Container flag types, domain orchestration logic, and container creation — shared between container subcommands (`run/`, `create/`, `start/`, `exec/`).

This package consolidates what was previously split between `opts/` (flag types) and `shared/` (domain logic) into a single package.

## API

### Container Options (`container.go`)

All container CLI flag types and configuration building.

`ContainerOptions` — all container flags. `NewContainerOptions()`, `AddFlags(flags, opts)`, `MarkMutuallyExclusive(cmd)`.

Key functions: `GetAgentName()`, `BuildConfigs(flags, mounts, cfg)`, `ValidateFlags()`, `ResolveAgentName(agent, generateRandom)`, `ParseLabelsToMap(labels)`, `MergeLabels(base, user)`, `NeedsSocketBridge(cfg)`.

**Types**: `ContainerOptions`, `ListOpts`, `MapOpts`, `PortOpts`, `NetworkOpt` with `NetworkAttachmentOpts`.

**Flag categories**: Basic, Environment, Volumes, Networking, Resources, Security (incl. `--disable-firewall`), Health, Process & Runtime (incl. `--workdir`), Devices.

### CreateContainer (`container.go`)

Single entry point for container creation, shared by `run` and `create` commands. Performs all init steps: workspace setup, config initialization, environment resolution, Docker container creation, and post-create injection.

Progress is communicated via an events channel (nil for silent mode). Callers own all terminal output.

```go
events := make(chan CreateContainerEvent, 64)
done := make(chan struct{})
go func() {
    defer close(done)
    for ev := range events { /* drive spinner, collect warnings */ }
}()

result, err := shared.CreateContainer(ctx, &shared.CreateContainerConfig{
    Client:     client,
    Config:     cfg,
    Options:    containerOpts,
    Flags:      cmd.Flags(),
    GitManager: f.GitManager,
    HostProxy:  f.HostProxy,
}, events)
close(events)
<-done // wait for consumer goroutine before reading result
// result.ContainerID, result.AgentName, result.ContainerName, result.HostProxyRunning
```

**Steps** (streamed via events channel):
1. **workspace** — resolve work dir, setup mounts, ensure volumes
2. **config** — init container config (or cached if volume exists)
3. **environment** — host proxy, git credentials, runtime env vars
4. **container** — validate flags, build Docker configs, create, inject onboarding + post-init

**Volume cleanup on failure**: Uses named return values with deferred cleanup. Tracks newly-created volumes; removes only those on error. Pre-existing volumes are never touched.

**Event types**: `CreateContainerEvent` with `Step` (string), `Status` (`StepRunning`/`StepComplete`/`StepCached`), `Type` (`MessageInfo`/`MessageWarning`), `Message` (string).

### Container Init (`containerfs.go`)

One-time Claude config initialization for new containers. Called by `CreateContainer` when the config volume was freshly created.

```go
import "github.com/schmitthub/clawker/internal/cmd/container/shared"

// Copy host config and/or credentials to config volume
err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
    ProjectName:      "myapp",
    AgentName:        "dev",
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
| `ContainerOptions` | All container CLI flags — basic, env, volumes, networking, resources, security, health, runtime, devices |
| `CreateContainerConfig` | All inputs: Client, Config, Options, Flags, GitManager, HostProxy, Version, color flags |
| `CreateContainerResult` | Outputs: ContainerID, AgentName, ContainerName, HostProxyRunning |
| `CreateContainerEvent` | Channel event: Step, Status, Type, Message |
| `StepStatus` | Step lifecycle: `StepRunning`, `StepComplete`, `StepCached` |
| `MessageType` | Event severity: `MessageInfo`, `MessageWarning` |
| `ListOpts` | pflag.Value for repeatable string list flags |
| `MapOpts` | pflag.Value for key=value map flags |
| `PortOpts` | pflag.Value for port mapping flags |
| `NetworkOpt` | pflag.Value for advanced network flags with `NetworkAttachmentOpts` |
| `CopyToVolumeFn` | Function type matching `(*docker.Client).CopyToVolume` |
| `CopyToContainerFn` | Simplified function type for tar-to-container copy |
| `InitConfigOpts` | Project/agent names, `ContainerWorkDir`, `*config.ClaudeCodeConfig`, `CopyToVolumeFn` |
| `InjectOnboardingOpts` | Container ID, `CopyToContainerFn` |
| `InjectPostInitOpts` | Container ID, Script content, `CopyToContainerFn` — injects `~/.clawker/post-init.sh` |
| `RebuildMissingImageOpts` | Image ref, IOStreams, TUI, Prompter, SettingsLoader, BuildImage fn, CommandVerb |

### Functions

| Function | Description |
|----------|-------------|
| `NewContainerOptions()` | Create ContainerOptions with initialized pflag.Value fields |
| `AddFlags(flags, opts)` | Register all container flags on a pflag.FlagSet |
| `MarkMutuallyExclusive(cmd)` | Mark `--agent` and `--name` as mutually exclusive |
| `CreateContainer(ctx, cfg, events)` | Single entry point — workspace, config, env, create, inject. Events channel for progress (nil = silent) |
| `NeedsSocketBridge(cfg)` | Check if GPG/SSH socket bridge is needed |
| `InitContainerConfig(ctx, InitConfigOpts)` | Copy host Claude config (strategy=copy) and/or credentials (use_host_auth) to config volume |
| `InjectOnboardingFile(ctx, InjectOnboardingOpts)` | Write `~/.claude.json` onboarding marker to a created container |
| `InjectPostInitScript(ctx, InjectPostInitOpts)` | Write `~/.clawker/post-init.sh` to a created container; entrypoint runs it once on first start |
| `RebuildMissingDefaultImage(ctx, RebuildMissingImageOpts)` | Interactive rebuild flow for missing default images with TUI progress |
| `NewCopyToContainerFn(client *docker.Client) CopyToContainerFn` | Creates a `CopyToContainerFn` closure wrapping `docker.Client.CopyToContainer` |

## Dependencies

Imports: `internal/cmdutil`, `internal/config`, `internal/containerfs`, `internal/docker`, `internal/git`, `internal/hostproxy`, `internal/logger`, `internal/workspace`, `pkg/whail`

## Testing

Unit tests in `shared/init_test.go` — `CreateContainer` tests using `dockertest.FakeClient` + `hostproxytest.MockManager`.
Unit tests in `shared/container_test.go` — Flag parsing, BuildConfigs, ValidateFlags, pflag.Value types.
Unit tests in `shared/image_test.go` — `RebuildMissingDefaultImage` interactive flow, `persistDefaultImageSetting` edge cases, `progressStatus` mapping.
Unit tests in `shared/containerfs_test.go` — uses mock CopyToVolume/CopyToContainer function trackers.
Integration tests in `test/internals/containerfs_test.go` — exercises full pipeline with real Docker containers.

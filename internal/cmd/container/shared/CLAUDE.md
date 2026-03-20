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
    ProjectManager: f.ProjectManager,
    HostProxy:  f.HostProxy,
}, events)
close(events)
<-done // wait for consumer goroutine before reading result
// result.ContainerID, result.AgentName, result.ContainerName, result.WorkDir, result.HostProxyRunning
```

**Steps** (streamed via events channel):
1. **workspace** — resolve work dir, setup mounts, ensure volumes
2. **config** — init container config (or cached if volume exists)
3. **environment** — host proxy, git credentials, runtime env vars
4. **container** — validate flags, build Docker configs, create, inject post-init (if configured)

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
    ContainerWorkDir: wsResult.ContainerPath, // host absolute path for Claude Code /resume compatibility
    ClaudeCode:       cfg.Agent.ClaudeCode,
    CopyToVolume:     client.CopyToVolume,
})

// NOTE: Onboarding bypass (hasCompletedOnboarding) is handled at image level —
// the entrypoint seeds ~/.claude/.config.json from ~/.claude-init/.config.json.
```

### Image Rebuild (`image.go`)

Interactive rebuild flow for missing default images, shared between `run/` and `create/`.

```go
err := shared.RebuildMissingDefaultImage(ctx, shared.RebuildMissingImageOpts{
    ImageRef:    resolvedImage.Reference,
    IOStreams:   ios,
    TUI:        opts.TUI,
    Prompter:   opts.Prompter,
    BuildImage: client.BuildDefaultImage,
    CommandVerb: "run",
})
```

Non-interactive mode prints instructions and returns an error. Interactive mode prompts for rebuild confirmation, flavor selection, then builds with TUI progress display (or spinner fallback when TUI is nil).

### Container Start Orchestration (`container_start.go`)

Three-phase orchestration for container start: pre-start bootstrap, Docker start, post-start bootstrap. Used by `run`, `start`, and `exec` commands.

**`CommandOpts`** — DI container with lazy function closures for service providers:

| Field | Type | Purpose |
|-------|------|---------|
| `Client` | `func(ctx) (*docker.Client, error)` | Docker client provider |
| `Config` | `func() (config.Config, error)` | Config provider (required) |
| `ProjectManager` | `func() (project.ProjectManager, error)` | Project manager provider |
| `HostProxy` | `func() hostproxy.HostProxyService` | Host proxy provider |
| `Firewall` | `func(ctx) (firewall.FirewallManager, error)` | Firewall manager provider |
| `SocketBridge` | `func() socketbridge.SocketBridgeManager` | Socket bridge provider |
| `Logger` | `func() (*logger.Logger, error)` | Logger provider |

Nil providers are safely skipped (debug logged). `Config` is the only required provider.

**`BootstrapServicesPreStart(ctx, container, cmdOpts) error`** — Pre-start phase: syncs firewall project rules, ensures firewall daemon, waits for healthy (60s timeout), starts host proxy. Runs before Docker start so the network stack is ready.

**`BootstrapServicesPostStart(ctx, container, cmdOpts) error`** — Post-start phase: enables firewall iptables inside the container, starts socket bridge for GPG/SSH forwarding. Runs after Docker start because the container must be running.

**`ContainerStart(ctx, cmdOpts, startOpts) (ContainerStartResult, error)`** — Three-phase orchestrator:
1. `BootstrapServicesPreStart` — firewall daemon + rules + health + host proxy
2. `client.ContainerStart` — Docker container start
3. `BootstrapServicesPostStart` — firewall enable + socket bridge

Returns `mobyClient.ContainerStartResult` from the Docker start call. Errors at any phase abort immediately.

### Types

| Type | Purpose |
|------|---------|
| `ContainerOptions` | All container CLI flags — basic, env, volumes, networking, resources, security, health, runtime, devices |
| `CommandOpts` | DI container with lazy function closures: Client, Config, ProjectManager, HostProxy, Firewall, SocketBridge, Logger |
| `CreateContainerConfig` | All inputs: Client, Config, Options, Flags, ProjectManager, HostProxy, Logger, Version, color flags |
| `CreateContainerResult` | Outputs: ContainerID, AgentName, ContainerName, WorkDir, HostProxyRunning |
| `CreateContainerEvent` | Channel event: Step, Status, Type, Message |
| `StepStatus` | Step lifecycle: `StepRunning`, `StepComplete`, `StepCached` |
| `MessageType` | Event severity: `MessageInfo`, `MessageWarning` |
| `ListOpts` | pflag.Value for repeatable string list flags |
| `MapOpts` | pflag.Value for key=value map flags |
| `PortOpts` | pflag.Value for port mapping flags |
| `NetworkOpt` | pflag.Value for advanced network flags with `NetworkAttachmentOpts` |
| `CopyToVolumeFn` | Function type matching `(*docker.Client).CopyToVolume` |
| `CopyToContainerFn` | Simplified function type for tar-to-container copy |
| `CopyFromContainerFn` | Function type for reading a tar stream from a container |
| `InitConfigOpts` | Project/agent names, `ContainerWorkDir`, `*config.ClaudeCodeConfig`, `CopyToVolumeFn` |
| `InjectPostInitOpts` | Container ID, Script content, `CopyToContainerFn` — injects `~/.clawker/post-init.sh` |
| `RebuildMissingImageOpts` | Image ref, IOStreams, TUI, Prompter, BuildImage fn, CommandVerb |

### Functions

| Function | Description |
|----------|-------------|
| `NewContainerOptions()` | Create ContainerOptions with initialized pflag.Value fields |
| `AddFlags(flags, opts)` | Register all container flags on a pflag.FlagSet |
| `MarkMutuallyExclusive(cmd)` | Mark `--agent` and `--name` as mutually exclusive |
| `CreateContainer(ctx, cfg, events)` | Single entry point — workspace, config, env, create, inject. Events channel for progress (nil = silent) |
| `NeedsSocketBridge(cfg)` | Check if GPG/SSH socket bridge is needed based on project config |
| `BootstrapServicesPreStart(ctx, container, cmdOpts)` | Pre-start: firewall daemon + rules sync + health wait + host proxy |
| `BootstrapServicesPostStart(ctx, container, cmdOpts)` | Post-start: firewall enable in container + socket bridge |
| `ContainerStart(ctx, cmdOpts, startOpts)` | Three-phase orchestrator: pre-start bootstrap, Docker start, post-start bootstrap |
| `InitContainerConfig(ctx, InitConfigOpts)` | Copy host Claude config (strategy=copy) and/or credentials (use_host_auth) to config volume |
| `InjectPostInitScript(ctx, InjectPostInitOpts)` | Write `~/.clawker/post-init.sh` to a created container; entrypoint runs it once on first start |
| `ResolveAgentEnv(agent, projectDir) (map[string]string, []string, error)` | Merges `env_file` + `from_env` + `env` into env map. Precedence: env_file < from_env < env |
| `RebuildMissingDefaultImage(ctx, RebuildMissingImageOpts)` | Interactive rebuild flow for missing default images with TUI progress |
| `NewListOpts(validator) *ListOpts` | Create ListOpts with optional validation function |
| `NewListOptsRef(values, validator) *ListOpts` | Create ListOpts backed by an existing slice |
| `NewMapOpts(validator) *MapOpts` | Create MapOpts with optional validation function |
| `NewPortOpts() *PortOpts` | Create PortOpts for port mapping flags |
| `NewCopyToContainerFn(client *docker.Client) CopyToContainerFn` | Creates a `CopyToContainerFn` closure wrapping `docker.Client.CopyToContainer` |

## Worktree Resolution (`resolveWorkDir`)

`resolveWorkDir()` resolves the host path for the container's workspace mount (mounted at the same absolute path inside the container). When `--worktree` is set, it creates or reuses a Git worktree:

1. Parses flag via `cmdutil.ParseWorktreeFlag(value, agentName)` → `WorktreeSpec{Branch, Base}`
2. Calls `proj.CreateWorktree(ctx, branch, base)` to create the worktree
3. If `project.ErrWorktreeExists` → falls back to `proj.GetWorktree(ctx, branch)` for idempotent reuse
4. Validates health: only `WorktreeHealthy` is accepted; stale worktrees produce an error suggesting `clawker worktree prune`
5. Returns `(worktreePath, proj.RepoPath(), nil)` — the second return is the main repo root (used for `.git` directory mount)

The `--worktree` flag is idempotent (get-or-create). This differs from `clawker worktree add` which is strict (create-only, rejects duplicates).

## Home Directory Safety (`safety.go`)

`IsOutsideHome(dir string) bool` — pure function (no I/O, stdlib only) that returns `true` when `dir` is `$HOME` itself or any directory not within `$HOME`. Uses `filepath.EvalSymlinks` for consistent comparison (macOS `/var` → `/private/var`), then `filepath.Rel(home, dir)` — result of `"."` means dir IS home, prefix `".."` means outside.

Returns `false` on any resolution error (conservative — don't block users when paths can't be resolved).

**Callers**:
- `run/create` commands: prompt for confirmation via `opts.Prompter().Confirm()` (default: No)
- `loop iterate/tasks` commands: hard error (`fmt.Errorf("loop mode is not supported outside of, or in, the home directory")`)

## Dependencies

Imports: `internal/cmdutil`, `internal/config`, `internal/containerfs`, `internal/docker`, `internal/firewall`, `internal/git`, `internal/hostproxy`, `internal/logger`, `internal/project`, `internal/socketbridge`, `internal/workspace`, `pkg/whail`

## Testing

Unit tests in `shared/init_test.go` — `CreateContainer` tests using `dockertest.FakeClient` + `hostproxytest.MockManager`.
Unit tests in `shared/container_test.go` — Flag parsing, BuildConfigs, ValidateFlags, pflag.Value types.
Unit tests in `shared/image_test.go` — `RebuildMissingDefaultImage` interactive flow, `progressStatus` mapping.
Unit tests in `shared/containerfs_test.go` — uses mock CopyToVolume/CopyToContainer function trackers.
Unit tests in `shared/workdir_test.go` — `resolveWorkDir` worktree idempotent reuse: create new, reuse healthy existing, error on stale.
Unit tests in `shared/safety_test.go` — `IsOutsideHome` tests: home dir (true), parent of home (true), root (true), subdirectory (false), deeply nested (false), outside home (true).
Integration tests in `test/internals/containerfs_test.go` — exercises full pipeline with real Docker containers.
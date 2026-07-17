# Container Shared Package

Container flag types, domain orchestration, and container creation -- shared between `run/`, `create/`, `start/`, `exec/`.

## API

### ContainerCreateOptions (`container_create.go`)

`ContainerCreateOptions` -- all container CLI flags. `NewContainerOptions()`, `AddFlags(flags, opts)`, `MarkMutuallyExclusive(cmd)`.

Key functions: `GetAgentName()`, `BuildConfigs(flags, mounts, cfg)`, `ValidateFlags()`, `ResolveAgentName(agent, generateRandom)`, `ParseLabelsToMap(labels)`, `MergeLabels(base, user)`, `NeedsSocketBridge(cfg)`.

### CreateContainer (`container_create.go`)

Single entry point for container creation. Developer diagnostics go to zerolog; callers own all terminal output. Signature is `(ctx, *CreateContainerOptions)`, returning a `*CreateContainerResult`. Commands typically run it in a goroutine behind a spinner and collect the outcome on a channel.

```go
result, err := shared.CreateContainer(ctx, &shared.CreateContainerOptions{
    Client:         client,
    Config:         cfg,
    ProjectName:    projectName,
    Options:        containerOpts,
    Flags:          cmd.Flags(),
    Version:        version,
    ProjectManager: opts.ProjectManager,
    HostProxy:      opts.HostProxy,
    Log:            log,
})
```

**Steps** (streamed via events): workspace, config, environment, container (validate+build+create+inject).

**Volume cleanup on failure**: Deferred cleanup via named returns. Tracks newly-created volumes; removes only those on error. Pre-existing volumes untouched.

### Agent Bootstrap Delivery (`agent_bootstrap.go`)

Per-agent registration material the CLI hands a managed container at boot.

```go
type AgentBootstrap struct {
    CertPEM, KeyPEM []byte  // mTLS leaf + key, signed by CLI CA
    CACertPEM       []byte  // CP server-trust CA (CLI CA cert)
    Assertion       string  // Hydra client_assertion JWT (single-use)
}

GenerateAgentBootstrap(caCertPath, caKeyPath string, project auth.ProjectSlug, agent auth.AgentName, containerID, hydraTokenURL string, signingKey *ecdsa.PrivateKey) (*AgentBootstrap, error)
WriteAgentBootstrapToContainer(ctx, containerID, copyFn CopyToContainerFn, b *AgentBootstrap) error
InstallAgentBootstrapMaterial(ctx, caCertPath, caKeyPath, signingKey, opts InstallAgentBootstrapOptions) error
```

The assertion's `iat` is minted in the host clock (the source of truth — Docker forces the CP/VM clock to track the host); there is **no** iat correction and **no** CP boot at create time. The container only needs the CP clock converged before it STARTS — the every-start `BootstrapServicesPreStart` CP-ensure (`EnsureRunning`, which blocks until the CP clock is in sync) handles that before clawkerd ever exchanges this baked assertion. Creating a container must not spin up CP.

`project` + `agent` (user-typed short identifiers) feed `auth.AgentFullName` to compose the per-agent identity (`clawker.<project>.<agent>`), which rides in a `urn:clawker:agent:<full-name>` URI SAN on the minted cert. The x509 CN is the deterministic `consts.ContainerClawkerd` literal (the binary identity), not a per-agent value.

`WriteAgentBootstrapToContainer` tars four files into `consts.BootstrapDir` (dir 0700, files 0400). Uses container writable layer (not tmpfs -- Docker's CopyToContainer cannot pre-populate tmpfs mounts).

### Container Init (`containerfs.go`)

One-time Claude config initialization for new containers, called by `CreateContainer` when config volume is fresh.

```go
err := shared.InitContainerConfig(ctx, shared.InitConfigOpts{
    ProjectName:      "myapp",
    AgentName:        "dev",
    ContainerWorkDir: wsResult.ContainerPath,
    ClaudeCode:       cfg.Agent.ClaudeCode,
    CopyToVolume:     client.CopyToVolume,
})
```

Onboarding bypass is image-level -- CP's generic seed-apply step places the harness's `.config.json` seed from the image's `~/.clawker/seed/` staging dir on first boot.

### Image Placeholder Resolution (`image.go`)

`ParseImagePlaceholder(image)` splits the `@` / `@:tag` image placeholder (ok=false for literal references). `ResolvePlaceholderImage(ctx, client, cfg, ios, projectName, harnessTag, commandVerb)` resolves the placeholder to a built image reference via `client.ResolveImageWithSource` — an explicit tag must name a known harness; no built image prints next-steps guidance (`clawker build`) and returns `cmdutil.SilentError`.

### Container Start Orchestration (`container_start.go`)

Three-phase orchestration: pre-start bootstrap, Docker start, post-start bootstrap.

**`CommandOpts`** -- DI container with lazy function closures:

| Field | Type | Purpose |
|-------|------|---------|
| `Client` | `func(ctx) (*docker.Client, error)` | Docker client provider |
| `Config` | `func() (config.Config, error)` | Config provider (required) |
| `HostProxy` | `func() hostproxy.Service` | Host proxy provider |
| `ControlPlane` | `func() cpboot.Manager` | CP container lifecycle |
| `AdminClient` | `func(ctx) (adminv1.AdminServiceClient, error)` | CP gRPC client (mTLS + OAuth2) |
| `SocketBridge` | `func() socketbridge.SocketBridgeManager` | Socket bridge provider |
| `Logger` | `func() (*logger.Logger, error)` | Logger provider |
| `AgentName` | `string` | Short agent name (set on new-container starts; empty on restart) |
| `Project` | `string` | Project slug for composite identity |

Nil providers safely skipped (debug logged). `Config` is the only required provider.

**Functions**:
- `BootstrapServicesPreStart(ctx, container, cmdOpts)` -- firewall rules sync + daemon ensure + health wait (60s) + host proxy + always-deliver the `agent.pre_run` hook to `~/.clawker/pre-run.sh` (user script when set, no-op when unset; not firewall-gated; copy failure aborts the start). Now requires a working `Client` provider.
- `BootstrapServicesPostStart(ctx, container, cmdOpts)` -- eBPF attachment + socket bridge
- `ContainerStart(ctx, cmdOpts, startOpts) (*mobyClient.ContainerStartResult, error)` -- runs all three phases; errors abort immediately. The docker client is resolved BEFORE pre-start so a failure can reap. Pre-start and Docker-start failures route through `ReapFailedStart`; post-start failures don't (the container is running). The result is the SDK's verbatim; nil means the Docker start call was never reached — the wrapper never fabricates an SDK result value (moby reserves the right to add fields to ContainerStartResult).
- `ReapFailedStart(client, containerID, startErr) error` -- reap-on-failed-start: when a start sequence fails, removes the container ONLY if it is destined for AutoRemove (`--rm`) and inspect proves it not running (nil `State` = unknown → untouched, a force-remove demands proof). Docker honors AutoRemove solely on exit-after-start, so a `--rm` container whose start never succeeded would otherwise squat its name forever in the `created` state, blocking a re-run. Non-AutoRemove and running containers are left untouched. NotFound/not-managed from inspect or remove is benign — the daemon already removed it. Always returns a non-nil error derived from `startErr` (the `ReapedNotice` const carries the user-facing removed-it message); cleanup uses a background context so Ctrl+C cannot abort it. Every start-sequence failure path routes through it; the one nuance worth knowing: plain `restart` and `start --attach` call it directly because they bootstrap without going through `ContainerStart`.

### Types

| Type | Purpose |
|------|---------|
| `ContainerCreateOptions` | All container CLI flags |
| `CommandOpts` | DI container with lazy closures + AgentName/Project |
| `CreateContainerOptions` | Inputs: Client, Config, ProjectName, Options, Flags, Version, ProjectManager, HostProxy, Log, Is256Color, IsTrueColor |
| `CreateContainerResult` | Outputs: ContainerID, AgentName, ContainerName, WorkDir, HostProxyRunning |
| `ListOpts` / `MapOpts` / `PortOpts` / `NetworkOpt` | pflag.Value types for repeatable/map/port/network flags |
| `CopyToVolumeFn` / `CopyToContainerFn` / `CopyFromContainerFn` | Function types for Docker copy operations |
| `InitConfigOpts` | Project/agent/harness names (harness name keys the harness-scoped volume identities), ContainerWorkDir, Harness+Staging+Volumes+FreshVolumes, CopyToVolumeFn, Log |
| `InjectPostInitOpts` | Container ID, Script, Cfg, CopyToContainerFn, Log |
| `InjectHookOpts` | Container ID, Script, Name, Cfg, CopyToContainerFn, Log |
| `AgentBootstrap` | CertPEM, KeyPEM, CACertPEM, Assertion |

### Functions

| Function | Description |
|----------|-------------|
| `NewContainerOptions()` | Create ContainerCreateOptions with initialized pflag.Value fields |
| `AddFlags(flags, opts)` | Register all container flags on a pflag.FlagSet |
| `MarkMutuallyExclusive(cmd)` | Mark `--agent`/`--name` mutually exclusive |
| `CreateContainer(ctx, cfg, events)` | Single entry point -- workspace, config, env, create, inject |
| `NeedsSocketBridge(cfg)` | Check if GPG/SSH bridge needed from project config |
| `InitContainerConfig(ctx, opts)` | Copy host Claude config to volume |
| `InjectHookScript(ctx, opts)` | Tar a bash-wrapped hook to `~/.clawker/<Name>.sh`; empty `Script` → no-op wrapper (always-deliver overwrites stale content) |
| `InjectPostInitScript(ctx, opts)` | Thin wrapper over `InjectHookScript` pinned to the `post-init` hook; used by the create path |
| `ResolveAgentEnv(agent, projectDir, log)` | Merge env_file + from_env + env. Precedence: env_file < from_env < env |
| `GenerateAgentBootstrap(...)` | Mint mTLS cert + JWT assertion for agent |
| `WriteAgentBootstrapToContainer(...)` | Tar bootstrap files into container |
| `InstallAgentBootstrapMaterial(...)` | Create-time install of agent bootstrap material |
| `NewListOpts` / `NewListOptsRef` / `NewMapOpts` / `NewPortOpts` | pflag.Value constructors |
| `NewCopyToContainerFn(client)` | Wraps `docker.Client.CopyToContainer` |

## Worktree Resolution (`resolveWorkDir`)

Resolves host path for container workspace mount when `--worktree` is set:

1. `cmdutil.ParseWorktreeFlag(value, agentName)` -> `WorktreeSpec{Branch, Base}`
2. `proj.CreateWorktree(ctx, branch, base, false)` -- on `ErrWorktreeExists`, falls back to `proj.GetWorktree`
3. Only `WorktreeHealthy` accepted; stale -> error suggesting `clawker worktree prune`
4. Returns `(worktreePath, proj.RepoPath(), nil)`

The `--worktree` flag is idempotent (get-or-create), unlike `clawker worktree add` (create-only). It is a limited happy-path shortcut: it always passes `noTrack=false` (default track-on-match — a branch matching a remote-tracking ref is checked out from the remote tip with upstream configured). The `--no-track` opt-out lives only on `clawker worktree add`, not on this shortcut.

## Home Directory Safety (`safety.go`)

`IsOutsideHome(dir string) bool` -- pure function, returns `true` when `dir` is `$HOME` itself or outside `$HOME`. Uses `filepath.EvalSymlinks` + `filepath.Rel`. Returns `false` on resolution error (conservative).

## Dependencies

Imports: `internal/cmdutil`, `internal/config`, `internal/containerfs`, `internal/controlplane` (for `ensureRunning` seam), `internal/docker`, `internal/git`, `internal/hostproxy`, `internal/logger`, `internal/project`, `internal/socketbridge`, `internal/workspace`, `pkg/whail`, `api/admin/v1`

## Testing

- `shared/init_test.go` -- `CreateContainer` with `mocks.FakeClient` + `hostproxytest.MockManager`
- `shared/container_create_test.go` -- Flag parsing, BuildConfigs, ValidateFlags, pflag.Value types
- `shared/container_start_test.go` -- `BootstrapServicesPreStart`/`PostStart` nil-safety, pre-run delivery, `ContainerStart` client validation
- `shared/agent_bootstrap_test.go` -- `GenerateAgentBootstrap`, `WriteAgentBootstrapToContainer` tar shape, `InstallAgentBootstrapMaterial`
- `shared/image_test.go` -- `validatePlaceholderHarness` reserved-tag rejection
- `shared/containerfs_test.go` -- Mock CopyToVolume/CopyToContainer trackers
- `shared/workdir_test.go` -- `resolveWorkDir` worktree idempotent reuse
- `shared/safety_test.go` -- `IsOutsideHome` boundary cases

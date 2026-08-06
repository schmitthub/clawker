# Container Shared Package

Container flag types, domain orchestration, and container creation -- shared between `run/`, `create/`, `start/`, `exec/`.

## API

### ContainerCreateOptions (`container_create.go`)

`ContainerCreateOptions` -- all container CLI flags. `NewContainerOptions()`, `AddFlags(flags, opts)`, `MarkMutuallyExclusive(cmd)`.

Key functions: `GetAgentName()`, `BuildConfigs(flags, mounts, security)`, `ValidateFlags()`, `ResolveAgentName(agent, generateRandom)`, `ParseLabelsToMap(labels)`, `MergeLabels(base, user)`, `NeedsSocketBridge(security)`.

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
    IOStreams:      ios,
})
```

**Steps** (streamed via events): workspace, config, environment, container (validate+build+create+inject).

**Volume cleanup on failure**: Deferred cleanup via named returns. Tracks newly-created volumes; removes only those on error. Pre-existing volumes untouched.

**`IOStreams` is required** — `CreateContainer` refuses a nil up front. A nil is
always a forgotten field, never a headless caller (headless runs carry a non-TTY
IOStreams, and prompt-suitability is `CanPrompt`'s call at the point of asking),
and it would silently turn an answerable authorization prompt into an
unexplained permission failure inside the container.

### ID-mapped workspace views (`idmap.go`)

A rootless daemon's user namespace maps the invoking user to container root, so
a bind-mounted workspace arrives root-owned inside the container and the
unprivileged clawker user cannot read or write it. Docker exposes no per-mount
ID mapping ([moby#52061](https://github.com/moby/moby/issues/52061) closed
unimplemented), and the kernel reserves idmapped-mount creation for
init-namespace CAP_SYS_ADMIN, so clawker provisions one itself.

`ensureIDMappedWorkspace(ctx, client, hostConfig, roots, ios, log)` runs inside
`buildContainerConfigs`, right after `BuildConfigs` — the one moment when every
host bind the container will ever have exists in one place (workspace, harness,
gitconfig, the user's own `-v` flags), because Docker fixes mounts at create.

Order of decisions, each cheap before the next:

1. Not Linux → no-op. The mount has to be made on the daemon's own kernel.
2. Nothing binds anything under a candidate root (snapshot workspaces, binds
   living elsewhere) → no-op, and the daemon is never even queried.
3. The daemon reports itself rootful (`Info.SecurityOptions` lacks
   `name=rootless`) → no-op. The daemon's own answer is the only trustworthy
   source; the CLI's privileges say nothing about how the daemon runs.
4. Otherwise, per root, deepest first: if no view is mounted at
   `consts.IDMapSubdir()/<basename>-<hash>`, run the embedded `idmap-mount`
   helper under sudo (via `cmdutil.RunElevated`) to attach one; then repoint
   every bind source at or under that root into the view.

Roots are `ws.wd` (the workspace — a worktree in `--worktree` mode) and
`ws.projectRootDir` (the main repository, bound for its git directory in
worktree mode). Deepest-first ordering keeps a worktree living inside its own
repository from being swallowed by the repository's view.

The mapping comes from `internal/idmap`: the workspace owner's on-disk IDs, and
the kernel IDs those occupy in the daemon's user namespace per the documented
rootless formula (container id 0 = the daemon user; id n≥1 = subuid base + n−1,
read from `/etc/subuid` + `/etc/subgid`). The container's clawker user carries
the host user's uid, so the owner's IDs are also the container-side IDs.

The view is state, not configuration: it survives daemon restarts and dies at
reboot, so its absence is simply re-provisioned — one sudo prompt on the first
create after a boot. A non-interactive run declines and returns the exact
`sudo clawker-idmap-mount …` command instead of a bare failure.

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

The assertion's `iat` is minted in the host clock (the source of truth — Docker forces the CP/VM clock to track the host); there is **no** iat correction and **no** CP boot at create time. The container only needs the CP clock converged before it STARTS — the every-start `BootstrapServicesPreStart` CP-ensure (`Manager.Start`, which blocks until the CP clock is in sync) handles that before clawkerd ever exchanges this baked assertion. Creating a container must not spin up CP.

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
| `IOStreams` | `*iostreams.IOStreams` | Terminal reach for mid-boot CP assistance prompts (required) |
| `HostProxy` | `func() hostproxy.Service` | Host proxy provider |
| `ControlPlane` | `func(ctx) (cpmanager.Manager, error)` | CP container lifecycle |
| `AdminClient` | `func(ctx) (adminv1.AdminServiceClient, error)` | CP gRPC client (mTLS + OAuth2) |
| `SocketBridge` | `func() socketbridge.SocketBridgeManager` | Socket bridge provider |
| `Logger` | `func() (*logger.Logger, error)` | Logger provider |
| `AgentName` | `string` | Short agent name (set on new-container starts; empty on restart) |
| `Project` | `string` | Project slug for composite identity |

Nil providers safely skipped (debug logged). Required: `Config`, and `IOStreams` (the struct field, not a provider) — `BootstrapServicesPreStart` refuses a nil IOStreams up front, because a nil is always a wiring bug (headless runs carry a non-TTY IOStreams; prompt-suitability is `CanPrompt`'s call inside `AssistSOS`) and would silently downgrade a control plane SOS to a plain error instead of prompting.

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
| `CreateContainerOptions` | Inputs: Client, Config, ProjectName, Options, Flags, Version, ProjectManager, ProjectRegistry, HostProxy, Log, IOStreams (**required**), Is256Color, IsTrueColor |
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
| `NeedsSocketBridge(security)` | Check if GPG/SSH bridge needed from the project's `security:` block |
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

Imports: `internal/cmdutil`, `internal/config`, `internal/containerfs`, `controlplane/manager` (the `Manager` noun) + `internal/cmd/controlplane/shared` (`AssistSOS`), `internal/docker`, `internal/git`, `internal/hostproxy`, `internal/logger`, `internal/project`, `internal/socketbridge`, `internal/workspace`, `pkg/whail`, `api/admin/v1`

## Testing

- `shared/init_test.go` -- `CreateContainer` with `mocks.FakeClient` + `hostproxytest.MockManager`
- `shared/container_create_test.go` -- Flag parsing, BuildConfigs, ValidateFlags, pflag.Value types
- `shared/container_start_test.go` -- `BootstrapServicesPreStart`/`PostStart` nil-safety, pre-run delivery, `ContainerStart` client validation
- `shared/agent_bootstrap_test.go` -- `GenerateAgentBootstrap`, `WriteAgentBootstrapToContainer` tar shape, `InstallAgentBootstrapMaterial`
- `shared/image_test.go` -- `validatePlaceholderHarness` reserved-tag rejection
- `shared/containerfs_test.go` -- Mock CopyToVolume/CopyToContainer trackers
- `shared/workdir_test.go` -- `resolveWorkDir` worktree idempotent reuse
- `shared/safety_test.go` -- `IsOutsideHome` boundary cases

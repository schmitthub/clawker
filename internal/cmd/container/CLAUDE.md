# Container Commands Package

Docker CLI-compatible container management commands. Subpackages (`run/`, `create/`, `start/`, etc.) are individual subcommands.

## Container Lifecycle Hooks

Where to add logic that must run at a lifecycle point. The deciding axis is **once (create) vs every start** — choose by how often the logic must run, not by which command the user typed.

### Create-time — runs ONCE, at container creation

`CreateContainer` (`shared/container_create.go`), shared by `run` and `create`: workspace mounts, config-volume seeding, env resolution, Docker create, agent bootstrap material, one-time `post_init` injection. Baked at creation and preserved across restarts; does NOT re-run on `start`/`restart`. Use for anything tied to the container's identity or volumes that should persist.

**No CP at create:** creating a container does NOT boot the control plane — that is the wrong lifecycle point. The agent assertion is minted in the host clock (the source of truth; Docker forces the CP/VM clock to track the host), so a created container carries a valid baked assertion without CP being up. The CP clock only needs to be converged before the container STARTS, which the every-start `BootstrapServicesPreStart` CP-ensure (below) waits for before clawkerd exchanges the assertion.

### Every-start — runs on EVERY run / start / restart

`ContainerStart` (`shared/container_start.go`) runs three phases in order; every start path funnels through them — `run`, `start`, and `restart --signal` go via `ContainerStart`, while plain `restart` (`restart/restart.go`) calls the two Bootstrap phases directly around `client.ContainerRestart`:

1. `BootstrapServicesPreStart` — before Docker start: host proxy + CP ensure, firewall rules sync (if enabled), every-start `pre_run` hook delivery. Use for host-side prep that must precede the container process.
2. Docker start (`client.ContainerStart` / `ContainerRestart`).
3. `BootstrapServicesPostStart` — after Docker start: eBPF cgroup enroll (cgroup exists only now), GPG/SSH socket bridge. Use for anything needing the running container's cgroup/PID.

In-container, CP then dispatches its init plan to clawkerd on every start (`internal/controlplane/agent`): the `post-init` step is marker-gated (once), `pre-run` runs every start. Mirror this once-vs-every choice when adding an init step (and add a matching `clawkerd/progress.go` label).

## Package Structure

```
internal/cmd/container/
├── container.go        # Parent command, registers subcommands
├── shared/             # Container flag types, domain logic, container init orchestration, CreateContainer
├── run/                # clawker container run (RunOptions, NewCmdRun)
├── create/             # clawker container create (CreateOptions, NewCmdCreate)
├── start/              # clawker container start (StartOptions, NewCmdStart)
├── exec/               # clawker container exec (ExecOptions, NewCmdExec)
└── ... (stop, attach, logs, list, inspect, cp, kill, pause, unpause, remove, rename, restart, stats, top, update, wait)
```

**Package rule**: `shared/` holds both container flag types and domain orchestration. Never put shared utilities in parent package.

### Canonical Error Handling

```go
// Docker connection error — return wrapped to Main() for centralized rendering
client, err := opts.Client(ctx)
if err != nil {
    return fmt.Errorf("connecting to Docker: %w", err)
}

// Per-item errors in multi-container loops — icon + name + error to stderr
cs := ios.ColorScheme()
fmt.Fprintf(ios.ErrOut, "%s %s: %v\n", cs.FailureIcon(), name, err)
```

### Canonical Table Rendering

```go
tp := opts.TUI.NewTable("NAME", "STATUS", "IMAGE")
for _, c := range containers {
    tp.AddRow(c.Name, c.Status, c.Image)
}
return tp.Render()
```

### Canonical Stream + Resize (attach/exec)

Separate I/O from resize — `pty.Stream(ctx, hijacked)` in goroutine, `signals.NewResizeHandler(resizeFunc, pty.GetSize)` after start. The +1/-1 resize trick forces SIGWINCH for TUI redraw.

### Format/Filter Flags (list command)

`container list` supports `--format`/`--json`/`-q`/`--filter key=value` via `cmdutil.FormatFlags` and `cmdutil.FilterFlags`. Valid filter keys: `name`, `status`, `agent`.

### Per-Command Documentation

- `attach/CLAUDE.md` — Stream+resize pattern
- `exec/CLAUDE.md` — Credential injection, TTY/non-TTY, detach
- `start/CLAUDE.md` — Attach-then-start, waitForContainerExit
- `shared/CLAUDE.md` — CreateContainer, ContainerStart, container flag types, domain orchestration

## Parent Command (`container.go`)

`NewCmdContainer(f *cmdutil.Factory) *cobra.Command` — registers all 20 subcommands. All follow `NewCmd*(f, runF)` pattern.

## Shared Package (`shared/`)

Container flag types, domain logic, container creation, and container start orchestration — all in one package. See `shared/CLAUDE.md` for full API.

### Daemon Bootstrap Pattern

`shared.ContainerStart()` is the unified container start mechanism used by `run` and `start`. It accepts a `CommandOpts` struct (DI container with lazy function closures for all service providers) and implements a three-phase daemon bootstrap:

1. **Pre-start** (`BootstrapServicesPreStart`) — host proxy start + CP ensure + health wait (60s) + firewall rules sync (if enabled) + every-start `pre_run` hook delivery
2. **Docker start** — `client.ContainerStart` (the actual Docker API call)
3. **Post-start** (`BootstrapServicesPostStart`) — eBPF program attachment for the container + socket bridge for GPG/SSH forwarding

Errors at any phase abort immediately. See `shared/CLAUDE.md` section "Container Start Orchestration" for `CommandOpts` fields, function signatures, and full details.

### Container Options (`container_create.go`)

`ContainerCreateOptions` — all container CLI flags. `NewContainerOptions()`, `AddFlags(flags, opts)`, `MarkMutuallyExclusive(cmd)`.

Key functions: `GetAgentName()`, `BuildConfigs(flags, mounts, cfg)`, `ValidateFlags()`, `ResolveAgentName(agent, generateRandom)`, `ParseLabelsToMap(labels)`, `MergeLabels(base, user)`, `NeedsSocketBridge(cfg)`.

**Types**: `ContainerCreateOptions`, `ListOpts`, `MapOpts`, `PortOpts`, `NetworkOpt` with `NetworkAttachmentOpts`.

**Flag categories**: Basic, Environment, Volumes, Networking, Resources, Security (incl. `--disable-firewall`), Health, Process & Runtime (incl. `--workdir`), Devices.

### CreateContainer (`container_create.go`)

Single entry point for container creation, shared by `run` and `create`. Performs all init steps: workspace setup, config initialization, environment resolution, Docker container creation, and post-create injection. Progress communicated via events channel (nil for silent mode).

**Types**: `CreateContainerOptions`, `CreateContainerResult`, `CreateContainerEvent`, `StepStatus`, `MessageType`.

**Low-level helpers**: `InitContainerConfig(ctx, opts)` copies host Claude config to volume; `InjectPostInitScript(ctx, opts)` writes post-init script. Onboarding bypass is image-level (entrypoint seeds `~/.claude/.config.json`).

**Types**: `CopyToVolumeFn`, `CopyToContainerFn`, `InitConfigOpts`, `InjectPostInitOpts`, `RebuildMissingImageOpts`

## Image Resolution (@ Symbol)

`opts.Image == "@"` → `client.ResolveImageWithSource(ctx, projectName)`. Source types: `ImageSourceProject`, `ImageSourceGlobal`. Resolution is scope-keyed: project scope (non-empty `projectName`) → project-label image lookup; global scope (empty `projectName`) → global image lookup (`ImageTag("")`). Scopes do not ladder, and there is no `build.image` config fallback — that is a bare base image, never runnable as an agent. Project name resolved via `project.ProjectManager.CurrentProject(ctx).Name()`. Returns `nil` when no built image exists for the scope (caller prints next-steps guidance pointing at `clawker build`).

## Home Directory Safety

Before container creation, `run` and `create` check if CWD is at or above `$HOME` via `shared.IsOutsideHome(".")`. If true, the user is prompted for confirmation (default: No).

## Workspace Setup

Handled internally by `CreateContainer()` via `workspace.SetupMounts()`.

## Command DI Pattern

Commands use function references on Options structs. `NewCmd*` takes `*Factory` and wires closures. Run functions accept `*Options` only, call `opts.Client(ctx)` etc.

## Exec Credential Forwarding

Auto-injects git credential env vars into exec'd processes via `workspace.SetupGitCredentials()`. HTTPS via host proxy; SSH/GPG env vars from the already-running socket bridge (set up at container start, not per-exec).

## SocketBridge Wiring

`BootstrapServicesPostStart` calls `EnsureBridge` as part of the post-start phase (used by `run` and `start`). `stop/remove` call `StopBridge` before Docker ops (best-effort, nil-safe).

## Testing

Cobra+Factory pattern: `mocks.NewFakeClient(cfg)` → `testFactory(f)` → `NewCmdRun(f, nil)` → assert output + `fake.AssertCalled`. Per-package `testFactory`/`testConfig` helpers (not shared). See `.claude/docs/TESTING-REFERENCE.md`.

**Tiers**: Tier 1 (flag parsing via `runF` trapdoor), Tier 2 (Cobra+Factory with `nil` runF), Tier 3 (unit, direct calls).

## Exit Code Handling

`cmdutil.ExitError{Code: status}` propagates non-zero exit codes while allowing deferred cleanup.

## Attach-Then-Start Pattern

Interactive `-it` sessions attach before starting to prevent race with `--rm` containers. I/O goroutines start before `ContainerStart`; resize + SIGWINCH after start.

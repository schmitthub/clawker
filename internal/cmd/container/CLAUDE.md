# Container Commands Package

Docker CLI-compatible container management commands. Subpackages (`run/`, `create/`, `start/`, etc.) are individual subcommands.

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
- `shared/CLAUDE.md` — CreateContainer, container flag types, domain orchestration

## Parent Command (`container.go`)

`NewCmdContainer(f *cmdutil.Factory) *cobra.Command` — registers all 20 subcommands. All follow `NewCmd*(f, runF)` pattern.

## Shared Package (`shared/`)

Container flag types, domain logic, and container creation — all in one package. See `shared/CLAUDE.md` for full API.

### Container Options (`container.go`)

`ContainerOptions` — all container flags. `NewContainerOptions()`, `AddFlags(flags, opts)`, `MarkMutuallyExclusive(cmd)`.

Key functions: `GetAgentName()`, `BuildConfigs(flags, mounts, cfg)`, `ValidateFlags()`, `ResolveAgentName(agent, generateRandom)`, `ParseLabelsToMap(labels)`, `MergeLabels(base, user)`, `NeedsSocketBridge(cfg)`.

**Types**: `ContainerOptions`, `ListOpts`, `MapOpts`, `PortOpts`, `NetworkOpt` with `NetworkAttachmentOpts`.

**Flag categories**: Basic, Environment, Volumes, Networking, Resources, Security (incl. `--disable-firewall`), Health, Process & Runtime (incl. `--workdir`), Devices.

### CreateContainer (`container.go`)

Single entry point for container creation, shared by `run` and `create`. Performs all init steps: workspace setup, config initialization, environment resolution, Docker container creation, and post-create injection. Progress communicated via events channel (nil for silent mode).

**Types**: `CreateContainerConfig`, `CreateContainerResult`, `CreateContainerEvent`, `StepStatus`, `MessageType`.

**Low-level helpers**: `InitContainerConfig(ctx, opts)` copies host Claude config to volume; `InjectOnboardingFile(ctx, opts)` writes onboarding marker; `InjectPostInitScript(ctx, opts)` writes post-init script.

**Types**: `CopyToVolumeFn`, `CopyToContainerFn`, `InitConfigOpts`, `InjectOnboardingOpts`, `InjectPostInitOpts`, `RebuildMissingImageOpts`

## Image Resolution (@ Symbol)

`opts.Image == "@"` → `client.ResolveImageWithSource(ctx)`. Source types: `ImageSourceExplicit`, `ImageSourceProject`, `ImageSourceDefault`. Interactive rebuild in `shared/image.go` (`RebuildMissingDefaultImage`).

## Workspace Setup

Handled internally by `CreateContainer()` via `workspace.SetupMounts()`.

## Command DI Pattern

Commands use function references on Options structs. `NewCmd*` takes `*Factory` and wires closures. Run functions accept `*Options` only, call `opts.Client(ctx)` etc.

## Exec Credential Forwarding

Auto-injects git credential env vars into exec'd processes. HTTPS via host proxy, SSH/GPG via socketbridge (`EnsureBridge`). Setup via `workspace.SetupGitCredentials()`.

## SocketBridge Wiring

`run/start/exec` call `EnsureBridge` (idempotent). `stop/remove` call `StopBridge` before Docker ops (best-effort, nil-safe).

## Testing

Cobra+Factory pattern: `dockertest.NewFakeClient()` → `testFactory(f)` → `NewCmdRun(f, nil)` → assert output + `fake.AssertCalled`. Per-package `testFactory`/`testConfig` helpers (not shared). See `.claude/memories/TESTING-REFERENCE.md`.

**Tiers**: Tier 1 (flag parsing via `runF` trapdoor), Tier 2 (Cobra+Factory with `nil` runF), Tier 3 (unit, direct calls).

## Exit Code Handling

`cmdutil.ExitError{Code: status}` propagates non-zero exit codes while allowing deferred cleanup.

## Attach-Then-Start Pattern

Interactive `-it` sessions attach before starting to prevent race with `--rm` containers. I/O goroutines start before `ContainerStart`; resize + SIGWINCH after start.

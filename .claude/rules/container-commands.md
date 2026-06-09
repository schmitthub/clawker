---
description: Container command patterns
paths: ["internal/cmd/container/**"]
---

# Container Command Rules

- `@` symbol triggers `client.ResolveImageWithSource(ctx, projectName)` for automatic image resolution — scope-keyed: project-label image in project scope, global image (`ImageTag("")`) when no project; no `build.image` config fallback; project name resolved via `project.ProjectManager.CurrentProject(ctx).Name()`
- Shared container flags and domain logic live in `internal/cmd/container/shared/` package
- Always use `f.Client(ctx)` from Factory — never `docker.NewClient()` directly
- Do NOT `defer client.Close()` — Factory manages client lifecycle
- `--agent` and `--name` are mutually exclusive; use `containerOpts.GetAgentName()`
- `BuildConfigs()` validates cross-flag constraints (memory-swap requires memory, etc.)
- Return `ExitError` instead of calling `os.Exit()` directly (allows deferred cleanup)
- `shared.CreateContainer(ctx, opts, events)` is the single entry point for container creation — performs all init steps (workspace, config, env, create, inject). Inputs/outputs via `CreateContainerOptions`/`CreateContainerResult`; progress streamed on `chan<- CreateContainerEvent` (Step/Status/Type/Message). Shared by `run` and `create`
- `--disable-firewall` flag is deprecated and a no-op; use `clawker firewall bypass` instead; `--workdir` overrides container working directory
- Three-phase command structure: Phase A (pre-progress: config+Docker+image+safety checks), Phase B (progress: CreateContainer with events channel), Phase C (post-progress: warnings+output)
- Home directory safety: `shared.IsOutsideHome(".")` before Phase B — `run`/`create` prompt for confirmation
- Low-level helpers: `InitContainerConfig(ctx, opts)` copies host Claude config to volume; `InjectPostInitScript(ctx, opts)` writes post-init script
- Onboarding bypass (`hasCompletedOnboarding`) is handled at image level via `claude-config.json` baked into `~/.claude-init/.config.json`; CP-driven init (`configSeedScript` in `internal/controlplane/agent/init.go`) copies it to `~/.claude/.config.json` on first boot
- Init is one-time: only runs on container creation, not on start/restart
- See `internal/cmd/container/CLAUDE.md` for full patterns

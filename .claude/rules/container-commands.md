---
description: Container command patterns
paths: ["internal/cmd/container/**"]
---

# Container Command Rules

- `@` symbol triggers `client.ResolveImageWithSource(ctx)` for automatic image resolution; interactive rebuild lives in `shared/image.go` (`RebuildMissingDefaultImage`)
- Shared container flags live in `internal/cmd/container/opts/` package (import cycle workaround)
- Always use `f.Client(ctx)` from Factory — never `docker.NewClient()` directly
- Do NOT `defer client.Close()` — Factory manages client lifecycle
- `--agent` and `--name` are mutually exclusive; use `containerOpts.GetAgentName()`
- `BuildConfigs()` validates cross-flag constraints (memory-swap requires memory, etc.)
- Return `ExitError` instead of calling `os.Exit()` directly (allows deferred cleanup)
- Container init orchestration uses `shared.ContainerInitializer` Factory noun — `run` and `create` call `Initializer.Run()` for 5-6 step progress-tracked init (workspace, config, env, create, post-init if configured, start)
- Three-phase command structure: Phase A (pre-progress: config+Docker+image), Phase B (progress: Initializer.Run), Phase C (post-progress: warnings+output)
- Low-level helpers: `InitContainerConfig(ctx, opts)` copies host Claude config to volume; `InjectOnboardingFile(ctx, opts)` writes onboarding marker; `InjectPostInitScript(ctx, opts)` writes post-init script
- Init is one-time: only runs on container creation, not on start/restart
- See `internal/cmd/container/CLAUDE.md` for full patterns

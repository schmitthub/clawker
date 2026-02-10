---
description: Container command patterns
paths: ["internal/cmd/container/**"]
---

# Container Command Rules

- `@` symbol triggers `client.ResolveImageWithSource(ctx)` for automatic image resolution; interactive rebuild lives in command layer (`handleMissingDefaultImage`)
- Shared container flags live in `internal/cmd/container/opts/` package (import cycle workaround)
- Always use `f.Client(ctx)` from Factory — never `docker.NewClient()` directly
- Do NOT `defer client.Close()` — Factory manages client lifecycle
- `--agent` and `--name` are mutually exclusive; use `containerOpts.GetAgentName()`
- `BuildConfigs()` validates cross-flag constraints (memory-swap requires memory, etc.)
- Return `ExitError` instead of calling `os.Exit()` directly (allows deferred cleanup)
- Container init orchestration lives in `internal/cmd/container/shared/containerfs.go` — used by both `run` and `create`
- `InitContainerConfig(ctx, opts)` copies host Claude config to volume; `InjectOnboardingFile(ctx, opts)` writes onboarding marker
- Init is one-time: only runs on container creation, not on start/restart
- See `internal/cmd/container/CLAUDE.md` for full patterns

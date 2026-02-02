---
description: Container command patterns
paths: ["internal/cmd/container/**"]
---

# Container Command Rules

- `@` symbol triggers `docker.ResolveAndValidateImage()` for automatic image resolution
- Shared container flags live in `internal/cmd/container/opts/` package (import cycle workaround)
- Always use `f.Client(ctx)` from Factory — never `docker.NewClient()` directly
- Do NOT `defer client.Close()` — Factory manages client lifecycle
- `--agent` and `--name` are mutually exclusive; use `containerOpts.GetAgentName()`
- `BuildConfigs()` validates cross-flag constraints (memory-swap requires memory, etc.)
- Return `ExitError` instead of calling `os.Exit()` directly (allows deferred cleanup)
- See `internal/cmd/container/CLAUDE.md` for full patterns

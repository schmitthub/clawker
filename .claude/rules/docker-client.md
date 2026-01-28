---
description: Docker client and whail engine guidelines
paths: ["internal/docker/**", "pkg/whail/**"]
---

# Docker Client Rules

- Never import `github.com/moby/moby/client` outside `pkg/whail`
- Never import `pkg/whail` outside `internal/docker`
- Per-operation `context.Context` as first parameter — never store context in structs
- Use `context.Background()` in deferred cleanup (original context may be cancelled)
- `IsContainerManaged()` returns `(false, nil)` for non-existent containers — not an error
- All whail methods check `IsContainerManaged` first, return `ErrContainerNotFound` for unmanaged
- Channel-based methods (ContainerWait): return nil response channel for unmanaged, use buffered error channels
- See `internal/docker/CLAUDE.md` for full patterns

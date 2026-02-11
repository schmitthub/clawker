---
description: Docker client and whail engine guidelines
paths: ["internal/docker/**", "pkg/whail/**"]
---

# Docker Client Rules

- Never import `APIClient` from `github.com/moby/moby/client` outside `pkg/whail`
- Never import `pkg/whail` outside `internal/docker`
- **Exception**: Standalone daemon packages (`internal/hostproxy`, `internal/cmd/bridge`) may import
  `github.com/moby/moby/client` directly. These are long-running daemon processes that need lightweight
  Docker API access (events, exec) without whail's label isolation overhead.
- **Logging**: Docker operations use file-only zerolog for diagnostics — never user-visible output. Status/errors go through `iostreams` or returned errors.
- Per-operation `context.Context` as first parameter — never store context in structs
- Use `context.Background()` in deferred cleanup (original context may be cancelled)
- `IsContainerManaged()` returns `(false, nil)` for non-existent containers — not an error
- All whail methods check `IsContainerManaged` first, return `ErrContainerNotFound` for unmanaged
- Channel-based methods (ContainerWait): return nil response channel for unmanaged, use buffered error channels
- See `internal/docker/CLAUDE.md` for full patterns

---
description: Host proxy architecture guidelines
paths: ["internal/hostproxy/**"]
---

# Host Proxy Rules

- `SessionStore` is generic with TTL and automatic cleanup
- `CallbackChannel` handles OAuth callback registration, capture, and retrieval
- Factory pattern: lazy init with `sync.Once`, call `EnsureRunning()` before container commands
- `BROWSER` env var set to `/usr/local/bin/host-open` so CLI tools use proxy automatically
- See `internal/hostproxy/CLAUDE.md` for full architecture diagrams and endpoint reference

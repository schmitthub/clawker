---
description: Host proxy architecture guidelines
paths: ["internal/hostproxy/**"]
---

# Host Proxy Rules

- `SessionStore` is generic with TTL and automatic cleanup
- `CallbackChannel` handles OAuth callback registration, capture, and retrieval
- Factory pattern: lazy init with `sync.Once`, call `EnsureRunning()` before container commands
- `BROWSER` env var set to `/usr/local/bin/host-open` so CLI tools use proxy automatically
- Daemon scope is strictly host proxy lifecycle — the firewall stack (Envoy+CoreDNS) is owned by the CP daemon (`internal/controlplane/firewall`, run by `cmd/clawkercp`)
- `Daemon.docker` field uses `ContainerLister` interface (satisfied by `*client.Client` from `github.com/moby/moby/client`); the concrete client is assigned to this field in `NewDaemon` — there is no separate `dockerClient` field on the struct
- See `internal/hostproxy/CLAUDE.md` for full architecture diagrams and endpoint reference

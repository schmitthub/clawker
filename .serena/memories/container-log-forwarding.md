# Container Log Forwarding [NOT_STARTED]

**Status:** NOT_STARTED — Future work identified during GPG socket conflict PR

## Concept

Forward container-side logs at `/var/log/clawker/*.log` to the host at `~/.local/clawker/logs/<container>/` via the muxrpc bridge.

## Motivation

Container-side binaries (e.g., `clawker-socket-server`) now write to `/var/log/clawker/socket-server.log`, but these logs are only accessible by exec-ing into the container. Forwarding them to the host would enable:
- Post-mortem debugging without a running container
- Centralized log viewing alongside host-side clawker logs
- Integration with the existing host-side zerolog rotation system

## Design Sketch

- **Container side:** Log files already exist at `/var/log/clawker/`. No changes needed.
- **Bridge side:** `internal/socketbridge` could add a log-forwarding channel (new muxrpc message type) or periodic file sync.
- **Host side:** Write to `~/.local/clawker/logs/<container-name>/socket-server.log` using the existing logger package rotation.

## References

- Socket-server logging: `internal/hostproxy/internals/cmd/clawker-socket-server/main.go` (`initLogging()`)
- Bridge protocol: `internal/socketbridge/bridge.go`
- Host-side logger: `internal/logger/`

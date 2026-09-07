# Container runtime

- `internal/cmd/container/shared/` owns common create/start operations. `CreateContainer` is the shared creation entry point.
- CLI startup uses the CP manager and host-service interfaces. Keep diagnostics in the logger; the command owns terminal output.
- `internal/workspace/` owns bind/snapshot workspace handling; `internal/project/` owns worktree identity and lifecycle.
- Bind mode exposes the mounted project. Snapshot mode changes remain in the container copy.
- `internal/hostproxy/` owns browser callbacks and host-side credential services. `internal/socketbridge/` owns socket forwarding. Preserve their interfaces and test helpers.

## Agent process

- `cmd/clawkerd/` delegates to `internal/clawkerd/cmd.go`; `clawkerd/` contains the daemon implementation.
- clawkerd is PID 1. It starts the user CMD only after CP sends `AgentReady`.
- `clawkerd/spawn_unix.go` owns child startup, process groups, reaping, and exit state.
- Repeated `AgentReady` must not start a second child or hide a failed first start.
- Initialization state and child-running state are distinct. Session reconnects must preserve both.
- For CP identity and session trust: `mem:controlplane/agent/core`.

## Attach and cleanup

- In raw mode, Ctrl+C goes to the container. Do not assume it delivers a host SIGINT.
- Do not wait for blocked stdin reads after the container exits.
- Close both sides of Docker hijacked connections.
- Restore terminal visual state and termios state. `os.Exit` does not run deferred cleanup.
- Package references: `internal/cmd/container/shared/AGENTS.md`, `clawkerd/AGENTS.md`, `internal/workspace/AGENTS.md`, `internal/hostproxy/AGENTS.md`, `internal/socketbridge/AGENTS.md`.

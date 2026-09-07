# Control plane

- Host startup and embedded CP assets: `controlplane/manager/`, through `Manager.Start`.
- CP process entry: `cmd/clawkercp/` delegates to `internal/controlplane/cmd.go`.
- Runtime packages: `controlplane/`. CLI-to-CP client: `controlplane/adminclient/`.
- CP owns the clawker network, authentication, AdminService, AgentService, agent registry, and agent sessions.
- Firewall enablement affects one subsystem. Never gate authentication, registry, sessions, or unrelated RPCs on that setting.

## Failure rules

- CP death leaves kernel-pinned eBPF state without its supervisor. It can also leave a bypass without its expiry timer.
- Constructors return errors. Subsystem failures log structured events with the error and downstream effect, then degrade where the startup contract permits.
- Do not add panic, fatal logging, or process exit to the serve path. Long-lived goroutines must recover.
- `wireExecutor` and `startAgentDialer` in `internal/controlplane/cmd.go` show subsystem degradation. `controlplane/pubsub/heartbeat.go` shows recovery.
- Only the orchestrator owns shutdown. Intentional drain-to-zero performs cleanup.
- Pre-`SetReady` startup-gate failures can exit nonzero without flushing eBPF. Agents from a previous CP must remain filtered.
- A broken executor prevents `AgentReady` dispatch and user-CMD startup; it must not stop the other CP services.

## Event and state ownership

- `controlplane/pubsub` owns typed topics and delivery. It does not own application state.
- `buildTopics` in the CP entrypoint constructs the Docker-event, agent-event, and eBPF-enrollment topics.
- `controlplane/agent/repository.go` owns agent state. `controlplane/dockerevents/` supplies Docker observations.
- Do not restore the removed overseer package or a shared cross-package state map.

## Focused references

- For registry identity, dialer trust, and sessions: `mem:controlplane/agent/core`.
- For rules, route identity, eBPF, and certificates: `mem:controlplane/firewall/core`.
- For clawkerd startup and child processes: `mem:runtime/core`.
- For telemetry paths and separate certificate roles: `mem:monitoring/core`.
- Package contracts: `controlplane/AGENTS.md`, `controlplane/manager/AGENTS.md`.

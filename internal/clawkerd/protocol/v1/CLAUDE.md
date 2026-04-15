# clawkerd Protocol v1

Protobuf definitions for the **future clawkerd↔CP wire protocol** (Branch 4+ of the control plane initiative — agent registration and command delivery from CP to in-container clawkerd daemons).

> **Status:** Reserved. Not on the current hot path. The **active** CLI↔CP wire protocol is at `api/admin/v1/admin.proto` (AdminService). This package is pre-cabling for when clawkerd agents land inside managed containers.

## Files

| File | Purpose |
|------|---------|
| `agent.proto` | `AgentReportingService` (clawkerd → CP: register, report) + `AgentCommandService` (CP → clawkerd: RunInit stream). Includes `ClawkerdConfiguration` for logger bootstrap |
| `agent_grpc.pb.go`, `agent.pb.go` | Generated gRPC + message types |
| `controlplane.proto` | Earlier design sketch of `ControlPlaneService` with responsibility-prefixed methods (EnableContainerFirewall, SyncFirewallRoutes, etc.). **Superseded by `api/admin/v1/admin.proto`** — retained as historical reference for clawkerd callers that will re-use the same transport + auth shape |
| `controlplane_grpc.pb.go`, `controlplane.pb.go` | Generated bindings for the sketch |

## Design notes

- **Package name:** `clawker.agent.v1` (gRPC package identifier). Go package: `github.com/schmitthub/clawker/internal/clawkerd/protocol/v1`.
- **Why `internal/`**: the agent protocol is not a public API surface — clawkerd is a clawker-internal daemon. `api/admin/v1/` stays at the top level because third-party tooling could in principle dial AdminService over the same mTLS/JWT contract.
- **Transport (intended):** mTLS over TCP from host CP → container clawkerd (via Docker inspect + container IP + `listen_port` from RegisterResponse) for `AgentCommandService`; clawkerd → CP via the same mTLS channel used by the CLI for `AgentReportingService`.
- **Auth (intended):** per-agent certs signed by the CP CA (PKCE-style registration). Scopes: `agent:register`, `agent:report` (add to `controlplane.AdminMethodScopes()`).

## When this gets wired up

Branch 4 ("clawkerd auth") + Branch 5 ("init migration") of the control plane initiative — see `.serena/memories/cp-initiative-status.md`. Until then, `agent.proto` and `controlplane.proto` are inert — generated code is compiled but no handlers register them on a server.

## Regenerating

Protobuf generation handled by `make proto` or equivalent `buf generate` invocation (check `buf.gen.yaml` at repo root if present). Never hand-edit `*.pb.go` / `*_grpc.pb.go`.

## Relationship to `api/admin/v1/`

| Dimension | `api/admin/v1/` (AdminService) | `internal/clawkerd/protocol/v1/` (this package) |
|-----------|-------------------------------|------------------------------------------------|
| Direction | CLI → CP | CP ↔ clawkerd (future) |
| Status | Shipped in PR #250 | Reserved (code-gen'd, not served) |
| Scope | `admin` (coarse in v1) | `agent:register`, `agent:report`, `firewall:admin` (proposed) |
| Visibility | `api/` (potentially public) | `internal/` (clawker-only) |

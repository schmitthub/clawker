# Control Plane Package

The clawker control plane. A containerized, privileged, long-lived Go service that owns authoritative state for managed containers. Runs as `clawker-cp` in the firewall stack, replacing the historical `clawker-ebpf` container that ran `sleep infinity` as a `docker exec` target.

## Responsibilities (v1)

1. **Authoritative ebpf management** — the CP holds `internal/controlplane/ebpf.Manager.Load()` lifetime for its process lifetime. BPF programs are attached once at boot and stay live; no more `docker exec ebpf-manager <subcommand>` round-trips. The hot-reload pinning bug from 2026-04-10 is fixed by construction: the CP owns `Load()` so it never runs twice.
2. **ControlPlaneService gRPC surface** — the host-side `clawker` CLI calls firewall/ebpf operations (Enable, Disable, Bypass, SyncFirewallRoutes, ...) as typed gRPC over a Unix domain socket. The plan docs this as the "primitive clawker control plane" — the first member of a family of clawkerd-related services.

## Auth (mTLS + OIDC + JWT, full stack from day 1)

| Layer | Purpose | Implementation |
|------|---------|----------------|
| mTLS over UDS | Authenticate the channel; bind peer identity to a CA-signed cert | `ca.go` + `controlplane.NewServer(Config{ServerOptions: ...})` with `credentials.NewTLS` |
| OIDC `/token` + `/keys` + `/.well-known/openid-configuration` | Issue short-lived JWTs via `client_credentials` + RFC 8705 `tls_client_auth` | `oidc_provider.go` — wire-compatible with OIDC, implemented directly via `go-jose/v4` |
| gRPC authz interceptor | Verify JWT signature, audience, expiration; cross-check mTLS peer CN vs JWT sub; enforce per-method scopes | `authz.go` |

**The shape is final.** Multi-caller expansion (clawkerd, webui, agents, ...) adds new `ClientRegistration` entries in `oidc_clients.go` and new `methodScopes` entries in `authz.go`. It does **not** rewire the interceptor, the client-side, the proto, the token format, or the cert management. v1's auth code is load-bearing forever.

**Not** full OIDC provider machinery. We speak the wire protocol (POST `/token` returns `{access_token, token_type, expires_in}`, JWTs are RS256 with standard claims) directly without embedding `zitadel/oidc`. When the first browser-delivered caller (webui) arrives, the `/token` handler can be swapped for `zitadel/oidc` without touching the interceptor, clients, or JWT format. This keeps v1 scope contained without taking on ~400 lines of `op.Storage` stubs for features v1 doesn't use (authorization_code, refresh tokens, userinfo, introspection).

## Files

| File | Purpose |
|------|---------|
| `server.go` | Cherry-picked from feature/clawkerd. `Server` struct, `ControlPlaneService` facade interface, `Registry`, `AgentReportingService` handler. Extended in this work to accept `*tls.Config` + gRPC interceptors via `Config.ServerOptions` and to expose the underlying `grpc.Server` via `GRPCServer()` for additional service registration. |
| `registry.go` | Cherry-picked. Tracks registered clawkerd agents by container ID. Unused in v1 (no TCP listener, no clients). |
| `controlplanetest/mock_server.go` | Cherry-picked. moq-style test double for `ControlPlaneService`. |
| `ebpf/` | eBPF subsystem: cgroup programs, manager, shared types. Moved from `internal/ebpf/` because ebpf is a feature of the CP, not a peer service. Owns `Manager.Load()` at runtime. Imported by `internal/dnsbpf/` for `DomainHash`/`IPToUint32`/types. |
| `ca.go` | Self-signed CA generation + persistence, server/client cert issuance via `crypto/x509`, OIDC RSA signing key management. `LoadOrGenerateTLSMaterial(dataDir)` is the single entry point — persists CA + signing key, regenerates leaf certs every call. |
| `uds_http.go` | Helpers for serving HTTPS over Unix domain sockets + the client-side `UnixHTTPTransport` factory for dialing them. |
| `oidc_clients.go` | Static `ClientRegistration` registry + scope constants. v1 has one client (`clawker-cli`) with one scope (`firewall:admin`). |
| `oidc_provider.go` | `TokenIssuer` (signs JWTs via `go-jose`) + `TokenVerifier` (validates them) + the `/token` / `/keys` / `/.well-known` HTTP handlers. |
| `authz.go` | `methodScopes` map (method → required scope) + `AuthUnaryInterceptor` / `AuthStreamInterceptor`. Cross-checks mTLS peer CN vs JWT `sub`; fail-closed on unmapped methods. |
| `controlplane_handler.go` | `v1.ControlPlaneServiceServer` implementation. Thin wrappers over `ebpf.Manager`. `BypassContainer` includes an in-CP timer goroutine for auto-unbypass — the timer survives the CLI exiting, unlike the old docker-exec pattern. |

## Test seam overview

The Go facade `ControlPlaneService` (not to be confused with the gRPC service of the same name) is the interface CLI-side consumers depend on. Tests use `controlplanetest.MockServer` to avoid standing up a real gRPC server.

Package unit tests live at:
- `ca_test.go` — CA round-trip, persistence, cert validation, mTLS CN invariants.
- `oidc_provider_test.go` — Token issue/verify round-trip, bad-signature rejection, expiry, scope narrowing, bearer-header parsing, method-scope coverage.

The end-to-end auth pipeline test (full cert generation → listeners → oauth2 TokenSource → gRPC call → interceptor → handler) lives at `internal/firewall/cp_client_test.go` because it exercises both the CP and the firewall manager's CLI-side client helpers.

## What's deferred to the multi-caller follow-up

Called out explicitly to track the "v1 is final shape, v2 is pure addition" commitment:

- **TCP listener** — v1 is UDS-only. Adding a TCP listener in v2 is a pure addition; the auth layer is ready to serve it without changes.
- **Embedding `zitadel/oidc`** — replaces our in-file `/token` handler with the full OIDC provider. Wire format stays identical; clients don't notice. Unlocks `/authorize` + PKCE for the webui.
- **Additional OIDC clients** — clawkerd, clawker-webui, etc. Each adds one entry to `registeredClients`.
- **Per-method scopes beyond `firewall:admin`** — finer-grained authz like `agent:register`, `webui:read`. Each adds one entry to `methodScopes`.
- **Docker socket** into the CP — arrives when the CP starts managing container lifecycles (metrics follow-up).
- **Agent reporting active usage** — the `AgentReportingService` handler is registered but unreachable in v1 (no TCP listener, no clawkerd). Lights up with the multi-caller PR.

## Package imports

- `internal/controlplane` imports `internal/controlplane/ebpf` for Manager + types, `internal/clawkerd/protocol/v1` for the gRPC service types, `internal/logger` for structured logging, `internal/config` + `internal/docker` for the cherry-picked agent registration flow.
- External: `google.golang.org/grpc`, `github.com/go-jose/go-jose/v4` (JWT signing/verification), `golang.org/x/oauth2` (client-side TokenSource, used via firewall manager).
- `internal/dnsbpf/` imports `internal/controlplane/ebpf` for shared types (no cycle).
- `internal/firewall/` imports `internal/controlplane` for cert management and gRPC helpers when building the CLI-side client.

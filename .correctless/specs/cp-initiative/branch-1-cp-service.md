# Spec: Branch 1 — Control Plane as a Proper Running Service

## Metadata
- **Created**: 2026-04-12
- **Status**: draft
- **Impacts**: control-plane-initiative
- **Branch**: feat/control-plane
- **Research**: null
- **Recommended-intensity**: high
- **Intensity**: high
- **Intensity reason**: trust boundary (TB-001: CLI-CP), auth keywords (mTLS, JWT, CA, certificate, OAuth, Hydra, Oathkeeper), security-critical infrastructure, CP is highest-privilege component
- **Override**: none

## Context

Branch 1 replaces the `clawker-ebpf` sleep-infinity + docker-exec pattern with a proper control plane daemon. The CP runs as a containerized service hosting the Ory auth stack (Hydra, Oathkeeper, Kratos) and a gRPC admin API. The CLI authenticates via mTLS on the gRPC admin API and authorizes via OAuth2 access tokens issued by Hydra.

This is the admin API — designed to scale beyond the firewall to monitoring, agent lifecycle, WebUI, and any future subsystem. The CLI is "the user agent to rule them all" — the end user's administrative sword. Four client types are planned across the initiative (CLI, clawkerd, WebUI, agents); Branch 1 wires the CLI only, but the infrastructure accommodates all four from day one.

The CLI is the root of trust. It generates an ES256 signing key pair, registers its public key (JWK) with Hydra at container creation time, and authenticates via `private_key_jwt` (RFC 7523). The CLI's private key material never enters any container.

Existing Branch 1 code is partially implemented but has design decisions that need correction: UDS transport (crash bug on Docker Desktop), hand-rolled OIDC (replaced by Hydra), CP-as-CA (replaced by CLI-as-CA). The spec captures the target design.

## Scope

**In scope:**
- CP container with four binaries: `clawker-cp`, `hydra`, `oathkeeper`, `kratos` — distroless final image
- All four processes running from Branch 1 (Kratos empty, Oathkeeper HTTP proxy with no routes — both present to catch integration issues early)
- Hydra: OAuth2 token server, in-memory store, admin API on `127.0.0.1` inside container only
- Oathkeeper gRPC middleware: vendored from `github.com/ory/oathkeeper/middleware` into `internal/controlplane/authkeeper/`, importing patched core
- Oathkeeper HTTP proxy: binary running, no routes configured until Branch 7
- Kratos: identity server running but empty — no users configured until Branch 7 (WebUI users will use env var for credentials so ephemeral boot works)
- CLI auth: `client_credentials` grant + `private_key_jwt` client auth (RFC 7523) against Hydra, ES256 signing algorithm
- CLI bootstrap: ES256 key pair generated on first run, public key (JWK) bind-mounted into CP, CP registers CLI client with Hydra via Go SDK (`github.com/ory/hydra-client-go`) on startup (`token_endpoint_auth_method: private_key_jwt`, `token_endpoint_auth_signing_alg: ES256`)
- mTLS on gRPC admin API (transport authentication) + JWT access tokens with `admin` scope (authorization)
- Hydra token endpoint: TLS + `private_key_jwt` authentication (not mTLS — Hydra's public API does not natively support client cert enforcement)
- Future: contribute RFC 8705 (`tls_client_auth` + certificate-bound access tokens) to fosite upstream, upgrade from `private_key_jwt`
- CP admin API: gRPC on TCP port 7443 (configurable via Settings schema), published to `127.0.0.1` on host
- CP admin port in Settings schema (`settings.yaml`) with config accessor — not project config
- Hydra public API: token endpoint on its default port, published to `127.0.0.1` on host
- HTTP `/healthz` endpoint for readiness probing (no ready files)
- eBPF operations exposed as gRPC RPCs (Enable, Disable, Bypass, SyncRoutes, etc.)
- CP container is NOT in `container_map` — it is infrastructure (like Envoy/CoreDNS), not a filtered agent
- Fresh access token per CLI invocation — new assertion + Hydra call every time, no token caching
- Auth material in `<DataDir>/auth/` — certs and keys persistent, private keys 0600
- `clawker auth rotate` — idempotent cert health check, creates if missing, rotates if requested
- CP container labels: `dev.clawker.purpose=controlplane`, `dev.clawker.managed=true`
- `internal/consts/` leaf package (constants extracted from config)
- `internal/ebpf/` moved to `internal/controlplane/ebpf/`
- Proto restructuring: `internal/clawkerd/protocol/v1/` → `api/{admin,agent}/v1/` with distinct gRPC services (`AdminService` for CLI, `AgentService` for clawkerd) — trust boundary visible in proto structure
- Proto definitions + buf tooling
- End-to-end auth pipeline test
- Verify no eBPF regression blocking host access to published CP ports

**Not in scope:**
- Ownership inversion (firewall.Manager still bootstraps CP — Branch 2)
- Envoy/CoreDNS container creation by CP (Branch 2)
- Hostproxy/socketbridge changes (Branch 3)
- clawkerd, PKCE, per-agent certs, CP CA for agents (Branch 4)
- WebUI frontend, Kratos user configuration, Oathkeeper HTTP routes (Branch 7)

**What stays the same (highway construction):**
- `firewall.Manager` still bootstraps the CP container
- `firewall.Manager` still creates Envoy/CoreDNS containers directly
- Firewall daemon still runs (health checks, container watcher)
- Hostproxy, socketbridge, monitor — untouched
- `BootstrapServicesPreStart` flow unchanged except eBPF ops go through gRPC

## Complexity Budget

- **Trust boundaries touched**: 1 (TB-001: CLI-CP mTLS + OAuth2)
- **Risk surface delta**: medium (CP gains localhost-bound admin port + Hydra token endpoint; mTLS + OAuth2 mitigate)

## CP Container Architecture

```
CP Container (distroless, static binaries, runs as root for eBPF)
│
├── kratos (subprocess)
│     └── :4433 identity (127.0.0.1 — empty until Branch 7)
│
├── hydra (subprocess)
│     ├── :4444 public (0.0.0.0) → published to host 127.0.0.1
│     └── :4445 admin (127.0.0.1) → internal only
│
├── oathkeeper (subprocess)
│     └── :4456 HTTP proxy (0.0.0.0) → published to host 127.0.0.1 (no routes until Branch 7)
│
└── clawker-cp (main process)
      ├── :7443 gRPC admin API (0.0.0.0) → published to host 127.0.0.1
      ├── :8080 /healthz HTTP (0.0.0.0) → Docker HEALTHCHECK
      ├── vendored Oathkeeper gRPC middleware (validates tokens against Hydra)
      └── eBPF manager (Load() on startup, programs stay live)

NOT in container_map — CP is infrastructure, not a filtered agent.
```

### Subprocess Management

clawker-cp is the main process and supervisor for all subprocesses:
- **Start order**: Kratos → Hydra → Oathkeeper → eBPF Load → gRPC server → healthz
- **Every step is a hard prerequisite**: any failure at any step = CP crashes with diagnostic error in `docker logs`. No partial-start states. No swallowing errors to get to healthz.
- **Health monitoring**: clawker-cp monitors all subprocess PIDs. If any subprocess exits, CP crashes immediately (fail-fast).
- **Signal forwarding**: On SIGTERM/SIGINT, clawker-cp forwards the signal to all subprocesses, waits for graceful shutdown with timeout, then exits.
- **Shutdown order**: Reverse of start — gRPC server GracefulStop → Oathkeeper → Hydra → Kratos → eBPF Close.
- **No restart loops**: subprocess crash = CP crash. Docker's restart policy handles container-level restarts.

### Startup Sequence

1. Start Kratos subprocess → if fails, **crash**
2. Start Hydra subprocess (in-memory store, admin on 127.0.0.1:4445) → if fails, **crash**
3. Wait for both healthy → if timeout, **crash**
4. Read CLI JWK from bind-mounted file (`<DataDir>/auth/cli/signing-jwk.json`) → if missing/malformed, **crash**
5. Register CLI client via Hydra admin API (localhost:4445) → if fails, **crash**
6. Start Oathkeeper HTTP proxy subprocess → if fails, **crash**
7. Load eBPF programs (`ebpf.Manager.Load()`, sets internal ready atomic bool) → if fails, **crash**
8. Start gRPC server on :7443 (vendored Oathkeeper gRPC middleware) → if fails, **crash**
9. Report healthy on /healthz (only reached if ALL above succeed)

### CLI Flow (every invocation)

```
CLI                                          CP
│                                            │
├─ Ensure auth material exists (idempotent)  │
├─ Ensure CP container running               │
├─ Wait for /healthz 200                     │
│                                            │
├─ Sign fresh JWT assertion:                 │
│  iss=clawker-cli, sub=clawker-cli,         │
│  aud=<Hydra token URL>, jti=<random>,      │
│  exp=now+30s, iat=now                      │
│  Signed with ES256 private key             │
│                                            │
├─ POST Hydra :4444/oauth2/token             │
│  grant_type=client_credentials             │
│  client_assertion_type=jwt-bearer          │
│  client_assertion=<signed JWT>             │
│                              ┌─────────────┤
│                              │ Verify JWT   │
│                              │ sig against  │
│                              │ registered   │
│                              │ JWK → issue  │
│                              │ access token │
│                              └─────────────┤
│<──── access token (admin scope) ───────────│
│                                            │
├─ Call gRPC :7443 with access token ───────>│
│                              ┌─────────────┤
│                              │ Oathkeeper   │
│                              │ middleware   │
│                              │ validates    │
│                              │ token via    │
│                              │ Hydra        │
│                              │ introspect   │
│                              └─────────────┤
│<──── RPC response ─────────────────────────│
```

No token caching. Fresh assertion + fresh access token per invocation.

## Auth Material Directory Layout

```
<DataDir>/auth/
├── cli/
│   ├── signing.key         ← CLI ES256 private key (0600, NEVER enters any container)
│   └── signing-jwk.json    ← CLI public key as JWK (bind-mounted into CP)
└── tls/
    ├── server.pem          ← Self-signed server cert (bind-mounted into CP)
    └── server.key          ← Server private key (bind-mounted into CP)
```

**Bind mount into CP (read-only):**
- `auth/tls/server.pem` — CP's TLS server identity (used by gRPC, Hydra, Kratos, Oathkeeper)
- `auth/tls/server.key` — CP's TLS server key
- `auth/cli/signing-jwk.json` — for Hydra `private_key_jwt` client registration

**Never enters any container (0600 permissions):**
- `auth/cli/signing.key` — CLI's ES256 JWT signing private key

No CA hierarchy — the server cert is self-signed. The CLI trusts it directly via cert pool. mTLS was dropped (see INV-B1-001).

## Invariants

### INV-B1-001: TLS is the transport layer on the gRPC admin API
- **Type**: must
- **Category**: security
- **Statement**: Every connection to the CP gRPC admin API (:7443) uses TLS with a self-signed server cert generated by the CLI. The CLI trusts this cert via its cert pool. Authorization is enforced by OAuth2 access tokens (INV-B1-002), not by client certificates. mTLS was considered but dropped: Hydra's public API does not support client cert enforcement, so the token endpoint already requires TLS + `private_key_jwt` (RFC 7523) — adding a second auth mechanism (mTLS) on the gRPC channel provides marginal benefit given both channels are localhost-only.
- **Violated when**: A gRPC connection to the CP succeeds without TLS, or the server cert is not validated by the client
- **Test approach**: integration (connect to :7443 without TLS → rejected; connect with TLS trusting the server cert → accepted)
- **Risk**: critical

### INV-B1-002: OAuth2 access tokens are the authorization layer
- **Type**: must
- **Category**: security
- **Statement**: After TLS authenticates the gRPC connection, every RPC (except Health) requires a valid OAuth2 access token with the `admin` scope, validated via Hydra token introspection. Authorization is distinct from authentication.
- **Violated when**: An RPC succeeds with valid TLS but no access token, or with a token lacking the required scope
- **Test approach**: integration (TLS + no token → rejected; TLS + token without `admin` scope → rejected; TLS + valid `admin` token → accepted)
- **Risk**: critical

### INV-B1-003: Hydra is the sole token issuer
- **Type**: must
- **Category**: security
- **Statement**: Access tokens are issued only by Hydra via its `/oauth2/token` endpoint using the `client_credentials` grant with `private_key_jwt` client authentication (RFC 7523). The CLI signs a short-lived JWT assertion (iss, sub, aud, jti, exp, iat) with its ES256 private key and sends it as `client_assertion`. Hydra verifies the signature against the registered JWK. No hand-rolled token issuance. No other code path produces access tokens.
- **Violated when**: Access tokens are created by any code other than Hydra, or Hydra accepts a grant type other than `client_credentials` for the CLI client, or the CLI authenticates with a method other than `private_key_jwt`
- **Test approach**: unit (verify Hydra client registration config specifies `client_credentials` grant + `private_key_jwt` + `ES256`); static (grep for `go-jose` sign calls outside `authkeeper/`); integration (full token flow through Hydra)
- **Risk**: critical

### INV-B1-004: Auth interceptor enforces per-method scopes via Hydra introspection
- **Type**: must
- **Category**: security
- **Statement**: All gRPC admin API authorization is enforced by `AuthInterceptor` which validates bearer tokens via Hydra's admin introspection endpoint (RFC 7662). Per-method scope requirements are defined in `AdminMethodScopes()`. Fail-closed on unmapped methods — any method not in the scope map is denied. The Oathkeeper binary runs as an HTTP reverse proxy for future webui auth; gRPC auth bypasses Oathkeeper entirely. The vendored Oathkeeper gRPC middleware (`authkeeper/`) was removed because it pulled in heavy Ory Go dependencies with CVEs whose preconditions don't apply.
- **Violated when**: Authorization logic is scattered across handlers instead of the interceptor, or an unmapped gRPC method is allowed through, or token validation bypasses Hydra introspection
- **Test approach**: unit (unmapped method → denied; valid token + correct scope → allowed; valid token + wrong scope → denied; no token → denied)
- **Risk**: critical

### INV-B1-005: Hydra admin API is internal only
- **Type**: must
- **Category**: security
- **Statement**: Hydra's admin API listens on `127.0.0.1` inside the CP container. It is never published to the host or exposed to any Docker network. Only the clawker-cp process (running inside the same container) calls it.
- **Violated when**: Hydra admin port is bound to `0.0.0.0`, or is published in the container's port mapping, or is reachable from any other container or the host
- **Test approach**: unit (verify Hydra startup config binds admin to `127.0.0.1`; verify container port mapping excludes admin port)
- **Risk**: critical

### INV-B1-006: CLI private key material never enters containers
- **Type**: must
- **Category**: security
- **Statement**: The CLI's ES256 signing private key (`cli/signing.key`) is never bind-mounted, copied, or otherwise placed inside any container. The CP receives only the public JWK (`cli/signing-jwk.json`) and the server TLS cert+key. The signing key is created with 0600 permissions.
- **Violated when**: CLI signing key appears in any container mount config, Dockerfile COPY, or env var
- **Test approach**: unit (assert `HostConfig.Mounts` in CP container creation code excludes the signing key path; assert allowed public material IS mounted)
- **Risk**: critical

### INV-B1-007: CLI client registered via JWK at container creation
- **Type**: must
- **Category**: security
- **Statement**: The CLI's OAuth2 client is registered with Hydra by the CP startup code using the Hydra Go SDK (`github.com/ory/hydra-client-go`), reading the CLI's public key (JWK) from a bind-mounted file, with `token_endpoint_auth_method: private_key_jwt`, `token_endpoint_auth_signing_alg: ES256`, and `grant_types: [client_credentials]`. No unauthenticated registration endpoint exists. No docker exec. In-memory store means every CP boot registers from scratch — idempotent by design. Registration failure = CP crash (fail-fast).
- **Violated when**: Client registration happens over a network endpoint, or requires docker exec, or persists across CP restarts, or uses a different auth method than `private_key_jwt`
- **Test approach**: unit (CP startup registers client with correct JWK and config; second boot re-registers cleanly)
- **Risk**: high

### INV-B1-008: Admin port published to localhost only and configurable
- **Type**: must
- **Category**: security
- **Statement**: The CP gRPC port (default 7443), Hydra public port, and Oathkeeper HTTP port are all published to `127.0.0.1` on the host. Never `0.0.0.0`. The CP admin port is defined in the Settings schema (`settings.yaml`) with a `Config` accessor method, `default:"7443"` tag, and proper `yaml`/`label`/`desc` tags. Never hardcoded in container creation code.
- **Violated when**: Any CP port is published to `0.0.0.0`, or the admin port is hardcoded without a config accessor
- **Test approach**: unit (verify container config binds all published ports to `127.0.0.1`; verify port comes from Settings schema accessor)
- **Risk**: high

### INV-B1-009: CP container is infrastructure, not filtered
- **Type**: must
- **Category**: functional
- **Statement**: The CP container is NOT added to the eBPF `container_map`. It is infrastructure (same category as Envoy and CoreDNS), not a filtered agent container. eBPF enforcement applies to agent containers only. The CP's outbound traffic is unfiltered.
- **Violated when**: `Enable()` is called on the CP container's cgroup, or the CP container ID appears in `container_map`
- **Test approach**: unit (verify CP container creation code does not call Enable; verify no eBPF attach targeting CP container)
- **Risk**: high

### INV-B1-010: eBPF lifecycle ordering preserved
- **Type**: must
- **Category**: functional
- **Statement**: The CP calls `ebpf.Manager.Load()` and pins BPF maps before serving any RPCs or reporting healthy on `/healthz`. An internal ready atomic bool is set only after `Load()` returns without error. The `/healthz` handler reads this bool — it returns non-200 until the bool is set. CoreDNS must not start until the CP has pinned the `dns_cache` map.
- **Violated when**: `/healthz` returns 200 before Load() completes, or CoreDNS starts before BPF maps are pinned
- **Test approach**: unit (mock `ebpf.Manager` that blocks Load() indefinitely → assert `/healthz` returns 503; let Load() complete → assert `/healthz` returns 200)
- **Risk**: high

### INV-B1-011: Auth and firewall TLS material are separate
- **Type**: must-not
- **Category**: security
- **Statement**: The CP server cert (`<DataDir>/auth/tls/`) and the firewall MITM CA (`<DataDir>/firewall/certs/`) are separate key material, separate directories, separate rotation commands (`clawker auth rotate` vs `clawker firewall rotate-ca`). Compromise of one must not cascade. The CP uses a self-signed server cert (no CA hierarchy). A CA for agent certs is a Branch 4 decision.
- **Violated when**: Auth server cert signs a domain cert, MITM CA signs a server cert, or either references the other's key material
- **Test approach**: unit (assert `auth/tls/` and `firewall/certs/` paths are distinct)
- **Risk**: critical

### INV-B1-012: Client assertion claims (RFC 7523 / Hydra spec)
- **Type**: must
- **Category**: security
- **Statement**: The CLI's client assertion (JWT used in `private_key_jwt`) contains the following claims per Hydra's requirements:
  - `iss` (REQUIRED): must be the `client_id` of the CLI OAuth client
  - `sub` (REQUIRED): must be the `client_id` of the CLI OAuth client
  - `aud` (REQUIRED): must be the URL of Hydra's token endpoint (the intended audience)
  - `jti` (REQUIRED): cryptographically random unique ID — single-use, never replayed
  - `exp` (REQUIRED): expiration — short-lived (30-60 seconds)
  - `iat` (OPTIONAL but included): time the assertion was issued
  Signed with ES256 (ECDSA P-256). Generated fresh per CLI invocation. Never cached or reused.
- **Violated when**: Assertion accepted without any REQUIRED claim, or with a replayed `jti`, or after `exp`, or signed with wrong algorithm
- **Test approach**: unit (missing iss → rejected; missing aud → rejected; missing jti → rejected; expired → rejected; wrong aud → rejected; wrong alg → rejected; valid → accepted)
- **Risk**: high

### INV-B1-013: CP health via HTTP endpoint with hard prerequisites
- **Type**: must
- **Category**: functional
- **Statement**: The CP exposes an HTTP `/healthz` endpoint for readiness probing. No ready files, no filesystem-based signaling. Returns 200 only after ALL startup steps succeed: all subprocesses healthy, CLI client registered with Hydra, eBPF programs loaded (ready atomic bool set), gRPC server serving. Any startup step failure crashes the CP with a diagnostic error — no partial-start states, no swallowed errors.
- **Violated when**: Readiness signaled via a file, or `/healthz` returns 200 before full initialization, or a startup step fails silently without crashing the CP
- **Test approach**: unit (healthz during startup → non-200; healthz after full init → 200; mock subprocess failure → CP exits non-zero)
- **Risk**: medium

### INV-B1-014: Auth material creation is idempotent
- **Type**: must
- **Category**: functional
- **Statement**: `clawker auth rotate` and the CLI's first-use bootstrap check cert/key existence and validity. If material exists and is valid, no action. If missing, create it (private keys with 0600 permissions). If rotation explicitly requested (`--force`), regenerate all material and reload CP. Safe to run repeatedly.
- **Violated when**: Running twice without `--force` changes files, or existing valid material is overwritten without explicit request, or private keys are created with permissions other than 0600
- **Test approach**: unit (run twice → same files; run with --force → new files; missing files → created with correct permissions)
- **Risk**: medium

### INV-B1-015: Distroless CP image
- **Type**: must
- **Category**: security
- **Statement**: The CP container uses a distroless base image (`gcr.io/distroless/static-debian12`). Four static binaries (clawker-cp, hydra, oathkeeper, kratos), no shell, no package manager, no OS userspace. Minimizes attack surface and image size.
- **Violated when**: Final image stage uses a non-distroless base, or includes a shell, or includes unnecessary binaries
- **Test approach**: unit (parse Dockerfile, assert final `FROM` is distroless); e2e (verify `docker run --rm <image> /bin/sh` exits non-zero)
- **Risk**: medium

### INV-B1-016: Admin and agent channels are separate proto packages
- **Type**: must
- **Category**: security
- **Statement**: Admin operations (CLI) and agent operations (clawkerd) are defined as separate gRPC services in separate proto packages (`AdminService` in `api/admin/v1/`, `AgentService` in `api/agent/v1/`). No shared service definition. Branch 1 defines both proto packages but only implements `AdminService`. Runtime scope enforcement (agent-scoped token cannot call admin RPCs) is tested in Branch 4 when `AgentService` is implemented.
- **Violated when**: Admin RPCs and agent RPCs share a proto package or gRPC service definition
- **Test approach**: unit (assert `api/admin/v1/` and `api/agent/v1/` are separate packages with no shared service; no `AgentService` RPCs in `AdminService` proto)
- **Risk**: critical

### INV-B1-017: No eBPF regression on published ports
- **Type**: must
- **Category**: functional
- **Statement**: The eBPF programs must not block host-to-container traffic on published ports (7443 gRPC, 4444 Hydra public, 4456 Oathkeeper HTTP, 8080 healthz). Published ports use Docker's port forwarding (host → container), not container outbound (which eBPF filters). The CP is not in `container_map` (INV-B1-009).
- **Violated when**: CLI cannot connect to any published CP port when eBPF programs are attached
- **Test approach**: integration (start CP with eBPF active, verify CLI can reach all four published ports)
- **Risk**: high

### INV-B1-018: CP container labels
- **Type**: must
- **Category**: functional
- **Statement**: The CP container carries `dev.clawker.managed=true` and `dev.clawker.purpose=controlplane` labels, plus any other labels required by the existing container lifecycle patterns (matching Envoy/CoreDNS label conventions).
- **Violated when**: CP container is created without required labels, or uses a different `purpose` value
- **Test approach**: unit (assert container creation config includes all required labels)
- **Risk**: medium

## Prohibitions

### PRH-B1-001: No UDS listeners
- **Statement**: The CP must not use Unix domain socket listeners for any service. All communication is TCP with mTLS (gRPC) or TLS (Hydra).
- **Detection**: grep for `net.ListenUnix`, `ListenUnix`, `unix` in listener code
- **Consequence**: UDS + bind mounts cause chmod failures on Docker Desktop VirtioFS. TCP with mTLS is portable.

### PRH-B1-002: No hand-rolled token issuance
- **Statement**: No hand-rolled token issuance, OIDC endpoints, or JWT signing/verification for auth server purposes. Token issuance is Hydra. Token validation is via Hydra's admin introspection endpoint (RFC 7662), called by `AuthInterceptor`. The vendored Oathkeeper gRPC middleware was removed (heavy deps, CVEs); the `AuthInterceptor` is a thin introspection client, not a token issuer. CLI assertion signing (for `private_key_jwt`) uses `go-jose/v4` which is acceptable — it's a standard JWT library, not hand-rolled auth server code. The old `oidc_provider.go`, `ca.go`, and `oidc_clients.go` are deleted.
- **Detection**: grep for custom JWT signing outside CLI assertion code, `/token` HTTP handlers
- **Consequence**: Hand-rolled token issuance is a security liability. Hydra handles all token lifecycle.

### PRH-B1-003: No `docker exec` as ecosystem interface
- **Statement**: The `docker exec ebpf-manager <cmd>` pattern is permanently retired. All CP communication is via authenticated gRPC. CP startup code handles Hydra registration internally via the Go SDK — no docker exec needed.
- **Detection**: grep for `ExecCreate` or `ExecStart` in ongoing communication paths
- **Consequence**: Bypasses auth layer, breaks audit trail.

### PRH-B1-004: No auth downgrades
- **Statement**: No `insecure.NewCredentials()` on any production gRPC connection to the CP. No skipping token verification. No disabling mTLS.
- **Detection**: grep for `insecure.NewCredentials` in CP client code
- **Consequence**: Authentication bypass, security regression.

### PRH-B1-005: No ready files
- **Statement**: The CP must not use filesystem-based readiness signaling. Readiness is determined via HTTP `/healthz`.
- **Detection**: grep for `cp-ready`, `writeReadyFile`, `readyFile`
- **Consequence**: Ready files require shared filesystem, add race conditions.

### PRH-B1-006: No Hydra admin API exposure
- **Statement**: Hydra's admin API port must never be published to the host or bound to `0.0.0.0` inside the container.
- **Detection**: verify container port mapping and Hydra bind address config
- **Consequence**: Unprotected admin API → anyone on the network can register clients and issue tokens.

## Boundary Conditions

### BND-B1-001: CP container crash during agent start
- **Boundary**: TB-001 (CLI-CP)
- **Input from**: CLI calling EnsureControlPlane during container start
- **Validation required**: HTTP `/healthz` returns 200 before proceeding. CLI does not attempt any connection until healthz passes.
- **Failure mode**: fail-closed (container start fails with "control plane unavailable")

### BND-B1-002: Cert material missing on CLI cold start
- **Boundary**: TB-001 (CLI-CP)
- **Input from**: CLI needing to connect before auth material exists
- **Validation required**: Check cert/key/JWK existence
- **Failure mode**: fail-closed (CLI generates all material idempotently before proceeding)

### BND-B1-003: Client assertion clock skew
- **Boundary**: TB-001 (CLI-CP)
- **Input from**: CLI-signed assertion with iat/exp
- **Validation required**: Hydra allows configurable clock skew tolerance (default 30s)
- **Failure mode**: fail-closed (token request rejected with clear error)

### BND-B1-004: CP port already in use
- **Boundary**: Host network
- **Input from**: Another process holding port 7443
- **Validation required**: Docker port publish fails with clear error
- **Failure mode**: fail-closed (CLI reports port conflict with guidance to configure different port via Settings)

### BND-B1-005: Hydra subprocess crash
- **Boundary**: CP internal
- **Input from**: Hydra process exits unexpectedly
- **Validation required**: CP monitors subprocess PID
- **Failure mode**: CP crashes immediately (fail-fast). Docker restart policy handles restart. CLI gets "control plane unavailable" on next attempt.

### BND-B1-006: CP startup step failure
- **Boundary**: CP internal
- **Input from**: Any step in the startup sequence (1-8) fails
- **Validation required**: Each step checks its result
- **Failure mode**: CP crashes with diagnostic error in `docker logs`. No partial states. `/healthz` never returns 200.

## Resolved Questions

| Question | Decision | Rationale |
|----------|----------|-----------|
| Default admin port | 7443 | Avoids Kubernetes (6443), Tomcat (8443), generic HTTPS-alt (4443), common dev ports. Configurable via Settings schema. |
| Token caching | None — fresh token per invocation | CLI invocations are infrequent. One extra Hydra call per invocation is sub-millisecond on localhost. Eliminates token file, flock, atomic writes, 403-triggered refresh. Can add caching later if needed. |
| CP readiness | HTTP `/healthz` with hard prerequisites | Every startup step is a hard prerequisite. Any failure = crash. No ready files, no partial states. |
| Auth rotation | `clawker auth rotate` | Idempotent. Separate from `clawker firewall rotate-ca` (MITM CA). |
| Auth library | Ory stack (Hydra + Oathkeeper + Kratos) | Battle-tested. No hand-rolled auth. Handles all four client types across the initiative. |
| CLI token endpoint auth | `private_key_jwt` (RFC 7523) with ES256 | Hydra supports it. RFC 8705 (`tls_client_auth`) not implemented in fosite/Hydra — future upstream contribution. |
| mTLS scope | gRPC admin API only | Hydra's public API does not natively support client cert enforcement. TLS + `private_key_jwt` on token endpoint. |
| Transport | TCP only, no UDS | UDS chmod crash on Docker Desktop. TCP with mTLS/TLS is portable. |
| Storage | In-memory (Hydra) | CP is ephemeral. No sessions/passwords to persist. Everything machine-generated on boot. |
| CLI client registration | CP startup code via Hydra Go SDK | No unauthenticated endpoints. No docker exec. JWK bind-mounted at container creation. Registration failure = crash. |
| Oathkeeper gRPC middleware | Vendored into `internal/controlplane/authkeeper/` | Avoids GitHub security check failures. Vuln preconditions don't apply to our setup. |
| Image base | `gcr.io/distroless/static-debian12` | Minimal size + attack surface. No shell. Four static binaries. |
| All Ory components from Branch 1 | Yes — Kratos and Oathkeeper HTTP running even if unused | Catch integration issues early. Branch 7 flips switches, not rebuilds. |
| CP in container_map | No — CP is infrastructure | Same as Envoy/CoreDNS. eBPF filters agent containers, not infrastructure. |
| Admin port config location | Settings schema (`settings.yaml`) | Not project config. Needs Settings struct field + Config accessor. |
| CP container labels | `dev.clawker.purpose=controlplane`, `dev.clawker.managed=true` | Standard label pattern, same as Envoy/CoreDNS. |
| Subprocess crash handling | Fail-fast — CP crashes | No restart loops inside the container. Docker restart policy handles restarts. |

## Reference

| What | Where |
|------|-------|
| Master initiative spec | `.correctless/specs/control-plane-initiative.md` |
| CP package docs | `internal/controlplane/CLAUDE.md` |
| Firewall package docs | `internal/firewall/CLAUDE.md` |
| Auth material paths | `internal/consts/consts.go` lines 243-266 |
| Existing CP binary | `cmd/clawker-cp/main.go` |
| Existing auth code (to be replaced) | `internal/controlplane/authz.go`, `ca.go`, `oidc_provider.go`, `oidc_clients.go` |
| Existing CLI-side client (to be replaced) | `internal/firewall/oidc_client.go` |
| Existing protos (to be restructured) | `internal/clawkerd/protocol/v1/` → `api/{admin,agent}/v1/` |
| RFC 9700 | OAuth 2.0 Security BCP |
| RFC 8705 | mTLS Client Auth + Certificate-Bound Tokens (future upstream contribution) |
| RFC 7523 | JWT Profile for Client Authentication |
| Ory Hydra | https://github.com/ory/hydra |
| Ory Hydra Go SDK | `github.com/ory/hydra-client-go` |
| Ory Oathkeeper | https://github.com/ory/oathkeeper |
| Ory Kratos | https://github.com/ory/kratos |
| Oathkeeper gRPC middleware source | `github.com/ory/oathkeeper/middleware` (~400 lines) |

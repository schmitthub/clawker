# Auth Package

CLI-side authentication infrastructure for communicating with the clawker control plane (`clawker-controlplane` container). The CLI is the **trust orchestrator** — it generates all cryptographic material, persists it to the user's config dir, and bind-mounts the public halves into the CP container at startup.

## Role in the trust chain

```
CLI (host)                              Control Plane (container)
──────────                              ─────────────────────────
Generates + owns:                       Receives bind-mounted (RO):
  CA (P-256 ECDSA)                        CA cert (trust root)
  Signing key (ES256)                     CLI public JWK (verify assertions)
  Client cert (mTLS)                      Server cert + key (for TLS)
  Server cert + key                       CP client cert + key (outbound mTLS)
  Hydra shared secret (HMAC)              Infra intermediate CA cert + key
  JWK export                              (Hydra secret read via data dir)

Dials CP via:
  1. mTLS handshake (client cert)
  2. ES256 JWT assertion → Hydra /oauth2/token (client_credentials)
  3. Bearer access token on AdminService gRPC calls
```

**Invariant:** CLI private signing key is NEVER mounted into the container. Hydra verifies the `private_key_jwt` assertion against the registered JWK (public half only).

## Files

| File | Purpose |
|------|---------|
| `auth_material.go` | `EnsureAuthMaterial`, `RotateAuthMaterial`, `CheckAuthMaterial`, `EnsureHydraSecret`, `LoadSigningKey`, `LoadClientCert`, `ReadJWK`, `CACert`, `AuthFileStatus` |
| `agent_cert.go` | `MintAgentCert(caCertPath, caKeyPath string, project ProjectSlug, agent AgentName, containerID string)` returns an `AgentCert{CertPEM, KeyPEM, Thumbprint [32]byte}` — ephemeral 24h mTLS leaf signed by the CLI CA. **CN is the deterministic `consts.ContainerClawkerd` literal** (the binary identity, not a per-agent value); the per-agent `AgentFullName(project, agent)` → `clawker.<project>.<agent>` rides in a `urn:clawker:agent:<full-name>` URI SAN so a long random `docker.GenerateRandomName` output can't push the cert past x509's 64-byte CN limit. The `urn:clawker:container:<id>` URI SAN binds the cert to its container. Typed `ProjectSlug` / `AgentName` (built via `NewProjectSlug` / `NewAgentName` at the wire boundary) push validation upstream so the helper itself trusts its inputs. Helpers: `BuildAgentSAN` / `AgentFullNameFromCert` (agent SAN), `BuildContainerSAN` / `ContainerIDFromCert` (container SAN). `AgentFullNameFromCert` returns tri-state sentinels `ErrAgentSANMissing` / `ErrAgentSANMalformed` so the CP IdentityInterceptor can emit distinct structured-log events while presenting a uniform `PermissionDenied` over the wire. The thumbprint is SHA-256 over the cert DER. The CP-side Register handler captures the live peer cert thumbprint and writes the agent registry sqlite row — the CLI never opens the sqlite DB directly. The displayed AgentFullName is reconstructed on demand from the row's `project` + `agent_name` columns; there is no precomputed identity column. PEM material is returned for in-memory bootstrap delivery only; never persisted on the host. |
| `identity.go` | Typed identity values `ProjectSlug` / `AgentName` for compile-time discipline (callers can't accidentally pass a raw string); `New*` constructors reject only the empty case for `AgentName` (empty `ProjectSlug` is the global-scope-agent signal (2-segment naming)); `Must*` for test/migration paths. Charset / length / form constraints are NOT enforced here — input normalization for user-typed names happens upstream at `cmdutil.ProjectSlugify`, and Docker / x509 / `IdentityInterceptor` enforce their own constraints downstream at op time. |
| `assertion.go` | `BuildSignedAssertion`, `ValidateAssertionClaims`, `AssertionClaims` — ES256 JWT assertion builder for `private_key_jwt` client auth |
| `agent_assertion.go` | `BuildAgentAssertion(audience, signingKey)` + `AgentAssertionTTL` — ES256 client_assertion identifying clawkerd as the `clawker-agent` OAuth2 client. Same signing key as the CLI assertion; only iss/sub differ. iat is minted in the host clock (the source of truth — Docker forces the CP/VM clock to track the host); no iat correction is applied. The transient post-sleep window where the VM clock lags is handled by *waiting* until the CP clock has caught up to the host before the assertion is exchanged (the pre-start CP-ensure), not by shifting iat. 24h TTL covers typical container session length. |
| ~~`cp_dial.go`~~ | **Moved to `internal/controlplane/adminclient/dial.go`** — `Dial(ctx, adminPort, hydraPort, ...grpc.DialOption)` returns `(adminv1.AdminServiceClient, *grpc.ClientConn, error)`. See `adminclient` package. |

## Agent cert mint

`MintAgentCert(caCertPath, caKeyPath string, project ProjectSlug, agent AgentName, containerID string)` returns an `AgentCert{CertPEM, KeyPEM, Thumbprint [32]byte}` — an ephemeral 24h mTLS leaf signed by the CLI CA.

- Typed `ProjectSlug` / `AgentName` (built via `NewProjectSlug` / `NewAgentName` at the wire boundary) push validation upstream so the helper itself trusts its inputs.
- `Thumbprint` is SHA-256 over the cert DER. The CP-side Register handler captures the live peer cert thumbprint and writes the agent registry sqlite row; the CLI never opens the sqlite DB directly. The displayed `AgentFullName` is reconstructed on demand from the row's `project` + `agent_name` columns — there is no precomputed identity column.
- The x509 CN is the deterministic `consts.ContainerClawkerd` literal (the binary identity, not a per-agent value). Per-agent identity rides in SANs: `AgentFullName(project, agent)` → `clawker.<project>.<agent>` in a `urn:clawker:agent:<full-name>` URI SAN, and `containerID` in a `urn:clawker:container:<id>` URI SAN. Keeping identity out of the CN avoids x509's 64-byte CN limit for long random agent names.
- PEM material is returned for in-memory bootstrap delivery only; never persisted on the host.

## Auth material layout

All paths resolved via `internal/consts` (`AuthCACertPath`, `AuthCAKeyPath`, `AuthServerCertPath`, `AuthServerKeyPath`, `AuthCLIClientCertPath`, `AuthCLIClientKeyPath`, `AuthCLISigningKeyPath`, `AuthCLISigningJWKPath`, `HydraSystemSecretPath`, `AuthOtelServerCertPath`, `AuthOtelServerKeyPath`, `AuthCPClientCertPath`, `AuthCPClientKeyPath`, `AuthInfraCACertPath`, `AuthInfraCAKeyPath`).

| File | Scope | Bind-mounted into CP? |
|------|-------|------------------------|
| CA cert | shared trust root | Yes (RO) |
| CA private key | host-only | No |
| Server cert + key | CP TLS | Yes (RO) |
| CLI client cert + key | CLI mTLS | No (used by CLI only) |
| CLI signing key (ECDSA) | ES256 signer | No (private half stays on host) |
| CLI signing JWK | public JWK export | Yes (RO — Hydra verifies assertions against this) |
| Hydra shared secret | HMAC between CLI and Hydra | No (read inside CP via `auth.EnsureHydraSecret()` from data dir) |
| OTel server cert + key | TLS identity for the monitoring OTel collector | No (used by `monitor init`, not the CP container) |
| CP client cert + key | CP outbound mTLS identity (CN=ContainerCP, ClientAuth EKU) | Yes (RO) |
| Infra intermediate CA cert + key | CP signs runtime leaves for Envoy/CoreDNS | Yes (RO) — cert + key both mounted |

## Token exchange flow (moved to `adminclient/dial.go`)

The `Dial` function and token exchange logic live in `internal/controlplane/adminclient/dial.go`:

1. `adminclient.Dial(ctx, adminPort, hydraPort)` loads CA cert, signing key, and CLI client cert
2. Builds `tokenTLSCfg` (plain TLS, CA trust) and `grpcTLSCfg` (mTLS with client cert, CA trust)
3. Mints the assertion in the **host clock** (the source of truth — `AssertionClaims.Now` unset → `time.Now()`; Docker forces the CP/VM clock to track the host). Hydra/fosite validates `iat` with zero leeway, but the dial path does **not** re-check the clock: host↔CP convergence is gated once at CP bring-up by the cpboot readiness gate (`waitForCPClockSync`), which is the precondition for any CP interaction, so by the time the CLI dials AdminService the CP clock is already at/ahead of the host and a host-clock `iat` is in the CP's past. (`GetSystemTime`/`ProbeCPTime` remain the probe that gate polls — the CLI token path no longer uses them.)
4. Constructs a `tokenSource` that lazily fetches + caches access tokens
5. Returns a gRPC `ClientConn` with a unary interceptor that attaches `authorization: Bearer <token>` on every call
6. Token fetch: POST to `https://127.0.0.1:<hydraPort>/oauth2/token` with:
   - `grant_type=client_credentials`
   - `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`
   - `client_assertion=<ES256 JWT signed by CLI signing key>`
   - `scope=admin`
7. Token cached until expiry - 30s refresh margin

## Assertion claims (`assertion.go`)

```go
type AssertionClaims struct {
    Issuer           string    // "clawker-cli" (iss == client_id)
    Subject          string    // "clawker-cli" (sub == client_id)
    Audience         string    // Hydra token endpoint URL
    JWTID            string    // UUID per assertion (jti — replay protection)
    ExpiresInSeconds int       // duration to exp (CLI assertion ~30s; agent assertion = AgentAssertionTTL/24h)
    Now              time.Time // reference clock for iat/exp; zero → time.Now(),
                               // which is what production always uses. The host
                               // clock is the source of truth (the CP/VM clock is
                               // Docker-forced to track it), so no per-mint clock
                               // override is applied: the CLI's own assertion
                               // (adminclient.Dial) and the clawker-agent assertion
                               // baked into bootstrap material (BuildAgentAssertion)
                               // both mint at host time, after *waiting* for the CP
                               // clock to converge. clawkerd does not mint — it only
                               // exchanges the pre-minted agent assertion. This field
                               // is an explicit seam for deterministic tests.
}
```

`BuildSignedAssertion` signs with ES256 via `go-jose/v4/jwt`, setting `iat` to the reference clock with no backdate (callers wait until the CP clock has caught up to the host before exchanging, so a host-clock `iat` is already in the CP's past). `ValidateAssertionClaims` enforces non-empty fields and sane expiry.

## Rotation

`RotateAuthMaterial(forceSigningKey bool)`:

1. Removes CA cert + key, server cert + key, CLI client cert + key, OTel server cert + key, CP client cert + key, infra intermediate CA cert + key
2. If `forceSigningKey`: also removes signing key + JWK (invalidates Hydra client registration)
3. Re-runs `EnsureAuthMaterial` to regenerate

The CP container must be restarted after rotation to re-read bind-mounted material. `clawker auth rotate` command orchestrates this.

## Used by

- `internal/cmd/factory` — `adminClientFunc` calls `adminclient.Dial` to mint the gRPC `AdminServiceClient` (cached + re-dialed on transient gRPC failures)
- `internal/controlplane/cpboot` — `EnsureRunning` calls `EnsureAuthMaterial` so the CP container boots with a populated config dir
- `internal/cmd/auth` — `rotate` subcommand calls `RotateAuthMaterial`
- `internal/cmd/project/init` — calls `EnsureAuthMaterial` before container creation
- `internal/cmd/monitor/init` — calls `EnsureAuthMaterial` to provision OTel mTLS material before mounting it into the monitoring stack
- `cmd/clawker-cp` (via bind-mounts + `auth.EnsureHydraSecret()`) — reads CA, server cert, JWK, Hydra secret at container startup

## Tests

`auth_test.go` covers: material generation/rotation lifecycle, assertion signing + validation, file permissions, idempotency of `EnsureAuthMaterial`.

## Package imports

**Uses**: `internal/consts` (path constants), `go-jose/v4` (JWT signing), `github.com/google/uuid` (jti), stdlib `crypto/*`.

**No dependency on**: `internal/config`, `internal/logger` (pure crypto/IO — errors returned, caller logs).

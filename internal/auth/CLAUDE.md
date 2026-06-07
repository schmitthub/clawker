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
  Server cert + key                       Hydra shared secret
  Hydra shared secret (HMAC)
  JWK export

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
| `identity.go` | Typed identity values `ProjectSlug` / `AgentName` for compile-time discipline (callers can't accidentally pass a raw string); `New*` constructors reject only the empty case for `AgentName` (empty `ProjectSlug` is the global-scope-agent signal (2-segment naming)); `Must*` for test/migration paths; `AgentFullName(project, agent)` composer. Charset / length / form constraints are NOT enforced here — input normalization for user-typed names happens upstream at `cmdutil.ProjectSlugify`, and Docker / x509 / `IdentityInterceptor` enforce their own constraints downstream at op time. |
| `assertion.go` | `BuildSignedAssertion`, `ValidateAssertionClaims`, `AssertionClaims` — ES256 JWT assertion builder for `private_key_jwt` client auth |
| `agent_assertion.go` | `BuildAgentAssertion(audience, signingKey, skew)` + `AgentAssertionTTL` — ES256 client_assertion identifying clawkerd as the `clawker-agent` OAuth2 client. Same signing key as the CLI assertion; only iss/sub differ. `skew` aligns the (host-minted, CP-validated) assertion's iat to the CP clock domain, mirroring `adminclient.Dial`'s correction for the CLI assertion. 24h TTL covers typical container session length. |
| ~~`cp_dial.go`~~ | **Moved to `internal/controlplane/adminclient/dial.go`** — `Dial(ctx, adminPort, hydraPort, log, ...grpc.DialOption)` returns `(adminv1.AdminServiceClient, *grpc.ClientConn, error)`. See `adminclient` package. |

## Agent cert mint

`MintAgentCert(caCertPath, caKeyPath string, project ProjectSlug, agent AgentName)` returns an `AgentCert{CertPEM, KeyPEM, Thumbprint [32]byte}` — an ephemeral 24h mTLS leaf signed by the CLI CA.

- Typed `ProjectSlug` / `AgentName` (built via `NewProjectSlug` / `NewAgentName` at the wire boundary) push validation upstream so the helper itself trusts its inputs.
- `Thumbprint` is SHA-256 over the cert DER. The CP-side Register handler captures the live peer cert thumbprint and writes the agent registry sqlite row; the CLI never opens the sqlite DB directly.
- The CN is composed via `CanonicalAgentCN(project, agent)` and pre-stored on the registry row alongside `AgentFullName` (the canonical `clawker.<project>.<agent>` identity used everywhere else in the codebase — `CanonicalAgentCN` exists only as the cert-subject format helper, not a general identity name).
- PEM material is returned for in-memory bootstrap delivery only; never persisted on the host.

## Auth material layout

All paths resolved via `internal/consts` (`AuthCACertPath`, `AuthCAKeyPath`, `AuthServerCertPath`, `AuthServerKeyPath`, `AuthCLIClientCertPath`, `AuthCLIClientKeyPath`, `AuthCLISigningKeyPath`, `AuthCLISigningJWKPath`, `AuthHydraSecretPath`).

| File | Scope | Bind-mounted into CP? |
|------|-------|------------------------|
| CA cert | shared trust root | Yes (RO) |
| CA private key | host-only | No |
| Server cert + key | CP TLS | Yes (RO) |
| CLI client cert + key | CLI mTLS | No (used by CLI only) |
| CLI signing key (ECDSA) | ES256 signer | No (private half stays on host) |
| CLI signing JWK | public JWK export | Yes (RO — Hydra verifies assertions against this) |
| Hydra shared secret | HMAC between CLI and Hydra | Yes (RO) |
| Infra intermediate CA cert + key | CP signs runtime leaves for Envoy/CoreDNS | Yes (RO) — key stays on host + in CP |

## Token exchange flow (moved to `adminclient/dial.go`)

The `Dial` function and token exchange logic live in `internal/controlplane/adminclient/dial.go`:

1. `adminclient.Dial(ctx, adminPort, hydraPort, log)` loads CA cert, signing key, and CLI client cert
2. Builds `tokenTLSCfg` (plain TLS, CA trust) and `grpcTLSCfg` (mTLS with client cert, CA trust)
3. Measures host↔CP clock skew: dials a short-lived mTLS connection (no bearer token) and calls the PUBLIC `GetSystemTime` RPC, computing the offset (`measureClockSkew`/`clockSkew`) to add to the local clock to reach the CP's clock domain. Assertions are then minted in CP-aligned time via `AssertionClaims.Now`, because clawker-cp and Hydra share a container clock and Hydra/fosite validates the assertion's `iat` with zero clock-skew leeway. A small residual leeway floor (`assertionClockSkewLeeway`) covers the measurement remainder. The probe runs lazily on first token fetch, so a transient failure during CP bring-up self-heals on retry. A failed or implausible (`> maxPlausibleClockSkew`) measurement is discarded — never cached — and `adminclient.Dial` is passed a `*logger.Logger` so each degrade emits a structured `event=clock_skew_probe_unavailable` / `clock_skew_implausible` line; an operator can then tie a later Hydra "Token used before issued" 500 back to a skew-probe failure instead of re-debugging the opaque error.
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
    ExpiresInSeconds int       // duration to exp (typically 30-60s)
    Now              time.Time // reference clock for iat/exp; zero → time.Now().
                               // Both host-minted assertions set this to CP-aligned
                               // time (local now + skew from ProbeClockSkew) so iat
                               // lands in the CP clock domain: the CLI's own assertion
                               // (adminclient.Dial) and the clawker-agent assertion
                               // the CLI bakes into bootstrap material
                               // (BuildAgentAssertion). clawkerd does not mint — it
                               // only exchanges the pre-minted agent assertion.
}
```

`BuildSignedAssertion` signs with ES256 via `go-jose/v4/jwt`, backdating `iat` by a small residual leeway floor (`assertionClockSkewLeeway`) on top of the reference clock. `ValidateAssertionClaims` enforces non-empty fields and sane expiry.

## Rotation

`RotateAuthMaterial(forceSigningKey bool)`:

1. Removes CA cert + key, server cert + key, CLI client cert + key
2. If `forceSigningKey`: also removes signing key + JWK (invalidates Hydra client registration)
3. Re-runs `EnsureAuthMaterial` to regenerate

The CP container must be restarted after rotation to re-read bind-mounted material. `clawker auth rotate` command orchestrates this.

## Used by

- `internal/cmd/factory` — `adminClientFunc` calls `adminclient.Dial` to mint the gRPC `AdminServiceClient` (cached + re-dialed on transient gRPC failures)
- `internal/controlplane/cpboot` — `EnsureRunning` calls `EnsureAuthMaterial` so the CP container boots with a populated config dir
- `internal/cmd/auth` — `rotate` subcommand calls `RotateAuthMaterial`
- `cmd/clawker-cp` (via bind-mounts, not imports) — reads CA, server cert, JWK, Hydra secret at container startup

## Tests

`auth_test.go` covers: material generation/rotation lifecycle, assertion signing + validation, file permissions, idempotency of `EnsureAuthMaterial`.

## Package imports

**Uses**: `internal/consts` (path constants), `api/admin/v1` (gRPC client type), `go-jose/v4`, stdlib `crypto/*`, `google.golang.org/grpc`.

**No dependency on**: `internal/config`, `internal/logger` (pure crypto/IO — errors returned, caller logs).

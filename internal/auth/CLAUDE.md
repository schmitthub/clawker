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
| `agent_cert.go` | `MintAgentCert(caCertPath, caKeyPath string, project ProjectSlug, agent AgentName, containerID string)` returns an `AgentCert{CertPEM, KeyPEM, Thumbprint [32]byte}` — ephemeral 24h mTLS leaf signed by the CLI CA. **CN is the deterministic `consts.ContainerClawkerd` literal** (the binary identity, not a per-agent value); the per-agent `AgentFullName(project, agent)` → `clawker.<project>.<agent>` rides in a `urn:clawker:agent:<full-name>` URI SAN so a long random `docker.GenerateRandomName` output can't push the cert past x509's 64-byte CN limit. The `urn:clawker:container:<id>` URI SAN binds the cert to its container. Typed `ProjectSlug` / `AgentName` (built via `NewProjectSlug` / `NewAgentName` at the wire boundary) push validation upstream so the helper itself trusts its inputs. Helpers: `BuildAgentSAN` / `AgentFullNameFromCert` (agent SAN), `BuildContainerSAN` / `ContainerIDFromCert` (container SAN). The thumbprint is SHA-256 over the cert DER. The CP-side Register handler captures the live peer cert thumbprint and writes the agent registry sqlite row — the CLI never opens the sqlite DB directly. The displayed AgentFullName is reconstructed on demand from the row's `project` + `agent_name` columns; there is no precomputed identity column. PEM material is returned for in-memory bootstrap delivery only; never persisted on the host. |
| `assertion.go` | `BuildSignedAssertion`, `ValidateAssertionClaims`, `AssertionClaims` — ES256 JWT assertion builder for `private_key_jwt` client auth |
| `agent_assertion.go` | `BuildAgentAssertion(audience, signingKey)` + `AgentAssertionTTL` — ES256 client_assertion identifying clawkerd as the `clawker-agent` OAuth2 client. Same signing key as the CLI assertion; only iss/sub differ. 24h TTL covers typical container session length. |
| ~~`cp_dial.go`~~ | **Moved to `internal/controlplane/adminclient/dial.go`** — `Dial(ctx, adminPort, hydraPort, ...grpc.DialOption)` returns `adminv1.AdminServiceClient`. See `adminclient` package. |

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

The `Dial` function and token exchange logic have moved to `internal/controlplane/adminclient/dial.go`. The flow is unchanged:

1. `adminclient.Dial(ctx, adminPort, hydraPort)` loads CA cert, signing key, and CLI client cert
2. Builds `tokenTLSCfg` (plain TLS, CA trust) and `grpcTLSCfg` (mTLS with client cert, CA trust)
3. Constructs a `tokenSource` that lazily fetches + caches access tokens
4. Returns a gRPC `ClientConn` with a unary interceptor that attaches `authorization: Bearer <token>` on every call
5. Token fetch: POST to `https://127.0.0.1:<hydraPort>/oauth2/token` with:
   - `grant_type=client_credentials`
   - `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`
   - `client_assertion=<ES256 JWT signed by CLI signing key>`
   - `scope=admin`
6. Token cached until expiry - 30s refresh margin

## Assertion claims (`assertion.go`)

```go
type AssertionClaims struct {
    Issuer    string        // "clawker-cli"
    Subject   string        // "clawker-cli"
    Audience  string        // Hydra token URL
    ExpiresIn time.Duration // typically 5 min
    JTI       string        // UUID per assertion (replay protection)
}
```

`BuildSignedAssertion` signs with ES256 via `go-jose/v4/jwt`. `ValidateAssertionClaims` enforces non-empty fields and sane expiry.

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

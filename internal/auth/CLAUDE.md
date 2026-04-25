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
| `agent_cert.go` | `MintAgentCert(caCertPath, caKeyPath, agentName)` returns an `AgentCert{CertPEM, KeyPEM, ThumbprintHex}` — ephemeral 24h mTLS leaf signed by the CLI CA. The thumbprint is lowercase-hex SHA-256 over the cert DER and is announced to the CP via `AdminService.AnnounceAgent` so any peer cert that doesn't match at `AgentService.Register` is rejected (cert-swap defense). PEM material is returned for tmpfs delivery only; never persisted on the host. |
| `assertion.go` | `BuildSignedAssertion`, `ValidateAssertionClaims`, `AssertionClaims` — ES256 JWT assertion builder for `private_key_jwt` client auth |
| `cp_dial.go` | `DialCPAdmin(ctx, adminPort, hydraPort)` → `adminv1.AdminServiceClient` — builds two TLS configs (Hydra plain TLS + AdminService mTLS) and a gRPC client with token-refreshing unary interceptor |

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

## Token exchange flow (`cp_dial.go`)

1. `DialCPAdmin(ctx, adminPort, hydraPort)` loads CA cert, signing key, and CLI client cert
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

- `internal/firewall` — `DialCPAdmin` for gRPC client; `EnsureAuthMaterial` at `Manager.EnsureRunning()`
- `internal/cmd/auth` — `rotate` subcommand calls `RotateAuthMaterial`
- `cmd/clawker-cp` (via bind-mounts, not imports) — reads CA, server cert, JWK, Hydra secret at container startup

## Tests

`auth_test.go` covers: material generation/rotation lifecycle, assertion signing + validation, file permissions, idempotency of `EnsureAuthMaterial`.

## Package imports

**Uses**: `internal/consts` (path constants), `api/admin/v1` (gRPC client type), `go-jose/v4`, stdlib `crypto/*`, `google.golang.org/grpc`.

**No dependency on**: `internal/config`, `internal/logger` (pure crypto/IO — errors returned, caller logs).

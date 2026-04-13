# Verification: Branch 1 — Control Plane as a Proper Running Service

**Spec**: `.correctless/specs/cp-initiative/branch-1-cp-service.md`
**Branch**: `feat/control-plane`
**Intensity**: high
**Date**: 2026-04-13
**Commits verified**: 14eae3bf..ac269eef (8 commits)

## Rule Coverage

| Rule | Test | Status | Notes |
|------|------|--------|-------|
| INV-B1-001 | (structural — `cmd/clawker-cp/main.go:220-223`) | weak | TLS enforced via `grpc.Creds(credentials.NewTLS(tlsCfg))` in `main.go`. No test asserts that a non-TLS connection is rejected. Covered by inspection, not a failing test. |
| INV-B1-002 | `TestAuthInterceptor_NoToken_Denied`, `TestAuthInterceptor_ValidToken_CorrectScope_Allowed`, `TestAuthInterceptor_ValidToken_WrongScope_Denied` | covered | Tests exist but do not reference INV-B1-002 by ID. Behaviorally covers the invariant. |
| INV-B1-003 | (structural — `hydra_client.go:31-36`, `cp_dial.go:133-148`) | weak | Registration config specifies `client_credentials` + `private_key_jwt` + `ES256`. Token fetch uses `client_assertion_type: jwt-bearer`. No test asserts these specific fields on the Hydra registration payload. |
| INV-B1-004 | `TestAuthInterceptor_UnmappedMethod_Denied`, `TestAdminMethodScopes_CoversAllRPCs`, `TestAuthInterceptor_IntrospectionError_Denied`, `TestAuthInterceptor_MalformedAuthHeader_Denied` | covered | Fail-closed on unmapped method (empty scope map test), all RPCs have scope entries, introspection error = denied. Strong coverage. |
| INV-B1-005 | `TestINV_B1_005_HydraAdminInternalOnly` | covered | Asserts Hydra admin port not in published port bindings. |
| INV-B1-006 | `TestINV_B1_006_PrivateKeysNeverMounted`, `TestINV_B1_006_PublicMaterialIsMounted` | covered | Asserts signing key excluded from mounts. Asserts JWK, server cert, server key ARE mounted read-only. |
| INV-B1-007 | (structural — `hydra_client.go:31-36`) | UNCOVERED | No test verifies the Hydra client registration payload fields (`grant_types`, `token_endpoint_auth_method`, `token_endpoint_auth_signing_alg`, `scope`, `jwks`). The `RegisterCLIClient` function is only tested indirectly via E2E. |
| INV-B1-008 | `TestINV_B1_008_AllPortsPublishedToLocalhostOnly`, `TestINV_B1_008_AdminPortFromSettings`, `TestINV_B1_008_SettingsSchemaAdminPort` | covered | Verifies all ports publish to 127.0.0.1, admin port from Settings, correct default. |
| INV-B1-009 | `TestINV_B1_009_CPIsInfrastructureNotFiltered` | covered | Asserts purpose label is `controlplane`, not `agent`. |
| INV-B1-010 | `TestINV_B1_010_HealthzReturns503BeforeReady`, `TestINV_B1_010_HealthzReturns200AfterReady`, `TestINV_B1_010_IsReadyAtomicBool`, `TestINV_B1_010_EBPFLoadGatesHealthz` | covered | Comprehensive: atomic bool gating, /healthz 503 before SetReady, 200 after, concurrent Load() simulation. |
| INV-B1-011 | (structural — `consts.go:284-286` vs `consts.go:96,274`) | weak | Auth material at `auth/{ca,cli,tls}`, firewall MITM at `firewall/certs` — separate directory trees. No test asserts path disjointness. |
| INV-B1-012 | `TestValidateAssertionClaims`, `TestBuildSignedAssertion` | covered | Tests missing iss/sub/aud/jti/exp rejection. Tests ES256 signing, correct claims in payload, signature verification. |
| INV-B1-013 | `TestINV_B1_013_HealthzOnlyAfterFullInit`, `TestINV_B1_013_NoReadyFiles` | covered | Pre-init 503, post-init 200. Asserts HTTP-based readiness, not filesystem. |
| INV-B1-014 | `TestEnsureAuthMaterial_Idempotent`, `TestRotateAuthMaterial_ForceRegeneratesFiles`, `TestRotateAuthMaterial_PreservesSigningKeyWithoutForce`, `TestRotateAuthMaterial_Permissions`, `TestEnsureAuthMaterial_PrivateKeyPermissions` | covered | Idempotency, force rotation, 0600 permissions. |
| INV-B1-015 | `TestINV_B1_015_CPImageTag` | weak | Only asserts image tag matches `consts.CPImageTag`. Does not assert distroless base. The `cpImageSpec.dockerfile` in `manager.go:1278` uses `gcr.io/distroless/static-debian12` — verified by inspection only. |
| INV-B1-016 | `TestINV_B1_016_SeparateProtoPackages` | covered | Separate package paths, distinct service names, correct RPCs, registration compiles. |
| INV-B1-017 | `TestINV_B1_017_AllRequiredPortsPublished`, `TestINV_B1_017_CPNotInContainerMap` | covered | All 4 ports published, CP not in container_map. |
| INV-B1-018 | `TestINV_B1_018_CPContainerLabels` | covered | `dev.clawker.managed=true` and `dev.clawker.purpose=controlplane`. |

### Summary: 14 covered, 3 weak, 1 UNCOVERED

**UNCOVERED (BLOCKING):**
- **INV-B1-007**: No unit test for `RegisterCLIClient` verifying the Hydra registration payload fields. The function is tested only via live Hydra integration. A unit test should verify the JSON body contains `grant_types: [client_credentials]`, `token_endpoint_auth_method: private_key_jwt`, `token_endpoint_auth_signing_alg: ES256`.

**Weak (non-blocking, should be strengthened):**
- **INV-B1-001**: TLS transport is structural (the code uses `credentials.NewTLS`), but no test asserts that a plaintext gRPC connection is rejected.
- **INV-B1-003**: Hydra registration fields verified by inspection only, no assertion test.
- **INV-B1-011**: Auth/firewall path separation verified by inspection (different consts directory paths), no assertion test.
- **INV-B1-015**: Distroless base image verified by reading `cpImageSpec.dockerfile`, but the test only checks image tag.

## Prohibition Compliance

| Prohibition | Status | Evidence |
|-------------|--------|----------|
| PRH-B1-001: No UDS listeners | PASS | No `net.ListenUnix` or `"unix"` in listener code. All listeners use TCP. |
| PRH-B1-002: No hand-rolled token issuance | PASS | No custom JWT signing in CP code. Token issuance is Hydra. CLI assertion signing uses `go-jose/v4` (acceptable per spec). Old `oidc_provider.go`, `ca.go`, `oidc_clients.go` are deleted. |
| PRH-B1-003: No `docker exec` | PASS | No `ExecCreate` or `ExecStart` in CP communication paths. |
| PRH-B1-004: No auth downgrades | PASS with note | `insecure.NewCredentials()` found in `server.go:280` — CP-to-agent RunInit callback (pre-Branch 4 agent auth). Per spec, PRH-B1-004 applies to connections **to the CP**, not from the CP to agents. Authz_test.go uses insecure for in-process bufconn (acceptable in tests). |
| PRH-B1-005: No ready files | PASS with note | No `cp-ready`, `writeReadyFile`, or `readyFile` patterns in controlplane code. **However**, `manager.go:64-66` contains a stale comment referencing the old ready file pattern. See Drift section. |
| PRH-B1-006: No Hydra admin API exposure | PASS | `TestINV_B1_005_HydraAdminInternalOnly` confirms admin port not published. Hydra admin binds `127.0.0.1` in config (`ory_configs.go:39`). |

## Dependencies

New dependencies in `go.mod`:

| Dependency | Version | Introduced by | In spec? |
|------------|---------|---------------|----------|
| `github.com/go-jose/go-jose/v4` | v4.1.4 | `internal/auth/assertion.go` (JWT assertion signing) | Yes (spec says "go-jose/v4 for CLI assertion signing") |
| `google.golang.org/grpc` | v1.80.0 | Promoted from indirect to direct | Yes (gRPC admin API) |
| `google.golang.org/protobuf` | v1.36.11 | Promoted from indirect to direct | Yes (proto definitions) |

No unexpected dependencies. Version bumps to `golang.org/x/{mod,text,tools}` are transitive.

## Architecture Compliance

- **PAT-001 (Factory DI)**: Commands use Options structs with Factory closure fields (`Firewall`, `ProjectManager`). `NewCmdBypass` and `NewCmdRotate` follow the `NewCmd(f, runF)` pattern.
- **PAT-004 (Firewall Stack)**: CP integrates correctly as the 3rd infrastructure container. eBPF ops route through typed gRPC. Global `route_map` design preserved.
- **Error handling**: All errors returned via `fmt.Errorf("context: %w", err)`. No `logger.Fatal` in Cobra hooks.
- **Logging**: zerolog for file logging only. User output via `fmt.Fprintf` to IOStreams.
- **Config access**: Settings fields accessed via `cfg.Settings().ControlPlane`. Ports come from Settings schema with defaults via struct tags.
- **Package boundaries**: `internal/auth` handles CLI-side cert/key material and gRPC dial. `internal/controlplane` handles CP server, auth interceptor, admin handler. Clear separation.

**New pattern**: `internal/auth` package (CLI-side auth infrastructure). Not in `.correctless/ARCHITECTURE.md` — needs entry.

## QA Class Fixes Verified

- QA round 1 (from `tdd-test-edits.log`): Test bug in `TestINV_B1_008_AllPortsPublishedToLocalhostOnly` — compared `netip.Addr` with `string` using `assert.Equal`. Fix: compare `binding.HostIP.String()` instead. **Verified**: current test at `container_config_test.go:103` uses `binding.HostIP.String()`. Fix is in place and test passes.

## Smells

- `internal/firewall/manager.go:64-66`: Stale comment referencing old ready file pattern ("The CP writes `<firewallDataDir>/cp-ready`..."). The implementation correctly uses HTTP `/healthz` polling (`waitForCPReadyImpl` at line 1598). Comment should be updated.

## Drift

1. **Stale comment in `manager.go:64-66`**: Comment says "The CP writes `<firewallDataDir>/cp-ready` when it has loaded the BPF programs" but the implementation polls `/healthz`. This is cosmetic drift — the behavior is correct, only the comment is stale.

2. **INV-B1-015 spec says "distroless" but test only checks image tag**: The spec states the CP uses `gcr.io/distroless/static-debian12`. The `cpImageSpec` Dockerfile at `manager.go:1278` does use distroless. But `TestINV_B1_015_CPImageTag` only asserts `consts.CPImageTag == "clawker-cp:latest"` — it doesn't parse the Dockerfile or assert the base image is distroless.

3. **INV-B1-002 spec mentions mTLS but implementation dropped mTLS**: The spec's INV-B1-002 statement starts with "After mTLS authenticates the gRPC connection..." but INV-B1-001 explicitly dropped mTLS in favor of TLS-only. INV-B1-002 wording is internally inconsistent with INV-B1-001. The implementation is correct (TLS + OAuth2), but the spec text for INV-B1-002 should be updated to remove the mTLS reference.

## Spec Updates

- 1 update from TDD: Test assertion type mismatch fix (see QA section above).
- INV-B1-002 wording references "mTLS" but INV-B1-001 explicitly dropped mTLS. Spec should update INV-B1-002 to say "After TLS authenticates" instead of "After mTLS authenticates."

## Overall: FAIL with 1 BLOCKING finding, 3 weak tests, 3 drift items

### BLOCKING
- **INV-B1-007**: `RegisterCLIClient` has no unit test verifying the Hydra registration payload fields. The function constructs a JSON body with security-critical OAuth2 client configuration (`grant_types`, `token_endpoint_auth_method`, `token_endpoint_auth_signing_alg`). If any of these fields are wrong, the CLI cannot authenticate. A unit test should mock the Hydra admin endpoint and assert the request body.

### Recommended actions
1. Add a unit test for `RegisterCLIClient` that intercepts the HTTP request and asserts the payload fields match INV-B1-007. This resolves the BLOCKING finding.
2. Fix the stale comment at `manager.go:64-66` (reference to `cp-ready` file).
3. Update INV-B1-002 spec text to remove "mTLS" reference (align with INV-B1-001 decision to drop mTLS).
4. Consider adding tests for INV-B1-001 (plaintext rejection), INV-B1-011 (path disjointness), and INV-B1-015 (distroless assertion) to strengthen weak rules.

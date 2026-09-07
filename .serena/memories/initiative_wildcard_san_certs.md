# Initiative: fix/wildcard-san-certs (#500 + #518 + Envoy bump)

Branch: `fix/wildcard-san-certs`. Plan: `/home/clawker/.claude/plans/500-envoy-noble-cake.md` (approved 2026-09-07).

## Issues
- **#518**: shared wildcard DFP cluster (`https_dfp`/`wss_dfp`) reuses TLS1.3 session tickets across hosts → resumed session carries wrong cert → `auto_san_validation` "verify SAN list" 503 on first request after host switch. Root: `upstreamReencryptSocket` (`controlplane/firewall/envoy_upstream.go:358`) has no `max_session_keys`; Envoy client session cache is per-context, not per-SNI. CIDR origdst clusters share the same class.
- **#500**: downstream MITM leaf for wildcard rules = SANs `[apex, *.apex]` (`certs.go:150-156`); RFC 6125 wildcard = one label → 3+-label hosts fail client hostname verification.

## Ratified design (user-approved)
1. **Envoy image bump** 1.37.1 → 1.39.1 (`stack.go:36` `envoyImage`; CVE fixes). Multi-arch OCI index digest required; Docker Hub may be firewall-blocked in container → user runs `docker buildx imagetools inspect` on host. CoreDNS v1.14.7 already latest. go-control-plane already envoy v1.39.0.
2. **#518**: thread `multiHost bool` through `upstreamReencryptSocket` + `decorateReencrypt`; `max_session_keys: 0` when multiHost. buildHTTPSDFPCluster→true, httpsOriginalDstUpstreamLayer→true, buildTLSDNSCluster→false. Goldens only (envoy.md contract), expect 8 hunks in comprehensive+comprehensive_mtls; exact/http/ssh goldens must NOT change.
3. **#500**: per-SNI on-demand minting. Envoy 1.38+ `envoy.tls.certificate_selectors.on_demand_secret` + `sni` certificate mapper (types present in pinned go-control-plane v1.39.0). CP hosts small SDS gRPC server: request for secret `<sni>` → validate against stored wildcard https/wss zones (fail closed) → mint P-256 leaf SAN=[sni] via GenerateDomainCert refactor → return tls.v3.Secret. Wildcard chains only; exact/IP/CIDR keep static file certs. `downstreamMITMSocket` gains `custom_tls_certificate_selector` for wildcard chains; prefetch [apex]. QUIC: verify selector support in 1.39, else punt (static cert, document h3 fallback). mTLS between Envoy↔CP SDS (pattern: otel ALS context `envoy_config.go:564`). CP degrade-never-cascade, no panics.

## Key facts
- Config gen = `map[string]any` + `validateBootstrap` (envoy_validate.go:47) fail-closed protojson.
- Goldens: `controlplane/firewall/testdata/envoy/`; re-bless `GOLDEN_UPDATE=1 go test ./controlplane/firewall/ -run TestGenerateEnvoyConfig`.
- `certs_test.go:264` TestGenerateDomainCert_WildcardSANs pins old SAN shape — update, don't delete. New tests: `VerifyHostname` with 3-label hosts (2-label hides bug).
- `certBasename` couples cert writer ↔ envoy_tls.go:55 ↔ envoy_udp.go:157 (QUIC).
- Stack.Reload = full ContainerRestart. No CoreDNS→CP channel exists (not needed under SDS design; earlier CoreDNS-observation design REJECTED by user).
- Docs task: remove/avoid "wildcard = 1 level" caveat after fix; known-issues in clawker-plugin submodule (branch+PR+pointer bump).

## Status
- [x] Task 2 (#518 max_session_keys) — DONE, commit 81420fd6 (multiHost bool through upstreamReencryptSocket/decorateReencrypt; DFP + origdst true, exact false; 8 golden hunks; envoy.md verified-fact added)
- [x] Task 1 (Envoy bump 1.37.1→1.39.1) — DONE, commit 026f7283 (OCI index digest eb2c01c1…, amd64+arm64 verified via registry API; registry-1.docker.io + auth.docker.io firewall-allowed)
- [x] Task 3 (#500 SDS on-demand mint) — CODE DONE, uncommitted: GenerateSNICert + validateSNIHostname (certs.go); SDSServer delta-xDS (sds_server.go — fail-closed removed_resources, longest-zone admit, CA-serial cache); downstreamOnDemandMITMSocket selector variant on wildcard chains (envoy_tls.go); sds_cluster STRICT_DNS→clawker-controlplane:7445 mTLS via /etc/envoy/otel-tls (envoy_config.go); SDSConfig gated on infraCertsReady (stack.go sdsConfig); startSDSServer listener in internal/controlplane/cmd.go (CP server leaf + infra-intermediate ClientCAs, degrade event=sds_unavailable, runs pre-firewall-gate); ControlPlaneSettings.SDSPort default 7445 (+consts.DefaultCPSDSPort, parity test); comprehensive_mtls golden carries selector (2 wildcard chains) + sds_cluster; validateBootstrap blank imports added. QUIC: selector REJECTED by Envoy for QUIC (verified v1.39.1 config_test.cc) — QUIC keeps static certs, h3 multi-label falls back to TCP.
- [x] golangci v2.12.2→v2.13.2 (prek rev + CI lint.yml) — staticcheck vs Go 1.27 stdlib buildir panic, commit 58186513
- [x] gen-docs regenerated (configuration.mdx + settings.schema.json in diff)
- [x] Task 3 lint fixes — complete. Extracted `matchesWildcardTLSZone`, `validateGenerationInputs`, `applyPermutations`, `buildInfraGRPCCluster`, and `sdsTLSConfig`; listener uses `ListenConfig.Listen(ctx, ...)`; unused parameter and named return removed. Shared Envoy keys are constants. Fixed v2.13.2 zero-field checks, formatting, and test helper lint. Generated golden files are unchanged from the resumed Task 3 state.
- [ ] Commit Task 3 + push
- [ ] Live UAT (firewall-uat.md): user rebuilds CLI + restarts CP stack on HOST (new embedded clawkercp + Envoy 1.39.1); then in-container probes — #518: alternate two different-cert subdomains under one wildcard, zero 503; #500: curl 3-label host under wildcard rule verifies without -k, openssl s_client shows leaf SAN=exact SNI; check event=sds_secret_minted in CP logs (docker logs clawker-controlplane).

## Resume milestones (2026-09-07)
- Initial verification passed before code changes: `GOTOOLCHAIN=go1.26.6 go build ./...` and `GOTOOLCHAIN=go1.26.6 go test ./controlplane/... ./internal/...` (exit 0). This includes the last `sds_server.go` edits. All six embed binaries are present.
- Remaining work: commit hooks, Task 3 commit, push, and host-run UAT. The plugin known-issues file has no entry for #500 or #518.
- Use the existing branch trailers for Task 3: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_01HtEbkKPQLaqvnfgAbJUnib`.

- Final code checks passed: `GOTOOLCHAIN=go1.26.6 go build ./...`, `go test ./controlplane/... ./internal/...`, and `golangci-lint run --config .golangci.yml ./...` with the same toolchain (0 issues). Lint uses `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0=/Users/andrew/Code/clawker` so its existing merge-base filter can read this mounted repository. `.golangci.yml` is unchanged. Golden SHA256 checks passed.
- README, design, architecture, package references, and Mintlify firewall docs describe the final certificate behavior and QUIC limit. The user requested UAT commands in the final reply only; no separate UAT document. Live UAT remains pending.

- Commit hooks: Go module tidy, Gitleaks, full lint, govulncheck, `make test`, generated docs, and the documentation advisory passed. Semgrep flagged the new test's in-memory `bufconn` gRPC server because it has no TLS. The listener has no network socket; add the established test-only suppression with that reason, then rerun the hook. Task 3 is not yet committed.

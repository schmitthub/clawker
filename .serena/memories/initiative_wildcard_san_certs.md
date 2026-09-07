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
- [x] Task 3 (#500 SDS on-demand mint) — COMMITTED as `8fabc880`: GenerateSNICert + validateSNIHostname (certs.go); SDSServer delta-xDS (sds_server.go — fail-closed removed_resources, longest-zone admit, CA-serial cache); downstreamOnDemandMITMSocket selector variant on wildcard chains (envoy_tls.go); sds_cluster STRICT_DNS→clawker-controlplane:7445 mTLS via /etc/envoy/otel-tls (envoy_config.go); SDSConfig gated on infraCertsReady (stack.go sdsConfig); startSDSServer listener in internal/controlplane/cmd.go (CP server leaf + infra-intermediate ClientCAs, degrade event=sds_unavailable, runs pre-firewall-gate); ControlPlaneSettings.SDSPort default 7445 (+consts.DefaultCPSDSPort, parity test); comprehensive_mtls golden carries selector (2 wildcard chains) + sds_cluster; validateBootstrap blank imports added. QUIC: selector REJECTED by Envoy for QUIC (verified v1.39.1 config_test.cc) — QUIC keeps static certs, h3 multi-label falls back to TCP.
- [x] golangci v2.12.2→v2.13.2 (prek rev + CI lint.yml) — staticcheck vs Go 1.27 stdlib buildir panic, commit 58186513
- [x] gen-docs regenerated (configuration.mdx + settings.schema.json in diff)
- [x] Task 3 lint fixes — complete. Extracted `matchesWildcardTLSZone`, `validateGenerationInputs`, `applyPermutations`, `buildInfraGRPCCluster`, and `sdsTLSConfig`; listener uses `ListenConfig.Listen(ctx, ...)`; unused parameter and named return removed. Shared Envoy keys are constants. Fixed v2.13.2 zero-field checks, formatting, and test helper lint. Generated golden files are unchanged from the resumed Task 3 state.
- [x] Commit Task 3 — `8fabc880`, with the branch's existing co-author and session trailers.
- [x] Push Task 3 — `8fabc880` and Envoy image commit `026f7283` are on `origin/fix/wildcard-san-certs`.
- [ ] Live UAT (firewall-uat.md): user rebuilds CLI + restarts CP stack on HOST (new embedded clawkercp + Envoy 1.39.1); then in-container probes — #518: alternate two different-cert subdomains under one wildcard, zero 503; #500: curl 3-label host under wildcard rule verifies without -k, openssl s_client shows leaf SAN=exact SNI; check event=sds_secret_minted in CP logs (docker logs clawker-controlplane).

## Resume milestones (2026-09-07)
- Initial verification passed before code changes: `GOTOOLCHAIN=go1.26.6 go build ./...` and `GOTOOLCHAIN=go1.26.6 go test ./controlplane/... ./internal/...` (exit 0). This includes the last `sds_server.go` edits. All six embed binaries are present.
- Remaining work: host-run UAT. Code, checks, commit, and push are complete. The plugin known-issues file has no entry for #500 or #518.
- Use the existing branch trailers for Task 3: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_01HtEbkKPQLaqvnfgAbJUnib`.

- Final code checks passed: `GOTOOLCHAIN=go1.26.6 go build ./...`, `go test ./controlplane/... ./internal/...`, and `golangci-lint run --config .golangci.yml ./...` with the same toolchain (0 issues). Lint uses `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0=/Users/andrew/Code/clawker` so its existing merge-base filter can read this mounted repository. `.golangci.yml` is unchanged. Golden SHA256 checks passed.
- README, design, architecture, package references, and Mintlify firewall docs describe the final certificate behavior and QUIC limit. The user requested UAT commands in the final reply only; no separate UAT document. Live UAT remains pending.

- Initial commit hook run: all checks passed except Semgrep's TLS check on the test-only `bufconn` listener. The following milestone records the correction and successful retry.

- Task 3 committed as `8fabc880` (`fix(firewall): mint certificates per SNI`). Every applicable commit hook passed, including Semgrep after the test-only in-memory listener exception. `make test` passed. The separate UAT document was removed at the user's request and is not in the commit. No prohibited test command ran and `.golangci.yml` was not edited.

- Push succeeded: remote branch advanced from `81420fd65` to `8fabc880d`. UAT remains pending on the host: rebuild with `GOTOOLCHAIN=go1.26.6 make clawker`, use the rebuilt CLI to stop/start CP, confirm stack health and the new Envoy image, then run the existing firewall-UAT probes from an enforced agent. Require verified TLS for a deep hostname and zero 503 responses while alternating different-certificate hosts through one wildcard DFP cluster. Provide commands in the final reply only.

## Go 1.27.1 update (2026-09-07)
- User requested all active Go version pins move to 1.27.1. Updated root and adversarial module directives, all Go builder image pins, the project dev installer, the lint hook toolchain, and source-build requirements. CI reads `go.mod` and needs no separate version edit. Existing historical test fixtures are unchanged.
- Verified official stable release at `https://go.dev/dl/?mode=json`. Verified `golang:1.27.1-alpine` with `docker buildx imagetools inspect`: OCI image index `sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125`, includes linux/amd64 and linux/arm64. Root `go mod tidy -diff` passes on 1.27.1. Existing bundler golden test passed before the image update and fails only on the expected image ref change afterward; the five golden files now carry that exact image-ref change and await the verification rerun.
- The dev-installer verification added an unnecessary versioned wrapper. Its extra download hit DNS NXDOMAIN for HTTPS `dl.google.com:443/go/go1.27.1.linux-arm64.tar.gz`. The user directed attention to the existing installed toolchain; removed only that newly added wrapper and resumed with the already available Go 1.27.1. No firewall rule changed and no alternate download was attempted.
- Go 1.27.1 verification passed: root `go build ./...`; adversarial server build; root and nested `go mod tidy -diff`; `make test` (6,545 tests, 11 skipped, including refreshed bundler goldens); full golangci-lint (0 issues). `.golangci.yml` remains unchanged.

- Committed Go update as `423dddf1` (`build: use Go 1.27.1`). All applicable commit hooks passed. The first hook attempts used prek's cached Go 1.27.0 with `GOTOOLCHAIN=local`; pinned the lint hook's `language_version` to 1.27.1. `prek exec golangci-lint-full -- go version` confirms it uses the existing installed 1.27.1 compiler.

## Suno E2E regression (2026-09-07)
- User requested an E2E test with `.suno.com` in the test project config. Added `TestFirewall_WildcardSANCerts` in the existing firewall suite: all seven Suno hosts from #518, including `studio-api.prod.suno.com`; fresh CP with the test CA, then eight rounds in one agent and Envoy process; TLS verification enabled, no retries or redirects; HTTP 2xx-4xx plus the Envoy upstream-service-time header required. A local firewall 403 cannot pass.
- E2E compile-only check passed with Go 1.27.1: `go test -c -o /tmp/clawker-e2e-go1271.test ./test/e2e`. Full golangci-lint passed with the test included (0 issues). Live execution needs the host because this harness stops CP during cleanup. Remaining: commit and push the version update and test; host runtime acceptance remains pending.

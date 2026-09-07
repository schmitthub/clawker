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
- [ ] Task 2 (#518 max_session_keys) — IN PROGRESS
- [ ] Task 1 (Envoy bump; needs host-side digest if Hub blocked)
- [ ] Task 3 (#500 SDS on-demand mint)
- [ ] Docs + completion gate + UAT (per .claude/rules/firewall-uat.md)

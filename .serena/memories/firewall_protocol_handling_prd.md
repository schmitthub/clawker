# Firewall protocol handling — PRD / high-level concept (PRE-DESIGN)

**Status: PRD only. NOT a design, NOT approved.** This captures the problem and the high-level concept the user articulated. The concrete design does NOT exist yet — it kicks off in a dedicated design session when the branch is started. Treat everything below as requirements + guiding concept to feed that session, not as decided mechanism. Do not build from this. Separate future work from `fix/dns-subtree-exfil` (#320).

**Nature of the work: this is a CONFIG GENERATOR, not custom networking code.** Every "gate/check/block" described below is an **Envoy config construct the generator emits** (listener filters, filter chains, `filter_chain_match`, virtual hosts, routes, clusters, deny chains) which **Envoy** evaluates at runtime via its own `tls_inspector` → filter-chain-match → HCM/tcp_proxy pipeline. We write NO packet-inspection or runtime traffic-checking code. "Pipeline of gates" = the factory composing Envoy config blocks dynamically; the layered flow describes how the generated config is structured and how Envoy evaluates it — not a Go middleware chain processing packets. Likewise CoreDNS handling is Corefile generation. The deliverable is generated `envoy.yaml` (+ `Corefile`), nothing more.

## Problem (the why)

`internal/controlplane/firewall/envoy_config.go` (`GenerateEnvoyConfig`) is a flat `proto:` string switch that privileges `https` and bolts deny on as a `default:` warning. Observed pain:
- Non-https denies (`http`/`tcp`/`ssh`/anything) silently drop to a file-only `warnings[]` line the user never sees → **fail-open** when a broader wildcard allow covers them (e.g. `allow .github.com` + `deny some.github.com:22` is not enforced anywhere in Envoy).
- Layer conflation: `tcp` (L4) treated as a peer of `http`/`https` (L7); `ssh` (L7) lumped with raw TCP because clawker can't inspect it yet — baking a current limitation into the type system.
- The generator collapses on itself every time anything beyond https must be handled → recurring regressions, hacks, tech debt.

Goal: replace it with a proper, extensible foundation — an Envoy config factory that grows to handle new protocols additively without breaking each time.

## High-level concept (user's framing — to be designed, not yet designed)

A pipeline of independent, layered defensive gates that mirrors the protocol stack. The user's words: "tls code passes to tcp code passes to http/ssh/ftp; if the tls blocks aren't hit then it starts at tcp -> onward... a building block of defensive checks/gates independent of the previous... goes in either direction... if conditions only hit the tcp block with no forward match it hits a default exit, so every tcp gets at least some generic protection, and if it further matches a supported higher level the host info gets further scrutiny."

Concept points (requirements the design should satisfy, mechanism TBD):
- Gates mirror layers: TLS detection (L5/6, SNI) → TCP baseline (L4) → L7 app gates (HTTP/SSH/FTP/…). Aligns with Envoy's `tls_inspector` → filter-chain-match → HCM/tcp_proxy pipeline.
- TCP is the universal baseline: every connection gets at least generic protection, with a **fail-closed default exit** when nothing higher matches.
- Each gate is an independent building block (defense in depth — enforces on its own, does not lean on a prior gate or another layer).
- More in-band identity → more host scrutiny. Less → still gets the baseline floor.
- Exhaustive: no silent `default:`-to-warning drop. Deny is a first-class output for every proto.
- Per-layer trust anchor for upstream resolution, never `ORIGINAL_DST`/client IP: SNI (TLS, all wrapped L7) / allowlisted Host (plaintext HTTP) / routing-layer eBPF-derived domain (opaque, weaker → fail-closed).
- Layer-correct modeling: L4 TCP ≠ L5/6 TLS ≠ L7 app. SSH is its own L7 (currently opaque strategy, upgradeable in place), NOT the same class as raw TCP.
- Pluggable/extensible/decoupled: adding an L7 protocol should be additive. Option to explicitly disallow protocols too risky to support.

## Known constraints to carry into the design session

- Security-critical, behavior-preserving refactor — NOT greenfield. Must preserve: `sni-lock` trust boundary, wildcard DFP sub-clusters (`allow_coalesced_connections:false`), HCM hardening set (`httpConnectionManagerHardening`), longest-prefix path routing, filter-chain specificity ordering, catch-all-deny-last. Golden files guard behavior.
- Per `.claude/rules/envoy.md`: verify ALL Envoy behavior against official docs/examples before coding (vhost domain-match specificity, `tls_inspector` SNI on non-HTTP TLS, filter-chain match selection). No Envoy semantics from training data.
- Opaque (raw TCP / SSH) deny + destination integrity live in the routing layer — `internal/controlplane/firewall/ebpf` + `internal/dnsbpf` (`route_map`/`dns_cache`, `DomainHash`). Surface as the explicit "no in-band name → routing-layer-only, fail-closed" case. NOTE bug to fix: wildcard TCP normalizes `r.Dst` to the apex → over-broad/wrong.
- Concrete fail-open already identified for the eventual fix: un-gate the SNI-reset deny (`buildTLSDenyFilterChain`) from `https`-only to all SNI-bearing TLS rules; add a plaintext-HTTP more-specific deny vhost (exact host beats `*.X`) → 403.
- Design-doc first (adhere to `.claude/docs/DESIGN.md` / `ARCHITECTURE.md`, update them). Tests cover each layer/gate as a vacuum.

## Related auto-memories (user global memory)
- feedback: protocol-model-layered-not-inspection
- feedback: tls-not-https-full-proto-coverage
- feedback: defense-in-depth-no-vacuum-excuse

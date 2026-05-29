# Firewall protocol handling — PRD + design concepts (PRE-DESIGN)

**Status: PRD + evolving design concepts. NOT the concrete design, NOT approved, NOT buildable.** Captures the problem, the high-level concept, and the design *concepts/principles* converged on during the design session on branch `refactor/envoy-config-generation`. The concrete design (exact interfaces, Go code snippets, sample Envoy YAML, golden-diff plan) is the NEXT artifact and does not exist yet. Do not build from this. Separate future work from `fix/dns-subtree-exfil` (#320).

**Nature of the work: this is a CONFIG GENERATOR, not custom networking code.** Every "gate/check/block" described below is an **Envoy config construct the generator emits** (listener filters, filter chains, `filter_chain_match`, virtual hosts, routes, clusters, deny chains) which **Envoy** evaluates at runtime via its own `tls_inspector` → filter-chain-match → HCM/tcp_proxy pipeline. We write NO packet-inspection or runtime traffic-checking code. "Pipeline of gates" = the factory composing Envoy config blocks dynamically; the layered flow describes how the generated config is structured and how Envoy evaluates it — not a Go middleware chain processing packets. Likewise CoreDNS handling is Corefile generation. The deliverable is generated `envoy.yaml` (+ `Corefile`), nothing more.

**Output is stock vanilla Envoy.** A plain `envoy.yaml` any upstream Envoy binary loads. NO custom Envoy filters/plugins, forks, WASM, Lua, `ext_proc`, or sidecars. The Go-side abstraction (builders/registry/assembler) is codegen only — zero Envoy runtime footprint. A "builder/emitter" is a Go function returning config maps, NEVER an Envoy extension. The design adds zero Envoy capability; it only organizes *which stock blocks get emitted* per derived realization.

## Problem (the why)

`internal/controlplane/firewall/envoy_config.go` (`GenerateEnvoyConfig`) is a flat `proto:` string switch that privileges `https` and bolts deny on as a `default:` warning. Observed pain:
- Non-https denies (`http`/`tcp`/`ssh`/anything) silently drop to a file-only `warnings[]` line the user never sees → **fail-open** when a broader wildcard allow covers them (e.g. `allow .github.com` + `deny some.github.com:22` is not enforced anywhere in Envoy).
- Layer conflation: `tcp` (L4) treated as a peer of `http`/`https` (L7); `ssh` (L7) lumped with raw TCP because clawker can't inspect it yet — baking a current limitation into the type system.
- Param explosion: `buildListeners(tls, tcp, http, tlsDeny, tcpMappings, ports, tlsExactDomains, httpExactDomains, als)` and `buildEgressListener(...)` thread one positional arg per bucket — every new proto grows every signature.
- The generator collapses on itself every time anything beyond https must be handled → recurring regressions, hacks, tech debt.

Goal: replace it with a proper, extensible foundation — an Envoy config factory that grows to handle new protocols additively without breaking each time.

## High-level concept (user's framing)

A pipeline of independent, layered defensive gates that mirrors the protocol stack. The user's words: "tls code passes to tcp code passes to http/ssh/ftp; if the tls blocks aren't hit then it starts at tcp -> onward... a building block of defensive checks/gates independent of the previous... goes in either direction... if conditions only hit the tcp block with no forward match it hits a default exit, so every tcp gets at least some generic protection, and if it further matches a supported higher level the host info gets further scrutiny."

Concept points (requirements the design must satisfy):
- Gates mirror layers: TLS detection (L5/6, SNI) → TCP baseline (L4) → L7 app gates (HTTP/SSH/FTP/…). Aligns with Envoy's `tls_inspector` → filter-chain-match → HCM/tcp_proxy pipeline.
- TCP is the universal baseline: every connection gets at least generic protection, with a **fail-closed default exit** when nothing higher matches.
- Each gate is an independent building block (defense in depth — enforces on its own, does not lean on a prior gate or another layer).
- More in-band identity → more host scrutiny. Less → still gets the baseline floor.
- Exhaustive: no silent `default:`-to-warning drop. Deny is a first-class output for every proto.
- Per-layer trust anchor for upstream resolution, never `ORIGINAL_DST`/client IP: SNI (TLS, all wrapped L7) / allowlisted Host (plaintext HTTP) / routing-layer eBPF-derived domain (opaque, weaker → fail-closed).
- Layer-correct modeling: L4 TCP ≠ L5/6 TLS ≠ L7 app. SSH is its own L7 (currently opaque strategy, upgradeable in place), NOT the same class as raw TCP.
- Pluggable/extensible/decoupled: adding an L7 protocol should be additive. Option to explicitly disallow protocols too risky to support.

## Design concepts (evolving — NOT the concrete design)

### Core pattern
Replace the flat `proto:` switch with **Strategy + Registry, organized by network layer, feeding an Assembler** ("Replace Conditional Dispatch with Polymorphism + Handler Registry"). Battle-tested in this exact domain — Envoy and CoreDNS both register filters/plugins via named factory registries. Three SEPARATED roles:
- **Deriver** (pure data): `token → []Realization`. The ONLY place that knows the intent→stack mapping.
- **Layer builders** (one per `LayerKind`): each emits ONLY its layer's stock-Envoy fragment.
- **Assembler**: walks realizations, invokes builders in order, wires fragments into the Envoy tree, resolves dispatch, enforces global invariants + the fail-closed floor.

### Token = application INTENT, not transport
A proto token declares the *app protocol* the user wants secured. Transport(s) beneath are **derived** and may be plural. Derivation maps intent → a SET of realizations (concrete stacks), not one stack:
- `https` → `[{tcp,tls,http}, {udp,quic,http3}]` — one rule, both wires; h3 negotiated below user intent (Alt-Svc / HTTPS-RR).
- `http`  → `[{tcp,http}]` — no cleartext h3.
- `ssh`   → `[{tcp,ssh}]` — SSH genuinely TCP-only.
- `tcp` / `udp` → transport-only escape hatch (`[{tcp}]` / `[{udp}]`), no app gate.

Realization-set size reflects protocol reality (singleton when single-transport). Pure declarative function; codomain is a set, still single-valued.

**Contract: secure the declared protocol across every legitimate realization; never force a transport the user didn't ask for; never diminish the protocol's real features (paths, websockets, h3); no convoluted/hackish routing.** Extensibility payoff: when the QUIC builder lands, every existing `https` rule covers h3 automatically — no token rename, no config migration.

REJECTED idea (was wrong): "curate single-transport tokens / make h3 its own token." Forces transport onto the user and breaks the intent/transport decoupling — users would re-author config every time clawker gains a transport. Note real protocols DO span transports (DNS UDP+TCP, HTTP TCP+QUIC) — proof plural realizations are required. DNS is not a user egress token anyway (eBPF-redirected to CoreDNS; `EgressRule` proto vocab = `tls|tcp|http|ssh|ip|cidr`).

### Total decoupling — each layer independent and additive
- TCP floor (and UDP floor) work for ANY protocol now or future — they contain ZERO references to http/ssh/anything above. Adding FTP/SMTP later never touches the floor.
- TLS doesn't care which floor (TCP vs UDP) carried it. HTTP doesn't care whether TLS ran or it came raw off TCP. Each layer's generation is additive and decoupled "to the security depth."
- **HTTP is identical regardless of TLS — VERIFIED in current code:** both `buildHTTPFilterChain` (plaintext, `:763`) and `buildTLSFilterChain` (https, `:841`) call the same `buildHTTPRoutes` (`:1066`). Path rules / longest-prefix sort / `path_default` / 403 direct-response / action-metadata are transport-independent. Plaintext HTTP DOES get path rules. The "no path rules → allow-all route" literal is copy-pasted in both builders today — a single decoupled HTTP builder removes that duplication.

### Layer ⊥ neighbors — but typed neighbor-context allowed
"Blind to neighbors" refined: a layer builder MAY take typed context (`LayerCtx{Transport, Below, Above}`) when an unavoidable constraint requires it — but variation is resolved INSIDE the single builder (switch / lookup table / functional-options / closures, composed from REUSABLE units), NEVER by multiplying named functions.
- **HARD RULE: one builder per `LayerKind`, forever.** Registry keyed by `LayerKind` → combinatorial names like `GenTlsConfigsTcpPrevHttpNext` are structurally impossible to register.
- Surface = O(layers). Grows by exactly one ONLY when a genuinely new layer kind appears; a transport×app combo reusing existing layers adds ZERO builders.
- Go tools for the nuance: first-class funcs / closures / HOFs, the **functional-options pattern** (idiomatic, reusable across cases), generics, `switch`/type-switch (Go has no pattern matching).

### The one unavoidable coupling — named + contained
QUIC integrates TLS 1.3 into the transport — cannot emit a separate TLS `transport_socket` over a QUIC floor. So the `{udp,quic,http3}` realization folds TLS *emission* into the QUIC floor builder, keyed on `ctx.Transport`. The TLS *contract* (expose SNI + decrypted stream to HTTP) is unchanged → HTTP above still doesn't care. This is the "within legitimate concrete unavoidable constraints" carve-out — encapsulated in one builder, never leaked to a neighbor.

### Fail-closed floor — assembler-owned, transport-generic
Catch-all deny emitted by the assembler PER TRANSPORT, not by any rule or L7 builder. Every TCP listener ends catch-all-deny-last; every UDP listener gets its own when UDP lands. The floor cannot enumerate what it denies-by-exclusion — that is what makes it total. No silent `default:`→warning drop.

### Dispatch is a resolved concern, not a layer's job
`filter_chain_match` built by the assembler from the most-specific identity a layer exposes: SNI (if TLS present) else `transport_protocol`/dst-port else catch-all. Host-header routing stays INSIDE the HCM (within-chain) → the HTTP builder never builds a chain-match. Chain topology differs by exposed identity (plaintext = 1 chain / N vhosts / Host-routed; TLS = N chains / 1 vhost each / SNI-routed) — a dispatch decision, NOT HTTP semantics.

### Layer → stock Envoy construct map
- TCP/UDP floor → `Listener` (tcp/udp socket) + `tcp_proxy`/`udp_proxy` + catch-all `filter_chain`
- fail-closed deny → empty-match `filter_chain` + RST, or HCM `direct_response: 403`
- TLS → `tls_inspector` listener filter + `DownstreamTlsContext` transport_socket + upstream `UpstreamTlsContext`
- dispatch → `filter_chain_match` (`server_names` / `transport_protocol` / dst port)
- HTTP → `http_connection_manager` + `virtual_hosts` + `routes` + `direct_response`
- upstream resolve → `dynamic_forward_proxy` + `set_filter_state` (the existing sni-lock composition — all stock HTTP filters)
- SSH / opaque → `tcp_proxy`

### Sketch of the Go shape (illustrative, NOT final)
```go
type Transport int   // TCP | UDP
type LayerKind  int   // FloorTCP | FloorUDP | TLS | HTTP | SSH | …
type Realization struct { Transport Transport; Layers []LayerKind } // floor→app
func realizations(token string) []Realization                       // Deriver, pure table
type LayerCtx struct { Transport Transport; Below, Above LayerKind }
var builders = map[LayerKind]LayerBuilder{ /* one slot per layer, keyed by kind */ }
```

## #313 fit — each gap = builder + table row (do NOT need to support all now; need the seam)
- `proto: udp` + UDP listener (gaps 1,2,8): UDPFloor builder + `udp` token. TCP floor/TLS/HTTP untouched.
- Per-rule port ranges (gap 5, FTP/WebRTC): `EgressRule` schema field; port-binding builders read it.
- SNI-routed TCP, SMTPS/IMAPS (gaps 6,7): realization `{tcp,tls,<opaque>}` — reuse TLS builder (already exposes SNI), terminal = `tcp_proxy` not HCM.
- QUIC/HTTP3 (gaps 2,9): implement `{udp,quic,http3}` realization, add to `https` set → existing https rules cover h3. Biggest lift; deferred.
- Explicitly disallow a proto (PRD): token → empty/deny-flagged realization → floor catch-all; optional per-SNI deny chain. Deny is first-class.

## Known constraints to carry into the concrete-design step

- Security-critical, behavior-preserving refactor — NOT greenfield. Must preserve: `sni-lock` trust boundary, wildcard DFP sub-clusters (`allow_coalesced_connections:false`), HCM hardening set (`httpConnectionManagerHardening`), longest-prefix path routing, filter-chain specificity ordering, catch-all-deny-last. Golden files guard behavior.
- Per `.claude/rules/envoy.md`: verify ALL Envoy behavior against official docs/examples before coding (vhost domain-match specificity, `tls_inspector` SNI on non-HTTP TLS, filter-chain match selection, SNI-routed `tcp_proxy` via `filter_chain_match.server_names` — #313 flags training data weak here, QUIC build availability). No Envoy semantics from training data.
- QUIC = upstream Envoy build flag — gated on the pinned Envoy image actually having QUIC; deferred until then. Not a fork. Until then `https` emits only its `{tcp,tls,http}` realization.
- Opaque (raw TCP / SSH) deny + destination integrity live in the routing layer — `internal/controlplane/firewall/ebpf` + `internal/dnsbpf` (`route_map`/`dns_cache`, `DomainHash`). Surface as the explicit "no in-band name → routing-layer-only, fail-closed" case. NOTE bug to fix: wildcard TCP normalizes `r.Dst` to the apex → over-broad/wrong.
- DELIBERATE behavior changes (golden-file UPDATES, not preservation): un-gate the SNI-reset deny (`buildTLSDenyFilterChain`) from `https`-only → all SNI-bearing TLS rules; add a plaintext-HTTP more-specific deny vhost (exact host beats `*.X`) → 403.
- The concrete design is its OWN artifact (interfaces + code snippets + sample YAML + golden-diff plan), produced before any implementation. NOT `.claude/docs/DESIGN.md` — that doc is high-level clawker-overall and is only updated when work ships. Tests cover each layer/builder as a vacuum.

## Related auto-memories (user global memory)
- feedback: protocol-model-layered-not-inspection
- feedback: tls-not-https-full-proto-coverage
- feedback: defense-in-depth-no-vacuum-excuse

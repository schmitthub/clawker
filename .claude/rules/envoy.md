---
description: Envoy egress config generation — transport-first, proto-token building blocks (NOT an HTTP/TLS proxy)
paths: ["internal/controlplane/firewall/envoy_*.go"]
---

# Envoy Egress Generation Rules

<critical>
## Envoy is NOT an HTTP proxy. It is NOT a TLS proxy. Stop building it that way.

From Envoy's own architecture docs (https://www.envoyproxy.io/docs/envoy/latest/intro/what_is_envoy and intro/arch_overview/intro/threading_model + intro/arch_overview/listeners/network_filters):

> "At its core, Envoy is an **L3/L4 network proxy**. A pluggable filter chain mechanism allows filters to be written to perform different TCP/UDP proxy tasks... **HTTP is such a critical component of modern application architectures that Envoy supports an additional HTTP L7 filter layer. The HTTP connection manager is itself a network filter.**"

Internalize that last sentence. **HTTP (the HCM) is ONE L3/L4 network filter among many.** TLS is a *transport socket* decoration. SNI is a *match* condition. These are leaves, not the trunk. The trunk is: a **listener** (TCP or UDP) → a **filter chain** selected by a **match** → **network filters** → an **upstream cluster**. Everything clawker does is an arrangement of those generic primitives.

When you catch yourself reaching for "the TLS chain" or "the HTTP path" as the organizing idea, STOP — you have the bias this entire document exists to kill. The organizing idea is the **L4 transport** (TCP/UDP); crypto (TLS/QUIC/mTLS/SNI) and L7 (HTTP/WS/...) are optional decorations layered on top.

clawker's egress firewall must support **every permutation of the network stack**, not just https:
- **L4:** raw TCP, raw UDP.
- **Crypto/L5-6:** plaintext, TLS-terminate (MITM), QUIC-terminate (MITM), SNI gating, mTLS.
- **L7 / app protocols:** HTTP, HTTPS, WS, WSS, HTTP/3, SSH, FTP, and any future protocol — including opaque (no L7 inspection) flows.

If a change only handles HTTP and HTTPS, it is incomplete by definition.
</critical>

<critical>
## Verify against source. Never guess Envoy behavior.

LLM training data on Envoy is weak and frequently wrong (this session alone: `require_sni` is unimplemented; `server_names` wildcards are asterisk-form not leading-dot; `auto_host_sni` doesn't work for dynamic-forward-proxy hosts — all contradicted "common knowledge"). The firewall is security-critical; a wrong config silently breaks egress enforcement.

**Every claim about Envoy behavior MUST cite a fetched source before you write code:**
1. Official example: `gh api "repos/envoyproxy/examples/contents/<path>" --jq '.content' | base64 -d`
2. Proto / docs: `WebFetch` the api-v3 proto or arch_overview page (use `raw.githubusercontent.com/envoyproxy/envoy/main/api/...` for exact proto field names + `[#not-implemented-hide:]` markers).
3. Reference impls (Contour, Istio) for how production control planes assemble the same blocks.
4. `envoyproxy/envoy` issues for known limits.

Do not name a field, default, or behavior from memory. If it isn't in fetched context, it isn't established.
</critical>

## The proto "token" model (the core abstraction)

An egress rule is `{host, port, paths, proto}`. **`proto` is a TOKEN: an abstract, polymorphic, orthogonal statement of user intent — NOT a config shape.** It names *what the user wants to reach*, and the generator derives the *full network-stack permutation* of Envoy config for that `host:port:proto(:paths)`.

| token | user intent | network stack the generator must derive |
|-------|-------------|------------------------------------------|
| `tcp` | raw TCP to host:port | TCP listener → opaque `tcp_proxy` → pinned cluster (no crypto, no L7) |
| `ssh` | SSH over TCP | TCP listener → opaque `tcp_proxy` → pinned cluster (L7=ssh is opaque to us; gate = pin) |
| `ftp` | FTP over TCP | TCP listener → opaque `tcp_proxy` (+ control/data port handling) → pinned cluster |
| `http` | cleartext HTTP | TCP listener (raw_buffer) → HCM (L7) → plaintext upstream |
| `https` | HTTP over TLS | TCP listener (TLS-terminate/MITM, SNI gate) → HCM → **reencrypt** upstream **+** sibling UDP/QUIC (h3) listener → HCM → reencrypt upstream |
| `ws` | WebSocket over HTTP | `http` + per-route `upgrade_configs` |
| `wss` | WebSocket over TLS | `https` + per-route `upgrade_configs` (+ upstream h1.1 pin) |
| `udp` | raw UDP datagrams | UDP listener → `udp_proxy` → pinned cluster (no crypto, no L7) |

This table is illustrative, not exhaustive — new tokens (gRPC, QUIC-raw, DNS, SMTP, ...) slot in by composing the same blocks. **A token is never special-cased with a bespoke code path.** It selects an ordered list of building blocks.

## Architecture: orthogonal building blocks, one forward pass

The generator (`envoy_*.go` in the firewall package, spec in `ENVOY_TARGET.md`) is a **protocol-agnostic orchestrator + a deriver + self-contained layer blocks**:

- **Orchestrator** (`GenerateEnvoyConfig`): dumb generic loop. For each permutation it chains an ordered list of `layer` methods through one mutable `genCtx`, then commits into the `EnvoyConfig` accumulator (upsert by key, fail-closed). It never names a protocol.
- **Deriver** (`layersFor(rule, genFacts)`): the ONLY proto-aware step. Maps a token (+ wildcard-ness, port, paths) → the ordered `[]layer` list(s). One token may yield multiple permutations (e.g. `https` → a TCP chain AND a QUIC chain).
- **Layer blocks** — three ORTHOGONAL kinds, each a `func(*genCtx) error` that reads/writes only `genCtx`, never the token:
  1. **Transport block** — binds the listener and decides L4 + downstream crypto. *This is the trunk.* Organized by L4: TCP transports (raw_buffer cleartext; TLS-terminate MITM) live with TCP; UDP transports (QUIC-terminate MITM; raw `udp_proxy`) live with UDP. A transport block is self-deciding; the deriver picks exactly one per permutation. **QUIC is a UDP transport — it never belongs in a "tls" file.**
  2. **Upstream block** — the cluster: "where do these bytes go, how is the host resolved." **Generic and L4-agnostic.** A cluster is a cluster (LOGICAL_DNS pin, dynamic_forward_proxy, ORIGINAL_DST, STATIC) with optional decorations (reencrypt `UpstreamTlsContext`, `HttpProtocolOptions`). ssh/tcp/udp pinned clusters are peers of the http/https ones — do NOT frame the upstream layer as "HTTP upstreams."
  3. **App block** — L7 inspection, i.e. the HCM. ONLY for HTTP-family tokens (http/https/ws/wss/h3). Opaque tokens (tcp/ssh/ftp/raw-udp) have **no app block**. The same HCM app block is reused verbatim across http/https/ws/wss/h3 — it inspects cleartext regardless of whether bytes arrived plaintext, TLS-decrypted, or QUIC-decrypted.

**Decoupling is mandatory.** Transport / upstream / app are independent. The app block must not know how bytes were decrypted; the upstream block must not know the L7; the transport block must not know the cluster. A block that reaches across this boundary (e.g. an L7 HTTP filter living in the upstream/cluster file, or QUIC config in a TLS file) is a bug to fix, not a pattern to extend.

**Single forward pass.** `genCtx` is threaded mutably transport → upstream → app. A block decides its facts BEFORE the next runs; nothing patches a committed block retroactively. Generation-wide facts that no single permutation can decide in isolation are computed once up front in the deriver (`genFacts`).

## Redundancy is REQUIRED — Envoy is its own island

Defense in depth here is NOT "layers that together cover the threat." It is **mandatory redundancy: every hop independently re-checks everything, as if it were the only defense.** No component of the firewall stack is load-bearing for the group. A regression, bug, missing feature, or outage in any one layer must change the firewall's security posture by ZERO.

**Envoy generates as if eBPF and CoreDNS do not exist.** They can fail, regress, be misconfigured, or be bypassed (a hardcoded IP skips CoreDNS; an eBPF gap skips the redirect). So Envoy's config must, entirely on its own:
- gate the host (SNI `server_names` / Host vhost + deny default) — even though CoreDNS also NXDOMAINs disallowed names;
- resolve the upstream IP itself, never the client's (see confused-deputy gotcha) — even though eBPF also redirects;
- pin or deny every flow — even though eBPF also filters at the cgroup.

Never reason "eBPF will catch it" / "CoreDNS NXDOMAINs it, so the vhost can fail open" / "eBPF doesn't redirect UDP anyway, so the UDP listener doesn't matter." Every one of those makes a sibling layer load-bearing — forbidden.

**This extends to supported CAPABILITIES, not just runtime liveness.** It does not matter whether eBPF (or anything else) currently supports UDP, a particular app protocol, IPv6, or any future stack feature. Envoy emits the COMPLETE, self-secure config for every permutation the egress rules express, regardless of what the rest of the stack can carry today. Envoy assumes nothing about, and depends on nothing from, eBPF or CoreDNS — it is in its own void. If a permutation is currently unreachable because another layer hasn't caught up, that is the other layer's gap to close; Envoy's atom is still correct and still self-secure. (This is also why "is eBPF UDP-ready?" is never a precondition for emitting a UDP/QUIC listener.)

**Redundancy applies INSIDE Envoy too.** Each listener, filter chain, and cluster runs its own checks; a later stage never trusts that an earlier one already validated. In the generated config:
- `udp_proxy`/`tcp_proxy` forwards ONLY to its pinned cluster — opaque flows are gated by the pin alone.
- a TLS/QUIC listener gates by per-SNI `server_names` + a deny `default_filter_chain`, AND each per-SNI chain carries only its own vhost so Host is re-gated at L7 (two independent checks, not one).
- a port-range pins per in-range port; never `ORIGINAL_DST`.
- the upstream cluster re-validates identity (`auto_sni` / `auto_san_validation` against the system CA) even though SNI already selected the chain.

See the `defense-in-depth-no-vacuum-excuse` and `transport-first-not-tls-centric` memories.

## Verified Envoy facts (this codebase relies on these — re-verify before changing)

- **`require_sni` is `[#not-implemented-hide:]`** in `tls.proto`. It does NOT gate SNI. A single multi-cert `DownstreamTlsContext` serves the **first cert** on SNI mismatch/absence (`full_scan_certs_on_sni_mismatch:false` default) and proceeds to L7 — i.e. no server-side gate. **The only server-side SNI gate is per-SNI `filter_chain_match.server_names` + `tls_inspector`**, with unmatched SNI falling to a deny `default_filter_chain` (`tcp_proxy` → zero-endpoint `deny_cluster` → reset).
- **`server_names` wildcards are asterisk-form `*.apex`** (NOT leading-dot `.apex`, which Envoy treats as an exact literal). Envoy stores `*.apex` as `.apex` and matches by stripping one label at a time, so `*.apex` covers the whole subtree (incl. multi-label). The bare apex needs its own entry. A wildcard rule and an exact rule for the same apex must not duplicate a `server_names` entry (Envoy rejects the whole config).
- **Per-SNI chain + own-vhost replaces the legacy `sni-lock` filter.** Each https/wss/h3 SNI gets its OWN filter chain carrying ONLY its own vhost + `deny_all`. SNI structurally pins the Host (a mismatched Host hits `deny_all` 403, never an upstream), so there is NO `set_filter_state`/sni-lock/`dynamic_host` L7 dance. If you find that legacy coupling, it is gone — do not reintroduce it.
- **Upstream identity:** `HttpProtocolOptions.upstream_http_protocol_options{auto_sni, auto_san_validation}` (SNI from `:authority`, SAN validated against it). `auto_host_sni` does NOT work for a `dynamic_forward_proxy` dynamic host (no static hostname) — use `auto_sni`. Reencrypt validates against the **system CA** (`/etc/ssl/certs/ca-certificates.crt`), never the MITM CA.
- **Dynamic forward proxy is Host-keyed** (the DFP LB derives host:port from `:authority`), system-resolver (CoreDNS) — no hardcoded resolver. Plaintext and reencrypt variants use distinct dns_caches so the secure-upstream default port (443 vs 80) is honored. Disable DFP per-vhost (`typed_per_filter_config`) on every non-Host-following vhost so it never pre-resolves a pinned/denied request.
- **h3 = QUIC over UDP**: a UDP listener (`udp_listener_config.quic_options`) + `QuicDownstreamTransport` (same per-SNI MITM certs) + HCM `codec_type: HTTP3`. The sibling TCP chain advertises it via an `alt-svc` response header on the **origin authority port** (what the client dials + eBPF redirects), not Envoy's listener port.
- **WS over h2 (RFC 8441 Extended CONNECT)** needs `allow_connect` on the relevant `http2_protocol_options`; WS over h1.1 uses RFC 6455 Upgrade. Verify the upstream codec before assuming.

## Gotchas — past regressions (do NOT reintroduce)

Real failures that have bitten this firewall. Each is a security AND correctness issue; the mitigations are load-bearing. Both are about **upstream IP resolution integrity** — they apply to every transport (TCP, UDP, QUIC), not just HTTP.

### 1. Same-SAN cert, different IPs → connection coalescing makes "first IP win"

When two+ allowed hosts present upstream certs with overlapping SANs (e.g. `api.anthropic.com` and `statsig.anthropic.com` both covered by a `*.anthropic.com` SAN) but resolve to DIFFERENT IPs, a multiplexed (h2/h3) upstream can **coalesce connections**: Envoy reuses the existing connection to the first-resolved host for a request whose `:authority` is a *different* same-SAN host, because the live connection's cert already covers that name (RFC 7540 §9.1.1 connection reuse). The first IP's pool "wins" — every other same-SAN host is silently dialed at the WRONG IP, so connections to those domains break, and traffic meant for host B egresses to host A's endpoint (a cross-host leak).

Mitigations, both load-bearing:
- **Per-(host,port) pool isolation.** Exact rules each get their OWN cluster (one connection pool per allowed host) so coalescing has nothing to reuse across hosts. Never collapse multiple allowed hosts into a single shared pool that can coalesce.
- **Disable cross-host coalescing on any shared cluster** (the dynamic-forward-proxy cluster, where multiple hosts legitimately share one cluster). Verify the exact upstream knob against the proto before relying on it (historically a "no coalesced connections" option) — do NOT assume the default is safe.

A single-host golden will not catch this — exercise any change with ≥2 allowed hosts that share a real SAN cert but resolve to different IPs.

### 2. Confused deputy — NEVER honor the client's desired destination IP

A compromised/injected agent picks BOTH the host AND, if allowed, the destination IP (via `/etc/hosts`, `curl --resolve`, a hardcoded IP, a crafted UDP dst). The host is gated (SNI/Host vhost + CoreDNS NXDOMAIN); the IP must be gated too — by **ignoring it entirely**.

**Any flow that passes host validation MUST have its upstream IP resolved by Envoy itself — never taken from the client's chosen destination.** Concretely: `LOGICAL_DNS` pinned to the rule's host (exact), or `dynamic_forward_proxy` resolving the validated `:authority` via CoreDNS (wildcard). **`ORIGINAL_DST` is FORBIDDEN for any host-validated flow** — it forwards to whatever IP the client's socket targeted, which lets a compromised agent resolve an allowed hostname to an attacker IP and have the firewall faithfully forward allowed-host traffic there. The client may choose the *host* (within the allow list); Envoy alone chooses the *IP*. This is why the port-range design rejected `ORIGINAL_DST` in favor of per-port pinned clusters, and it holds identically for TCP, UDP, and QUIC.

**Carve-out — `ORIGINAL_DST` is CORRECT for a range-validated (CIDR) flow.** "Host-validated" means the grant is a single host/FQDN, so honoring the arriving dst is delegating that single-host decision to the datapath (the deputy). A CIDR rule's grant is the whole range, enforced on the chain itself by `filter_chain_match.prefix_ranges` + `use_original_dst` — Envoy validates the recovered original dst against the range BEFORE the cluster sees it, so forwarding to the in-range dst is *honoring* the grant, not trusting the client. Hence: bare IP → `STATIC` pin (the address IS the resolution); FQDN → `LOGICAL_DNS`/DFP; **CIDR → `ORIGINAL_DST`/`CLUSTER_PROVIDED` scoped by the chain's `prefix_ranges`** (plaintext for http/ws, reencrypt for https/wss). UDP-CIDR has no filter chains to range-gate on, so it fails closed — the carve-out needs the chain-level `prefix_ranges` to hold.

### 3. CIDR-TLS range cert — invalid-by-design, and that is fine

A `https`/`wss` rule to a CIDR mints ONE MITM leaf whose iPAddress SAN is the network address — it cannot validate against any single in-range host. That is intentional and not a bug to "fix" by enumerating SANs or capping the range: **agent-side verification is not clawker's enforcement boundary** (egress gating + MITM inspection are — see the `agent-cert-trust-not-load-bearing` memory). The leaf still encrypts the downstream hop and lets Envoy MITM-inspect; a client connecting to a raw in-range IP sets its own no-verify, exactly as it must for any self-signed endpoint. The *upstream* hop is still secure-by-default: do NOT gate `insecure_skip_tls_verify` — when unset, Envoy `VERIFY_TRUST_CHAIN`s and refuses an untrusted in-range upstream (fail-closed); a non-fatal generation warning is the only UX nudge. `certBasename` folds the CIDR `/`→`_` so the cert is one flat file and the `DownstreamTlsContext` ref agrees.

## HCM hardening contract (when an HCM exists)

Every clawker HCM MUST carry the full `httpConnectionManagerHardening()` set (applied via `maps.Copy` so no site forgets one):
- `normalize_path: true` + `merge_slashes: true` + `path_with_escaped_slashes_action: UNESCAPE_AND_REDIRECT` — defeats URL-encoded traversal (`/allowed/%2e%2e/denied`) that would otherwise bypass path rules.
- `common_http_protocol_options.headers_with_underscores_action: REJECT_REQUEST` — RFC 9110 §5.4.5 header aliasing.
- `http2_protocol_options.max_concurrent_streams: 100` — h2 amplification cap.

Timeouts and `per_connection_buffer_limit_bytes` are deliberately UNSET: LLM API calls stream for minutes with multi-MB bodies; short caps rupture mid-stream. Envoy defaults are correct for this workload.

## Deny body — non-fingerprinting

Every `direct_response: 403` (path-rule deny, `deny_all` vhost) uses the generic `firewallBlockedBody` ("Forbidden\n") — never names clawker. An injected-prompt adversary must not distinguish a clawker block from a generic upstream Forbidden by body. The verdict travels on the `action` access-log field, not the body.

## Access-log schema

Records use OTel semantic conventions (`network.*`, `server.*`, `client.*`, `tls.*`) plus the clawker `action` verdict. Full field reference + sources live in code comments and `ENVOY_TARGET.md`. Non-obvious rules:
- `action` (`allowed`/`denied`) is the canonical verdict, stamped at generation (per-route `%METADATA(ROUTE:clawker:action)%` for HCMs; hardcoded for opaque `tcp_proxy`/`udp_proxy`). NEVER inferred from `response_code`/`response_flags` — a legitimate upstream 403 is still `action: allowed`.
- `network.transport` must reflect the ACTUAL L4 (`tcp`/`udp`/`quic`) — do not hardcode `tcp` once UDP/QUIC transports exist.
- Config vocabulary (`allow`/`deny`, present tense) and event vocabulary (`allowed`/`denied`, past tense) are independent by design — never propagate "consistency" between them.

## Testing — STRICT, non-negotiable

Three hard requirements. A test touching Envoy generation that violates ANY of them does not belong in this package — delete it.

1. **Real egress-rules YAML, parsed via `storage.NewFromString[EgressRulesFile]`.** Every case's input is a real egress-rules YAML document run through the production storage read engine, then `NormalizeAndDedup` → `GenerateEnvoyConfig`. NEVER hand-built `EgressRule`/`config` structs, NEVER mocks, NEVER calling generator internals directly. The test must exercise the exact parse + normalize path production uses.
2. **Compare the resulting Envoy CONFIG against a control.** Every case generates the COMPLETE config and compares it against a committed control — a golden file (preferred: byte-for-byte against `testdata/envoy/<case>.envoy.golden`) or explicit string matches on the rendered YAML. NEVER assert on intermediate Go structures or poke individual fields of the in-memory tree. Assert against the produced config artifact, nothing else.
3. **Test tables.** All cases live in ONE table-driven test (`cases := []struct{...}` + `t.Run`). Adding coverage = adding a row (a new rules YAML + its control), never a new bespoke test function.

Why: a whole-config control captures EVERYTHING — every chain, vhost, cluster, filter, listener, access-log field — so any regression surfaces as a diff and is caught inherently. Field-level structural assertions are redundant, brittle, and let drift slip through; they are banned. `validateBootstrap` inside `GenerateEnvoyConfig` is the structural backstop; the control is the behavioral contract. Re-bless goldens with `GOLDEN_UPDATE=1`, then read the diff against `ENVOY_TARGET.md` before committing.

## See also

- `ENVOY_TARGET.md` (firewall package) — the concrete final-state config spec for every token, the source of truth the generator must reproduce.
- `internal/controlplane/firewall/CLAUDE.md` — firewall domain (Stack, handler, rules store).
- Memories: `transport-first-not-tls-centric`, `defense-in-depth-no-vacuum-excuse`, `dont-fabricate-patterns`.

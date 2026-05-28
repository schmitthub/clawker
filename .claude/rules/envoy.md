---
description: Envoy proxy configuration guidelines for the firewall egress stack
paths: ["internal/controlplane/firewall/envoy_config.go", "internal/controlplane/firewall/envoy_config_test.go", "internal/controlplane/firewall/stack.go", "internal/controlplane/firewall/handler.go"]
---

# Envoy Configuration Rules

<critical>
The firewall is a security-critical system. LLM training data on Envoy proxy is extremely weak and unreliable. Regressions are unacceptable. Sacrificing existing functionality for unverified assumptions is unacceptable. 

**Every assumption about Envoy behavior MUST be verified against official documentation and reference implementations before writing or modifying code.** Do not rely on training data. Do not guess. Do not claim behavior without citing a doc page or example config.

Verification workflow:
1. Fetch the relevant official example from `envoyproxy/examples` via `gh api`
2. Read the relevant Envoy documentation page via `WebFetch`
3. Search `envoyproxy/envoy` GitHub issues for known bugs or limitations
4. Only then propose a change — citing which doc/example/issue supports it

Incorrect changes can break egress enforcement for all users and create security vulnerabilities.
</critical>

## Required References

Before ANY Envoy config change, consult:

### Official Examples
Fetch via: `gh api "repos/envoyproxy/examples/contents/<path>" --jq '.content' | base64 -d`
- `websocket/envoy-ws.yaml` — WS proxy (no TLS)
- `websocket/envoy-wss.yaml` — WSS proxy (TLS termination + upstream TLS)
- `websocket/envoy-wss-passthrough.yaml` — WSS passthrough (tcp_proxy, no termination)
- `websocket/envoy-ws-route.yaml` — per-route WS enable/disable
- `tls-inspector/envoy.yaml` — tls_inspector with filter chain matching
- Sandbox index: https://www.envoyproxy.io/docs/envoy/latest/start/sandboxes/
- Repo source: https://github.com/envoyproxy/examples

### Envoy Documentation
- Upgrade/WebSocket: https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/http/upgrades
- HCM proto: https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/filters/network/http_connection_manager/v3/http_connection_manager.proto
- Security quickstart: https://www.envoyproxy.io/docs/envoy/latest/start/quick-start/securing

### Go Integration
- Go filter: https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/golang_filter#config-http-filters-golang
- Go Extension API: https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/filters/http/golang/v3alpha/golang.proto#envoy-v3-api-file-contrib-envoy-extensions-filters-http-golang-v3alpha-golang-proto
- Go Plugin API: https://github.com/envoyproxy/envoy/blob/6f94ab127f45cf93a29da0a740c7e84d466d14fb/contrib/golang/common/go/api/filter.go
- Go control plane: https://github.com/envoyproxy/go-control-plane

## Envoy Architecture in Clawker

### Egress Listener (single port, tls_inspector)

```
Client → eBPF connect4 rewrite → Envoy :10000
                            ├─ TLS exact (SNI match)   → per-domain filter chain → LOGICAL_DNS cluster (apex-pinned)
                            ├─ TLS wildcard (suffix)   → per-apex filter chain   → DFP cluster (sub_clusters_config, SNI-keyed)
                            ├─ HTTP (raw_buffer)       → Host header routing     → per-domain LOGICAL_DNS cluster (plaintext)
                            └─ Unknown                 → deny chain (tcp_proxy → deny_cluster → reset)
```

- `tls_inspector` listener filter detects TLS vs plaintext
- TLS: `filter_chain_match.server_names` (SNI) routes to per-domain chains. Exact rules match an exact SNI; wildcard rules (`.example.com`) match by SNI suffix
- HTTP: `filter_chain_match.transport_protocol: "raw_buffer"` catches plaintext
- Deny: empty `filter_chain_match: {}` catches everything else

### TLS Cluster Shapes (Security-Critical Design)

Two cluster shapes back the TLS chains. Both lock the upstream identity to the downstream SNI so the HTTP Host header can never influence which IP, cert, or SAN the request validates against.

| | Exact rule (e.g. `api.anthropic.com`) | Wildcard rule (e.g. `.mintlify.com`) |
|---|---|---|
| Cluster | `tls_exact_<domain>_<port>` LOGICAL_DNS, endpoint pinned to the apex hostname | `tls_wildcard_<apex>_<port>` `envoy.clusters.dynamic_forward_proxy` with `sub_clusters_config` (per `host:port` sub-cluster + pool) |
| TCP destination | Cluster endpoint (host header has no say) | Sub-cluster derived from the `envoy.upstream.dynamic_host` filter state (written from SNI by the chain's set_filter_state filter) |
| Pool isolation | One pool per cluster (one cluster per allowed domain) → no cross-cluster coalescing | One pool per sub-cluster, `allow_coalesced_connections: false` explicitly → no cross-pool reuse even when sibling subdomains share a SAN cert |

Both cluster shapes share an identical upstream TLS posture: `auto_sni`, `auto_san_validation`, `ecdh_curves: [X25519, P-256, P-384]`, trusted CA at `/etc/ssl/certs/ca-certificates.crt`, `auto_config` advertising HTTP/2 + HTTP/1.1 ALPN, `dns_lookup_family: V4_ONLY`.

A wildcard rule and an exact rule for the same apex produce two distinct clusters keyed by `(kind, domain, port)`. Their connection pools never share.

The plaintext-HTTP listener still uses `http_<domain>_<port>` LOGICAL_DNS (no TLS) and the deny chain uses `deny_cluster` STATIC.

### Filter Chain Matching (critical ordering)

Envoy evaluates filter chains IN ORDER and stops at the first match. Multiple filter chains with the same `server_names` means only the first is reachable. Same-domain TLS rules with different path_rules MUST be merged into a single filter chain (`addRulesToStore` / `MergeRule` enforces this at the rules-store layer: same `dst:proto:port` key collapses into one entry whose `path_rules` are unioned).

### http_filters — SNI Lock (Security-Critical)

The TLS chain http_filters list looks like this:

| Chain | http_filters |
|---|---|
| TLS exact | `[set_filter_state(sni-lock), router]` |
| TLS wildcard | `[set_filter_state(sni-lock), set_filter_state(dynamic_host), dynamic_forward_proxy, router]` |
| Plaintext HTTP | `[router]` |

**The sni-lock filter is the trust boundary** on every TLS chain. It pre-populates two upstream-targeting filter-state keys from `%REQUESTED_SERVER_NAME%` (the downstream SNI that selected the chain):

- `envoy.network.upstream_server_name` — upstream TLS SNI
- `envoy.network.upstream_subject_alt_names` — expected upstream cert SAN

`Router::Filter::decodeHeaders` only writes these under `auto_sni` / `auto_san_validation` when filter state does not already carry them, so the sni-lock value wins. Without the sni-lock filter the router derives both from the `:authority` header — an attacker with code-exec in the agent container could craft a request with allowed SNI and a different Host, causing the upstream TLS handshake to validate against the Host name. Exact-rule attacks would not cross IPs (cluster endpoint is pinned) but can still reach a non-allowed sibling at the app layer when the destination edge serves a cert covering Host's name. Wildcard-rule attacks cross IPs as well (TCP target follows the `dynamic_host` filter state).

Wildcard chains additionally write `envoy.upstream.dynamic_host` from SNI; the DFP HTTP filter consumes it (`allow_dynamic_host_from_filter_state: true`) so sub-cluster resolution sees the per-subdomain SNI rather than the apex.

Order matters: every set_filter_state writer must run before any filter that reads the corresponding filter-state key. dynamic_host writer must precede DFP; sni-lock must precede the router (the router consults filter state during its own auto_sni/auto_san_validation defaulting).

## WebSocket Proxying

### How Envoy Proxies WebSocket

1. Client sends HTTP/1.1 Upgrade request with `Sec-WebSocket-Key`
2. Envoy's HCM processes through `upgrade_configs` filter chain
3. Envoy establishes independent upstream connection with its OWN `Sec-WebSocket-Key`
4. Upstream responds with `Sec-WebSocket-Accept` (computed from Envoy's key)
5. Envoy responds to client with `Sec-WebSocket-Accept` (computed from CLIENT's key)
6. Both sides bridge as raw TCP after upgrade

### upgrade_configs Behavior

From Envoy docs: "If no filters are present, the filter chain for HTTP connections will be used for this upgrade type."

When custom filters ARE specified, they **completely replace** the HCM's `http_filters` — no merging.

### TLS WebSocket: ALPN Override

TLS clusters use `auto_config` with both `http_protocol_options` and `http2_protocol_options`. When the upstream supports h2, ALPN negotiates HTTP/2. **Envoy's `upgrade_configs` path uses the RFC 6455 HTTP/1.1 Upgrade mechanism, which does not exist in HTTP/2** (RFC 8441 extended-CONNECT WebSockets are a separate code path Envoy does not take here). When upstream ALPN lands on h2, the upgrade fails.

Fix: TLS filter chains use custom `upgrade_configs` filters: `set_filter_state` overrides `envoy.network.application_protocols` to `"http/1.1"`, forcing HTTP/1.1 on the upstream TLS handshake for WebSocket connections only.

### TLS WebSocket: SNI Lock Carries Over

`upgrade_configs.filters` **replaces** the HCM `http_filters` wholesale for upgrade requests — no merging. The sni-lock filter therefore must also be present on every TLS upgrade chain, or the trust boundary documented above evaporates on WebSocket connections. The actual upgrade filter lists:

| Chain | upgrade_configs filters |
|---|---|
| TLS exact | `[sni-lock, ALPN override, router]` |
| TLS wildcard | `[sni-lock, dynamic_host writer, ALPN override, dynamic_forward_proxy, router]` |
| Plaintext HTTP | (none — `upgrade_type: websocket` only; default http_filters reused) |

### HTTP WebSocket: No Custom Filters Needed

HTTP (plaintext) filter chains use simple `upgrade_configs: [{upgrade_type: websocket}]`. No ALPN override needed because there's no TLS negotiation; no sni-lock needed because there's no upstream TLS handshake to confuse. The default HCM http_filters (router only) are used for WebSocket traffic.

### HTTP/2 Downstream (Client → Envoy)

Our TLS listener advertises `alpn_protocols: ["h2", "http/1.1"]` downstream. Clients that negotiate h2 (like Node.js `ws` library, wscat) cannot use the HTTP/1.1 Upgrade mechanism over h2. Real browsers use HTTP/1.1 for WebSocket natively. Programmatic clients may need to force HTTP/1.1 ALPN.

This is a known Envoy limitation, not something we can fix without removing h2 from the downstream ALPN (which would break HTTP/2 for all regular traffic).

## Rule Schema

Egress rule types live in `internal/config/schema.go` (not in the firewall package) because they're part of the persisted project config schema:

- `EgressRule` — single egress rule (dst, proto, port, action, path_rules, path_default)
- `PathRule` — HTTP path-level filtering (path prefix + action)
- `FirewallConfig` — per-project firewall config (add_domains shorthand + full rules)

The firewall package imports these types from config; config does NOT import firewall.

### `proto:` values

Routing key for filter-chain shape selection. `NormalizeRule` (`internal/controlplane/firewall/rules_store.go`) silently translates legacy `proto: tls` → `proto: https` for backwards compatibility.

| Proto | Default port | Pipeline | Notes |
|-------|--------------|----------|-------|
| `https` | 443 | TLS-MITM HCM filter chain (`buildTLSFilterChain`) | Envoy terminates TLS with a per-domain MITM cert, inspects HTTP, re-encrypts upstream. Default when proto is empty. |
| `http` | 80 | Plaintext HCM filter chain (`buildHTTPFilterChain`) | `transport_protocol: raw_buffer` match. Host-header routing, no TLS termination. For sites that genuinely serve plaintext on port 80. |
| `ssh` | 22 | Opaque TCP listener (`buildTCPListener`) | Per-rule listener on `tcp_<port>+offset`. No L7 inspection. |
| `tcp` | 443 | Opaque TCP listener (`buildTCPListener`) | Generic TCP passthrough. |
| (other) | 443 | Opaque TCP listener | Unknown proto names fall through to opaque TCP. |

## Access Log Schema

Every access log record emitted by clawker's Envoy uses OTel semantic conventions for network/server/client/tls identity (network, server, client, network.peer, tls registries), with one clawker-defined field (`action`) alongside the standard Envoy substitution operators. The colloquial pre-rename fields (`domain`, `client_ip`, `upstream_ip`, `upstream_port`) have been replaced by their OTel canonical equivalents (`server.address`, `client.address`, `network.peer.address`, `network.peer.port`); `request_host` (Host header) has been consolidated into `server.address` (plaintext HCMs override the SNI default with `%REQ(Host)%`). The legacy single `proto` field — which conflated L4/L5/L7 into one rule-type label and at one point even carried the verdict (`proto: "deny"`) — and the `source: envoy` discriminator are also gone (`resource.service.name=envoy` covers source; `@timestamp` covers timestamp). **Rule schema (`proto:` on `EgressRule`) and access-log schema are NOT in parity**: the rule keeps the simple `proto:` knob as the routing key for filter-chain shape selection; the access-log decomposes observed protocol layers into separate OTel fields.

Field reference (one per row on every access log record):

| Field | Source | Notes |
|-------|--------|-------|
| `server.address` | TLS HCM + TCP/SSH: `%REQUESTED_SERVER_NAME%` (SNI). Plaintext HCM: `%REQ(Host)%` (override in `buildHTTPAccessLog` because SNI is unavailable). Deny TCP chain: optional static value from `buildTCPAccessLog`'s variadic. | OTel-stable, replaces deprecated `tls.server.name` / `tls.client.server_name` (both deprecated in semconv ≥ v1.21 in favor of `server.address`). |
| `server.port` | not emitted (clawker's per-rule clusters pin upstream port — see `network.peer.port` for the resolved value) | No Envoy substitution operator exposes the downstream-target port. |
| `client.address` | `%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%` | OTel-stable, replaces colloquial `client_ip`. |
| `network.peer.address` | `%UPSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%` | OTel network-semconv for the post-resolution upstream peer Envoy actually connected to. Distinct from `server.address` (what the client asked for) so confused-deputy mismatch is queryable. |
| `network.peer.port` | `%UPSTREAM_REMOTE_PORT%` | Pair to `network.peer.address`. |
| `network.transport` | hardcoded `"tcp"` per call site | All filter chains are TCP-bound. OTel enum: `tcp\|udp\|pipe\|quic\|unix`. |
| `network.protocol.name` | rule's L7 name (HCM: `"http"`; opaque TCP: rule's `proto:` value; deny chain: `""`) | Lowercase free-form, IANA-aligned. Deny chain has no negotiated L7 — emits empty string. |
| `network.protocol.version` | `%PROTOCOL%` (HTTP only) | Envoy emits the raw HCM value (`HTTP/1.1` / `HTTP/2` / `HTTP/3`). Absent on TCP/SSH records (`%PROTOCOL%` is HTTP-only and would emit `-`). |
| `tls.established` | hardcoded per filter-chain shape: `"true"` for TLS-terminated HCM, `"false"` for plaintext HCM and per-rule opaque TCP/SSH listeners. OMITTED on the deny chain (catches both TLS handshakes and plaintext flows that no allow chain claimed — Envoy resets before observing which, so stamping a bool would mislead forensics). | Bool-as-string from Envoy substitution. The OS ingest pipeline `envoy-normalize` coerces to actual boolean type via Painless before storage, matching the index template's `boolean` mapping. Consumers query `tls.established:true`. |
| `tls.protocol.version` | `%DOWNSTREAM_TLS_VERSION%` | Returns `-` when TLS wasn't on the wire. |
| `tls.cipher` | `%DOWNSTREAM_TLS_CIPHER%` | Same null behavior as version. |
| `action` | `"allowed"` / `"denied"` (past-tense verdict) | The canonical firewall verdict per record. See verdict-source rules below. |
| `listener_ip` | `%DOWNSTREAM_LOCAL_ADDRESS_WITHOUT_PORT%` | Envoy-specific (no OTel mapping). Useful for multi-listener deployments; clawker today binds one listener so the value is constant but the field stays for observability hygiene. |
| `response_flags` | `%RESPONSE_FLAGS%` | Envoy short-code response-flag bitmap (e.g. `UC`, `UH`, `NR`). The `envoy-normalize` ingest pipeline strips Envoy's `"-"` sentinel for unset values. Diagnostic surface, not an OTel semconv field. |
| `bytes_sent` / `bytes_received` | `%BYTES_SENT%` / `%BYTES_RECEIVED%` | Downstream byte counters. Coerced to int by the OTel collector `transform/envoy_logs` Int() pass. |
| `upstream_bytes_sent` / `upstream_bytes_received` | `%UPSTREAM_WIRE_BYTES_SENT%` / `%UPSTREAM_WIRE_BYTES_RECEIVED%` | Upstream wire-level byte counters (post-TLS for MITM chains). Same int coercion. |
| `duration_ms` | `%DURATION%` | Total request duration in milliseconds. Emitted on every record (HTTP + TCP). Coerced to int. |
| `method` / `path` / `response_code` / `user_agent` | `%REQ(:METHOD)%` / `%REQ(:PATH)%` / `%RESPONSE_CODE%` / `%REQ(USER-AGENT)%` | **HTTP-only.** Standard HTTP access-log fields. `response_code` coerced to int. The `path` field is the post-`normalize_path` / `merge_slashes` / `path_with_escaped_slashes_action` canonical form — exactly what the route matcher saw. |
| `response_code_details` | `%RESPONSE_CODE_DETAILS%` | **HTTP-only.** Distinguishes Envoy-generated responses from upstream pass-through. Common values: `direct_response` (Envoy 4xx, e.g. path-rule deny → 403), `via_upstream` (response came from upstream), `local_reset` (Envoy abort). The `action`/`response_code_details` pair makes "Envoy verdict vs upstream verdict" queryable. |
| `req_duration_ms` / `resp_duration_ms` / `resp_tx_duration_ms` | `%REQUEST_DURATION%` / `%RESPONSE_DURATION%` / `%RESPONSE_TX_DURATION%` | **HTTP-only.** Phased durations: req = time spent receiving the downstream request; resp = time from request received to upstream response headers; resp_tx = time spent transmitting the response back to the downstream. All coerced to int. Distinct from `duration_ms` (total) so streaming workloads can attribute time between phases. |
| `upstream_tls_*` | upstream MITM re-encryption metadata | Kept as flat clawker operational fields (no OTel `tls.client.*` mapping; OTel deprecated `tls.client.*` for downstream identity, leaving no canonical home for upstream-side re-encryption diagnostics). Diagnostic, not part of the access-event schema. |
| `upstream_transport_failure_reason` | `%UPSTREAM_TRANSPORT_FAILURE_REASON%` | **HTTP-only.** Deliberately dropped from the TCP/SSH path because Envoy emits `-` for non-HTTP contexts and the field gave the false impression of TCP-side diagnostics. The OS ingest pipeline drops the literal `"-"` sentinel that Envoy substitutes when the field is unset. |

ALPN / `tls.next_protocol` is intentionally not on the schema — Envoy does not expose a downstream ALPN substitution that works reliably across filter-chain shapes. Add only when a verified substitution exists.

Verdict-source rules for `action`:
  - **TCP-level filter chains** (deny chain, per-rule TCP/SSH listeners) carry a uniform verdict — `action` is hardcoded at config generation in the call site (`buildTCPAccessLog("", "denied", ...)` for the deny chain catch-all, `"allowed"` for per-rule TCP/SSH allow listeners).
  - **HCM filter chains** (TLS-terminated HTTPS MITM) serve mixed verdicts because one HCM handles both allow and `direct_response` deny routes. Per-route metadata (`metadata.filter_metadata.clawker.action`) carries the verdict; the access log format reads it via `%METADATA(ROUTE:clawker:action)%`. Every route literal in the route table MUST stamp this metadata at construction (see `clawkerActionMetadata()` helper in `envoy_config.go`).

The `action` field is the canonical firewall verdict for downstream consumers (dashboards, alerting). It is NEVER inferred from `response_code`, `response_flags`, upstream health, or any downstream-of-routing signal. A legitimate upstream 403 (e.g. GitHub returning 403) lands on a `route → cluster` route whose metadata reads `action: "allowed"` — `response_code: 403` while `action: "allowed"` is the expected shape.

### Config vs event vocabulary

Rule input (`EgressRule.Action`, `PathRule.Action`, `PathDefault`) uses present-tense `allow` / `deny` — simple for users editing YAML / CLI. The access log emits past-tense `allowed` / `denied` (the verdict that actually happened). These are independent vocabularies by design — do NOT propagate "consistency" changes between them.

## HCM Hardening Contract

Every clawker HCM (`buildTLSFilterChain` for TLS-MITM termination AND `buildHTTPFilterChain` for plaintext HTTP) MUST include the field set returned by `httpConnectionManagerHardening()`. Applied via `maps.Copy` at HCM construction so no site can forget any field. The set is load-bearing for the path-rule security boundary:

- `normalize_path: true` + `merge_slashes: true` + `path_with_escaped_slashes_action: UNESCAPE_AND_REDIRECT` — defeats URL-encoded traversal (`%2e%2e/`, `..%2f`, double-encoded). Without these, `/allowed/%2e%2e/denied` literally starts with `/allowed/` → matches allow prefix → forwards upstream, bypassing path rules entirely.
- `common_http_protocol_options.headers_with_underscores_action: REJECT_REQUEST` — RFC 9110 §5.4.5 header-name aliasing prevention.
- `http2_protocol_options.max_concurrent_streams: 100` — h2 amplification cap.

Timeouts (`request_timeout`, `stream_idle_timeout`, `idle_timeout`) and `per_connection_buffer_limit_bytes` are deliberately NOT set. LLM API calls regularly stream for minutes with multi-MB request bodies; short caps rupture mid-stream and tight buffer limits trigger `downstream_local_disconnect(purging_socket_that_have_not_progressed_to_connections)` under upstream backpressure. Envoy's built-in defaults are the right choice for this workload.

The `TestGenerateEnvoyConfig_HCMHardening` regression test asserts every HCM in the generated YAML carries the full hardening set; a missing field would re-introduce the path-smuggling vector.

## Deny Body — Non-Fingerprinting

The package-level `firewallBlockedBody` constant is the response body returned by every clawker-firewall `direct_response: 403` route (SNI-block deny_all virtual host, per-path-rule deny, default unmatched path deny). Value is generic ("Forbidden\n") — never names the clawker product. An injected-prompt adversary should not be able to trivially distinguish a clawker block from a generic upstream Forbidden by reading the response body; the firewall verdict travels via the `action` access log field, not the body.

## Testing Requirements

- All Envoy config generation tests in `envoy_config_test.go` (`internal/controlplane/firewall/`)
- Test WebSocket assertions: `upgrade_type: websocket`, `envoy.network.application_protocols`
- Test LOGICAL_DNS clusters: correct domain endpoint, correct port, type: LOGICAL_DNS
- Test filter chain simplicity: http_filters should contain router only
- Test filter chain ordering: deny chain must be last
- Live testing: use `wscat` (NOT curl, which doesn't validate Sec-WebSocket-Accept)

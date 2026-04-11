---
description: Envoy proxy configuration guidelines for the firewall egress stack
paths: ["internal/firewall/envoy.go", "internal/firewall/envoy_test.go", "internal/firewall/manager.go"]
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
                            ├─ TLS (SNI match) → per-domain filter chain → LOGICAL_DNS cluster (re-encrypt upstream)
                            ├─ HTTP (raw_buffer) → Host header routing → per-domain LOGICAL_DNS cluster (plaintext)
                            └─ Unknown → deny chain (tcp_proxy → deny_cluster → reset)
```

- `tls_inspector` listener filter detects TLS vs plaintext
- TLS: `filter_chain_match.server_names` (SNI) routes to per-domain chains
- HTTP: `filter_chain_match.transport_protocol: "raw_buffer"` catches plaintext
- Deny: empty `filter_chain_match: {}` catches everything else

### LOGICAL_DNS Clusters (Security-Critical Design)

Each domain gets its own LOGICAL_DNS cluster with the domain name as the endpoint address. This is a deliberate security design:

- **Upstream destination is determined by the cluster endpoint, NOT the HTTP Host header.** This prevents confused deputy attacks where a malicious client inside the container manipulates the Host header to redirect traffic to unintended destinations.
- **Ports are hardcoded in cluster endpoints** from the rule's configured port. No `envoy.upstream.dynamic_port` filter state needed.
- **No DFP (Dynamic Forward Proxy) filter** — eliminated entirely. DFP trusts the Host header for routing, which is a security vulnerability in a transparent proxy where the client is untrusted.

Cluster types:
- **Per-domain TLS** (`tls_<domain>`): LOGICAL_DNS, upstream re-encryption with `auto_config` (h2 + h1.1 ALPN), `auto_sni`, `auto_san_validation`. Used by TLS filter chains.
- **Per-domain HTTP** (`http_<domain>`): LOGICAL_DNS, no upstream TLS. Used by HTTP filter chain.
- **Deny** (`deny_cluster`): STATIC, no endpoints — connection reset.

### Filter Chain Matching (critical ordering)

Envoy evaluates filter chains IN ORDER and stops at the first match. Multiple filter chains with the same `server_names` means only the first is reachable. Same-domain TLS rules with different path_rules MUST be merged into a single filter chain.

### http_filters

Both TLS and HTTP filter chains use a single http_filter: `envoy.filters.http.router`. No other HTTP filters are needed because LOGICAL_DNS clusters handle DNS resolution independently.

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

TLS clusters use `auto_config` with both `http_protocol_options` and `http2_protocol_options`. When the upstream supports h2, ALPN negotiates HTTP/2. **WebSocket upgrade doesn't work over HTTP/2 upstreams** — the Upgrade header mechanism doesn't exist in h2.

Fix: TLS filter chains use custom `upgrade_configs` filters: `set_filter_state` overrides `envoy.network.application_protocols` to `"http/1.1"`, forcing HTTP/1.1 on the upstream TLS handshake for WebSocket connections only.

### HTTP WebSocket: No Custom Filters Needed

HTTP (plaintext) filter chains use simple `upgrade_configs: [{upgrade_type: websocket}]`. No ALPN override needed because there's no TLS negotiation. The default HCM http_filters (router only) are used for WebSocket traffic.

### Correct WebSocket upgrade_configs

```
TLS filter chains:  ALPN override + router (custom filters)
HTTP filter chain:  upgrade_type: websocket (no custom filters, uses default HCM filters)
```

### Extended CONNECT (RFC 8441) — Untested

`allow_connect: true` in `http2_protocol_options` enables WebSocket over HTTP/2 via Extended CONNECT. This would allow H2 downstream clients to use WebSocket without falling back to HTTP/1.1.
- envoyproxy/envoy#38645 reports a multi-stream issue, but the referenced predecessor (#8547) was fixed in 2019 and the report lacks strong verification
- **Needs live testing** against the attacker server's `/ws/echo` endpoint before drawing conclusions
- If Extended CONNECT works, it would eliminate the H2 downstream WebSocket limitation entirely

### HTTP/2 Downstream (Client → Envoy)

Our TLS listener advertises `alpn_protocols: ["h2", "http/1.1"]` downstream. Clients that negotiate h2 (like Node.js `ws` library, wscat) cannot use the HTTP/1.1 Upgrade mechanism over h2. Real browsers use HTTP/1.1 for WebSocket natively. Programmatic clients may need to force HTTP/1.1 ALPN.

This is a known Envoy limitation, not something we can fix without removing h2 from the downstream ALPN (which would break HTTP/2 for all regular traffic).

## Rule Schema

Egress rule types live in `internal/config/schema.go` (not in the firewall package) because they're part of the persisted project config schema:

- `EgressRule` — single egress rule (dst, proto, port, action, path_rules, path_default)
- `PathRule` — HTTP path-level filtering (path prefix + action)
- `FirewallConfig` — per-project firewall config (add_domains shorthand + full rules)

The firewall package imports these types from config; config does NOT import firewall.

## Testing Requirements

- All Envoy config generation tests in `envoy_test.go`
- Test WebSocket assertions: `upgrade_type: websocket`, `envoy.network.application_protocols`
- Test LOGICAL_DNS clusters: correct domain endpoint, correct port, type: LOGICAL_DNS
- Test filter chain simplicity: http_filters should contain router only
- Test filter chain ordering: deny chain must be last
- Live testing: use `wscat` (NOT curl, which doesn't validate Sec-WebSocket-Accept)

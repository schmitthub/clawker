# Wildcard TLS Cluster Fix — Requirements

## The bug (concrete)

`buildClusters` line ~1075-1082: wildcard rule `.mintlify.com` and exact rule `mintlify.com`
both `normalizeDomain()` → `mintlify.com`. `seen` map dedups → ONE cluster (`tls_mintlify.com_443`).
Cluster is `LOGICAL_DNS` with endpoint address pinned to apex string `mintlify.com`.

Filter chain SNI=`www.mintlify.com` (wildcard chain) → routes to that single cluster →
LOGICAL_DNS resolves `mintlify.com` → Vercel `76.76.21.21` → Envoy connects to Vercel with
SNI=`www.mintlify.com` → Vercel returns wrong cert → `auto_san_validation` fails.

`upstream_transport_failure_reason: TLS_error:|268435581:SSL_routines:OPENSSL_internal:CERTIFICATE_VERIFY_FAILED:verify_cert_failed:_verify_SAN_list`

Confirmed via OpenSearch (`clawker-envoy` index) and direct openssl s_client probe.

## Regression history

- **PR #215** (Apr 1): wildcard domain support — worked because DFP resolved per Host header at runtime.
- **PR #231** (Apr 5): replaced DFP with LOGICAL_DNS to close confused-deputy + same-SAN h2 coalescing race that broke api.anthropic.com / statsig.anthropic.com (different IPs, shared SAN → DFP pool collapse → analytics calls hijacked chat connections).

Wildcard cluster shape was not carried forward into the LOGICAL_DNS world.

## What the fix must preserve

The fix replaces only the **wildcard TLS cluster + wildcard TLS filter chain** path. Exact-rule TLS,
plaintext HTTP, TCP/SSH, deny, OTel ALS, and health listeners are all untouched. The list below
is the contract the new wildcard path must satisfy. Every item links to where it lives today.

### Cluster contract (per wildcard rule)

| # | Requirement | Source |
|---|-------------|--------|
| C1 | Resolves the actual SNI hostname, NOT the apex string. | The bug. |
| C2 | Per-`host:port` isolated upstream connection pool (no SAN-driven h2/h3 coalescing across pools). | `feedback envoy_config.go:1072` + #231 rationale. |
| C3 | `connect_timeout: 10s` parity with exact cluster. | `buildTLSDNSCluster:1138` |
| C4 | `dns_lookup_family: V4_ONLY`. | `buildTLSDNSCluster:1140` |
| C5 | Upstream TLS context: trusted CA `/etc/ssl/certs/ca-certificates.crt`, `auto_sni: true`, `auto_san_validation: true`. | `buildTLSDNSCluster:1160-1176, 1181-1183` |
| C6 | `tls_params.ecdh_curves: [X25519, P-256, P-384]`. | `buildTLSDNSCluster:1167` |
| C7 | `typed_extension_protocol_options.auto_config` with both `http_protocol_options` (h1.1) and `http2_protocol_options`. | `buildTLSDNSCluster:1184-1188` |
| C8 | Upstream ALPN `["h2", "http/1.1"]` is implied by `auto_config` — must be visible to upstream so h2 is selected when supported. | `buildTLSDNSCluster:1165` (downstream side); `auto_config` handles upstream. |

### Filter chain contract (per wildcard rule)

| # | Requirement | Source |
|---|-------------|--------|
| F1 | SNI matches `[".<apex>", "<apex>"]` (apex omitted when an exact same-listener rule owns it). Wildcard suffix matching by Envoy. | `serverNames():1327`, `buildTLSFilterChain:773-775` |
| F2 | Downstream TLS termination with per-domain MITM cert at `/etc/envoy/certs/<apex>-cert.pem` (`-key.pem`). Cert generation already mints both `<apex>` AND `*.<apex>` DNS SANs for wildcard rules — keep unchanged. | `buildTLSFilterChain:751-790`, `certs.go:103-109` |
| F3 | Downstream ALPN advertises `["h2", "http/1.1"]`. | `buildTLSFilterChain:781` |
| F4 | HCM `stat_prefix = tls_<apex>`. | `tlsHCMTypedConfig:830` |
| F5 | HCM access log: `buildHTTPAccessLog(true, "%METADATA(ROUTE:clawker:action)%", als)` with full OTel semconv field set (every field in `.claude/rules/envoy.md` "Access Log Schema"). | `tlsHCMTypedConfig:832` |
| F6 | HCM hardening fields via `maps.Copy(tc, httpConnectionManagerHardening())`. Path-rule security boundary depends on these. | `tlsHCMTypedConfig:849`, `httpConnectionManagerHardening:243` |
| F7 | WebSocket upgrade with ALPN-override-to-http/1.1: separate `upgrade_configs` filter chain via `buildTLSWebSocketUpgrade()`. Regular requests still negotiate h2 via ALPN. | `tlsHCMTypedConfig:836`, `buildTLSWebSocketUpgrade:931` |
| F8 | Route table from `buildHTTPRoutes(r, clusterName)` when `r.PathRules` is set; allow-all single-route otherwise. Each route carries `clawkerActionMetadata("allowed"|"denied")` and `timeout: "0s"`. | `buildTLSFilterChain:759-770`, `buildHTTPRoutes:870`, `clawkerActionMetadata:266` |
| F9 | Virtual host name via `virtualHostName(r.Dst)` (wildcard rules get `wildcard_<apex>` prefix to avoid collision when exact rule for same apex exists in same route_config). | `tlsHCMTypedConfig:841`, `virtualHostName:1342` |
| F10 | Virtual host domains via `httpDomains(r.Dst, exactDomains)` — wildcard yields `["*.<apex>", "*.<apex>:*"]` (and apex if no exact owner). | `tlsHCMTypedConfig:842`, `httpDomains:1354` |
| F11 | Confused-deputy boundary: upstream destination is determined by SNI, **never** by Host header. A malicious request with allowed SNI but malicious Host MUST NOT redirect to a different upstream IP. | #231 design intent, `.claude/rules/envoy.md` "LOGICAL_DNS Clusters (Security-Critical Design)" |
| F12 | %REQUESTED_SERVER_NAME% is available to HTTP filters at request time (it's an L4-connection-level value, present throughout the HCM). The set_filter_state HTTP filter's `on_request_headers` reads from stream info, where it's exposed. | `set_filter_state` HTTP filter `format_string` spec + existing usage at `buildTLSWebSocketUpgrade:946` (already uses `inline_string` in same context). |

### Cluster + chain interaction

| # | Requirement |
|---|-------------|
| I1 | A wildcard rule and an exact rule for the same apex must produce **two distinct clusters** (one DFP-wildcard, one LOGICAL_DNS-exact). Their connection pools never share. |
| I2 | The wildcard cluster name must differ from the exact cluster name. The filter chains route accordingly: wildcard chain → wildcard cluster, exact chain → exact cluster. |
| I3 | `tlsExactDomains` set (line 497-502) continues to suppress the apex from wildcard `server_names` / `httpDomains` lists when an exact rule owns the apex. (Pure metadata, no change.) |
| I4 | Deny filter chain stays last in the listener (`buildEgressListener:655-656`). |

### Operational

| # | Requirement |
|---|-------------|
| O1 | Reload (`Stack.Reload`) regenerates config and restarts Envoy. New cluster shape must reload cleanly with existing rule store. |
| O2 | `FirewallStatus` / `ListRules` / per-RPC status enums (PR #311) untouched. |
| O3 | Access log records continue to carry `server.address`, `network.peer.address`, `network.peer.port`, `network.protocol.version`, `tls.*`, `action`, `cluster_name` (if we add it — optional but recommended for the UAT). |
| O4 | Existing exact-rule code path unchanged — proves by inspection no behavioral change for non-wildcard rules. |
| O5 | All existing unit tests in `envoy_config_test.go` continue to pass (1647 lines, lots of structural assertions). |
| O6 | `TestGenerateEnvoyConfig_HCMHardening` still passes — wildcard chain HCM must carry hardening. |

### Out of scope (do not regress, but not adding to)

- UDP/443 (QUIC/HTTP3) — still denied at eBPF (intentional).
- TLS passthrough — clawker MITMs all TLS by design.
- HTTP/3 downstream.
- Cert hot-reload mechanics.
- Project-config rule composition layer.

## Design (verified against Envoy 1.37.1 docs)

### New cluster builder: `buildTLSWildcardDFPCluster(apex string, port int)`

```yaml
name: tls_wildcard_<apex>_<port>           # I2 (distinct from tls_<apex>_<port>)
connect_timeout: 10s                        # C3
lb_policy: CLUSTER_PROVIDED                 # required by DFP
cluster_type:
  name: envoy.clusters.dynamic_forward_proxy
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.clusters.dynamic_forward_proxy.v3.ClusterConfig
    sub_clusters_config:                    # C1, C2
      max_sub_clusters: 1024
      sub_cluster_ttl: 300s
    # allow_coalesced_connections defaults false → C2 enforced.
transport_socket:                           # C5, C6
  name: envoy.transport_sockets.tls
  typed_config:
    "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext
    common_tls_context:
      tls_params:
        ecdh_curves: [X25519, P-256, P-384]
      validation_context:
        trusted_ca:
          filename: /etc/ssl/certs/ca-certificates.crt
typed_extension_protocol_options:           # C5 (auto_sni/san), C7 (auto_config)
  envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
    "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
    upstream_http_protocol_options:
      auto_sni: true
      auto_san_validation: true
    auto_config:
      http_protocol_options: {}
      http2_protocol_options: {}
```

### New filter-chain shape: wildcard chains add 2 HTTP filters before `router`

Existing exact-rule chain http_filters: `[router]`.
Wildcard-rule chain http_filters: `[set_filter_state, dynamic_forward_proxy, router]`.

```yaml
http_filters:
  - name: envoy.filters.http.set_filter_state            # F11, F12
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.filters.http.set_filter_state.v3.Config
      on_request_headers:
        - object_key: envoy.upstream.dynamic_host
          format_string:
            text_format_source:
              inline_string: "%REQUESTED_SERVER_NAME%"
  - name: envoy.filters.http.dynamic_forward_proxy       # C1, C2
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.filters.http.dynamic_forward_proxy.v3.FilterConfig
      allow_dynamic_host_from_filter_state: true        # Envoy 1.35+; we're on 1.37.1
      sub_cluster_config:
        cluster_init_timeout: 5s
  - name: envoy.filters.http.router
```

### WebSocket upgrade interaction (F7)

Today `buildTLSWebSocketUpgrade()` replaces the HCM's `http_filters` with
`[set_filter_state (ALPN override), router]` for the websocket upgrade.

For wildcard chains, the upgrade filter list must contain the dynamic_host wiring too:

```yaml
upgrade_configs:
  - upgrade_type: websocket
    filters:
      - name: envoy.filters.http.set_filter_state    # dynamic_host (NEW for wildcards)
        typed_config: { ... inline_string: "%REQUESTED_SERVER_NAME%" }
      - name: envoy.filters.http.set_filter_state    # ALPN override → http/1.1
        typed_config: { ... inline_string: "http/1.1" }
      - name: envoy.filters.http.dynamic_forward_proxy
        typed_config: { allow_dynamic_host_from_filter_state: true, sub_cluster_config: {...} }
      - name: envoy.filters.http.router
```

Two `set_filter_state` filters in sequence are valid — each writes a different `object_key`.

### Why this satisfies F11 (confused deputy)

- Filter chain entry gated on SNI suffix match `.<apex>` (existing `serverNames()`).
- `dynamic_host` written from `%REQUESTED_SERVER_NAME%` (SNI), NOT from `:authority` (Host).
- Even if a hostile client sends `Host: unrelated.com`, the DFP filter reads `dynamic_host`
  from filter state (which has SNI), not from the request authority.
- Therefore the upstream destination is bounded to the wildcard suffix that the SNI matched
  the filter chain on. Host header is forwarded as-is to upstream (which is the right thing
  for app-layer routing) but cannot influence which IP Envoy connects to.

### Why this satisfies C2 (#231 race anti-regression)

- `sub_clusters_config` creates an independent sub-cluster per `host:port`.
- Verified via `source/extensions/clusters/dynamic_forward_proxy/cluster.cc`:
  `LoadBalancer::lifetimeCallbacks()` returns `{}` when `allow_coalesced_connections` is false.
  No cross-pool lifetime tracking → no cross-pool reuse.
- `allow_coalesced_connections` proto field default is `false` (Go zero value, we never set it).
- Therefore api.anthropic.com and statsig.anthropic.com (or apex+www mintlify) get separate pools
  even when their certs share SANs.

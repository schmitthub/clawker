# Envoy generator — target final-state config (the spec the Go must reproduce)

Every shape below is grounded in a concrete Envoy sandbox/example or proto, not training memory.

| Source | Establishes |
|--------|-------------|
| `examples/tls-inspector/envoy.yaml` | one listener, `tls_inspector`, chains keyed by `transport_protocol`; empty catch-all chain is least-specific |
| `examples/tls-sni/envoy.yaml` | `DownstreamTlsContext` per chain; SNI cert; HCM→cluster (we collapse the N-chain part — see below) |
| `tls.proto` `require_sni` (`[#not-implemented-hide:]`) | **`require_sni` is NOT implemented** — cannot be the SNI gate. `full_scan_certs_on_sni_mismatch:false` (default): on SNI mismatch *or absent SNI* **the first cert is served and the request proceeds** (no TLS-layer reject) |
| `listener_components.proto` `FilterChainMatch.server_names` + `examples/tls-inspector` | **`server_names` + `tls_inspector` IS the only server-side SNI gate**: SNI matched to a chain (exact, then `*.apex`); no match → `default_filter_chain` (we set deny) or reset → N per-SNI chains, each its own MITM cert |
| `examples/tls/envoy-https-https.yaml` | downstream-terminate + upstream-originate = MITM reencrypt (`UpstreamTlsContext` on cluster) |
| DFP filter docs + `proxy_filter.cc` | DFP LB derives host+port from `:authority`; default port 80 cleartext / 443 secure-upstream |
| `examples/websocket/envoy-ws.yaml` | plaintext WS = HCM + `upgrade_configs:[{upgrade_type:websocket}]` |
| `examples/websocket/envoy-wss.yaml` | WSS = downstream TLS terminate + `upgrade_configs` + upstream `UpstreamTlsContext` |
| `examples/websocket/envoy-ws-route.yaml` | `upgrade_configs` settable **per-route** (path-scoped WS) |
| `examples/tls-inspector` + `tls-sni` (domain3) + `wss-passthrough` | opaque `tcp_proxy` filter chain → pinned cluster (ssh / raw tcp) |
| `examples/udp/envoy.yaml` | raw UDP = `udp_proxy` listener filter (future `udp` token) |
| `configs/envoyproxy_io_proxy_http3_downstream.yaml` | h3 = UDP listener `quic_options` + `QuicDownstreamTransport` + HCM `codec_type:HTTP3`; TCP chain emits `alt-svc:h3` |
| `listener_components.proto` FilterChainMatch | one `destination_port` per chain (no range field); most-specific-port wins |

## Core invariant — shared L7 app (HCM route_config) reused; transport differs per chain

The **HCM `route_config` (the L7 "httpApp": Host-gated vhosts, path-rule routes, hardening, Host-keyed DFP wildcard, `deny_all`) is identical** across http / https / ws / wss / h3 — the *same* `route_config` object is referenced by every chain's HCM. What the deriver supplies per transport:
- **transport block** — what decrypts the downstream:
  - http/ws: `{transport_protocol: raw_buffer}`, no socket.
  - https/wss: **one `server_names` filter chain per SNI** (exact host or `*.apex`), each with its **own MITM cert** as `transport_socket`. `server_names` IS the SNI gate (`require_sni` is not-implemented). Unknown/absent SNI → no chain matches → `default_filter_chain` deny / reset.
  - h3: UDP `quic_options` + `QuicDownstreamTransport` (same per-SNI certs).
- **upstream block** — whether the cluster reencrypts (plaintext vs `UpstreamTlsContext`).
- **ws enrichment** — adds `upgrade_configs` (per-route); downstream HCM sets `http2_protocol_options.allow_connect:true` so WS-over-h2 Extended CONNECT works; wss upstream cluster forced to **http/1.1** (WS-native; no upstream-h2 `allow_connect` needed).

> The HCM-**envelope** (codec, hardening, filter order, deny_all skeleton) and the `route_config` are shared. The transport supplies `{server_names+cert (https), dfp cache name, upstream cluster}`. They are *referenced* identically, not re-derived per chain — but the https chains are N (one per SNI), not one, because the SNI gate lives in `filter_chain_match`.

ssh / raw-tcp / raw-udp are the exception: **opaque** — no L7, their own dedicated listener + `tcp_proxy`/`udp_proxy`, because raw TCP/UDP has no Host/SNI to gate on (the gate is eBPF + the pinned cluster).

```
http        chain {transport_protocol: raw_buffer}                                  → httpApp → plaintext upstream
https       N chains {transport_protocol: tls, server_names:[host]} + per-SNI cert  → httpApp → reencrypt upstream
            (default_filter_chain = deny: unknown/absent SNI reset server-side)
ws          == http  + per-route upgrade_configs:websocket   (+ HCM allow_connect:true)
wss         == https + per-route upgrade_configs:websocket    (upstream cluster pinned http/1.1)
h3 (https)  UDP listener quic_options + QuicDownstream(per-SNI certs), HCM codec HTTP3 → httpApp → reencrypt upstream
ssh / tcp   dedicated listener :TCPPortBase+idx → tcp_proxy → pinned LOGICAL_DNS     (opaque TCP, no L7)
udp (raw)   dedicated UDP listener + udp_proxy listener filter → pinned cluster      (opaque datagrams, no TLS/L7)
```

`udp` ≠ `h3`: h3 is QUIC (TLS 1.3 + HTTP/3) over UDP and rides the L7 `httpApp` with full inspection; raw `udp` is opaque datagram forwarding (`udp_proxy`) with no handshake and no L7 — gated only by eBPF + the pinned cluster, the UDP analogue of the ssh/tcp opaque path.

## Defense in depth (independent layers; each enforces alone)

1. **eBPF** — only allowed `dst:proto:port` is redirected to Envoy at all.
2. **CoreDNS** — unlisted domains NXDOMAIN (exact-host scoped; wildcard subtree-forward).
3. **SNI chain gate** (https/wss/h3) — `tls_inspector` reads the ClientHello SNI; only allowed domains have a `server_names` filter chain. Unknown/absent SNI matches no chain → `default_filter_chain` deny / connection reset, **server-side, before any HTTP**. (NOT `require_sni` — that field is unimplemented; a single multi-cert chain would serve the first cert and proceed, so this layer requires per-SNI `server_names` chains.) Even if a client reaches L7, the MITM cert it's served is the *requested* domain's cert, so a redirected/unknown host fails the client's own cert validation (defense-in-depth, not the primary gate).
4. **Host vhost whitelist** (http/https/ws/wss/h3) — decrypted `Host` not matching → `deny_all` 403.
5. **Path rules** — per-vhost prefix deny → 403.
6. **Upstream identity** — reencrypt clusters: `auto_sni` + `auto_san_validation` vs the real cert via system CA.
7. **Opaque pinning** (ssh/tcp) — dedicated listener + LOGICAL_DNS pinned to the rule's host:port; client cannot redirect.
8. **Wildcard scoping** — DFP resolves any arriving Host but only vhost-admitted `*.apex` Hosts route to it.

---

## Representative ruleset (exercises every token + feature)

```yaml
rules:
  - {dst: example.com,       proto: http,  port: 80}                                  # exact http
  - {dst: .example.com,      proto: http,  port: 8080}                                # wildcard http
  - {dst: api.site.com,      proto: http,  port: 80,                                  # http + path rules
     path_default: deny, path_rules: [{path: /v1, action: allow},
                                       {path: /v1/internal, action: deny}]}
  - {dst: api.anthropic.com, proto: https}                                            # exact https (443)
  - {dst: .mintlify.com,     proto: https}                                            # wildcard https
  - {dst: realtime.io,       proto: ws,    port: 80}                                  # websocket over http
  - {dst: stream.anthropic.com, proto: wss}                                           # websocket over https
  - {dst: github.com,        proto: ssh,   port: 22}                                  # ssh (opaque)
  - {dst: db.example.com,    proto: tcp,   port: 5432}                                # raw tcp domain rule
  - {dst: cluster.example.com, proto: tcp, port_range: "9000-9100"}                   # NEW: port range
  - {dst: relay.example.com,   proto: udp, port: 3478}                                # raw udp (opaque datagrams)
```

## Target `envoy.yaml`

```yaml
admin: {address: {socket_address: {address: 127.0.0.1, port_value: 9901}}}

static_resources:
  listeners:
    # ════════════════ shared TCP egress (http/https/ws/wss) ════════════════
    - name: egress
      address: {socket_address: {address: 0.0.0.0, port_value: 10000}}
      listener_filters:
        - {name: envoy.filters.listener.tls_inspector,
           typed_config: {"@type": type.googleapis.com/envoy.extensions.filters.listener.tls_inspector.v3.TlsInspector}}
      filter_chains:

        # ── PLAINTEXT chain (http + ws share it) ──
        - filter_chain_match: {transport_protocol: raw_buffer}
          filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": …HttpConnectionManager
                stat_prefix: http_egress
                codec_type: AUTO
                normalize_path: true            # + merge_slashes, path_with_escaped_slashes_action,
                merge_slashes: true             #   headers_with_underscores_action, max_concurrent_streams
                path_with_escaped_slashes_action: UNESCAPE_AND_REDIRECT
                common_http_protocol_options: {headers_with_underscores_action: REJECT_REQUEST}
                http2_protocol_options: {max_concurrent_streams: 100}
                access_log: [ stdout(tls.established=false, server.address=%REQ(Host)%) ]   # +OTel iff MTLS
                http_filters:
                  - {dynamic_forward_proxy(dns_cache_config: http_dfp_cache)}   # present iff any wildcard http/ws
                  - {router}
                route_config:
                  name: http_egress_routes
                  virtual_hosts:
                    - name: example_com_80               # exact http
                      domains: [example.com, "example.com:80"]
                      typed_per_filter_config: {dynamic_forward_proxy: {FilterConfig, disabled: true}}
                      routes: [ {prefix /, meta allowed, route {cluster: http_example_com_80, timeout: 0s}} ]
                    - name: wildcard_example_com_8080    # wildcard http → DFP (no disable)
                      domains: ["*.example.com:8080", "example.com:8080"]
                      routes: [ {prefix /, meta allowed, route {cluster: http_dfp, timeout: 0s}} ]
                    - name: api_site_com_80              # http + path rules (longest-prefix first)
                      domains: [api.site.com, "api.site.com:80"]
                      typed_per_filter_config: {dynamic_forward_proxy: {disabled: true}}
                      routes:
                        - {prefix /v1/internal, meta denied,  direct_response: {status 403, body "Forbidden\n"}}
                        - {prefix /v1,          meta allowed, route {cluster: http_api_site_com_80, timeout 0s}}
                        - {prefix /,            meta denied,  direct_response: {status 403, body "Forbidden\n"}}  # path_default: deny
                    - name: realtime_io_80               # ws over http → upgrade_configs on the route
                      domains: [realtime.io, "realtime.io:80"]
                      typed_per_filter_config: {dynamic_forward_proxy: {disabled: true}}
                      routes:
                        - {prefix /, meta allowed,
                           route: {cluster: http_realtime_io_80, timeout: 0s,
                                   upgrade_configs: [{upgrade_type: websocket}]}}
                    - name: deny_all
                      domains: ["*"]
                      typed_per_filter_config: {dynamic_forward_proxy: {disabled: true}}
                      routes: [ {prefix /, meta denied, direct_response: {status 403, body "Forbidden\n"}} ]

        # ── TLS chains: ONE per https/wss SNI (server_names IS the SNI gate) ──
        #   Each chain differs in {server_names match, per-SNI MITM cert, its OWN single vhost}.
        #   The HCM ENVELOPE (stat_prefix, codec, hardening, http_filters skeleton, route_config name,
        #   deny_all) is identical and rendered by the SAME httpAppLayer code as plaintext http.
        #   What is NOT shared: the vhost list. Each chain carries ONLY its own domain's vhost +
        #   deny_all — NEVER the full set. Putting all vhosts in every chain is a confused-deputy bug:
        #   SNI=api.anthropic.com + Host=mintlify.com would route to the mintlify upstream. The per-SNI
        #   chain pins the connection to its domain; Host must equal that domain or hit deny_all.
        #   (Contrast plaintext http: all http rules share ONE raw_buffer chain, so mergeHCMVHosts
        #   unions their vhosts; https chains have distinct server_names → never merged → one vhost each.)

        # exact https — own vhost only, pinned LOGICAL_DNS reencrypt, no DFP filter on this chain
        - filter_chain_match: {transport_protocol: tls, server_names: [api.anthropic.com]}
          transport_socket: {tls: DownstreamTlsContext{common_tls_context:{alpn_protocols:[h2,http/1.1],
              tls_certificates:[{cert /etc/envoy/certs/api.anthropic.com-cert.pem, key …-key.pem}]}}}
          filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": …HttpConnectionManager
                stat_prefix: http_egress
                codec_type: AUTO
                # …identical httpConnectionManagerHardening() (normalize_path/merge_slashes/…)…
                access_log: [ stdout(tls.established=true, server.address=%REQUESTED_SERVER_NAME%) ]  # +OTel iff MTLS
                http_filters: [ {router} ]                       # no DFP filter — exact chain
                route_config:
                  name: http_egress_routes
                  virtual_hosts:
                    - name: api_anthropic_com_443
                      domains: [api.anthropic.com, "api.anthropic.com:443"]
                      routes: [ {prefix /, meta allowed, route {cluster: tls_api_anthropic_com_443, timeout 0s}} ]
                    - name: deny_all
                      domains: ["*"]
                      routes: [ {prefix /, meta denied, direct_response: {status 403, body "Forbidden\n"}} ]

        # wildcard https — own vhost only → DFP (https_dfp_cache); DFP filter present on THIS chain;
        #   deny_all disables DFP per-vhost so it never pre-resolves/503s the 403
        - filter_chain_match: {transport_protocol: tls, server_names: ["*.mintlify.com", mintlify.com]}
          transport_socket: {tls: DownstreamTlsContext{common_tls_context:{alpn_protocols:[h2,http/1.1],
              tls_certificates:[{cert /etc/envoy/certs/mintlify.com-cert.pem, key …}]}}}   # SAN *.mintlify.com + mintlify.com
          filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": …HttpConnectionManager
                # …same envelope…
                http_filters: [ {dynamic_forward_proxy(dns_cache_config: https_dfp_cache)}, {router} ]
                route_config:
                  name: http_egress_routes
                  virtual_hosts:
                    - name: wildcard_mintlify_com_443
                      domains: ["*.mintlify.com", "*.mintlify.com:443", mintlify.com, "mintlify.com:443"]
                      routes: [ {prefix /, meta allowed, route {cluster: https_dfp, timeout 0s}} ]   # Host-following, DFP live
                    - name: deny_all
                      domains: ["*"]
                      typed_per_filter_config: {dynamic_forward_proxy: {disabled: true}}
                      routes: [ {prefix /, meta denied, direct_response: {status 403, body "Forbidden\n"}} ]

        # wss — exact-shape chain + per-route upgrade_configs (own vhost only). Upstream cluster pinned http/1.1.
        - filter_chain_match: {transport_protocol: tls, server_names: [stream.anthropic.com]}
          transport_socket: {tls: DownstreamTlsContext{common_tls_context:{alpn_protocols:[h2,http/1.1],
              tls_certificates:[{cert /etc/envoy/certs/stream.anthropic.com-cert.pem, key …}]}}}
          filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config: {…envelope, http_filters:[{router}], route_config:{name: http_egress_routes,
                virtual_hosts:[
                  {name: stream_anthropic_com_443, domains:[stream.anthropic.com,"stream.anthropic.com:443"],
                   routes:[{prefix /, meta allowed, route:{cluster: tls_stream_anthropic_com_443, timeout 0s,
                            upgrade_configs:[{upgrade_type: websocket}]}}]},
                  {name: deny_all, domains:["*"], routes:[{prefix /, meta denied, direct_response:{status 403}}]} ]}}

      # default_filter_chain: DENY — a tls connection whose SNI matches no server_names chain
      #   (unknown or absent SNI) lands here. THIS is the server-side SNI gate (layer 3): tcp_proxy →
      #   deny_cluster (STATIC, no endpoints) → reset. (Absent a default chain Envoy also closes on no
      #   match; the explicit deny chain makes the reject loggable + unambiguous.)

    # ════════════════ UDP egress — https/wss over QUIC (h3) ════════════════
    #   reuses the SAME app block (own-vhost-per-chain http_egress_routes); only transport (quic) +
    #   codec (HTTP3) differ. Self-secure: per-SNI server_names chains + per-SNI cert; an unmatched
    #   SNI matches no chain and the QUIC handshake fails (fail-closed) — no tcp_proxy deny default
    #   (tcp_proxy is meaningless on a UDP listener). The TCP chain emits `alt-svc: h3=":<origin
    #   port>"` (the authority the client dials + eBPF redirects — 443 here, NOT Envoy's :10000).
    #   No dependency on any other layer's UDP handling.
    - name: egress_quic
      address: {socket_address: {protocol: UDP, address: 0.0.0.0, port_value: 10000}}
      udp_listener_config: {quic_options: {}, downstream_socket_config: {prefer_gro: true}}
      filter_chains:
        - transport_socket:
            name: envoy.transport_sockets.quic
            # QUIC listener also matches filter chains by server_names → one chain per SNI, per-SNI cert
          # (same shape as the TCP tls chains; require_sni is unimplemented so server_names is the gate here too).
          typed_config:
              "@type": …QuicDownstreamTransport
              downstream_tls_context: {common_tls_context: {tls_certificates: […per-SNI cert, one chain each…]}}
          filters: [ HCM {codec_type: HTTP3, http3_protocol_options: {},   # allow_extended_connect lands with the ws/wss token
                          route_config: {…own-vhost-per-chain, same as TCP tls chain…},
                          http_filters: [dfp(https_dfp_cache) iff wildcard, router]} ]

    # ════════════════ opaque per-rule listeners (ssh / raw tcp) ════════════════
    - name: tcp_github_com_22           # ssh
      address: {socket_address: {address: 0.0.0.0, port_value: 15000}}   # TCPPortBase + 0
      filter_chains:
        - filters: [ {tcp_proxy: {cluster: tcp_github_com_22, stat_prefix: tcp_github_com_22,
                                  access_log: [stdout(network.protocol.name=ssh, action=allowed,
                                                      server.address=github.com, tls.established=false)] }} ]
    - name: tcp_db_example_com_5432     # raw tcp
      address: {socket_address: {address: 0.0.0.0, port_value: 15001}}   # TCPPortBase + 1
      filter_chains:
        - filters: [ {tcp_proxy: {cluster: tcp_db_example_com_5432, stat_prefix: …, access_log: […tcp, allowed…]}} ]

    # ──── port-range raw tcp (mapping A — per-port pinned; self-secure) ────
    #   ONE listener + tcp_proxy + pinned LOGICAL_DNS cluster PER in-range port. Envoy dials
    #   host:exact-port itself; no ORIGINAL_DST, no trust in any arriving dst. 9000-9100 → 101 of these.
    - name: tcp_cluster_example_com_9000
      address: {socket_address: {address: 0.0.0.0, port_value: 15002}}   # TCPPortBase + 2
      filter_chains:
        - filters: [ {tcp_proxy: {cluster: tcp_cluster_example_com_9000, stat_prefix: …,
                                  access_log: […tcp, allowed, server.address=cluster.example.com…]}} ]
    - name: tcp_cluster_example_com_9001
      address: {socket_address: {address: 0.0.0.0, port_value: 15003}}   # TCPPortBase + 3
      filter_chains:
        - filters: [ {tcp_proxy: {cluster: tcp_cluster_example_com_9001, stat_prefix: …}} ]
    # … one per port through 9100 (TCPPortBase + 2 + (port-9000)) …

    # ════════════════ opaque raw-UDP per-rule listener (udp token) ════════════════
    #   per examples/udp/envoy.yaml — a UDP listener whose udp_proxy listener filter
    #   forwards datagrams to a pinned cluster ONLY. No tls_inspector, no HCM, no SNI/Host.
    #   Self-secure: the single pinned cluster is the only reachable destination through this listener.
    - name: udp_relay_example_com_3478
      address: {socket_address: {protocol: UDP, address: 0.0.0.0, port_value: 16000}}   # UDPPortBase + 0
      listener_filters:
        - name: envoy.filters.udp_listener.udp_proxy
          typed_config:
            "@type": type.googleapis.com/envoy.extensions.filters.udp.udp_proxy.v3.UdpProxyConfig
            stat_prefix: udp_relay_example_com_3478
            matcher:
              on_no_match:
                action:
                  name: route
                  typed_config:
                    "@type": type.googleapis.com/envoy.extensions.filters.udp.udp_proxy.v3.Route
                    cluster: udp_relay_example_com_3478

  clusters:
    # plaintext exact (http / ws)
    - {name: http_example_com_80,   LOGICAL_DNS, V4_ONLY, → example.com:80}
    - {name: http_api_site_com_80,  LOGICAL_DNS, V4_ONLY, → api.site.com:80}
    - {name: http_realtime_io_80,   LOGICAL_DNS, V4_ONLY, → realtime.io:80}
    # plaintext DFP (wildcard http) — Host-keyed dns_cache (http_dfp_cache), NO upstream TLS, default port 80
    - {name: http_dfp, CLUSTER_PROVIDED, cluster_type: dynamic_forward_proxy(dns_cache_config: http_dfp_cache)}

    # https reencrypt — UpstreamTLS posture is UNIFORM for exact + wildcard, and carries NO sni-lock
    #   filter: the per-SNI chain's own-vhost gate already pins Host==SNI-domain (mismatched Host → deny_all
    #   403, never an upstream), so deriving upstream SNI from :authority is safe. Posture:
    #     HttpProtocolOptions.upstream_http_protocol_options{auto_sni:true, auto_san_validation:true}
    #       — SNI from :authority (works for DFP's dynamic host; auto_host_sni does NOT, no static hostname),
    #         SAN validated against it; + auto_config (h1/h2 ALPN).
    #     UpstreamTlsContext.common_tls_context: alpn[h2,http/1.1], tls_params.ecdh_curves[X25519,P-256,P-384],
    #       validation_context.trusted_ca /etc/ssl/certs/ca-certificates.crt.
    # exact https — LOGICAL_DNS pinned to the domain (IP-pinned) + the uniform UpstreamTLS posture.
    - {name: tls_api_anthropic_com_443, LOGICAL_DNS → api.anthropic.com:443, +UpstreamTLS(auto_sni,auto_san_validation), auto_config}
    # wss — SAME, but HttpProtocolOptions explicit_http_config (upstream pinned http/1.1; WS is h1.1-native).
    - {name: tls_stream_anthropic_com_443, LOGICAL_DNS → stream.anthropic.com:443, +UpstreamTLS, explicit_http_config: http_protocol_options}
    # wildcard https — Host-keyed dns_cache (https_dfp_cache), same UpstreamTLS posture, default port 443.
    - {name: https_dfp, CLUSTER_PROVIDED, cluster_type: dynamic_forward_proxy(dns_cache_config: https_dfp_cache), +UpstreamTLS(auto_sni,auto_san_validation)}

    # opaque ssh / tcp — LOGICAL_DNS pinned, NO TLS, NO L7
    - {name: tcp_github_com_22,        LOGICAL_DNS → github.com:22}
    - {name: tcp_db_example_com_5432,  LOGICAL_DNS → db.example.com:5432}
    # port-range — one pinned LOGICAL_DNS cluster per in-range port (mapping A; self-secure, no ORIGINAL_DST)
    - {name: tcp_cluster_example_com_9000, LOGICAL_DNS → cluster.example.com:9000}
    - {name: tcp_cluster_example_com_9001, LOGICAL_DNS → cluster.example.com:9001}
    # … through :9100 …
    # raw udp — pinned LOGICAL_DNS over UDP datagrams (no TLS, no L7)
    - {name: udp_relay_example_com_3478, LOGICAL_DNS → relay.example.com:3478}

    # deny_cluster — STATIC, zero endpoints. Backs the egress listener's default_filter_chain
    #   (unmatched SNI / unmatched transport) so the connection resets. Emitted whenever the tls
    #   transport is present (any https/wss rule).
    - {name: deny_cluster, type: STATIC, load_assignment: {endpoints: []}}

    # otel_collector_als — only when ALSConfig.MTLS
```

## Deriver block-mapping (`layersFor`)

| rule | transport | upstream | app enrichment |
|------|-----------|----------|----------------|
| http exact / wildcard | `tcpEgressLayer` | `httpExactUpstream` / `httpWildcardUpstream(http_dfp)` | `httpApp` |
| http + path rules | `tcpEgressLayer` | `httpExactUpstream` | `httpApp` (routes from path_rules) |
| https exact / wildcard | **TWO chains per rule** (deriver returns both): `tlsSNIChainLayer` (TCP egress, own `server_names` chain + per-SNI cert + deny default + tls_inspector + alt-svc h3) AND `quicSNIChainLayer` (egress_quic UDP, QuicDownstreamTransport + HTTP3) | `httpsExactUpstreamLayer` / `httpsWildcardUpstreamLayer(https_dfp)` (shared by both chains; AddCluster dedups) | `httpAppLayer` (own vhost only; `appDFP{active: wildcard, cache: https_dfp_cache}`) |
| ws | *(enriches the origin's `http` stack — `wsEnrichLayer` prepended)* | http upstream (plaintext h1.1 — no pin needed) | `httpAppLayer` reads `ctx.websocket` → route `upgrade_configs` + HCM `allow_connect` |
| wss | *(enriches the origin's `https` stack — `wsEnrichLayer` prepended to BOTH the tls and quic sibling chains)* | https upstream **pinned http/1.1** (`ctx.websocket` → `explicit_http_config`; wildcard → `wss_dfp`) | `httpAppLayer` reads `ctx.websocket` → route `upgrade_configs` + HCM `allow_connect`/`allow_extended_connect` |
| tcp/udp **port-range** | `tcpDedicatedListenerLayer(envoyPort, dstPort)` / `udpDedicatedListenerLayer(...)` ×N (one perm per in-range port, via `dedicatedPorts`) | `tcpPinnedUpstreamLayer` / `udpPinnedUpstreamLayer` ×N (LOGICAL_DNS host:port from `ctx.port`) | `tcpProxyTerminalLayer` / `udpProxyTerminalLayer` ×N |
| ssh / tcp | `tcpDedicatedListenerLayer(envoyPort)` | `tcpPinnedUpstreamLayer` | `tcpProxyTerminalLayer(proto)` — opaque L4 terminal (`tcp_proxy` network filter), NOT an L7 app |
| tcp port-range | `tcpDedicatedListenerLayer` ×N (one per in-range port) | `tcpPinnedUpstreamLayer` ×N (LOGICAL_DNS host:port) | `tcpProxyTerminalLayer` ×N |
| udp (raw) | `udpDedicatedListenerLayer(envoyPort)` (plain UDP socket, no quic_options/filter_chains) | `udpPinnedUpstreamLayer` (LOGICAL_DNS pin) | `udpProxyTerminalLayer` — `udp_proxy` LISTENER filter (matcher→Route→cluster), set via `SetListenerField`; NOT a chain filter |

> Opaque tokens have no L7 HCM **app** block, but they still need a third layer to render the L4 forwarding terminal (`tcp_proxy`/`udp_proxy`) — the opaque analogue of the app block. It reads `ctx.upstreamCluster` (named by the upstream block) and fails closed if absent. For TCP the terminal is a chain network filter (`ctx.filters` → `commit` builds the chain); for UDP it is a listener filter on a chainless listener (`commit` adds only the cluster).

Generation-wide facts decided once in the deriver (cannot be patched after a shared chain commits): the set of https/wss SNIs (→ one `server_names` chain + per-SNI cert each) + `tls_inspector` on the egress listener + the shared `http_egress_routes` route_config + the deny `default_filter_chain`; `httpDFPActive` / `httpsDFPActive`; the dedicated-listener port assignments (`tcpListenerPorts` = `TCPPortBase + idx` via `TCPMappings`; `udpListenerPorts` = `UDPPortBase + idx` via `UDPMappings`) — deterministic, keyed by `dedicatedPortKey(host, dstPort)`; the TCP layout must stay in lockstep with the eBPF `route_map` (`RoutesFromRules`). NOTE: `require_sni` is **not** a fact we can use — it is unimplemented; the SNI gate is the per-SNI `server_names` chains + deny default, decided here.

## Open design decisions

1. **SNI chain shape — RESOLVED by grounding → N per-SNI `server_names` chains.** `require_sni` is `[#not-implemented-hide:]` in `tls.proto`; a single multi-cert chain serves the first cert on SNI mismatch/absence and proceeds to L7 (`full_scan_certs_on_sni_mismatch:false` default), i.e. **no server-side SNI gate**. The only working primitive is `FilterChainMatch.server_names` + `tls_inspector`. Target now uses one `server_names` chain per https/wss SNI (each its own MITM cert) all sharing the HCM `route_config`, with a deny `default_filter_chain` catching unknown/absent SNI server-side. This restores defense-in-depth layer 3 — non-negotiable per the fail-closed mantra. *(If you want to override and accept a client-validation-only SNI posture, say so; otherwise this is settled.)*
2. **WS as a proto token — RESOLVED → additive enrichment, its own rule entry.** `proto: ws|wss` is its OWN egress-rule entry that ENRICHES the http/https stack for the same origin — never a separate chain, never a replacement for the base rule. UX (option #2): a user keeps their `https` rule and ADDS a `wss` rule to turn on websocket; `wss` does not subtract or shadow `https`. Mechanics: `canonicalProto` maps `ws→http`/`wss→https` so the pair COMPOSES on one host:port (no proto-collision); the deriver ABSORBS the ws/wss rule into an explicit base rule when one exists, or PROMOTES it (rewrite to base proto) when none does; `wsEnrichLayer` flips `ctx.websocket` so the shared app/upstream blocks add the upgrade inline. `https` with no `wss` → plain https; `http` with no `ws` → plain http.

3. **Port range — RESOLVED → A (per-port pinned).** Every Envoy atom must enforce host on its own; `ORIGINAL_DST` (B) forwards to whatever dst arrives = Envoy delegating host enforcement to the datapath = not self-secure. The self-secure atom is per in-range port: `tcp_proxy` → `LOGICAL_DNS` pinned to `host:exact-port`, Envoy dials the pin and trusts no arriving dst. Fan-out (100-port range → 100 listeners) is the cost of independence. B is rejected. (Needs a `port_range` schema field; eBPF's own range handling is eBPF's separate concern, not a precondition for this Envoy config.)
4. **Raw `udp` + `h3` — RESOLVED → emit; self-secure regardless of any other layer.** The `udp_proxy` listener forwards *only* to a pinned `LOGICAL_DNS` cluster — nothing else is reachable through it. The QUIC listener gets `server_names` chains + per-SNI cert + deny `default_filter_chain`, identical self-secure SNI gate to the TCP TLS chains. `alt-svc: h3` ships; the listener it advertises is self-secure on its own. No capability gate, no coupling to whether eBPF redirects UDP — that is a separate atom with its own enforcement.

## IP-literal destinations + self-signed certs (`insecure_skip_tls_verify`)

An IP dst (e.g. `192.168.1.3`, real for local dev) is NOT a degenerate FQDN — TLS to an IP sends **no SNI** (RFC 6066 is hostnames only), so the per-SNI `server_names` matcher cannot apply. "IP + self-signed" decomposes into **four orthogonal per-rule axes**; the deriver composes them, no monolithic TLS block hardcodes any one (see memory `code-resists-means-bad-coupling`):

| axis | FQDN | IP literal (`isIPOrCIDR`) | knob |
|------|------|---------------------------|------|
| 1. downstream chain-match **gate** | `server_names` (SNI) | `filter_chain_match.{prefix_ranges:[IP/32], destination_port}`, **no** `server_names`; listener needs `use_original_dst` | dst type |
| 2. downstream MITM cert **SAN** | dNSName | **iPAddress** SAN (`certs.go` must mint it) | dst type |
| 3. upstream cluster **resolution** | `LOGICAL_DNS` | **`STATIC`** (literal endpoint, no DNS) | dst type |
| 4. upstream cert **verify** | verify (system CA + SAN) | verify | **`insecure_skip_tls_verify`** |

Verified Envoy mechanics (grounded from protos):
- `FilterChainMatch` precedence is **destination_port → destination IP (`prefix_ranges`) → `server_names`**; `prefix_ranges`/`destination_port` only match "when the listener is bound to 0.0.0.0/:: or when `use_original_dst` is specified." Recovering the original dst after the eBPF redirect (`use_original_dst` / `original_dst` listener filter) is an **eBPF-datapath dependency** — emit the self-secure `prefix_ranges` chain regardless (island rule); reachability is eBPF's gap to close.
- `insecure_skip_tls_verify: true` ⇒ `UpstreamTlsContext.common_tls_context.validation_context.trust_chain_verification: ACCEPT_UNTRUSTED` (enum 1). The `validation_context` block must still be present. Default false ⇒ `VERIFY_TRUST_CHAIN` (current posture: system-CA `trusted_ca` + `auto_san_validation`).

`insecure_skip_tls_verify` is axis 4 and is **orthogonal to IP-ness** — a self-signed FQDN dev host (`local.dev`) uses it too. Schema: `EgressRule.InsecureSkipTLSVerify bool` (`yaml:"insecure_skip_tls_verify"`, default false) in `internal/config/schema.go`. Name keeps the `insecure_` danger signal + explicit `tls_` (clawker's stack has non-TLS verifications, so bare `skip_verify` is ambiguous).

## Implementation status (for handoff)

DONE (golden-tested in `envoy_config_test.go`, validated by `validateBootstrap`):
- http (exact/wildcard, multi-port, path rules, plaintext DFP); https (exact/wildcard, apex dedup, per-SNI chains, reencrypt DFP) + h3/QUIC sibling + alt-svc; OTel-MTLS access-log gating; global deny floor (orchestrator catch-all); **proto-collision fail-closed** (same host:port, different proto → error).
- **IP-literal + self-signed** — all 4 axes (goldens `ip_dst` default-verify + `insecure_skip_verify` FQDN+IP). Axis 1: FQDN→`server_names`+`tls_inspector`, IP→`prefix_ranges`+`destination_port`+`use_original_dst` (`downstreamCryptoMatch` in `envoy_tls.go`, shared by TCP-tls + QUIC). Axis 2: `certs.go` `GenerateDomainCert` mints an iPAddress SAN for IP dsts. Axis 3: `pinnedCluster` is resolution-by-dst-type — `STATIC` for IP, `LOGICAL_DNS` for FQDN. Axis 4: `EgressRule.InsecureSkipTLSVerify` → `validation_context.trust_chain_verification: ACCEPT_UNTRUSTED` (chain trust only; `auto_san_validation` SAN binding retained — stricter than Go's `InsecureSkipVerify`, the safer posture).
- **Opaque `tcp` / `ssh` / `udp`** — goldens `raw_tcp`, `ssh`, `raw_udp`. Each gets a dedicated per-rule listener (TCP at `TCPPortBase+idx`, raw UDP at `UDPPortBase+idx`) → opaque L4 proxy → pinned `LOGICAL_DNS` cluster, NO app block: the pin is the gate. ssh vs raw tcp differ only in the `network.protocol.name` access-log token. `tcp_proxy` (`envoy_tcp.go`) is a chain network filter; `udp_proxy` (`envoy_udp.go`) is a listener filter on a chainless plain-UDP listener (matcher→Route→cluster, grounded in `examples/udp/envoy.yaml` + UdpProxyConfig v3). `buildTCPAccessLog` (hardcoded `action: allowed`, `network.transport` = tcp/udp). Port layout: `TCPMappings`/`UDPMappings` → genFacts `tcpListenerPorts`/`udpListenerPorts`. `EnvoyPorts.UDPPortBase` added through `consts` → config → handler/stack. (FQDN opaque dsts only here; IP/CIDR opaque dsts ride the shared egress listener — see the next bullet. eBPF UDP routing is its own atom, not yet in `RoutesFromRules`.)

- **`ws` / `wss` (websocket) + opaque port-range** — goldens `ws`, `wss`, `ws_wildcard`, `wss_wildcard`, `ws_wss_absorb`, `tcp_port_range`. **ws/wss is an ENRICHMENT of the origin's http/https stack, NOT a separate chain** (see the additive-UX note under "Core invariant"). A `ws`/`wss` rule sets `genCtx.websocket` via `wsEnrichLayer` (prepended by the deriver) on the one http/https stack for its origin: the app block adds per-route `upgrade_configs:[websocket]` + HCM `http2_protocol_options.allow_connect` (and `http3_protocol_options.allow_extended_connect` on the QUIC sibling); the https upstream block pins the reencrypt cluster to http/1.1 (`explicit_http_config`, ALPN `http/1.1`). `canonicalProto` maps `ws→http`, `wss→https` so `https`+`wss` (or `http`+`ws`) COMPOSE on one host:port instead of colliding. The deriver ABSORBS a ws/wss rule into an explicit base rule for the same origin, or PROMOTES it (rewrites to the base proto) when no base rule exists. Wildcard wss uses a distinct `wss_dfp` cluster (h1.1) sharing `https_dfp_cache`. `certs.go` mints the MITM cert for `wss` as for `https`. **Opaque port-range**: `EgressRule.PortRange` (`"lo-hi"`, inclusive) expands an opaque tcp/ssh/udp rule into one self-secure dedicated listener + pinned cluster PER in-range port (mapping A — never `ORIGINAL_DST`); `dedicatedMappings`/`dedicatedPorts` fan the layout out, the eBPF `RoutesFromRules` mirrors the TCP fan-out one-to-one.

- **Opaque `tcp` / `ssh` / `udp` → IP + CIDR** — goldens `opaque_ip_cidr` (tcp IP, tcp CIDR, ssh IP) + `udp_ip`, fail-close row `udp_cidr_unsupported`. The dst-type axis for opaque protos. An IP/CIDR dst is KNOWN at gen time, so opaque tcp/ssh ride the **shared egress listener** as a `prefixRangeTransportLayer` raw_buffer chain (`prefix_ranges` + `destination_port`, `use_original_dst: true`) — NOT a dedicated listener (that is FQDN-opaque only, which has no wire discriminator). **IP → `STATIC` pin** (the address IS the resolution; `opaqueMatchedUpstreamLayer`); **CIDR → `ORIGINAL_DST` / `CLUSTER_PROVIDED`** scoped by the chain's `prefix_ranges` (`originalDstCluster`). UDP has no filter chains, so UDP-IP still gets a dedicated listener (`STATIC` pin) and UDP-CIDR fails closed (`validateProtoDstSupport` — `udp_proxy` cannot forward to the original destination). `dedicatedMappings` gained a `skipDst` predicate: TCP/SSH skip IP+CIDR, UDP skips only CIDR. Zero eBPF changes (`RoutesFromRules` stays FQDN-only). ORIGINAL_DST shape grounded vs Envoy `configs/original-dst-cluster/proxy_config.yaml` + Istio `PassthroughCluster`.

- **http / ws / https / wss → CIDR** — goldens `http_cidr`, `https_cidr`, `ws_cidr`, `wss_cidr`. The L7/TLS half of the dst-type axis (opaque half above; single-IP L7/TLS already covered by `ip_dst`). A CIDR cannot enumerate per-host vhosts, so each rides ONE wildcard-host vhost (`domains: ["*"]`) owning the rule's routes, with **NO `deny_all`** (the chain's `prefix_ranges` IS the boundary) and **NO DFP** (the upstream is a pinned `ORIGINAL_DST`, never Host-resolved). Transport: http/ws reuse `prefixRangeTransportLayer` (the same raw_buffer prefix-ranges chain as opaque — only the app terminal differs, HCM vs tcp_proxy: the transport-first payoff); https/wss reuse `tlsSNIChainLayer` (its `downstreamCryptoMatch` IP branch already returns `prefix_ranges` + `use_original_dst`). Upstream: `httpOriginalDstUpstreamLayer` (plaintext ORIGINAL_DST) / `httpsOriginalDstUpstreamLayer` (reencrypt ORIGINAL_DST, h1.1-pinned for wss). **Range cert**: `certs.go` mints ONE leaf whose iPAddress SAN is the network address — invalid for any single in-range host on purpose ([[feedback_agent_cert_trust_not_load_bearing]]: agent-side verification is not the enforcement boundary; the cert encrypts the hop + enables MITM). `certBasename` folds the CIDR `/` to `_` so the cert is one flat file (`10.0.0.0_24-cert.pem`); both minting and the `DownstreamTlsContext`/QUIC socket refs flow through it. **No gate on `insecure_skip_tls_verify`** — secure by default (Envoy `VERIFY_TRUST_CHAIN` refuses an untrusted upstream, fail-closed); a non-fatal generation WARNING is the UX nudge. **TCP-only** — no QUIC/h3 sibling for a range (see next bullet).

  **ORIGINAL_DST carve-out (host-validated vs range-validated):** `ORIGINAL_DST` is forbidden for a *host*-validated flow (FQDN/single-IP — forwarding to the arriving dst would be Envoy delegating host enforcement to the datapath = confused deputy). It is CORRECT for a *range*-validated flow: the chain's `prefix_ranges` already authorized the whole range, so forwarding to the in-range arriving dst is honoring the grant, not trusting the client.

PENDING (next work, each its own token/pass):
- **IP/CIDR-over-h3 `use_original_dst` on the QUIC listener — UNVERIFIED.** `quicSNIChainLayer` emits a `prefix_ranges` + `use_original_dst` IP chain on the UDP/QUIC listener for atom completeness, but original-dst recovery / filter-chain matching on a QUIC listener is not grounded in Envoy source (docs are TCP-framed). For this reason CIDR https/wss is deliberately **TCP-only** (no QUIC sibling emitted). Fail-closed if unsupported. Either ground it (QUIC listener original-dst support) or formally make the IP/CIDR-https QUIC sibling no-emit (the one sanctioned per-dst-type carve-out, documented).
- **`otel_collector_als` cluster** — `otel_mtls` golden's access-log sink references this cluster but the layered generator never emits it (dangling ref; `validateBootstrap` can't catch cross-refs).

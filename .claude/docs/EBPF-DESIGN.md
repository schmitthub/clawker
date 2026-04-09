# eBPF Traffic Routing Design

> Replaces iptables-based traffic routing with eBPF cgroup hooks for per-container, DNS-aware egress enforcement.

## Status: Design Draft

## Problem

The current firewall uses iptables DNAT rules inside agent containers to redirect traffic to Envoy. This has fundamental limitations:

1. **Port-only matching**: iptables can only match on destination port, not domain. Two SSH rules for different Git providers (github.com:22, gitlab.com:22) are indistinguishable — first DNAT rule wins.
2. **Global ordering**: iptables rules are ordered lists. When multiple projects define SSH rules for different providers, only the first rule's Envoy listener receives traffic.
3. **In-container execution**: `firewall.sh` runs inside each container via `docker exec`, requiring iptables packages, `CAP_NET_ADMIN`, and `CAP_NET_RAW` capabilities.
4. **Port instability**: Envoy listener port assignments are positional (`TCPPortBase + idx`). Adding/removing rules shifts ports, breaking running containers' stale iptables.

## Solution

Replace all iptables rules with eBPF cgroup programs attached per-container from outside. eBPF intercepts `connect()` and `sendmsg()` syscalls at the cgroup level, rewriting destinations to the correct Envoy/CoreDNS endpoints.

### What Changes

| Component | Before (iptables) | After (eBPF) |
|---|---|---|
| Traffic routing | `firewall.sh` iptables DNAT inside container | `cgroup/connect4` BPF program attached to container cgroup from outside |
| DNS redirect | iptables UDP DNAT + resolv.conf rewrite | `cgroup/sendmsg4` BPF program redirects all UDP:53 |
| TCP domain routing | Port-only matching (can't distinguish domains on same port) | IP-based matching via DNS resolution cache in BPF maps |
| IPv6 blocking | ip6tables DROP rules | `cgroup/connect6` + `cgroup/sendmsg6` deny |
| ICMP blocking | iptables ICMP DROP | `cgroup/sock_create` blocks SOCK_RAW |
| Gateway lockdown | iptables per-IP rules | BPF program checks dst IP against gateway |
| Per-container bypass | Flush/re-add iptables rules | Set `bypass_map[cgroup_id] = 1` (atomic, instant) |
| SNAT for attribution | iptables POSTROUTING SNAT (cross-bridge fix) | Not needed — connect() rewrite picks correct source interface natively |
| resolv.conf modification | sed rewrite inside container | Not needed — all UDP:53 redirected regardless of resolv.conf |
| Container capabilities | `CAP_NET_ADMIN` + `CAP_NET_RAW` | None needed |
| Container packages | iptables, ip6tables | None needed |

### What Stays the Same

| Component | Role |
|---|---|
| **Envoy** | TLS termination, MITM inspection, HTTP path filtering, SNI matching, access logging, per-domain TCP listeners |
| **CoreDNS** | Domain allowlist gate (NXDOMAIN for unlisted domains), DNS logging |
| **Certificate PKI** | CA generation, per-domain MITM certs |
| **Rules store** | `storage.Store[EgressRulesFile]` for persistent rule state |
| **Daemon** | Health probes, container watcher, lifecycle management |
| **FirewallManager interface** | Enable/Disable/Bypass/AddRules/RemoveRules — same contract, different implementation |

## Architecture

### Container Topology

```
┌─────────────────────────────────────────────────────────────┐
│  Docker Host / Docker Desktop VM                             │
│                                                              │
│  clawker-net (bridge network)                                │
│  ┌──────────────────────────────────────────────────────┐   │
│  │                                                       │   │
│  │  ┌─────────────┐  ┌──────────┐  ┌────────────────┐  │   │
│  │  │   Envoy      │  │ CoreDNS  │  │  eBPF Manager  │  │   │
│  │  │   .2          │  │  .3      │  │    .4          │  │   │
│  │  │              │  │          │  │                │  │   │
│  │  │ TLS inspect  │  │ DNS gate │  │ BPF loader     │  │   │
│  │  │ HTTP filter  │  │ NXDOMAIN │  │ Map manager    │  │   │
│  │  │ TCP proxy    │  │ logging  │  │ DNS→IP cache   │  │   │
│  │  │ Access logs  │  │          │  │ Cgroup attach  │  │   │
│  │  └──────────────┘  └──────────┘  └────────────────┘  │   │
│  │         ▲                ▲               │            │   │
│  │         │                │               │ attach     │   │
│  │         │   eBPF rewrites connect()      │ per-cgroup │   │
│  │         │   to Envoy/CoreDNS IPs         ▼            │   │
│  │  ┌──────────────┐  ┌──────────────┐                   │   │
│  │  │ Agent: dev    │  │ Agent: ops   │  ...              │   │
│  │  │ (no iptables) │  │ (no iptables)│                   │   │
│  │  │ (no caps)     │  │ (no caps)    │                   │   │
│  │  └──────────────┘  └──────────────┘                   │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### eBPF Manager Container

A new managed container alongside Envoy and CoreDNS. Privileged, with access to the host cgroup and BPF filesystems.

**Container spec:**
- Image: minimal Go binary (built from `internal/ebpf/cmd/`)
- Capabilities: `CAP_BPF`, `CAP_SYS_ADMIN`, `CAP_NET_ADMIN`
- Mounts: `/sys/fs/cgroup` (ro), `/sys/fs/bpf` (rw)
- Labels: `dev.clawker.purpose=firewall`, `dev.clawker.firewall.role=ebpf`
- Static IP: `.4` on clawker-net

**Responsibilities:**
1. Load BPF programs once at startup
2. Attach/detach programs to agent container cgroups on Enable/Disable
3. Maintain BPF maps with per-container routing entries
4. Expose a control interface (via docker exec or mounted socket) for the firewall manager

### CoreDNS Plugin: dns-to-bpf

A custom CoreDNS plugin that intercepts DNS responses and populates a shared BPF map with IP→domain mappings. This is the same architectural pattern as Cilium (userspace DNS parsing → BPF maps) but simpler because CoreDNS is already in the stack.

**How it works:**
1. CoreDNS resolves a query (e.g., `github.com` → `140.82.121.4`)
2. The `dns-to-bpf` plugin intercepts the response
3. It writes `{140.82.121.4} → {domain: "github.com", ttl: 300}` to a pinned BPF map at `/sys/fs/bpf/clawker/dns_cache`
4. The `cgroup/connect4` program reads this map to determine which Envoy listener to route to

**Shared BPF map mount:** CoreDNS and the eBPF Manager share `/sys/fs/bpf/clawker/` via a Docker volume or bind mount from the host BPF filesystem. The eBPF Manager creates and pins the maps; CoreDNS writes to the DNS cache map; the BPF programs read from all maps.

**Implementation:** ~200 lines of Go using `cilium/ebpf` for map access. Built as a CoreDNS plugin (CoreDNS has a well-defined plugin interface). Ships in the clawker CoreDNS image.

**TTL handling:** Entries include TTL from the DNS response. The eBPF Manager runs a periodic garbage collector (every 60s) that removes expired entries. Alternatively, use map-in-map atomic swap on each resolution cycle.

### Why Not Snoop DNS in BPF?

DNS response parsing in BPF is not viable. DNS is variable-length with compression pointers, multi-answer responses, and EDNS0 extensions. BPF's constrained instruction set (bounded loops, limited stack) makes parsing brittle. Cilium explicitly chose userspace DNS parsing for this reason.

## BPF Programs

Five BPF programs, loaded once, attached per-container:

### 1. `cgroup/connect4` — TCP/UDP IPv4 Routing

The primary routing program. Intercepts all `connect()` syscalls from processes in the attached cgroup.

```
connect(dst_ip, dst_port)
  │
  ├─ uid == 0? → ALLOW (root bypass)
  ├─ bypass_map[cgroup_id]? → ALLOW (temporary bypass)
  ├─ container_map[cgroup_id] missing? → ALLOW (unmanaged container)
  │
  ├─ dst is loopback? → ALLOW
  ├─ dst in clawker-net CIDR? → ALLOW (intra-network)
  ├─ dst == host_proxy_ip:port? → ALLOW
  ├─ dst == gateway_ip?
  │   ├─ dst_port == host_proxy_port? → ALLOW
  │   └─ else → REWRITE to envoy_ip:egress_port (denied by Envoy)
  │
  ├─ dst_port == 53 (DNS)? → REWRITE to coredns_ip:53
  │
  ├─ dns_cache[dst_ip] exists?
  │   ├─ route_map[cgroup_id, domain_hash, dst_port] exists?
  │   │   └─ REWRITE to envoy_ip:specific_envoy_port
  │   └─ no specific route → REWRITE to envoy_ip:egress_port (catch-all)
  │
  └─ no DNS cache entry → REWRITE to envoy_ip:egress_port (catch-all TLS/SNI)
```

### 2. `cgroup/sendmsg4` — UDP IPv4 (DNS + Block)

Intercepts `sendto()`/`sendmsg()` for UDP traffic.

```
sendmsg(dst_ip, dst_port)
  │
  ├─ uid == 0? → ALLOW
  ├─ bypass_map[cgroup_id]? → ALLOW
  ├─ dst_port == 53? → REWRITE to coredns_ip:53
  ├─ dst in clawker-net CIDR? → ALLOW
  └─ else → DENY (drop non-DNS UDP)
```

### 3. `cgroup/connect6` — IPv6 TCP Deny

```
connect6(dst_ip6, dst_port)
  │
  ├─ uid == 0? → ALLOW
  ├─ bypass_map[cgroup_id]? → ALLOW
  ├─ dst is ::1? → ALLOW
  └─ else → DENY
```

### 4. `cgroup/sendmsg6` — IPv6 UDP Deny

Same logic as connect6 but for UDP sendmsg.

### 5. `cgroup/sock_create` — Raw Socket Blocking

```
sock_create(type, protocol)
  │
  ├─ uid == 0? → ALLOW
  ├─ type == SOCK_RAW? → DENY (prevents ICMP tunneling)
  └─ else → ALLOW
```

## BPF Maps

All maps are pinned to `/sys/fs/bpf/clawker/` for cross-container sharing.

### `container_map` — Per-Container Configuration

```
Type: BPF_MAP_TYPE_HASH
Key:   u64 cgroup_id
Value: struct {
    u32 envoy_ip;          // Envoy static IP (network byte order)
    u32 coredns_ip;        // CoreDNS static IP
    u32 gateway_ip;        // clawker-net gateway IP
    u32 net_addr;          // clawker-net network address
    u32 net_mask;          // clawker-net subnet mask
    u32 host_proxy_ip;     // Host proxy IP (resolved)
    u16 host_proxy_port;   // Host proxy port
    u16 egress_port;       // Envoy egress listener port (e.g., 10000)
}
```

Populated by the eBPF Manager when `Enable()` is called. Removed on `Disable()`.

### `bypass_map` — Temporary Bypass Flags

```
Type: BPF_MAP_TYPE_HASH
Key:   u64 cgroup_id
Value: u8 (1 = bypassed)
```

Set on `Bypass()`, deleted on re-enable. Timer managed by eBPF Manager.

### `dns_cache` — DNS Resolution Cache

```
Type: BPF_MAP_TYPE_HASH
Key:   u32 ip_addr (network byte order)
Value: struct {
    u32 domain_hash;   // FNV-1a hash of normalized domain
    u32 expire_ts;     // Expiration timestamp (kernel monotonic seconds)
}
```

Written by the CoreDNS `dns-to-bpf` plugin on every DNS response. Read by `cgroup/connect4` to resolve IP → domain for per-domain TCP routing. The eBPF Manager garbage-collects expired entries.

### `route_map` — Per-Container, Per-Domain TCP Routes

```
Type: BPF_MAP_TYPE_HASH
Key:   struct {
    u64 cgroup_id;
    u32 domain_hash;   // FNV-1a of domain (matches dns_cache value)
    u16 dst_port;      // Original destination port
}
Value: struct {
    u16 envoy_port;    // Target Envoy TCP listener port
}
```

Populated by the eBPF Manager from the rules store. When a container has a specific TCP route for a domain+port, traffic is routed to that domain's dedicated Envoy listener instead of the catch-all.

## DNS-Aware TCP Routing Flow

This is how eBPF solves the "two SSH providers on port 22" problem:

```
1. Agent container: git clone git@github.com:user/repo.git
2. SSH client resolves github.com → asks CoreDNS
3. CoreDNS: github.com is allowed → resolves → 140.82.121.4
4. CoreDNS dns-to-bpf plugin: writes dns_cache[140.82.121.4] = {domain_hash: FNV("github.com"), ttl: 300}
5. SSH client: connect(140.82.121.4, 22)
6. BPF connect4: looks up dns_cache[140.82.121.4] → domain_hash = FNV("github.com")
7. BPF connect4: looks up route_map[{cgroup_id, FNV("github.com"), 22}] → envoy_port = 10047
8. BPF connect4: rewrites dst to envoy_ip:10047
9. Envoy listener 10047: proxies to github.com:22
```

Meanwhile, a different agent doing `git clone git@gitlab.com:...`:
```
3. CoreDNS: gitlab.com → 172.65.251.78
4. dns_cache[172.65.251.78] = {domain_hash: FNV("gitlab.com")}
7. route_map[{cgroup_id_B, FNV("gitlab.com"), 22}] → envoy_port = 10083
8. rewrites to envoy_ip:10083 (different Envoy listener)
```

Both containers route port 22 to different Envoy listeners based on the actual resolved IP. No ordering conflict. No priority hacks.

## Envoy Port Assignment

With DNS-aware routing, Envoy's TCP port assignment model changes:

**Before**: Positional (`TCPPortBase + idx`). Fragile — adding/removing rules shifts ports.

**After**: Content-addressed (`TCPPortBase + FNV32(domain:proto:port) % range`). Stable — same rule always gets the same port regardless of store ordering. Linear probing for hash collisions.

This is the content-addressed port assignment from the original branch plan, and it still makes sense — it stabilizes Envoy listener ports so BPF map entries don't need updating when unrelated rules change.

## Container Lifecycle

### Enable (container start)

```
Manager.Enable(ctx, containerID):
  1. Look up container's cgroup path via Docker API
  2. Get container's cgroup ID from /proc/<pid>/cgroup
  3. Compute routing entries from rules store + project config
  4. docker exec ebpf-manager: populate container_map[cgroup_id] with network config
  5. docker exec ebpf-manager: populate route_map entries for this container's TCP rules
  6. docker exec ebpf-manager: attach BPF programs to container's cgroup (if not already attached)
  7. Touch firewall-ready signal file in container
```

### Disable (container stop / firewall disable)

```
Manager.Disable(ctx, containerID):
  1. docker exec ebpf-manager: delete container_map[cgroup_id]
  2. docker exec ebpf-manager: delete all route_map entries for this cgroup_id
  3. docker exec ebpf-manager: detach BPF programs from container's cgroup
```

### Bypass (temporary unrestricted egress)

```
Manager.Bypass(ctx, containerID, timeout):
  1. docker exec ebpf-manager: set bypass_map[cgroup_id] = 1
  2. Schedule re-enable after timeout:
     docker exec ebpf-manager: delete bypass_map[cgroup_id]
```

Bypass is now atomic and instant. No iptables flush/re-add race. No shell script execution inside the agent container.

## Entrypoint Changes

The container entrypoint simplifies significantly:

**Removed:**
- `firewall.sh` wait loop (replaced by BPF attachment signal)
- resolv.conf rewriting (all DNS redirected by BPF)
- CAP_NET_ADMIN / CAP_NET_RAW requirements

**Kept:**
- Firewall-ready signal file wait (eBPF Manager touches it after attachment)
- Docker socket permissions
- User-level init (config, git, ssh, post-init)
- Privilege drop via gosu

**Container image changes:**
- Remove `iptables`, `ip6tables` packages from Dockerfile
- Remove `firewall.sh` from bundler assets
- Remove `CAP_NET_ADMIN`, `CAP_NET_RAW` from container capabilities

## Package Structure

```
internal/
  ebpf/                     # NEW: eBPF management package
    ebpf.go                 # Manager interface, types
    loader.go               # BPF program loading, map creation, pinning
    attach.go               # Cgroup discovery, program attachment/detachment
    maps.go                 # BPF map CRUD operations (container, route, bypass, dns_cache)
    manager.go              # Docker implementation (manages eBPF container)
    bpf/                    # BPF C programs
      connect4.c            # cgroup/connect4 — main routing program
      sendmsg4.c            # cgroup/sendmsg4 — DNS redirect + UDP blocking
      connect6.c            # cgroup/connect6 — IPv6 deny
      sendmsg6.c            # cgroup/sendmsg6 — IPv6 UDP deny
      sock_create.c         # cgroup/sock_create — raw socket blocking
      common.h              # Shared structs, map definitions
    cmd/                    # eBPF manager container binary
      main.go               # Control loop: accepts commands, manages maps
    mocks/                  # Test doubles
  firewall/
    manager.go              # Updated: Enable/Disable/Bypass use eBPF instead of docker exec firewall.sh
    
pkg/
  coredns-plugin/           # NEW: CoreDNS dns-to-bpf plugin
    plugin.go               # CoreDNS plugin interface implementation
    handler.go              # Response interception, A/AAAA extraction
    bpfmap.go               # BPF map writer (dns_cache)
```

**Build tooling:**
- `bpf2go` (from `cilium/ebpf/cmd/bpf2go`) generates Go bindings from BPF C at build time
- BPF C compiled with clang/LLVM (available in CI, `go generate` directive)
- CO-RE (Compile Once, Run Everywhere) via BTF — single binary works across kernel versions

## Compatibility and Fallback

### Runtime Detection

```go
func SupportsEBPF() bool {
    // 1. Check cgroup v2
    if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
        return false
    }
    // 2. Probe BPF program type
    if err := features.HaveProgramType(ebpf.CGroupSockAddr); err != nil {
        return false
    }
    // 3. Check BTF availability (optional, for CO-RE)
    if _, err := btf.LoadKernelSpec(); err != nil {
        log.Warn("BTF not available, using non-CO-RE BPF programs")
    }
    return true
}
```

### Fallback to iptables

If eBPF is not supported (old kernel, cgroup v1, missing BPF config):
1. Log warning: "eBPF not available, falling back to iptables"
2. Use existing `firewall.sh` approach (preserved as legacy path)
3. Container images include iptables packages (conditional on fallback)

**Supported matrix:**

| Environment | eBPF | Fallback |
|---|---|---|
| Docker Desktop (macOS/Windows) | Yes (kernel 6.12, cgroup v2) | N/A |
| Ubuntu 22.04+ / Debian 11+ / Fedora 38+ | Yes | N/A |
| RHEL 9 / Amazon Linux 2023 | Yes | N/A |
| RHEL 8 / Amazon Linux 2 / Ubuntu 20.04 | No (cgroup v1) | iptables |

### Migration Path

1. **Phase 1**: Build eBPF manager container + BPF programs. Add `SupportsEBPF()` detection. Wire into firewall manager with feature flag.
2. **Phase 2**: Build CoreDNS dns-to-bpf plugin. Enable DNS-aware routing.
3. **Phase 3**: Remove iptables from default container images. Keep as opt-in fallback.
4. **Phase 4**: Remove iptables fallback when minimum supported kernel is cgroup v2 only.

## Dependencies

| Dependency | Purpose | Version Pinning |
|---|---|---|
| `github.com/cilium/ebpf` | Go eBPF library (program loading, map management) | Exact version in go.mod |
| `clang` / `llvm` | BPF C compiler (build-time only) | CI pinned version |
| `bpf2go` | Go code generation from BPF C | `go install` pinned version |

## Security Considerations

1. **eBPF Manager privileges**: The eBPF manager container runs with `CAP_BPF`, `CAP_SYS_ADMIN`, `CAP_NET_ADMIN`. This is a privileged component — it can attach BPF programs to any cgroup on the host. Mitigated by: Docker label isolation, no network exposure, no user interaction.

2. **BPF verifier**: All BPF programs pass through the kernel's BPF verifier before loading. The verifier guarantees: bounded execution time, no arbitrary memory access, no kernel crashes. This is the fundamental safety guarantee of eBPF.

3. **Pinned maps**: BPF maps are pinned to `/sys/fs/bpf/clawker/`. Only the eBPF Manager and CoreDNS (via shared mount) have write access. Agent containers have no access to BPF maps.

4. **DNS cache poisoning**: If an attacker inside a container could somehow write to the dns_cache BPF map, they could redirect their own traffic. Mitigated by: map access restricted to eBPF Manager and CoreDNS containers; agent containers have no BPF filesystem mount.

5. **Cgroup escape**: If an attacker could change their cgroup ID, they could bypass routing. Mitigated by: cgroup assignment is kernel-enforced; requires `CAP_SYS_ADMIN` which agent containers don't have.

## Observability

eBPF transforms monitoring from log-parsing to kernel-native metrics. Every `connect()`, `sendmsg()`, and denied connection is visible per-container, per-domain, in real time.

### Current Monitoring Limitations (iptables)

| Problem | Root Cause |
|---|---|
| SNAT attribution hack (25 lines in firewall.sh) | Cross-bridge DNAT loses source IP; SNAT rewrites it so Envoy logs show the real container |
| No per-container connection metrics | iptables counters are global rule counters, not per-UID or per-container |
| Blocked connections are invisible | iptables DROP rules silently discard — no logging, no feedback to users |
| DNS queries unattributed | CoreDNS sees container IPs but can't tie them to agent names without the SNAT hack |
| Log-based pipeline fragility | Promtail → regex parse → Loki → Grafana. One format change breaks the dashboard |

### eBPF Observability Architecture

```
BPF Programs (kernel)
  │
  ├─ Per-map counters (atomic u64 increments, zero overhead)
  │   ├─ connections_total{cgroup_id, domain_hash, dst_port, action}
  │   ├─ bytes_total{cgroup_id, domain_hash, direction}
  │   ├─ dns_queries_total{cgroup_id}
  │   └─ denied_total{cgroup_id, reason}
  │
  └─ BPF ringbuf (event stream for denied connections)
      └─ {timestamp, cgroup_id, dst_ip, dst_port, action, reason}

eBPF Manager Container
  │
  ├─ Prometheus /metrics endpoint
  │   └─ Reads BPF counter maps, enriches with agent name from cgroup_id
  │       → connections_total{agent="dev", domain="github.com", port="22", action="allow"}
  │       → denied_total{agent="ops", reason="ipv6_blocked"}
  │
  └─ Ringbuf consumer → Loki push
      → {agent: "dev", domain: "evil.com", port: 443, action: "denied", reason: "domain_not_resolved"}
```

### New Metrics (Not Possible with iptables)

| Metric | Type | Labels | Source |
|---|---|---|---|
| `clawker_connections_total` | Counter | agent, domain, port, action | connect4 per-map counter |
| `clawker_connection_duration_seconds` | Histogram | agent, domain | connect4 timestamp pairs |
| `clawker_bytes_total` | Counter | agent, domain, direction | Socket-level BPF counters |
| `clawker_dns_queries_total` | Counter | agent, domain | sendmsg4 per-map counter |
| `clawker_denied_total` | Counter | agent, reason | All deny paths in BPF programs |
| `clawker_bypass_active` | Gauge | agent | bypass_map membership |

### What Gets Eliminated

1. **SNAT rules** — `connect()` rewrite picks the correct source interface natively. Envoy sees the container's clawker-net IP without any NAT tricks. The 25-line SNAT section of firewall.sh and the `emit_agent_identity()` Loki push hack both go away.

2. **Promtail regex pipelines** — Envoy access logs still exist (and still go through Promtail for HTTP-level detail), but the primary connection metrics come from BPF counters via Prometheus. No fragile regex parsing for the core metrics.

3. **Agent identity push** — Currently `firewall.sh` pushes `{agent, client_ip}` pairs to Loki so Grafana can join Envoy logs with agent names. With BPF counter maps, the eBPF Manager already knows `cgroup_id → agent name` and labels metrics directly. No join needed.

### Denied Connection Feedback

The BPF ringbuf enables actionable user feedback. When a connection is denied, the eBPF Manager can:

1. Push a structured log to Loki: `{agent: "dev", dst: "evil.com:443", reason: "domain_not_resolved"}`
2. Surface it in `clawker firewall status`: "agent-dev: 3 blocked connections in last 5m (evil.com:443, sketchy.io:22)"
3. Suggest fixes: "Run `clawker firewall add evil.com` to allow this domain"

This replaces the current experience where blocked connections silently time out and the user has no idea why.

### Monitoring Deletion Inventory

The eBPF migration eliminates the entire IP-based agent attribution pipeline. Here is every component that gets deleted or rewritten:

#### Deleted Entirely

| File / Symbol | Lines | What It Does | Why It's Gone |
|---|---|---|---|
| `firewall.sh` lines 101-128 (`emit_agent_identity()`) | 28 | Bash function that pushes `{agent, client_ip}` to Loki HTTP API from inside the container | eBPF Manager knows cgroup_id → agent directly |
| `firewall.sh` lines 140-164 (SNAT section) | 25 | Cross-bridge SNAT to preserve container source IP in Envoy logs | connect() rewrite picks correct source interface natively |
| `firewall.sh` lines 162-163 (SNAT POSTROUTING rules) | 2 | `iptables -t nat -A POSTROUTING` SNAT rules per-container | No NAT needed |
| `manager.go:522-593` (`emitAgentMapping()`) | 72 | Go function that pushes `{agent, client_ip}` JSON to Loki HTTP API on Enable/Disable | eBPF Manager labels metrics with agent name directly |
| `manager.go:421` (`emitAgentMapping` call in Disable) | 1 | Calls emitAgentMapping on disable | Deleted with function |
| `manager.go:499` (`emitAgentMapping` call in Enable) | 1 | Calls emitAgentMapping on enable | Deleted with function |
| `firewall.sh` lines 277-279 (`emit_agent_identity` call) | 1 | Calls emit_agent_identity at end of enable_firewall | Deleted with function |
| Grafana `$agent_ips` variable (dashboard.json:2494-2502) | 8 | `label_values({source="agent_map"}, client_ip)` — resolves agent→IP from Loki | Replaced by Prometheus `agent` label |

#### Rewritten

| Component | Current | After eBPF |
|---|---|---|
| Grafana dashboard panels (all Envoy/CoreDNS panels) | Filter by `client_ip=~"${agent_ips:regex}"` (IP-based join via agent_map) | Filter by `agent=~"${agent:regex}"` (direct Prometheus label) |
| Grafana dashboard variables | `$agent_ips` hidden variable (Loki query on agent_map) | `$agent` selector (Prometheus label values from BPF metrics) |
| Promtail `client_ip` label extraction | Required for IP-based agent join | Optional — Envoy logs still have it for debugging, but not used for attribution |
| Envoy access log `client_ip` field | Critical — only way to attribute traffic to agents | Informational — BPF metrics are the primary attribution source |
| CoreDNS log `client_ip` field | Critical — only way to attribute DNS queries | Informational — BPF DNS counters attribute per-container |

#### Simplified

| Component | Before | After |
|---|---|---|
| `firewall.sh` | 306 lines (iptables + SNAT + DNS rewrite + IPv6 + ICMP + gateway + agent identity) | **Deleted** — eBPF handles everything from outside |
| `manager.go` Enable() | docker exec firewall.sh + emitAgentMapping + FormatPortMappings | docker exec ebpf-manager: populate maps + attach cgroup |
| `manager.go` Disable() | docker exec firewall.sh disable + emitAgentMapping | docker exec ebpf-manager: clear maps + detach cgroup |
| `manager.go` Bypass() | Disable + scheduled sleep + re-Enable (shell script in container) | Set bypass_map flag + timer in eBPF Manager |
| Container image | iptables packages + firewall.sh + CAP_NET_ADMIN + CAP_NET_RAW | No firewall packages, no capabilities, no scripts |
| `entrypoint.sh` firewall wait | Wait for firewall-ready signal (iptables applied post-start) | Wait for firewall-ready signal (BPF attached post-start) — same pattern, simpler backing |

### Grafana Dashboard Changes

The existing egress dashboard (`grafana.internal/d/api-latency`) currently relies on:
- Envoy access logs (JSON → Promtail → Loki) for connection data
- CoreDNS logs (regex → Promtail → Loki) for DNS data
- Agent identity Loki streams for IP→agent joins

With eBPF, the dashboard adds a Prometheus data source for BPF counter metrics:
- **Per-agent connection rates** — `rate(clawker_connections_total{agent="dev"}[5m])`
- **Per-agent denied connections** — `rate(clawker_denied_total{agent="dev"}[5m])`
- **Top domains by agent** — `topk(10, clawker_connections_total) by (agent, domain)`
- **Bypass status** — `clawker_bypass_active == 1`

Envoy access logs remain for HTTP-level detail (path, response code, duration). BPF metrics provide the connection-level overview that was previously impossible.

## Open Questions

1. **eBPF Manager communication**: Docker exec vs. mounted Unix socket vs. gRPC? Docker exec is simplest (matches Envoy/CoreDNS pattern) but adds exec overhead per operation. Unix socket is lower latency for frequent map updates.

2. **CoreDNS image**: Build custom CoreDNS image with dns-to-bpf plugin, or use the plugin's external plugin mechanism? Custom image is simpler but requires maintaining a CoreDNS fork.

3. **Map sizing**: How large should each BPF map be? `container_map` = max concurrent containers (~100). `route_map` = containers * TCP rules per container (~5000). `dns_cache` = unique resolved IPs (~10000). `bypass_map` = max concurrent containers (~100).

4. **Envoy per-domain TCP listeners**: With DNS-aware routing, each domain+port combo gets a dedicated Envoy listener. This is the same as today but now actually reachable per-container. Should Envoy listener count be bounded?

5. **Wildcard domains**: Wildcard rules (`.example.com`) match any subdomain. The dns_cache stores exact IPs. The CoreDNS plugin needs to recognize which wildcard rule a resolution falls under and tag the dns_cache entry with the wildcard's domain hash.

# Envoy + CoreDNS Firewall Design Spec

**Date:** 2026-03-17
**Branch:** `feat/global-firewall`
**Status:** Approved
**Replaces:** IP-based firewall (`init-firewall.sh` with ipset/iptables/API fetches)

---

## Overview

Replace clawker's per-container IP-based firewall with a shared Envoy + CoreDNS architecture on `clawker-net`. Envoy handles TLS SNI passthrough and MITM path inspection. CoreDNS handles DNS whitelisting. Agent containers use iptables DNAT to route traffic through Envoy and DNS through CoreDNS.

## Architecture

```
Host
├── clawker CLI → FirewallManager (Docker impl, v1)
│                    │
│               egress-rules.yaml (config dir, flock-protected)
│                    │
│               Config Generators
│               ├── envoy.yaml
│               ├── Corefile
│               └── certs/ (CA + per-domain MITM certs)
│
└── clawker-net (Docker network, Docker-assigned subnet)
    ├── clawker-envoy     (static IP, :10000 TLS, :10001+ TCP)
    ├── clawker-coredns   (static IP, :53 DNS, :8080 health)
    └── agent containers  (iptables DNAT → Envoy, --dns → CoreDNS)
```

**Data flow:**
1. Project configs + required domains → `[]EgressRule` (union policy)
2. `Update()` diffs incoming rules against `egress-rules.yaml`, appends new rules
3. Config generators produce `envoy.yaml` + `Corefile` from the full rule set
4. Configs bind-mounted into containers; Envoy restarted, CoreDNS auto-reloads
5. Agent containers: iptables DNAT redirects non-root TCP → Envoy, `--dns` → CoreDNS
6. Allowed domain: CoreDNS resolves → Envoy passes through (or MITM inspects) → success
7. Blocked domain: CoreDNS returns NXDOMAIN and/or Envoy resets connection

---

## Confirmed Design Decisions

These decisions are final. Implementing agents MUST NOT revisit or deviate from them.

### 1. Union Egress Policy
A single merged rule set across all projects. Trust is orthogonal to need — if a domain is trusted for one project, it's trusted for all. Per-project isolation is deferred (door open via source-IP Envoy policy, not needed v1).

### 2. Envoy Config Reload via Container Restart
Envoy does NOT support SIGHUP for config reload. It requires hot restart via `--restart-epoch`. For v1, use `docker restart clawker-envoy`. Sub-second, acceptable for local dev. Do NOT implement xDS control plane or `hot-restarter.py`.

### 3. CoreDNS Config Reload via `reload` Plugin
CoreDNS watches the Corefile via SHA512 checksum every 30s. Write the file, CoreDNS picks it up. No signal, no restart needed.

### 4. Docker-Assigned Subnet
Let Docker pick the subnet for `clawker-net`. Inspect the network to get the gateway IP, compute static IPs for Envoy and CoreDNS relative to it. Do NOT hardcode a subnet — dev machines have competing Docker networks (Compose stacks, Kind clusters, etc.) that can collide.

### 5. MITM Path Inspection Is In Scope
Not deferred. The full pipeline is v1: CA management, per-domain certs, Envoy MITM filter chains with HTTP route matching, path allow/deny rules. This is a core security feature — the threat model is prompt injection via UGC-hosting domains (storage.googleapis.com, github.com) that must be allowed for packages but are exfiltration vectors.

### 6. Path Rules Follow Standard Firewall Pattern
Ordered rules, first match wins, explicit default action:

```go
type PathRule struct {
    Path   string `yaml:"path"`   // "/v1/models", "/uploads/*"
    Action string `yaml:"action"` // allow, deny
}
```

`PathDefault` (allow or deny) is required when `PathRules` is present. No implicit defaults.

Presence of `PathRules` on an `EgressRule` implies MITM inspection. There is NO separate `Inspect` bool — it would create confusing states where paths are set but inspection is disabled.

### 7. Always Install dante-server + proxychains4
Base packages in every clawker image (Debian + Alpine). Small footprint, bypass must "just work."

### 8. Persist by Default
`clawker firewall add <domain>` writes to the egress rules file. No session-scoped rules, no in-memory-only state. Removal is deliberate: `clawker firewall remove`.

### 9. NET_ADMIN Already Granted
Agent containers already have NET_ADMIN for the existing iptables firewall. The bypass escape hatch needs no new privileges.

### 10. Docker Restart Policy + Hostproxy Lifecycle
Envoy and CoreDNS containers use `--restart unless-stopped`. Docker handles crash recovery. The hostproxy daemon manages lifecycle bookkeeping: starts Envoy + CoreDNS containers on daemon startup (if firewall enabled), stops them on daemon teardown (when no agent containers running for 60s grace period). This piggybacks on the existing hostproxy auto-start/auto-stop mechanism — no new daemon or lifecycle manager needed. The firewall manager's `EnsureRunning()` handles health checks and config correctness, called during container creation.

**Hostproxy already has Docker.** The daemon creates a raw moby `client.Client` internally and polls `ContainerList` via a `ContainerLister` interface. Extending the daemon to start/stop Envoy+CoreDNS containers on startup/teardown requires no new imports — just additional calls on the existing Docker client. The `ContainerLister` interface may need to be extended or a broader interface used for container lifecycle operations.

### 11. Agent Prompt via `/etc/claude-code/CLAUDE.md`
Static skill document baked into the image at build time via the Dockerfile template. Teaches the agent how to operate in a clawker container: firewall awareness, troubleshooting, bypass instructions. List-agnostic — does NOT include specific domains or rules. Will grow over time to cover other clawker capabilities (hostproxy, socket bridge, monitoring, etc.). Uses Claude Code's managed policy location for Linux.

### 12. CA Cert Baked Into Image at Build Time
CA cert is copied into the Dockerfile build context and installed via `update-ca-certificates`. No runtime injection. `clawker firewall rotate-ca` writes new CA to disk and warns: "This won't go into effect until Envoy is rebuilt. All containers must be rebuilt as well."

### 13. Failsafe Killswitch
`clawker firewall disable` flushes DNAT rules and restores DNS in agent containers via `docker exec`. `clawker firewall enable` re-applies them. Deliberate user action, not automatic. Does NOT auto-disable on Envoy crash — that would create a security bypass triggered by crashing Envoy (fail closed, not fail open). Future clawkerd daemon will handle proper health monitoring.

### 14. Cross-Process Locking
`egress-rules.yaml` is protected by `flock` via `storage.Store[T]` with `WithLock()`. The lock covers the full read-diff-write cycle to prevent lost updates from concurrent `clawker run` invocations.

### 15. No Config Hash Caching
`Update()` diffs incoming rules against the current set. If no new rules, return early (no write, no regeneration). If rules changed, always regenerate. No hash comparison, no cached hashes.

### 16. No In-Memory State Caching
Each `clawker` CLI invocation is a separate process. Envoy/CoreDNS IPs and network info are discovered via Docker API calls on every invocation. No persistence of runtime values — just ask Docker.

---

## Explicitly Rejected Ideas

Implementing agents MUST NOT introduce these. They were considered and rejected for the stated reasons.

| Rejected Idea | Reason |
|---|---|
| xDS control plane for Envoy | Overkill for local dev tool. Container restart is sub-second. |
| Hardcoded Docker subnet | Collides with competing networks on dev machines. |
| Session-scoped / ephemeral rules | Adds complexity (in-memory vs on-disk tracking). Persist by default is simpler. |
| Separate `Inspect` bool on EgressRule | Redundant. `PathRules` presence implies MITM. Separate flag creates confusing states. |
| Config hash for regeneration skipping | `Update()` already diffs rules. Hash is redundant. |
| In-memory caching of IPs/network info | CLI is not a daemon. Each invocation is a fresh process. Ask Docker. |
| Auto-disable firewall on Envoy crash | Creates a security bypass. Fail closed. User must explicitly `clawker firewall disable`. |
| Runtime CA injection into containers | Known at build time. Bake into image via Dockerfile. |
| Runtime prompt injection into containers | Known at build time. Bake `/etc/claude-code/CLAUDE.md` via Dockerfile. |
| Dynamic rule list in agent prompt | Context bloat risk. Prompt is a skill document (how to operate), not a manifest. |
| Scanning all project configs on every container start | Unnecessary. The calling CLI process already has the merged config in memory. `Update()` is additive only. |
| Firewall-specific subdirectory | Rules file is `egress-rules.yaml` in the config dir alongside `settings.yaml` and `projects.yaml`. |
| Per-project rule isolation | Trust is orthogonal to need. Union policy. Door open for future via source-IP matching. |
| Dedicated firewall lifecycle daemon | Piggyback on hostproxy's existing auto-start/auto-stop (already has Docker client). Future clawkerd daemon will own this properly. |
| `clawker firewall stop` command | Lifecycle managed by hostproxy. Manual teardown via `docker stop` if needed. |

---

## Schema

### EgressRule

```go
// internal/config/schema.go

type PathRule struct {
    Path   string `yaml:"path"`   // "/v1/models", "/uploads/*" (trailing * = prefix match)
    Action string `yaml:"action"` // allow, deny — first match wins
}

type EgressRule struct {
    Dst         string     `yaml:"dst"`                    // domain or IP
    Proto       string     `yaml:"proto,omitempty"`         // tls (default), ssh, tcp
    Port        int        `yaml:"port,omitempty"`          // optional, default by proto
    Action      string     `yaml:"action,omitempty"`        // allow (default), deny
    PathRules   []PathRule `yaml:"path_rules,omitempty"`    // ordered, first match wins; presence triggers MITM
    PathDefault string     `yaml:"path_default,omitempty"`  // allow or deny; required when path_rules present
}

type FirewallConfig struct {
    Enable         *bool           `yaml:"enable,omitempty"`
    AddDomains     []string        `yaml:"add_domains,omitempty" merge:"union"`
    Rules          []EgressRule    `yaml:"rules,omitempty" merge:"union"`
    IPRangeSources []IPRangeSource `yaml:"ip_range_sources,omitempty"` // DEPRECATED: log warning if used, ignore at runtime
}
```

**Migration note:** `Enable` changes from `bool` to `*bool`. The existing `FirewallEnabled()` method (`f != nil && f.Enable`) must be updated to dereference: `f != nil && f.Enable != nil && *f.Enable`. `nil` means "not set" (use default: enabled). `false` means explicitly disabled. Add a storage migration to handle existing `enable: true/false` values (already booleans in YAML, deserialized correctly by `*bool`).

**Union merge note:** `[]EgressRule` with nested `[]PathRule` uses deep-equality deduplication (already supported by storage layer — see storage CLAUDE.md on unhashable values). Add oracle/golden test coverage for nested struct union dedup.

### NormalizeRules()

Method on `*FirewallConfig` with nil receiver guard (follows `FirewallEnabled()` pattern — if `f == nil`, returns empty slice). Expands `AddDomains` into `[]EgressRule` (each becomes `{Dst: domain, Proto: "tls", Action: "allow"}`), merges with `Rules`, deduplicates by `dst+proto+port`. Single conversion point from user config to internal rule format.

### EgressRulesFile Type

```go
// internal/firewall/types.go

type EgressRulesFile struct {
    Rules []EgressRule `yaml:"rules"`
}
```

Managed by `storage.Store[EgressRulesFile]`. This is NOT a config schema type — it lives in the firewall package, not config. The config package owns `EgressRule` and `PathRule` (shared types); the firewall package owns the file wrapper.

### Config Interface Additions

```go
EgressRulesFileName() string          // "egress-rules.yaml"
FirewallDataSubdir() (string, error)  // XDG data dir for generated configs + certs
```

### Egress Rules File

Location: `~/.config/clawker/egress-rules.yaml` (config dir)
Managed by: `storage.Store[EgressRulesFile]` with `WithConfigDir()` and `WithLock()`

```yaml
# ~/.config/clawker/egress-rules.yaml
rules:
  - dst: api.anthropic.com
    proto: tls
    action: allow
  - dst: github.com
    proto: tls
    action: allow
    path_rules:
      - path: "/*/raw/*"
        action: deny
    path_default: allow
  - dst: storage.googleapis.com
    proto: tls
    action: allow
    path_rules:
      - path: "/golang/*"
        action: allow
      - path: "/download/storage/v1/*/golang/*"
        action: allow
    path_default: deny
```

### Generated Configs

Location: `~/.local/share/clawker/firewall/` (data dir)

```
firewall/
├── envoy.yaml          # generated, bind-mounted into clawker-envoy
├── Corefile            # generated, bind-mounted into clawker-coredns
├── ca-cert.pem         # CA certificate (also baked into images)
├── ca-key.pem          # CA private key (never leaves host)
└── certs/              # per-domain MITM certs
    ├── github.com-cert.pem
    ├── github.com-key.pem
    ├── storage.googleapis.com-cert.pem
    └── storage.googleapis.com-key.pem
```

---

## FirewallManager Interface

```go
// internal/firewall/firewall.go

type FirewallManager interface {
    // Lifecycle
    EnsureRunning(ctx context.Context) error
    Stop(ctx context.Context) error
    IsRunning(ctx context.Context) bool

    // Rule management
    Update(ctx context.Context, rules []EgressRule) error   // additive only
    Remove(ctx context.Context, rules []EgressRule) error   // explicit removal
    Reload(ctx context.Context) error                       // force regeneration from current rules
    List(ctx context.Context) ([]EgressRule, error)

    // Killswitch
    Disable(ctx context.Context, containerID string) error  // flush DNAT + restore DNS
    Enable(ctx context.Context, containerID string) error   // re-apply DNAT + firewall DNS

    // Escape hatch
    Bypass(ctx context.Context, containerID string, timeout time.Duration) error

    // Status
    Status(ctx context.Context) (*FirewallStatus, error)

    // Network info (discovered from Docker on each invocation)
    EnvoyIP() string
    CoreDNSIP() string
}

type FirewallStatus struct {
    Running       bool
    EnvoyHealth   bool
    CoreDNSHealth bool
    RuleCount     int
    EnvoyIP       string
    CoreDNSIP     string
    NetworkID     string
}
```

### v1 Implementation: DockerFirewallManager

- Uses `whail.Engine` for container + network operations
- Uses `config.Config` for paths and constants
- Uses `*logger.Logger` for file logging
- Uses `storage.Store[EgressRulesFile]` with `WithLock()` for rule persistence
- Factory noun: `f.Firewall(ctx) (FirewallManager, error)` — lazy `sync.Once` closure, follows `clientFunc()` pattern (not `hostProxyFunc()`)

### Test Double

`firewalltest.MockFirewallManager` with function fields — same pattern as `hostproxytest.MockManager`.

---

## Container Lifecycle

### EnsureRunning()

1. Check if `clawker-net` exists → if not, create (let Docker pick subnet)
2. Inspect network → get gateway IP → compute static IPs for Envoy/CoreDNS
3. Check if `clawker-envoy` running → if not, start:
   - Image: `envoyproxy/envoy:distroless-<version>` (pin version)
   - Static IP on `clawker-net`
   - Bind mount: `envoy.yaml` from data dir
   - Bind mount: `certs/` from data dir (for MITM)
   - Restart policy: `unless-stopped`
   - Admin endpoint for health checks
4. Check if `clawker-coredns` running → if not, start:
   - Image: `coredns/coredns:<version>` (pin version)
   - Static IP on `clawker-net`
   - Bind mount: `Corefile` from data dir
   - Restart policy: `unless-stopped`
   - Health endpoint: `:8080/health`
5. Health check both containers

### Stop()

Stop and remove both containers. Remove `clawker-net` network.

### Partial State Handling

`EnsureRunning()` must handle partial state (e.g., network exists but Envoy is dead, or Envoy is running but CoreDNS isn't). Inspect each component independently, start/restart only what's missing or unhealthy.

### Network Coordination

`clawker-net` is already used by the monitoring stack and container creation. `EnsureRunning()` MUST NOT recreate the network if it already exists — only create if absent. If the network exists, inspect it and compute IPs from its existing subnet. Other containers (monitoring, agents) may already be attached.

---

## Update() Flow

```
Update(ctx, incomingRules []EgressRule) error:
    flock egress-rules.yaml
    read current rules
    diff: newRules = incoming - current (key: dst+proto+port)
    if len(newRules) == 0:
        unlock, return nil  // no new rules, no I/O
    append newRules to current rules
    generate envoy.yaml from full rule set
    generate Corefile from full rule set
    generate MITM certs for rules with PathRules (if CA exists)
    write updated egress-rules.yaml (atomic) ← PERSIST RULES FIRST
    write envoy.yaml + Corefile to data dir
    restart clawker-envoy container
    (CoreDNS auto-reloads via reload plugin)
    unlock
```

---

## Config Generation

### Envoy Config (envoy.yaml)

Follows openclaw-deploy's proven structure. Input: `[]EgressRule`. Output: YAML + warnings.

**Listener on `:10000` (TLS egress):**
- TLS Inspector reads SNI from ClientHello
- Filter chain ordering:
  1. **MITM filter chains** — for rules with `PathRules`. TLS termination with per-domain cert, HTTP route matching (ordered path rules, first match wins, `PathDefault` catch-all), `dynamic_forward_proxy` for upstream re-encryption.
  2. **SNI passthrough filter chains** — for plain TLS allow rules without path inspection. `sni_dynamic_forward_proxy` → forward to upstream on port 443.
  3. **Default deny** — catch-all filter chain → `deny_cluster` (static, no endpoints, connection reset).

**TCP/SSH listeners on `:10001+`:**
- One dedicated listener per SSH/TCP rule
- Sequential ports starting from `ENVOY_TCP_PORT_BASE`
- Per-destination clusters (STRICT_DNS for domains, STATIC for IPs)

**Clusters:**
- `dynamic_forward_proxy_cluster` — TLS passthrough
- `deny_cluster` — static, empty, resets connections
- Per-domain MITM forward proxy clusters — TLS origination with upstream CA validation
- Per-destination TCP clusters

### CoreDNS Config (Corefile)

Input: `[]EgressRule`. Output: Corefile text.

```
# Per-domain forward zones (one per allowed domain)
github.com {
    forward . 1.1.1.2 1.0.0.2
}

storage.googleapis.com {
    forward . 1.1.1.2 1.0.0.2
}

# ... more domains ...

# Catch-all: NXDOMAIN for everything else + health + reload
. {
    template IN ANY . {
        rcode NXDOMAIN
    }
    health :8080
    reload
}
```

- Upstream DNS: 1.1.1.2, 1.0.0.2 (Cloudflare malware-blocking)
- `reload` and `health` plugins MUST be in the catch-all `.` block, NOT in per-domain zones. CoreDNS `health` binds a port globally; placing it in a per-domain zone means it disappears if that domain is removed. `reload` in the catch-all ensures Corefile changes to any zone trigger a reload.
- IP/CIDR destinations and deny rules excluded from DNS

### CA + Certificate Management (certs.go)

- `EnsureCA(dataDir) (caCert, caKey, error)` — generate self-signed CA if not exists, load if exists
- `GenerateDomainCert(caCert, caKey, domain) (cert, key, error)` — sign per-domain cert
- `RegenerateDomainCerts(rules, dataDir) error` — generate for all rules with `PathRules`
- `RotateCA(dataDir) error` — regenerate CA + all domain certs, warn user

CA keypair persisted in data dir. Per-domain certs generated when rules have `PathRules`. CA cert also copied into Dockerfile build context for `update-ca-certificates`.

---

## Agent Container Integration

### Container Creation Flow

```
CreateContainer():
    cfg = (already loaded, merged project config in memory)

    firewallManager.EnsureRunning(ctx)

    // NormalizeRules() has nil receiver guard — safe even if Firewall config is nil
    rules = cfg.Project().Security.Firewall.NormalizeRules(cfg.RequiredFirewallRules())
    firewallManager.Update(ctx, rules)

    envoyIP = firewallManager.EnvoyIP()
    corednsIP = firewallManager.CoreDNSIP()

    // RuntimeEnvOpts (replaces FirewallDomains + FirewallIPRangeSources)
    opts.FirewallEnvoyIP = envoyIP
    opts.FirewallCoreDNSIP = corednsIP

    // Container created on clawker-net with --dns=corednsIP
```

### RuntimeEnvOpts Changes

Remove:
- `FirewallDomains []string`
- `FirewallIPRangeSources []config.IPRangeSource`

Add:
- `FirewallEnvoyIP string`
- `FirewallCoreDNSIP string`
- `FirewallNetCIDR string`  // clawker-net subnet CIDR for iptables RETURN rule

### init-firewall.sh Rewrite

Strip all IP range fetching, domain DNS resolution, ipset logic. New script:

```bash
#!/bin/bash
# Receive Envoy + CoreDNS IPs and network CIDR as env vars
ENVOY_IP="${CLAWKER_FIREWALL_ENVOY_IP}"
COREDNS_IP="${CLAWKER_FIREWALL_COREDNS_IP}"
CLAWKER_NET_CIDR="${CLAWKER_FIREWALL_NET_CIDR}"  # e.g., 172.20.0.0/16

# Root (uid 0) bypasses all rules — escape hatch
iptables -t nat -A OUTPUT -m owner --uid-owner 0 -j RETURN

# Preserve Docker DNS (127.0.0.11) and loopback
iptables -t nat -A OUTPUT -d 127.0.0.0/8 -j RETURN

# Preserve clawker-net internal traffic (Envoy, CoreDNS, other containers)
# Without this, traffic to Envoy/CoreDNS IPs would loop through DNAT
iptables -t nat -A OUTPUT -d ${CLAWKER_NET_CIDR} -j RETURN

# DNS redirect: all non-root UDP/TCP 53 → CoreDNS
iptables -t nat -A OUTPUT -p udp --dport 53 -j DNAT --to-destination ${COREDNS_IP}:53
iptables -t nat -A OUTPUT -p tcp --dport 53 -j DNAT --to-destination ${COREDNS_IP}:53

# TCP DNAT: all non-root TCP → Envoy
iptables -t nat -A OUTPUT -p tcp -j DNAT --to-destination ${ENVOY_IP}:10000

# Drop all other UDP (prevent exfiltration)
iptables -A OUTPUT -p udp ! -d 127.0.0.0/8 -m owner ! --uid-owner 0 -j DROP
```

**iptables rule evaluation order:** Rules are evaluated top-to-bottom in insertion order (`-A` appends). Root traffic hits the uid-owner 0 RETURN first and exits the chain — all subsequent rules only apply to non-root. The `clawker-net` RETURN ensures traffic to Envoy/CoreDNS IPs is not re-DNAT'd (would cause a routing loop). DNS DNAT comes before the TCP catch-all so port 53 traffic goes to CoreDNS, not Envoy.

### Dockerfile Template Changes

Add to base packages (both Debian + Alpine):
- `dante-server`
- `proxychains-ng` (Alpine) / `proxychains4` (Debian)

Add managed policy CLAUDE.md and CA cert to the **root-mode section** of the Dockerfile template (before `USER ${USERNAME}`, around the existing firewall script block). Both require root privileges (`update-ca-certificates` writes to system dirs).

Template gating: add `HasFirewallCA bool` field to `DockerfileContext`. The CLAUDE.md block is unconditional; the CA cert block is conditional on `HasFirewallCA` (CA may not exist yet on first build).

```dockerfile
# Clawker agent environment awareness (managed policy location for Claude Code)
RUN mkdir -p /etc/claude-code
COPY clawker-agent-prompt.md /etc/claude-code/CLAUDE.md

# Firewall CA cert for MITM inspection (conditional — CA may not exist on first build)
{{- if .HasFirewallCA}}
COPY clawker-ca.crt /usr/local/share/ca-certificates/clawker-firewall-ca.crt
RUN update-ca-certificates
{{- end}}
```

---

## Escape Hatch (Dante Bypass)

`clawker firewall bypass <duration> --agent <name>`

Implementation via `docker exec` into target agent container:
1. Write Dante config to `/run/firewall-bypass-danted.conf` (loopback only, port 9100)
2. Write proxychains config to `/run/firewall-bypass-proxychains.conf`
3. Add iptables RETURN rule for root (uid 0) — already exists from init-firewall.sh
4. Start `danted` as root (backgrounded)
5. Schedule timeout kill (background process)

`clawker firewall bypass stop --agent <name>`:
- Kill danted, clean up configs

Agent prompt teaches the agent how to use the bypass when the user opens it (proxychains4, socks5h://).

---

## CLI Commands

Package: `internal/cmd/firewall/`

| Command | Description |
|---|---|
| `clawker firewall status` | Health, active rule count, network info |
| `clawker firewall list` | List active rules |
| `clawker firewall add <domain> [--proto tls] [--port 443]` | Add rule (persists to egress-rules.yaml) |
| `clawker firewall remove <domain>` | Remove rule |
| `clawker firewall reload` | Force config regeneration from current rules |
| `clawker firewall disable [--agent <name>]` | Killswitch: flush DNAT + restore DNS |
| `clawker firewall enable [--agent <name>]` | Restore DNAT + firewall DNS |
| `clawker firewall bypass <duration> --agent <name>` | Start Dante SOCKS proxy |
| `clawker firewall bypass stop --agent <name>` | Stop Dante SOCKS proxy |
| `clawker firewall rotate-ca` | Regenerate CA + warn about rebuilds |

Follow existing command patterns: `NewCmd(f, runF)`, Options struct, `FormatFlags` for `--json`/`--format` on list/status.

**No `clawker firewall stop` command.** Envoy/CoreDNS lifecycle is managed by the hostproxy daemon — they start when hostproxy starts and stop when hostproxy auto-terminates (no agent containers for 60s). Users who need manual teardown can use `docker stop clawker-envoy clawker-coredns` directly.

---

## Agent Prompt (`/etc/claude-code/CLAUDE.md`)

Static, list-agnostic skill document. Teaches the agent how to operate, not what's configured.

Content covers:
- You are running in a clawker container with an egress firewall
- Outbound TCP/UDP is restricted to whitelisted domains
- Always attempt connections first — domains may have been added
- If a connection fails: report to user with `clawker firewall add <hostname>` command
- Bypass instructions: user runs `clawker firewall bypass <duration> --agent <name>`, then use `proxychains4 -f /run/firewall-bypass-proxychains.conf <command>` or `socks5h://localhost:9100`
- Bypass config files only exist while proxy is running
- This file is auto-generated — do not modify

Will grow over time to cover other clawker container capabilities.

---

## Existing Infrastructure to Leverage

Implementing agents MUST use these existing systems. Do NOT reimplement.

### storage.Store[T]
- Use `WithConfigDir()` + `WithLock()` for `egress-rules.yaml`
- Atomic writes (temp file + rename) come for free
- `flock` advisory locking comes for free
- `Read()` for lock-free reads, `Set(fn)` for COW mutation, `Write()` for persistence

### config.Config Interface
- All new path accessors go on the Config interface (`EgressRulesFileName()`, `FirewallDataSubdir()`)
- Never hardcode paths, filenames, or env vars
- Use existing `ConfigDir()`, `DataDir()`, `StateDir()` for base directories

### Factory Noun Pattern
- `f.Firewall()` returns `(FirewallManager, error)` — lazy `sync.Once` closure
- Wire in `internal/cmd/factory/default.go` following `clientFunc()` pattern (takes `context.Context`, returns error) — NOT `hostProxyFunc()` which has no error return. The firewall manager requires Docker connectivity, which can fail.
- Factory field signature: `Firewall func(context.Context) (FirewallManager, error)`
- Commands capture on Options struct: `opts.Firewall = f.Firewall`

### whail.Engine
- Network CRUD: `NetworkCreate()`, `NetworkInspect()`, `NetworkRemove()`
- Container creation with static IP via `NetworkingConfig.EndpointsConfig`
- Container lifecycle: `ContainerCreate()`, `ContainerStart()`, `ContainerStop()`, `ContainerRemove()`
- Label-based filtering via `LabelFilter()`

### hostproxy.Manager Pattern
- `EnsureRunning()` idempotent: check health → start if needed → verify
- `IsRunning()` checks real state (container running + healthy)
- Follow the same structure for `DockerFirewallManager`

### Test Doubles
- `firewalltest.MockFirewallManager` with function fields
- `dockertest.NewFakeClient()` for Docker API mocking
- `configmocks.NewBlankConfig()` / `configmocks.NewFromString()` for config
- `testenv.Env` for isolated test directories

---

## Standing Rules for Implementing Agents

1. **Read before writing.** Before modifying any package, read its `CLAUDE.md`, the relevant `.claude/rules/` files, and explore existing code with Serena tools. Understand what exists.

2. **Documentation can drift.** If you encounter tension between CLAUDE.md docs / rules and what the code actually does, STOP and consult the user. Do not resolve discrepancies yourself.

3. **No redundant abstractions.** If a capability exists in the storage, config, or whail packages, use it. Do not reimplement atomic writes, file locking, config merging, or Docker operations.

4. **Package boundaries are strict.** Only `pkg/whail` imports moby. Only `internal/docker` imports `pkg/whail`. The firewall package should accept `whail.Engine` or `docker.Client` via constructor, not import moby directly.

5. **All tests must pass.** `make test` before considering any task complete. Add golden file tests for generated envoy.yaml and Corefile.

6. **Consult the user on ambiguity.** If a design decision isn't covered in this spec, ask. Do not invent new patterns or make architectural choices independently.

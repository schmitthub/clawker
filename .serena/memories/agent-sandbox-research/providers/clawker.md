# clawker (self-assessment, maintainer-written, grounded in repo @ main 2026-07-18)
category: local
Local-first Docker-based agent sandbox manager: granular egress firewall, monitoring stack, host-seamless DX | built on: containerization (Docker Engine API via whail client) | license: AGPL-3.0-or-later + dual-licensing CLA (#380; was MIT) | maturity: alpha, active development

## A. Identity
### built_on
Containerization. CLI talks to Docker Engine API (any API-compatible engine is drop-in by design; validated on Docker Engine/Desktop; monitoring stack shells out to docker compose). Control-plane container (clawkercp) supervises; clawkerd PID-1 in agent containers. Firewall subsystem (optional, on by default): CoreDNS + Envoy + eBPF — implementation detail, not identity.
Sources: docs/architecture.mdx, docs/container-internals.mdx, pkg/whail
### execution_locality
execution_locality: Local — containers on the user's Docker host; code/creds never leave machine. Remote Docker daemons technically reachable via Engine API but UNDOCUMENTED → not claimed (own evidence rules).
### open_source
AGPL-3.0-or-later, dual-licensing CLA (commit c3d7517a #380). Fully self-hosted by nature.
### maturity
Alpha; single-org backing; rapid iteration.

## B. Threat protection
### host_fs_damage
host_fs_damage: Yes — container boundary; only workspace mounted (bind) or nothing live (snapshot mode = copy, fully disposable). Caveat: bind mode intentionally exposes the workspace itself (policy choice per run).
Sources: docs/configuration.mdx workspace.default_mode
### credential_theft
credential_theft: Yes — mediated forwarding: SSH/GPG agent socket bridges (keys never enter container), git HTTPS via host proxy, managed-config seeds copied at create. Host secrets outside seeds unreachable. Caveat: seeded harness creds readable inside container (inherent to seeding).
Sources: docs/credentials.mdx
### data_exfiltration
data_exfiltration: Yes — deny-by-default egress: unlisted domains NXDOMAIN at CoreDNS; eBPF denies unrouted flows; path/method/regex scoping narrows allowed hosts (e.g. github.com/your-org/ + GET); per-request logs.
Sources: docs/firewall.mdx
### malicious_execution
malicious_execution: Yes — container blast radius + firewall + unprivileged user + no docker socket by default.
### escape_resistance
escape_resistance: Yes (container-tier) — shared-kernel container isolation: unprivileged user, raw sockets blocked (eBPF sock_create). Honest caveat: weaker boundary than microVM/hardware virt; no gVisor/Kata layer.
Sources: controlplane/firewall/ebpf/bpf/clawker.c
### resource_abuse
resource_abuse: Yes — full docker resource flags on run (--cpus, --memory, --memory-swap, cpuset...).
Sources: docs/cli-reference/clawker_run.md

## C. Features & granularity
### network_default_posture
Deny-by-default — unconfigured container: only allowlisted destinations resolve/route; everything else NXDOMAIN/denied.
Sources: docs/firewall.mdx
### egress_allowlist
egress_allowlist: Yes — full ladder: domain → .wildcard subdomains → IP/CIDR → proto+port/port-range → deny rules w/ documented precedence (exact>wildcard, deny-wins-in-tier, longest-rule-wins paths) → path/method/regex.
Sources: docs/firewall.mdx
### dns_level_blocking
dns_level_blocking: Yes — CoreDNS returns NXDOMAIN for unlisted domains; subtree exfil closed via exact-host scoping (#320).
### domain_native_enforcement
domain_native_enforcement: Yes — rules enforced against hostnames at request time end-to-end: CoreDNS policy at resolution, Envoy SNI/Host matching + dynamic-forward-proxy clusters for FQDN flows (zero ORIGINAL_DST for domain rules; IP/CIDR is a separate explicit rule type, not an enforcement fallback). No resolve-once IP-set snapshotting — immune to both LB-rotation breakage and CDN shared-IP over-permission that resolve-to-iptables designs (devcontainers reference firewall, resolve-then-CIDR-check proxies) exhibit.
Sources: docs/firewall.mdx; .claude/rules/firewall-uat.md (LOGICAL_DNS/DFP, no ORIGINAL_DST for FQDN flows)
### tls_mitm_inspection
tls_mitm_inspection: Yes — Envoy always MITMs TLS (SSL_CERT_FILE/CURL_CA_BUNDLE preset); per-rule insecure_skip_tls_verify for upstream self-signed.
### http_path_rules
http_path_rules: Yes — literal prefix + `~` full-anchored RE2 regex paths + methods enum per rule + path_default (deny default = allowlist mode).
Sources: docs/firewall.mdx path_rules table
### proto_coverage
proto_coverage: Yes — DNS (CoreDNS policy+logs), TCP (eBPF redirect→Envoy), UDP incl. QUIC routing (envoy_udp, main), ICMP structurally blocked (raw-socket denial), IPv6 native-deny + v4-mapped routing, L7 rules: https/http/ws/wss/ssh/tcp/udp + any opaque L7 name (documented extensible proto model; FTP-class protos config-addable, not shipped-tested).
Sources: controlplane/firewall/ebpf/bpf/clawker.c program list; docs/firewall.mdx proto field
### live_rule_reload
live_rule_reload: Yes — firewall add/refresh live-apply without restart.
### firewall_escape_hatch
firewall_escape_hatch: Yes — timed bypass with auto-expiry (blocking countdown or background), per-agent disable/enable, per-rule loosening. Break-glass without abandoning tool.
### enforcement_plane
Kernel eBPF (cgroup connect/sendmsg/recvmsg/sock_create hooks, host-side, pinned /sys/fs/bpf) + Envoy dataplane. Agent is unprivileged in-container: cannot see or detach host cgroup programs — no tamper path from inside. Traffic logged at Envoy + CoreDNS layers.
### fail_closed
fail_closed: Yes — CP crash leaves pinned eBPF enforcing last ruleset (documented invariant; startup-gate failures exit WITHOUT flushing eBPF so enrolled agents stay filtered).
Sources: CLAUDE.md CP invariant; cmd/clawkercp
### network_audit
network_audit: Yes — per-request Envoy access logs + CoreDNS query logs + netlogger → OpenSearch (clawker-envoy, clawker-coredns indexes), dashboards preconfigured.
Sources: docs/monitoring.mdx
### workspace_modes
workspace_modes: Yes — bind (live) and snapshot (ephemeral copy), per-project default + per-run override.
### observability
observability: Yes — monitor stack: OTel collector → Prometheus + OpenSearch; preconfigured dashboards (CC Cost & Usage, Activity #343); clawker monitor up/status.
### supervision
supervision: Yes — CP↔clawkerd sessions: observation (overseer events), command dispatch, containment; agent registry + cert attestation.
Sources: docs/control-plane.mdx
### fleet_mgmt
fleet_mgmt: Yes — hierarchical naming clawker.project.agent, authoritative labels, project registry, full container lifecycle CLI.
### snapshots_persistence
snapshots_persistence: Partial — config + shell history persist in named volumes across restarts/recreation; no memory-state pause/resume snapshot productized.

## D. Setup
setup: Easy — install CLI, clawker init, clawker run (prereq: Docker). Zero-config seeding: host harness settings/plugins/creds copied at create; shared state (CC memories) mounted in sync.
## E. Daily use
daily_use: Easy — run/start/attach; image rebuild on build-config change is the main friction.
## F. Configuration
config_depth: Deep — clawker.yaml (build/agent/workspace/security incl. full firewall rules) + settings.yaml, JSON schemas + yaml-lsp headers, versionable.
policy_model: Fully-policy-driven — secure defaults, per-project config, per-run overrides, break-glass (bypass/disable), copy-vs-bind per run. The design center of the tool.
## G. DX
bind_mount_sharing: Yes — bind mode = live two-way. cred_forwarding: Yes (ssh/gpg sockets, git https, gitconfig). browser_auth: Yes — host proxy round-trips browser-open + callback (gh auth login etc.). shared_dirs: Partial — docker run volume flags; no first-class extra-dirs config key. git_worktrees: Yes — worktree subcommands + containers per worktree. nested_containers: Partial — security.docker_socket opt-in. harness_agnostic: Yes — multi-harness + bundles (claude default; harness.yaml authoring).
## H. Performance
performance: Lightweight — container-tier (no guest kernel); no published benchmarks (honest gap). macOS bind-mount IO = virtiofs tax (known Docker Desktop property).
## I. Feasibility
feasibility: Adoptable today — macOS/Linux + Docker; alpha maturity caveat; no Windows docs.
## J. Price
Free, open source (AGPL-3.0-or-later). No hosted tier.
## K. Extensibility
extensibility: Yes — bundles (harnesses/stacks/monitoring extensions), plugin system, custom Dockerfile injection points, post_init/pre_run hooks.

## Condensation framing notes (maintainer, 2026-07-18)
- Creds × egress compose: sentinel/header-injection (Docker Sandboxes model) hides the credential STRING, not the CAPABILITY — an agent holding proxied auth can still exfil to any allowlisted domain (their docs concede github.com-wide allows are exfil channels), and where a real token is readable (`gh auth token`) a loose allowlist lets it ship anywhere. Containment = scope the USE: path/method-scoped egress + per-request audit. Clawker: ssh/gpg agent mediation (key never present), seeded harness creds readable-but-contained by egress+audit. "Hidden token ≠ contained agent" is the callout.
- Cred-proxy-is-theater frame (README callout): the injection layer hides the secret in flight, but agent-facing CLIs mint/print live tokens on demand — `gh auth token`, `aws configure export-credentials`, `az account get-access-token`, `gcloud auth print-access-token`. Once the agent holds a real token AND egress is domain-wide, it posts creds+data to an attacker-controlled resource on the SAME allowlisted service (attacker's S3 bucket, Azure Blob container, GCS bucket, github repo/gist) — same trusted domain, no injection layer in the path. So cred-proxy without path/method-scoped egress is a facade: it defends the copy in transit, not the token's use. The only real containment is scoping WHERE authenticated requests can go (clawker: `github.com/your-org/` + method gating + per-request audit) — and mediating the primitive so no replayable token exists at all (ssh/gpg agent sockets).
- Domain-native enforcement is a differentiator row candidate: several competitors enforce domain rules as resolved IP sets (devcontainers resolve-once iptables; Docker Sandboxes resolve-then-CIDR-check) with LB-breakage + CDN shared-IP over-permission failure modes.

## Unknowns & caveats
- Remote-engine operation undocumented → excluded from claims.
- shared_dirs first-class config: not present (flags only).
- No published perf benchmarks.
- Alpha status disclosed in feasibility.

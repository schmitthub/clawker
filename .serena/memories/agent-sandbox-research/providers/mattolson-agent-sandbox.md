# agentbox (mattolson/agent-sandbox)
category: local
Secure local Docker sandbox for running AI coding-agent CLIs behind a mitmproxy egress allowlist + iptables firewall | built on: Debian container, Docker/Colima, mitmproxy sidecar, iptables | license: MIT | maturity: early-stage single-maintainer OSS ("You may experience breaking changes between releases")

As-of: 2026-07-18. All sourcing from official repo (README + docs/) fetched directly.

## A. Identity
### built_on (prose-only)
Shared-kernel Docker container ("Debian container with pinned dependencies"), target platform Colima + Docker Engine on Apple Silicon (also Docker Desktop/Podman/OrbStack/Rancher). Two-container model: an agent container + a mitmproxy "proxy" sidecar. No microVM/hypervisor isolation; no persistent control-plane/supervisor process — orchestration is docker compose invoked by the `agentbox` CLI.
Sources:
- https://github.com/mattolson/agent-sandbox — "Debian container with pinned dependencies"; "Target platform: Colima + Docker Engine on Apple Silicon"
- README — "Proxy (mitmproxy sidecar)"; "Firewall (iptables)"

### execution_locality
Local — agent runs in a local Docker container on the developer's own machine via `agentbox exec`; no vendor cloud, project code and secrets never leave the host (secrets stay in a host-side dir and are injected by the local proxy).
Sources:
- README — "CLI (preferred) - run the agent in a terminal session using `agentbox exec`"
- docs/secrets — "The token lives in a host-side secret directory, never in the container's filesystem or Docker volumes."

### open_source (prose-only)
MIT License; fully self-hostable (runs entirely on the user's local Docker/Colima).
Sources:
- README — "MIT License"

### maturity (prose-only)
Early-stage, single maintainer (mattolson). README warns of breaking changes between releases. Supports a broad agent matrix (Claude Code, Codex, Gemini, OpenCode, Pi, Factory, Copilot, Hermes).
Sources:
- README — "You may experience breaking changes between releases"; agent support matrix

## B. Threat protection
### host_fs_damage
Yes — only the repo workspace + project-scoped agent state are mounted; agent has R/W to the repo dir only.
- README — "Minimal mounts: only the repo workspace + project-scoped agent state"; "read/write access to only your repository directory"

### credential_theft
Yes — API keys / git tokens held in a host-side secret dir and injected by the proxy; the agent container never sees them.
- docs/secrets — "Secret injection conducted in the proxy, so the agent container never sees secrets such as API keys"

### data_exfiltration
Yes — default-deny egress: mitmproxy allowlist (non-matching → 403) plus iptables blocking all direct outbound so the proxy cannot be bypassed. SSH (port 22) explicitly blocked to prevent tunneling.
- README — "Firewall (iptables) - Blocks all direct outbound from the agent container"; "Non-matching requests get a 403"
- docs/plan/decisions/002 — "Block all outbound SSH. Git operations must use HTTPS."

### malicious_execution
Partial — network + fs blast radius contained, but isolation is a shared-kernel container; a kernel/container escape is the residual surface. No microVM.
- README — container-based; no VM boundary documented.

### escape_resistance
Isolation boundary stronger than a plain host process (container namespaces + kernel iptables), but weaker than microVM/gVisor — shared-kernel Docker only. No syscall-filter/hypervisor hardening documented.
- README — "Debian container"; iptables firewall.

### resource_abuse
Unknown — no documented CPU/mem/disk limits (docker compose could set them but not surfaced as an agentbox feature). Docs silent.

## C. Feature set & granularity
### network_default_posture
Deny-by-default — explicit YAML allowlist; unlisted hosts blocked.
- README — "Default deny network policy"; "Non-matching requests get a 403"

### egress_allowlist
Yes — YAML allowlist. Granularity: named `services` (github, claude, …) + `domains` (exact host and `*.example.com` wildcard) + per-host request rules (schemes http/https, methods, path exact/prefix, query exact). NO IP/CIDR, NO ports/port-ranges, NO deny rules, NO regex. "Rules are allow-only conjunctions."
- docs/policy/schema — "Wildcard matching ... *.example.com matches both example.com and any subdomain"; "Supported matchers: exact, prefix"; "Rules are allow-only conjunctions"; schemes "Allowed values: http, https"

### dns_level_blocking
No — unlisted domains fail at the proxy with 403 (and iptables blocks direct egress); enforcement is not at the DNS tier (no NXDOMAIN). The agent resolves via the proxy, not its own external DNS.
- README — proxy 403 model; iptables block.

### domain_native_enforcement
Yes — mitmproxy checks the HTTPS CONNECT tunnel host (SNI/Host) against the policy at request time (forward proxy), not a resolve-once IP snapshot.
- README — "mitmproxy addon (enforcer.py) ... checks HTTPS CONNECT tunnels against the host policy, then checks decrypted HTTP/HTTPS requests"

### tls_mitm_inspection
Yes — mitmproxy MITMs TLS to inspect decrypted HTTP/HTTPS for scheme/method/path/query rules.
- README — "checks decrypted HTTP/HTTPS requests against any scheme, method, path, or query rules"

### http_path_rules
Yes — path exact/prefix + method gating + query-exact per host. No regex.
- docs/policy/schema — path matchers exact/prefix; `methods: [GET, POST]`; query exact only.

### proto_coverage
HTTP/HTTPS controlled with rich L7 rules; SSH explicitly blocked (ADR 002, port 22); all other direct outbound blocked by iptables (bypass prevention). No ws/wss/tcp/udp/grpc rule types in the policy language (schemes limited to http/https). QUIC/HTTP3, ICMP not documented (would be blocked by default-deny firewall but not a governed policy dimension). Fixed protocol set — not documented-extensible.
- docs/policy/schema — schemes "Allowed values: http, https"
- docs/plan/decisions/002 — "Block all outbound SSH"

### live_rule_reload
Yes — policy hot-reloads: saving active-policy changes reloads the running proxy; `agentbox proxy reload` sends SIGHUP; no container restart.
- README — "agentbox hot-reloads the proxy policy"; docs/cli — "agentbox proxy reload - Sends SIGHUP to reload proxy policy"

### firewall_escape_hatch
No — no timed break-glass/auto-re-enforce and no per-sandbox firewall disable/enable. Widening access = editing the allowlist. All-or-nothing otherwise.
- docs/cli — commands list has no bypass/disable-firewall verb.

### enforcement_plane
Dual: kernel netfilter (iptables `init-firewall.sh` drops all direct outbound except the Docker bridge to the proxy) + userspace mitmproxy for L7 allowlist. Agent cannot route around the proxy because iptables blocks direct egress. Traffic logged at the proxy.
- README — "even if an application ignores the proxy env vars, it cannot reach the internet directly"

### fail_closed
Yes (by architecture) — iptables default-deny is applied at container start and persists in the kernel netns; there is no separate control plane whose death opens egress. If the proxy sidecar dies, the agent has no egress path (fails closed to total block). Not documented as an explicit guarantee but structurally implied.
- README — iptables "Blocks all direct outbound"; proxy is the only egress path.

### network_audit
Yes (functional) — the mitmproxy sidecar processes/logs every proxied request and enforcement decision (403s); surfaced via `agentbox proxy logs`. Documented as a debugging aid rather than a formal audit trail.
- docs/cli — "agentbox proxy logs - Runs docker compose logs proxy"

### workspace_modes
Bind-mount only (repo dir mounted R/W). No ephemeral snapshot/copy mode documented.
- README — "read/write access to only your repository directory"

### observability
Log-level only — `agentbox logs` (compose logs) and `agentbox proxy logs`. No metrics/dashboards/OTel pipeline.
- docs/cli — logs commands only.

### supervision
No — the proxy blocks disallowed network requests but there is no external supervisor observing agent BEHAVIOR that can quarantine/kill the agent on conduct. Enforcement is network-gate only.
- README/docs — no supervisor/containment layer documented.

### fleet_mgmt
No — single-project model; `agentbox switch` changes the active agent but there is no multi-agent registry/naming/hierarchy/grouping.
- docs/cli — init/switch/up/down/exec; no fleet verbs.

### snapshots_persistence
Persistence yes, snapshots no — a persistent per-agent state volume preserves auth/config across restarts; no pause/resume/snapshot of running state documented.
- README — "Persistent volume for agent state - auth and config preserved across container restarts"

## D. Setup
setup: Moderate — install script (`curl … install.sh | sh`) then interactive `agentbox init` (generates compose + policy); prerequisites are Colima + Docker (Apple-Silicon-targeted). Secrets/git creds require manual host-side setup.
- README — install one-liner; "agentbox init (interactive setup)"

## E. Daily use
daily_use: Easy — `agentbox exec` opens a shell/agent session; `agentbox up/down`; devcontainer mode for IDEs. Policy edits hot-reload without restart.
- docs/cli — exec/up/down; README hot-reload.

## F. Configuration
### config_depth
Deep-ish, versionable — per-project `.agent-sandbox/` holds layered policy YAML (base + `user.policy.yaml` + per-agent `user.agent.<agent>.policy.yaml`) and editable compose files; image digests pinned. Tunable: allowlist rules, agent choice, dotfiles/shell.d, custom images. No documented post-init/pre-run lifecycle-command hooks.
- README/docs/policy — layered policy files; docs/images — compose editing + digest pinning.

### policy_model
policy_model: Moderate — network posture is a real default-deny policy with granular per-host HTTP rules and layered overrides, but limited escape hatches (no timed bypass, no per-sandbox firewall toggle) and a fixed http/https-only rule vocabulary. Workspace mode is fixed (bind-mount only).
- docs/policy/schema; docs/cli.

## G. DX
### bind_mount_sharing
Yes — repo workspace bind-mounted R/W; edits shared live with host.
- README — "read/write access to only your repository directory"

### cred_forwarding
Partial — git credentials mediated via proxy injection (git-askpass shim; token stays host-side, injected at request time). NO ssh-agent forwarding (SSH blocked) and NO gpg forwarding documented.
- docs/git — "let the proxy inject the credential at request time. No token is stored in the container"; docs/002 — SSH blocked.

### browser_auth
No — no host-browser auth proxy documented; agents authenticate via proxy-injected API keys/tokens or in-container login persisted to the state volume. No automatic host browser-open + callback forwarding.
- docs/secrets — proxy secret injection; no browser-proxy mechanism documented.

### shared_dirs
Partial — optional dotfiles mount (`~/.config/agent-sandbox/dotfiles` symlinked into home, read-only) and `shell.d` scripts; otherwise minimal mounts.
- docs/dotfiles — dotfiles/shell.d mounting.

### git_worktrees
No (sandbox feature) — docs give guidance for using git's own worktrees inside the container (Git 2.50.1, `worktree.useRelativePaths=true`, "Create shared worktrees under the repo root"); this is git usage guidance, not a sandbox-implemented worktree management feature.
- docs/git — worktree guidance.

### nested_containers
Unknown — no documented docker-socket/DinD support inside the sandbox; the firewall blocks direct outbound which would complicate it. Docs silent on nested runtimes.

### harness_agnostic
Yes — supports many agent CLIs (Claude Code, Codex, Gemini, OpenCode, Pi, Factory, Copilot, Hermes) with `agentbox switch --agent <x>`.
- README — agent support matrix; "agentbox switch --agent codex"

## H. Performance
performance: Unknown — no benchmarks published. Architecture is a shared-kernel Docker container + a proxy sidecar; expected overhead is container startup + MITM proxy latency. macOS bind-mount IO perf not measured in docs. No numbers to cite.

## I. Feasibility
feasibility: Adoptable today for the target platform (Apple Silicon + Colima/Docker), solo-dev friendly, MIT + local (no lock-in), but early-stage with breaking-change risk; SSH-based git workflows must move to HTTPS.
- README — target platform; breaking-change warning; docs/002 SSH→HTTPS.

## J. Price
### pricing (prose-only)
Free and open source (MIT); no cloud service, no paid tier — runs entirely on the user's local Docker/Colima.
- README — MIT; local install.

## K. Extensibility
extensibility: Moderate — custom/local images (`./images/build.sh` + edit compose), layered policy overrides, dotfiles/shell.d customization, a `services` catalog of named allowlist bundles, and a shipped `operating-in-agent-sandbox` skill. No formal plugin/extension system or user-extensible monitoring pipeline.
- docs/images — local image build; README — services catalog + shipped skill.

## Unknowns & caveats
- resource_abuse: docs silent on CPU/mem/disk limits — unknown, not confirmed absent.
- nested_containers: no docs on docker-socket/DinD — unknown.
- performance: no benchmarks — unknown.
- audit_log: proxy logs are per-request but documented as a debugging aid, not a formal audit product; credited functionally.
- fail_closed: inferred from documented iptables-default-deny architecture, not an explicit doc guarantee.
- lifecycle hooks: no documented post-init/pre-run command hooks (shell.d is shell-init customization only).
- UDP/QUIC/ICMP: blocked by the default-deny firewall in practice but not surfaced as governed policy dimensions; only SSH is an explicitly documented protocol block.

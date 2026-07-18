# OpenSandbox
category: orchestration (self-hosted sandbox execution layer; built on containers/microVMs)
General-purpose, protocol-first sandbox runtime for AI agent workloads (coding agents, GUI/browser agents, evaluation, RL training) with multi-language SDKs, a CLI (`osb`), and pluggable Docker/Kubernetes runtime backends | built on: Docker containers by default, optional gVisor/Kata Containers/Firecracker microVM secure runtimes, Kubernetes for distributed scheduling | Apache 2.0 | maturity: ~12k GitHub stars, ~1k forks, OpenSSF Best Practices badge, CNCF Landscape listed, released ~March 2026 by Alibaba

**Identity note (uncertain — flagged per assignment):** Confirmed via research that the project meant by "OpenSandbox" is Alibaba's `github.com/alibaba/OpenSandbox` (mirrored at `github.com/opensandbox-group/OpenSandbox`), Apache-2.0, released March 2026. This is distinct from several unrelated same/similar-named repos surfaced in search (`atarashansky/opensandbox`, `leo0481/2026_OpenSandbox` — appear to be forks/derivatives of the same codebase, not verified as canonical; `dsebastien.net/opensandbox` — an unrelated personal blog page) and from **Microsandbox** (a completely different, unrelated project by Super Rad Company/Zerocore AI using libkrun microVMs — similar name only). This writeup assesses `alibaba/OpenSandbox` exclusively, using its official docs (VitePress site content under `docs/` in the repo, plus `server/configuration.md` and component READMEs).

## A. Identity
### built_on (prose-only)
Architecture is split into a control plane and data plane. Control plane = Python FastAPI "Lifecycle Server" (`server/`) that validates requests and delegates to a runtime backend (`DockerSandboxService` or `KubernetesSandboxService`, same interface). Data plane = the sandbox workload container + an injected Go (Gin) daemon `execd` for command/file/PTY/Jupyter operations, plus an optional Go "Egress Sidecar" sharing the sandbox's network namespace for network policy enforcement, plus an optional Kubernetes-oriented HTTP/WebSocket "Ingress Proxy". Default isolation is plain Docker containers (runc); optional `[secure_runtime]` config swaps in gVisor (`runsc`), Kata Containers (`kata-qemu`/`kata-clh`/`kata-fc`), or Firecracker microVM (Kubernetes-only, via RuntimeClass) for stronger isolation.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/architecture/index.md — "SDKs and tools should depend on the public contracts, the server should own lifecycle orchestration, runtime providers should own platform-specific resource creation, and `execd`/egress should own operations that happen from inside the sandbox network and filesystem namespace."
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — "`type` — '' (runc default), 'gvisor', 'kata', or 'firecracker' (K8s only)"

### execution_locality
Determination: **Both**. Local execution via the Docker runtime backend on a developer's own machine (self-hosted lifecycle server + Docker daemon), or remote/distributed execution via the Kubernetes runtime backend for large-scale scheduling. There is no first-party managed/hosted OpenSandbox cloud offering documented — "remote" here means self-operated Kubernetes infrastructure, not a vendor SaaS. Code/credentials stay within whatever infrastructure the operator points the lifecycle server at (own laptop's Docker, or own/company Kubernetes cluster) — no third-party data path was found in official docs.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/README.md — "offering multi-language SDKs, unified sandbox APIs, and Docker/Kubernetes runtimes... enabling both local runs and large-scale distributed scheduling"

### open_source (prose-only)
Apache 2.0 license, fully self-hostable (in fact the only deployment model — there is no vendor-hosted SaaS documented). Source at github.com/alibaba/OpenSandbox.
Sources:
- https://github.com/alibaba/OpenSandbox — License: Apache 2.0

### maturity (prose-only)
~12,000 stars, ~1,000 forks, primary languages Python/Go/Kotlin/C#/TypeScript (multi-SDK reflected in repo composition). OpenSSF Best Practices badge and CNCF Landscape listing. Released publicly ~March 2026 (per third-party coverage), so roughly 4 months old as of this assessment (2026-07-18). Backed by Alibaba; active release cadence (e.g. a Kotlin CodeInterpreter SDK sub-release dated July 13, 2026). Community channels: Discord, DingTalk.
Sources:
- https://github.com/alibaba/OpenSandbox — "Star Count: 12,000... Fork Count: 1,000... Notable Badges: OpenSSF Best Practices certified, CNCF Landscape listed"
- https://www.marktechpost.com/2026/03/03/alibaba-releases-opensandbox-to-provide-software-developers-with-a-unified-secure-and-scalable-api-for-autonomous-ai-agent-execution/ — third-party, dates public release to March 2026

## B. Threat protection
### host_fs_damage
host_fs_damage: Yes — bind-mounting host paths into a sandbox requires the path to match a server-side allowlist that defaults to empty (reject all). Combined with default container-level filesystem isolation (or stronger with gVisor/Kata/Firecracker), an agent cannot reach arbitrary host paths unless an operator explicitly allowlists them.
`[storage].allowed_host_paths` — "list of absolute path prefixes for host bind mounts (empty = reject all, default)".
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — "`allowed_host_paths` — list of absolute path prefixes for host bind mounts (empty = reject all, default)"

### credential_theft
credential_theft: Partial — a "Credential Vault" component injects outbound-request credentials (bearer/basic/API-key/custom headers, with scoped placeholder substitution) into MITM-proxied traffic without exposing the real secret value to the sandboxed workload process. This covers one narrow scenario (outbound HTTP auth headers via the experimental MITM egress path). No documentation was found for host secret files, dotfiles, or general credential-store isolation beyond that vault, and no ssh-agent/gpg-agent socket forwarding is documented (see `cred_forwarding` under axis G for the DX-facing angle on this same gap).
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/README.md — "Credential Vault: Secure credential injection for sandbox outbound requests without exposing real secrets"
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/egress.md — "automatic credential injection (bearer, basic, API-key, custom headers, and scoped placeholder substitutions)"

### data_exfiltration
data_exfiltration: Partial — when a sandbox is created with an explicit `networkPolicy`, the attached egress sidecar enforces default-deny (allowlist) at DNS and optionally IP layers, which does restrict exfiltration. But egress control is opt-in per sandbox: the sidecar is "attached only when a sandbox is created with `networkPolicy`" — an unconfigured sandbox has no egress sidecar and therefore no exfiltration restriction beyond whatever the underlying Docker/Kubernetes network mode provides by default (typically open egress). See `network_default_posture` under axis C for the full discriminator.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — "The egress sidecar is attached only when a sandbox is created **with** `networkPolicy`"

### malicious_execution
malicious_execution: Yes — code runs inside a container (or optional gVisor/Kata/Firecracker sandbox) rather than the host process space, with resource limits (`resourceLimits` API for CPU/mem/GPU, `pids_limit`) bounding a compromised or runaway workload. Blast radius still depends on which isolation runtime is selected (see `escape_resistance`).
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/architecture/index.md — "API supports `resourceLimits` for: CPU and memory, GPU, Platform constraints"

### escape_resistance
escape_resistance: Partial — default isolation runtime is plain `runc` (shared-kernel container, standard container-escape surface). Operators can opt into `gvisor`, `kata` (`kata-qemu`/`kata-clh`/`kata-fc`), or `firecracker` (Kubernetes-only) via `[secure_runtime]` for stronger syscall/hardware-virt isolation. However, there is a documented, real tradeoff: the egress network-isolation sidecar's transparent-interception approach "works with `runc` (default) and all Kata Containers variants... but not with gVisor" because gVisor's netstack lacks the `nat` table support the sidecar needs. So choosing gVisor for stronger syscall isolation forfeits the sidecar's transparent egress enforcement mechanism as documented — a genuine escape_resistance vs. network_control coupling gap.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/architecture/network-isolation.md — "works with `runc` (default) and all Kata Containers variants (`kata-qemu`, `kata-clh`, `kata-fc`), but not with gVisor" since gVisor's netstack lacks the `nat` table support needed for transparent interception

### resource_abuse
resource_abuse: Yes — per-sandbox `resourceLimits` (CPU/memory/GPU/platform constraints) via the API, Docker `pids_limit` (default 4096), `max_sandbox_timeout_seconds` server-side TTL cap, and Kubernetes "extended resources" translation for the k8s backend.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — "`pids_limit` (default 4096) — max processes per sandbox"; "`max_sandbox_timeout_seconds` — upper bound on sandbox TTL (≥60 if set)"

## C. Feature set & granularity

### network_default_posture
network_default_posture: Partial — neither a clean deny-by-default nor open-by-default answer. At the platform level, an unconfigured sandbox gets NO egress sidecar and thus no network restriction (effectively open, gated only by the chosen Docker network mode / k8s network policy, if any). Once an operator opts a sandbox into `networkPolicy` at creation time, that policy's own default mode is deny (`"defaultAction":"deny"` is the documented standard mode, and the DNS proxy is described with fail-closed sidecar startup). So the enforcement mechanism itself is deny-by-default when used, but using it is opt-in per sandbox rather than a platform-wide secure default.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — "The egress sidecar is attached only when a sandbox is created **with** `networkPolicy`"
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/egress.md — `"defaultAction":"deny"` as a standard policy mode; "Fail-Closed Enforcement: ...the sidecar exits if no enforced redirect can be installed"

### egress_allowlist
egress_allowlist: Yes — granularity ladder actually present: exact domain, wildcard subdomain (`*.pypi.org`), literal IP address, and CIDR range (`10.0.0.0/8`) targets; explicit allow/deny action with a `deny.always` file taking unconditional top priority over other rules; both a static rule-file mode (hot-reloaded ~once/minute) and a dynamic policy mode via `/policy` API (applies immediately). No HTTP path/method/regex rules are part of this mechanism (see `http_path_rules`), and no port-range scoping is documented for the FQDN/IP rules themselves.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/egress.md — "IP / CIDR Targets: Egress rules can also target literal IP addresses or CIDR ranges (e.g., `10.0.0.0/8`)"; "Allow subdomains using wildcards (e.g., `*.pypi.org`)"; "Always-rules are hot-reloaded: the sidecar polls the files once per minute and applies changes without restart"

### dns_level_blocking
dns_level_blocking: Yes — a DNS proxy on `127.0.0.1:15353` (with `iptables` redirect of port 53) filters queries against the allowlist and returns NXDOMAIN for denied domains before resolution occurs.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/egress.md — "Runs on `127.0.0.1:15353`"; "Filters queries based on the allowlist. Returns `NXDOMAIN` for denied domains."

### tls_mitm_inspection
tls_mitm_inspection: Partial — transparent HTTPS MITM via mitmproxy is offered for outbound 80/443 traffic in the sidecar's network namespace, primarily to power automatic credential injection, but it is explicitly labeled "Experimental" rather than a stable/core feature.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/egress.md — "Experimental: Transparent HTTPS MITM (mitmproxy): Optional transparent TLS interception for outbound `80/443` traffic in the sidecar network namespace"

### http_path_rules
http_path_rules: No — the egress/network-isolation docs describe only DNS-name and IP/CIDR targets; no path, method, or regex-level HTTP rule primitive is documented anywhere in the egress component or network-isolation architecture docs.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/egress.md — rule targets documented are domain/wildcard/IP/CIDR only; no path or method fields described

### proto_coverage
proto_coverage: Partial — controlled: DNS (proxy/NXDOMAIN), TCP/UDP via nftables in `dns+nft` mode, and experimental HTTP(S) MITM fixed to ports 80/443. Not documented as controlled: ICMP, QUIC/HTTP3, SSH, WebSocket, gRPC, or HTTP/2-specific handling — these protocols would only be gated indirectly via the coarse IP/CIDR or FQDN allow/deny rules (no protocol-aware policy for them), and IPv6 is disabled by default (`disable_ipv6 = true`) rather than filtered. No documented design for adding new/custom L7 protocols into the rule model — the mechanism as shipped is DNS+IP/CIDR-scoped, not protocol-extensible.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — "`disable_ipv6` (default true) — blocks IPv6; set false only on IPv4-only clusters or for experiments"; egress `mode`: "'dns' — DNS proxy; CIDR/static IP rules not enforced" / "'dns+nft' — adds nftables for CIDR/IP rule enforcement"

### live_rule_reload
live_rule_reload: Yes — static "always-rules" files are polled and hot-reloaded roughly once per minute without sidecar restart; dynamic policy changes via the `/policy` API endpoint apply immediately.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/egress.md — "Always-rules are hot-reloaded: the sidecar polls the files once per minute and applies changes without restart"; "Dynamic API rules apply immediately via `/policy` endpoints"

### firewall_escape_hatch
firewall_escape_hatch: Unknown — no documented timed-bypass-with-automatic-re-enforcement or per-sandbox live disable/enable toggle was found. The closest related mechanism is `OPENSANDBOX_EGRESS_NAMESERVER_EXEMPT`, which exempts specific DNS servers from filtering, not a general break-glass. Since egress enforcement itself is opt-in at sandbox-creation time (see `network_default_posture`), an operator could simply not request `networkPolicy` for a given sandbox, but that is a creation-time choice, not a documented runtime bypass for an already-policy-enforced sandbox. Treating as Unknown rather than No per guidelines (no explicit doc statement ruling it out was found, just absence in the material reviewed).
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/egress.md — "Nameserver Bypass: `OPENSANDBOX_EGRESS_NAMESERVER_EXEMPT`" is the only bypass-flavored mechanism documented

### enforcement_plane
enforcement_plane: prose — kernel-level, via a combination of a userspace DNS proxy (with a kernel `iptables` redirect forcing port-53 traffic to it) and kernel `nftables` for IP/CIDR-level enforcement (`dns+nft` mode). The sidecar "shares the network namespace with the sandbox application" rather than sitting at a separate network boundary (e.g. a host-level proxy outside the sandbox's namespace) — enforcement lives inside the same namespace as the workload, set up at container start. The sidecar is fail-closed at its own startup (exits if it cannot install the redirect). Denied traffic is observable via a webhook (`OPENSANDBOX_EGRESS_DENY_WEBHOOK`); a noise-reduction skip-list exists for expected/frequent DNS blocks so they aren't logged.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/egress.md — "The egress control is implemented as a Sidecar that shares the network namespace with the sandbox application"
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/architecture/network-isolation.md — "The control point lives inside the sandbox (egress sidecar), not at the cluster network layer."; "OpenSandbox egress expects to own outbound interception inside the pod network namespace."

### fail_closed
fail_closed: Partial — the egress sidecar itself is fail-closed at startup: "the sidecar exits if no enforced redirect can be installed" — so if the interception mechanism can't be set up, the sandbox doesn't come up with silently-open networking. However, the behavior of already-running sandboxes if the lifecycle *server* (control plane) crashes after they're up is explicitly not documented; the architecture doc discusses graceful restart recovery ("restore expiration timers for existing managed containers after server restart") but says nothing about whether an already-enforced egress sidecar keeps enforcing independently of the control plane (it likely does, since enforcement lives in-sidecar per the enforcement_plane finding, but this independence is not explicitly asserted in docs).
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/egress.md — "Fail-Closed Enforcement: DNS redirect setup is required through `iptables` or the native nft fallback; the sidecar exits if no enforced redirect can be installed."
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/architecture/index.md — control-plane-crash behavior for already-running sandboxes "not explicitly documented"

### network_audit
network_audit: Partial — denied requests can trigger a webhook (`OPENSANDBOX_EGRESS_DENY_WEBHOOK`), and there's a configurable skip-list to suppress logging of expected/noisy DNS blocks. No documentation was found describing a comprehensive per-request audit log covering both allowed and denied egress (the material found is deny-focused: webhook + skip-list), so this falls short of a full audit trail as described in the criterion.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/egress.md — "Denied hostname webhook: `OPENSANDBOX_EGRESS_DENY_WEBHOOK`, `OPENSANDBOX_EGRESS_SANDBOX_ID`"; "Domain patterns whose DNS blocks are not logged (noise reduction)" via `log_skip.always` file

### workspace_modes
workspace_modes: Yes — three runtime-neutral volume models: `host` (bind-mount a host path, live/two-way, gated by `allowed_host_paths` allowlist), `pvc` (platform-managed persistent storage — Docker named volume or Kubernetes PVC), and `ossfs` (Alibaba Cloud OSS object-storage mount). Live bind-mount and managed-persistent-volume modes both exist; there's no distinct "ephemeral snapshot copy-in" mode as such, though pause/resume (see `snapshots_persistence`) provides a related capability.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/architecture/index.md — "Three runtime-neutral volume models: `host`: Bind permitted host paths. `pvc`: Platform-managed named storage... `ossfs`: Alibaba Cloud OSS mounts"

### observability
observability: Yes — `execd` exposes `GET /metrics` (point-in-time host metrics) and `GET /metrics/watch` (1s-cadence SSE stream), plus optional OpenTelemetry metrics export; the lifecycle server exposes a diagnostics API; ingress/egress/execd all support logs; request IDs are propagated across components for debugging.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/execd.md — "`GET /metrics`: point-in-time host metrics snapshot"; "`GET /metrics/watch`: SSE stream (1s cadence)"
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/architecture/index.md — "execd exposes metrics, the server exposes diagnostics, ingress/egress/execd support logs and OpenTelemetry metrics where implemented, and request IDs are propagated for debugging"

### supervision
supervision: Partial — the lifecycle server tracks sandbox state (`state`, `reason`, `message`, transition time) and exposes lifecycle operations (create/pause/resume/delete), which is more than passive metrics-watching. But no documented active-containment capability beyond ordinary lifecycle calls was found (e.g. no described "quarantine mid-session" or automated policy-driven intervention triggered by observed behavior) — this reads as state-tracking + manual lifecycle control rather than a supervisor that autonomously intervenes.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/architecture/index.md — "Sandbox state includes `state`, `reason`, `message`, and transition time"

### fleet_mgmt
fleet_mgmt: Yes — Kubernetes runtime backend supports "large-scale distributed scheduling" with a pluggable `workload_provider` (`batchsandbox` or `agent-sandbox`); sandboxes are individually identified/tracked; a SQLite store (`~/.opensandbox/opensandbox.db`) persists sandbox/snapshot metadata for the server.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/README.md — "high-performance Kubernetes runtime, enabling both local runs and large-scale distributed scheduling"
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — "`workload_provider` ('batchsandbox' or 'agent-sandbox')"

### snapshots_persistence
snapshots_persistence: Partial — pause/resume with actual image-snapshotting is documented for both backends: Docker does container-level pause/resume with commit-to-local-image; Kubernetes' `BatchSandbox` "pause commits the sandbox root filesystem to an OCI image and releases runtime resources; resume rewrites the workload template to use the snapshot image... while preserving the sandbox ID." However, a third-party review (Northflank, marked as such) states persistent storage that survives restarts is "on OpenSandbox's roadmap but not yet available" — this appears to refer to durable data volumes distinct from the documented rootfs pause/resume snapshot mechanism, but official docs don't reconcile the two, so treating durable/general persistence as not fully proven.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/architecture/index.md — "pause commits the sandbox root filesystem to an OCI image and releases runtime resources; resume rewrites the workload template to use the snapshot image and recreates the runtime while preserving the sandbox ID"
- https://northflank.com/blog/alibaba-opensandbox-architecture-use-cases — third-party: "Persistent storage: stateful agent sessions need volumes or databases that survive restarts; this is on OpenSandbox's roadmap but not yet available"

## D. Setup
### setup
setup: Moderate — requires Python 3.10+ and either Docker Engine 20.10+ or Kubernetes 1.21.1+ (Linux/macOS/Windows-with-WSL2). Multiple separate installable components: `pip install opensandbox-server`, `opensandbox-cli`, a language SDK (e.g. `pip install opensandbox`), and — for network policy — an egress sidecar OCI image referenced in config. Config is generated via `opensandbox-server init-config ~/.sandbox.toml --example docker` then edited. No first-party statement of total setup time; it's a multi-package pip install plus TOML authoring rather than a single-binary/one-liner install.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/getting-started/installation.md — "Python 3.10+"; "Docker Engine 20.10+"; "Kubernetes 1.21.1+"; "Linux, macOS, or Windows with WSL2"
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — "opensandbox-server init-config ~/.sandbox.toml --example docker"

## E. Daily use
### daily_use
daily_use: Moderate — the `osb` CLI covers the common loop (`osb sandbox create`, `osb command run`, file operations, diagnostics, egress-policy inspection), and `execd` provides PTY-over-WebSocket for interactive sessions plus persistent bash sessions. But the project reads primarily as an SDK/API-first building block for platform teams (multi-language SDKs, OpenAPI-driven protocol, MCP server) rather than a turnkey "wrap my current terminal session in a sandbox" CLI experience; no documented single-command "attach me to a running sandbox shell against my current repo" workflow comparable to a `docker exec -it`-style shortcut was found.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/README.md — "osb sandbox create --image python:3.12 --timeout 30m -o json"; "osb command run <sandbox-id>"
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/execd.md — "PTY over WebSocket (`/pty`)"; persistent bash sessions

## F. Configuration
### config_depth
config_depth: Deep — TOML config (`~/.sandbox.toml`, overridable via `SANDBOX_CONFIG_PATH` or `--config`) with sections `[server]`, `[log]`, `[docker]`, `[kubernetes]`, `[agent_sandbox]`, `[ingress]`, `[egress]`, `[storage]`, `[store]`, `[secure_runtime]`, `[renew_intent]`, `[otel]`, each with multiple keys (capability drops, AppArmor/seccomp profiles, pids limits, egress mode/image, storage allowlists, secure-runtime selection, OTel export, etc.), plus per-sandbox-creation API parameters (`resourceLimits`, `networkPolicy`, volume specs, timeout). Cross-field validation rules are documented (e.g. Docker runtime can't combine with `[kubernetes]`/firecracker; gateway ingress needs `[ingress.gateway]`).
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — full section list and cross-field validation rules as summarized above

### policy_model
policy_model: Moderate — mostly policy-driven at the per-sandbox/per-resource level (storage bind-mounts default to reject-all and must be allowlisted; isolation runtime is chosen per sandbox via `[secure_runtime]`/API; egress policy is chosen per sandbox via `networkPolicy`), but there is no global "secure by default" network posture — an operator who doesn't explicitly request `networkPolicy` gets a sandbox with no egress restriction at all (see `network_default_posture`). So control knobs exist and are granular, but the safe default is opt-in rather than opt-out for the network axis specifically.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — storage allowlist defaults reject-all; egress sidecar attached only when `networkPolicy` requested

## G. DX — host↔sandbox integration
### bind_mount_sharing
bind_mount_sharing: Yes — `host` volume mode bind-mounts host paths live (two-way) into the sandbox, restricted to prefixes in `[storage].allowed_host_paths`.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/architecture/index.md — "`host`: Bind permitted host paths"

### cred_forwarding
cred_forwarding: Partial — the "Credential Vault" injects outbound HTTP request credentials (bearer/basic/API-key/custom headers) into MITM-proxied traffic without exposing the raw secret to the sandboxed process — a narrower, HTTP-egress-specific mechanism. No documentation was found for ssh-agent socket forwarding, gpg-agent forwarding, or a git-credential-helper bridge for interactive `git`/`ssh` workflows inside the sandbox (searched README, execd docs, egress docs, config reference).
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/README.md — "Credential Vault: Secure credential injection for sandbox outbound requests without exposing real secrets"
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/execd.md — no ssh-agent/gpg/git-credential forwarding mentioned in the execd capability list

### browser_auth
browser_auth: Unknown — no documentation describing a host-browser-open proxy mechanism (for OAuth/device-code login flows triggered from inside the sandbox) was found across the README, getting-started, architecture, or component docs reviewed.
Sources:
- (searched, absent) https://raw.githubusercontent.com/alibaba/OpenSandbox/main/README.md and https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/architecture/index.md — no mechanism of this kind described

### shared_dirs
shared_dirs: Yes — same mechanism as `bind_mount_sharing`; `allowed_host_paths` is a list, so multiple host directories can be allowlisted beyond a single workspace root.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — "`allowed_host_paths` — list of absolute path prefixes for host bind mounts"

### git_worktrees
git_worktrees: Unknown — no mention of git-worktree-aware handling found in any official doc surface reviewed (README, getting-started, architecture, components).
Sources:
- (searched, absent) across README.md, docs/architecture/*, docs/components/* — no worktree references found

### nested_containers
nested_containers: Unknown — no documentation found confirming or denying Docker-socket/DinD access from inside a sandbox workload. The lifecycle server itself requires a host Docker daemon connection (`runtime.type = "docker"`), but whether that access is exposed to the sandboxed workload (vs. only to the server/runtime-provider layer) is not addressed in the docs reviewed.
Sources:
- (searched, inconclusive) https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/components/execd.md — no docker-socket/DinD reference found in execd capability documentation

### harness_agnostic
harness_agnostic: Yes — README explicitly lists integrations/examples for Claude Code, Cursor, Gemini CLI, OpenAI Codex CLI, Qwen Code, Kimi CLI, LangGraph, Google ADK, and OpenClaw, plus a generic MCP server so any MCP-capable client can drive it.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/README.md — "Supported Integrations: Claude Code, Cursor, Gemini CLI, OpenAI Codex CLI, Qwen Code, Kimi CLI, LangGraph, Google ADK, OpenClaw"

## H. Performance
### performance
performance: Unknown — no first-party startup-latency, disk-footprint, RAM-overhead, or IO-throughput benchmarks were found in the official docs. A third-party comparison piece (Northflank, marked as such) discusses container cold-start factors in general terms ("end-to-end sandbox creation involves image pulling, execd injection, container start, and runtime initialisation. This is longer than VMM boot time alone") but gives no OpenSandbox-specific numbers, and cites its own competing product's timing (~1-2s) instead. Per guidelines, no numbers found means say so rather than estimate.
Sources:
- https://northflank.com/blog/alibaba-opensandbox-architecture-use-cases — third-party: "end-to-end sandbox creation involves image pulling, execd injection, container start, and runtime initialisation. This is longer than VMM boot time alone" (general commentary, not an OpenSandbox-specific benchmark)

## I. Feasibility
### feasibility
feasibility: Moderate — cross-platform (Linux/macOS/Windows-via-WSL2), Apache-2.0 with no vendor lock-in, and adoptable today for teams willing to self-host Docker or Kubernetes infrastructure. Counterweights: young project (~4 months old as of 2026-07-18), a third-party review notes teams "take on responsibility" for lifecycle orchestration, multi-tenancy enforcement, scaling, and observability beyond what's built in, and persistent-storage durability is called out (third-party) as still roadmap. Reads as more practical for a platform/infra team building agent tooling than as a zero-config solo-developer sandbox.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/docs/getting-started/installation.md — "Linux, macOS, or Windows with WSL2"
- https://northflank.com/blog/alibaba-opensandbox-architecture-use-cases — third-party: teams using OpenSandbox "take on responsibility" for lifecycle orchestration, multi-tenancy enforcement, scaling, and observability

## J. Price (prose-only)
Apache 2.0, free and open source; no first-party managed/hosted SaaS tier was found. Cost model is entirely "bring your own infrastructure" — a developer's own Docker host or an organization's own Kubernetes cluster. No pricing page or paid tier exists for the OpenSandbox project itself (distinct from generic Alibaba Cloud compute pricing, which is unrelated to the OpenSandbox project specifically).
Sources:
- https://github.com/alibaba/OpenSandbox — License: Apache 2.0, no pricing/billing pages found on the repository

## K. Extensibility
### extensibility
extensibility: Yes — the project explicitly defines a "Sandbox Protocol" (OpenAPI-based lifecycle-management + execution APIs) intended so operators "can extend custom sandbox runtimes" beyond the built-in Docker/Kubernetes providers. Isolation runtime is pluggable (`[secure_runtime]`: gVisor/Kata/Firecracker via Docker OCI runtime name or Kubernetes RuntimeClass). Custom OCI images can be used for `execd`/workloads. Multiple official SDKs are generated/maintained against the same OpenAPI contract (Python, Java/Kotlin, JS/TS, C#/.NET, Go), and an MCP server exposes the same operations to MCP clients.
Sources:
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/README.md — "Sandbox Protocol: Defines sandbox lifecycle management APIs and sandbox execution APIs so you can extend custom sandbox runtimes"
- https://raw.githubusercontent.com/alibaba/OpenSandbox/main/server/configuration.md — `[secure_runtime]` pluggable runtime selection

## Unknowns & caveats
- **Identity**: treated as `github.com/alibaba/OpenSandbox` per the assignment's "same rule as SmolVM" instruction (identity needed confirming). Confidence is reasonably high (Alibaba-authored, Apache 2.0, matches the "general-purpose AI-agent sandbox platform" description used across independent third-party coverage e.g. MarkTechPost, Northflank), but a few same/similar-named repos exist that were NOT verified as forks-of vs. independent (`atarashansky/opensandbox`, `leo0481/2026_OpenSandbox`) and were excluded from this assessment.
- **Control-plane-crash fail behavior for already-running sandboxes**: architecture docs explicitly say this is not documented (only restart-recovery of timers is described). Left as an open gap rather than assumed fail-open or fail-closed.
- **firewall_escape_hatch, browser_auth, git_worktrees, nested_containers/docker-socket, cred_forwarding (ssh/gpg specifically)**: all Unknown/Partial due to docs silence, not confirmed absence — flagged per guidelines rule that silence ≠ No.
- **performance**: no first-party benchmarks published; not estimated.
- **snapshots_persistence**: tension between documented pause/resume-to-OCI-image (official docs, works) and a third-party claim that durable persistent storage is still roadmap — not reconciled by official docs, so treated as Partial rather than a clean Yes.
- No URLs were blocked by network/firewall issues during this research — all fetches to github.com/alibaba/OpenSandbox (raw + tree views) and the third-party Northflank article succeeded.

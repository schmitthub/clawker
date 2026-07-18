# Blaxel
category: cloud
Managed cloud platform providing per-agent microVM sandboxes with programmable networking (MITM proxy, domain allow/deny lists), persistent snapshotting, and SDK/CLI tooling for autonomous coding agents | built on: Firecracker-class microVMs (exact hypervisor not named in fetched docs) | license: platform closed-source SaaS; `blaxel-ai/sandbox` template/runtime-image repo is MIT | maturity: networking (proxy/domain-filter/egress-gateway) features shipped private-preview 2026-03, GA "all regions" 2026-04 — young feature set; core sandbox/snapshot product older and more established
as-of: 2026-07-18

## A. Identity

### built_on (prose-only)
Blaxel sandboxes are described consistently across official site and docs as isolated microVMs, one per sandbox, with sub-25ms resume from standby. The specific VMM (Firecracker vs. Cloud Hypervisor vs. proprietary) is never named in the pages fetched — only "microVM" generically, plus the claim that "each workload runs its own kernel" for hardware-enforced tenant isolation. Filesystem is layered: EROFS read-only base from host storage + OverlayFS/tmpfs writable layer in RAM (~50% of allocated memory), with optional disk-backed root (`storageMb`) or attached persistent volumes.
Sources:
- https://blaxel.ai/ — "isolated microVMs that boot in milliseconds and resume in ~25ms, persistent shared memory, and programmable networking"
- https://docs.blaxel.ai/Sandboxes/Overview — "Blaxel automatically creates a snapshot of the entire state (including the complete file system in memory...)"
- https://blaxel.ai/blog/ai-sandbox — "MicroVMs provide the strongest security boundary because each workload runs in its own kernel to prevent the container escape vulnerabilities that affect shared-kernel approaches" (vendor blog)

### execution_locality
Remote. Sandboxes run exclusively on Blaxel's managed cloud infrastructure (region selection like `us-pdx-1`); no self-hosting / on-prem deployment option was found anywhere in docs, pricing, or product pages. Project code, agent execution, and any secrets routed through the proxy leave the developer's machine and run on Blaxel-operated compute; local machine only runs the CLI/SDK client that talks to the remote API.
Sources:
- https://docs.blaxel.ai/Sandboxes/Overview — region field examples (`us-pdx-1`) in sandbox creation
- https://blaxel.ai/pricing — no self-host tier listed; only "Custom" plan for "dedicated & custom deployments" (still Blaxel-operated, not confirmed on-prem)

### open_source (prose-only)
The `blaxel-ai/sandbox` GitHub repo (MIT license) contains only the collection of sandbox *template* Dockerfiles/images (base image, Python/TypeScript/Expo apps) and is not the control plane, proxy, scheduler, or VMM. The core platform (orchestration, proxy/MITM engine, billing, workspace/RBAC) is closed-source SaaS with no self-host distribution found.
Sources:
- https://github.com/blaxel-ai/sandbox — "MIT License" governing the templates repo; "collection of development environment templates for creating secure, isolated micro VM environments"

### maturity (prose-only)
Company/product markets itself as SOC 2 Type II, HIPAA-compliant, ISO 27001-certified with a compliance portal. The flagship network-security features for this comparison (proxy MITM injection, domain allow/deny filtering, dedicated egress gateways) are explicitly labeled "private preview" as of 2026-03-26 in the changelog and reached "all regions" only 2026-04-26 — i.e., under 4 months old as of this writeup, and the proxy-secrets-injection docs page itself still carries a "not recommended for production use" notice.
Sources:
- https://blaxel.ai/ — "SOC 2 Type II," "HIPAA Compliant," "ISO 27001 Certified"
- https://docs.blaxel.ai/changelog — "Added new sandbox networking features in private preview: proxy routing... domain filtering (allowlist/denylist firewalls)" (2026-03-26); "Proxy and egress gateway now available in all regions" (2026-04-26)
- https://docs.blaxel.ai/Sandboxes/Proxy-secrets-injection — "This feature is currently in public preview and is not recommended for production use."

## B. Threat protection

### host_fs_damage
Yes — no host filesystem exists to damage in the traditional sense since execution is remote; the microVM's own root filesystem (EROFS base + RAM overlay) is isolated per-sandbox with hardware/kernel-level separation from other tenants and from Blaxel's host infrastructure, per vendor's stated model.
Blaxel is a remote multi-tenant cloud, so "host" here means Blaxel's fleet hosts, not the developer's machine — irrelevant to local dev-machine damage by construction (execution never touches the developer's disk).
Sources:
- https://blaxel.ai/blog/ai-sandbox — "a multi-tenant platform must ensure that even if an attacker gains full control within a sandbox, they cannot access the hypervisor or other tenants' VMs" (vendor blog, describing the model Blaxel says it follows)

### credential_theft
Partial — the proxy-based secrets injection (`{{SECRET:name}}` resolved server-side, "sandbox runtime never sees raw secret values") mediates *outbound API-call* credentials well, but this is opt-in per routing rule; secrets not routed through the proxy (e.g. baked into env vars via `envs` at creation, or into a Dockerfile/`.env.build`) are directly visible to any code running in the sandbox, and the docs explicitly warn "Never add secrets in a Dockerfile."
The mediated path is real but narrow (HTTPS proxy rules only); anything not funneled through a configured `ProxyTarget` (ssh keys, generic tokens set as plain env vars, git credentials) sits in the sandbox unmediated.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy-secrets-injection — "Secrets are resolved server-side by the proxy — the sandbox runtime never sees raw secret values."
- https://docs.blaxel.ai/Sandboxes/Variables-and-secrets — "Never add secrets in a Dockerfile" / build-time secrets "injected during the build phase only and never persisted in the runtime environment"

### data_exfiltration
Partial — domain allow/deny lists (`allowedDomains`/`forbiddenDomains`) exist and are opt-in per sandbox, but enforcement is via `HTTP_PROXY`/`HTTPS_PROXY` env-var convention rather than network-layer interception, and the docs state plainly that non-compliant tools bypass it entirely, with true network-layer enforcement explicitly future work.
This is the single most load-bearing finding for this provider: "Traffic from tools that ignore these variables will not be filtered. Routing-level enforcement is planned for a future release." A raw socket, a tool that ignores proxy env vars, or any non-HTTP(S) protocol currently exfiltrates uncontrolled even with domain filtering configured.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy-domains — "Traffic from tools that ignore these variables will not be filtered. Routing-level enforcement is planned for a future release."
- https://docs.blaxel.ai/Sandboxes/Proxy — "Local traffic (localhost, private IP ranges) are always bypassed automatically."

### malicious_execution
Yes — vendor's stated design goal is exactly this: microVM-per-sandbox with kernel isolation means a compromised/hallucinated-code process inside one sandbox cannot pivot to the hypervisor or other tenants, per vendor's architecture description. No independent (non-vendor) confirmation or third-party escape research was found.
Sources:
- https://blaxel.ai/blog/ai-sandbox — "For AI agents, all LLM-generated code is treated as potentially malicious, so it requires hardware-level isolation through microVMs rather than traditional containers that share the host kernel" (vendor blog)

### escape_resistance
Partial — architecture claim is strong (microVM > shared-kernel container, per vendor), but two caveats pull it off a clean "Yes": (1) the exact hypervisor/VMM is never named in official docs so the claim can't be independently checked against known CVEs for that specific technology; (2) Docker-in-Docker is offered as a first-class template (`blaxel/docker-in-sandbox`), meaning a user can opt into nested containers whose isolation reverts to shared-kernel semantics inside the outer microVM boundary.
No mention of any published third-party security audit or pentest of the microVM boundary was found in the fetched docs.
Sources:
- https://blaxel.ai/blog/ai-sandbox — "Containers share the host operating system kernel across workloads, and a kernel vulnerability in one container can expose other tenants on the same host" (contrast used to justify Blaxel's microVM choice)
- https://docs.blaxel.ai/Sandboxes/Templates — `blaxel/docker-in-sandbox:latest` — "Docker environment" (nested container support)

### resource_abuse
Yes — memory is set per sandbox at creation and CPU is allocated proportionally to memory ("CPU resources are allocated accordingly by Blaxel based on your selected memory allocation and are not charged separately" — i.e., no independent CPU dial, but it is capped); concurrent/standby sandbox counts, job parallelism, and storage sizes are tier-gated quotas. Exact numeric limits are gated behind the authenticated console (`app.blaxel.ai/account/quotas`), not published in public docs.
Sources:
- https://docs.blaxel.ai/Sandboxes/Overview — "You are charged for memory...and storage while a sandbox is in active mode. CPU resources are allocated accordingly by Blaxel based on your selected memory allocation and are not charged separately."
- https://docs.blaxel.ai/Security/Quotas — "higher tiers unlock higher limits such as more standby/concurrent sandboxes, more concurrent jobs, higher sandbox and volume sizes, longer sandbox TTLs" (exact numbers not published, only in-console)

## C. Feature set & granularity

### network_default_posture
Partial/Unknown — no page explicitly states "sandboxes can reach the internet unrestricted unless configured," but every piece of structural evidence points to open-by-default: domain filtering (`allowedDomains`/`forbiddenDomains`) is an optional field on the `SandboxNetwork` object set at creation, the proxy/env-var mechanism must be actively wired up, and iptables inside the guest kernel is likewise off unless requested via `extraArgs`. No default-deny/allowlist-mode stance is documented anywhere.
Torn between "open by default" (structurally implied) and "unknown" (never explicitly stated) — leaning open-by-default per the config shape, but this is inference, not a direct quote, so treat as provisional.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy-domains — `allowedDomains`/`forbiddenDomains` documented as optional configuration fields, not defaults
- https://docs.blaxel.ai/Sandboxes/Overview — "By default, sandboxes run on a minimal kernel without iptables support... You can enable iptables by passing extraArgs at creation time" (a different, guest-kernel-internal iptables toggle, cited to show the general pattern of network features being opt-in)

### egress_allowlist
Partial — a real allowlist/denylist exists (`allowedDomains` wins over `forbiddenDomains` when both set), with wildcard subdomain support (`*.example.com`). Granularity stops at the domain/wildcard-domain level: no IP/CIDR scoping, no port scoping, no path/method/regex rules were found, and enforcement is env-var/proxy-based (bypassable — see data_exfiltration) rather than network-layer.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy — "Allowlist (allowedDomains): Only specified domains are reachable" / "Denylist (forbiddenDomains): All domains except specified ones are reachable" / "The allowlist takes precedence when both are configured."
- https://docs.blaxel.ai/Sandboxes/Proxy-domains — wildcard examples `*.s3.amazonaws.com`, `*.malware.com`; "Domain filtering operates at the domain level only" (no path-level rules found)

### dns_level_blocking
No — no DNS-layer blocking mechanism is documented anywhere; filtering happens (per docs) at the HTTP(S) proxy layer via env vars, which is downstream of DNS resolution, not at resolution time. A tool that resolves a blocked domain and connects without honoring the proxy env vars is explicitly stated to bypass filtering entirely, which structurally rules out DNS-level enforcement as currently implemented.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy-domains — "Traffic from tools that ignore these variables will not be filtered" (confirms enforcement point is not DNS)

### tls_mitm_inspection
Yes — the proxy explicitly does TLS MITM: it installs a CA certificate into the sandbox (`NODE_EXTRA_CA_CERTS`/`SSL_CERT_FILE`) and intercepts outbound HTTPS to inject headers/body/secrets server-side.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy — "performs man-in-the-middle (MITM) interception on outbound HTTPS traffic and injects headers, body fields, and secrets server-side"; "Installs a CA certificate and sets NODE_EXTRA_CA_CERTS and SSL_CERT_FILE"

### http_path_rules
No — domain filtering docs explicitly scope the mechanism to domain-level matching; no path, method, or regex rule fields were found in the `SandboxNetwork`/`ProxyTarget` schema across the Proxy and Proxy-domains pages.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy-domains — filtering documented via `allowedDomains`/`forbiddenDomains` domain patterns only; no path/method fields present in the described schema

### proto_coverage
No — the entire enforcement mechanism (env-var `HTTP_PROXY`/`HTTPS_PROXY`, CA cert injection) is HTTP/HTTPS-specific, with only passing mention of gRPC-over-HTTP. Nothing in the fetched docs describes control over DNS, ICMP, raw TCP, UDP/QUIC, or SSH traffic; by construction of the proxy-env-var mechanism, non-HTTP(S) protocols cannot be routed through it and would pass unfiltered (consistent with the "tools that ignore these variables will not be filtered" statement). No documented extensibility path for adding new L7 protocols to the rule model was found.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy — env vars are `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` only; "Most HTTP clients work transparently including curl, pip, npm, git" (all HTTP-based tools)

### live_rule_reload
Unknown — no page addresses whether `allowedDomains`/`forbiddenDomains` can be changed on a running sandbox versus being fixed at creation time (unlike the guest-kernel `extraArgs`/iptables toggle, which is explicitly creation-time-only and immutable). Searched Proxy, Proxy-domains, and Standby-control pages; none state either way.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy-domains — no statement on mutability of domain filter rules post-creation (checked, absent)

### firewall_escape_hatch
Unknown — no documented timed-bypass or per-sandbox disable/enable toggle for the domain filter was found; the only "escape hatch" that IS documented is structural (any tool that ignores the proxy env vars bypasses filtering by default), which is an unintentional gap rather than a controlled break-glass mechanism.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy-domains — no bypass/escape-hatch language found in the fetched page content

### enforcement_plane
No (weak plane) — enforcement is userspace/application-layer: an HTTP(S) forward-proxy the sandbox's HTTP client libraries are pointed at via standard `HTTP_PROXY`/`HTTPS_PROXY` env vars, not kernel-level (eBPF/netfilter), not VM/hypervisor-boundary, and not transparently intercepted network infra. The docs' own words confirm the sandbox can trivially route around it (any tool/library ignoring the env vars, or using raw sockets) and that "routing-level enforcement is planned for a future release" — i.e., Blaxel itself frames the current mechanism as pre-routing-level.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy-domains — "Routing-level enforcement is planned for a future release."
- https://docs.blaxel.ai/Sandboxes/Proxy — "Sets HTTP_PROXY, HTTPS_PROXY... environment variables inside the sandbox" (app-layer convention, not transparent redirect)

### fail_closed
Unknown — no documentation addresses what happens to domain-filtering/proxy enforcement if Blaxel's control plane or proxy service itself becomes unavailable (fail open vs fail closed). Searched Proxy, Proxy-domains, Standby-control, and changelog; none address supervisor/control-plane failure modes.
Sources:
- https://docs.blaxel.ai/Sandboxes/Standby-control — page covers active/standby connection lifecycle only, no control-plane-failure discussion (checked, absent)

### network_audit
Unknown — general process stdout/stderr log streaming is documented (Console dashboard, real-time callback streaming), but no page describes a per-request egress log (which domains/URLs were allowed or blocked, timestamps, request IDs tied to policy decisions) specific to the domain filter or proxy. The proxy does add an `X-Blaxel-Request-Id` tracing header per request, which is adjacent to but not confirmed as an audit log.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy — "The proxy adds an X-Blaxel-Request-Id header to every proxied request for tracing purposes" (tracing, not confirmed as a queryable audit log of allow/deny decisions)
- https://docs.blaxel.ai/Sandboxes/Log-streaming — covers process stdout/stderr streaming to Console, not network-request-level audit

### workspace_modes
Partial — no local host bind-mount concept applies (execution is remote — see execution_locality), so the "live bind mount" half of this criterion is structurally N/A rather than absent by choice. What IS documented: an ephemeral in-RAM root filesystem by default (EROFS base + tmpfs overlay), with opt-in disk-backed persistence via `storageMb` or attached Volumes, and full-state (filesystem + process) snapshotting automatically on transition to standby.
Sources:
- https://docs.blaxel.ai/Sandboxes/Overview — "Writable layer: tmpfs entirely in RAM via OverlayFS" / "Optional persistent storage: Disk-backed root via storageMb parameter or mounted volumes"

### observability
Yes — Console dashboard shows per-sandbox process logs; OpenTelemetry-based metrics/logs/traces are automatic ("Instrumentation happens automatically when workloads run on Blaxel"), though traces are sampled at only 10% of executions. SDK offers real-time log streaming callbacks (`onLog`/`onStdout`/`onStderr`) that backfill history then stream live.
Sources:
- https://docs.blaxel.ai/Sandboxes/Log-streaming — "visible in the Blaxel Console" Logs section; streaming "first backfills with all past logs before beginning the real-time stream"
- https://docs.blaxel.ai/Security/Data-collection-and-privacy — "only collects and saves traces for a sampled 10% of all your executions"

### supervision
Partial — the control plane can stop/delete/manage sandboxes via API/CLI (external lifecycle control exists), but no documentation describes an autonomous runtime supervisor that observes in-sandbox agent *behavior* and actively intervenes (e.g., killing a process mid-exfiltration attempt, quarantining on a detected policy violation). What exists is user/operator-triggered lifecycle management, not behavior-triggered containment.
Sources:
- https://docs.blaxel.ai/Sandboxes/Standby-control — describes active/standby transitions and manual keep-alive, no autonomous intervention logic documented

### fleet_mgmt
Unknown — sandboxes are individually named/creatable/addressable through the CLI/SDK/API within a workspace, and the API reference confirms "Management API" covers agents/functions/policies/jobs, but a dedicated list-sandboxes endpoint could not be directly confirmed (attempted URL 404'd) and no fleet-level grouping, bulk-operation, or registry features beyond basic per-resource CRUD were found in the fetched pages.
Sources:
- https://docs.blaxel.ai/api-reference/introduction — "Management API" enables management of "agents, functions, policies and much more" (general statement; fleet-specific grouping/bulk ops not confirmed)

### snapshots_persistence
Yes — full filesystem + running-process state snapshotting is automatic on the ~15s transition to standby, with sub-25ms resume restoring both; "persist sandboxes forever across sessions, resuming them near-instantly even after months." Caveat: external network connections (DB pools, message queues) do NOT survive a snapshot/resume cycle and must be re-established.
Sources:
- https://docs.blaxel.ai/Sandboxes/Overview — "Blaxel automatically creates a snapshot of the entire state (including the complete file system in memory, preserving both files and running processes)"; "The snapshot preserves process and filesystem state, but not external network connections... which will automatically time out and close"
- https://blaxel.ai/sandbox — "Persist sandboxes forever across sessions, resuming them near-instantly even after months."

## D. Setup
setup: Moderate — signup at app.blaxel.ai, install CLI (one-line brew/curl/PowerShell installer, cross-platform), `bl login`, then `npm install @blaxel/core` or `pip install blaxel` and write a few lines of SDK code (or `bl new sandbox`) to create a sandbox. No Docker/Kubernetes needed locally since execution is remote, but it does require a Blaxel account/API key and network access to Blaxel's cloud — heavier than a single local binary, lighter than standing up your own infra.
Sources:
- https://docs.blaxel.ai/Get-started — "brew install blaxel-ai/blaxel/blaxel" (Mac); curl installer for Linux; PowerShell installer for Windows; `bl login` step

## E. Daily use
daily_use: Moderate — sandboxes are connection-driven: they stay "active" only while a WebSocket/TCP connection exists (or a keep-alive process is running), auto-transition to no-cost "standby" after inactivity, and resume in ~25ms on reconnect. This is convenient for cost but adds a mental model burden (idle timeouts, need for explicit keep-alive to avoid unwanted standby, snapshot excludes live network connections which must be re-opened after resume) not present in an always-on local dev container.
Sources:
- https://docs.blaxel.ai/Sandboxes/Standby-control — "Sandboxes stay active as long as there's an active connection to them, typically through a WebSocket connection"; "you can also use process keep-alive to keep the sandbox running... even if there isn't an active connection"

## F. Configuration
### config_depth
config_depth: Moderate-deep — `blaxel.toml` is a declarative, versionable per-project config covering env vars, runtime memory overrides, triggers, and volumes; sandbox image is either a prebuilt template or a custom Dockerfile (with a required `sandbox-api` binary injected). Escape hatches exist (custom Dockerfile, guest-kernel iptables via `extraArgs`) but several settings are creation-time-immutable (env vars, `extraArgs`), and network/proxy rules are configured separately from `blaxel.toml` via the `SandboxNetwork`/`ProxyTarget` objects at the API/SDK level.
Sources:
- https://docs.blaxel.ai/llms-full.txt — "blaxel.toml" as declarative config; "[runtime] section lets you override agent deployment parameters: memory (in MB) to allocate"
- https://docs.blaxel.ai/Sandboxes/Variables-and-secrets — "Environment variables cannot be added or changed after a sandbox is created."

### policy_model
policy_model: Involved (partway toward policy-driven, not fully) — some controls ARE genuinely policy-like and per-instance-tunable (domain allow/deny per sandbox, custom Dockerfile per template, optional disk-backed persistence via `storageMb`), but several load-bearing settings are fixed at creation with no runtime toggle (env vars, guest iptables `extraArgs`), and the flagship security control (domain filtering) currently has no documented live-reload or break-glass bypass — it's set-once, not dial-able mid-session. Workspace-level "Policies" exist as an admin-manageable resource type but their content/scope wasn't confirmed in fetched docs.
Sources:
- https://docs.blaxel.ai/Sandboxes/Overview — "extraArgs can only be set when creating a sandbox. It cannot be changed after creation."
- https://docs.blaxel.ai/Security/Workspace-access-control — "creating and editing policies" listed as an admin-only capability (content of policies not detailed in fetched excerpt)

## G. DX — host↔sandbox integration
### bind_mount_sharing
No — execution is remote (see execution_locality), so there is no host filesystem to live-bind-mount; getting code into a sandbox means baking it into a custom Docker image, having the agent `git clone` from inside the sandbox, or writing files via the SDK's filesystem API. No continuous host↔sandbox live-sync mechanism is documented.
Sources:
- https://docs.blaxel.ai/Sandboxes/Filesystem — page documents file CRUD operations (read/write/copy/delete/watch) via SDK/API only; no host-sync/upload/mount mechanism described

### cred_forwarding
Partial — (corrected 2026-07-18, attribution audit) no ssh-agent/GPG socket forwarding is documented, but the proxy-mediated secret injection (`{{SECRET:name}}` resolved server-side into outbound HTTPS requests, "the sandbox runtime never sees raw secret values") IS exactly the rule's own "proxy header-injection with sentinel values that never expose the raw secret inside the sandbox" category — a real mediated-forwarding mechanism, just scoped to configured `ProxyTarget` HTTPS routes rather than general-purpose like ssh-agent/GPG, and it doesn't cover git-over-SSH or commit signing.
Sources:
- https://docs.blaxel.ai/Sandboxes/Proxy-secrets-injection — secrets model is proxy-injection into HTTP(S) requests, not agent forwarding
- https://docs.blaxel.ai/Sandboxes/Variables-and-secrets — env-var and build-time-secret mechanisms only; no ssh-agent/GPG forwarding described

### browser_auth
Unknown — no page describes a host-browser-open-and-callback relay (the pattern `gh auth login`/OAuth device flows depend on). Blaxel's own CLI login (`bl login`) presumably uses a browser flow for authenticating the *developer* to Blaxel itself, but nothing was found describing forwarding an *in-sandbox* process's browser-auth request back out to a host browser. Genuinely unclear whether this is absent or just undocumented, so kept Unknown rather than No.
Sources:
- https://docs.blaxel.ai/Get-started — `bl login` step described without detail on underlying auth flow mechanics

### shared_dirs
Partial — Volumes can be attached to a sandbox at a mount path, but current limitations are explicit: "you can only attach one volume to a sandbox. A volume can also only be attached to one sandbox at a time," and volumes can't be detached after attachment. No mention of attaching multiple additional shared directories simultaneously.
Sources:
- https://docs.blaxel.ai/Sandboxes/Volumes — "At this time, you can only attach one volume to a sandbox. A volume can also only be attached to one sandbox at a time."

### git_worktrees
Unknown — no mention of git worktree support (or non-support) anywhere in the fetched Sandboxes, Filesystem, Templates, or Tutorials pages.
Sources:
- https://docs.blaxel.ai/Sandboxes/Filesystem — file-operations page has no worktree-specific content (checked, absent)

### nested_containers
Yes — a dedicated official template image provides a Docker runtime inside the sandbox for running nested containers, on top of the outer microVM boundary.
Sources:
- https://docs.blaxel.ai/Sandboxes/Templates — `blaxel/docker-in-sandbox:latest` — "Docker environment"

### harness_agnostic
Yes — infrastructure is generic: any Dockerfile can be used to build a custom sandbox image (requiring only the injected `sandbox-api` binary), so any coding-agent CLI can in principle be installed. Blaxel ships an official pre-built `blaxel/claude-code:latest` template with Claude Code preinstalled and a dedicated tutorial for it, but nothing in the fetched docs ties the platform exclusively to one vendor's agent.
Sources:
- https://docs.blaxel.ai/Sandboxes/Templates — custom Dockerfile support; requirement to "Always include the sandbox-api binary from the Blaxel base image"
- https://docs.blaxel.ai/Tutorials/Claude-Code — "The blaxel/claude-code:latest image includes Claude Code on PATH"

## H. Performance
performance: Unknown/vendor-only — Blaxel publishes its own performance claims (sub-25ms resume from standby, "sub-second sandbox startup" per blog title) but no independently-verified (non-vendor) benchmark was found in the fetched sources, and no disk-footprint, RAM-overhead-per-VM, or bind-mount-IO numbers were found since there is no bind-mount path to begin with. Treat all timing figures below as vendor-reported.
Sources:
- https://docs.blaxel.ai/Sandboxes/Overview — "sub-25ms cold starts" when resuming from standby (vendor docs)
- https://blaxel.ai/blog/sub-second-sandbox-startup-time — vendor blog title referencing sub-second startup (not independently verified)

## I. Feasibility
feasibility: Adoptable-today with caveats — cross-platform CLI installers (macOS/Linux/Windows) and a generous free tier ($200 credit, no card required) make initial trial low-friction; being a hosted-only SaaS means full vendor lock-in for execution (no self-host escape), and the headline security differentiator for this comparison (network egress control) is explicitly private-preview-derived and only reached general availability roughly 3-4 months before this writeup, with the docs themselves stating true routing-level enforcement isn't built yet — a real production-readiness caveat for security-sensitive teams specifically.
Sources:
- https://docs.blaxel.ai/Get-started — cross-platform install instructions (brew/curl/PowerShell)
- https://blaxel.ai/ — "up to $200 credits. No credit card required"
- https://docs.blaxel.ai/Sandboxes/Proxy-domains — "Routing-level enforcement is planned for a future release."

## J. Price (prose-only)
Free tier: up to $200 in credits, no card required, Tier 0 allows 10 sandboxes. Paid tiers are usage-based and scale with a monthly floor: Tier 1 $20/mo (50 sandboxes) up through Tier 9 (100,000+ concurrent sandboxes), tiers auto-upgrading with top-ups. Metered compute: active sandbox CPU/RAM $0.0000115/GB-RAM-second, snapshot storage $0.20/GB-month, custom images $0.045/GB-month, batch job compute $0.000006/GB-RAM-second (cron jobs included free), MCP hosting compute $0.000007/GB-RAM-second, Volumes $0.12/GB-month, custom domains $20/domain-month; egress gateways/proxy/LLM gateway/internal traffic currently "Included" or "Free during beta." Zero compute cost while a sandbox is in standby (idle). Premium support add-ons: email support $800/mo + 3% of usage, Slack support $1,600/mo + 10% of usage, HIPAA compliance add-on $250/mo. No self-hosted/free-forever option; a "Custom" plan exists for dedicated deployments but is still Blaxel-hosted.
Sources:
- https://blaxel.ai/pricing — "$0 Compute cost while idle"; per-unit rates as listed above; tier structure

## K. Extensibility
extensibility: Broad — custom Dockerfile-based sandbox images, 20+ official prebuilt templates (language runtimes, frameworks, browser-automation/Playwright, Jupyter, VNC desktop, Claude Code), TypeScript/Python/Go SDKs plus REST API and MCP-server hosting as a first-class workload type, async job callback URLs, and CLI/GitHub App/GitHub Action integration for deployment workflows.
Sources:
- https://docs.blaxel.ai/Sandboxes/Templates — 20+ prebuilt images enumerated (`blaxel/py-app`, `blaxel/nextjs`, `blaxel/playwright-chromium`, `blaxel/jupyter-notebook`, `blaxel/xfce-vnc`, etc.), all hosted at `github.com/blaxel-ai/sandbox/tree/main/hub/[image-name]`
- https://docs.blaxel.ai/llms-full.txt — "You can bring your custom agents developed in TypeScript or Python and deploy them to Blaxel with our developer tools (Blaxel CLI, GitHub app, GitHub action, etc.)"

## Unknowns & caveats
- **Exact hypervisor/VMM**: never named (Firecracker vs. other) in any fetched official page — only generic "microVM" language. Provider-note claim of "per-sandbox microVMs" is corroborated consistently across site/docs/blog but the specific technology is unconfirmed from primary sources.
- **Network enforcement is the standout weak point**: the domain allow/denylist is real but enforced via `HTTP_PROXY`/`HTTPS_PROXY` env-var convention at the application layer, not network/kernel layer. Blaxel's own docs state tools that ignore the env vars bypass filtering and that "routing-level enforcement is planned for a future release" — meaning the current mechanism should be read as best-effort/opt-in rather than a hard security boundary. No DNS-level blocking, no path/method rules, no non-HTTP(S) protocol coverage, live-reload, escape-hatch, fail-closed behavior, or per-request audit log were confirmed either present or absent — all Unknown after direct searches of the relevant pages.
- **Fleet management** (multi-sandbox registry/grouping/bulk ops) could not be confirmed beyond basic named per-resource CRUD; a guessed list-sandboxes API URL 404'd and no fleet-specific docs page was located.
- **Control-plane failure mode** (fail open vs closed) for any enforcement point is undocumented.
- **git_worktrees** and **browser_auth** (host-browser OAuth relay into a sandboxed process) are both plain silence in the docs — Unknown, not No, per guideline.
- No blocked URLs — all fetches to blaxel.ai and docs.blaxel.ai succeeded (some returned pages with less detail than hoped, reflected as Unknown/Partial above rather than treated as blocks).
- WebSearch budget was exhausted mid-research (200/200 calls used session-wide); remaining gaps were pursued via direct WebFetch of guessed/known docs.blaxel.ai URLs only, which may have missed a small number of relevant pages not linked from those already fetched (e.g., a dedicated Policies or Network overview page that could not be located by URL-guessing alone).

# Daytona
category: cloud
Cloud infrastructure for running AI-generated code in remote, API-provisioned sandboxes | built on rootless Linux containers (Sysbox runtime) by default, with optional dedicated VM and GPU sandbox tiers | core platform closed-source since June 2026 (old OSS repo public but unmaintained, license unclear) | founded 2023, $31M raised ($24M Series A Feb 2026, FirstMark-led, Datadog/Figma Ventures strategic), 72.3k GitHub stars on the now-frozen OSS repo

## A. Identity

### built_on (prose-only)
Sandboxes run as isolated Linux containers by default: "isolated instance with its own Linux namespaces for processes, network, filesystem mounts, and inter-process communication," each with "dedicated kernel, filesystem, network stack, and allocated vCPU, RAM, and disk." Daytona also offers VM sandboxes (dedicated Linux or Windows VM) and GPU sandboxes (up to 16 vCPU/192GB RAM/512GB disk, H100/H200/RTX Pro 6000/4090/5090). Container isolation is provided by Sysbox, a rootless-container runtime: Sysbox "enforces Linux user-namespaces on all sandboxes, ensuring that the root user inside a sandbox maps to a fully unprivileged user on the host," giving each sandbox exclusive UID/GID mappings — this is container-based, not hardware-virtualized, isolation (distinct from gVisor/Firecracker approaches used by some competitors).
Architecture is three-plane: Interface plane (SDKs for Python/TypeScript/Ruby/Go/Java, CLI, Dashboard, MCP, SSH), Control plane (NestJS API, Redis, PostgreSQL, Auth0/OIDC, Sandbox Manager, Snapshot Builder), Compute plane (Runners — Docker/Sysbox hosts that execute sandboxes — plus an OCI-compliant snapshot registry on S3-compatible storage). The Runner container itself runs Docker-in-Docker (based on `docker:28.5.2-dind-alpine3.22`) to host sandboxes.
Sources:
- https://www.daytona.io/docs/en/sandboxes/ — "dedicated kernel, filesystem, network stack, and allocated vCPU, RAM, and disk"
- https://www.daytona.io/docs/llms-full.txt — three-plane architecture (Interface/Control/Compute), NestJS/Redis/PostgreSQL/Auth0 control plane
- (third-party, Sysbox mechanics not independently verified against Daytona's own architecture docs) WebSearch summary of Sysbox/Daytona isolation blog coverage

### execution_locality
execution_locality: Remote — agent/user code executes on Daytona-managed cloud Runners, reached over the network via API/SDK/CLI/SSH; there is no default local-execution mode. An enterprise "Bring Your Own Compute" (BYOC) option lets a customer attach their own runner nodes/Kubernetes infrastructure for data-locality/compliance reasons, but "the control plane always runs on Daytona's infrastructure" even in BYOC — so BYOC is a separate remote deployment topology, not local execution, and does not change the default usage mode. Project code and any credentials passed to the sandbox (API keys, secrets, git tokens) leave the developer's machine over the network to reach the Runner.
Sources:
- https://www.daytona.io/docs/en/bring-your-own-compute/ — "attach your own runner nodes... the control plane always runs on Daytona's infrastructure" (paraphrased from fetched page content)
- https://www.daytona.io/docs/en/getting-started/ — sandbox creation via `app.daytona.io` API/SDK, no local execution path documented

### open_source (prose-only)
As of June 11, 2026, Daytona moved its production codebase to closed source: "Today we are moving Daytona's production codebase to closed source. There is one reason behind this, and it is the only reason that matters to us: security." The company's own stated rationale is that AI-assisted vulnerability discovery against public source now outpaces the benefit of staying open ("an AI system independently discovered all twelve zero-day vulnerabilities in a single OpenSSL release"). The prior public repo (`github.com/daytonaio/daytona`) "is not going anywhere. It will stay public so anyone who wants to keep using it, fork it, or build on it can continue to do so" but "will no longer maintain or update it" — no further releases/fixes. Consequently self-hosting the current platform is no longer possible from the public codebase; only Daytona's managed cloud (optionally with customer-provided compute via BYOC, control plane still Daytona-hosted) is available going forward.
License status is unclear: third-party sources describe the old repo as AGPL-3.0, but querying GitHub's own API for the repo returns a null license field (no SPDX license detected), and a direct fetch of the repo's LICENSE file returned 404. This is flagged as unresolved rather than asserted either way.
Sources:
- https://www.daytona.io/dotfiles/updates/daytona-is-going-closed-source — "moving Daytona's production codebase to closed source... it is the only reason that matters to us: security"; "The existing open source repository is not going anywhere... anyone who wants to keep using it, fork it, or build on it can continue to do so"
- https://api.github.com/repos/daytonaio/daytona — license field returned null (fetched directly)
- https://github.com/daytonaio/daytona/blob/main/LICENSE — 404 Not Found

### maturity (prose-only)
Founded 2023 (Ivan Burazin, Vedran Jukic, Goran Draganic). Raised $31M total across 3 rounds, including a $24M Series A closed Feb 5, 2026 led by FirstMark Capital with Pace Capital, Upfront Ventures, E2VC, Darkmode, and strategic investment from Datadog and Figma Ventures. Prior OSS repo had 72.3k GitHub stars / 5.7k forks / 205 releases (latest v0.190.0, June 23, 2026) before being frozen. Customers cited include LangChain, Turing, Writer, SambaNova.
Sources:
- https://www.daytona.io/dotfiles/daytona-raises-24m-series-a-to-give-every-agent-a-computer — Series A details (fetched via WebSearch summary of official post, not independently re-fetched verbatim)
- https://github.com/daytonaio/daytona — 72.3k stars, 5.7k forks, 205 releases, "Repository is no longer maintained"

## B. Threat protection

### host_fs_damage
host_fs_damage: Yes — each sandbox gets "its own Linux namespaces for processes, network, filesystem mounts" and a rootless Sysbox container where "the root user inside a sandbox maps to a fully unprivileged user on the host," so a compromised sandbox process cannot write to the host filesystem outside the container as host root. Underlying isolation is namespace/rootless-container based (not a microVM/hardware boundary) — see escape_resistance for boundary strength caveats.
Sources:
- https://www.daytona.io/docs/en/sandboxes/ — "dedicated kernel, filesystem, network stack"
- https://www.daytona.io/dotfiles/updates/security-update-cve-2026-31431-copy-fail — Sysbox runtime boundary described as the isolation mechanism in the CVE writeup

### credential_theft
credential_theft: Partial — secrets are mediated rather than stored in plaintext by design (organization "Secrets" are injected as opaque placeholder env vars, "with the real value substituted transparently on outbound requests to the Secret's allowed hosts," and env vars matching token/key/secret/password/auth patterns are redacted from API output by default), and SSH access uses short-lived (60-minute) tokens rather than long-lived keys. However, Daytona disclosed a real credential-exposure vulnerability (patched Apr 9, 2026): "API credentials passed via the Daytona CLI or SDK [could] be read from sandbox memory by anyone with shell access on the same sandbox," requiring affected users to rotate credentials. No host ssh-agent/gpg-agent forwarding mechanism is documented (git provider auth instead goes through OAuth via the Git Provider integration).
Sources:
- WebSearch summary of Daytona SDK docs — "environment variable is set to the Secret's opaque placeholder... substituted transparently on outbound requests to the Secret's allowed hosts"
- https://www.daytona.io/dotfiles/updates/security-update-cve-2026-31431-copy-fail (and related advisory) — "API credentials passed via the Daytona CLI or SDK to be read from sandbox memory by anyone with shell access on the same sandbox"
- https://www.daytona.io/docs/en/ssh-access/ — SSH access tokens "expire automatically after 60 minutes"

### data_exfiltration
data_exfiltration: Partial — network egress CAN be restricted (IP/CIDR allowlist, domain allowlist, or full block), but this is only the default enforced behavior for lower billing tiers; higher tiers (3 & 4) get "full internet access... by default" and must opt in to restriction. See axis C network sub-criteria for full granularity detail.
Sources:
- https://www.daytona.io/docs/en/network-limits/ — "Tier 1 & 2: Network access is restricted and cannot be overridden"; "Tier 3 & 4: Full internet access is available by default"

### malicious_execution
malicious_execution: Partial — blast radius of untrusted code is contained by rootless container isolation, per-sandbox resource quotas, and (for higher tiers) network restriction, but the isolation boundary is a shared-kernel rootless container (Sysbox), not a hardware/VM boundary, and Daytona has publicly disclosed at least one kernel-level vulnerability (CVE-2026-31431, "Copy Fail") reachable from inside a sandbox that created "a theoretical path for one sandbox to corrupt cached file content visible to co-tenant sandboxes" on multi-tenant hosts, though Daytona states no confirmed escape occurred.
Sources:
- WebSearch summary of https://www.daytona.io/dotfiles/updates/security-update-cve-2026-31431-copy-fail — "a theoretical path for one sandbox to corrupt cached file content visible to co-tenant sandboxes"; "Daytona did not observe a sandbox escape to the underlying runner host"

### escape_resistance
escape_resistance: Partial — default isolation is a rootless Linux container (Sysbox: user-namespace UID/GID remapping, "partial virtualization of procfs and sysfs," "immutable initial mounts," "selective syscall interception") which is stronger than a plain unprivileged Docker container but still a shared-kernel boundary, weaker than a microVM (Firecracker) or gVisor-style syscall-interposition boundary. Daytona offers a stronger alternative — dedicated VM sandboxes — for workloads needing hardware-level isolation, but container sandboxes (the default/most common path) remain shared-kernel. A real kernel-level vulnerability (CVE-2026-31431) was disclosed as reachable from inside a Daytona container sandbox in 2026, underscoring the shared-kernel exposure, even though Daytona reports the Sysbox boundary itself held.
Sources:
- WebSearch summary of third-party technical writeups on Sysbox mechanics (not Daytona's own docs) — "enforces Linux user-namespaces... partial virtualization of procfs and sysfs... immutable initial mounts... selective syscall interception"
- https://www.daytona.io/docs/en/sandboxes/ — VM sandboxes offered as a "dedicated Linux VM" alternative
- WebSearch summary of Daytona's CVE-2026-31431 advisory — kernel vuln reachable from inside sandbox; Sysbox boundary itself reported not breached

### resource_abuse
resource_abuse: Yes — every sandbox has enforced CPU/RAM/disk quotas (default 1 vCPU/1GB RAM/3GiB disk; org max up to 4 vCPU/8GB/10GB on standard tiers, up to 500 vCPU/1000GiB/5000GiB on higher tiers), and quota accounting reclaims resources on stop/pause/archive/delete. The specific enforcement mechanism (cgroups, kernel quotas) is not documented.
Sources:
- https://www.daytona.io/docs/en/limits/ — per-sandbox resource maximums by tier; "stopped, paused, archived, and deleted sandboxes free reserved CPU and memory"
- https://www.daytona.io/docs/en/getting-started/ — default 1 vCPU/1GB RAM/3GiB disk, org max 4/8/10

## C. Feature set & granularity

### network_default_posture
network_default_posture: Partial — NOT deny-by-default across the board. "Network access policies for your organization are set automatically depending on your organization's limits tier and cannot be modified by organization administrators." Tier 1 & 2 orgs get restricted-by-default egress (limited to a pre-whitelisted "essential services" list) with no override; Tier 3 & 4 orgs get open-by-default (full internet access) and must opt in to restriction via `networkAllowList`/`domainAllowList`/`networkBlockAll`.
Sources:
- https://www.daytona.io/docs/en/network-limits/ — "Network limits are automatically applied to sandboxes based on your organization's billing tier"; "Tier 1 & 2: Network access is restricted and cannot be overridden at the sandbox level"; "Tier 3 & 4: Full internet access is available by default, with the ability to configure custom network settings"

### egress_allowlist
egress_allowlist: Yes — two allowlist mechanisms, mutually exclusive with each other and with a full-block flag: `networkAllowList` (IPv4 CIDR blocks, max 10 entries, each requiring an explicit `/0`–`/32` prefix) and `domainAllowList` (DNS domains including wildcards like `*.daytona.io`, max 20 entries), plus `networkBlockAll` to deny all outbound traffic. Granularity tops out at domain/CIDR — no documented port, path, method, or regex scoping, and no documented deny-with-exceptions precedence model (the three modes are mutually exclusive rather than composable allow+deny rules).
Sources:
- https://www.daytona.io/docs/en/network-limits/ — "comma-separated list of IPv4 CIDR blocks... CIDR required: every entry must include a `/` prefix length"; "comma-separated list of DNS domains"; max 10 CIDR / 20 domain entries

### dns_level_blocking
dns_level_blocking: Unknown — docs describe the allow/block policy but do not state whether unlisted domains fail at DNS resolution or are blocked post-resolution at the connection layer. Searched the network-limits page directly for this detail; not specified.
Sources:
- https://www.daytona.io/docs/en/network-limits/ — page fetched in full; no DNS-resolution-layer detail found

### tls_mitm_inspection
tls_mitm_inspection: Unknown — no mention anywhere in fetched docs of TLS interception/MITM or certificate injection for L7 inspection. Domain-based allowlisting could be implemented via SNI inspection without full MITM, but this is not documented either way.
Sources:
- https://www.daytona.io/docs/en/network-limits/ — no TLS/inspection detail present

### http_path_rules
http_path_rules: No — the documented network-limit API surface is limited to exactly two list types (CIDR blocks, DNS domains) plus a block-all flag; no path, method, or regex field exists in the documented schema. This is a structural absence in the documented API, not silence.
Sources:
- https://www.daytona.io/docs/en/network-limits/ — full parameter set documented is `networkAllowList` (CIDR), `domainAllowList` (domains), `networkBlockAll` — no path/method field described

### proto_coverage
proto_coverage: Unknown — docs don't state which protocols the allow/block rules actually govern (e.g., whether a domain-allowlist entry gates only HTTP(S) or all ports/protocols to that host, and whether DNS/ICMP/UDP/QUIC are separately covered, logged, or unrestricted). No mention of SSH/WS/gRPC handling or of any extensibility model for custom L7 protocols.
Sources:
- https://www.daytona.io/docs/en/network-limits/ — page fetched in full; no protocol-scope detail found

### live_rule_reload
live_rule_reload: Partial — supported only for Tier 3 & 4 organizations: "Organizations on Tier 3 and Tier 4 can change outbound firewall policy on a running sandbox... The sandbox keeps running; stop or start are not required." Tier 1 & 2 organizations cannot modify network policy at all (fixed, not just non-live).
Sources:
- https://www.daytona.io/docs/en/network-limits/ — "Organizations on Tier 3 and Tier 4 can change outbound firewall policy on a running sandbox... stop or start are not required"

### firewall_escape_hatch
firewall_escape_hatch: No — no documented timed/auto-reverting bypass mechanism. The only lever is manually clearing `domainAllowList`/`networkAllowList` (only possible for Tier 3/4 orgs) which removes restriction until manually reapplied — an all-or-nothing manual toggle, not a scoped, automatically-expiring break-glass mechanism.
Sources:
- https://www.daytona.io/docs/en/network-limits/ — "Sending `domainAllowList` as an empty string clears a stored domain allow list" (manual, permanent-until-changed, Tier 3/4 only)

### enforcement_plane
enforcement_plane: Unknown — docs describe the policy layer (which domains/IPs are allowed) in detail but never state the underlying enforcement mechanism (kernel eBPF/netfilter, userspace proxy, cloud VPC security group, or hypervisor-level). Could not confirm whether the sandbox process could tamper with or route around the enforcement point.
Sources:
- https://www.daytona.io/docs/en/network-limits/ — page fetched in full; no enforcement-mechanism detail found

### fail_closed
fail_closed: Unknown — no documentation found describing what happens to a sandbox's network policy if the Daytona control plane becomes unavailable.
Sources:
- (searched network-limits, sandboxes, security-exhibit/trust center pages; no statement found)

### network_audit
network_audit: No — Daytona's audit logging is confirmed to cover platform API actions only (sandbox create/start/stop/delete/archive/snapshot/fork, org management, SSH-token issuance), not per-domain or per-request egress traffic: "There is no mention of per-domain or per-IP network traffic logging. The documented actions focus on infrastructure operations... rather than egress monitoring." Log fields are `actorId`, `actorEmail`, `action`, `targetType`, `targetId`, `statusCode`, `errorMessage`, `ipAddress` (of the API caller), `userAgent`, `createdAt` — none of which represent an outbound-request log.
Sources:
- https://www.daytona.io/docs/en/audit-logs/ — "detailed record of user and system activity across your organization... track sandbox lifecycle events, user access, system changes"; field list confirms platform-action scope, not egress-request scope

### workspace_modes
workspace_modes: Partial — no true host bind-mount is possible (sandboxes execute remotely, so there is no local filesystem to bind-mount into a live remote container). Instead Daytona offers: (1) Snapshots — reusable base images (Dockerfile or existing OCI image) baked once and reused, functionally equivalent to "copy/ephemeral" mode; (2) `daytona sandbox sync` — a one-way, near-real-time (sub-second) local→remote file watcher/push, not a bidirectional mount; (3) VS Code Remote-SSH, where the editor connects directly to the remote sandbox filesystem (files live only on the sandbox, edited in place over SSH). No mode gives a genuine two-way live host↔sandbox filesystem link the way a local bind mount does.
Sources:
- WebSearch summary of https://www.daytona.io/docs/en/snapshots/ — snapshots as reusable base-image templates
- WebSearch summary of `daytona sandbox sync` CLI docs — "modifications to local files are reflected in the remote Sandbox path within sub-second latency"; one-way watcher

### observability
observability: Yes — "multi-layer observability stack that integrates the OpenTelemetry (OTel) ecosystem with custom collectors and storage backends," instrumented across API services, runner daemon, and toolbox; logs are exposed so "clients can integrate any log collection and storage tools they prefer" (tooling-agnostic, bring-your-own-backend rather than a built-in dashboard product).
Sources:
- WebSearch summary of Daytona observability docs/DeepWiki analysis of the codebase — OTel-based multi-layer stack; "tooling agnosticism... logging exposed so clients can integrate any log collection and storage tools"

### supervision
supervision: Partial — the platform's Sandbox Manager "schedules sandboxes onto runners, reconciles states, and enforces sandbox lifecycle management policies," and operators can forcibly stop/delete a sandbox via API/dashboard at any time (coarse containment). No documented behavioral-anomaly detection or automatic containment response to suspicious in-sandbox activity was found — supervision here is infrastructure lifecycle management, not active security oversight of agent behavior.
Sources:
- https://www.daytona.io/docs/llms-full.txt — "Sandbox Manager... schedules sandboxes onto runners, reconciles states, and enforces sandbox lifecycle management policies"

### fleet_mgmt
fleet_mgmt: Yes — organizations provide multi-tenant grouping/RBAC; sandboxes can be created as "linked" parent/child sets forming an internal-DNS-addressable network ("Deleting the parent deletes all of its linked children (cascade). One parent may have many linked children (1:N)"); dashboard and CLI (`daytona list`) provide fleet-wide visibility.
Sources:
- https://www.daytona.io/docs/llms-full.txt — linked-sandbox parent/child cascade behavior, "link network" internal DNS
- https://www.daytona.io/docs/en/organizations/ — organization-level grouping (per WebSearch summary)

### snapshots_persistence
snapshots_persistence: Yes — "cold" (container, filesystem-only, sandbox must be stopped) and "hot" (VM, filesystem+memory via `includeMemory`, sandbox must be running) snapshots, stored in an OCI-compliant registry on S3-compatible object storage, reusable across many sandboxes; separate Volumes feature provides FUSE-based persistent storage mountable across sandboxes (not usable for block-storage workloads like databases) with per-subpath isolation; auto-stop (15 min default), auto-pause (VM, 60 min default), auto-archive (container, 7 days default), and auto-delete lifecycle policies.
Sources:
- https://www.daytona.io/docs/llms-full.txt — cold/hot snapshot definitions, OCI registry, auto-stop/pause/archive/delete defaults
- WebSearch summary of https://www.daytona.io/docs/en/volumes/ — FUSE-based, per-subpath isolation, multi-sandbox mount

## D. Setup (spectrum)
setup: Easy — no local runtime prerequisite (no Docker/K8s needed client-side since execution is remote); sign up at app.daytona.io, get an API key, then `pip install daytona` (or CLI/other SDK) and `daytona.create()` / `daytona create` produces a running sandbox in "under 90ms" per vendor claim. First $200 of compute is free, no credit card required.
2-4 sentences: Getting a sandbox running requires an account + API key but nothing installed locally beyond the SDK/CLI itself — a materially lower local-prerequisite bar than container/VM-based local tools, at the cost of being entirely dependent on Daytona's cloud being reachable.
Sources:
- https://www.daytona.io/docs/en/getting-started/ — dashboard "Create Sandbox" flow; Python SDK `daytona.create()`; CLI `daytona create`
- WebSearch summary of pricing page — "$200 in free compute credits... no credit card required"

## E. Daily use (spectrum)
daily_use: Moderate — day-to-day friction centers on the sandbox being remote: attach via SSH/VS Code Remote-SSH/dashboard/API, work happens on the remote filesystem (or is pushed one-way via `daytona sandbox sync`), and auto-stop/auto-archive lifecycle timers (defaults 15 min idle / 7 days archive) mean idle sandboxes are reclaimed and must be explicitly restarted — a different rhythm than an always-on local container. Preview URLs (ports 3000-9999) provide a straightforward way to view running web services.
Sources:
- https://www.daytona.io/docs/llms-full.txt — auto-stop 15 min default, auto-archive 7 days default
- WebSearch summary of https://www.daytona.io/docs/en/preview/ — preview URL for ports 3000-9999

## F. Configuration

### config_depth (spectrum)
config_depth: Moderate-deep — Snapshots act as the declarative per-project config unit (built from a Dockerfile or existing OCI/Docker-Hub/GCR/ECR/GHCR/private-registry image), versionable as images; per-sandbox parameters cover resources (vCPU/RAM/disk), env vars, secrets (host-scoped placeholder substitution), and (tier-gated) network rules; volumes/external-storage mounts for shared state. No lifecycle-hook concept equivalent to local tools' post-init/pre-run scripts was found documented beyond baking steps into the snapshot image itself.
Sources:
- https://www.daytona.io/docs/llms-full.txt — snapshot build from Dockerfile or OCI-compatible registry image
- https://www.daytona.io/docs/en/network-limits/ — per-sandbox network parameters

### policy_model (spectrum)
policy_model: Rigid-to-moderate, tier-gated — network restriction behavior is NOT a free per-sandbox policy choice: it is fixed by organization billing tier ("cannot be modified by organization administrators" for Tier 1/2; configurable only for Tier 3/4). Resource/secrets/snapshot config is per-sandbox and flexible, but the security-relevant network posture is largely take-it-or-leave-it below the top tiers, which is a materially less policy-driven model than a tool where every sandbox can independently dial security tighter or looser regardless of paid tier.
Sources:
- https://www.daytona.io/docs/en/network-limits/ — tier-gated network policy, no admin override below Tier 3

## G. DX — host↔sandbox integration

### bind_mount_sharing
bind_mount_sharing: No — see workspace_modes above; there is no live two-way bind mount between host and sandbox. The closest analogs are one-way local→remote `daytona sandbox sync` and remote-SSH in-place editing, neither of which shares changes bidirectionally the way a local bind mount does.
Sources:
- WebSearch summary of `daytona sandbox sync` docs — one-way local→remote watcher

### cred_forwarding
cred_forwarding: No — no ssh-agent or gpg-agent forwarding into the sandbox is documented. Instead, Daytona issues its own short-lived (60-minute) SSH access tokens to reach the sandbox, and git-host authentication is handled via a Git Provider OAuth integration (GitHub/GitLab/Bitbucket/Azure DevOps) rather than forwarding the developer's existing local credentials.
Sources:
- https://www.daytona.io/docs/en/ssh-access/ — token-based SSH access, 60-minute expiry
- WebSearch summary of Git Provider Integration docs — OAuth-based, provider-agnostic interface

### browser_auth
browser_auth: Partial — no host-browser-triggered proxy-back mechanism (like a local tool auto-opening the developer's own browser for an OAuth callback) is documented. Two partial substitutes exist: Preview URLs can expose a locally-listening OAuth callback port (3000-9999) from inside the sandbox to a public URL, and "computer use" sandboxes provide an actual browser running inside the sandbox itself (accessible via the sandbox's own desktop session) for flows that need an interactive browser. Neither is the "host browser opens automatically" pattern.
Sources:
- WebSearch summary of https://www.daytona.io/docs/en/preview/ — preview URL exposes sandbox-internal listening ports
- WebSearch summary of computer-use / Agents SDK integration docs — sandbox desktop session with a controllable Chromium/browser process

### shared_dirs
shared_dirs: Yes — Volumes (FUSE-based, mountable to multiple sandboxes, per-subpath isolation) and External Storage mounts (S3/GCS buckets mounted as local directories) both provide additional persistent, shareable storage beyond the sandbox's own ephemeral disk.
Sources:
- WebSearch summary of https://www.daytona.io/docs/en/volumes/ and https://www.daytona.io/docs/en/mount-external-storage/ — FUSE-based shared/persistent mounts, S3/GCS bucket mounting

### git_worktrees
git_worktrees: Unknown — the documented Toolbox Git API covers clone/status/add/commit/push/pull/log/checkout/branch create/list/delete; no explicit mention of git-worktree support (first-class or otherwise) was found in searched docs.
Sources:
- WebSearch summary of https://www.daytona.io/docs/en/git-operations/ — operation list does not mention worktrees

### nested_containers
nested_containers: Yes — Docker-in-Docker is documented and supported: "create a Docker-in-Docker snapshot using prebuilt docker:dind images or install Docker manually in a custom image," enabling Docker Compose workloads (Postgres/Redis/MySQL/multi-container dev environments) inside a sandbox.
Sources:
- WebSearch summary of Daytona DinD docs/definitions page — dind snapshot creation, Docker Compose support inside sandbox

### harness_agnostic
harness_agnostic: Yes — sandboxes are general-purpose Linux (or VM) environments reachable via SSH/exec/CLI, so any coding-agent CLI can be installed and run; Daytona additionally ships an official MCP server documented to work with "Claude, Cursor, and Windsurf," and a documented OpenCode plugin, but nothing in the platform restricts it to a single vendor's harness.
Sources:
- WebSearch summary of https://www.daytona.io/docs/en/mcp/ — MCP server supports Claude, Cursor, Windsurf
- WebSearch summary of https://www.daytona.io/docs/en/guides/opencode/opencode-plugin/ — OpenCode plugin

## H. Performance (spectrum)
performance: Lightweight (vendor-claimed) — Daytona's own marketing/docs claim "sub-90ms sandbox creation," positioned against Docker container startup times; this is a vendor benchmark, not independently reproduced in this research. No independent/third-party benchmark numbers were directly fetched and verified in this pass (several third-party comparison blogs surfaced in search results but were not fetched/verified as primary sources for this writeup). Bind-mount IO performance (a common pain point for local tools on macOS) is not applicable since there's no local bind mount.
Sources:
- https://www.daytona.io/docs/en/sandboxes/ — "spinning up in under 90ms from code to execution" (vendor claim)

## I. Feasibility (spectrum)
feasibility: Adoptable today, with caveats — Daytona is cloud-only (no offline/local mode); a solo developer can start immediately with just an account, API key, and $200 free credit, no local Docker/K8s required. Multi-region availability (US, EU, Asia-South) supports data-locality needs. The main feasibility risk is recency of the June 2026 closed-source transition: the platform itself remains operational and commercially backed ($31M raised), but the public/self-hostable path is now frozen, meaning adoption commits a team to Daytona's managed cloud (or BYOC-with-Daytona-control-plane) rather than a portable open deployment — a lock-in consideration for teams that valued the prior open-source option.
Sources:
- https://www.daytona.io/dotfiles/updates/daytona-is-going-closed-source — closed-source transition, June 11 2026
- WebSearch summary of regions/pricing pages — US/EU/Asia-South regions

## J. Price (prose-only)
Pay-as-you-go compute billing with $200 in free compute credit on signup (no credit card required) and 5GB free cold storage; a startup program offers up to $50,000 in credits. One third-party source (not independently verified against an official price list in this research) cites general compute at roughly $0.0504/vCPU-hour + $0.0162/GiB-hour (~$0.083/hr for 1 vCPU + 2GB); the official pricing page as fetched directly only exposed a Windows-OS compute rate of $0.0858/vCPU/hour and did not surface a full Linux/GPU rate table in the fetched content (likely rendered client-side). Enterprise tier adds SSO, audit logs, and BYOC for custom pricing. No free self-hosted tier remains available following the June 2026 closed-source transition (the frozen public repo can still be run without a Daytona account, but receives no updates/support).
Sources:
- WebSearch summary of https://www.daytona.io/pricing — "$200 in free compute credits... no credit card required"; "up to $50,000 in complimentary credits" for startups
- https://www.daytona.io/pricing (direct fetch) — "$0.0858/vCPU/h" (Windows), full Linux rate table not present in fetched content

## K. Extensibility
extensibility: Yes — custom images via Snapshots (Dockerfile or any OCI-compatible registry: Docker Hub, GAR, GHCR, private registries), SDKs in 5 languages, a documented REST Platform API + Toolbox API + Analytics API (OpenAPI specs published), an official MCP server for agent-tool integration, and community/vendor plugins (e.g., OpenCode plugin). No user-defined custom-protocol or firewall-rule-extension mechanism was found (see proto_coverage — extensibility of the network-rule model itself is undocumented).
Sources:
- https://www.daytona.io/docs/llms-full.txt — snapshot build from Dockerfile/any OCI registry; Platform/Toolbox/Analytics APIs with OpenAPI specs; SDKs in Python/TypeScript/Ruby/Go/Java
- WebSearch summary of MCP server and OpenCode plugin docs

## Unknowns & caveats
- **License of the frozen OSS repo**: third-party sources call it AGPL-3.0, but GitHub's own API reports no detected license (`license: null`) and the LICENSE file 404'd on direct fetch — left as unresolved rather than asserted.
- **Network enforcement mechanism** (enforcement_plane), **fail_closed behavior**, **DNS-level blocking mechanics** (dns_level_blocking), **TLS/MITM inspection** (tls_mitm_inspection), and **protocol coverage beyond domain/IP** (proto_coverage) are all genuinely undocumented in Daytona's official docs as fetched — these are docs-silence Unknowns, not confirmed absences, except http_path_rules which is a structural No based on the fully-enumerated API schema.
- **git_worktrees** support: not mentioned either way in the Git Operations docs; Unknown rather than No.
- **Full Linux/GPU pricing table**: the official pricing page's fetched content only exposed Windows-OS compute pricing; a fuller table likely exists but wasn’t captured (possibly client-side rendered) — the vCPU/GiB-hour figures cited above are from a third-party blog, not confirmed against Daytona's own page text.
- **Performance**: only vendor-claimed sub-90ms figure found and cited; no independent benchmark was fetched/verified for this writeup despite several third-party comparison articles surfacing in search.
- No connection failures/blocked URLs occurred during this research (firewall was bypassed per operational note); all fetches either succeeded or returned ordinary HTTP 404/redirect responses (not egress blocks) — the two LICENSE-file 404s and the security-exhibit→trust.daytona.io redirect were content-not-found/redirect conditions, not firewall blocks.

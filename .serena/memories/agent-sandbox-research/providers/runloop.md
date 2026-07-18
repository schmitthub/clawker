# Runloop
category: cloud
AI-agent sandbox infrastructure platform ("AI Agent Accelerator") providing on-demand cloud "Devboxes" | micro-VM + container two-layer isolation on a custom bare-metal hypervisor (exact hypervisor tech unconfirmed in official docs) | SDKs (Python/TS) MIT-licensed on GitHub; core platform closed-source SaaS | founded 2023/2024 (sources vary), SF-based, ~$7M seed (The General Partnership, Blank Ventures, per VentureBeat, Jul 2025)

## A. Identity

### built_on (prose-only)
Runloop's own marketing states Devboxes are a "Secure, isolated, micro-VM* environment (Two layers of security, VM + Container)" running on a "custom bare-metal hypervisor." Docs describe this generically as "virtual machine technology" providing isolation for API keys, code, secrets, and internal systems. No official source (docs or marketing) names the underlying hypervisor technology (e.g., confirms or denies Firecracker/KVM/Cloud Hypervisor); the asterisk on "micro-VM*" on the homepage suggests a footnote/caveat that WebFetch did not surface. Third-party comparison blogs (Northflank, a competitor) independently describe "a custom bare-metal hypervisor with VM plus container dual-layer architecture" matching the vendor's own language, but do not name the hypervisor either. **Underlying tech remains provisional/unconfirmed** per the task's own note — no official doc pins down the specific virtualization technology.
Sources:
- https://www.runloop.ai/ — "Secure, isolated, micro-VM* environment (Two layers of security, VM + Container)"
- https://www.runloop.ai/ — "custom bare-metal hypervisor"
- https://docs.runloop.ai/docs/devboxes/overview — "We use virtual machine technology to provide isolation and safety for your API keys, code, secrets, sensitive data, and internal systems."

### execution_locality
execution_locality: Remote — all agent code runs on Runloop's cloud infrastructure (Devboxes are cloud VMs reached via API/SDK/CLI/SSH); no local execution mode exists. The Enterprise "Deploy to VPC" option runs Runloop's stack inside the customer's own AWS/GCP/Azure account, but this is still a separate remote deployment (not the developer's own machine) and is sales-gated, not self-service.
2-3 sentences: Every documented workflow (quickstart, CLI `rli devbox ssh`, SDK `devbox.create()`) provisions and executes on Runloop-hosted (or Runloop-managed-in-VPC) compute; there is no "run the sandbox locally" mode. Code/credentials reach Runloop's infrastructure (or the customer's own VPC, for Enterprise) but never stay purely on the developer's laptop once a Devbox is created.
Sources:
- https://docs.runloop.ai/docs/tutorials/quickstart — describes creating/SSHing into a remote Devbox via API key + SDK, no local execution path
- https://runloop.ai/about — "Deploy to VPC" capability, deploying into the customer's own cloud account (still a remote deployment)

### open_source (prose-only)
The Python and TypeScript API client SDKs (`runloopai/api-client-python`, `runloopai/api-client-ts`) are MIT-licensed and open on GitHub. The core Devbox platform, control plane, network-policy engine, and dashboard are closed-source SaaS with no self-host option documented outside of the sales-gated Enterprise "Deploy to VPC" offering (still Runloop-operated infrastructure placed into the customer's cloud account, not a customer-run open build).
Sources:
- https://github.com/runloopai/api-client-python — repository carries an MIT license; contains only the client SDK, not the platform
- https://runloop.ai/about — "Deploy to VPC" capability enabling deployment "directly into their own cloud accounts" (Enterprise only)

### maturity (prose-only)
Runloop is an early-stage startup: founded 2023 (per company "About" page) — some third-party trackers list 2024 — led by CEO Jonathan Wall (ex-Google Wallet co-founder), a small team (~15.5 yrs avg. engineer experience, alumni of Google/Stripe/Vercel/AWS/Meta). Raised a $7M seed round (The General Partnership, Blank Ventures) reported by VentureBeat, Jul 2025. No larger funding round found in official sources during this research pass.
Sources:
- https://runloop.ai/about — "Founder & CEO: Jonathan Wall... Founded: 2023"
- VentureBeat (third-party) — "Runloop lands $7M to power AI coding agents with cloud-based devboxes"

## B. Threat protection

### host_fs_damage
host_fs_damage: Yes — agents execute entirely inside a remote, isolated micro-VM+container Devbox, never on the "host" (Runloop's infra) filesystem directly; there is no local host filesystem in scope by default (execution is remote), so this is structurally satisfied rather than configured.
Sources:
- https://docs.runloop.ai/docs/devboxes/overview — "We use virtual machine technology to provide isolation and safety for your... code..."

### credential_theft
credential_theft: Partial — Agent Gateways and MCP Hub strongly mediate LLM/tool-provider credentials (agent only ever sees an opaque, devbox-bound gateway token; real keys never enter the devbox and stolen tokens don't work elsewhere). However, "Account Secrets" and git repo tokens are the opposite: they are injected as **plaintext environment variables** / written into the git credential cache inside the devbox, fully readable by any code (or prompt-injected agent) running there.
Sources:
- https://docs.runloop.ai/docs/devboxes/agent-gateways — "Your real credentials stay secure on Runloop's servers—the agent only gets a temporary gateway token." / "Your API keys never enter the devbox."
- https://docs.runloop.ai/docs/devboxes/configuration/account-secrets — "Secrets are encrypted at rest and automatically made available as environment variables in your Devboxes."
- https://docs.runloop.ai/docs/devboxes/mounts/code-mounts — "Runloop automatically sets up the GH_TOKEN environment variable and credential cache for you."

### data_exfiltration
data_exfiltration: Partial — Network Policies can restrict egress to an explicit allowlist of DNS hostnames, but the platform default is fully open ("unrestricted network access"), restriction is opt-in, and granularity is coarse (see axis C for full detail: hostname-only, no IP/CIDR/port/path/deny rules documented).
Sources:
- https://docs.runloop.ai/docs/network-policies — "By default, Devboxes have unrestricted network access."

### malicious_execution
malicious_execution: Yes — blast radius of hallucinated/malicious code is contained to the ephemeral, resource-quota-capped Devbox (isolated VM+container, capped CPU/mem/disk, destroyable on demand); credential-gateway design (Agent Gateways/MCP Hub) further limits what a compromised devbox can do with any credentials it does have.
Sources:
- https://docs.runloop.ai/docs/devboxes/configuration/sizes — CPU/memory/storage hard caps per devbox
- https://docs.runloop.ai/docs/devboxes/agent-gateways — credential-token isolation design

### escape_resistance
escape_resistance: Partial — vendor claims a "hardware-level" boundary via VM+container dual-layer isolation on a "custom bare-metal hypervisor," which if accurate is stronger than a shared-kernel container alone. However, the specific hypervisor technology is not named in any official source, and no independent third-party security audit or CVE/escape-history disclosure was found during this research pass — so the strength claim rests entirely on vendor marketing language, not verifiable technical detail.
2 sentences: The dual-layer (VM outer boundary + container inner boundary) architecture is a stronger default posture than plain-container-only sandboxes structurally, assuming the marketing description is accurate. No public escape research, bug-bounty history, or independent audit report was located to substantiate or refute the claim.
Sources:
- https://www.runloop.ai/ — "enterprise-grade security through isolated micro-VMs that create strong hardware-level boundaries"

### resource_abuse
resource_abuse: Yes — hard resource caps enforced via predefined sizes (X_SMALL–XX_LARGE) or CUSTOM_SIZE with documented bounds (CPU 0.5–16 cores, memory 1–64GiB, disk 2–64GiB, in multiples of 2), plus configurable `keep_alive_time_seconds` max lifetime and idle-based auto-suspend/shutdown to cap runaway compute cost.
Sources:
- https://docs.runloop.ai/docs/devboxes/configuration/sizes — "Must be multiple of 2. Min is 0.5 core, max is 16 cores."
- https://docs.runloop.ai/docs/devboxes/start-stop — "Configure either `keep_alive_time_seconds` or an idle policy, not both."

## C. Feature set & granularity

### network_default_posture
network_default_posture: Open-by-default — restriction is entirely opt-in; an unconfigured Devbox reaches the open internet with no restriction unless the caller explicitly attaches a Network Policy with `allow_all=False`.
Sources:
- https://docs.runloop.ai/docs/network-policies — "By default, Devboxes have unrestricted network access."

### egress_allowlist
egress_allowlist: Partial — exists, but coarse: allow/deny is binary at the policy level (`allow_all` true/false) with a single-tier allowlist of **DNS hostnames only**, supporting first-label wildcards (e.g. `*.github.com`). No IP/CIDR rules, no port scoping, no explicit deny/blocklist rules (i.e., no "allow everything except X" mode), and no documented precedence semantics beyond `allow_all` overriding everything else. A separate, independent policy can be applied at Blueprint-build time vs. Devbox-runtime, and a Devbox can override its Blueprint's runtime policy at creation.
Sources:
- https://docs.runloop.ai/docs/network-policies — field table: `allowed_hostnames` — "List of DNS hostnames to allow, with wildcard support"
- https://docs.runloop.ai/docs/devboxes/blueprints/network-policies — `allowed_hostnames=["github.com", "*.npmjs.org", "pypi.org"]`; "Devboxes can override the Blueprint's runtime network policy by specifying a different `network_policy_id`"

### dns_level_blocking
dns_level_blocking: Unknown — docs describe the allowlist as operating on "DNS hostnames" but never state the enforcement mechanism (DNS-response blocking vs. connection-level/proxy blocking vs. SNI inspection). No architecture page was found describing how a disallowed hostname request actually fails.
Sources:
- https://docs.runloop.ai/docs/network-policies — page discusses policy configuration only, no enforcement-mechanism detail found

### tls_mitm_inspection
tls_mitm_inspection: Unknown — no official source mentions TLS interception/MITM. Because policy rules are hostname-only (not path/method-based), MITM may not be architecturally necessary, but this is inference, not a documented fact either way.
Sources:
- https://docs.runloop.ai/docs/network-policies — no mention of TLS/MITM found

### http_path_rules
http_path_rules: No — the full documented Network Policy field set (`name`, `allow_all`, `allowed_hostnames`, `allow_devbox_to_devbox`, `allow_agent_gateway`, `allow_mcp_gateway`, `description`) contains no path, method, or regex field. Policies operate strictly at hostname granularity.
Sources:
- https://docs.runloop.ai/docs/network-policies — full field table as fetched, no path/method fields present

### proto_coverage
proto_coverage: Unknown — docs speak generically of "egress traffic" / "network access" without ever enumerating protocol coverage (DNS, ICMP, TCP, UDP/QUIC, SSH, WebSocket, gRPC). Inbound SSH access to the Devbox itself is documented in detail (TLS-wrapped proxy, ECDSA keys) but that is access-to-the-box, not an egress-control protocol statement. No statement of protocol extensibility (fixed vs. pluggable L7 rule model) was found.
Sources:
- https://docs.runloop.ai/docs/devboxes/ssh — "a TLS-based proxy command" secures the inbound SSH tunnel (separate from egress Network Policies)

### live_rule_reload
live_rule_reload: Yes — updating a Network Policy object propagates to every running Devbox/Blueprint currently referencing that policy ID, without restart, though propagation is not instantaneous.
Sources:
- https://docs.runloop.ai/docs/network-policies — "When you update a network policy, all running Devboxes and Blueprints using that policy will be updated. Changes are eventually consistent and may take a few moments to propagate."

### firewall_escape_hatch
firewall_escape_hatch: Partial — there is no documented **timed** bypass with automatic re-enforcement. The closest mechanisms are: (a) choosing a more permissive `network_policy_id` (including one with `allow_all=True`) at Devbox-creation time, overriding a Blueprint's default policy, and (b) live-editing an existing policy object (e.g., toward `allow_all=True`), which propagates to running Devboxes without restart per `live_rule_reload` above. Both are durable manual toggles, not a scoped break-glass timer that self-reverts.
Sources:
- https://docs.runloop.ai/docs/devboxes/blueprints/network-policies — "Devboxes can override the Blueprint's runtime network policy by specifying a different `network_policy_id` at creation time"

### enforcement_plane
enforcement_plane: Unknown — no official source describes where policy is enforced (kernel/eBPF, userspace proxy, hypervisor/VM boundary, or cloud network infrastructure), whether the enforcement point is tamper-resistant from inside the Devbox, or whether traffic is logged at that layer.
Sources:
- https://docs.runloop.ai/docs/network-policies — no architecture/enforcement-layer detail found

### fail_closed
fail_closed: Unknown — no documentation addresses what happens to an already-running Devbox's network enforcement if Runloop's control plane suffers an outage.
Sources:
- https://docs.runloop.ai/docs/network-policies — no statement on control-plane-failure behavior found

### network_audit
network_audit: Unknown — no dedicated per-request egress log/audit feature is documented for Network Policies. The Dashboard's "Log Viewer" streams general Devbox logs (stdout/stderr) and the "Resource Monitoring" view tracks CPU/mem/storage — neither is described as a network-request audit trail.
Sources:
- https://docs.runloop.ai/docs/tools/dashboard — "Deep dive into Devbox logs with real-time streaming and querying" (general logs, not specifically network/egress audit)

### workspace_modes
workspace_modes: Partial — no live host bind-mount exists (Runloop is remote-only cloud compute, so there is no "host" filesystem to live-share by default). File movement is one-way/pull-based: `rli devbox upload/scp/rsync/download`, Blueprint build-context upload, or git "code mounts" (clone-based, PAT-authenticated). Ephemeral, branchable disk-snapshot state is well supported as the platform's answer to reusable/persistent environments.
Sources:
- https://docs.runloop.ai/docs/tools/cli — upload/scp/rsync/download file-transfer commands; "the documentation does not mention bind mounts or persistent local filesystem mounts"
- https://docs.runloop.ai/docs/devboxes/snapshots — "Currently only disk snapshots are supported."

### observability
observability: Yes — Dashboard provides a real-time streaming/queryable Log Viewer, historical Resource Monitoring graphs (CPU/mem/storage) across Devboxes, a browser-based shell, and Advanced Search filtering by status/metadata/time-range. Resource-optimization *recommendations* are explicitly marked not-yet-shipped.
Sources:
- https://docs.runloop.ai/docs/tools/dashboard — "Track and optimize CPU, memory, and storage usage across your Devboxes"; optimization features marked "(Coming Soon)"

### supervision
supervision: No — the Dashboard/CLI/API give passive visibility (logs, metrics, manual shutdown) but no documented active-intervention layer was found: no automated anomaly detection, no policy-triggered kill/quarantine, no platform-initiated containment response to detected misbehavior. Stopping a Devbox is a manual action the customer's own code/operator must take (`devbox.shutdown()`), not something the platform does on its own.
Sources:
- https://docs.runloop.ai/docs/devboxes/start-stop — manual `devbox.shutdown()` and configured idle/lifetime timers are the only documented lifecycle-ending mechanisms; no anomaly-triggered intervention found

### fleet_mgmt
fleet_mgmt: Yes — Dashboard Advanced Search filters Devboxes by status/metadata/creation-time across an account; a "Coordination" product surface ("Orchestrate multi-agent workflows with shared state and secrets") plus shared Object/Secret stores ("Reuse tools, files, and keys") support multi-agent fleets; billing includes a distinct "agent coordination" line item.
Sources:
- https://www.runloop.ai/ — "Orchestrate multi-agent workflows with shared state and secrets"
- https://docs.runloop.ai/docs/tools/dashboard — status/metadata/time-range search across Devboxes
- https://runloop.ai/pricing — "Agent coordination: $0.006/active axon-hour"

### snapshots_persistence
snapshots_persistence: Yes — Devbox Snapshots capture **disk state only** (no memory/process state), can be used to fan out multiple new Devboxes from one saved state (branching), accelerate builds via cached state, and enable rollback to "a known good point in time." Snapshots persist indefinitely (storage-billed) until explicitly deleted. SSH keys separately persist across suspend/resume cycles.
Sources:
- https://docs.runloop.ai/docs/devboxes/snapshots — "Snapshots can be used to save the current disk state of a Devbox, and to create new Devboxes from a previously saved state."
- https://docs.runloop.ai/docs/devboxes/ssh — SSH keys "survive the full Devbox lifecycle" through suspend/resume

## D. Setup

### setup
setup: Easy — sign up (no card required for the $50-credit trial), export `RUNLOOP_API_KEY`, `pip install runloop_api_client` or `npm install @runloop/api-client`, run a short script; "Creating and starting a Devbox takes just seconds." No local Docker/Kubernetes prerequisite since compute is remote — only a language runtime + API key.
Sources:
- https://docs.runloop.ai/docs/tutorials/quickstart — "Creating and starting a Devbox takes just seconds"

## E. Daily use

### daily_use
daily_use: Moderate — day-to-day interaction is via CLI (`rli devbox ssh <id>`, upload/scp/rsync), SDK calls, or the Dashboard's browser shell/log viewer. SSH sessions auto-disconnect after 15 minutes of inactivity unless the user configures `ServerAliveInterval` keepalive. Devboxes can be configured with an idle-timeout or max-lifetime that will suspend/shut them down mid-session if not tuned correctly, which is a source of workflow interruption not present in a purely local sandbox. Blueprint rebuilds are needed when the base image/deps change.
Sources:
- https://docs.runloop.ai/docs/devboxes/ssh — 15-minute inactivity disconnect; keepalive workaround
- https://docs.runloop.ai/docs/devboxes/start-stop — idle/lifetime auto-suspend-shutdown configuration

## F. Configuration

### config_depth
config_depth: Deep, but API/SDK-driven rather than a single declarative project file — Blueprints (custom Dockerfile, build context, files/mounts, install/launch commands), per-Devbox `launch_parameters` (network_policy_id override, secrets mapping, `resource_size_request` custom CPU/mem/disk, `lifecycle.after_idle` / `keep_alive_time_seconds`), and code-mount credential/setup_commands are all independently configurable via the Python/TS SDK, CLI, or REST API. There is no single versionable project-root config file analogous to a `clawker.yaml`; configuration is composed of separate API-created resources (Blueprint, NetworkPolicy, Secret) referenced by ID/name.
Sources:
- https://docs.runloop.ai/docs/devboxes/blueprints/overview — Dockerfile-based Blueprint creation, `blueprint_name` vs `blueprint_id` versioning guidance
- https://docs.runloop.ai/docs/devboxes/configuration/sizes — custom resource sizing parameters

### policy_model
policy_model: Moderate — most security/workspace/network behaviors (network policy, secrets, resource sizing, idle/lifetime behavior, git-credential handling) are independent, swappable per-resource objects that can be overridden per Devbox or Blueprint (e.g., a Devbox can pick a different `network_policy_id` than its Blueprint's default). This gives real per-run flexibility. However there is no unified declarative policy file tying these together, and — per `firewall_escape_hatch` above — no timed/self-reverting security bypass; loosening network policy is a durable object edit, not a scoped break-glass action.
Sources:
- https://docs.runloop.ai/docs/devboxes/blueprints/network-policies — build-time vs. runtime policy separation; runtime override at Devbox creation

## G. DX — host↔sandbox integration

### bind_mount_sharing
bind_mount_sharing: No — no live bind-mount between a host machine and a Devbox is documented anywhere in the CLI, SDK, or overview docs; all file movement is explicit, one-directional-at-a-time (upload/scp/rsync/download, or git clone via code mounts).
Sources:
- https://docs.runloop.ai/docs/tools/cli — file-transfer command list; no bind-mount option present

### cred_forwarding
cred_forwarding: Partial — for LLM/tool-provider API credentials and MCP server credentials, Agent Gateways / MCP Hub provide strong mediation (opaque devbox-bound tokens, real keys never enter the devbox). For git, the model is **copy, not forwarding**: a GitHub Personal Access Token is placed into the devbox's environment/git-credential-cache (`GH_TOKEN`) via HTTPS auth — there is no live ssh-agent or gpg-agent forwarding from the host documented anywhere, and GPG (commit signing) is not mentioned at all in the SSH or code-mounts docs.
Sources:
- https://docs.runloop.ai/docs/devboxes/mounts/code-mounts — "Runloop automatically sets up the `GH_TOKEN` environment variable and credential cache for you." (HTTPS-based, PAT copy — not agent forwarding)
- https://docs.runloop.ai/docs/devboxes/agent-gateways — token-mediation model for LLM API credentials

### browser_auth
browser_auth: Unknown — no documentation describes a mechanism for a process inside a Devbox to trigger a browser-open on the *developer's host machine* for OAuth/device-code login flows (the pattern `gh auth login` or `claude` login rely on). The Dashboard offers its own in-browser terminal (browser-to-cloud, not cloud-to-host-browser), which is a different mechanism and doesn't address this criterion.
Sources:
- https://docs.runloop.ai/docs/tools/dashboard — browser-based shell exists, but is not described as a host-browser-auth-proxy mechanism

### shared_dirs
shared_dirs: Partial — no "host directory" concept exists (cloud-only execution), but Runloop offers cloud-native equivalents: Object storage (uploadable/downloadable files with TTL), shared Secret store, and code mounts, marketed as letting teams "Reuse tools, files, and keys" across agents/Devboxes.
Sources:
- https://www.runloop.ai/ — "Agent, Object, & Secret store for seamless Agentic development"; "Reuse tools, files, and keys"

### git_worktrees
git_worktrees: Unknown — no mention of git worktree support was found. The documented "code mounts" feature clones a single repo/branch per mount (`repo_name`, `repo_owner`, `token`); no worktree-specific API or multi-branch-per-repo pattern was found in the pages fetched.
Sources:
- https://docs.runloop.ai/docs/devboxes/mounts/code-mounts — code-mount parameters cover single repo/owner/token, no worktree mention found

### nested_containers
nested_containers: Yes — Docker-in-Docker is supported via dedicated pre-built Blueprint images (`runloop/ubuntu-dnd-x86_64`/`arm64`, `runloop/universal-ubuntu-24.04-*-dnd`), all running as root; a custom Blueprint can also `FROM runloop:runloop/ubuntu-dnd-*`. Privileged-mode / security implications of this root+DinD setup are not discussed in the docs.
Sources:
- https://docs.runloop.ai/docs/devboxes/capabilities/docker-in-docker — "Runloop solves this problem by allowing you to run AI agents alongside Docker-based services all on the same Devbox."

### harness_agnostic
harness_agnostic: Yes — Devboxes are generic Linux compute environments (exec/SSH/API access, arbitrary Blueprint-installed tooling); marketing explicitly claims "Framework agnostic," and no documentation ties Devbox functionality to a specific coding-agent CLI/vendor. This is inferred from the general-purpose architecture plus the explicit framework-agnostic claim, since no dedicated "supported agents/harnesses" compatibility page was found.
Sources:
- https://www.runloop.ai/ — "Framework agnostic and lightning-fast starts"

## H. Performance

### performance
performance: Vendor-claimed lightweight, not independently verified — official marketing cites "2x faster vCPUs," "~100ms" command execution, "<2s" boot for a 10GB image, and resume-from-standby figures; a third-party comparison blog (Northflank, itself a competing vendor) separately reports "resume from standby in under 25ms" and "30,000+ concurrent environments" attributed to Runloop, which is a larger number than the homepage's own "10k+ parallel sandboxes" claim — the discrepancy is unresolved and both figures are unverified against any independent benchmark. No third-party/independent benchmark suite result was found. Bind-mount IO performance is not applicable (no bind mount exists).
Sources:
- https://www.runloop.ai/ — "2x faster vCPUs"; "ultra fast command execution at 100ms"; "10GB image startup time in <2s"; "Run 10k+ parallel sandboxes"

## I. Feasibility

### feasibility
feasibility: Adoptable today, with startup/lock-in caveats — works from any OS since interaction is via cloud API/SDK/CLI/SSH (no local Docker/k8s prerequisite); solo developers can start on the free $50-credit trial (capped at 3 running Devboxes, 5 Blueprints, 10 snapshots, 3 objects). Caveats: proprietary API/CLI surface (not a portable/open spec — see `open_source`), an early-stage company (small team, single disclosed $7M seed round), and at least one Dashboard feature area explicitly marked "Coming Soon," suggesting the product is still maturing.
Sources:
- https://runloop.ai/pricing — trial limits: "3 running devboxes, 5 blueprints, 10 snapshots, 3 objects"
- https://docs.runloop.ai/docs/tools/dashboard — Resource Optimization marked "(Coming Soon)"

## J. Price (prose-only)

Free "Basic" tier: $50 trial credit, no card required, capped resources (see feasibility), 100GB free storage, includes public benchmarks/devboxes/blueprints/snapshots/email support. "Pro": $250/month + usage, adds suspend/resume, custom benchmarks, repo connections, beta access, Slack support, 1TB free storage. "Enterprise": custom/contact-sales, adds reinforcement fine-tuning, priority support, "Deploy to VPC," custom storage, multi-region. Usage-based compute (all tiers): $0.108/CPU-hour, $0.0252/GB-memory-hour; storage $0.00034236/GB-hour (devbox) or $0.000072/GB-hour (blueprints/snapshots/objects); agent coordination $0.006/active "axon"-hour, $0.20/GB-month for inactive storage. No open-source/self-host-for-free option beyond the MIT-licensed SDK client libraries.
Sources:
- https://runloop.ai/pricing — full tier/pricing breakdown as summarized above

## K. Extensibility

### extensibility
extensibility: Yes — Blueprints support fully custom Dockerfiles and custom/private registries (not just a fixed base-image list); code mounts integrate arbitrary git repos with per-repo credential config and post-clone `install_command`; Agent Gateways proxy arbitrary LLM providers; MCP Hub exposes arbitrary MCP tool servers behind glob-based per-tool allow-filtering; the full platform is programmable via Python/TS SDK, CLI, and REST API; documented tutorial integrations exist for third-party browser-use providers (Browserbase, Kernel).
Sources:
- https://docs.runloop.ai/docs/devboxes/blueprints/overview — `rli blueprint from-dockerfile` custom Dockerfile builds
- https://docs.runloop.ai/docs/devboxes/mcp-hub — glob-pattern tool filtering (e.g. `["github.search_*"]`) per MCP server

## Unknowns & caveats

- **Underlying hypervisor/microVM technology**: Not confirmed by any official source. Homepage says "micro-VM* environment... custom bare-metal hypervisor" with an unresolved footnote asterisk; no doc names Firecracker, Cloud Hypervisor, KVM directly, or any other specific tech. This matches the task's own "provisional" flag — treat `built_on` as vendor-asserted, not independently verified.
- **Network-policy enforcement internals** (the axis requiring deepest scrutiny) are almost entirely undocumented: enforcement plane (kernel/eBPF vs. proxy vs. hypervisor vs. cloud infra), DNS-vs-connection-level blocking mechanism, TLS/MITM inspection, protocol coverage beyond "egress traffic" generically, fail-closed behavior on control-plane outage, and per-request audit logging are all Unknown — docs are silent, not confirmatory-negative, on each.
- **Third-party "agentsh" (canyonroad/agentsh-runloop) confusion avoided**: an early search surfaced claims that Runloop blocks cloud-metadata endpoints (`169.254.169.254`) by default. Verified this is a **third-party open-source wrapper** ("agentsh," an independent execution-layer security engine that wraps multiple sandbox providers including Runloop) adding that protection on top — **not** a native Runloop capability. Excluded from all Runloop determinations above; Runloop's own docs state the opposite default ("unrestricted network access" out of the box).
- Two official doc URLs cited by search results/third parties initially 404'd at their expected paths (`/docs/devboxes/docker-in-docker`, `/docs/devboxes/code-mounts`) — both were located at different current paths via the site's sitemap.xml (`/docs/devboxes/capabilities/docker-in-docker`, `/docs/devboxes/mounts/code-mounts`) and fetched successfully; not a firewall/network block, just stale/moved doc URLs.
- Founding-year discrepancy (2023 per official "About" page vs. 2024 per some third-party trackers) left unresolved — noted, not adjudicated.
- No dedicated official "Security & Compliance" doc page was found (only homepage/About marketing copy asserting SOC2/HIPAA/GDPR); no compliance report or audit artifact was located or fetchable in this pass.
- git_worktrees and browser_auth (host-browser OAuth proxying) could not be confirmed or denied — true doc silence, correctly left Unknown rather than guessed.

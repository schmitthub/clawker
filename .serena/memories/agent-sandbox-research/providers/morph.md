# Morph (Morph Cloud / cloud.morph.so)
category: cloud
Cloud VM platform ("cloud computers for every agent") providing Devboxes (workspaces) and Instances/Snapshots (VM primitives) with fast snapshot/branch/restore ("Infinibranch") | built on: VM ("MorphVM"), specific hypervisor undisclosed | license: proprietary SaaS platform; client SDKs Apache-2.0 | maturity: Morph Labs, founded 2023 by ex-OpenAI researcher Jesse Han; $5.75M seed (Sept 2024, Khosla Ventures, third-party source); thin public GitHub footprint (SDK repos, 3-4 stars each)

Note: a different, unrelated product exists at docs.morphllm.com ("Morph" fast-apply code LLM, morphllm.com) — NOT the same company/product as Morph Cloud (cloud.morph.so). This writeup covers Morph Cloud only; a self-hosting doc found at docs.morphllm.com/api-reference/self-hosting was excluded as out-of-scope/wrong product.

## A. Identity
### built_on (prose-only)
Docs state every instance is a VM: "Every virtual machine (VM) running in Morph Cloud is an instance" and each instance boots from a snapshot. Devboxes are described as "full virtual machines capable of running any configuration of containers: docker-in-docker, a local Kubernetes cluster, self-hosted Supabase, etc.", with experimental support for Windows, OSX (x86), and nested virtualization. **No official source (full docs nav, blog post "Introducing Infinibranch", GitHub org) names the hypervisor** — no mention of Firecracker, KVM, QEMU, or gVisor found anywhere. The breadth of guest support (DinD, k8s, Windows/OSX guests, nested virtualization) is atypical of a stripped-down microVM stack like Firecracker (which historically has limited nested-virt and no Windows guest support), weakly suggesting a fuller VM/hypervisor stack — but this is inference from indirect capability claims, not a documented confirmation. **The "microVM?" question in the provider note cannot be confirmed either way from official sources; treat as unconfirmed/provisional.**
Sources:
- https://cloud.morph.so/docs/documentation/overview — "Instances and snapshots are the underlying infrastructure primitives. Every virtual machine (VM) running in Morph Cloud is an instance"
- https://cloud.morph.so/docs/devboxes/getting-started — "full virtual machines capable of running any configuration of containers: docker-in-docker, a local Kubernetes cluster, self-hosted Supabase, etc."
- https://cloud.morph.so/docs/blog/developers — Infinibranch post; no hypervisor named, only "<250ms" snapshot/branch/restore claims

### execution_locality
Remote — all agent/dev workloads run on Morph's cloud VM instances (cloud.morph.so infra). Local machine only runs the SDK/CLI/SSH/VSCode-remote client. No documented local-execution mode. No self-host option found for the Morph Cloud VM platform itself (the self-hosting doc that surfaced in search belongs to the unrelated docs.morphllm.com product, not this platform). Code/credentials placed inside a devbox (e.g., ANTHROPIC_API_KEY, git auth) reside on Morph's infrastructure, not the developer's machine.
Sources:
- https://cloud.morph.so/docs/examples/claude-code — setup script configures `MORPH_API_KEY`/`ANTHROPIC_API_KEY` and git auth inside the remote instance
- https://cloud.morph.so/docs/devboxes/getting-started — "export MORPH_DEVBOX_BASE_URL=\"https://devbox.svc.cloud.morph.so\""

### open_source (prose-only)
Platform is closed/proprietary SaaS (no self-host option documented for cloud.morph.so). Client libraries are open source: `morph-python-sdk` and `morph-typescript-sdk` on GitHub, both Apache-2.0 licensed.
Sources:
- https://github.com/morph-labs — morph-typescript-sdk (Apache-2.0), morph-python-sdk (Apache-2.0)

### maturity (prose-only)
Morph Labs, San Francisco, founded 2023 by Jesse Han (ex-OpenAI research scientist, GPT-4/theorem-proving background). Raised $5.75M seed in September 2024 led by Khosla Ventures (third-party source, not vendor docs). Public GitHub footprint is thin (2 SDK repos, single-digit stars) since the core product is closed SaaS rather than an open-source project.
Sources:
- https://startupintros.com/orgs/morph — third-party: "Morph Labs raised $5.75 million in seed funding led by Khosla Ventures" (Sept 2024)
- https://github.com/morph-labs — repo list/star counts

## B. Threat protection
### host_fs_damage
Yes — agent code executes inside a remote Morph-hosted VM instance, never on the developer's host machine; the host filesystem is structurally unreachable because execution locality is Remote (see A). This is inherent to the remote-VM architecture, not a configurable feature.
Sources:
- https://cloud.morph.so/docs/documentation/overview — instances are VMs on Morph's infra, accessed via API/SSH from the client

### credential_theft
Partial — devboxes support scoped "agent tokens" as a least-privilege alternative to exposing a full `MORPH_API_KEY` to a self-managing agent, limiting blast radius for the Morph control-plane API specifically. However, for the workload's OWN secrets (e.g., `ANTHROPIC_API_KEY`, git credentials), the documented pattern is to configure them directly inside the remote instance (copied into env/config), not mediated/forwarded the way an ssh-agent-forwarding model would do it — so once inside the VM, those secrets are as exposed to a compromised agent process as on any plain VM.
Sources:
- https://cloud.morph.so/docs/devboxes — "devbox-scoped agent tokens safely for self-managing agents running inside a Morph Devbox"
- https://cloud.morph.so/docs/examples/claude-code — script sets `ANTHROPIC_API_KEY` and configures git auth inside the instance

### data_exfiltration
Unknown — exhaustively reviewed every page in the docs site navigation (Start Here, Concepts, Devboxes incl. getting-started, Actions, EFS, Responses Proxy, REST API, CHANGELOG, Setup/{signing-up,api-keys,ssh-keys}, Instances/{creating-snapshot,snapshot-setup,basic-lifecycle,pause-resume,branch,metadata,ssh,command-execution,file-management,fuse,http-services}, Examples/{claude-code,github-actions-on-morph}) and found **no egress allowlist, outbound firewall, DNS-blocking, or network-policy feature of any kind**. The two pages closest to networking (HTTP Services, SSH) document only INBOUND access controls (optional API-key auth on exposed HTTP services; SSH pubkey auth) with no outbound counterpart mentioned. This is thorough docs-silence, not a positive vendor statement of absence, so it is recorded Unknown per guidelines rather than No — but see network_default_posture below for directly observed default behavior.
Sources:
- https://cloud.morph.so/docs/documentation/instances/http-services — covers only inbound exposure + optional API-key auth, no outbound controls
- https://cloud.morph.so/docs/documentation/instances/ssh — covers only inbound SSH access controls

### malicious_execution
Partial — blast radius of a compromised/hallucinated process is contained to the single VM instance (crash/resource abuse doesn't escape it per the remote-VM boundary), but no documented sandboxing WITHIN the instance beyond the VM boundary itself (no seccomp/gVisor-style secondary layer mentioned), and outbound network access appears unrestricted by default (see network_default_posture), so a compromised process inside the VM is not prevented from phoning home or downloading further payloads.
Sources:
- https://cloud.morph.so/docs/documentation/overview — VM-level isolation only, no secondary in-VM sandbox documented

### escape_resistance
Unknown — docs consistently describe the isolation boundary as "a virtual machine" but never name or describe the hypervisor, its hardening, or its historical escape surface (see built_on). Without knowing the hypervisor, escape resistance relative to e.g. Firecracker/gVisor/runc cannot be assessed from official sources.
Sources:
- https://cloud.morph.so/docs/documentation/overview — "virtual machine" terminology only, no hypervisor disclosure found anywhere in docs/blog/GitHub

### resource_abuse
Yes — resource ceilings are enforced per subscription tier: Free tier up to 64 vCPU / 256 GB RAM / 1024 GB storage / 8 concurrent devboxes; Developer up to 256 vCPU / 1024 GB RAM / 4096 GB storage / 32 concurrent devboxes; Team/Scale up to 1024 vCPU / 4096 GB RAM / 16384 GB storage / 128 concurrent devboxes. Usage beyond included credits is metered in Morph Compute Units (MCUs).
Sources:
- https://cloud.morph.so/web/subscribe — tier limits: "up to 64 vCPU, 256 GB RAM, and 1024 GB storage... 8 concurrent devboxes" (Free) up to "1024 vCPU, 4096 GB RAM, and 16384 GB storage" (Team/Scale)

## C. Feature set & granularity
### network_default_posture
Open-by-default (observed) — no deny-by-default/allowlist mechanism is documented anywhere (see data_exfiltration), and the official Claude Code setup example has the agent freely install Git/GitHub CLI/Node.js/Python and `npm install`-based tooling inside a freshly started instance with no prior network-authorization or firewall-configuration step shown. This is treated as positive evidence of an open-by-default posture (actual documented default behavior), distinct from the Unknown calls below which concern whether ANY restriction feature exists at all.
Sources:
- https://cloud.morph.so/docs/examples/claude-code — setup script installs Git, GitHub CLI, Node.js, Python and Claude Code CLI via npm with no preceding egress-configuration step

### egress_allowlist
Unknown — no allow/deny outbound rule feature documented anywhere in the full docs nav (see data_exfiltration for the exhaustive page list checked). Docs silence, not a vendor statement of absence.
Sources:
- (same exhaustive nav review as data_exfiltration; no dedicated networking/firewall doc section exists in the site nav at all)

### dns_level_blocking
Unknown — no DNS-layer control documented; dependent on egress_allowlist which is itself undocumented.
Sources:
- (no page found; full nav reviewed, see data_exfiltration)

### tls_mitm_inspection
Unknown — no TLS interception / L7 inspection feature documented.
Sources:
- (no page found; full nav reviewed, see data_exfiltration)

### http_path_rules
Unknown — the only path/route-related feature documented is INBOUND HTTP Service exposure (`expose_http_service`, optional API-key auth), which is not a path/method-scoped outbound rule system.
Sources:
- https://cloud.morph.so/docs/documentation/instances/http-services — inbound exposure + API-key auth only, no path/method rule language

### proto_coverage
Unknown — no protocol-scoped egress control (DNS/ICMP/TCP/UDP/QUIC/ssh/ws/grpc) documented in either direction beyond the basic inbound SSH (port 22 via a shared `ssh.cloud.morph.so` endpoint) and inbound HTTP service exposure primitives.
Sources:
- https://cloud.morph.so/docs/documentation/instances/ssh — `ssh <morphvm>@ssh.cloud.morph.so` centralized endpoint, no protocol-scoping language
- https://cloud.morph.so/docs/documentation/instances/http-services — HTTP-only inbound exposure

### live_rule_reload
NA — no rule system exists to document a reload capability for.

### firewall_escape_hatch
NA — no firewall/network-policy feature exists to document an escape hatch for.

### enforcement_plane
Unknown — no network-policy enforcement plane (kernel/eBPF, userspace proxy, VM-boundary, cloud infra) documented; VM boundary itself is the only isolation layer disclosed (see escape_resistance), and it is undocumented whether host-level/cloud-network infra applies any egress policy invisibly to the user.
Sources:
- (no page found; full nav reviewed)

### fail_closed
NA — no enforcement plane is documented to assess fail-closed behavior for.

### network_audit
Unknown — no per-request egress log documented. The only access-adjacent audit-like feature is the Responses Proxy's telemetry/metrics dashboard, which covers LLM-provider API routing traffic specifically, not general instance egress.
Sources:
- https://cloud.morph.so/docs/responses-proxy — "recording telemetry" and "maintaining durable routing state" scoped to registered LLM provider routing, not general network egress

### workspace_modes
Partial — only snapshot/copy-based persistence is offered; no live host bind-mount. Environment state is captured via images → snapshots → instances → branches, and file transfer between local machine and instance is explicit one-way SFTP-based copy (`morphcloud instance copy`, SDK SFTP helpers) — not a continuous live-sync/bind-mount. EFS adds Morph-managed persistent/mountable storage (FUSE-based) as a separate primitive, distinct from a host-shared directory.
Sources:
- https://cloud.morph.so/docs/documentation/instances/file-management — "All transfers are explicit copy operations rather than live bind-mounts or continuous synchronization"
- https://cloud.morph.so/docs/efs — EFS described as filesystems/mounts/shared mounts, FUSE-based, Morph-managed (not host-shared)

### observability
Partial — dashboard shows instance/devbox status and (for Responses Proxy users) LLM-provider routing metrics/telemetry; no general agent-activity logging, metrics, or dashboard for arbitrary in-VM process behavior is documented.
Sources:
- https://cloud.morph.so/docs/responses-proxy — "dashboard for provider management, metrics tracking, routing state visibility, and telemetry analysis" (LLM routing scope only)

### supervision
No — no runtime supervisor/control-plane that observes agent behavior and can intervene (containment, kill, quarantine) is documented. Management is user-driven via API/CLI/dashboard (start/stop/pause/branch), not an autonomous overseer process watching the agent.
Sources:
- https://cloud.morph.so/docs/documentation/instances/basic-lifecycle — lifecycle ops are explicit user/API-initiated (start/stop/save), no autonomous supervision described

### fleet_mgmt
Yes — instances/devboxes are listable via API/CLI/dashboard with metadata tagging (dedicated Metadata doc), org-scoped devbox sharing, and per-plan concurrent-devbox caps (8/32/128 across Free/Developer/Team). The Branch primitive launches a specified number of new instances from one snapshot for fan-out (RL environments, test-time scaling, parallel testing).
Sources:
- https://cloud.morph.so/docs/documentation/instances/branch — "Branching creates a snapshot of an instance and then launches a specified number of new instances from that snapshot"
- https://cloud.morph.so/web/subscribe — concurrent devbox caps per tier

### snapshots_persistence
Yes — core primitive of the platform. Snapshots are immutable, bootable filesystem copies; pause "creates a new snapshot and suspends the VM," preserving full memory/process state ("perfect process preservation") so a resumed/restored instance continues exactly where processes left off, including active background processes. EFS provides an additional persistent/ephemeral mountable storage layer independent of instance lifecycle.
Sources:
- https://cloud.morph.so/docs/documentation/instances/pause-resume — "create a new snapshot and suspend the VM"
- https://cloud.morph.so/docs/developers — "perfect process preservation" claim for snapshot restore

## D. Setup
### setup
Easy-to-moderate — prerequisites are a Morph account + API key from the dashboard, then `pip install morphcloud` / `npm install morphcloud` or the CLI; no local Docker/Kubernetes needed since execution is remote. Three documented paths to a first devbox (CLI, dashboard, API). Getting a full coding-agent harness running (e.g., Claude Code) requires additional scripted steps beyond bare signup — installing Node/git/gh, npm-installing the agent CLI, and creating a tmux session — shown as a multi-step Python setup script in the official example, not a single command.
Sources:
- https://cloud.morph.so/docs/devboxes/getting-started — API key + `pip install morphcloud`/`npm install morphcloud`; CLI/Dashboard/API paths
- https://cloud.morph.so/docs/examples/claude-code — multi-step scripted setup for a working agent devbox

## E. Daily use
### daily_use
Moderate — devboxes support wake-on-SSH/HTTP and TTL-based auto-pause of idle machines (cost-friendly), one-click VSCode/Cursor SSH access, tmux sessions to reattach to long-running agent work, and org-internal URL sharing of a live devbox. Countervailing friction: since there is no live bind-mount (see workspace_modes), a local-edit + remote-exec workflow requires explicit SFTP-style copy commands rather than instant file sync; day-to-day work is smoothest when done entirely inside the remote VSCode/SSH session rather than split between local editor and remote execution.
Sources:
- https://cloud.morph.so/docs/devboxes/getting-started — wake-on-SSH/HTTP, TTL auto-pause, one-click VSCode/Cursor, tmux
- https://cloud.morph.so/docs/documentation/instances/file-management — copy-only file transfer model

## F. Configuration
### config_depth
Moderate — snapshots function as reusable, versionable base images (dedicated "Snapshot Setup"/"Creating Snapshot" docs) baking OS, packages, and config into a bootable state; instance/devbox metadata tagging is supported. However, there is no single declarative project-config file (workflow is imperative: SDK/CLI calls or scripted setup, as in the Claude Code example) and no documented lifecycle-hook concept (e.g., post-init/pre-run scripts) beyond "run your own script after boot." Network-rule scope to tune is absent entirely (axis C).
Sources:
- https://cloud.morph.so/docs/concepts/mental-model — snapshot-based "boot and run" (image→snapshot→instance) and "checkpoint and fork" workflows
- https://cloud.morph.so/docs/examples/claude-code — imperative Python setup script, not a declarative manifest

### policy_model
Rigid — no documented policy toggles for network restriction (no such feature exists per axis C), workspace mode (bind-mount vs copy — only copy is offered), or per-run security tightening. The SDK/API exposes VM lifecycle primitives (start/stop/pause/snapshot/branch), not a security-policy layer with switchable defaults; environment behavior is entirely whatever the user's own images/scripts configure.
Sources:
- (synthesized from full axis C and workspace_modes findings above — no policy-toggle documentation found anywhere in the site)

## G. DX — host↔sandbox integration
### bind_mount_sharing
No — file transfer is one-way explicit copy (SFTP-based upload/download via SDK or `morphcloud instance copy`), not a live bind mount; no continuous host↔instance sync documented.
Sources:
- https://cloud.morph.so/docs/documentation/instances/file-management — "No Native Sync: The documentation provides no evidence of automatic sync mechanisms, watch-based file mirroring, or persistent mount options"

### cred_forwarding
Partial — SSH access uses registered public keys (no password auth); devboxes support scoped "agent tokens" as least-privilege alternative to a full Morph API key for self-managing agents; devbox getting-started mentions "Git authentication integration" for git operations. No ssh-agent/gpg-agent forwarding mechanism (in the sense of mediating host keys without copying them) is documented — the pattern shown for workload secrets (e.g. `ANTHROPIC_API_KEY`) is direct configuration inside the remote instance.
Sources:
- https://cloud.morph.so/docs/documentation/instances/ssh — "Your SSH public key must be registered with Morph Cloud first"
- https://cloud.morph.so/docs/devboxes/getting-started — "Git authentication integration," scoped agent tokens
- https://cloud.morph.so/docs/examples/claude-code — `ANTHROPIC_API_KEY` set directly in the remote instance

### browser_auth
Unknown — no documentation found describing a host-browser OAuth/device-code proxying mechanism (e.g., a sandboxed `gh auth login` triggering a browser open on the developer's local machine with the callback forwarded back in). Devbox access is via VSCode/Cursor remote-SSH and tmux; whether OAuth flows requiring a browser redirect work smoothly (vs. requiring manual token copy-paste) is not addressed in any reviewed page.
Sources:
- (no page found; devboxes/getting-started and claude-code example reviewed, neither mentions browser-auth proxying)

### shared_dirs
Partial — EFS ("Ephemeral File System") offers additional persistent/mountable storage beyond the base instance disk, including "shared mounts" per its concepts summary, but this is Morph-managed cloud storage mounted into the VM (FUSE-based), not a host directory shared into the sandbox.
Sources:
- https://cloud.morph.so/docs/efs — "filesystems, mounts, shared mounts, and lifecycle"; "Technical requirements exist for Linux and FUSE compatibility"

### git_worktrees
Unknown — no mention of git worktree support (first-class or otherwise) found in any reviewed doc; example scripts use plain `git clone`.
Sources:
- https://cloud.morph.so/docs/examples/claude-code — plain git clone/GitHub CLI usage shown, no worktree language

### nested_containers
Yes — devboxes are explicitly documented as able to run "any configuration of containers: docker-in-docker, a local Kubernetes cluster, self-hosted Supabase, etc.," i.e., Docker-in-Docker and nested container runtimes are supported inside the VM.
Sources:
- https://cloud.morph.so/docs/devboxes/getting-started — "full virtual machines capable of running any configuration of containers: docker-in-docker, a local Kubernetes cluster, self-hosted Supabase, etc."

### harness_agnostic
Partial — the platform is a general-purpose VM (any CLI tool can be installed via the setup script), and official examples cover both Claude Code and Codex-via-Responses-Proxy, showing more than one harness works in practice. However, no page makes a documented "any agent CLI works, unmodified" guarantee — each example is its own bespoke bootstrap script, and the Responses Proxy (multi-provider LLM routing) is presented as a distinct, opt-in feature rather than something all harnesses get automatically.
Sources:
- https://cloud.morph.so/docs/examples/claude-code — Claude Code-specific bootstrap
- https://cloud.morph.so/docs/responses-proxy — "integrates with Claude Code through configurable aliases for Anthropic, OpenAI Responses, and OpenAI-compatible Chat Completions endpoints"; also mentions Codex

## H. Performance
### performance
Unknown/vendor-claimed only — the Infinibranch blog post claims "<250ms" for snapshot/branch/restore operations and "near-zero overhead branching," but this is a vendor marketing claim with no methodology, no independent/third-party benchmark, and no disclosed baseline hardware. No published cold/warm boot latency for a plain instance start (vs. branch-from-snapshot), no disk footprint, RAM overhead, or IO throughput numbers found anywhere in official docs.
Sources:
- https://cloud.morph.so/docs/blog/developers — vendor claim: "<250 ms startup times" / "near-zero overhead branching" (Infinibranch announcement, no methodology disclosed)

## I. Feasibility
### feasibility
Adoptable-with-caveats — client side is platform-agnostic (any OS with SSH/SDK/CLI can drive it, since execution is entirely remote); no local Docker/Kubernetes prerequisite. Countervailing: full dependency on Morph's SaaS (API key + account required, ongoing lock-in to their control plane, pricing tiers cap concurrency/vCPU), and the vendor is an early-stage startup (founded 2023, single $5.75M seed round as of the last found funding data) — a longevity/stability risk for teams building durable workflows on top of it. Network-egress-control gap (axis C) is a material feasibility concern for security-conscious adoption specifically.
Sources:
- https://cloud.morph.so/web/subscribe — account/API-key-gated tiers with hard resource/concurrency caps
- https://startupintros.com/orgs/morph — third-party: single seed round found, no later-stage funding data located

## J. Price (prose-only)
Usage-based subscription with three published tiers: **Free** — $0/mo, 300 MCU starting credit, up to 64 vCPU / 256 GB RAM / 1024 GB storage, 8 concurrent devboxes. **Developer** — $40/mo, 1,000 MCU credit, up to 256 vCPU / 1024 GB RAM / 4096 GB storage, 32 concurrent devboxes. **Team/Scale** — $250/mo, 7,500 MCU credit, up to 1024 vCPU / 4096 GB RAM / 16384 GB storage, 128 concurrent devboxes. Usage beyond included credits is pay-as-you-go, billed in Morph Compute Units (MCUs); GitHub Actions runners separately priced 3–10 MCU/hour by size, marketed as "about 30-45% below industry-leading alternatives" (vendor claim). No self-host option found for the Morph Cloud VM platform.
Sources:
- https://cloud.morph.so/web/subscribe — tier pricing, MCU credits, resource caps, GitHub Actions runner MCU/hour rates

## K. Extensibility
### extensibility
Partial — custom environments are built via reusable snapshot-based images/templates (own package/config baked in); Responses Proxy lets users register custom model-provider aliases/adapters (OpenAI, OpenAI-compatible Chat, Kimi/OpenRouter, Anthropic) for routing coding-agent LLM calls through a unified proxy with telemetry; REST API plus Python/TypeScript SDKs support custom automation/tooling. No plugin/bundle system or a declarative custom-harness-definition format is documented — extending the platform means scripting your own snapshot setup, not authoring a manifest against a defined extension point.
Sources:
- https://cloud.morph.so/docs/responses-proxy — custom provider alias registration, multi-provider adapters
- https://cloud.morph.so/docs/documentation/instances/snapshot-setup — (referenced in nav) reusable snapshot-based environment customization
- https://cloud.morph.so/docs/developers — Python/TypeScript SDK + REST API references

## Unknowns & caveats
- **Underlying hypervisor/VM tech is unconfirmed.** No official source names Firecracker, KVM, QEMU, gVisor, or any specific stack; only "virtual machine"/"MorphVM" terminology is used. The provider note's "microVM?" question could not be resolved from official documentation, blog, or public GitHub — treat as open/provisional, not settled.
- **The entire network-egress-control cluster (egress_allowlist through network_audit) is Unknown, not No**, per guideline rules on docs silence — despite an exhaustive read of every page in the site's documentation navigation (confirmed via the full sidebar dump) turning up zero mention of any outbound network policy, DNS blocking, MITM/TLS inspection, path rules, protocol coverage, live reload, escape hatch, enforcement plane, or fail-closed behavior. The one exception is network_default_posture, which is called "open-by-default" based on directly observed behavior (the official Claude Code example freely reaches npm/apt/git registries with no prior firewall setup step), a materially different kind of evidence than the absence-of-a-feature calls.
- **escape_resistance is Unknown** — cannot be assessed without knowing the hypervisor.
- **browser_auth and git_worktrees are Unknown** — no documentation found either way; not enough evidence to call No.
- No WebFetch/WebSearch requests were blocked (no NXDOMAIN/connection-refused encountered); nothing to report in a blocked-URLs sense. The unrelated docs.morphllm.com self-hosting page was deliberately excluded as wrong-product, not as a blocked source.
- Funding/company-maturity facts are third-party-sourced (startupintros.com, corroborated loosely by Crunchbase/PitchBook search snippets, not independently fetched from Crunchbase/PitchBook directly) and should be treated as lower-confidence than the vendor-doc-sourced capability determinations.

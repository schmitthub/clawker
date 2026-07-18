# CodeSandbox SDK (Together Code Sandbox)
category: cloud
Programmatic Firecracker-microVM sandboxes for running untrusted/agent code, sold via npm SDK + CLI | built on Firecracker microVMs (own kernel per sandbox) | SDK/CLI client (`@codesandbox/sdk`) is MIT-licensed; hosted sandbox infrastructure itself is closed/proprietary, no self-host | CodeSandbox founded 2017 (browser IDE), acquired by Together AI Dec 2024, SDK reached GA May 2025; as of mid-2026 mid-rebrand to "Together Code Sandbox" with docs split across codesandbox.io and docs.together.ai

## A. Identity
### built_on (prose-only)
Each sandbox is a full Firecracker microVM (its own kernel, not a shared-kernel container). A lightweight in-VM "agent"/"pitcher" process (see `AgentClient`, `pitcher-protocol` in the SDK source) exposes a WebSocket control API (fs, commands, tasks, ports) that the SDK/CLI talk to. Control plane = CodeSandbox/Together's hosted API (`codesandbox.io` REST, token via `CSB_API_KEY`). CLI is `csb`.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/key-terms.mdx — "Firecracker VM: The virtualization technology that powers each sandbox, providing complete isolation and security for running untrusted code."
- https://raw.githubusercontent.com/codesandbox/codesandbox-sdk/main/CLAUDE.md — "WebSocket protocol layer (`src/pitcher-protocol/`) enables real-time communication ... `AgentClient` implements the WebSocket client for sandbox agent connections"

### execution_locality
execution_locality: Remote — all code runs on CodeSandbox/Together-managed cloud VMs; there is no local/on-device execution mode. Self-hosting is explicitly not offered, so there is no separate-deployment option either — Remote is the only mode. Project files live in the VM's own disk (`/project/workspace`); getting local files in/out is done via `git`/`fs` API upload-download or a batch write, not a live bind mount.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/faq.mdx — "No, we do not offer self hosting at this time."
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/filesystem.mdx — "All operations work within `/project/workspace`, which persists between reboots with automatic git tracking."

### open_source (prose-only)
The `@codesandbox/sdk` npm package/CLI (github.com/codesandbox/codesandbox-sdk) is MIT-licensed (package.json `"license": "MIT"`, v2.4.2 as of Dec 2025). The sandbox execution service itself is closed and hosted-only — no self-host distribution exists, so "open source" applies only to the client library/CLI, not the underlying infrastructure.
Sources:
- https://raw.githubusercontent.com/codesandbox/codesandbox-sdk/main/package.json — `"license": "MIT"`, `"version": "2.4.2"`
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/faq.mdx — "No, we do not offer self hosting at this time."

### maturity (prose-only)
CodeSandbox: founded 2017 as a browser IDE, acquired by Together AI in Dec 2024. SDK GA'd May 2025. GitHub repo: 110 stars / 38 forks / 39 releases / 328 commits (modest size for an infra SDK but actively maintained — releases roughly every 2-4 weeks through Dec 2025, e.g. v2.4.2 Dec 4 2025, v2.4.1 Oct 23 2025, v2.4.0 Oct 16 2025, v2.1.0 Aug 22 2025 added listing/OTel/CLI-debug). Infra is claimed SOC 2 Type II compliant. As of the research date, product is actively being rebranded/migrated into Together AI's platform as "Together Code Sandbox," with parallel documentation on `docs.together.ai` (thin, single page) and `codesandbox.io/docs/sdk` (the fuller, original doc set, mirrored on GitHub).
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/faq.mdx — "CodeSandbox and its infrastructure, including VMs are SOC 2 Type II compliant."
- https://codesandbox.io/blog/joining-together-ai-introducing-codesandbox-sdk (title only, page body 403 to WebFetch — see Unknowns)
- WebSearch summary (2026-07-18): "CodeSandbox ... was acquired by Together AI in December 2024 ... SDK reached general availability in May 2025 and is being rebranded as Together Code Sandbox."

## B. Threat protection
### host_fs_damage
host_fs_damage: Yes — each sandbox is an isolated Firecracker microVM with its own filesystem; there is no host bind-mount by default, so an agent inside the VM has no path to the operator's host filesystem at all (not merely a permission boundary — it structurally isn't reachable).
Sandboxes only see `/project/workspace` and the rest of the VM's own disk; files move in/out only via explicit `fs` API calls or git push/pull that the integrator's backend controls.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/key-terms.mdx — "Sandbox: An isolated, persistent virtual environment supported by a Firecracker Virtual Machine"
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/filesystem.mdx — "All operations work within `/project/workspace`"

### credential_theft
credential_theft: Partial — git credentials (access tokens) and env vars are set explicitly per user/session through the SDK; this is token injection into the VM, not host ssh-agent/gpg-agent mediation. Docs themselves flag the exposure risk once injected.
"Git Configuration - Set email, name, and optional credentials (access tokens, provider details)" is stored per-user; the docs warn against writing this to shared/public sandboxes because it persists reachably inside the VM once set, i.e. there's no secret-forwarding boundary — whatever token is configured is a plain credential living in the sandbox.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/users.mdx — "Do not write git/env configuration to public Sandboxes or the global user if multiple users has access to Sandbox"

### data_exfiltration
data_exfiltration: Unknown — no egress-restriction mechanism of any kind (allowlist, firewall, DNS control) was found after an exhaustive search of the official docs (core-concepts, FAQ, ports, hosts, sandbox-hosts, create, templates, key-terms, CLI, setup); the only access-control feature documented anywhere is INBOUND (who can reach a sandbox's exposed preview ports via `privacy`/host tokens), never outbound. Docs describe unrestricted-sounding capabilities ("install any dependencies," "run any Dockerfile," "run servers") with no gating step for network access in the creation/setup flow. Absence across this many docs pages is suggestive of no egress control existing, but there is no explicit vendor statement saying so, so per guidance this is recorded as Unknown rather than No.
Sources:
- https://codesandbox.io/ (WebFetch summary) — "Security through isolation ... allowing safe execution of untrusted code" (isolation framed as VM-level only, no network-egress claim)
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/sandbox-hosts.mdx — doc covers only inbound host-token access; "does not mention outbound network restrictions, firewall allowlists, or egress limitations"

### malicious_execution
malicious_execution: Partial — Firecracker VM isolation contains a compromised/hallucinated-code blast radius from spreading to the host or other tenants' VMs (see escape_resistance), but nothing in the docs describes containment of what that code can do once inside the VM (e.g., no documented egress control per data_exfiltration, no documented syscall filtering beyond the hypervisor boundary itself).
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/key-terms.mdx — "Firecracker VM ... providing complete isolation and security for running untrusted code"

### escape_resistance
escape_resistance: prose-only, but a clear determination is warranted — isolation boundary is a Firecracker microVM (dedicated kernel per sandbox), materially stronger than a shared-kernel container (runc/Docker) and the same class of isolation AWS Lambda/Fargate use for multi-tenant untrusted workloads. No known-escape disclosures were found in official docs (none would be expected to be volunteered there); no third-party CVE/escape research was reviewed (out of scope per evidence-priority rules — official docs first, and WebSearch budget ran out before a dedicated search could be run — see Unknowns).
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/key-terms.mdx — "Firecracker VM: The virtualization technology that powers each sandbox, providing complete isolation and security for running untrusted code."

### resource_abuse
resource_abuse: Yes — explicit VM tiers with hard CPU/RAM caps, selectable per sandbox, plus hibernation timeouts to reclaim idle resources.
Seven tiers from Pico (2 vCPU/1GB, 5 credits/hr) to XLarge (64 vCPU/128GB, 320 credits/hr); via the SDK API tiers are capped at Small (8 cores/16GB) — larger tiers require a custom template built via the CLI. `hibernationTimeoutSeconds` bounds idle runtime (default 300s free / 1800s pro, max 86400s).
Sources:
- https://docs.together.ai/docs/together-code-sandbox (WebFetch summary) — VM tier table: Pico 2 CPU/1GB/5 credits, ... XLarge 64 CPU/128GB/320 credits
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/specs.mdx — "SDK Sandbox VMTier parameters can be used to create/update environments up to Small (8 Cores/16GB RAM) specs."

## C. Feature set & granularity
### network_default_posture
network_default_posture: Open-by-default (inferred) — no allowlist/deny-by-default mode is documented anywhere in the sandbox creation, setup, or template flow; sandboxes are described as supporting arbitrary dependency installs, arbitrary Dockerfiles, and running arbitrary servers, none of which is gated by any network-permission step.
This is an inference from the absence of any network-permission step across `create.mdx`, `setup.mdx`, and `templates.mdx`, combined with functionality (npm/apt/pip installs, git clone, Docker builds) that requires open outbound internet to work as described — not an explicit vendor statement of "unrestricted network."
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/templates.mdx — "Docker & Environment Configuration ... Docker Compose Support: Add `docker-compose.yml` for additional services (databases, etc.)"
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/create.mdx — creation options cover template/fork/privacy/VM-tier/hibernation only, no network option

### egress_allowlist
egress_allowlist: Unknown — no allow/deny outbound list of any granularity (domain, wildcard, IP/CIDR, port, path) found anywhere in SDK types, CLI commands, or the 32-page SDK docs set. The only access-control primitives documented are inbound (`privacy`: public/private/public-hosts, host tokens for preview URLs).
Sources:
- https://raw.githubusercontent.com/codesandbox/codesandbox-sdk/main/src/types.ts — fields found: `privacy`, `hostToken`, `permission` (read/write) — all inbound/session-access, nothing egress-related
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/sandbox-hosts.mdx — "the guide does not mention outbound network restrictions, firewall allowlists, or egress limitations for sandbox hosts"

### dns_level_blocking
dns_level_blocking: Unknown — no DNS-layer control documented; consistent with the absence of any egress control mechanism found elsewhere.
Sources:
- (same search coverage as egress_allowlist — no DNS-specific page or field exists in the 32-file SDK docs listing or `src/types.ts`)

### tls_mitm_inspection
tls_mitm_inspection: Unknown — no TLS interception / L7 rule engine documented.
Sources:
- (no dedicated networking/security architecture page exists in the SDK docs — see Axis C intro and Unknowns)

### http_path_rules
http_path_rules: Unknown — no outbound path/method rule engine documented. The only "path"-flavored access control found is inbound preview-URL authentication (host tokens as URL param/header/cookie), unrelated to outbound request filtering.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/sandbox-hosts.mdx — "when proxying requests to CodeSandbox hosts, you must set the `trust_csb_preview` cookie" (inbound proxy auth, not outbound path rules)

### proto_coverage
proto_coverage: Unknown — no protocol-scoped egress control (DNS/ICMP/TCP/UDP/QUIC/ssh/ws/grpc) documented; the VM appears to have a standard, undifferentiated outbound network stack.
Sources:
- (no networking/protocol control page found among the 32 SDK docs files)

### live_rule_reload
live_rule_reload: NA — no egress rule engine exists to reload (see egress_allowlist).

### firewall_escape_hatch
firewall_escape_hatch: NA — no firewall exists to bypass (see egress_allowlist).

### enforcement_plane
enforcement_plane: Unknown — with no documented rule engine, there is nothing identifiable as an enforcement plane for egress; whatever the VM's raw network path is (presumably direct from the Firecracker guest through Together/CodeSandbox's cloud network), it is not described as policy-enforcing or audited in any docs page reviewed.
Sources:
- (no networking architecture page found; inference only, no direct source)

### fail_closed
fail_closed: NA — no egress policy exists whose fail-closed behavior could be assessed.

### network_audit
network_audit: Unknown — OpenTelemetry tracing instruments SDK/control-plane operations (sandbox create/hibernate/shutdown/connect, fs reads/writes, command execution, port events, all REST calls) when a tracer is explicitly configured, but this is control-plane telemetry about SDK calls, not a per-request log of the VM's actual outbound network traffic. No egress/network audit log was found.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/tracing.mdx — "automatically instruments all major operations ... Sandbox operations (create, hibernate, shutdown, connect) ... File system actions ... Command execution ... Port management ... All REST API interactions"; "When tracing is not configured, the SDK operates normally without any performance impact"

### workspace_modes
workspace_modes: No — only a remote, git-backed persistent workspace exists; there is no host bind-mount mode. Local↔sandbox file sync is one-directional per call: explicit `fs.writeFile`/batch-write/upload, or git push/pull — not a live two-way mount.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/filesystem.mdx — "Files transfer between local and sandbox environments using read/write methods. Directories download as zip files via temporary URLs valid for 5 minutes."

### observability
observability: Yes — optional OpenTelemetry tracing (opt-in, zero overhead when unconfigured) covering sandbox lifecycle, fs, commands, ports, and API calls; plus a CLI (`csb`) interactive dashboard with CPU/memory/storage resource monitoring and real-time debugging.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/tracing.mdx — "automatically instruments all major operations"
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/cli.mdx — "Resource monitoring (CPU, memory, storage) ... Real-time debugging capabilities"

### supervision
supervision: No — the SDK exposes lifecycle control (hibernate/shutdown/restart) that the *integrator's own backend* can call, but there is no built-in supervisory layer that observes agent behavior and autonomously intervenes/contains it. Lifecycle management is manual/application-driven ("active lifecycle management as best practice" is explicitly framed as work the developer does), not an automated security supervisor.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/manage-sandboxes.mdx — "active lifecycle management as best practice ... Explicitly controlling resume/hibernate operations"

### fleet_mgmt
fleet_mgmt: Partial — sandboxes can be listed, tagged (up to 10 tags), titled, and organized into a `path`/folder, with CLI `csb sandboxes list/fork/hibernate/shutdown`; but there's no documented structured naming hierarchy/registry beyond these metadata fields and sandbox IDs.
Sources:
- https://raw.githubusercontent.com/codesandbox/codesandbox-sdk/main/src/types.ts — "tags: Categorization mechanism (max 10 tags)"
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/cli.mdx — "`csb sandboxes list/fork/hibernate/shutdown` - Manage sandbox lifecycle"

### snapshots_persistence
snapshots_persistence: Yes — memory+disk snapshot/hibernate/resume is a core, well-documented feature with fast timings, plus git-backed workspace persistence across recreation.
Regular resume from snapshot: 1-3s; archived/cold snapshot restore: 10-60s; fork from a hibernated sandbox: 1-3s (from a live/running sandbox, "live fork" capped at 5 concurrent forks); workspace persists via automatic git tracking at `/project/workspace`.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/resume.mdx — "Resume times vary from 1-3 seconds (regular) to 10-60 seconds (archived Sandbox)."
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/create.mdx — "hibernated = fast 1-3s fork; running = \"live fork\" limited to 5; archived = slow 20-60s"

## D. Setup (spectrum)
setup: Easy for a hello-world, Moderate for a production template — `npm install @codesandbox/sdk`, create an API token at codesandbox.io/t/api, set `CSB_API_KEY`, then `sdk.sandboxes.create()` → `sandbox.connect()` → `commands.run(...)`. No local Docker/VM/account-beyond-API-key needed for basic use since it's a pure API client. Production use requires an extra layer: authoring `.codesandbox/tasks.json` (+ optional `.codesandbox/Dockerfile`/`docker-compose.yml`) and building a template via the CLI (`npx @codesandbox/sdk build ...`) to get fast, predictable sandbox creation — meaningfully more setup than the quickstart implies.
Sources:
- https://raw.githubusercontent.com/codesandbox/codesandbox-sdk/main/CLAUDE.md — quickstart: `npm install @codesandbox/sdk` → `sdk.sandboxes.create()` → `sandbox.connect()` → `client.commands.run(...)`
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/templates.mdx — "Execute: `npx @codesandbox/sdk build ./my-template --ports 5173`"

## E. Daily use (spectrum)
daily_use: Moderate — this is an infrastructure SDK meant to be wired into the integrator's own backend/app (sessions are minted server-side and passed to a browser/Node client), not a single local CLI dev loop. Docs explicitly recommend active lifecycle management (explicit hibernate/resume calls tied to your own session/DB state) over relying on auto-hibernation, which is extra application code rather than zero-config daily use. Resume is fast (1-3s) once a sandbox exists.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/manage-sandboxes.mdx — "active lifecycle management as best practice provides better user experience and cost optimization"
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/clients.mdx — server mints a session via `sandbox.createSession()`; client connects via `connectToSandbox()`

## F. Configuration
### config_depth
config_depth: Deep — declarative, versionable `.codesandbox/` directory in the repo: `tasks.json` (setupTasks for install/build, tasks for long-running dev servers), optional `.codesandbox/Dockerfile` for environment-level setup (Node version, system packages, shell), `docker-compose.yml` for sibling services, VM tier selection, `hibernationTimeoutSeconds`, `automaticWakeupConfig` (http/websocket), per-user git config and env vars, privacy mode, tags. No native network-policy config exists (see Axis C).
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/templates.mdx — "Dockerfile scope: Environment-level setup ... setupTasks scope: Project-specific setup"
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/users.mdx — "Git Configuration ... Environment Variables ... Host Tokens"

### policy_model
policy_model: Rigid — workspace mode is fixed (remote VM only, no bind-mount alternative to choose per-run), and there is no network-policy dial at all (no allowlist to tighten/loosen, so also no "escape hatch" because there's no gate in the first place). The user-tunable levers that do exist are resource/lifecycle knobs (VM tier, hibernation timeout, auto-wakeup) and inbound-access knobs (privacy, host tokens) — not a broader security policy model with sane-default-plus-override semantics across workspace and network dimensions.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/create.mdx — creation options limited to template/fork source, metadata, privacy, VM tier, hibernation/wakeup settings

## G. DX — host↔sandbox integration
### bind_mount_sharing
bind_mount_sharing: No — no live bind mount; changes are copied one-way via `fs` API calls (write/batch-write/upload/download-as-zip) or git push/pull, not shared live with the host.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/filesystem.mdx — "Files transfer between local and sandbox environments using read/write methods."

### cred_forwarding
cred_forwarding: No — (corrected 2026-07-18, attribution audit) git access tokens and env vars configured per user/session through the SDK (`users.mdx`) are static credential injection copied into the VM, not a mediated forwarding mechanism (no ssh-agent/gpg-agent socket forwarding, no proxy header-injection with sentinel values). Per the cred_forwarding rule, "env vars you pass yourself" do not count as forwarding — that's just no isolation. Docs' own warning ("do not write git/env configuration to public Sandboxes ... if multiple users has access") confirms these are persisted, reachable secrets inside the VM once set, not brokered per-request.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/users.mdx — "Git Configuration - Set email, name, and optional credentials (access tokens, provider details)"

### browser_auth
browser_auth: Unknown — no documentation found describing a host-browser-triggered OAuth/device-code proxy (the mechanism CLIs like `gh auth login` or `claude` login rely on). The only browser-related mechanism documented is preview-URL host tokens for accessing a sandbox's own exposed HTTP ports from a browser, which is a different concept (inbound access, not host-auth-flow bridging). Searched `core-concepts.mdx`, `faq.mdx`, `clients.mdx`, `users.mdx`, `ports.mdx`, `hosts.mdx`.
Sources:
- (absence across the pages above; no positive or negative statement found)

### shared_dirs
shared_dirs: Unknown — `filesystem.mdx` references "Additional storage options exist through mount configurations detailed in Sessions documentation," but no "sessions"/"mounts" page appears among the 32 files enumerated in the SDK docs directory, so this could not be verified.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/filesystem.mdx — "Additional storage options exist through mount configurations detailed in Sessions documentation."
- https://api.github.com/repos/codesandbox/docs/contents/packages/projects-docs/pages/sdk — full 32-file listing does not include a "sessions.mdx" or "mounts.mdx"

### git_worktrees
git_worktrees: Unknown — docs mention replacing the default git remote and using git submodules for external repos, but no explicit worktree feature/command is documented in either the SDK API docs or the `csb` CLI reference.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/create.mdx — "you can: Replace the default remote with your own repository; Use git submodules to manage external repositories"
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/cli.mdx — command list has no worktree-related command

### nested_containers
nested_containers: Yes — since each sandbox is a full Firecracker VM with its own kernel, Docker Compose can run natively inside it; this is documented as the supported way to add sibling services (e.g. databases) alongside the main workload.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/templates.mdx — "Docker Compose Support: Add `docker-compose.yml` for additional services (databases, etc.), executing composition in setupTasks."

### harness_agnostic
harness_agnostic: Yes — the SDK is a general-purpose VM/command-execution primitive (arbitrary Dockerfile, arbitrary shell commands/tasks), explicitly marketed for multiple use cases (browser IDEs, code interpreters, "AI Agent runtime environments," A/B testing) rather than tied to one coding-agent vendor.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/index.mdx — "Browser-based IDEs ... Code interpretation ... AI Agent runtime environments ... A/B testing"

## H. Performance (spectrum)
performance: Lightweight (per vendor-published figures; not independently benchmarked) — Firecracker is purpose-built for fast boot/low overhead. Vendor figures: clean VM boot ~2s; snapshot resume 1-3s (regular) to 10-60s (archived/cold); fork from snapshot or live VM within ~2-3s; Together's own marketing separately claims ~500ms snapshot starts and <2.7s p95 from-scratch creation. No independent/third-party benchmark was found; no bind-mount IO throughput figures exist because there is no bind mount (workspace is remote-VM-resident). All numbers below are vendor-published, not third-party verified.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/resume.mdx — "Resume times vary from 1-3 seconds (regular) to 10-60 seconds (archived Sandbox)."
- WebSearch summary (2026-07-18, Together AI's own materials) — "starting VMs from a snapshot in about 500 ms and creating them from scratch in under 2.7 seconds (P95)"

## I. Feasibility (spectrum)
feasibility: Adoptable today for prototyping; moderate risk for production commitment — Pure API/SDK client (npm install + API key), no local Docker/VM/OS prerequisite, works from any dev OS since it's just HTTP/WebSocket. Free "Build" tier allows 10 concurrent VMs to start immediately. Risk factors for production: no self-host/exit path (fully vendor-dependent), usage-based billing scales with concurrency and VM tier, and the product is mid-rebrand (CodeSandbox → "Together Code Sandbox") with documentation currently split/thin across two domains, suggesting near-term URL/branding churn.
Sources:
- https://docs.together.ai/docs/together-code-sandbox (WebFetch summary) — "Build (free): 10 concurrent VMs"
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/faq.mdx — "No, we do not offer self hosting at this time."

## J. Price (prose-only)
Two-part billing: VM credits (pay-as-you-go, $0.01486/credit, billed to the minute) × VM tier credit rate, plus a per-plan concurrent-VM cap. Tiers: Pico (2 vCPU/1GB, 5 cr/hr ≈ $0.074/hr) through XLarge (64 vCPU/128GB, 320 cr/hr ≈ $4.76/hr); Nano (2 vCPU/4GB, 10 cr/hr ≈ $0.149/hr) is the documented recommended default. Plans: Build (free, 10 concurrent VMs), Scale ($170/mo base + 1,100 free credits/mo, 250 concurrent VMs), Enterprise (custom, volume discounts, contact Together sales). No self-host option, so there is no way to avoid usage-based billing at any scale.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/pricing.mdx — "one credit equates to a specific amount of resources used per hour ... Credits cost $0.01486 each"; "Scale ... Includes 1,100 free credits monthly"

## K. Extensibility
extensibility: Partial — extensibility runs through standard container/devcontainer tooling rather than a bespoke plugin API: custom `.codesandbox/Dockerfile`, `docker-compose.yml` for extra services, custom `tasks.json` task definitions, a reusable template library (build once via CLI, reference by Template ID for fast creation), and an OpenTelemetry tracer injection point for observability integration. No plugin/bundle system, marketplace, or custom-harness-definition mechanism was found.
Sources:
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/templates.mdx — "Templates function as custom, pre-configured environments ... Building Templates: Execute: `npx @codesandbox/sdk build ./my-template --ports 5173`"
- https://raw.githubusercontent.com/codesandbox/docs/main/packages/projects-docs/pages/sdk/tracing.mdx — tracer injection at SDK init: `new CodeSandbox({ tracer })`

## Unknowns & caveats
- **Maintainer verification (2026-07-18):** the codesandbox.io fetch failures were NOT firewall blocks — direct probe shows HTTP 403 from a Cloudflare IP with the egress firewall bypassed: bot protection rejecting non-browser clients. Docs remain unverifiable by tooling; a human browser can read them.
- **Maintainer verification (2026-07-18):** official OpenAPI specs in the SDK repo (`openapi.json`, `openapi-sandbox-container.json`, `openapi-sandbox-system.json` @ main) grepped for firewall/egress/allowlist/network-policy/domain/proxy/dns fields — ZERO hits. The sandbox API surface exposes no egress-control parameters. Strengthens the egress-control finding from pure docs-silence toward API-level absence; verdict stays Unknown only because a dashboard/platform-level control not surfaced in this API can't be ruled out.
- **Blocked (403, not firewall-related):** `https://codesandbox.io/sdk`, `https://codesandbox.io/docs/sdk` (and subpaths e.g. `/guides/security`, `/guides/networking`, `/reference/api/sandboxes/create`), `https://codesandbox.io/docs`, `https://codesandbox.io/t/api`, `https://codesandbox.io/blog/joining-together-ai-introducing-codesandbox-sdk` all returned HTTP 403 to WebFetch (site-level bot/Cloudflare protection — the research session's own egress firewall was bypassed per the operational note, so this is the origin server, not our containment). The `codesandbox.io/` homepage itself DID load. Worked around via the docs' GitHub source (`raw.githubusercontent.com/codesandbox/docs/.../pages/sdk/*.mdx`, 32 files enumerated and the security/networking-relevant ones read) and via `docs.together.ai/docs/together-code-sandbox`, which is a thinner, newer mirror. Content is believed substantively equivalent to the live rendered site but could theoretically lag it.
- `web.archive.org` fetches errored at the tool level ("Claude Code is unable to fetch from web.archive.org") — not a network block, could not be used to corroborate the live site.
- WebSearch budget was exhausted mid-session (fleet-wide 200/200 used) after two searches; no further targeted searches (e.g. third-party Firecracker escape-resistance research, independent performance benchmarks, `gh auth`-style browser-auth confirmation) could be run. This affects escape_resistance (no third-party corroboration) and browser_auth/shared_dirs (could not search around the docs-silence).
- **No dedicated networking/firewall/security-architecture doc page exists** anywhere in the 32-file SDK docs set (confirmed via full directory listing) — this is genuine, confirmed docs-silence (not a fetch failure), which is why all network-control sub-criteria in Axis C are marked Unknown rather than No: absence across an exhaustive, targeted search is strong evidence but not an explicit vendor statement of non-support.
- `filesystem.mdx` references a "Sessions documentation" page for additional mount configuration that does not appear among the enumerated SDK docs files — shared_dirs capability could not be confirmed or denied.
- Two live doc surfaces exist for the same product (`codesandbox.io/docs/sdk`, richer/original) and (`docs.together.ai/docs/together-code-sandbox`, single-page, newer/rebranded) — some numbers differ slightly between them (e.g. workspace path `/project/workspace` vs. `/project/sandbox` mentioned once each; treated `/project/workspace` as authoritative since it recurs across more pages).

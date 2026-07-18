# OpenAI API Sandboxes (Agents SDK "Sandbox Agents")
category: orchestration (pluggable sandbox abstraction over multiple backends) + a distinct OpenAI-hosted primitive (Responses API "container" tool)
one-liner: A harness/compute-split abstraction in the OpenAI Agents SDK that lets an agent drive a Unix-like workspace on one of nine pluggable backends (local, Docker, or seven third-party hosted providers); separately, the Responses API offers its own OpenAI-managed ephemeral container ("Shell"/Code Interpreter) with a documented default-deny network policy. | built on: SDK abstraction (no single isolation tech) + OpenAI-managed Debian containers for the Responses API tool | license: MIT (SDK) | maturity: SDK mature (28k★), sandbox feature itself in beta since ~April 2026

As of 2026-07-18. Two related-but-distinct surfaces are both reachable from "OpenAI API" and are called out separately throughout: (1) **Sandbox Agents** — the Agents SDK's pluggable sandbox abstraction, the subject of the seed URL; (2) the **Responses API hosted container tool** ("Shell"/Code Interpreter) — OpenAI's own first-party hosted execution backend, which is where almost all of the documented network-control detail actually lives. Codex CLI's local sandbox (Seatbelt/bubblewrap) is a separate product and is explicitly excluded per the task scope.

## A. Identity

### built_on (prose-only)
Sandbox Agents is not one isolation technology — it is a client abstraction. Documented clients: `UnixLocalSandboxClient` (runs directly against the local filesystem, no extra install), `DockerSandboxClient` (local Docker container from a specified image, requires `openai-agents[docker]`), and seven hosted-provider clients — Blaxel, Cloudflare, Daytona, E2B, Modal, Runloop, Vercel — each requiring its own install extra (e.g. `openai-agents[e2b]`) and each provider's own isolation tech, which OpenAI's own docs do not describe (that detail lives on each provider's site, out of scope here). Architecture: a strict **harness (control plane)** vs **compute (sandbox)** split — harness owns "the agent loop, model calls, tool routing, handoffs, approvals, tracing, and recovery... in trusted infrastructure," while compute is "provider-specific execution where agents read/write files, run commands, install dependencies, use mounted storage, expose ports, and snapshot state." Separately, the Responses API's hosted "container" tool runs in OpenAI-managed ephemeral containers ("Debian 12 with preinstalled Python 3.11, Node.js 22.16, Java 17") reachable via `"environment": {"type": "container_auto"}`.
Sources:
- https://developers.openai.com/api/docs/guides/agents/sandboxes — "Harness (Control Plane): Manages the agent loop, model calls, tool routing, handoffs, approvals, tracing, and recovery. Runs in trusted infrastructure and owns run state."
- https://openai.github.io/openai-agents-python/sandbox/clients/ — lists UnixLocalSandboxClient, DockerSandboxClient, and BlaxelSandboxClient/CloudflareSandboxClient/DaytonaSandboxClient/E2BSandboxClient/ModalSandboxClient/RunloopSandboxClient/VercelSandboxClient with per-client install extras
- https://developers.openai.com/api/docs/guides/tools-shell — "environment: { type: 'container_auto' }" / hosted runtime described as "Debian 12 with preinstalled Python 3.11, Node.js 22.16, Java 17"

### execution_locality
execution_locality: Both — Unix-local and Docker clients execute on the developer's own machine; the seven hosted-provider clients and the Responses API's OpenAI-managed container execute remotely.
For local clients, code/files never leave the dev machine except whatever the agent chooses to send over the network. For every hosted path (seven third-party providers, or OpenAI's own Responses API container), code and workspace files execute off-machine, in a provider- or OpenAI-managed environment. Credential handling for the remote paths is partially mediated: the Responses API container's `domain_secrets` lets "the model and runtime see placeholder names (for example, `$API_KEY`) instead of raw credentials," and "raw secret values don't persist on API servers." Docs do not describe an equivalent mediation mechanism for the Agents SDK sandbox's hosted-provider clients — general guidance there is only to "use provider-native secret systems for hosted providers."
Sources:
- https://developers.openai.com/api/docs/guides/agents/sandboxes — "Use provider-native secret systems for hosted providers... Treat sandbox credentials as runtime configuration, not prompt content"
- https://developers.openai.com/api/docs/guides/tools-shell — "The model and runtime see placeholder names (for example, `$API_KEY`) instead of raw credentials." / "Raw secret values don't persist on API servers and don't appear in model-visible context."

### open_source (prose-only)
`openai-agents-python` (and the TypeScript `@openai/agents`) is MIT-licensed and hosted at the official `openai` GitHub org. Unix-local/Docker clients need no external service and are effectively self-hostable by construction (they run wherever the SDK process runs). Hosted-provider clients depend on that third party's own (non-OpenAI, non-open-source) infrastructure. The Responses API's own hosted container tool is closed and OpenAI-hosted-only — not self-hostable.
Sources:
- https://github.com/openai/openai-agents-python — MIT license, official OpenAI-org repository

### maturity (prose-only)
Sandbox Agents itself is explicitly beta: "Both in beta; API details, defaults, and capabilities may change." Third-party coverage (Help Net Security) dates the Agents SDK sandbox feature to an April 2026 update. The underlying SDK is mature and widely adopted: MIT license, ~28,000 GitHub stars, ~4,300 forks, 109 releases (latest v0.18.3 as of July 2026).
Sources:
- https://developers.openai.com/api/docs/guides/agents/sandboxes — "Both in beta; API details, defaults, and capabilities may change."
- https://github.com/openai/openai-agents-python — star/fork/release counts as observed 2026-07-18

## B. Threat protection

### host_fs_damage
host_fs_damage: Partial — containment is a property of which client you choose, not of the abstraction itself; Unix-local gives no filesystem boundary.
Manifest entry paths are workspace-relative and "cannot be absolute paths or escape the workspace with `..`," and `extra_path_grants` is the explicit, opt-in mechanism for reaching outside the workspace root — docs explicitly warn to "treat manifests that contain `extra_path_grants` as trusted configuration" and never load grants from untrusted/model output. But `UnixLocalSandboxClient` is described only as giving "the smallest local loop" with no isolation boundary language at all, in contrast to Docker/hosted being recommended explicitly "when you need stronger environment isolation." So the default/easiest client provides no host-fs containment; containment requires deliberately moving to Docker or a hosted provider.
Sources:
- https://openai.github.io/openai-agents-python/sandbox/guide/ — "Manifest entry paths are workspace-relative. They cannot be absolute paths or escape the workspace with `..`, which keeps the workspace contract portable across local, Docker, and hosted clients."
- https://openai.github.io/openai-agents-python/sandbox/clients/ — "Unix-local is the easiest way to start developing against a local filesystem. Move to Docker or a hosted provider when you need stronger environment isolation or production-style parity."

### credential_theft
credential_theft: Partial — mediation exists for the Responses API container's HTTP auth headers (`domain_secrets`); no equivalent documented for the Agents SDK sandbox abstraction itself.
For the Responses API hosted container, `domain_secrets` substitute placeholder tokens for raw values so "the model and runtime see placeholder names... instead of raw credentials," and raw values "don't persist on API servers." For the Agents SDK Sandbox Agents, guidance is generic best-practice prose only: "Treat sandbox credentials as runtime configuration, not prompt content," "Use `Manifest.environment` for startup values, marking sensitive entries as ephemeral," "Never save secrets or private documents in artifacts." No ssh-agent-style forwarding or secret-injection primitive is documented for the sandbox abstraction — it's operator discipline, not a mediating mechanism.
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — "Use `domain_secrets` when a domain in your `allowed_domains` list requires private authorization headers."
- https://developers.openai.com/api/docs/guides/agents/sandboxes — "Treat sandbox credentials as runtime configuration, not prompt content."

### data_exfiltration
data_exfiltration: Partial — documented allowlisting exists only for the separate Responses API container tool; the Agents SDK sandbox abstraction has no documented egress control of its own (see axis C network block for full detail).
Sources: see axis C `network_default_posture` / `egress_allowlist` below.

### malicious_execution
malicious_execution: Partial — blast radius depends entirely on the chosen client/provider; the abstraction supplies no independent containment of its own.
Docker/hosted clients contain a compromised or hallucinated command inside the container/provider boundary; Unix-local does not (it "runs directly on host filesystem" — no isolation boundary is described). The Responses API container tool is explicitly ephemeral and network-isolated by default, which independently limits blast radius for that specific backend. Docs' own security framing treats the sandbox as the containment mechanism for exactly this scenario: "There are no API keys or secrets in the sandbox — you want it to be totally isolated" (per search-summarized doc language, echoing the "no secrets in sandbox" design intent stated across the sandboxes guide).
Sources:
- https://openai.github.io/openai-agents-python/sandbox/clients/ — client isolation comparison (Unix-local = minimal, Docker/hosted = stronger)
- https://developers.openai.com/api/docs/guides/tools-shell — "running arbitrary shell commands can be dangerous," recommends sandboxing/allowlists/audit logging

### escape_resistance
escape_resistance: Unknown for the general abstraction; Partial for the two OpenAI-native paths — determination varies by client and OpenAI's own docs don't state the isolation boundary strength for any of them.
Unix-local = no isolation boundary (shares the host process/filesystem). Docker = shared-kernel container, a stronger but still shared-kernel boundary; OpenAI's docs don't state whether any particular seccomp/capabilities hardening is applied beyond what a stock Docker image gives. Hosted-provider isolation tech (e.g. Firecracker microVMs for E2B) is not described in OpenAI's own docs at all — that claim would need each provider's own docs to substantiate, out of scope for this OpenAI-focused assessment. The Responses API's own hosted container isolation mechanism (VM? gVisor? plain container?) is likewise never named in the docs fetched.
Sources:
- https://openai.github.io/openai-agents-python/sandbox/clients/ — client comparison, no isolation-tech names given
- https://developers.openai.com/api/docs/guides/tools-shell — hosted container described only by OS image/runtime, not isolation mechanism

### resource_abuse
resource_abuse: Partial — SDK-side manifest-materialization limits exist; no documented CPU/memory/disk caps for the sandbox abstraction itself, but the separate Responses API container tool does document a memory limit and session timeout.
Agents SDK: `archive_limits` and `concurrency_limits` bound only SDK-side workspace-staging work ("archive extraction," "how much sandbox materialization work can run in parallel") — not runtime CPU/memory/disk of the running sandbox. The Docker client's own options (`image`, `exposed_ports`) expose no CPU/memory parameters. Responses API container tool: `memory_limit: "1g"` example param and `expires_after: { anchor: "last_active_at", minutes: 20 }` session TTL are documented; default per-command timeout is 120 seconds.
Sources:
- https://openai.github.io/openai-agents-python/sandbox/guide/ — "`archive_limits` controls SDK-side resource checks for archive extraction... `concurrency_limits` controls how much sandbox materialization work can run in parallel."
- https://developers.openai.com/api/docs/guides/tools-shell — "memory_limit: '1g'", "expires_after: { anchor: 'last_active_at', minutes: 20 }", default timeout 120 seconds per command

## C. Feature set & granularity

### network_default_posture
network_default_posture: Partial — the two OpenAI-native surfaces disagree. The Responses API hosted container is explicitly deny-by-default; the Agents SDK sandbox abstraction states no default posture at all.
Responses API container tool: "Hosted containers don't have outbound network access by default" — genuinely deny-by-default. Agents SDK Sandbox Agents: the only network-related text on the seed page is an architecture-diagram caption — "gateway-mediated access to data, APIs, and the web" — with no statement of what's reachable out of the box; Docker's client options expose no network-mode toggle (default Docker bridge networking, which is open, would apply absent any documented override); Unix-local runs on the host, i.e. inherits full host network access with no sandbox-level restriction at all.
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — "Hosted containers don't have outbound network access by default."
- https://developers.openai.com/api/docs/guides/agents/sandboxes — diagram caption: "gateway-mediated access to data, APIs, and the web" (no further specification found on this page)
- https://openai.github.io/openai-agents-python/ref/sandbox/sandboxes/docker/ — `DockerSandboxClientOptions` fields are `image`, `exposed_ports`, `type` only — no network-mode parameter

### egress_allowlist
egress_allowlist: Partial — documented only for the Responses API container tool, and only at bare-domain granularity; the Agents SDK sandbox abstraction has none.
Responses API container: two-tier — an org-level admin allowlist configured in the dashboard, then a request-level `network_policy` that can further restrict to a subset: `network_policy: { type: 'allowlist', allowed_domains: ['pypi.org', 'files.pythonhosted.org', 'github.com'] }`. Granularity stops at bare domain strings — a follow-up query specifically asking about wildcard subdomains, IP/CIDR, port scoping, or path rules got: "The documentation does not address these features. The only network policy specification shown is... with no mention of wildcards, IP ranges, ports, or path-level restrictions." No egress allowlist mechanism at all is documented for the Agents SDK Sandbox Agents abstraction (Unix-local/Docker/hosted-provider clients).
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — "network_policy: { type: 'allowlist', allowed_domains: ['pypi.org', 'files.pythonhosted.org', 'github.com'] }"; "Your org allow list defines the full set of `allowed_domains`. Request-level `network_policy` further restricts access."

### dns_level_blocking
dns_level_blocking: Unknown — not stated for either surface. The Responses API container's `allowed_domains` produces some enforcement effect, but the docs never say whether it's implemented at DNS resolution, at a forward proxy, or via kernel/network-layer filtering.
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — no mechanism-of-enforcement text found despite targeted search

### tls_mitm_inspection
tls_mitm_inspection: Unknown — no mention of TLS interception, MITM, or L7 inspection in any fetched page for either surface.
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — targeted search for "TLS/HTTPS/proxy" found no such text

### http_path_rules
http_path_rules: No — the documented `network_policy` schema for the Responses API container is domain-only, with no path field; a direct check for path/method-level rules came back negative. NA for the Agents SDK sandbox abstraction, which has no network-policy feature to have path rules on.
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — schema shown is `{type: 'allowlist', allowed_domains: [...]}` only; explicit check returned "The documentation does not address these features [wildcards, IP ranges, ports, path-level restrictions]."

### proto_coverage
proto_coverage: Partial — only HTTP(S)-style domain access is covered by any documented policy; no protocol breadth (DNS/ICMP/TCP/UDP/QUIC/ssh/ws/grpc) is mentioned anywhere, for either surface, and no extensibility model for adding protocols is described.
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — `allowed_domains` implies HTTP(S) egress; no other protocol named

### live_rule_reload
live_rule_reload: Unknown — no documentation states whether `network_policy` (or any Agents SDK network setting, which doesn't exist) can be changed against a running/live sandbox session versus only at container-creation time. The request-level `network_policy` is shown as a creation-time parameter only.
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — `network_policy` appears only in container-creation examples

### firewall_escape_hatch
firewall_escape_hatch: Unknown — no timed-bypass, per-sandbox disable/enable, or break-glass mechanism is documented for either surface.
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — no bypass/disable text found on targeted search

### enforcement_plane
enforcement_plane: Unknown — neither surface's docs name the enforcement layer (no mention of eBPF, netfilter, userspace proxy, hypervisor, or cloud network infra for either the Agents SDK sandbox or the Responses API container's `network_policy`).
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — targeted search for enforcement-plane detail returned "Not specified in the documentation."

### fail_closed
fail_closed: Partial — the Responses API container's default (no network unless allowlisted) is a fail-closed *default posture* by design, but survivability under a control-plane outage is never addressed. Unknown/NA for the Agents SDK sandbox, which has no network policy to fail open or closed.
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — "Hosted containers don't have outbound network access by default." (default-deny by construction); no outage-behavior text found on targeted search

### network_audit
network_audit: No — no OpenAI-provided egress log is documented; the guidance explicitly places audit-logging responsibility on the customer.
"Capture requested hosts and actual outbound destinations for each session" and "periodically review logs" are instructions telling the developer to build their own logging around the tool, not a description of a built-in audit feature. Nothing found for the Agents SDK sandbox abstraction either.
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — "capture requested hosts and actual outbound destinations for each session," "periodically review logs" (framed as customer responsibility, not a product feature)

### workspace_modes
workspace_modes: Partial — Unix-local is explicitly copy-and-cleanup, not a live bind mount; semantics for `LocalDir` staging (copy vs bind vs symlink) are otherwise undocumented.
"The runner can create a temporary workspace from the agent's default manifest and clean it up after the run" describes Unix-local's default flow — an ephemeral staged copy, not a live two-way bind mount of an existing project directory. `extra_path_grants` gives access to "a concrete absolute path outside the workspace" as an opt-in escape hatch, but the docs never state whether edits there are live/reflected on the host or one-way. Storage mounts (S3/GCS/R2/Azure Blob/Box) are a separate, explicitly ephemeral concept: "Mounted storage treated as ephemeral—not persisted in snapshots."
Sources:
- https://developers.openai.com/api/docs/guides/agents/sandboxes — "the runner can create a temporary workspace from the agent's default manifest and clean it up after the run"
- https://openai.github.io/openai-agents-python/sandbox/guide/ — "Use `extra_path_grants` only when the agent needs a concrete absolute path outside the workspace..."

### observability
observability: Yes — built-in tracing dashboard (`platform.openai.com/traces`) covers the harness's model calls, tool calls, handoffs, and guardrails automatically; not sandbox-network- or file-level telemetry specifically.
"The overall run or workflow... each model call... tool calls and their outputs... handoffs and guardrails... any custom spans you wrap around the workflow" are captured by default. No third-party observability integrations (Datadog, etc.) are mentioned, and sandbox-internal activity (file diffs, network requests inside the container) is not called out as part of this trace surface.
Sources:
- https://developers.openai.com/api/docs/guides/agents/integrations-observability — trace content list quoted above; Traces dashboard at platform.openai.com/traces

### supervision
supervision: Partial — a resumable approval-interruption model lets the harness pause and gate specific risky tool calls (shell commands, `apply_patch`), but this is scoped intervention at pre-declared checkpoints, not a general runtime supervisor that can observe and contain an already-running sandbox session arbitrarily.
"If provided, [the approval handler] will be invoked immediately when an approval is needed" and a blocked run "records an approval interruption instead of executing the tool," resumable via `RunState.approve()`/`.reject()`. This is real intervention capability, but only at points the agent developer wires an approval requirement onto (specific tools), not a standing supervisor process watching all sandbox behavior. Session-level teardown (`delete()` on a Docker/hosted session) exists as a blunt stop mechanism but isn't documented as a security-driven containment command.
Sources:
- https://developers.openai.com/api/docs/guides/agents/guardrails-approvals — "the run records an approval interruption instead of executing the tool... Your application approves or rejects pending items... The same run resumes from `state`"
- https://openai.github.io/openai-agents-python/ref/sandbox/sandboxes/docker/ — `DockerSandboxClient.delete()` "Removes session and associated volumes"

### fleet_mgmt
fleet_mgmt: Unknown — no multi-agent naming/registry/lifecycle system is documented; the closest adjacent features (handoffs, agents-as-tools) route control between agents within a single run rather than manage a fleet of named, independently addressable sandboxes.
Sources:
- https://developers.openai.com/api/docs/guides/agents — "Handoffs for delegated ownership... Agents-as-tools for manager-style workflows" (orchestration patterns, not a registry/fleet system)

### snapshots_persistence
snapshots_persistence: Yes — explicit snapshot and resumable-session-state features, with a documented limit that they cover only the workspace root, not extra path grants or mounted storage.
Three resolution tiers exist: live sandbox session reuse, resumption from serialized session state, and seeding a fresh session from a snapshot. "Snapshots and `persist_workspace()` still include only the workspace root. Extra granted paths are runtime access, not durable workspace state," and mounted storage is explicitly excluded from snapshots too.
Sources:
- https://developers.openai.com/api/docs/guides/agents/sandboxes — RunState / Session State / Snapshots three-tier resolution order
- https://openai.github.io/openai-agents-python/sandbox/guide/ — "Snapshots and `persist_workspace()` still include only the workspace root. Extra granted paths are runtime access, not durable workspace state."

## D. Setup
### setup
setup: Easy for local iteration, more involved as you move up the isolation ladder — Unix-local needs "no extra install"; Docker needs an extras package plus a running Docker daemon; each hosted provider needs its own extras package and (undocumented by OpenAI) provider account/credentials.
Unix-local: "No extra install, simple local filesystem development." Docker: `pip install "openai-agents[docker]"` plus a local Docker installation. Hosted providers: seven separate extras (`openai-agents[e2b]`, `[modal]`, `[vercel]`, etc.) — OpenAI's own docs don't cover each provider's account-setup steps, deferring to each provider's docs. Prerequisite floor: Python 3.10+ (or Node for the TS SDK) and the OpenAI Agents SDK.
Sources:
- https://openai.github.io/openai-agents-python/sandbox/clients/ — "No extra install, simple local filesystem development" (Unix-local); Docker requires `openai-agents[docker]`; hosted clients each need `openai-agents[<provider>]`
- https://openai.github.io/openai-agents-python/sandbox_agents/ — "Python 3.10 or higher" prerequisite

## E. Daily use
### daily_use
daily_use: Unknown/not-applicable-as-asked — this is a library primitive embedded in a developer's own application, not an interactive CLI a human operates session-to-session, so day-to-day operator friction depends entirely on what the integrating app builds. The one documented continuity pattern is session reuse for multi-turn work: "Multi-turn chats should use stable SDK sessions with the same live sandbox session to group runs by conversation ID." No rebuild/restart/attach workflow is described because there is no first-party interactive shell/CLI surface to describe one for.
Sources:
- https://developers.openai.com/api/docs/guides/agents/sandboxes — "Multi-turn chats should use stable SDK sessions with the same live sandbox session to group runs by conversation ID."

## F. Configuration
### config_depth
config_depth: Deep in scope, but code-constructed rather than a versionable static config file — Manifest covers files/dirs, five storage-mount backends, env vars, OS users/groups, per-entry file permissions, path grants, and archive/concurrency limits, plus a pluggable Capabilities list (filesystem/shell/skills/memory/compaction, or custom).
There is no equivalent of a checked-in project config file (e.g. a committed YAML) — Manifest and SandboxRunConfig are built via SDK objects in application code, at either agent-definition scope (default manifest, capabilities) or per-run scope (client choice, manifest override, session injection).
Sources:
- https://developers.openai.com/api/docs/guides/agents/sandboxes — Manifest fields (Files & Directories, Storage Mounts, Environment Variables, Users/Groups); per-agent vs per-run configuration split
- https://openai.github.io/openai-agents-python/sandbox/guide/ — Permissions/FileMode, `extra_path_grants`, `archive_limits`, `concurrency_limits`

### policy_model
policy_model: Moderately policy-driven for compute-backend choice, but not for network policy on the sandbox abstraction itself — the sandbox client (Unix-local/Docker/hosted provider) is swappable per run with no agent-code changes, which is a genuine escape-hatch/override strength; no equivalent per-run toggle for network restriction exists on the Agents SDK sandbox, because no network policy exists on it at all. Only the separate Responses API container tool exposes a request-level policy override (`network_policy` narrowing the org-level allowlist).
Sources:
- https://developers.openai.com/api/docs/guides/agents/sandboxes — code sample showing `sandbox: { client: new UnixLocalSandboxClient() }` swapped to `new DockerSandboxClient({ image: "node:22" })` for "Same agent, different providers"
- https://developers.openai.com/api/docs/guides/tools-shell — request-level `network_policy` overrides/narrows the org-level allowlist

## G. DX — host↔sandbox integration

### bind_mount_sharing
bind_mount_sharing: Unknown — `LocalDir` staging semantics (copy vs bind vs symlink) are never stated; the one concrete data point (Unix-local "creates a temporary workspace... and cleans it up after the run") points toward copy-based staging rather than a live two-way bind mount, but this isn't confirmed for `LocalDir` specifically.
Sources:
- https://developers.openai.com/api/docs/guides/agents/sandboxes — "the runner can create a temporary workspace from the agent's default manifest and clean it up after the run"

### cred_forwarding
cred_forwarding: Partial — HTTP auth-header mediation (`domain_secrets`) is documented for the Responses API container tool only; no ssh-agent/GPG-style forwarding is documented for either surface.
Sources:
- https://developers.openai.com/api/docs/guides/tools-shell — `domain_secrets` placeholder-token mechanism (quoted above under credential_theft)

### browser_auth
browser_auth: Unknown — no mention of proxying a host-browser OAuth/device-code flow into the sandbox in any fetched page.
Sources:
- searched https://developers.openai.com/api/docs/guides/agents/sandboxes and https://openai.github.io/openai-agents-python/sandbox/guide/ — no browser/OAuth/device-code text found

### shared_dirs
shared_dirs: Yes — five storage-mount backends beyond the base workspace (S3, GCS, R2, Azure Blob, Box) with a `read_only` toggle, plus `extra_path_grants` for additional host paths on local/Docker clients.
Sources:
- https://developers.openai.com/api/docs/guides/agents/sandboxes — "Storage Mounts: S3, GCS, R2, Azure Blob, Box mount options"
- https://openai.github.io/openai-agents-python/sandbox/clients/ — mount strategies list, `read_only` parameter

### git_worktrees
git_worktrees: Unknown — no mention of git or worktree-specific handling in any fetched page; `LocalDir` staging of a repo appears only as a generic "repo" manifest entry example, with no worktree-aware behavior documented.
Sources:
- https://openai.github.io/openai-agents-python/sandbox_agents/ — `Manifest(entries={"repo": LocalDir(src=HOST_REPO_DIR)})` example (plain directory staging, no worktree semantics mentioned)

### nested_containers
nested_containers: Unknown — no statement on whether a container runtime (Docker socket, DinD) is available inside any of the sandbox clients or the Responses API hosted container.
Sources:
- searched https://openai.github.io/openai-agents-python/ref/sandbox/sandboxes/docker/ and https://developers.openai.com/api/docs/guides/tools-shell — no docker-in-docker / nested-runtime text found

### harness_agnostic
harness_agnostic: No — Sandbox Agents is a capability of the OpenAI Agents SDK's own harness (which owns the agent loop, tool routing, handoffs, approvals, and tracing); it is invoked through that SDK, not usable as a generic sandbox by arbitrary third-party coding-agent CLIs.
Sources:
- https://developers.openai.com/api/docs/guides/agents/sandboxes — "Harness (Control Plane): Manages the agent loop, model calls, tool routing, handoffs, approvals, tracing, and recovery."

## H. Performance
### performance
performance: Unknown from official OpenAI docs — no cold/warm start latency, disk footprint, RAM overhead, or IO throughput numbers were found on any fetched OpenAI page. Third-party vendor blogs (Blaxel, Modal, etc.) publish comparative cold-start numbers for the various hosted providers (e.g. one such post claims Cloudflare sub-50ms, E2B ~150ms), but these are third-party marketing benchmarks about the *providers*, not OpenAI-published figures about the Agents SDK integration itself, and are not used as a determination here.
Sources:
- searched https://developers.openai.com/api/docs/guides/agents/sandboxes, https://openai.github.io/openai-agents-python/sandbox/guide/, https://openai.github.io/openai-agents-python/sandbox/clients/ — no performance numbers found in official docs

## I. Feasibility
### feasibility
feasibility: Adoptable today for local dev, with an explicit beta caveat — Unix-local documented as macOS/Linux only ("fastest iteration on macOS/Linux"); Docker and hosted-provider clients extend reach further (Docker itself is cross-platform) but neither is explicitly confirmed for Windows in OpenAI's own docs. The feature carries the SDK's own beta disclaimer ("API details, defaults, and supported capabilities may change"), which is a real adoption-risk caveat for anyone building production tooling on it today.
Sources:
- https://openai.github.io/openai-agents-python/sandbox/clients/ — "Start with Unix-local for local development on macOS or Linux" framing
- https://developers.openai.com/api/docs/guides/agents/sandboxes — "Both in beta; API details, defaults, and capabilities may change."

## J. Price (prose-only)
The Agents SDK itself (including the sandbox abstraction) carries no separate OpenAI fee — it's an MIT-licensed library; you pay standard OpenAI API model-call pricing plus, for hosted-provider clients, whatever that third-party provider bills separately (not set or documented by OpenAI). The distinct Responses API hosted container/Shell tool IS billed directly by OpenAI: "1 GB $0.03, 4 GB $0.12, 16 GB $0.48, 64 GB $1.92 per 20-minute session per container," billed by the minute with a 5-minute minimum. The pricing page has no line item for "Sandbox Agents" specifically.
Sources:
- https://developers.openai.com/api/docs/pricing — hosted container pricing tiers quoted above; no separate Sandbox Agents line item found

## K. Extensibility
### extensibility
extensibility: Yes — custom Docker images via `DockerSandboxClientOptions.image`, a pluggable `Capabilities` system (default: filesystem/shell/compaction; can add custom sandbox-specific tools/instructions, plus Memory and Skills capabilities), custom Manifest entries and mount strategies, per-run client/provider swapping, and MCP servers as a documented per-agent configuration option.
Sources:
- https://openai.github.io/openai-agents-python/ref/sandbox/sandboxes/docker/ — `DockerSandboxClientOptions(image=...)`
- https://developers.openai.com/api/docs/guides/agents/sandboxes — Capabilities table (Shell, Filesystem, Skills, Memory, Compaction); "Custom capabilities can add sandbox-specific tools or instructions"; per-agent config includes "Handoffs and MCP servers"

## Unknowns & caveats

- **Two distinct products, one seed URL.** The seed page (Sandbox Agents / Agents SDK) is thin on network-control detail — the only network-related text found on it is an architecture-diagram caption ("gateway-mediated access to data, APIs, and the web") with no specification of defaults, rules, or enforcement. Nearly all concrete, quotable network-policy detail in this writeup (`network_policy`, `allowed_domains`, `domain_secrets`, default-deny) comes from a *different, though related and still first-party*, OpenAI API surface: the Responses API's hosted "Shell"/Code Interpreter container tool. Where a determination is genuinely about the Agents SDK sandbox abstraction and docs are silent, it is marked Unknown or Partial with the split stated explicitly, per guidelines' "docs silence is never No" rule.
- **No blocked URLs.** No NXDOMAIN / connection-refused / firewall-403 was encountered — the firewall is bypassed for this research session per the operational note, and all fetch failures encountered were ordinary HTTP 404s (wrong/nonexistent doc path), not network blocks: `https://openai.github.io/openai-agents-python/sandbox/` (nav/index page) and `https://openai.github.io/openai-agents-python/sandbox/permissions/` both 404'd; content from those was instead recovered via `https://openai.github.io/openai-agents-python/sandbox/guide/`.
- **Isolation tech of hosted providers unconfirmed by OpenAI.** Third-party vendor blogs claim specific isolation tech (e.g. Firecracker microVMs for E2B) and cold-start numbers for the seven hosted providers, but OpenAI's own docs never name any provider's isolation mechanism or publish comparative performance numbers — those claims are excluded from determinations here as out of scope (belongs to each provider's own assessment, not OpenAI's).
- **`LocalDir`/bind-mount semantics never directly confirmed.** Multiple targeted fetches could not find explicit "copy vs bind-mount vs symlink" language for `LocalDir` manifest entries; the only positive signal (Unix-local "creates a temporary workspace... and cleans it up") suggests copy-based staging, used cautiously as circumstantial rather than definitive evidence.
- **Enforcement-plane and outage/fail-closed behavior are undocumented** for both the Agents SDK sandbox and the Responses API container's `network_policy` — a real gap for the "network-control axis gets deepest scrutiny" mandate; this is a genuine documentation silence, not a product absence, so left as Unknown throughout axis C's network sub-criteria rather than marked No.

# OpenAI Codex CLI Sandbox
category: local (+ optional cloud)
Lightweight terminal coding agent with OS-native process sandboxing (Seatbelt/Landlock) by default; separate opt-in "Codex Cloud" runs tasks in OpenAI-hosted containers | built on: Seatbelt (macOS), Bubblewrap+Landlock+seccomp (Linux/WSL2), Restricted Tokens/ACLs (Windows native); optional userspace `codex-network-proxy` for L7 policy | license: Apache-2.0, self-hostable (CLI only) | maturity: OpenAI-backed, actively released (frequent point releases, e.g. v0.125, v0.129 referenced in changelog/community threads), large adoption (~85k GitHub stars per third-party count, unverified against official source)

Note on scope: this assessment covers the **local CLI's** sandbox (Seatbelt/Landlock/network-proxy), per the provider brief. Codex Cloud is a materially different, container-based execution mode and is called out separately where relevant (execution_locality, workspace_modes) but is not the primary subject.

## A. Identity
### built_on (prose-only)
Local execution is **not** container- or VM-based — Codex CLI runs as a normal host process whose syscalls/filesystem/network are constrained in-place by the OS's native sandboxing primitive: Apple's Seatbelt (`sandbox-exec` + a dynamically generated SBPL profile) on macOS; a combination of Bubblewrap (namespace isolation, vendored in-repo), Landlock (LSM filesystem restriction), and seccomp (syscall/network filtering) on Linux; Restricted Tokens/ACLs on native Windows (WSL2 uses the Linux path). `codex debug seatbelt`/`codex debug landlock` (legacy aliases for `codex sandbox macos`/`codex sandbox linux`) let developers test commands through the sandbox directly. A separate, off-by-default `codex-network-proxy` Rust component (loopback HTTP `127.0.0.1:3128` / SOCKS `127.0.0.1:8081`) adds L7 domain policy on top of the OS-level network gate. Codex Cloud, by contrast, runs tasks in OpenAI-managed containers ("Codex creates a container and checks out your repo").
Sources:
- https://learn.chatgpt.com/docs/sandboxing — "macOS: Uses the built-in Seatbelt framework"; "Linux/WSL2: Implements enforcement via bubblewrap (user namespace isolation)"
- (search synthesis of official docs) "On Linux, Codex uses a dedicated codex-linux-sandbox helper binary that combines: Bubblewrap ... Landlock ... Seccomp"
- https://learn.chatgpt.com/docs/environments/cloud-environment — "Codex creates a container and checks out your repo at the selected branch or commit SHA."

### execution_locality
execution_locality: Both — Local | Remote, via two distinct products.
Default/primary usage (`codex` CLI) executes entirely on the developer's own machine; the sandbox is a restriction on the local process, so project code and credentials never leave the machine except for model-API calls to OpenAI. A separate opt-in product, Codex Cloud (web/`codex cloud`), clones the repo into an OpenAI-hosted container and runs the agent remotely, returning a diff/PR — this is a genuinely separate deployment, not "self-host," since it always runs on OpenAI's infrastructure (there is no self-hosted Codex Cloud). Classified as Both because the same brand/CLI ships both modes and users route between them.
Sources:
- https://learn.chatgpt.com/docs/sandboxing — "Commands execute locally on the user's machine within enforced constraints"
- https://learn.chatgpt.com/docs/environments/cloud-environment — "Codex clones the repository and checks out the default branch"

### open_source (prose-only)
Apache-2.0 license, github.com/openai/codex. The CLI/sandbox code is fully open source and self-hostable in the sense that the sandbox itself requires no OpenAI infrastructure to run (only model calls need the API). Codex Cloud's container backend is not open source / not self-hostable.
Sources:
- https://github.com/openai/codex/blob/main/LICENSE (per search result title/metadata: "Apache License 2.0")

### maturity (prose-only)
OpenAI-backed, frequent releases (community threads reference v0.116–v0.129 across a few months), broad community adoption. Star count (~85k) comes from third-party aggregator summaries, not independently confirmed against the live GitHub repo page in this session (the initial WebFetch of github.com/openai/codex returned only a stripped nav/description, no star count).
Sources:
- (third-party, unverified) search-engine synthesis citing "OpenAI Codex CLI (~85k GitHub stars)"

## B. Threat protection
### host_fs_damage
host_fs_damage: Yes — default `workspace-write` sandbox mode confines **writes** to the workspace root(s) (plus explicitly configured `writable_roots`); `read-only` mode blocks all writes; only `danger-full-access` removes the boundary.
Sandbox mode is a first-class CLI concept (`sandbox_mode = read-only | workspace-write | danger-full-access`), and the newer permission-profile system (`:read-only`, `:workspace`, `:danger-full-access`) generalizes this into named, extensible profiles with per-path `read`/`write`/`deny` rules and glob support.
Sources:
- https://learn.chatgpt.com/docs/sandboxing — "workspace-write: The agent can read files, edit within the workspace, and run routine local commands inside that boundary."
- https://learn.chatgpt.com/docs/permissions — "write: Allows commands to read and modify files under the path, including creating, renaming, and deleting files" (deny overrides write overrides read)

### credential_theft
credential_theft: Partial — writes are workspace-confined by default, but **filesystem reads are not confined to the workspace by default**; protecting credential paths (`~/.ssh`, `~/.aws`, dotfiles) requires the user to add explicit `deny` rules — it is not a default posture.
An open GitHub issue on the official repo (#5237) reports Codex reading files outside the working directory without prompting, and the official Permissions doc's own worked examples for hardening explicitly show the user adding `"~/.ssh" = "deny"` — i.e., the vendor's own guidance is "add this yourself," not "this is already denied." Separately, CVE-2025-61260 (third-party-reported, CVSS 9.8) described a trust-boundary bug where a repo's `.env` could redirect `CODEX_HOME` and cause unapproved MCP server auto-loading. Network egress is blocked by default in workspace-write mode (see network_default_posture), which limits — but does not eliminate — exfiltration of anything read.
Sources:
- https://github.com/openai/codex/issues/5237 — user-reported: Codex "accessed files outside the launch directory without permission"
- https://learn.chatgpt.com/docs/permissions — "Exact paths work well for stable locations such as `~/.ssh`" (shown as a rule the user adds)
- (third-party) miggo.io CVE database — "CVE-2025-59532 ... CVE-2025-61260" sandbox/trust-boundary bypasses

### data_exfiltration
data_exfiltration: Partial — network access is off by default in `workspace-write` mode (blocks all egress), but turning network on is a single blunt boolean that, by itself, grants **unrestricted** outbound access; domain-level allowlisting is a separate, also-off-by-default feature (`features.network_proxy`) that must be independently enabled to constrain that traffic.
The documented interaction matrix: network off + proxy on → still fully blocked; network on + proxy off → unrestricted outbound; network on + proxy on → constrained by configured domain policy. So "safe by default, unrestricted-if-enabled-without-a-second-step" is the accurate characterization.
Sources:
- https://learn.chatgpt.com/docs/agent-approvals-security — "Network access defaults to off"; `[sandbox_workspace_write] network_access = true`
- (search synthesis of developers.openai.com/codex config docs) "Network on + network_proxy off means network stays on with unrestricted direct outbound access"

### malicious_execution
malicious_execution: Partial — approval policies (`on-request` default, `untrusted`, `never`, or granular sub-toggles) gate command execution, and the sandbox limits blast radius via fs/network confinement, but multiple disclosed CVEs demonstrate real bypasses of these boundaries.
CVE-2025-59532 (third-party-reported) describes a Codex-cloud-container sandbox escape via improperly sanitized GitHub branch names, letting an attacker inject commands and retrieve auth tokens; a separate researcher (Cymulate) documented a Windows-specific chain turning a prompt-injected web search into host-level command execution via binary hijacking. These are patched-per-disclosure, not evidence of an unfixable design flaw, but show the sandbox has been bypassed in practice more than once.
Sources:
- (third-party) miggo.io — "CVE-2025-59532 ... sandbox bypass caused by improper handling of the current working directory (cwd)... enabling arbitrary file writes and command execution"
- (third-party) cymulate.com — "a routine web search into silent host-level command execution outside the sandbox by abusing prompt injection through web.run"

### escape_resistance
escape_resistance: isolation boundary stronger than a plain unsandboxed process, but weaker than a container or microVM — it is a same-kernel, syscall/MAC-level process sandbox, not a separate isolation kernel.
Seatbelt is Apple's in-kernel Mandatory Access Control framework (SBPL profile applied via `sandbox-exec`); Landlock is an unprivileged Linux Security Module providing a per-process filesystem ruleset, paired with seccomp syscall filtering and Bubblewrap namespaces. All three run on the **same kernel** as the rest of the host — there is no hypervisor or separate-kernel boundary the way a microVM (Firecracker/gVisor) provides. This tier is consistent with "process sandbox," the shallowest of the escape-resistance tiers (below container, below microVM). Documented CVEs (CVE-2025-59532, CVE-2025-61260, plus the Cymulate Windows chain) are evidence of a real, exercised escape surface, though each was patched.
Sources:
- https://learn.chatgpt.com/docs/sandboxing — "macOS: Uses the built-in Seatbelt framework"; "Linux/WSL2: Implements enforcement via bubblewrap"
- (third-party synthesis) "Linux uses a combination of Landlock/seccomp APIs to enforce the sandbox configuration"

### resource_abuse
resource_abuse: No — no documented CPU, memory, disk, or process-count quotas for the local sandbox; open feature requests ask for exactly this.
An open GitHub issue reports unbounded memory growth (Windows CLI reaching ~90GB committed memory while idle) and a separate open feature request (#11523) explicitly asks for "Global memory budget and proactive OOM protection," confirming none exists today. Codex Cloud vaguely references containers being "killed during the step (likely due to resource limits)" per a community forum post, but no published limit values were found in official docs.
Sources:
- (GitHub, official repo, community-reported) openai/codex#12414 — "codex-cli 0.104.0 on Windows 10 exhibits unbounded memory commit growth when idle (reaches ~90GB...)"
- (GitHub, official repo) openai/codex#11523 — "Feature request: Global memory budget and proactive OOM protection for Codex CLI"

## C. Feature set & granularity
### network_default_posture
network_default_posture: deny-by-default — both the local `workspace-write` sandbox and the Codex Cloud agent phase block all outbound network access unless explicitly turned on.
Local: `sandbox_workspace_write.network_access` defaults to `false`. Cloud: "Codex blocks internet access during the agent phase" by default (the separate setup-script phase does retain internet access for installing dependencies).
Sources:
- https://learn.chatgpt.com/docs/agent-approvals-security — "Network access defaults to off"
- https://learn.chatgpt.com/docs/cloud/internet-access — "By default, Codex blocks internet access during the agent phase."

### egress_allowlist
egress_allowlist: Partial — a real domain-allowlist mechanism exists (both locally, opt-in, and in Codex Cloud, per-environment) but it sits behind a second on/off toggle separate from the base network switch, so out-of-the-box "enabling network" gives zero allowlisting.
Local granularity ladder (via `features.network_proxy.domains`, a `map<string, allow|deny>`, off by default): exact host (`example.com`) → subdomain wildcard (`*.example.com`) → apex+subdomain wildcard (`**.example.com`) → global (`*`), with explicit `deny`-wins-on-conflict precedence, plus a parallel Unix-socket allow/deny map. Cloud granularity: per-environment preset ("None" / "Common dependencies" 70+-domain preset / "All unrestricted"), individually addable domains, plus optional HTTP-method scoping (see http_path_rules). No IP/CIDR or port-range scoping was documented for either surface, and no path/regex rules were found.
Sources:
- (search synthesis of developers.openai.com/codex config-reference) "features.network_proxy.domains is a map<string, allow | deny>... deny wins on conflicts"
- https://learn.chatgpt.com/docs/cloud/internet-access — "Common dependencies: Use a preset allowlist of domains commonly used for downloading and building dependencies"

### dns_level_blocking
dns_level_blocking: No — the managed network proxy does not provide allowlist-aware DNS resolution inside the sandbox; multiple official-repo issues report DNS resolution failing wholesale (for allowed and blocked domains alike) rather than being selectively gated.
An open feature request explicitly asks OpenAI to add "an allowlist-aware DNS forwarder in the sandbox network namespace" — confirming this does not exist yet. Community reports include SSH/git operations failing with "Could not resolve hostname github.com" purely due to sandbox DNS handling, independent of the domain policy.
Sources:
- (GitHub, official repo) openai/codex#22387 — "Managed network proxy should provide allowlisted DNS resolution inside Linux sandbox"
- (GitHub, official repo) openai/codex#12867 — "Codex sandbox blocks DNS/SSH sockets; git push fails with \"Could not resolve hostname github.com\""

### tls_mitm_inspection
tls_mitm_inspection: Unknown/Partial — the officially documented direction is Codex **trusting an external** corporate TLS-inspecting proxy (custom CA cert support); a **Codex-operated** MITM CA that inspects the agent's own traffic for header injection/audit/method enforcement is described only in third-party writeups, not confirmed in the official docs fetched this session.
Officially confirmed: "a unified custom-CA subsystem covers every outbound connection" so Codex works behind enterprise TLS-intercepting proxies (Zscaler/Bluecoat/Forcepoint-style) — this is Codex trusting a corporate MITM, not Codex performing L7 inspection itself. A third-party blog additionally describes a `ManagedMitmCa` subsystem where Codex's own local proxy terminates TLS for header injection, request auditing, and method enforcement — plausible given the documented proxy architecture, but not found in an official source, so kept Partial/Unknown rather than Yes.
Sources:
- (search synthesis, described as citing OpenAI docs) "a unified custom-CA subsystem covers every outbound connection... Codex CLI will reject these connections unless you provide the CA bundle"
- (third-party, explicitly unverified) codex.danielvaughan.com — "the proxy supports optional TLS termination using internally generated CA certificates managed by the ManagedMitmCa subsystem"

### http_path_rules
http_path_rules: Partial — HTTP **method**-level gating is documented (Codex Cloud, officially; local network_proxy, third-party-only); no URL **path**-level or regex rule was found in any source for either surface.
Cloud: "restrict network requests to GET, HEAD, and OPTIONS" with POST/PUT/PATCH/DELETE blockable as an explicit safeguard. Local `network_proxy`: domain-only policy documented officially (`domains` map); a third-party source additionally claims "method enforcement" for the local proxy but this wasn't corroborated in official docs.
Sources:
- https://learn.chatgpt.com/docs/cloud/internet-access — "restrict network requests to GET, HEAD, and OPTIONS. Requests using other methods... are blocked"

### proto_coverage
proto_coverage: Partial — HTTP/HTTPS domain policy and a separate Unix-socket allow/deny policy are documented; DNS is explicitly NOT policy-scoped (fails or passes wholesale, not per-domain); no documentation found for ICMP, UDP, QUIC/HTTP3, or per-protocol SSH/WS/gRPC control, and SSH in practice appears to hit the same coarse network on/off gate rather than domain-scoped policy (git-over-SSH failures are reported as an all-or-nothing sandbox DNS/socket problem, not a scoped-SSH-policy gap). No documented design for extending the policy model to arbitrary new L7 protocols.
Sources:
- (search synthesis of official config docs) "features.network_proxy.unix_sockets is a map<string, allow | deny>"
- (GitHub, official repo) openai/codex#12867 — SSH/git failures attributed to sandbox DNS/socket handling, not domain policy

### live_rule_reload
live_rule_reload: Unknown — no source fetched this session states whether editing `config.toml` (domain/permission rules) applies to an already-running Codex session/sandbox without restart, or requires relaunching `codex`.

### firewall_escape_hatch
firewall_escape_hatch: Partial — a break-glass bypass exists (`--dangerously-bypass-approvals-and-sandbox` / `--yolo`, and the `:danger-full-access` permission profile) but it is all-or-nothing and scoped to how the session is launched, not a timed bypass with automatic re-enforcement.
No evidence of a "disable for N minutes then auto-re-enable" mechanism; the only lever is choosing full-bypass mode for that invocation/profile, which the docs explicitly flag as "not recommended."
Sources:
- (search synthesis of official agent-approvals-security doc) "--dangerously-bypass-approvals-and-sandbox flag (alias --yolo) provides 'No sandbox; no approvals' access—explicitly marked as 'not recommended'"

### enforcement_plane
enforcement_plane (prose): split across two layers — kernel-level OS sandbox (Landlock LSM + seccomp BPF on Linux; Seatbelt/SBPL Mandatory Access Control on macOS; Restricted Tokens/ACLs on Windows) gates filesystem writes and the coarse network on/off switch; a cooperating **userspace loopback proxy** (`codex-network-proxy`, HTTP `127.0.0.1:3128` / SOCKS `127.0.0.1:8081`) enforces domain-level policy only when `features.network_proxy` is separately enabled.
Because domain allowlisting lives in a userspace proxy the sandboxed process must be routed through (rather than being enforced at the kernel/eBPF layer the way the coarse network toggle is), the trust boundary for L7 policy is one layer removed from the kernel enforcement of the write/execute sandbox — consistent with the documented proxy-config escape hatches (`dangerously_allow_non_loopback_proxy`, `dangerously_allow_all_unix_sockets`) that explicitly widen this boundary. Traffic through the proxy is logged (see network_audit); traffic when the proxy is not engaged is not.
Sources:
- (search synthesis of official config docs) "dangerously_allow_non_loopback_proxy = true can expose proxy listeners beyond loopback, and dangerously_allow_all_unix_sockets = true bypasses the Unix socket allowlist"

### fail_closed
fail_closed: Unknown — no source describes what happens to already-permitted traffic or the sandbox's network posture if the `codex-network-proxy` process crashes mid-session. The outer network on/off gate is enforced by the OS sandbox layer itself (not the proxy), which suggests policy-scoped traffic would fail closed if the proxy died, but this is inference, not a documented guarantee.

### network_audit
network_audit: Yes, opt-in — Codex supports OpenTelemetry log export that includes network-proxy allow/deny decisions, giving per-decision egress visibility, but only when OTel export is configured (not on by default) and only for traffic actually routed through the network_proxy feature.
Sources:
- (search synthesis of official managed-configuration/enterprise docs) "Codex supports OpenTelemetry log export for various Codex events including network proxy allow or deny events, enabling security teams to monitor network activity"

### workspace_modes
workspace_modes: Partial — live/bind-style editing locally, snapshot/clone-style in the cloud, but the two are tied to different execution localities rather than being a single user-selectable choice within one mode.
Local CLI operates directly on the live working directory in place (there is no container/VM boundary to bind-mount across — it IS the host filesystem, subject to the sandbox's write confinement). Codex Cloud clones the repo into an ephemeral, cached container (state cached "up to 12 hours") and surfaces changes back as a diff/PR rather than syncing live.
Sources:
- https://learn.chatgpt.com/docs/environments/cloud-environment — "Codex caches container state for up to 12 hours to speed up new chats and follow-ups."

### observability
observability: Partial — OpenTelemetry export of policy/network events plus enterprise "Workspace Analytics" / "Analytics API" surfaces exist, but there is no dashboard bundled with the open-source CLI itself; dashboards/analytics are ChatGPT-workspace/enterprise features.
Sources:
- (search synthesis of official enterprise docs) "Use the Compliance API for audit and investigation records" and "interactive ChatGPT workspace analytics and Codex analytics"

### supervision
supervision: No — approval prompts are synchronous human-in-the-loop gates (or an optional "auto_review" reviewer subagent), not an external always-on supervisor process that can observe and intervene in a running agent from outside; no containment/kill/quarantine control plane was documented.
Sources:
- (search synthesis of official config-reference) "approvals_reviewer = 'user | auto_review' determines who reviews eligible prompts under on-request policies... auto_review uses reviewer subagent"

### fleet_mgmt
fleet_mgmt: Partial — `agents.max_threads` (default 6) governs parallel subagents within a single session, and enterprise Compliance/Analytics APIs give org-wide visibility, but no documented CLI-native registry/naming/lifecycle system for multiple independent local sandboxed agent instances was found.
Sources:
- (search synthesis of official docs) "agents.max_threads defaults to 6 when you leave it unset"

### snapshots_persistence
snapshots_persistence: Partial — Codex Cloud caches container state for up to 12 hours (ephemeral, not indefinite, with an optional maintenance script on resume); the local CLI has no documented pause/resume/snapshot of sandbox state, only plain config persistence (auth, permission profiles) under `~/.codex/`.
Sources:
- https://learn.chatgpt.com/docs/environments/cloud-environment — "Codex caches container state for up to 12 hours to speed up new chats and follow-ups."

## D. Setup
### setup
setup: Easy — a single install script, run `codex` in a project directory, and a browser-based ChatGPT OAuth sign-in (or API key); no Docker/VM/account infrastructure required for local mode since sandboxing is OS-native.
Steps documented: `curl -fsSL https://chatgpt.com/codex/install.sh | sh` (macOS/Linux; separate npm/Windows installers exist), open a project directory, run `codex`, sign in.
Sources:
- (search synthesis of official CLI docs) "Run the installer... Open a project directory and execute codex... Sign in using ChatGPT or alternative authentication methods"

## E. Daily use
### daily_use
daily_use: Moderate — the sandbox itself is transparent per-command (no container start/stop step), but community-reported friction is real: approval prompts under `on-request` interrupt flow, and the newer `network_proxy`/DNS interplay has produced multiple open bug reports of routine operations (npm installs, git/ssh) breaking inside the sandbox even when nominally allowed.
Sources:
- (GitHub, official repo) openai/codex#18675 — "Windows Codex app: npm/network DNS fails in sandbox despite network enabled; cannot reach local proxy"
- (GitHub, official repo) openai/codex#19146 — "codex cli interactive mode doesn't honor config for domain filtering"

## F. Configuration
### config_depth
config_depth: Deep — a single `config.toml` covers sandbox mode, named/extendable permission profiles with per-path filesystem rules (glob patterns, `:workspace_roots`/`:minimal` scoped tokens), network domain/unix-socket policy, layered approval policy (including granular sub-toggles for `sandbox_approval`, `rules`, `mcp_elicitations`, etc.), writable roots, MCP server config, and enterprise-managed overrides (`requirements.toml`, MDM `com.openai.codex` preferences) layered above user config.
Sources:
- https://learn.chatgpt.com/docs/config-file/config-reference — "approval_policy = 'untrusted | on-request | never | { granular = {...} }'"; "[permissions.<name>] description / extends / filesystem / network"

### policy_model
policy_model: Moderate-to-deep, not fully unified — named permission profiles with sane secure-default built-ins (`:read-only`, `:workspace`, `:danger-full-access`) and layered precedence (system/enterprise > user > project) give real per-case policy control, but the network story is split across two independently-toggled layers (coarse `network_access` boolean + separate opt-in `network_proxy` domain policy) rather than one seamless dial, and default filesystem reads outside the workspace are open unless the user opts into `deny` rules.
Sources: see egress_allowlist and credential_theft above (same evidence).

## G. DX
### bind_mount_sharing
bind_mount_sharing: Yes (local) / No (cloud) — local Codex CLI edits the live project directory in place since there is no VM/container boundary to cross (the sandboxed process runs directly on the host, confined by Seatbelt/Landlock, not virtualized). Codex Cloud is one-way: changes surface back only as a diff/PR against the cloned container's working copy.
Sources:
- https://learn.chatgpt.com/docs/sandboxing — "Commands execute locally on the user's machine within enforced constraints"

### cred_forwarding
cred_forwarding: NA/Partial — because the local sandbox constrains an ordinary **host process** rather than a remote/virtualized environment, there is no "forwarding bridge" concept the way a container-based tool needs one: `~/.ssh`, `~/.gnupg`, and git credential helpers are already present on the host and are reachable by the sandboxed process **by default** (see credential_theft) unless the user explicitly adds `deny` filesystem rules. No dedicated ssh-agent/gpg-agent forwarding mechanism is documented because none is architecturally needed for local mode.
Sources: see credential_theft above (same evidence).

### browser_auth
browser_auth: Yes — `codex` opens a local browser window for ChatGPT OAuth sign-in and the browser returns an access token to the CLI; this works natively (no proxy/socket-bridge needed) because the CLI process runs directly on the host, not inside a remote/virtualized sandbox.
Sources:
- (search synthesis of official auth docs) "Codex opens a browser window, you sign in with your ChatGPT account, and the browser returns an access token."

### shared_dirs
shared_dirs: Yes — `sandbox_workspace_write.writable_roots` (array of additional writable directories) and permission-profile filesystem maps let the user extend read/write access to directories beyond the primary workspace root.
Sources:
- https://learn.chatgpt.com/docs/config-file/config-reference — "sandbox_workspace_write.writable_roots is an array<string> specifying additional writable directories beyond workspace roots"

### git_worktrees
git_worktrees: Yes — worktrees are documented as a first-class parallel-work pattern: an isolated copy of the repo sharing history, each with its own branch and its own independent Codex session.
Sources:
- (search synthesis of official CLI docs) "A worktree is an isolated copy of your Git repository sharing the same history but checked out to its own folder. Each worktree can have its own branch and its own Codex session running independently."

### nested_containers
nested_containers: Unknown — no dedicated, documented "give the sandboxed agent a Docker socket" toggle was found. Because local execution is a plain host process (not itself containerized), Docker would be reachable via ordinary filesystem/exec permission unless denied, gated additionally by whatever network/unix-socket policy is active if network_proxy is enabled — but this is inference from the general permission model, not a documented first-class feature. (Distinct from running Codex itself *inside* Docker, which third-party guides cover but which is a different question.)

### harness_agnostic
harness_agnostic: No/NA — this sandbox subsystem is built into, and ships only as part of, Codex CLI itself; it is not a general-purpose sandbox usable with other coding-agent CLIs.

## H. Performance
### performance
performance: Unknown — no vendor-published startup-latency, disk-footprint, RAM-overhead, or IO-throughput benchmarks were found in the official docs fetched. The only concrete numbers found were community bug reports (not benchmarks): one GitHub issue describes Windows CLI memory climbing to ~90GB when idle over several hours, which is an anecdote about a bug, not a representative performance baseline.
Sources:
- (GitHub, official repo, community-reported) openai/codex#12414 — "reaches ~90GB, causes system OOM"

## I. Feasibility
### feasibility
feasibility: Adoptable today, with rough edges in the newer network layer — macOS, Linux (native + WSL2), and Windows are all supported with no Docker/Kubernetes prerequisite for local mode (only the installer + ChatGPT/API auth), and the CLI is Apache-2.0 so there's no licensing lock-in. The `network_proxy`/domain-allowlist feature is explicitly labeled experimental and has several open correctness issues (DNS handling, interactive-mode config not honored), so teams relying on fine-grained egress control specifically should expect friction; the base sandbox (fs/network on-off) is comparatively mature.
Sources:
- (search synthesis of official config docs) "features.network_proxy as boolean or table enables sandboxed networking (experimental; off by default)"
- (GitHub, official repo) openai/codex#19146 — "codex cli interactive mode doesn't honor config for domain filtering"

## J. Price (prose-only)
The CLI itself is free and open source (Apache-2.0) — no cost to install or run the sandbox. Usage of the underlying model requires either a ChatGPT plan (tiers exist from a free tier up through higher-usage paid tiers, per the official pricing page at developers.openai.com/codex/pricing, not independently re-fetched this session after redirect handling) or a pay-as-you-go API key billed at standard OpenAI per-token rates. There is no self-host option for Codex Cloud (OpenAI-hosted only); the local sandbox mode requires no OpenAI-hosted infrastructure beyond model inference calls.
Sources:
- (search synthesis, official pricing page exists but was not directly re-fetched) "Codex access depends on your ChatGPT plan, workspace plan, or API key"

## K. Extensibility
### extensibility
extensibility: Partial — MCP server support (local/remote, shared config across ChatGPT app/CLI/IDE extension), extendable named permission profiles (`extends` a parent profile), and enterprise config layering (`requirements.toml`, MDM preferences) provide real extension points; there is no documented plugin/bundle system for shipping custom prebuilt sandbox images, and no custom-harness concept (this product IS the harness).
Sources:
- (search synthesis of official MCP docs) "The ChatGPT desktop app, Codex CLI, and IDE extension support MCP servers and share MCP configuration for the same Codex host."
- https://learn.chatgpt.com/docs/config-file/config-reference — "[permissions.<name>] ... extends = 'parent-profile-name'"

## Unknowns & caveats
- **Blocked**: `https://openai.com/index/running-codex-safely/` returned HTTP 403 Forbidden to WebFetch (likely bot protection) — this official OpenAI blog post on Codex safety architecture could not be read directly; findings here rely on developers.openai.com/learn.chatgpt.com docs and search-engine synthesis of that same domain instead.
- **live_rule_reload**, **fail_closed**, and full **tls_mitm_inspection** (Codex-operated MITM specifically, as opposed to trusting an external corporate MITM) remain genuinely undetermined — official docs are silent, so these are marked Unknown rather than No per the guidelines.
- Most `developers.openai.com/codex/*` official-doc URLs 308-redirect to `learn.chatgpt.com/docs/*` (same content, different host) — sources are cited under whichever host actually served the content in this session.
- A meaningful fraction of the WebFetch extractions in this session came back as "search synthesis of official docs" rather than a direct fetch of the primary page, because several official doc pages (config-reference, agent-approvals-security, security overview) are long and the fetch tool's summarizer sometimes missed sections found by a second, more targeted WebFetch or by WebSearch snippets citing the same official page. Where a claim rests only on such synthesis (not a direct quote from a page this session fetched in full), it is marked as such in the Sources line above; a follow-up researcher should re-fetch developers.openai.com/codex/config-reference and /codex/agent-approvals-security directly to confirm exact wording.
- Filesystem-read-scope claims conflicted across sources by apparent doc/version vintage: an older description (and the open GitHub issue) describe read access as effectively filesystem-wide by default in the legacy `sandbox_mode` system; the newer named-permission-profile docs describe missing/empty filesystem tables as "keep[ing] filesystem access restricted." The credential_theft/policy_model verdicts above take the conservative reading (reads not confined to workspace, sensitive-path denial is opt-in) since the primary-source GitHub issue and the permission doc's own remediation guidance both point that direction, but this is flagged as a live doc/behavior discrepancy, not resolved with full confidence.
- No CVE database or NVD entry was fetched directly for CVE-2025-59532 / CVE-2025-61260; both are cited via a third-party vulnerability-database site (miggo.io), not confirmed against a primary NVD/GitHub-Security-Advisory record in this session.

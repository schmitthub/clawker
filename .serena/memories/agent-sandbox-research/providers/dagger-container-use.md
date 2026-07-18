
# Dagger container-use
category: local (with optional CI/remote execution via Dagger Engine)
Containerized dev environments for coding agents, each agent gets its own container + git branch | built on Dagger Engine (BuildKit/runc/containerd) | Apache 2.0 | early/active development, ~3.9k GitHub stars (Aug 2025 snapshot, likely higher by 2026-07-18), backed by Dagger Inc.

As-of date: 2026-07-18. Primary sources: official docs at container-use.com (fetched via /llms.txt page index + raw .mdx from github.com/dagger/container-use), official docs.dagger.io (engine architecture/security), github.com/dagger/container-use README/CLI (github.com and raw.githubusercontent.com). deepwiki NOT used (banned).

## A. Identity
### built_on (prose-only)
Container-based. "Powered by Dagger" — the Dagger Engine, which is a BuildKit-based execution engine using containerd (high-level OCI runtime) and runc (low-level OCI runtime) to actually start containers. Each container-use "environment" = one container + one git branch, combined via git worktrees. Critically, per official Dagger docs (docs.dagger.io/faq/), the Dagger Engine itself "must currently run in a privileged container" because rootless mode is unsupported (overlayfs snapshotter needs root; moving network devices into a container's own netns needs root) — the engine's own recommended security posture is "approach securing Dagger as you would approach securing Docker or Kubernetes... making the host machine the security boundary." docs.dagger.io/reference/configuration/engine/ further states Dagger "by default has an open security policy that allows exec operations to access the `insecureRootCapabilities` options, which effectively allows all operations root access (similar to Docker's `--privileged` flag)," configurable off via `engine.json`/`engine.toml`. No supervisor/control-plane beyond the CLI + Dagger Engine daemon; no separate policy-enforcement process observed in docs.
Sources:
- https://docs.dagger.io/faq/ — "Dagger Engine must currently run in a privileged container"
- https://docs.dagger.io/reference/configuration/engine/ — "allows exec operations to access the insecureRootCapabilities options, which effectively allows all operations root access"
- https://container-use.com/introduction.md — "combines Dagger containerization with Git worktrees to enable isolated agent execution"

### execution_locality
execution_locality: Local — default usage is a CLI installed on the developer's machine (`brew install dagger/tap/container-use`) driving a local Docker/Dagger Engine; the MCP server runs locally via `container-use stdio`. Dagger also supports running the same containerized workflow in CI ("the same environment used on your laptop can be executed identically in your CI system via the Dagger Engine" — Dagger blog), which is an optional secondary deployment mode, not the default. Project code and any configured secrets stay on the local Docker host unless the user separately wires this into a remote CI runner.
Sources:
- https://container-use.com/quickstart.md — "Docker and Git installed before starting"
- https://dagger.io/blog/agent-container-use/ — "the same environment used on your laptop can be executed identically in your CI system via the Dagger Engine"

### open_source (prose-only)
Apache 2.0 license, fully open source, self-hostable by nature (it IS the local/self-hosted tool — no managed-cloud variant found in docs).
Sources:
- https://github.com/dagger/container-use — repo license Apache 2.0 (per repo metadata)

### maturity (prose-only)
~3.9k stars / ~201 forks as of the fetched snapshot (page appears to reflect an Aug 2025-era state, latest release shown v0.4.2; actual current numbers likely higher as of 2026-07-18 but could not be freshly re-verified beyond the fetched snapshot). README/docs self-describe the project as in "early development and actively evolving." Backed by Dagger Inc., the company behind the established Dagger CI/CD engine/BuildKit-based product.
Sources:
- https://github.com/dagger/container-use — "3.9k stars, 201 forks, 14 releases (v0.4.2 latest as of Aug 2025)"

## B. Threat protection
### host_fs_damage
host_fs_damage: Partial — Yes, in the sense that agent work happens inside a container's own filesystem, not directly on host files (host review/import is via explicit `checkout`/`merge`/`apply`, not live host-file editing). Weakened by the fact the underlying Dagger Engine must run `--privileged` on the host Docker daemon, and Dagger's default engine security policy is described as "open" (permits `insecureRootCapabilities`, i.e. Docker-`--privileged`-equivalent execs) unless the operator explicitly disables it in `engine.json`/`engine.toml`. Container-use docs do not state whether it disables this default or what security policy the agent's own environment container runs under.
Sources:
- https://docs.dagger.io/reference/configuration/engine/ — "By default, Dagger has an open security policy that allows exec operations to access the insecureRootCapabilities options"
- https://container-use.com/environment-workflow.md — "Isolated Development: Agents make changes, execute commands, and build features completely separated from your local work"

### credential_theft
credential_theft: Yes — secrets are handled via an explicit reference model (`op://`, `env://`, `vault://`, `file://` schemas), configured per-key with `container-use config secret set`; actual values are resolved and injected as env vars only inside the container at runtime and are "stripped from logs and command outputs to prevent leaks." The AI model itself never sees raw secret values — only code running in-container can use them. No ambient/automatic mounting of host credential directories (`~/.ssh`, `~/.aws`, etc.) is documented; anything the agent needs must be explicitly configured as a secret reference.
Sources:
- https://container-use.com/secrets.md — "secrets are stripped from logs and command outputs to prevent leaks"
- https://container-use.com/secrets.md — "the AI model never accesses actual secret values—only agents running within the container environment can use them"

### data_exfiltration
data_exfiltration: No — no network egress control feature is documented anywhere in the official docs. The CLI reference (explicitly "Complete reference for all Container Use CLI commands and options") lists every command/flag and none relate to network/firewall/egress. The environment-configuration doc, when asked directly, confirms network configuration is not among the documented settings. There is no allowlist, no DNS filtering, no proxy — a container-use environment container gets whatever default outbound network access the underlying Docker/Dagger container networking provides (i.e., open by default, same as a plain `docker run` without `--network none`).
Sources:
- https://container-use.com/cli-reference.md — page is described as "Complete reference for all Container Use CLI commands and options"; no network-related command or flag present
- https://raw.githubusercontent.com/dagger/container-use/main/docs/environment-configuration.mdx — fetch explicitly confirms "does NOT include information about: Network configuration ... DNS settings ... Certificate Authority (CA) configuration"

### malicious_execution
malicious_execution: Partial — blast radius of untrusted code is bounded to the container's own filesystem/process namespace (standard container containment), and host review is gated behind explicit git operations. However this containment sits on top of a Dagger Engine that itself requires host-level `--privileged` and defaults to a permissive `insecureRootCapabilities` engine policy (see built_on), which is a materially larger trust boundary than sandboxes built on rootless containers, gVisor, or microVMs.
Sources:
- https://docs.dagger.io/faq/ — privileged-container requirement
- https://container-use.com/environment-workflow.md — "disposable sandboxes where agents can work safely without affecting your main codebase"

### escape_resistance
escape_resistance: Partial — isolation boundary is standard container namespaces/cgroups (runc/containerd via BuildKit), stronger than a bare host process, but shared-kernel — no microVM (Firecracker) or gVisor-style syscall-filtering boundary is used or mentioned for the agent's environment container. The underlying Dagger Engine itself must run `--privileged` on the host, and by default carries an "open security policy" granting `insecureRootCapabilities` (Docker-`--privileged`-equivalent) to exec operations unless explicitly disabled — a strictly weaker default posture than hardened container runtimes.
Sources:
- https://docs.dagger.io/faq/ — "approach securing Dagger as you would approach securing Docker or Kubernetes... making the host machine the security boundary"
- https://docs.dagger.io/reference/configuration/engine/ — "insecureRootCapabilities... effectively allows all operations root access (similar to Docker's --privileged flag)"

### resource_abuse
resource_abuse: No — no CPU/memory/disk limit configuration is documented. The environment-configuration reference (which enumerates base image, setup/install commands, env vars, secrets) explicitly does not include resource limits, and the CLI reference contains no resource-limit flags.
Sources:
- https://raw.githubusercontent.com/dagger/container-use/main/docs/environment-configuration.mdx — confirmed absence of "Resource limits (CPU/memory constraints)" from documented config surface
- https://container-use.com/cli-reference.md — no resource-limit flags present

## C. Feature set & granularity

### network_default_posture
network_default_posture: open-by-default — no egress restriction subsystem exists in the product at all (see data_exfiltration). An unconfigured environment container has whatever outbound network access standard Docker/Dagger container networking grants, i.e. effectively unrestricted internet access.
Sources:
- https://container-use.com/cli-reference.md — full CLI reference, no network/firewall commands
- https://raw.githubusercontent.com/dagger/container-use/main/docs/environment-configuration.mdx — network configuration confirmed not part of documented config surface

### egress_allowlist
egress_allowlist: No — no allow/deny egress list feature exists; granularity ladder is "none" (no domain list, no CIDR, no port scoping, no path rules — the feature does not exist).
Sources:
- https://container-use.com/cli-reference.md — complete CLI reference, no egress-control commands/flags

### dns_level_blocking
dns_level_blocking: No — no DNS filtering documented; follows from the total absence of a network-control subsystem.
Sources:
- https://container-use.com/cli-reference.md — no DNS/network flags in complete reference

### tls_mitm_inspection
tls_mitm_inspection: No — no TLS interception/L7 inspection capability documented.
Sources:
- https://container-use.com/cli-reference.md — no such feature in complete reference

### http_path_rules
http_path_rules: No — no per-path/method rule feature documented (no egress control layer to attach it to).
Sources:
- https://container-use.com/cli-reference.md — no such feature in complete reference

### proto_coverage
proto_coverage: No — no protocol-level egress control of any kind (HTTP/S, DNS, ICMP, TCP, UDP/QUIC, ssh, ws/wss, grpc) is documented; all protocols pass uncontrolled through standard container networking. No documented extensibility point for adding protocol-specific rules either, since no rule engine exists.
Sources:
- https://container-use.com/cli-reference.md — no protocol-scoped flags/commands anywhere in the complete CLI reference

### live_rule_reload
live_rule_reload: NA — there is no rule system to reload; the feature category does not apply.
Sources:
- https://container-use.com/cli-reference.md — no rule/firewall subsystem exists

### firewall_escape_hatch
firewall_escape_hatch: NA — no firewall exists to bypass; category does not apply.
Sources:
- https://container-use.com/cli-reference.md — no firewall subsystem exists

### enforcement_plane
enforcement_plane: No — there is no dedicated network-policy enforcement plane. Networking is whatever the Dagger Engine/BuildKit container runtime provides by default (standard Linux container networking via runc/containerd), with no kernel-level (eBPF/netfilter) or userspace-proxy policy layer sitting in front of it. Since there is no enforcement point, there is nothing for an agent to tamper with or route around specifically — but also nothing restricting it in the first place.
Sources:
- https://docs.dagger.io/faq/ — describes engine networking constraints (privileged netns move) but no policy/enforcement layer
- https://container-use.com/cli-reference.md — no enforcement-related commands

### fail_closed
fail_closed: NA — no network enforcement exists to fail open or closed; category does not apply.
Sources:
- https://container-use.com/cli-reference.md — no firewall subsystem exists

### network_audit
network_audit: Partial — general command/output history is logged and reviewable (`container-use log <env-id>`, `container-use watch`), which would surface e.g. a `curl` invocation an agent ran, but there is no dedicated per-request egress log (no connection-level record of destination host/IP/port/protocol independent of what the agent's own shell commands happened to print).
Sources:
- https://container-use.com/environment-workflow.md — "Log <env-id>: Review commit history" / "watch: Monitor real-time agent activity"

### workspace_modes
workspace_modes: Partial — only a git-branch/commit-sync model is documented: each environment is "a dedicated Git branch, containerized runtime, and automatic history tracking," and bringing agent work to the host requires an explicit `checkout`, `merge`, or `apply` step. No live bind-mount mode (host sees uncommitted working-tree edits in real time) is documented or implied — review of "any agent's work" is explicitly done via `git checkout <branch_name>`, i.e. only committed state syncs.
Sources:
- https://container-use.com/environment-workflow.md — "Environments function as isolated workspaces combining a dedicated Git branch, containerized runtime, and automatic history tracking"
- https://raw.githubusercontent.com/dagger/container-use/main/README.md — "Standard git workflow - just `git checkout <branch_name>` to review any agent's work"

### observability
observability: Yes — command history and logs per environment (`log`), real-time activity monitoring (`watch`), and diffs (`diff`) are all first-class CLI features giving passive visibility into what an agent actually did, beyond the agent's self-reported summary.
Sources:
- https://container-use.com/environment-workflow.md — "watch | Monitor real-time agent activity" / "log <env-id> | Review commit history"

### supervision
supervision: Partial — a human can manually "drop into any agent's terminal to see their state and take control when they get stuck" (`container-use terminal <env-id>`) and can `delete` a runaway environment, which is real intervention capability. But this is entirely manual/human-driven; no automated control-plane process is documented that itself observes agent behavior and dispatches containment actions (no auto-kill on policy violation, no automated anomaly response).
Sources:
- https://raw.githubusercontent.com/dagger/container-use/main/README.md — "Drop into any agent's terminal to see their state and take control"
- https://container-use.com/cli-reference.md — `terminal`, `delete` are user-invoked commands, no automated equivalent documented

### fleet_mgmt
fleet_mgmt: Yes — `container-use list` enumerates all environments with status/IDs; each environment is independently addressable, letting multiple concurrent agents be tracked, reviewed, and cleaned up (`delete --all`) as a fleet.
Sources:
- https://container-use.com/cli-reference.md — "list | List all environments and their status"

### snapshots_persistence
snapshots_persistence: Yes — environments persist (container + branch + history) until explicitly deleted; resuming a conversation or starting a new one that references an environment ID lets "the agent resume with all previous work intact." This is per-environment persistent state, not a generic pause/resume snapshot primitive with restore points, but it does survive across sessions.
Sources:
- https://container-use.com/environment-workflow.md — "Continue existing chat or start new session mentioning environment ID; Agent resumes with all previous work intact"

## D. Setup (spectrum)
setup: Easy — install via one command (`brew install dagger/tap/container-use` or a curl install script), verify with `container-use version`, then one command to wire into an MCP-capable agent (e.g. `claude mcp add container-use -- container-use stdio`). Only stated prerequisites are Docker and Git. Docs frame it as a 5-minute quickstart.
Prerequisites (Docker + Git) mean it inherits Docker's own setup burden on top, but no account/API key/cloud signup is required.
Sources:
- https://container-use.com/quickstart.md — page titled "Get started with Container Use in 5 minutes"; "Docker and Git installed before starting"

## E. Daily use (spectrum)
daily_use: Moderate — starting/attaching is lightweight (agent creates environments itself via MCP tool calls), and `list`/`log`/`diff`/`terminal` give quick inspection. But every environment provisions through a pull-base-image → setup-commands → copy-code → install-commands pipeline (mitigated by Dagger's build caching, per marketing copy, though no concrete numbers are given), and turning agent work into host state is a deliberate multi-step git ritual (`checkout`/`merge`/`apply`) rather than instant live file sync, adding procedural overhead versus a live bind-mount tool. Managing several concurrently running environment IDs by hand is also on the user.
Sources:
- https://container-use.com/environment-configuration.md — "Setup Commands: Run after pulling base image, before copying code" / "Install Commands: Run after copying code"
- https://dagger.io/blog/agent-container-use/ — "intelligent caching ensures that common operations are fast" (no benchmark given)

## F. Configuration
### config_depth
config_depth: Moderate — declarative, versionable config at `.container-use/environment.json` (committable for team sharing) covering base image, setup commands, install commands, env vars, and secret references, with a two-layer model (project baseline vs ephemeral per-agent adaptations, importable back into the baseline). Notably absent from the documented schema: network/firewall settings, resource limits (CPU/mem), and volume/mount configuration beyond the workspace itself.
Sources:
- https://container-use.com/environment-configuration.md — "Storage Location: .container-use/environment.json; Commit to version control for team sharing"
- https://raw.githubusercontent.com/dagger/container-use/main/docs/environment-configuration.mdx — confirmed absence of network/mounts/resource-limit config from the schema

### policy_model
policy_model: Rigid — there's no dial for isolation strength (no bind-mount-vs-copy choice, no per-run network tightening/loosening, no resource-limit override) — the product offers exactly one workspace/security model (container + git-branch, open networking, no resource caps) with no documented escape hatches to make it either more locked-down or more permissive per run.
Sources:
- https://container-use.com/cli-reference.md — no policy/security-toggle commands present in the complete CLI reference

## G. DX — host↔sandbox integration
### bind_mount_sharing
bind_mount_sharing: No — changes are shared via git commit/branch sync, not a live bind mount; see workspace_modes above. Bringing agent output to the host requires an explicit `checkout`/`merge`/`apply`, and there is no documented mode where host and container share a live writable filesystem view.
Sources:
- https://container-use.com/environment-workflow.md — review/merge/apply are explicit, discrete actions, not live sync

### cred_forwarding
cred_forwarding: No — (corrected 2026-07-18, attribution audit) credentials reach the container only through the explicit secrets mechanism (`op://`, `env://`, `vault://`, `file://` references configured per-key, resolved to a raw value injected as a plain container env var), which can reference an SSH key or cert via `file://`. There is no documented ssh-agent-socket or gpg-agent-socket forwarding, and no proxy header-injection with sentinel values — per the cred_forwarding rule, a resolved secret reference copied in as a plain env var is not a mediated forwarding mechanism.
Sources:
- https://container-use.com/secrets.md — "File References: Uses file:// schema to read secrets from local files (SSH keys, certificates, credential files)"

### browser_auth
browser_auth: Unknown — no mention anywhere in the fetched docs (quickstart, secrets, environment-configuration, agent-integrations, README) of a host-browser-proxying mechanism for OAuth/device-code flows triggered from inside the container. Searched the secrets doc (explicit alternative: configure tokens as secret references) and quickstart/CLI reference; neither confirms nor denies a browser-proxy path.
Sources:
- https://container-use.com/secrets.md — documents token-based auth via secret references as the credential path, with no browser-flow mechanism mentioned

### shared_dirs
shared_dirs: Unknown — official docs do not document additional host directory/volume mounts beyond the workspace itself (environment-configuration's documented schema omits "mount points or volume management" entirely). A third-party blog post (not official docs, noted as such per evidence rules) mentions "known shortcomings with directory mounts like `.azure/` for authentication," suggesting some mount capability may exist informally/undocumented, but this is not corroborated by official sources.
Sources:
- https://raw.githubusercontent.com/dagger/container-use/main/docs/environment-configuration.mdx — "does NOT include information about: ... Mount points or volume management"
- (third-party, low confidence) https://blog.techdecline.dev/container-use-in-dagger-projects/ — referenced via search snippet mentioning `.azure/` mount shortcomings; not independently fetched/verified

### git_worktrees
git_worktrees: Yes — git worktrees are core to the architecture, not an add-on: "combines Dagger containerization with Git worktrees to enable isolated agent execution," each environment = one worktree/branch pair.
Sources:
- https://container-use.com/introduction.md — "combines Dagger containerization with Git worktrees to enable isolated agent execution"

### nested_containers
nested_containers: Unknown — not documented whether the agent's own environment container gets a docker socket or nested container runtime. The underlying Dagger Engine generally supports container-based CI/build workloads, but container-use's docs make no statement about exposing this inside an agent's environment.
Sources:
- https://container-use.com/environment-configuration.md — no docker-socket/nested-runtime option documented
- https://container-use.com/cli-reference.md — no such flag/command

### harness_agnostic
harness_agnostic: Yes — MCP-protocol-based; the same `container-use stdio` command is wired into 17 documented agents/IDEs (Claude Code, Cursor, Windsurf, VSCode/Copilot, Zed, OpenCode, Goose, Sourcegraph Amp, Charm Crush, Cline, Qodo Gen, Kilo Code, Kiro, OpenAI Codex, Warp, Gemini CLI, JetBrains Junie, Amazon Q Developer), plus "any MCP-compatible agent" generically.
Sources:
- https://container-use.com/agent-integrations.md — enumerates setup for Claude Code, Amazon Q, Cursor, Windsurf, VSCode/Copilot, Zed, OpenCode, Goose, Sourcegraph Amp, Charm Crush, Cline, Qodo Gen, Kilo Code, Kiro, OpenAI Codex, Warp, Gemini CLI, JetBrains Junie

## H. Performance (spectrum)
performance: Unknown — no startup-latency, disk-footprint, RAM-overhead, or IO-throughput numbers found in official docs or the Dagger blog post announcing the tool; only an unquantified marketing claim ("intelligent caching ensures that common operations are fast, even across dozens of parallel agent environments"). No benchmark data of any kind (vendor or third-party) was located.
Sources:
- https://dagger.io/blog/agent-container-use/ — "intelligent caching ensures that common operations are fast, even across dozens of parallel agent environments" (no numbers given)

## I. Feasibility (spectrum)
feasibility: Adoptable-today-with-caveats — macOS (Homebrew) and a universal Linux/other shell installer are supported today; Windows support is explicitly tracked as in-progress work (open GitHub issue "Add Windows deployment support"), and a separate open issue asks how to port-forward under WSL, suggesting native Windows is not yet smooth. Prerequisites (Docker + Git) are realistic for a target audience of developers already using containers. Project explicitly self-describes as "early development and actively evolving" (pre-1.0, v0.4.x latest seen), which is a real stability/API-churn risk for adoption. No lock-in beyond git branches/Docker, both portable.
Sources:
- https://github.com/dagger/container-use — open issue "Add Windows deployment support" (#252, status open/in-progress); open issue "How to port forward while running in WSL?" (#36)
- https://raw.githubusercontent.com/dagger/container-use/main/README.md — project described as in "early development and actively evolving"

## J. Price (prose-only)
Fully open source (Apache 2.0), no pricing tiers, no managed/hosted SaaS offering found in any fetched doc — the tool itself runs on infrastructure the user already controls (their own Docker host, or their own CI runner). No free-tier-vs-paid-tier distinction exists because there is no paid product surfaced in the docs.
Sources:
- https://github.com/dagger/container-use — Apache 2.0 license, no pricing page/mention found

## K. Extensibility
extensibility: Partial — environments are configured as code (base image swap, setup/install command hooks, env vars, secret references) rather than static fixed images, giving real per-project customization, and downloadable "agent rule files" let teams tailor per-agent guidance. However there is no documented plugin/extension API, no custom-harness-definition system beyond "point any MCP client at `container-use stdio`," and (per axis C/F above) no hook into network policy or resource limits to extend.
Sources:
- https://container-use.com/environment-configuration.md — "Agent environments are defined with code (not static images) for dynamic composition" (paraphrase of documented base-image/setup/install-command model)
- https://container-use.com/agent-integrations.md — "Most agents support optional agent rule files to guide behavior, downloadable from the Container Use GitHub repository"

## Unknowns & caveats
- **Network/firewall subsystem**: concluded "No" (not "Unknown") based on the CLI reference explicitly billing itself as the "Complete reference for all Container Use CLI commands and options" (zero network-related entries) plus the environment-configuration doc's explicit confirmation, when directly queried, that network config is outside its documented schema. This is a stronger basis than mere silence, but it remains possible an undocumented/experimental flag exists that official docs simply haven't listed yet.
- **shared_dirs / nested_containers / browser_auth**: genuinely Unknown — official docs are silent and no authoritative secondary source was found; a single third-party blog post hints at extra mount behavior for `shared_dirs` but was not independently verified and is flagged low-confidence.
- **Workspace filesystem mechanics**: no official source explicitly states whether the container's filesystem is a copy-on-write layer seeded from a git worktree, or something else at the OCI layer — inferred as "not a live bind mount" from the explicit checkout/merge/apply review workflow, but the exact plumbing (worktree directory bind-mounted read-only vs fully copied into the image) is not confirmed.
- **Current star count / release version**: the GitHub page fetch appears to reflect an Aug 2025-era snapshot (v0.4.2, 3.9k stars); could not independently re-verify a fresher count within this session — noted as approximate/stale in the maturity section.
- **docs.dagger.io/api/architecture**: returned HTTP 404 during research; not a blocked-egress case (page simply doesn't exist at that path), substituted with docs.dagger.io/faq/ and docs.dagger.io/reference/configuration/engine/ which covered the same ground with direct quotes.
- No URLs were blocked by network/firewall failures during this research session (firewall was bypassed per operational note; all fetch failures were ordinary 404s or auth-walls, not egress blocks).

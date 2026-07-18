# Sculptor (Imbue)
category: local
Desktop app (Electron + local backend server) for running multiple coding-agent sessions in parallel workspaces on your own machine | built on: git worktrees (default) — NOT containers/VMs by default | license: MIT, open source | maturity: explicitly "experimental research preview," actively developed by Imbue (AI research lab)

## A. Identity
### built_on (prose-only)
Default architecture is a **local backend server** (FastAPI/OpenAPI-generated contracts) + **Electron GUI** + an **agent runner** subprocess that supervises the Claude Code or Pi CLI, operating on a **git worktree** — an ordinary directory + branch on the user's real filesystem, sharing the host's git history. This is NOT a container, VM, or any OS-level sandbox. Critically, the project's own history doc states Sculptor previously used per-agent Docker containers and **reversed that decision**: "The project moved away from per-agent Docker containers, discovering that it was very powerful to allow agents to inspect each other's work. Instead, containerization now applies at the application level." An experimental, opt-in "container backend" exists that relocates the *entire* backend server (not per-agent) into a Docker container or a remote/SSH-reachable machine via a user-supplied launcher command; it is explicitly labeled experimental and is a deployment-relocation feature, not a per-agent sandbox.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/history.md — "The project moved away from per-agent Docker containers, discovering that it very powerful to allow agents to inspect each other's work. Instead, containerization now applies at the application level."
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/SPEC.md — "a workspace is a git worktree — it shares your repo's history but has its own branch"
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/requirements.md — "in-place mode (editing the real checkout) and the container/remote backend are the deliberate, opt-in exceptions"
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/experimental/container_backend.md — "the whole backend off-host: a user-supplied launcher command (e.g. Docker, SSH, a VM)"

### execution_locality
execution_locality: Local — "Sculptor runs on your computer, not Imbue's servers." The Electron app + backend server + agent worktrees all run on the user's own machine. An opt-in experimental "container/remote backend" lets the whole backend (not per-agent execution) be relocated to a user-supplied Docker host or remote machine, but this is a separate deployment the user manages, not a hosted product mode.
Code/credentials never leave the local machine in the default mode; the opt-in remote-backend mode is a self-managed exception, not a vendor-hosted service.
Sources:
- https://imbue.com/product/sculptor — "Sculptor runs on your computer, not Imbue's servers"
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/SPEC.md — "Sculptor is a server running on your machine, so `sculpt` automates the Sculptor you already have open"

### open_source (prose-only)
MIT licensed, source at github.com/imbue-ai/sculptor. README explicitly states "Sculptor is actively under development and should be treated as an experimental research preview." Self-hostable by nature (it already runs locally); the experimental remote-backend option additionally allows relocating the backend to a self-managed remote host.
Sources:
- https://github.com/imbue-ai/sculptor — license: MIT
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/README.md

### maturity (prose-only)
Public GitHub repo, language mix Python 57.7% / TypeScript 36.8% / SCSS 2.0% / HTML 1.8% / Shell 0.5%. Backed by Imbue, an AI research lab. Explicitly self-described as "experimental research preview," not GA. Star count/age not retrieved (not queryable via available tools in this pass).
Sources:
- https://github.com/imbue-ai/sculptor — language composition, MIT license
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/README.md — "experimental research preview"

## B. Threat protection
### host_fs_damage
host_fs_damage: No — default workspace is a git worktree, i.e. an ordinary directory on the real host filesystem; the agent "may run real shell commands there" directly on the host with no filesystem namespace, chroot, or container boundary. Protection is limited to git-level recoverability (discard the branch/file) and the fact that the *original* checkout is untouched until the user "Pull"s — it does not prevent writes to paths outside the repo, symlink traversal, or damage to the worktree's own files.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/requirements.md — "By default an agent works only inside its isolated workspace copy and MAY run real shell commands there; nothing is pushed to a remote and no PR is opened without an explicit user action"
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/workspaces.md — "Default Mode: Git Worktree... shares your repo's git history"

### credential_theft
credential_theft: No — in the default worktree mode the agent's shell is an ordinary host process running as the user, with no isolation boundary separating it from the user's real environment/keychain/files; env files (`~/.sculptor/.env`, per-repo `.sculptor/.env`) are injected directly into that process. Positive mitigation: secret values like Pi API keys are explicitly "never persisted to config" (read from env at runtime only) and Imbue states it does not store repos or train on code. The one documented case of credential *isolation* is the opposite of intended UX: in the opt-in experimental container-backend mode, the container cannot reach the host's Keychain-stored Claude Code credentials, forcing a separate manual login inside the container — an accidental byproduct of container boundary, not a designed mediation layer.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/requirements.md — "Pi API keys and similar secrets are read from the environment and never persisted to config"
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/experimental/container_backend.md — "Claude Code stores credentials in the system keychain... the container cannot access host credentials. Users must manually authenticate `claude` within the container environment"
- https://imbue.com/product/sculptor — "Your data never leaves your machine"

### data_exfiltration
data_exfiltration: No — no network egress restriction of any kind is documented anywhere in official sources. This is not mere page-silence: SPEC.md, requirements.md, settings.md, workspaces.md, agents.md, and the experimental container_backend.md were all checked specifically for network/firewall/egress/proxy/allowlist language and none exists. Architecturally, the default agent shell is an ordinary host process (full host network access); the opt-in experimental container-backend mode is described only as a standard container with git/CLI tooling and port binding for the backend's own API — no firewall, proxy, or DNS control is mentioned for traffic originating from inside it either.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/settings.md — settings enumerated (extensions, harness deps, env-var overrides) with no network/firewall section
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/requirements.md — no network/egress requirement present among enumerated REQs
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/experimental/container_backend.md — describes only git/CLI tooling and a bound API port, no egress control

### malicious_execution
malicious_execution: No — no execution sandbox or blast-radius containment beyond git recoverability and human review gating. The documented safety model is procedural, not technical: "you remain the gatekeeper for anything that reaches the outside world," i.e. nothing is pushed/PR'd without explicit user action, but the shell commands themselves run unconfined on the host during the session.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/SPEC.md — "agents run real shell commands inside their workspace...while you remain the gatekeeper for anything that reaches the outside world"

### escape_resistance
escape_resistance: No isolation boundary exists to escape in the default mode — the "workspace" is a directory + git branch on the same kernel, same user account as the host session, not a container/microVM/syscall filter. This is weaker than a shared-kernel container, not comparable to one. The opt-in experimental container-backend relocates the whole backend into what the docs describe only as "Docker or a remote machine" with no further hardening details (no mention of gVisor, seccomp profile, rootless mode, or microVM); default Docker/runc-level isolation would be the ceiling, and it isn't even the default execution mode.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/history.md — confirms per-agent containers were removed in favor of shared-worktree "application level" containerization
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/experimental/container_backend.md — "deployment in Docker or on a remote machine via a custom backend command" with no hardening detail

### resource_abuse
resource_abuse: No — no CPU/memory/disk resource limits are documented anywhere in settings, SPEC, or requirements docs for agent execution.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/requirements.md — enumerated requirements contain no resource-limit REQ
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/settings.md — no resource-limit setting present

## C. Feature set & granularity
### network_default_posture
network_default_posture: No allowlist mode exists — default posture is fully open. The default execution unit is a host shell process (git worktree), which by definition has the same unrestricted outbound network access as the user's own machine; no deny-by-default or opt-in-restriction mode is documented for any execution mode.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/workspaces.md — "Default Mode: Git Worktree"
- (absence across all fetched official docs of any network-restriction feature — see data_exfiltration sources)

### egress_allowlist
egress_allowlist: No — not offered in any mode. No domain/IP/CIDR/port allow-deny feature is documented in settings.md, SPEC.md, requirements.md, or the experimental container-backend doc.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/settings.md — full settings tour with no network-rules section

### dns_level_blocking
dns_level_blocking: No — no DNS-layer control of any kind is mentioned anywhere in official docs.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/requirements.md (silent on DNS)

### tls_mitm_inspection
tls_mitm_inspection: No — no TLS interception/inspection capability is documented.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/SPEC.md (silent on TLS/network layer entirely)

### http_path_rules
http_path_rules: No — no HTTP path/method-level rule feature documented; no such control surface exists at all.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/settings.md (silent)

### proto_coverage
proto_coverage: NA — there is no network-control feature to have protocol coverage of; the criterion presumes an existing rule engine, which Sculptor does not have in any documented mode.
No sources beyond the absence already documented under data_exfiltration/egress_allowlist.

### live_rule_reload
live_rule_reload: NA — no rule engine exists to reload.

### firewall_escape_hatch
firewall_escape_hatch: NA — no firewall exists to bypass; the "gatekeeper" model is a human approving pushes/PRs, not a network control with a break-glass mode.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/SPEC.md — "you remain the gatekeeper for anything that reaches the outside world" (describes push/PR gating, not network egress gating)

### enforcement_plane
enforcement_plane: No — none exists. In the default mode the agent's outbound traffic is simply the host OS's normal networking stack, unmediated by any proxy, eBPF program, or VM boundary; nothing logs or filters it at a distinct enforcement layer.
Sources: same absence as above (settings.md, requirements.md, SPEC.md).

### fail_closed
fail_closed: NA — there is no network enforcement mechanism whose failure mode could be evaluated.

### network_audit
network_audit: No — no per-request egress log is documented. The only logging/telemetry mentioned is opt-out, anonymized product-usage and error-reporting (Sentry) with content masking (file names, branch names, prompts kept private) — this is product analytics, not a security/network audit trail.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/requirements.md — "when it's on...high-level, anonymized product-usage and error-reporting signal"

### workspace_modes
workspace_modes: Partial — three git-level modes exist: **Worktree** (default; new branch sharing repo history), **Clone** (experimental; fully independent repo copy with separate history/mirrored remotes, isolated until explicitly pushed), and **In-place** (experimental; agent edits the real checkout directly, no isolation). All three operate on the live host filesystem — none is a container/VM snapshot or an ephemeral throwaway environment; "changes stay local until Pull" is a git-mediated one-way sync, not a bind-mount-vs-copy container distinction.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/workspaces.md — describes Worktree (default), Clone, In-place modes

### observability
observability: Partial — rich UI-level visibility (per-agent chat/task history, live diff/Changes panel, PR status with review/CI state) and session persistence (SQLite-backed), but no dedicated security/audit dashboard; the only system-level telemetry is opt-out anonymized product analytics + Sentry error reporting, not an operational monitoring stack.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/changes.md — diff review UI
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/requirements.md — telemetry consent flags

### supervision
supervision: No — no separate supervisor/control-plane process observes and can intervene on agent behavior. The documented safety mechanism is a human reviewing diffs and manually approving commit/push/PR actions ("you remain the gatekeeper"), not software that can contain or kill a misbehaving agent process beyond the user closing it themselves.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/SPEC.md — "you remain the gatekeeper for anything that reaches the outside world"

### fleet_mgmt
fleet_mgmt: Yes — single-machine fleet management: a workspace list UI surfaces every active agent from one surface; `sculpt` CLI is workspace/agent-scoped (`--workspace/-w`, `SCULPT_WORKSPACE_ID`, `SCULPT_AGENT_ID`); branch naming convention `<user>/<slug>`. Scope is local-machine-only (one backend, one user), not a multi-host registry.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/SPEC.md — `sculpt` CLI surface and workspace/agent env vars
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/agents.md — "running multiple agents in parallel within one workspace"

### snapshots_persistence
snapshots_persistence: Partial — local persistence via SQLite + Alembic-migrated state and an append-only snapshot log; "Saves every agent session with its plans, chats, tool calls, and code changes"; workspaces persist under `~/.sculptor/workspaces/` across app restarts. There is no per-agent container/VM pause-resume concept (since there's no per-agent container by default) — persistence is of session/chat/task state and the worktree's git state, not of a running execution environment snapshot.
Sources:
- https://imbue.com/sculptor/ — "Saves every agent session with its plans, chats, tool calls, and code changes"
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/SPEC.md — "Local persistence: Append-only snapshot log + materialized state; versioned migrations"

## D. Setup
### setup
setup: Easy — install desktop app (Mac Apple Silicon or Linux only), then a 3-step first-run wizard: enter name/email, verify Claude CLI + git are installed (optionally install the Pi harness), connect first repo. No Docker install is required for the default (non-container) execution mode, which is a real point in Sculptor's favor for onboarding friction versus container-based competitors — at the cost of the isolation that Docker would have provided.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/getting_started.md — "verify Claude CLI and git are installed and ready," 3-step wizard

## E. Daily use
### daily_use
daily_use: Moderate — starting/attaching agents is lightweight (spin up from the workspace UI), but multiple agents sharing one workspace's files have **no locking**: "agents placed in the same workspace share the same files with no locking between them," and the docs explicitly instruct users to keep concurrent agents on separate concerns or use separate workspaces to avoid conflicting edits — a manual, ongoing daily-use burden.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/SPEC.md — "agents placed in the same workspace share the same files with no locking between them"
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/agents.md — "All agents in a workspace share the same working copy"

## F. Configuration
### config_depth
config_depth: Partial — configurable surface includes env files (global `~/.sculptor/.env`, per-repo `.sculptor/.env`, project overrides global), per-repo setup commands (e.g. `npm install`) run at workspace creation, branch naming/target/cleanup policy, and per-extension settings — but there is no single declarative, versionable project manifest covering image/packages/network-rules/mounts/lifecycle hooks together (no analog to a firewall or mount config section was found anywhere).
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/workspaces.md — setup commands, env file locations, branch policy
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/settings.md — settings tour (harness deps, extensions, env overrides)

### policy_model
policy_model: Rigid on security, moderate on git-isolation choice — the three workspace modes (worktree/clone/in-place) are a genuine per-workspace dial for how much git isolation you want, including the "in-place" escape hatch to opt out of isolation entirely. But there is no equivalent dial for security/network posture: since no network or filesystem-sandboxing control exists at all in any mode, there is nothing to tighten or loosen — the "policy" axis simply isn't present for network/exec security, only for git workflow shape.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/workspaces.md — three modes described

## G. DX — host↔sandbox integration
### bind_mount_sharing
bind_mount_sharing: Partial — the workspace's own worktree directory (`~/.sculptor/workspaces/<id>/code/`) is a live directory on the real host filesystem (fully bind-mount-equivalent from the moment of creation — visible to any host tool, IDE, or terminal), but it is a *separate* worktree from the user's original checkout: changes only reach the original branch/repo when the user explicitly "Pull"s. So sharing is live within the agent's own worktree, one-way and git-mediated back to the original repo — not a bidirectional bind mount of the user's actual working directory.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/workspaces.md — "Workspaces are stored in `~/.sculptor/workspaces/`, with each workspace's working copy located at a `code/` subdirectory"
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/README.md — "Your current branch and uncommitted work are never touched"

### cred_forwarding
cred_forwarding: Partial — GitHub auth goes through `gh auth login` (GitHub CLI, typically browser/device-flow OAuth); Claude CLI auth is either a managed install or a user-supplied custom binary path; Pi/model API keys are read from `.env` files into the agent's env. No ssh-agent or GPG-specific forwarding/mediation is documented (silence — treated as unknown for those two, not asserted as unsupported).
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/pull_requests.md — "Sculptor uses the GitHub CLI (`gh`) under the hood. The first time, you may need to run `gh auth login`"
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/SPEC.md — "Credentials and keys the app needs to do its work are handled locally on your behalf"

### browser_auth
browser_auth: Yes — GitHub authentication uses `gh auth login`, which drives the standard GitHub CLI OAuth/device-code flow (browser opens on host, user authorizes, control returns to the CLI). Documented gap in the opt-in experimental container-backend mode only: Claude Code's Keychain-stored credentials aren't reachable there, requiring a separate manual `claude` login inside that container.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/pull_requests.md — "you may need to run `gh auth login` in a terminal"
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/experimental/container_backend.md — "Users must manually authenticate `claude` within the container environment"

### shared_dirs
shared_dirs: Unknown — no documentation found describing mounting/sharing additional host directories beyond the workspace's own worktree.

### git_worktrees
git_worktrees: Yes — first-class and central to the product: "workspace" *is* a git worktree by default, with dedicated docs (`workspaces.md`) covering branch naming, target-branch, and cleanup policy.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/workspaces.md

### nested_containers
nested_containers: Unknown — no mention of Docker-socket access, DinD, or any nested-container capability inside a workspace/terminal in the fetched terminal, workspaces, or settings docs.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/terminal.md — describes shell scoped to workspace files only, no container detail either way

### harness_agnostic
harness_agnostic: Partial — two harnesses are documented with real integration depth: **Claude Code** (full feature parity — streaming JSON control protocol, tool substitution, system-prompt additions, plugin bundling, compaction hooks, context reporting) and **Pi** (community-supported, RPC mode, missing fast-mode and context-percentage reporting). The GitHub repo's top-level description separately claims support for "any terminal-based agents," which is broader than what the detailed harness doc substantiates — flagged as a tension between marketing/README framing and the documented integration surface.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/integrated_harnesses.md — Claude Code and Pi feature matrix
- https://github.com/imbue-ai/sculptor — README description mentioning "any terminal-based agents" (broader, less detailed claim)

## H. Performance
### performance
performance: Unknown — no startup-latency, disk-footprint, RAM-overhead, or IO-throughput figures (vendor or third-party) were found in any fetched official source. Not estimated.

## I. Feasibility
### feasibility
feasibility: Adoptable today, with caveats — macOS (Apple Silicon only, no Intel yet) and Linux x64 (Linux arm64 best-effort); no Windows. No Docker prerequisite for the default mode. Explicitly self-labeled "experimental research preview" (stability/maturity risk). Free, open source (MIT), local-only by default, so lock-in risk is low. The absence of any network isolation is a functional gap versus container-first competitors for teams that need it, independent of adoption ease.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/specs/requirements.md — "Supported targets are macOS (arm64 only) and Linux x64 (with Linux arm64 as best-effort)"
- https://imbue.com/sculptor/ — Mac Intel and Windows listed as "Coming Soon"

## J. Price (prose-only)
Free and open source (MIT); "No subscription required beyond your model provider" — users bring their own Claude subscription/API key (or another model via Pi). Imbue does not resell model tokens. No paid tier, seat pricing, or enterprise plan documented.
Sources:
- https://imbue.com/product/sculptor — "Free & open source... No subscription required beyond your model provider"

## K. Extensibility
### extensibility
extensibility: Yes — several documented extension points: (1) JS-based UI **extensions** (panels, workspace widgets, home views, overlays, per-extension settings) loaded at runtime from a folder, a URL, or via `sculpt extension load`, each isolated behind an error boundary; (2) bundled **skill bundles** (`sculptor-workflow` six-stage pipeline, `sculptor-experimental`) plus auto-discovery of the user's own Claude Code skills/commands (`~/.claude/skills/`, `~/.claude/commands/`, repo `.claude/`); (3) the `sculpt` CLI itself, generated from the backend's OpenAPI schema; (4) the experimental custom-backend launcher for arbitrary Docker/SSH/VM relocation of the whole backend. Caveats: no custom Dockerfile support yet (listed as "Coming Soon" on the marketing roadmap, implying the underlying execution environment is currently fixed/non-customizable), and no MCP (Model Context Protocol) tool-server integration was found in chat, extensions, or harness docs.
Sources:
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/extensions.md — extension types, isolation, install methods
- https://raw.githubusercontent.com/imbue-ai/sculptor/main/docs/help/skills.md — bundled skill bundles, custom skill discovery
- https://imbue.com/sculptor/ — roadmap: "custom MCPs, custom Dockerfiles" listed as upcoming (not yet shipped)

## Unknowns & caveats
- **docs.imbue.com RESOLVED (maintainer verification 2026-07-18):** not a firewall block — `docs.imbue.com/` returns 302 → `github.com/imbue-ai/sculptor`, and subpaths 404. The standalone docs site has been retired into the GitHub repo's `docs/` tree — exactly the source this writeup used. WebSearch's cached `docs.imbue.com/*` snippets are stale remnants of the old site. No hidden docs exist; the "does docs.imbue.com document a network feature we couldn't see" uncertainty is closed (it documents nothing — it redirects).
- **Direct contradiction in official sources on container isolation.** `imbue.com/sculptor` (marketing) states "Every agent runs in its own container... Containers keep the local machine safe compared to git worktrees." This is contradicted by: `imbue.com/product/sculptor` ("Each workspace is an isolated worktree"), the GitHub repo's `docs/specs/SPEC.md`, `docs/specs/requirements.md`, `docs/help/workspaces.md`, `docs/help/agents.md`, `docs/help/README.md` (all describing git-worktree as the default with container/remote backend as an opt-in experimental exception), and decisively `docs/history.md`, which documents an explicit architectural reversal away from per-agent Docker containers. This writeup weights the GitHub docs tree over the marketing page because it is more detailed, internally consistent across ~8 independent pages, and directly explains *why* the marketing claim is now inaccurate (a superseded design). Treat the marketing copy as stale rather than as evidence of current per-agent container isolation.
- **Network/egress control: no evidence found of any restriction in any mode**, default or opt-in-experimental-container. This was cross-checked across SPEC.md, requirements.md, settings.md, workspaces.md, agents.md, and container_backend.md specifically for this purpose (not a single-page silence).
- WebSearch quota was exhausted partway through this research pass (session budget reached), which capped supplementary searches for third-party corroboration/independent star-count-and-age data; all determinations above rely on official sources actually fetched.
- `shared_dirs`, `nested_containers`, ssh-agent/GPG forwarding specifics, and startup/resource performance numbers are genuinely undocumented gaps (Unknown), not inferred absences.

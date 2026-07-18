# Nono
category: local
OS-level least-privilege sandbox for running vendored coding-agent CLIs, with a supervisor process + credential proxy | built on Landlock (Linux) / Seatbelt (macOS) / WSL2-seccomp (Windows), no containers/VMs | Apache-2.0 | by the Sigstore/nolabs-ai team; multi-agent registry (registry.nono.sh); as-of 2026-07-18

Litmus: passes — a dev runs `nono run --profile X -- claude` and gets a contained agent; protects a prompt-injected/mistaken coding agent (fs scope, credential proxy, egress allowlist, runtime supervisor). Not an SDK (though FFI bindings exist, the primary product is the `nono` CLI).

## A. Identity
### built_on (prose-only)
OS-level sandboxing, no containers or VMs. Linux = Landlock LSM (ABI v4+) as the irreversible enforcement floor + seccomp user-notification (BPF traps `openat`/`openat2`, routes to supervisor for fd-injection decisions). macOS = Seatbelt (`sandbox_init()`). Windows = WSL2 with seccomp errno (`--block-net`, per-port filtering unavailable). Architecture: in supervised mode nono forks BEFORE sandboxing; the child is sandboxed and runs the agent, the parent stays unsandboxed to provide "audit logging, capability decisions, proxy mediation, and optional rollback snapshots." A direct mode (`nono wrap`) applies the sandbox then `exec()`s with no supervisor (fewer features).
Sources:
- https://raw.githubusercontent.com/nolabs-ai/nono/main/docs/cli/internals/security-model.mdx — "Landlock ... permanently restricted to its initial capability set. No API exists to expand or remove the restrictions"
- https://raw.githubusercontent.com/nolabs-ai/nono/main/docs/cli/features/supervisor.mdx — "The parent remains unsandboxed to provide supervisor services such as audit logging, capability decisions, proxy mediation, and optional rollback snapshots"

### execution_locality
Local — agent runs directly on the user's own machine via OS sandbox primitives; no cloud, no remote execution. "nono runs in your existing environment - it doesn't provide a separate runtime." Project code and credentials never leave the machine; the credential proxy holds secrets locally outside the sandboxed child.
Sources:
- https://raw.githubusercontent.com/nolabs-ai/nono/main/docs/cli/internals/containers.mdx — "nono runs in your existing environment - it doesn't provide a separate runtime"

### open_source (prose-only)
Apache-2.0. Fully self-hostable/local by nature (no vendor cloud dependency for execution). A hosted registry (registry.nono.sh) distributes packs/profiles but is not required to run.
Sources:
- https://github.com/nolabs-ai/nono — "License: Apache-2.0"

### maturity (prose-only)
Built by the Sigstore team (supply-chain heritage: pack signing, cryptographic audit). Broad agent support (Claude Code, Codex, opencode, openclaw, Goose, Copilot, Qwen Code, Pi, Hermes). Star count / adoption not captured in this pass.

## B. Threat protection
### host_fs_damage
Yes — agent gets read/write to the current directory and nothing else, enforced by Landlock as an irreversible floor; supervisor injects fds after canonical-path validation with `O_NOFOLLOW`, never passing `O_CREAT`/`O_TRUNC`. Escape "only possible through a kernel exploit."
Sources:
- security-model.mdx — "read/write access to the current directory and nothing else"; "walks the canonical path component-by-component using openat with O_NOFOLLOW"

### credential_theft
Yes — host secrets are NOT copied in; the agent receives phantom tokens (`nono_sess_...`) and real keys are injected by a local reverse proxy on the fly. "The credential never enters the sandbox, not even as an environment variable." SSH keys / cloud creds outside cwd are unreachable.
Sources:
- https://raw.githubusercontent.com/nolabs-ai/nono/main/docs/cli/features/credential-injection.mdx — "The agent never sees the API key. Even if the agent is compromised, it cannot extract credentials"

### data_exfiltration
Partial — egress CAN be restricted (allowlist via proxy) but is OPEN by default. "By default the sandboxed process has unrestricted network access." Exfiltration is contained only once a profile sets `network.block` or `--allow-domain`.
Sources:
- https://raw.githubusercontent.com/nolabs-ai/nono/main/docs/cli/features/networking.mdx — "By default the sandboxed process has unrestricted network access"

### malicious_execution
Yes — tool sandboxing: delegated tools (git/gh/curl/kubectl) run in separate child sandboxes with their own fs/network/credential policies, "outside the agent's control." Dangerous-command blocking and per-command policies further constrain blast radius.
Sources:
- security-model.mdx / tool-sandbox — "nono can put delegated tools in their own isolated child sandboxes, outside the agent's control"

### escape_resistance
Determination: isolation boundary stronger than plain process (shared-kernel, no VM). Landlock is irreversible and inherited by children; escape requires a kernel exploit. Weaker than a microVM (shared kernel) but stronger than seccomp-only. macOS relies on Seatbelt; WSL2 is the weakest tier (all-or-nothing `--block-net`).
Sources:
- security-model.mdx — "escape only possible through a kernel exploit"

### resource_abuse
Yes (documented feature: cli/features/resource-limits) — resource limits exist; specifics not extracted in this pass.
Sources:
- docs.json nav lists cli/features/resource-limits

## C. Feature set & granularity
### network_default_posture
Open-by-default — "By default the sandboxed process has unrestricted network access." Restriction is opt-in via `network.block` / `--allow-domain`. NOT deny-by-default.
Sources:
- networking.mdx — "By default the sandboxed process has unrestricted network access"

### egress_allowlist
Yes — `--allow-domain` / profile `network.allow_domain`. Granularity: domain (bare hostname = all paths), subdomain wildcard suffix (`*.internal.example`), HTTP path globs (`https://github.com/org/**`, `/repos/*/issues/**`), and per-route method gating (`openai:POST:/v1/chat/completions`, `*` = any verb). No user-authorable IP/CIDR allow rules; no port-scoped egress rules (egress is funneled through the proxy). Path matching is glob, not regex.
Sources:
- networking.mdx — "When --allow-domain is given a URL with a path ... only requests matching that path pattern are allowed"; "*.internal.example ... suffix matching"

### dns_level_blocking
No — unlisted domains are denied at the proxy (403 Forbidden), not at the DNS tier. The proxy performs its own DNS resolution (with rebinding protection) but does not return NXDOMAIN to the agent.
Sources:
- networking.mdx — "Denied requests return 403 Forbidden"; "The proxy resolves DNS itself and checks all resolved IPs"

### domain_native_enforcement
Yes (hostname at request time) — a supervisor-side HTTP forward proxy matches the request hostname and resolves DNS itself per request (dynamic-forward-proxy style), not a resolve-once iptables IP snapshot. Kernel rules only pin `connect()` to the proxy port; hostname policy lives in the proxy.
Sources:
- networking.mdx / security-model.mdx — "Traffic flows through an HTTP proxy in the supervisor"; "The proxy resolves DNS itself"

### tls_mitm_inspection
Yes (selective) — TLS is end-to-end CONNECT-tunneled by default (proxy never sees plaintext), but interception activates "when a route requires layer-7 visibility — for example an endpoint-scoped --allow-domain" with path globs/endpoint rules.
Sources:
- networking.mdx — "Interception activates only when a route requires layer-7 visibility"

### http_path_rules
Yes — per-path allow via globs (`**`, `*`) plus HTTP method gating on credential-injected routes. No regex.
Sources:
- networking.mdx — "openai:POST:/v1/chat/completions allows only POST"; "/repos/*/issues/**"

### proto_coverage
TCP is the controlled dimension: all outbound TCP blocked at the kernel except the proxy port, and the proxy applies HTTP(S) allowlist/L7 rules. UDP, QUIC/HTTP3, DNS (as agent egress), ICMP, SSH, WebSocket are NOT documented as policy dimensions — the model is HTTP-proxy-centric. Fixed non-overridable deny floor blocks cloud-metadata / link-local CIDRs regardless of config. No documented protocol-extensibility model.
Sources:
- networking.mdx — "all other outbound TCP is blocked at the kernel level"; "always blocked ... cannot be overridden — including 169.254.169.254"

### live_rule_reload
Unknown — not documented. Rules are set per-run via flags/profile.

### firewall_escape_hatch
No — "No escape hatch for denied destinations." No timed bypass with auto re-enforcement; restriction is chosen per-run (all-or-nothing per invocation).
Sources:
- networking.mdx — "No escape hatch for denied destinations"

### enforcement_plane
Kernel-anchored + userspace proxy. Kernel (Landlock v4 per-port TCP on Linux; Seatbelt `remote tcp "localhost:PORT"` on macOS) restricts `connect()` to only the proxy port — the agent cannot open a direct socket around the proxy. A 256-bit session token (constant-time compared) stops other localhost processes from using the proxy. WSL2 is all-or-nothing (`--block-net`). The agent cannot route around the enforcement point.
Sources:
- security-model.mdx — "all other outbound TCP is blocked at the kernel level"; "256-bit random session token"

### fail_closed
Yes — "Proxy crashes — Child loses all network access (only proxy port was allowed)." "Supervisor crashes — pending syscalls get ENOSYS; child falls through to Landlock" (deny). Invariant: "if anything goes wrong, the child does not get access." Network policy fails to no-egress if the control plane dies.
Sources:
- security-model.mdx — "if anything goes wrong, the child does not get access"

### network_audit
Yes — "All proxy decisions are logged via tracing" with ALLOW/DENY and reason (e.g. `DENY CONNECT 169.254.169.254:80 reason=denied_cidr`), plus a cryptographic tamper-evident audit trail.
Sources:
- networking.mdx — "All proxy decisions are logged"; landing page — "Cryptographic, tamper-evident audit trail"

### workspace_modes
Single model: in-place live host directory (agent gets read/write to cwd; edits land on the real host files — no container, no copy). No ephemeral snapshot/copy workspace mode. Atomic rollback snapshots (before/after, content-addressable SHA-256 + Merkle) let you review/undo changes afterward, but changes still occur in place.
Sources:
- atomic-rollbacks.mdx — "snapshots before and after ... restore any modified or deleted files"; "This is an undo/rollback mechanism for the live directory, not an ephemeral sandbox copy"

### observability
Passive: cryptographic audit log + `tracing` decision logs. No metrics dashboard / OTel metrics pipeline documented.
Sources:
- audit.mdx (nav) / networking.mdx — proxy decision logging

### supervision
Yes — an external, unsandboxed parent "Runtime Supervisor" observes the running agent via seccomp-notify syscall interception + proxy mediation and dynamically GRANTS or DENIES operations at runtime (file access outside allowed paths, rate-limited requests "automatically denied"). Decisions via a pluggable `ApprovalBackend` (default = user prompt; planned webhook/policy backends). It contains individual harmful actions at the OS boundary; docs do NOT describe a whole-agent quarantine/kill-on-behavior command.
Sources:
- supervisor.mdx — "the supervisor intercepts the request and can grant access on the fly"; "requests exceeding the rate limit are automatically denied"

### fleet_mgmt
No — the registry manages profiles/packs, not a multi-agent fleet (no agent naming/hierarchy/grouping/lifecycle registry).
Sources:
- managing-packs.mdx — packs are "signed bundles of profiles, hooks, plugins" (profile distribution, not agent fleet)

### snapshots_persistence
Partial — content-addressable filesystem snapshots for undo/rollback exist, but per-agent persistent state (config/shell history surviving sandbox recreation) is not a documented managed feature (state simply lives on the host since execution is in-place).
Sources:
- atomic-rollbacks.mdx — "Files are stored by their SHA-256 hash ... Merkle tree"

## D. Setup
setup: Trivial — "nono requires no setup - just install and run"; no Docker/daemon/VM/account. Comparison table lists nono setup = "None" vs Docker "Dockerfile, daemon."
Sources:
- containers.mdx — "nono requires no setup - just install and run"

## E. Daily use
daily_use: Easy — `nono run --profile <name> -- <agent>`; profiles compose via `extends`; packs pulled from registry. No image rebuilds (no images). Restriction is opt-in per run.
Sources:
- README / profile-authoring.mdx

## F. Configuration
### config_depth
Deep, declarative — JSONC profiles (per-project, versionable, `extends` inheritance up to 10 levels, profile groups). Sections: `filesystem` (allow/read/write/bypass_protection), `network` (block/allow_domain/credentials/open_port/network_profile), `credentials`/`env_credentials`, `environment.set_vars`/`deny_vars`, `security` (signal/process_info/ipc modes), `groups`, `commands.allow/deny`, `hooks` (key-value map). No custom image/packages (OS-level, no images).
Sources:
- profile-authoring.mdx — "declarative, versionable per-project config file[s]" using JSONC; section list

### policy_model
Fully-policy-driven on fs/credentials/commands with secure defaults (fs deny-all-but-cwd, credential phantoming) and granular per-case overrides via profiles/groups. Weak spot: network defaults OPEN (opt-in restriction) and no per-run firewall break-glass. Mixed overall.
Sources:
- networking.mdx (open default) + profile-authoring.mdx (granular policy sections)

## G. DX
### bind_mount_sharing
Yes (in-place, live) — the agent operates directly on the host cwd with read/write granted in place; edits are the real host files (live both ways) by design, since there is no container/copy layer.
Sources:
- security-model.mdx — "read/write access to the current directory"; containers.mdx — "runs in your existing environment"

### cred_forwarding
Partial — API/OAuth credentials are mediated via the reverse proxy (secrets outside the sandbox, phantom token inside; GitHub/GitLab/OpenAI/Anthropic/Gemini/custom). But SSH-agent and GPG forwarding are NOT provided, and "Git authentication via SSH or credential helpers operates outside nono's proxy model."
Sources:
- credential-injection.mdx — "Not mentioned as proxied: SSH keys, GPG keys, or git credentials ... operates outside nono's proxy model"

### browser_auth
Yes — sandboxed-OAuth flow: `--allow-launch-services` lets the sandboxed agent trigger a host browser open; the proxy intercepts the OAuth token exchange, stores the real token outside the sandbox, and hands the agent a phantom token. Seamless open-approve-done (not copy-paste). Dedicated docs page + manual-QA harness.
Sources:
- sandboxed-oauth-logins.mdx — "nono run ... --allow-launch-services -- claude auth login"; "proxy intercepts OAuth responses, replaces real tokens with phantom values"

### shared_dirs
Yes — profile `filesystem` allow/read/write grants arbitrary additional host paths beyond cwd.
Sources:
- profile-authoring.mdx — filesystem section (allow/read/write)

### git_worktrees
No — no product-level git-worktree management documented (git runs as a delegated tool).

### nested_containers
No — nono itself uses no containers; a docker-socket/DinD nesting feature is not documented (it is a container-free model).
Sources:
- containers.mdx — "doesn't provide a separate runtime"

### harness_agnostic
Yes — runs many vendored agent CLIs (Claude Code, Codex, opencode, openclaw, Goose, Copilot, Qwen Code, Pi, Hermes) selected via profiles; not tied to one vendor.
Sources:
- README / registry — agent list

## H. Performance
performance: Lightweight (vendor framing) — "zero setup, zero latency," no daemon/container/VM startup; in-place fs (no bind-mount translation, no image pull). No independent benchmarks captured.
Sources:
- README — "zero setup, zero latency"

## I. Feasibility
feasibility: Adoptable today — macOS, Linux, Windows(WSL2, degraded network tier). Prereq = install only. Shared-kernel isolation (not microVM) is the main risk ceiling; WSL2 is all-or-nothing on network.
Sources:
- security-model.mdx / wsl2 nav pages

## J. Price (prose-only)
Apache-2.0 OSS; runs locally free. Hosted pack registry for distribution; no paid-tier gating of core sandbox documented.

## K. Extensibility
extensibility: Packs — "signed bundles of profiles, hooks, plugins, and other artifacts distributed through the nono registry"; profile inheritance/composition; FFI bindings (Rust/Python/TS/Go). No custom container image concept.
Sources:
- managing-packs.mdx — "Packs are signed bundles of profiles, hooks, plugins"

## Unknowns & caveats
- live_rule_reload: docs silent (not confirmed either way).
- Exact syscall coverage on WSL2 (network per-port unavailable; `--block-net` only) — degraded tier.
- `hooks` field exists in profiles but hook TYPES (pre-run/post-init/setup) are explicitly "not detailed" in docs — cannot confirm lifecycle-command hooks.
- UDP/QUIC/ICMP/SSH/WS: not documented as controllable policy; on macOS Seatbelt "tcp only" may incidentally block UDP, but this is not exposed as a user policy dimension.
- Per-agent durable state / shell-history persistence across recreation: not a documented managed feature (execution is in-place on host).
- Metrics dashboard / OTel: not documented (audit + tracing logs only).
- git-over-HTTPS credential mediation: the proxy supports GitHub/GitLab API tokens, but docs explicitly say git SSH/credential-helper auth is OUTSIDE nono's model — so treat git credential forwarding as not a managed feature.

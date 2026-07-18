# Docker Sandboxes
category: local (with paid cloud-managed org governance layer)
Disposable microVM sandboxes for AI coding agents, run via the `sbx` CLI | built on lightweight microVMs (hypervisor unspecified in docs) with per-sandbox Linux kernel + Docker Engine | proprietary (closed-source binary, free to use) | active: 83 releases by v0.35.0 (2026-07-10), 240 GitHub stars on the releases repo, docker/sbx-releases has 209 open issues (285 total per activity), first-party Docker product

## A. Identity

### built_on (prose-only)
"Every sandbox runs inside a lightweight microVM with its own Linux kernel" — separate kernel per sandbox, not a shared-kernel container. Docs do not name the hypervisor (no confirmation of Firecracker/Cloud Hypervisor/other). Each sandbox additionally runs a private Docker Engine (its own daemon, image cache, package installs) fully isolated from the host daemon. Host-side companion process: an HTTP/HTTPS proxy on the host (reachable from inside the VM at `host.docker.internal:3128`) that enforces network policy and injects credentials; a host OS keychain stores secrets. `sbx login` triggers an org-level control plane (paid tier) for centralized governance.
Sources:
- https://docs.docker.com/ai/sandboxes/security/isolation/ — "Every sandbox runs inside a lightweight microVM with its own Linux kernel"
- https://docs.docker.com/ai/sandboxes/architecture/ — "its own Docker daemon state, image cache, and package installations"
- https://www.docker.com/blog/untrusted-autonomous-workload-ai-sandboxes/ — "Host-side proxy ... accessible from VM at host.docker.internal:3128"

### execution_locality
execution_locality: Local — the microVM runs on the developer's own machine (macOS/Windows/Linux host), not in Docker's cloud. Project workspace is bind-mounted (filesystem passthrough) directly from host disk at the same absolute path; code never leaves the machine except via explicitly allowed network egress. Credentials also stay host-side (proxy header-injection model — raw values never enter the VM). The paid "organization governance" feature is a cloud control plane for policy *distribution*, not a remote execution mode — it pushes policy to locally-running sandboxes rather than running the sandbox itself remotely.
Sources:
- https://docs.docker.com/ai/sandboxes/architecture/ — "Your workspace is mounted directly into the sandbox through a filesystem passthrough" / "mounted at the same absolute path as on your host"
- https://docs.docker.com/ai/sandboxes/security/ — "The host-side proxy injects authentication headers into outbound HTTP requests. The raw credential values never enter the VM."

### open_source (prose-only)
Not open source. `docker/sbx-releases` on GitHub is a binary-distribution/issues repo only (no source), stating "Proprietary — Docker Inc." Free to use (including commercially) as a binary; no self-host option for the sandbox runtime itself (it runs on the user's own machine already — "self-host" doesn't apply in the SaaS sense, but the underlying engine is closed-source).
Sources:
- https://github.com/docker/sbx-releases — "Proprietary — Docker Inc." (repo license reference); 240 stars, 209 open issues, latest v0.35.0 as of 2026-07-10

### maturity (prose-only)
First-party Docker product, actively developed (83 releases through v0.35.0 as of 2026-07-10, roughly weekly-to-biweekly cadence implied by version count). Backed by Docker Inc. (large-vendor backing, not a community project). GitHub releases-repo has 240 stars and an active issues tracker (209 open) used for both bug reports and feature requests (e.g., layer-7 policy). No independent adoption/usage numbers found in official sources.
Sources:
- https://github.com/docker/sbx-releases/releases — release history, v0.35.0 tag dated 2026-07-10

## B. Threat protection

### host_fs_damage
host_fs_damage: Yes — agent confined to a dedicated microVM with its own kernel; only the project workspace directory (plus any directories explicitly added) is shared with the host, everything else on the host filesystem is unreachable. Optional "clone mode" additionally makes the shared workspace read-only, with the agent working against a private in-VM clone.
2-4 sentences: Direct mode (default) still gives the agent read/write/delete on the whole workspace tree, so host_fs_damage protection is scoped to "outside the workspace," not "within it." A caveat the vendor documents itself: because the workspace is a real, absolute-path bind mount, agent writes to implicitly-executed files (git hooks, CI config, Makefile, package.json scripts) can affect the host indirectly once a human runs/commits them post-session — isolation ends at the workspace boundary.
Sources:
- https://docs.docker.com/ai/sandboxes/security/ — "the VM boundary prevents the agent from reaching anything on your host except what is explicitly shared"
- https://www.docker.com/blog/untrusted-autonomous-workload-ai-sandboxes/ — "Agent writes malicious post-commit hook... boundary ended at workspace" (vendor's own worked example)

### credential_theft
credential_theft: Yes — credentials are never placed inside the VM. Secrets live encrypted in the host OS keychain (macOS Keychain / Windows Credential Manager / Linux Secret Service or an encrypted-file fallback), and the host-side proxy injects the actual header value into outbound requests after matching them to a declared service; the sandbox only ever sees a sentinel/placeholder value. SSH agent forwarding follows the same never-expose-the-key model (agent can request signatures via the forwarded `SSH_AUTH_SOCK`, cannot read/copy the private key).
2-4 sentences: 11 built-in services (anthropic, openai, github, google, etc.) get this treatment automatically via `sbx secret`; custom services need `sbx secret set-custom` domain+env-var binding. Requests that bypass the forward proxy (the "transparent" or "forward-bypass" egress paths) do NOT get credential injection, so a misconfigured client can silently lose this protection. GPG signing forwarding is not documented (git creds beyond SSH not mentioned).
Sources:
- https://docs.docker.com/ai/sandboxes/security/credentials/ — "The real credential stays on the host; the sandbox sees only a sentinel value."
- https://docs.docker.com/ai/sandboxes/troubleshooting/ — "Requests routed through the transparent proxy don't get credential injection"

### data_exfiltration
data_exfiltration: Partial — default (Balanced) policy is deny-by-default at the HTTP/HTTPS proxy layer, and non-HTTP protocols (raw TCP/UDP/ICMP) are blocked outright, but the vendor's own documentation states the filtering is domain-level only, not content-aware, so any allowlisted domain (e.g. `github.com` for legitimate cloning) can also be used to exfiltrate via public gists, comments, etc. — "useful but not hermetic."
2-4 sentences: Domain fronting through allowlisted CDNs is explicitly called out as a residual gap ("proxy sees which domain a request claims to be going to; it cannot always prevent the request from being routed elsewhere through that allowed CDN"). "Open" policy mode (opt-in) disables egress restriction almost entirely. Vendor frames this correctly as reducing accidental/blast-radius exfiltration, not preventing a determined/compromised agent from shipping data to an already-allowed destination.
Sources:
- https://www.docker.com/blog/untrusted-autonomous-workload-ai-sandboxes/ — "allowing broad domains like github.com permits access to any content on that domain, and agents could use these as channels for data exfiltration"

### malicious_execution
malicious_execution: Yes — untrusted/hallucinated code, malicious packages, or compromised deps run inside the microVM's own kernel and private Docker Engine; blast radius is contained to that VM (own filesystem, own container runtime, no path to host daemon). Removing the sandbox discards everything installed/built inside it.
Sources:
- https://docs.docker.com/ai/sandboxes/ (WebSearch synthesis) — "Packages it installs, images it pulls, and containers it starts stay inside the sandbox... removing the sandbox discards them"
- https://docs.docker.com/ai/sandboxes/architecture/ — "each sandbox has its own Docker Engine with no path to the host daemon"

### escape_resistance
escape_resistance: Yes — isolation boundary is a hardware-virtualization microVM (separate guest kernel per sandbox), a materially stronger boundary than a shared-kernel container or syscall filter (gVisor/seccomp), though weaker in principle than physically air-gapped hardware. Docs explicitly contrast this with containers: "processes inside the VM are invisible to your host and to other sandboxes" because there is no shared kernel.
2-4 sentences: The specific hypervisor/VMM is not named in official docs (no Firecracker/Cloud Hypervisor confirmation found), so the exact escape-surface pedigree (e.g., known CVE history of the underlying VMM) can't be assessed from official sources. No documented resistance testing, red-team results, or CVE disclosures were found. The vendor's stated threat model is explicit that the boundary protects against *accidental* host damage and contains *consequences*, not that it's an impenetrable barrier against a determined attacker with full guest-root.
Sources:
- https://docs.docker.com/ai/sandboxes/security/isolation/ — "Every sandbox runs inside a lightweight microVM with its own Linux kernel... processes inside the VM are invisible to your host and to other sandboxes"

### resource_abuse
resource_abuse: Yes — `sbx run`/`sbx create` support `--cpus` (default: auto, N-1 host CPUs, min 1) and `-m/--memory` (binary units, e.g. `8g`; default 50% of host RAM, max 32 GiB) limits per sandbox; no swap, so a sandbox at its memory ceiling gets its processes OOM-killed rather than degrading the host.
2-4 sentences: No explicit per-sandbox disk-quota flag was found in the material retrieved (disk appears managed at the sandbox-lifecycle level, not as a runtime constraint flag). CLI reference pages for exact flag syntax repeatedly failed to render full content via direct fetch (JS-rendered doc site returned only headings); the flags above are corroborated via web search of the same docs.docker.com source but could not be quote-verified against raw page text — treat flag names as reasonably confident, not verbatim-quoted.
Sources:
- https://docs.docker.com/reference/cli/sbx/run/ (indirect — WebSearch summary of this page) — "`--cpus` ... 0 = auto: N-1 host CPUs, min 1"; "`-m, --memory` ... default of 50% of host memory, max 32 GiB"

## C. Feature set & granularity

### network_default_posture
network_default_posture: Partial — deny-by-default is the architecture and the recommended/pre-selected default, but the user explicitly chooses among three modes at first `sbx login` (or after `sbx policy reset`): **Open** (allow everything except private networks/localhost/link-local/cloud metadata), **Balanced** (deny-by-default with a pre-populated allowlist of common dev domains — described as "a good starting point"), or **Locked Down** (deny-by-default, explicit allowlist only, strictest). So an "unconfigured" sandbox's actual posture depends on the choice made at login, not a single fixed default.
Sources:
- https://www.docker.com/blog/untrusted-autonomous-workload-ai-sandboxes/ — "Open ... Balanced - Deny-by-default with pre-allowed common dev domains ... Locked Down - Deny-by-default, explicit allowlist required"
- https://docs.docker.com/ai/sandboxes/security/defaults/ — "All outbound HTTP and HTTPS traffic is blocked unless an explicit rule allows it (deny-by-default)"

### egress_allowlist
egress_allowlist: Yes — `sbx policy allow/deny network <resources>` takes a comma-separated list of hostnames/domains/IPs. Granularity: exact domain (`example.com`), wildcard subdomain (`*.example.com`), optional port suffix (`example.com:443`), `**` for allow-all, and IP/CIDR rules (CIDR blocks resolved-IP matching). Rules can be global (all sandboxes) or scoped to one sandbox (`--sandbox <name>`). No path or HTTP-method scoping — an open GitHub feature request (#239) for "layer-7 egress policy — per-host HTTP method and path rules" confirms current granularity stops at host/domain/port; it is unresolved as of the doc/issue capture date.
2-4 sentences: Deny rules exist as a distinct verb (`sbx policy deny network`) alongside allow; docs found do not spell out explicit precedence-on-overlap semantics (e.g., longest-match vs most-recent-wins) — not documented in material retrieved. Multiple domains can be passed in one call. Per-sandbox rules can supplement/override global ones except when org (paid) governance is active, in which case org policy fully replaces local rules.
Sources:
- https://docs.docker.com/reference/cli/sbx/policy/allow/network/ (via WebSearch of same page) — "supports exact domains (example.com), wildcard subdomains (*.example.com), and optional port suffixes (example.com:443)"; "'**' to allow all hosts"; "--sandbox to add the rule to policy 'local' scoped to a single sandbox"
- https://github.com/docker/sbx-releases/issues/239 — "sbx policy allow network <host[:port]> and deny network <host> are host/domain/port granularity only"

### dns_level_blocking
dns_level_blocking: Partial — DNS resolution is routed through/checked by the host proxy rather than being a separate DNS-tier block (classic NXDOMAIN-for-unlisted-domain behavior, as used by some competitors, is not confirmed). "When you block or allow a domain, the proxy resolves it to IP addresses and checks those IPs against CIDR rules." Enforcement is effectively proxy-layer (HTTP CONNECT/host-header matching), not a dedicated DNS resolver denying the query itself.
Sources:
- https://docs.docker.com/ai/sandboxes/troubleshooting/ (via WebSearch synthesis) — "blocking a CIDR range affects any domain that resolves to an IP in that range"
- https://docs.docker.com/ai/sandboxes/network-policies/ (via WebSearch synthesis; direct WebFetch of this URL 404'd both with and without trailing slash) — "the proxy checks the policy rules against the host in the request, and if the host is blocked, the request is stopped immediately"

### tls_mitm_inspection
tls_mitm_inspection: Yes — the host proxy terminates TLS, inspects the host header, applies policy, and re-encrypts; described by Docker itself as "man-in-the-middle by design." An opt-out **bypass mode** (`--bypass-host`, `--bypass-cidr`) tunnels HTTPS directly without inspection for apps using certificate pinning, at the documented cost of losing "the visibility and security benefits of MITM inspection."
Sources:
- https://www.docker.com/blog/untrusted-autonomous-workload-ai-sandboxes/ — "Proxy terminates TLS, inspects host header, applies policy, re-encrypts" / "man-in-the-middle by design"
- (via WebSearch synthesis of docs.docker.com/ai/sandboxes/network-policies/) — "use bypass mode to tunnel HTTPS traffic directly without inspection... bypassed traffic loses the visibility and security benefits of MITM inspection"

### http_path_rules
http_path_rules: No — confirmed absent via an open, unresolved GitHub feature request from the vendor's own issue tracker asking for exactly this ("Feature: Layer-7 egress policy — per-host HTTP method and path rules," #239), which states current policy is "host/domain/port granularity only" despite the proxy already having full L7/TLS-terminated visibility to support it technically. No method gating (GET/HEAD-only, block PUT/POST) either.
Sources:
- https://github.com/docker/sbx-releases/issues/239 — "sbx policy allow network <host[:port]> and deny network <host> are host/domain/port granularity only" (feature request, unresolved)

### proto_coverage
proto_coverage: Partial — HTTP/HTTPS is the only protocol with real policy control (allow/deny by host, port-scoped). UDP and ICMP are unconditionally blocked at the network layer and "can't be unblocked with policy rules" (i.e., no opt-in, not even for legitimate UDP use). Non-HTTP TCP (SSH, generic ports) requires IP+port-based rules since hostname resolution isn't available to the proxy in that path ("myhost:22 don't work for non-HTTP connections... use the IP address directly"). DNS itself is implicitly handled by the proxy's own resolution step, not separately gateable. No documented extensibility model for adding new/custom L7 protocols into the policy engine — protocol set is fixed (HTTP/HTTPS proxied+inspected; TCP by IP/port; UDP/ICMP hard-blocked, no exception).
Sources:
- https://docs.docker.com/ai/sandboxes/troubleshooting/ (via WebSearch synthesis) — "UDP and ICMP traffic is blocked at the network layer and can't be unblocked with policy rules"; "Hostname-based rules... don't work for non-HTTP connections... Use the IP address directly"

### live_rule_reload
live_rule_reload: Yes — local policy changes "take effect immediately," no sandbox restart required. For paid org governance, network-policy changes also propagate live (within up to 5 minutes to sync from the control plane; users can force-sync via `sbx policy reset`, which also wipes local overrides), while filesystem policy changes for org governance apply only to newly-created sandboxes, not running ones.
Sources:
- https://docs.docker.com/ai/sandboxes/security/policy/ (via WebSearch synthesis) — "policy changes 'take effect immediately'"
- https://docs.docker.com/ai/sandboxes/security/governance/ — "Network policies take effect immediately after syncing" / "Filesystem policies only apply when new sandboxes are created" / propagation "within up to 5 minutes"

### firewall_escape_hatch
firewall_escape_hatch: Partial — `--bypass-host`/`--bypass-cidr` provide a targeted, persistent (not visibly timed/auto-expiring) exemption for specific hosts/CIDRs that tunnels HTTPS uninspected, rather than an all-or-nothing disable. Separately, the whole-sandbox "Open" policy mode removes egress restriction almost entirely. No documentation found of a *timed* bypass with automatic re-enforcement (the criterion's stated bar for a clean Yes) — bypass rules appear to persist as configured until manually removed, and switching to Open policy is a manual, not self-expiring, choice.
Sources:
- https://www.docker.com/blog/untrusted-autonomous-workload-ai-sandboxes/ (via WebSearch synthesis of docs.docker.com/ai/sandboxes/network-policies/) — "use bypass mode to tunnel HTTPS traffic directly without inspection, using commands like --bypass-host api.service-with-pinning.com, --bypass-cidr 203.0.113.0/24"

### enforcement_plane
enforcement_plane: prose — Enforcement is a host-side userspace HTTP/HTTPS proxy (default port surfaced to the VM as `host.docker.internal:3128`) combined with network-layer blocking of UDP/ICMP (mechanism for that block not detailed — plausibly VM network-device/hypervisor-level rather than eBPF, but not confirmed). The proxy is outside the VM (agent cannot tamper with the enforcement point from inside the sandbox, since it has no access to host processes), and traffic through it is logged (`sbx policy log`, showing a PROXY column of `forward`/`forward-bypass`/`transparent`).
Sources:
- https://www.docker.com/blog/untrusted-autonomous-workload-ai-sandboxes/ — "Host-side proxy ... accessible from VM at host.docker.internal:3128"
- https://docs.docker.com/ai/sandboxes/troubleshooting/ (via WebSearch synthesis) — "sbx policy log ... PROXY column showing forward, forward-bypass, or transparent"

### fail_closed
fail_closed: Yes — vendor's own blog states default network-proxy denial behavior is fail-closed: non-allowlisted domains are blocked, allowlisted ones succeed, "no fallback mechanism documented for bypass." No official doc found addressing the specific supervisor-death scenario (what happens if the host proxy process itself crashes — does egress fail-closed at that point, or does the VM lose its enforcement point and get unrestricted local egress?); this specific mechanism-under-failure question is Unknown, distinct from the general fail-closed policy-decision behavior which is documented as Yes.
Sources:
- https://www.docker.com/blog/untrusted-autonomous-workload-ai-sandboxes/ (via WebSearch synthesis) — "Default behavior: Network proxy denial is fail-closed... No fallback mechanism documented for bypass"

### network_audit
network_audit: Yes — `sbx policy log` shows which requests were allowed/blocked, including a PROXY column distinguishing `forward` (credential-injecting), `forward-bypass`, and `transparent` egress paths, useful for diagnosing both policy and credential-injection/cert issues.
2-4 sentences: This is a local, CLI-driven log rather than a persistent shippable audit trail by default. True org-wide "audit logs" as a durable/centralized capability is explicitly named as part of the paid organization-governance tier alongside sign-in enforcement and centralized policy — implying the free tier's logging is local/ephemeral/CLI-only, not a shipped audit system.
Sources:
- https://docs.docker.com/ai/sandboxes/troubleshooting/ (via WebSearch synthesis) — "sbx policy log ... check which requests are being blocked"
- https://docs.docker.com/ai/sandboxes/faq/ (via WebSearch synthesis) — paid org tier covers "centralized policies, sign-in enforcement, audit logs"

### workspace_modes
workspace_modes: Yes — **direct mode** (default): live bind mount via filesystem passthrough, agent edits appear in the host working tree immediately, reviewed as an ordinary git diff. **Clone mode** (`--clone`, fixed at sandbox-create time): host repo mounted read-only, agent works against a private in-VM clone; requires the workspace to be a git repo, and is rejected when invoked from a git worktree other than the main checkout (can't resolve the worktree's `.git` pointer file through the read-only mount).
Sources:
- https://docs.docker.com/ai/sandboxes/usage/ (via WebSearch synthesis) — "Clone mode is fixed at create time"; direct mode: "agent and your host see the same files"
- https://github.com/docker/sbx-releases (context, via general search) — clone-mode/worktree limitation

### observability
observability: Partial — a dashboard is referenced (third-party review site, not yet confirmed via a directly-fetched official doc page) showing live per-sandbox CPU/memory usage; `sbx ls` gives sandbox status; `sbx policy log` gives network-request visibility. No official doc found describing a broader activity/behavior monitoring surface (e.g. command history dashboards, session transcripts) beyond resource usage and network logs.
Sources:
- https://docs.docker.com/ai/sandboxes/usage/ — `sbx ls` "Displays running sandboxes and their status" (via WebSearch synthesis)
- (third-party, marked explicitly) https://www.ajeetraina.com/10-things-you-must-know-about-docker-sandboxes/ — dashboard CPU/memory claim, NOT independently confirmed against an official page in this research pass

### supervision
supervision: Unknown — no official documentation found describing an active runtime supervisor/control-plane process that observes agent *behavior* (beyond network policy enforcement, which is a static ruleset, not adaptive intervention) and can dispatch containment commands (kill/quarantine) in response to detected misbehavior. Org governance covers policy distribution/enforcement, not behavioral intervention. Torn between No and Unknown per guidelines — recorded Unknown since docs are silent rather than positively stating no such capability exists.
Sources:
- https://docs.docker.com/ai/sandboxes/security/governance/ — describes policy propagation and enforcement only, no behavioral-intervention language found

### fleet_mgmt
fleet_mgmt: Yes — `sbx ls` lists all sandboxes with status; sandboxes are named (`--name`) and independently addressable for `run`/`stop`/`rm`/`exec`; org governance adds team/organization-scoped policy targeting on top of individual sandbox lifecycle. No dedicated multi-sandbox registry/naming-hierarchy features beyond name + org/team scoping were found.
Sources:
- https://docs.docker.com/ai/sandboxes/usage/ (via WebSearch synthesis) — "sbx ls: Displays running sandboxes and their status"
- https://docs.docker.com/ai/sandboxes/security/governance/ — org/team policy scoping

### snapshots_persistence
snapshots_persistence: Partial — state persists across `sbx stop`/restart ("installed packages, Docker images, configuration changes, and command history all persist across stops and restarts"), but everything is permanently deleted on `sbx rm`. No true point-in-time snapshot/rollback capability (e.g., snapshot-and-branch a sandbox state) was found documented — persistence is stop/resume only, not snapshot/restore.
Sources:
- https://docs.docker.com/ai/sandboxes/usage/ (via WebSearch synthesis) — "installed packages, Docker images, configuration changes, and command history all persist across stops and restarts", deleted on `sbx rm`

## D. Setup
### setup
setup: Easy — a single package-manager install command per platform (`brew install docker/tap/sbx` on macOS, `winget install Docker.sbx` on Windows, `curl | sh` + `apt-get install docker-sbx` on Linux), then `sbx login` (browser OAuth + one-time network-policy choice), then `sbx run --name my-sandbox claude` from a project directory. Docker Desktop/Engine is explicitly NOT required. First run is slower (pulls the agent image); subsequent runs are described as starting "within seconds."
2-4 sentences: Platform prerequisites are real constraints, not just Docker: macOS requires Sonoma+ **and Apple silicon** (no Intel Mac support documented); Windows requires 64-bit x86_64, Windows 11, and Hypervisor Platform enabled; Linux requires Ubuntu 24.04+ with KVM enabled and the user in the `kvm` group. An account (`sbx login`, Docker OAuth) is mandatory even for pure local use.
Sources:
- https://docs.docker.com/ai/sandboxes/get-started/ (via WebSearch synthesis) — install commands; "macOS: Sonoma (v14+) with Apple silicon"; "Docker Desktop not required"; "The first run takes a little longer while the agent image is pulled"

## E. Daily use
### daily_use
daily_use: Easy-to-moderate — core loop is `sbx run`/`sbx create` (background)/`sbx exec` (shell in)/`sbx stop`/`sbx rm`, plus `sbx cp` for file transfer and `sbx ports --publish HOST:SANDBOX` for port forwarding. Direct-mode workspace sync is automatic (live bind mount), so no explicit "sync" step is needed for the common path.
2-4 sentences: A documented friction point: sandboxes do NOT inherit host user-level agent config (e.g. `~/.claude`), so per-agent settings/customizations must be re-established per project or baked into a template/kit — a real recurring friction for users who rely on global dotfile-style config. `sbx exec` also bypasses shell initialization unless wrapped in `bash -c`, a documented gotcha.
Sources:
- https://docs.docker.com/ai/sandboxes/faq/ (via WebSearch synthesis) — "Sandboxes don't inherit user-level agent configuration from host directories like ~/.claude"; "Commands run via sbx exec bypass shell initialization unless wrapped with bash -c"

## F. Configuration
### config_depth
config_depth: Partial — configuration is split across CLI flags (`--cpus`, `--memory`, `--clone`, `--branch`, multi-workspace mounts, `--publish`), `sbx policy` (network rules), `sbx secret` (credential declarations), and "kits" (YAML, explicitly marked **experimental**, "subject to change") for install commands, file drops, and declared network/credential rules at sandbox-creation time. There is no single first-class declarative per-project config file analogous to a committed `clawker.yaml`-style manifest documented as stable — the closest analog (kits) is explicitly unstable.
2-4 sentences: Templates (Dockerfile-based, or "save a running sandbox" then push to a registry) provide durable, versionable images with baked-in packages — that part is mature. Lifecycle hooks equivalent to post-init/pre-run were not found documented outside of what a kit's install commands can express experimentally.
Sources:
- https://docs.docker.com/ai/sandboxes/customize/ (via WebSearch synthesis) — "Kits are experimental... subject to change as the feature evolves"; kit can "run install commands, drop files into the sandbox, declare network and credential rules"

### policy_model
policy_model: Moderately policy-driven — secure-by-default-with-overrides is real: three named network-policy presets (Open/Balanced/Locked Down) plus fully custom allow/deny rules scoped globally or per-sandbox; workspace mode is a per-run choice (direct bind-mount vs read-only+clone); a documented (if not clearly timed) bypass mechanism exists for TLS-pinned apps. It falls short of "fully policy-driven": filesystem access outside the workspace is permanently fixed/non-configurable ("cannot be changed through policy"), and org-tier governance is all-or-nothing once active (it fully replaces, rather than layers with, local policy).
Sources:
- https://docs.docker.com/ai/sandboxes/security/defaults/ (via WebSearch synthesis) — "Access outside the workspace is permanently blocked and cannot be changed through policy"
- https://docs.docker.com/ai/sandboxes/security/governance/ — "local sbx policy rules... are no longer evaluated and can't be used to supplement or override the organization policy"

## G. DX — host↔sandbox integration

### bind_mount_sharing
bind_mount_sharing: Yes — default (direct) mode is a live, two-way filesystem passthrough bind mount at the same absolute path as the host; agent edits appear on the host immediately, reviewed via ordinary `git diff`. Clone mode inverts this to read-only + private in-VM clone, opt-in per sandbox at creation time.
Sources:
- https://docs.docker.com/ai/sandboxes/architecture/ — "Your workspace is mounted directly into the sandbox through a filesystem passthrough"

### cred_forwarding
cred_forwarding: Partial — SSH-agent forwarding is explicit and documented (host `SSH_AUTH_SOCK` forwarded, private key never leaves host, sandbox can request signatures only) — covers git-over-SSH and SSH commit signing. Documentation found does not mention GPG signing forwarding or generic git-credential-helper forwarding; API/service credentials use the separate proxy header-injection model (`sbx secret`), not literal forwarding.
Sources:
- https://docs.docker.com/ai/sandboxes/security/credentials/ (via WebSearch synthesis) — "Private keys stay on your host. Processes inside the sandbox can request signatures from the forwarded agent, but they can't read or copy the private key." / doc explicitly noted as silent on GPG

### browser_auth
browser_auth: Yes — `sbx login` itself uses browser-based Docker OAuth; several supported agents (Codex, Claude Code, Cursor, Droid) support OAuth login flows that run on the host so the token is never exposed inside the sandbox — e.g. Claude Code's `/login` slash command inside the sandboxed session triggers a host-side OAuth flow.
Sources:
- https://docs.docker.com/ai/sandboxes/security/credentials/ (via WebSearch synthesis) — "the flow runs on the host, so the token is never exposed inside the sandbox"
- https://docs.docker.com/ai/sandboxes/agents/claude-code/ — "use the /login command inside Claude Code to authenticate via OAuth"

### shared_dirs
shared_dirs: Yes — additional host directories can be mounted alongside the primary workspace, with an explicit read-only suffix, e.g. `sbx run ... ~/project ~/shared:ro`.
Sources:
- https://docs.docker.com/ai/sandboxes/usage/ (via WebSearch synthesis) — "mount additional directories alongside the primary workspace... ~/project ~/shared:ro, with :ro designating read-only access"

### git_worktrees
git_worktrees: Partial — first-class support exists for Claude Code's "agents view," which dispatches parallel subagents each into their own git worktree — but those worktrees live *inside* the sandbox's private clone (clone mode only) and never touch the host repository. Conversely, clone mode itself is explicitly rejected when invoked from within a host-side git worktree other than the main checkout (can't resolve the worktree's `.git` pointer file through a read-only bind mount) — so host-side worktree-as-sandbox-input is NOT supported, only sandbox-internal worktrees for subagent fan-out.
Sources:
- https://github.com/docker/sbx-releases (via WebSearch synthesis of official docs) — "Clone mode is rejected from inside a Git worktree other than the main one... Run sbx create --clone from the main repository checkout instead."
- https://docs.docker.com/ai/sandboxes/agents/claude-code/ — "Agents View: Dispatches tasks to parallel subagents in isolated Git worktrees" (worktrees "live inside the sandbox's private clone")

### nested_containers
nested_containers: Yes — each sandbox has its own private Docker Engine (own daemon, image cache) with no path to the host daemon; explicitly marketed as "Agents Can Run Docker" / nested Docker container execution within the sandbox is a supported, not opt-in-via-socket-mount, capability.
Sources:
- https://www.docker.com/products/docker-sandboxes/ — "Agents Can Run Docker: Sandboxes support nested Docker container execution within the sandbox"
- https://docs.docker.com/ai/sandboxes/architecture/ — "its own Docker daemon state, image cache, and package installations"

### harness_agnostic
harness_agnostic: Partial — out-of-the-box first-class support for six named agents (Claude Code, Gemini CLI, Copilot CLI, Codex, OpenCode, Kiro), each with per-agent docs/config (auth method, base template image), plus documented ability to define custom agents. Not a fully generic "any CLI works unmodified" claim — the vendor curates and documents specific integrations, and custom-agent setup is its own (less-detailed-in-retrieved-docs) path.
Sources:
- https://www.docker.com/products/docker-sandboxes/ — "Claude Code (Anthropic), Gemini CLI (Google), Copilot CLI (Microsoft), Codex, OpenCode, Kiro... Custom agents can also be created"

## H. Performance
### performance
performance: Unknown/lightweight-leaning — no first-party benchmark numbers for startup latency, RAM overhead, or bind-mount IO throughput were found in official docs. A Docker vendor blog post gives one anecdotal build-time comparison (sandbox 1:28.58 vs host 1:44.62 for an unspecified build) suggesting near-parity or even a sandbox-favorable result for that specific case, but this is a single, vendor-sourced, non-methodology-disclosed data point, not a systematic benchmark. A vendor-documented platform-specific correctness (not performance) issue: Apple Silicon sandboxes use 16K memory pages vs the host's typical 4K assumption, breaking some Rust/jemalloc-based tooling until worked around.
Sources:
- https://www.docker.com/blog/untrusted-autonomous-workload-ai-sandboxes/ (vendor blog, marked as such) — "Build performance: Essentially zero overhead (measured: sandbox 1:28.58 vs host 1:44.62)"; "Rust dependencies with jemalloc assume 4K page sizes, fail on sandbox VMs (16K pages)"

## I. Feasibility
### feasibility
feasibility: Adoptable-today, with real platform gates — supported on macOS (Sonoma+, **Apple silicon only**, no documented Intel Mac support), Windows 11 (x86_64 with Hypervisor Platform), and Linux (Ubuntu 24.04+ with KVM, user in `kvm` group). Free for individual/commercial solo use; actively released (weekly-ish cadence); closed-source (vendor lock-in risk — no self-hosted/forkable fallback if Docker discontinues it, unlike an OSS tool). Requires a Docker account (`sbx login`) even for fully local execution, which is a soft dependency on Docker's identity infrastructure being reachable.
Sources:
- https://docs.docker.com/ai/sandboxes/get-started/ (via WebSearch synthesis) — platform prerequisites as listed above

## J. Price (prose-only)
Free for individual developers, including commercial use, for the core `sbx` CLI and all documented sandboxing/isolation/network-policy features. The only paid tier is "organization governance" (centralized policy targeting orgs/teams, sign-in enforcement, audit logs) via a separate subscription — contact Docker Sales. No self-host/on-prem option for the governance control plane found documented; the sandbox runtime itself always runs locally regardless of tier.
Sources:
- https://docs.docker.com/ai/sandboxes/faq/ (via WebSearch synthesis) — "sbx CLI is completely free for individual use, including commercial applications"; org governance "requires a separate paid subscription" via Docker Sales
- https://docs.docker.com/ai/sandboxes/security/governance/ — "This capability requires a separate paid subscription"

## K. Extensibility
### extensibility
extensibility: Yes — two documented mechanisms: **Templates** (Dockerfile-built or saved-from-running-sandbox images, pushed to a registry, for durable package/tool baking) and **Kits** (YAML manifests applied at sandbox-creation time: run install commands, drop files, declare network and credential rules) — kits explicitly marked experimental/subject to change. A community contrib repo exists (`docker/sbx-kits-contrib`) for sharing kits. Custom agent definitions are also supported beyond the six built-ins.
Sources:
- https://docs.docker.com/ai/sandboxes/customize/ (via WebSearch synthesis) — "The kit can run install commands, drop files into the sandbox, declare network and credential rules"; "Kits are experimental"
- https://github.com/docker/sbx-kits-contrib (existence confirmed via WebSearch result title) — "Community repository for sbx kits"

## Unknowns & caveats

- **CLI reference pages (`docs.docker.com/reference/cli/sbx/...`) would not render body content via direct WebFetch** — repeated attempts on `sbx policy`, `sbx policy allow network`, `sbx policy deny network`, `sbx run`, and top-level `sbx` returned only the page heading with no body (likely a client-side-rendered docs framework that WebFetch's fetcher doesn't execute JS for). Facts attributed to these pages were instead corroborated via WebSearch's own synthesis of the same URLs, which does appear to have accessed fuller content (it returned specific quoted flag syntax) — flagged inline wherever used as "via WebSearch synthesis" rather than a raw WebFetch quote, per evidence-rule honesty. Recommend a maintainer spot-check these specific pages with a JS-capable fetch if higher confidence is needed.
- **`https://docs.docker.com/ai/sandboxes/network-policies/`** — the single most important page for the network axis (proxy modes, MITM, bypass syntax) consistently 404'd on direct WebFetch (with and without trailing slash) despite appearing as a valid, indexed URL in WebSearch results and being synthesized correctly by WebSearch itself. Not recorded as a "blocked" URL (no NXDOMAIN/connection-refused — it's an application-level 404 from the fetcher), but flagged because it means this critical page's claims rest on WebSearch synthesis + a corroborating vendor blog post (docker.com/blog/untrusted-autonomous-workload-ai-sandboxes/) rather than direct primary-source quote verification.
- **Hypervisor identity** (Firecracker vs Cloud Hypervisor vs other) is not stated in any official doc found — docs consistently say "microVM" and "its own Linux kernel" without naming the technology.
- **supervision** (active behavioral intervention, distinct from static policy enforcement) — docs silent; recorded Unknown, not No.
- **observability dashboard** (live CPU/mem per sandbox) — only found in a third-party review (ajeetraina.com), not confirmed in official docs directly fetched in this pass; recorded Partial with the gap noted.
- **Escape-hatch timing** — whether `--bypass-host`/`--bypass-cidr` auto-expire was not found documented either way; treated as Partial (targeted exemption exists) rather than assuming a timed mechanism.
- **Precedence semantics for overlapping/conflicting allow+deny network rules** (e.g., longest-match, most-specific-wins, most-recent-wins) not found documented.
- No URLs were blocked by firewall/DNS in this research pass (operational firewall was bypassed per guidelines); the only fetch failures were the 404s noted above, not connectivity blocks.

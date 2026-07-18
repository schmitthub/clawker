# Claude Code Sandboxing (built-in sandboxed Bash tool / sandbox runtime)
category: local (primitive-style, OS-level)
Built-in filesystem+network isolation for Claude Code's Bash tool, powered by the open-source `@anthropic-ai/sandbox-runtime` engine (Seatbelt on macOS, bubblewrap on Linux/WSL2) | built on OS sandbox primitives, no container/VM | sandbox-runtime engine Apache-2.0 open source; Claude Code CLI itself proprietary | sandboxing shipped ~Nov 2025, sandbox-runtime engine 4.7k GitHub stars, "Beta Research Preview" status, backed by Anthropic

Scope note: this assessment covers the **built-in sandboxed Bash tool** and its underlying `sandbox-runtime` engine — the feature Claude Code documentation calls out as unique to Claude Code, distinct from the dev-container/custom-container/VM/"Claude Code on the web" options the same vendor also documents as alternative (generic, Docker- or cloud-based) isolation approaches. Those are noted for context but are not the primary subject.

## A. Identity
### built_on (prose-only)
OS-level sandbox, not a container or microVM. macOS: Seatbelt (`sandbox-exec` with dynamically generated profiles). Linux and WSL2: bubblewrap (`bwrap`) with network-namespace isolation, plus an optional seccomp filter (installed via the `sandbox-runtime` npm package) that blocks Unix domain sockets. Network access is mediated by a userspace proxy process running outside the sandboxed process; the sandboxed process's OS-level network is restricted to a Unix domain socket connected to that proxy, so no direct sockets are permitted at all. No persistent daemon/control-plane — policy is resolved per Claude Code session from `settings.json` scopes (managed > CLI > local > project > user). The same engine ships as the standalone `@anthropic-ai/sandbox-runtime` package usable to sandbox arbitrary processes.
Sources:
- https://code.claude.com/docs/en/sandboxing — "macOS: uses Seatbelt for sandbox enforcement... Linux: uses bubblewrap... WSL2: uses bubblewrap, same as Linux"
- https://www.anthropic.com/engineering/claude-code-sandboxing — "only allowing internet access through a unix domain socket connected to a proxy server running outside the sandbox"
- https://github.com/anthropic-experimental/sandbox-runtime — "A lightweight sandboxing tool for enforcing filesystem and network restrictions on arbitrary processes at the OS level, without requiring a container."

### execution_locality
execution_locality: Local — the sandboxed Bash tool runs directly on the developer's own machine (or WSL2), operating in place on the actual working directory; no code or files leave the host to execute elsewhere. (Claude Code separately offers "Claude Code on the web," an Anthropic-managed remote VM option, but that is a distinct product surface from the built-in sandbox being assessed here — noted for context, not folded into this determination.)
Sources:
- https://code.claude.com/docs/en/sandbox-environments — "The sandbox is built into Claude Code and runs on macOS, Linux, and WSL2." / "[Claude Code on the web] runs each session in an isolated, Anthropic-managed virtual machine."

### open_source (prose-only)
The underlying `@anthropic-ai/sandbox-runtime` engine is open source on GitHub (Apache-2.0 license), independently installable/usable outside Claude Code ("not just Claude Code" per its own description). The Claude Code CLI that invokes it (`/sandbox`) is Anthropic's proprietary product, not open source. There is no vendor-hosted "self-host the sandbox" offering to speak of — it always runs locally alongside the user's own Claude Code install.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime — License: Apache-2.0 (repo LICENSE file); "The Sandbox Runtime is a research preview developed for Claude Code to enable safer AI agents... available as an open source npm package that can be used in your own agent projects, not just Claude Code."

### maturity (prose-only)
Sandboxing for Claude Code launched publicly around November 2025 (per third-party coverage) and is now a standard, documented capability of the mainline CLI (`/sandbox`). The extracted engine `sandbox-runtime` carries 4.7k GitHub stars / 363 forks and is explicitly labeled "Beta Research Preview," with Windows support called out separately as "alpha." Backed and maintained by Anthropic.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime — "Beta Research Preview - The Sandbox Runtime is a research preview developed for Claude Code to enable safer AI agents." (Windows: alpha)

## B. Threat protection
### host_fs_damage
host_fs_damage: Yes — writes are OS-enforced to the working directory plus the session temp dir by default; the sandbox "cannot modify files outside the current working directory and session temp directory without explicit permission, including shell configuration files such as `~/.bashrc` and system binaries in `/bin/`." Caveat: default **read** access is broad — "read access to the entire computer, except certain denied directories," which explicitly still includes credential files like `~/.aws/credentials` and `~/.ssh/` unless the operator opts into `sandbox.credentials`/`denyRead`. So write-side damage is contained by default; read-side exposure is not, without extra config.
Sources:
- https://code.claude.com/docs/en/sandboxing — "Blocked access: cannot modify files outside the current working directory and session temp directory without explicit permission..."; "Default read behavior: read access to the entire computer, except certain denied directories. Note that this default still allows reading credential files such as `~/.aws/credentials` and `~/.ssh/`."

### credential_theft
credential_theft: Partial — a dedicated `sandbox.credentials` mechanism exists (`deny` unsets env vars / blocks file reads; `mask` substitutes a per-session sentinel that the proxy swaps for the real secret only on requests to allowlisted `injectHosts`), but it is opt-in and unpopulated by default: "There is no built-in credential deny list, so only the files and variables you list are restricted." `mask` additionally requires the experimental `network.tlsTerminate` proxy setting or it "fails closed" (auth breaks, but so does leakage). `mask`/`tlsTerminate` are honored only from user/managed settings, not repo-committed project settings, closing an obvious attacker-edits-the-repo bypass.
Sources:
- https://code.claude.com/docs/en/sandboxing — "There is no built-in credential deny list, so only the files and variables you list are restricted."; "`mask` entries, `network.tlsTerminate`, and `credentials.allowPlaintextInject` in a repository's `.claude/settings.json` or `.claude/settings.local.json` are ignored."

### data_exfiltration
data_exfiltration: Partial — network access is deny-by-default with per-domain prompting/allowlisting (strong default posture), but the vendor's own docs flag a concrete bypass: because the built-in proxy makes its allow decision from the client-supplied hostname without inspecting TLS by default, "code running inside the sandbox can potentially use domain fronting or similar techniques to reach hosts outside the allowlist" once any broad domain (e.g. `github.com`) is allowed. Full protection against this requires configuring a custom TLS-inspecting proxy yourself.
Sources:
- https://code.claude.com/docs/en/sandboxing — "Allowing broad domains such as `github.com` can create paths for data exfiltration. Because the proxy makes its allow decision from the client-supplied hostname without inspecting TLS, code running inside the sandbox can potentially use domain fronting or similar techniques to reach hosts outside the allowlist."

### malicious_execution
malicious_execution: Partial — blast radius of a hallucinated/malicious Bash command (or compromised package it invokes) is contained to the fs+network boundary described above, and containment covers "all scripts, programs, and subprocesses spawned by commands," not just the top-level command. Caveat, stated explicitly by the vendor: the built-in `/sandbox` only wraps the Bash tool — "Other built-in tools such as Read, Edit, and WebFetch run inside the Claude Code process... MCP servers and hooks are separate processes that run unconstrained on the host" unless you additionally wrap the whole Claude Code process with the standalone `sandbox-runtime` package.
Sources:
- https://code.claude.com/docs/en/sandbox-environments — "The per-command sandbox does not cover everything that runs in a session: ... MCP servers and hooks are separate processes that run unconstrained on the host."
- https://code.claude.com/docs/en/sandboxing — "Comprehensive coverage: restrictions apply to all scripts, programs, and subprocesses spawned by commands"

### escape_resistance
Shared-kernel OS sandbox (Seatbelt / bubblewrap + network namespaces), not a microVM or hardware-virtualized boundary — stronger than an unconfined process, weaker than VM-level isolation. The vendor's own "Security limitations" section documents several concrete, self-inflicted weakening vectors an operator must actively avoid: `allowUnixSockets` "can inadvertently grant access to powerful system services that could lead to sandbox bypasses" (their example: allowing `/var/run/docker.sock` "effectively grants access to the host system"); `enableWeakerNestedSandbox` for running inside unprivileged containers "considerably weakens security"; overly broad `allowWrite` to `$PATH` or shell rc files "can lead to code execution in different security contexts"; and `allowAppleEvents` on macOS "removes code-execution isolation" for AppleScript-capable escapes. determination: isolation boundary stronger than a plain unsandboxed process, but shared-kernel with several documented, config-triggered bypass surfaces — not comparable to VM/hypervisor separation.
Sources:
- https://code.claude.com/docs/en/sandboxing — "Privilege escalation via Unix sockets: the `allowUnixSockets` configuration can inadvertently grant access to powerful system services that could lead to sandbox bypasses. For example, allowing access to `/var/run/docker.sock` effectively grants access to the host system through the Docker socket."
- https://code.claude.com/docs/en/sandboxing — "Apple Events on macOS: ... it removes code-execution isolation: sandboxed commands can launch other applications unsandboxed with no user prompt..."

### resource_abuse
resource_abuse: No — no CPU, memory, or disk quota/rate-limiting controls appear anywhere in the sandbox documentation or the `sandbox-runtime` config schema (`allowedDomains`/`deniedDomains`/`allowRead`/`allowWrite`/credentials/proxy ports only). The sandbox scopes filesystem and network reachability, not resource consumption; a runaway sandboxed process is not throttled by the sandbox itself.
Sources:
- https://code.claude.com/docs/en/sandboxing — full "Configure sandboxing" and settings surface covers only filesystem paths, network domains, and credentials; no resource-limit keys documented.
- https://github.com/anthropic-experimental/sandbox-runtime — README config-key inventory contains no CPU/memory/disk keys.

## C. Feature set & granularity
### network_default_posture
network_default_posture: Deny-by-default (allowlist mode) — "no domains are pre-allowed by default. The first time a command needs a new domain, Claude Code prompts for approval." An unconfigured sandbox can reach nothing until a human (or a pre-populated `allowedDomains` list) approves each destination.
Sources:
- https://code.claude.com/docs/en/sandboxing — "Domain restrictions: no domains are pre-allowed by default. The first time a command needs a new domain, Claude Code prompts for approval."

### egress_allowlist
egress_allowlist: Partial — supports domain-list allowlisting with subdomain-wildcard syntax (`*.github.com`) and explicit `deniedDomains` that override a broader `allowedDomains` wildcard ("Blocks specific domains even when a broader `allowedDomains` wildcard would otherwise permit them"). No documented IP/CIDR matching, no port scoping, and no native path/method/regex rules — those require standing up your own custom proxy behind `httpProxyPort`/`socksProxyPort`. Granularity ladder actually reached: domain list → subdomain wildcard → explicit deny-overrides-allow precedence. Not reached natively: IP/CIDR, port ranges, path/method/regex.
Sources:
- https://code.claude.com/docs/en/sandboxing — `allowedDomains: ["*.github.com", "registry.npmjs.org"]` example; "Sandbox `deniedDomains` | Blocks specific domains even when a broader `allowedDomains` wildcard would otherwise permit them"
- https://code.claude.com/docs/en/sandboxing — "Custom proxy support: advanced users can implement custom rules on outgoing traffic"

### dns_level_blocking
dns_level_blocking: Unknown — the docs describe blocking at the **proxy** layer by requested hostname ("the built-in proxy enforces the allowlist based on the requested hostname"), not a described DNS-resolution/NXDOMAIN step comparable to a CoreDNS-style firewall. Architecturally, the sandboxed process can only reach the network via a Unix domain socket to the proxy (no raw sockets), which structurally forecloses any independent DNS lookup outside that path — but the docs never state this as a "DNS blocking" mechanism, so it's not confirmed as a distinct enforcement layer.
Sources:
- https://code.claude.com/docs/en/sandboxing — "The built-in proxy enforces the allowlist based on the requested hostname and, by default, does not terminate or inspect TLS traffic."
- https://www.anthropic.com/engineering/claude-code-sandboxing — "only allowing internet access through a unix domain socket connected to a proxy server running outside the sandbox"

### tls_mitm_inspection
tls_mitm_inspection: Partial — default is no interception: "by default, does not terminate or inspect TLS traffic." An experimental `network.tlsTerminate` setting (v2.1.199+) makes the built-in proxy terminate TLS itself, but explicitly *only* to support credential `mask` substitution — the docs state it "does not add content filtering." Full MITM-based content/path inspection requires bringing your own custom proxy and installing its CA in the sandbox.
Sources:
- https://code.claude.com/docs/en/sandboxing — "The experimental `network.tlsTerminate` setting... makes the built-in proxy terminate TLS itself, which `mask` credential entries require... but does not add content filtering."

### http_path_rules
http_path_rules: No — the native `allowedDomains`/`deniedDomains` mechanism operates at domain granularity only; no method-gating or path/regex construct is documented for the built-in proxy. Path/method-level control is achievable only by substituting a fully custom proxy, which is then entirely the operator's own implementation, not a feature of the built-in sandbox.
Sources:
- https://code.claude.com/docs/en/sandboxing — settings reference for `sandbox.network.*` documents only `allowedDomains`/`deniedDomains`/proxy ports; "Custom proxy configuration" section lists path-level filtering as something *you* build: "Decrypt and inspect HTTPS traffic... Apply custom filtering rules."

### proto_coverage
proto_coverage: Partial — natively documented coverage is HTTP/HTTPS through the built-in proxy, plus an optional generic-TCP path via a configurable `socksProxyPort`. No mention anywhere in the docs of dedicated DNS, ICMP, UDP, or QUIC/HTTP3 handling. Because the OS sandbox denies the process any socket other than the one Unix-domain-socket path to the proxy, non-proxied protocols are plausibly blocked outright rather than filtered — but this is an architectural inference, not a stated coverage claim. Extensibility: the docs explicitly support routing through a fully custom proxy for "advanced" rule sets, but that is bring-your-own, not a built-in extensible protocol model.
Sources:
- https://code.claude.com/docs/en/sandboxing — `"network": {"httpProxyPort": 8080, "socksProxyPort": 8081}` custom-proxy example
- https://www.anthropic.com/engineering/claude-code-sandboxing — "only allowing internet access through a unix domain socket connected to a proxy server running outside the sandbox"

### live_rule_reload
live_rule_reload: Partial — approving a new domain via the interactive prompt updates the *running session's* allowlist immediately with no restart required, and "as of v2.1.191, choosing Yes allows the host for the rest of the current session, so later connections to the same host do not prompt again." However, this is session-scoped, ad hoc approval, not a documented mechanism for live-reloading a `settings.json` policy edit into an already-running session; broader policy changes (e.g. managed-settings pushes) are not described as hot-applying mid-session.
Sources:
- https://code.claude.com/docs/en/sandboxing — "As of v2.1.191, choosing Yes allows the host for the rest of the current session, so later connections to the same host do not prompt again."

### firewall_escape_hatch
firewall_escape_hatch: Yes — a controlled, per-command break-glass: when a command fails under the sandbox, Claude Code "may retry the command with the `dangerouslyDisableSandbox` parameter," which routes that one retried command back through the regular (prompted, or auto-mode-classified) permission flow rather than tearing down the whole sandbox. This can be forced to always prompt (`Bash(dangerouslyDisableSandbox:true)` ask rule) or disabled entirely ("Strict sandbox mode," `allowUnsandboxedCommands: false`), at which point "the `dangerouslyDisableSandbox` parameter is completely ignored." No documented timed/automatic-re-enforcement bypass window (contrast: a fixed-duration bypass with auto re-arm) — it is per-command opt-out, always subject to the permission layer, not a session-wide timer.
Sources:
- https://code.claude.com/docs/en/sandboxing — "Claude Code includes an escape hatch: when a command fails because of sandbox restrictions, Claude analyzes the failure and may retry the command with the `dangerouslyDisableSandbox` parameter."
- https://code.claude.com/docs/en/sandboxing — "You can disable this escape hatch by setting `allowUnsandboxedCommands: false`... the `dangerouslyDisableSandbox` parameter is completely ignored."

### enforcement_plane
enforcement_plane: kernel-level. Filesystem and network boundaries are enforced by OS primitives (Seatbelt on macOS, bubblewrap + Linux network namespaces on Linux/WSL2), not a userspace wrapper alone — the sandboxed process is denied direct sockets at the OS level and can reach the network only via a Unix domain socket to an external userspace proxy process, so the proxy's domain-allow logic sits behind, not instead of, kernel enforcement. From inside, the sandboxed command cannot open competing sockets to route around the proxy (absent one of the documented weakening options like `allowUnixSockets`/`enableWeakerNestedSandbox`). Traffic-level logging at this layer is not described as a built-in feature (see network_audit below).
Sources:
- https://code.claude.com/docs/en/sandboxing — "OS-level enforcement: The sandboxed Bash tool leverages operating system security primitives... These OS-level restrictions ensure that all child processes spawned by Claude Code's commands inherit the same security boundaries."
- https://www.anthropic.com/engineering/claude-code-sandboxing — "only allowing internet access through a unix domain socket connected to a proxy server running outside the sandbox"

### fail_closed
fail_closed: Partial — two different failure modes documented with opposite defaults. (1) *Sandbox-unavailable-at-startup* (missing bubblewrap, unsupported platform) defaults to fail-**open**: "if the sandbox cannot start because dependencies are missing or the platform is unsupported, Claude Code shows a warning and runs commands without sandboxing" — unless the operator sets `failIfUnavailable: true` (recommended in the managed-settings example), which makes it a hard startup failure instead. (2) *Mid-session proxy-process failure* is not documented at all; the architecture (sandboxed process only has a socket path to the proxy, no direct network) suggests network access would fail closed if the proxy died mid-session, but no doc statement confirms this behavior.
Sources:
- https://code.claude.com/docs/en/sandboxing — "By default, if the sandbox cannot start because dependencies are missing or the platform is unsupported, Claude Code shows a warning and runs commands without sandboxing. To make this a hard failure instead, set `sandbox.failIfUnavailable` to `true`."

### network_audit
network_audit: Partial/No — no built-in per-request egress log is documented for the native proxy; comprehensive request logging is explicitly listed as something you get by building your *own* custom proxy ("Log all network requests" under "Custom proxy configuration"), implying the built-in path does not provide it. The only native visibility is the interactive first-use approval prompt per domain (a point-in-time decision record, not a request-level audit trail) and Claude Code's broader session logs/OTel metrics, which are not sandbox-network-specific.
Sources:
- https://code.claude.com/docs/en/sandboxing — "For organizations requiring advanced network security, you can implement a custom proxy to: ... Log all network requests"

### workspace_modes
workspace_modes: Partial — the built-in sandbox has exactly one mode: it operates directly, in place, on the user's real working directory (equivalent to always-bind-mount; there is no copy). No ephemeral/snapshot workspace mode exists for the built-in Bash sandbox itself. (The separate dev-container and "Claude Code on the web" approaches each have their own, different copy/mount semantics, but are outside this feature's scope.)
Sources:
- https://code.claude.com/docs/en/sandboxing — "By default, commands inside the sandbox can write only to the working directory and the session temp directory" (no alternate snapshot mode described).

### observability
observability: Partial — Claude Code has product-wide OpenTelemetry metrics/session logging (`/en/monitoring-usage`), but this is not a sandbox-specific network/fs activity dashboard; the sandbox's own visibility is the interactive per-domain approval prompt plus the `/sandbox` "Config" tab showing resolved settings, not a passive metrics/log stream of what the sandbox actually blocked or allowed over time.
Sources:
- https://code.claude.com/docs/en/sandboxing — "`/sandbox`... **Config**: view the resolved sandbox settings"
- https://code.claude.com/docs/en/security — "Monitor Claude Code usage through OpenTelemetry metrics" (general product feature, not sandbox-specific)

### supervision
supervision: No — there is no external control-plane process that observes a running sandboxed session and can issue containment/kill/quarantine commands into it. Enforcement is entirely local: OS-level policy set at session start, plus local prompts/managed-settings lockdown. Nothing in the docs describes real-time remote intervention capability.
Sources:
- https://code.claude.com/docs/en/sandboxing / https://code.claude.com/docs/en/sandbox-environments — no supervisory/control-plane mechanism described anywhere in either page; enforcement is described purely as local OS + settings-resolution.

### fleet_mgmt
fleet_mgmt: Partial — not a sandbox feature per se, but Claude Code's separate "background agents" capability runs a per-user supervisor process that "starts on demand, outlives your shell, and hosts every `claude agents`, `--bg`, and `/background` session," i.e., multiple concurrent sessions on one machine under one local supervisor. No cross-machine registry, naming hierarchy, or fleet-wide lifecycle management is documented; this is single-host process supervision, not fleet management.
Sources:
- https://code.claude.com/docs/en/network-config — "A per-user supervisor process starts on demand, outlives your shell, and hosts every `claude agents`, `--bg`, and `/background` session."

### snapshots_persistence
snapshots_persistence: No — the built-in Bash sandbox has no pause/resume/snapshot mechanism of its own; it has no persistent state to snapshot (it acts directly on the live host filesystem each session). The closest documented analog belongs to the separate dev-container approach (a named Docker volume mounted at `~/.claude` to persist auth/settings/history across container rebuilds) — a different isolation approach, not the built-in sandbox. "Claude Code on the web" VMs are explicitly the opposite of persistent: "Automatic cleanup: Cloud environments are automatically terminated after session completion."
Sources:
- https://code.claude.com/docs/en/devcontainer — "By default, the container's home directory is discarded on rebuild... Mount a named volume at that path to keep this state across rebuilds."
- https://code.claude.com/docs/en/security — "Automatic cleanup: Cloud environments are automatically terminated after session completion."

## D. Setup
### setup
setup: Trivial-to-easy — on macOS, "there is nothing to install: sandboxing uses the built-in Seatbelt framework," and enabling is one command (`/sandbox`) plus a mode choice. On Linux/WSL2, it needs two packages (`sudo apt-get install bubblewrap socat`) and optionally the seccomp helper (`npm install -g @anthropic-ai/sandbox-runtime`); Ubuntu 24.04+ may additionally need a one-time AppArmor profile addition for bubblewrap user-namespaces. No account, no Docker, no cloud dependency. Native Windows is unsupported outright — must run inside WSL2.
Sources:
- https://code.claude.com/docs/en/sandboxing — "On macOS, there is nothing to install... On Linux and WSL2, the sandbox relies on two packages"; "`sudo apt-get install bubblewrap socat`"

## E. Daily use
### daily_use
daily_use: Moderate — in auto-allow mode, sandboxed commands run with no per-command prompting once the initial per-session domain approvals are done, which is the tool's explicit goal (vendor blog claims an 84% internal reduction in permission prompts from sandboxing). Friction resurfaces around documented tool-compatibility gaps requiring manual tuning: `jest`+`watchman` hangs (workaround: `--no-watchman`), `docker` commands fail outright (must be excluded via `excludedCommands`), Go-based CLIs (`gh`, `gcloud`, `terraform`) can fail TLS verification under Seatbelt on macOS, and macOS browser/OAuth flows via `open`/`osascript` fail with error `-600` unless Apple Events are explicitly (and security-degradingly) re-enabled.
Sources:
- https://www.anthropic.com/engineering/claude-code-sandboxing — "sandboxing reduced permission prompts by 84%" (internal Anthropic figure, vendor-reported)
- https://code.claude.com/docs/en/sandboxing — Troubleshooting list: `jest`/watchman, Go CLIs + TLS, Apple Events `-600` error, `docker` incompatibility

## F. Configuration
### config_depth
config_depth: Deep, within its scope (filesystem + network + credentials, not full container-style config). Declarative and versionable via `settings.json` at multiple scopes (managed/org, CLI `--settings`, user `~/.claude/settings.json`, project `.claude/settings.json`, project-local `.claude/settings.local.json`), covering: filesystem `allowWrite`/`denyWrite`/`allowRead`/`denyRead` with path-prefix semantics and cross-scope array-merge rules; network `allowedDomains`/`deniedDomains`/custom proxy ports/experimental `tlsTerminate`; dedicated `sandbox.credentials` block (deny/mask, per-var `injectHosts`); `excludedCommands` escape valve; org-lockdown keys (`allowManagedDomainsOnly`, `allowManagedReadPathsOnly`, `failIfUnavailable`, `allowUnsandboxedCommands`). No mount-management, image-build, or lifecycle-hook (post-init/pre-run) scope, because it is an OS sandbox around an existing host, not a container/image system.
Sources:
- https://code.claude.com/docs/en/sandboxing — full "Configure sandboxing" + "Protect credentials" + "Configure the sandbox for your organization" sections

### policy_model
policy_model: Fully policy-driven — secure-by-default posture (deny-network, cwd-only-write) combined with granular per-scope overrides and org-enforced break-glass controls that developers cannot loosen: boolean managed keys (`enabled`, `failIfUnavailable`) always win over local settings, array keys merge (a developer can only *widen*, not narrow, unless `allowManagedReadPathsOnly`/`allowManagedDomainsOnly` lock reads/domains to the managed list exclusively). Local escape hatches exist (`dangerouslyDisableSandbox`, `excludedCommands`) but are themselves policy-controllable (`allowUnsandboxedCommands: false` disables the sandbox bypass entirely). This is a mature, dial-able policy surface rather than fixed take-it-or-leave-it behavior.
Sources:
- https://code.claude.com/docs/en/sandboxing — "For boolean keys such as `enabled` and `failIfUnavailable`, Claude Code uses the managed value and ignores anything a developer sets locally... Set `allowManagedReadPathsOnly` to `true`... This prevents developers from widening read access beyond the organization-approved paths."

## G. DX — host↔sandbox integration
### bind_mount_sharing
bind_mount_sharing: Yes — the sandbox operates directly on the real working directory; there is no copy-in/copy-out step. Every allowed write is immediately the host file.
Sources:
- https://code.claude.com/docs/en/sandboxing — "By default, commands inside the sandbox can write only to the working directory and the session temp directory."

### cred_forwarding
cred_forwarding: Partial — a dedicated, mediated mechanism exists for generic env-var/file credentials: `mask` mode gives the sandboxed process only a per-session sentinel value, and the external proxy substitutes the real secret solely on outbound requests to that credential's declared `injectHosts` ("The command and anything it logs never hold the real credential, but its requests still authenticate"). This is a real forwarding-without-exposure design, but it's experimental, requires `network.tlsTerminate`, and is restricted to user/managed settings (not repo-controllable). No dedicated ssh-agent/gpg-agent socket-forwarding feature is documented — an SSH agent socket would have to go through the generic (and vendor-flagged-risky) `allowUnixSockets` setting rather than a purpose-built mediation path.
Sources:
- https://code.claude.com/docs/en/sandboxing — "`mask` protects a credential while keeping the tools that authenticate with it working... The command and anything it logs never hold the real credential, but its requests still authenticate."
- https://code.claude.com/docs/en/sandboxing — "Privilege escalation via Unix sockets: the `allowUnixSockets` configuration can inadvertently grant access to powerful system services..."

### browser_auth
browser_auth: No — (corrected 2026-07-18, attribution audit) no mediated browser-auth relay/proxy exists; the sandbox merely blocks Apple-Events-based process launching by default (`open`/`osascript`/browser flows fail with error `-600`), and the only documented fix, `allowAppleEvents: true`, doesn't mediate or proxy anything — it "removes code-execution isolation" wholesale so the OS's normal (sandbox-unrelated) flow can proceed. Per the sharp test, a blanket isolation bypass is not a seamless open-approve-done mechanism — there's no forwarding/callback relay, just isolation being turned off. Behavior on Linux/bubblewrap for equivalent flows remains undocumented.
Sources:
- https://code.claude.com/docs/en/sandboxing — "`open`, `osascript`, or browser-based auth flows fail with error -600 on macOS: the sandbox blocks Apple Events by default."

### shared_dirs
shared_dirs: Yes — `sandbox.filesystem.allowWrite`/`allowRead` grant OS-enforced access to arbitrary additional host paths beyond the working directory (e.g. `~/.kube`, `/tmp/build`), merged across settings scopes.
Sources:
- https://code.claude.com/docs/en/sandboxing — `"filesystem": {"allowWrite": ["~/.kube", "/tmp/build"]}` example

### git_worktrees
git_worktrees: Yes — explicitly handled: "when the working directory is a linked git worktree, the sandbox also allows writes to the main repository's shared `.git` directory so commands such as `git commit` can update refs and the index," while still denying writes to that shared directory's `hooks/` and `config`.
Sources:
- https://code.claude.com/docs/en/sandboxing — "Git worktrees: when the working directory is a linked git worktree, the sandbox also allows writes to the main repository's shared `.git` directory..."

### nested_containers
nested_containers: No — Docker is explicitly unsupported inside the sandbox: "`docker` commands fail: `docker` is incompatible with the sandbox. Add `docker *` to `excludedCommands` to run it outside the sandbox." There is no documented DinD/nested-runtime accommodation; the only path is to exempt Docker invocations from the sandbox entirely.
Sources:
- https://code.claude.com/docs/en/sandboxing — "`docker` commands fail: `docker` is incompatible with the sandbox. Add `docker *` to `excludedCommands` to run it outside the sandbox."

### harness_agnostic
harness_agnostic: Partial — the `/sandbox` Bash-tool feature itself is Claude-Code-specific (invoked from within a Claude Code session). However, the engine behind it, `@anthropic-ai/sandbox-runtime`, is shipped as a standalone open-source npm package explicitly usable to sandbox "agents, local MCP servers, bash commands and arbitrary processes" outside Claude Code, so the isolation technology (though not the `/sandbox` UX) is not vendor-locked.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime — "can sandbox agents, local MCP servers, bash commands and arbitrary processes" (per repo description/README)

## H. Performance
### performance
performance: Lightweight, per the vendor's own unquantified characterization — the docs state "performance overhead: minimal, but some filesystem operations may be slightly slower," and the engineering blog post reports an 84% reduction in permission prompts (a UX metric) but explicitly provides no latency, cold/warm-start, RAM, or IO-throughput benchmark numbers. No independent or third-party benchmark was found. Treat "lightweight" as the vendor's characterization, not a measured result.
Sources:
- https://code.claude.com/docs/en/sandboxing — "Performance overhead: minimal, but some filesystem operations may be slightly slower."
- https://www.anthropic.com/engineering/claude-code-sandboxing — no overhead/latency benchmark numbers found in the post; only the 84% permission-prompt-reduction figure.

## I. Feasibility
### feasibility
feasibility: Adoptable-today for individual developers on macOS/Linux, with real caveats for teams and for Windows. macOS needs zero extra install; Linux/WSL2 needs two packages. Native Windows is unsupported (WSL2 required), and the underlying `sandbox-runtime` engine itself is still "Beta Research Preview" with Windows support marked "alpha" even there. Multiple documented tool-compatibility gaps (Docker, jest/watchman, Go-CLI TLS on macOS, Apple Events) mean nontrivial toolchains need per-command tuning (`excludedCommands`) before it's frictionless, and the "Security limitations" section itself lists several ways a well-intentioned config change can quietly weaken the boundary — so org-wide rollout is feasible but not zero-effort.
Sources:
- https://code.claude.com/docs/en/sandboxing — "This option does not support native Windows. On Windows hosts, use WSL2..."; Troubleshooting section tool-compatibility list
- https://github.com/anthropic-experimental/sandbox-runtime — "Beta Research Preview"; Windows: alpha

## J. Price
### pricing (prose-only)
The sandbox itself carries no separate charge or SKU — it is a built-in capability of the Claude Code CLI, and its underlying engine (`@anthropic-ai/sandbox-runtime`) is a free, Apache-2.0-licensed open-source package. Cost is entirely a function of using Claude Code itself, which requires either a Claude subscription (Pro/Max/Team/Enterprise) or pay-per-token API/Bedrock/Vertex/Foundry access — the same cost a user would already be paying to use Claude Code at all, with or without sandboxing enabled. No dedicated pricing page for the sandboxing feature was located or expected, given it is a settings toggle rather than a separate product.
Sources:
- (self-evident from feature framing across https://code.claude.com/docs/en/sandboxing and https://code.claude.com/docs/en/sandbox-environments: sandboxing is presented throughout as a `settings.json` toggle within the existing Claude Code product, never as a metered or separately priced add-on.)

## K. Extensibility
### extensibility
extensibility: Yes — the built-in proxy can be pointed at a fully custom proxy via `network.httpProxyPort`/`socksProxyPort`, explicitly to let advanced users "decrypt and inspect HTTPS traffic," "apply custom filtering rules," "log all network requests," and "integrate with existing security infrastructure" — i.e. the native allowlist model is designed to be superseded by organization-owned infrastructure rather than being a closed box. Org policy itself is extensible through managed-settings delivery (MDM file or Claude.ai server-managed settings) layered on top of per-project settings. Additionally, the separate (non-built-in-sandbox) dev-container path offers a git-committable `devcontainer.json` + Dockerfile + `init-firewall.sh` as a parallel, Docker-based extensibility surface for teams that want a stronger boundary than the OS sandbox alone. The underlying `sandbox-runtime` package is independently reusable to sandbox other/non-Claude agent processes.
Sources:
- https://code.claude.com/docs/en/sandboxing — "For organizations requiring advanced network security, you can implement a custom proxy to: Decrypt and inspect HTTPS traffic; Apply custom filtering rules; Log all network requests; Integrate with existing security infrastructure"
- https://code.claude.com/docs/en/devcontainer — "The reference container includes an `init-firewall.sh` script that blocks all outbound traffic except the domains Claude Code and your development tools need."

## Unknowns & caveats
- **dns_level_blocking / proto_coverage (DNS/ICMP/UDP/QUIC)**: docs describe hostname-based proxy filtering and an optional generic-TCP SOCKS path, but never explicitly characterize DNS resolution handling or ICMP/UDP/QUIC treatment. Architecture (no direct sockets outside the proxy Unix socket) suggests these are structurally blocked rather than filtered, but this is inference, not a documented claim — recorded as Unknown/Partial rather than asserted.
- **fail_closed mid-session**: no doc statement covers what happens if the external network proxy process itself crashes mid-session (as opposed to being unavailable at startup, which is documented and defaults fail-open unless `failIfUnavailable` is set).
- **network_audit**: no built-in per-request egress log was found; recorded as absent based on the custom-proxy section positioning logging as something *you* add, but no page explicitly states "the built-in proxy does not log requests."
- **browser_auth on Linux/bubblewrap**: only the macOS Apple-Events failure mode (`-600` error) is documented; no equivalent statement was found (positive or negative) for Linux/WSL2 browser-auth flows under bubblewrap.
- All URLs fetched successfully; no blocked/unreachable sources to report.

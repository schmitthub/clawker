# Anthropic Sandbox Runtime (srt)
category: primitive
OS-level process sandboxing CLI/library (no container/VM) enforcing filesystem and network restrictions via native OS primitives | built on: macOS `sandbox-exec`/Seatbelt, Linux `bubblewrap`+seccomp BPF, Windows WFP+dedicated low-priv account | license: Apache-2.0 | maturity: Anthropic "research preview" / "early open source preview", 4.7k GitHub stars, v0.0.66 (latest release, 2026-07-17), 618 commits

## A. Identity
### built_on (prose-only)
`srt` wraps a single arbitrary command (`srt <command>`) with OS-native security boundaries — explicitly "without requiring a container." No persistent daemon/supervisor process and no control-plane architecture: it is a CLI tool (also usable as an npm library via `SandboxManager.initialize(config)`) whose process lifetime matches the wrapped command's. Platform mechanisms: macOS uses dynamically-generated Seatbelt profiles via `sandbox-exec`; Linux uses `bubblewrap` bind-mount/namespace isolation plus a seccomp BPF filter (blocking Unix-socket creation, among other things), with the sandboxed process launched under a non-dumpable nested-init PID 1 so it isn't reparented on exit; Windows uses a native Windows Filtering Platform (WFP) kernel filter blocking all outbound connects except loopback to a proxy port range, plus a dedicated low-privilege `srt-sandbox` account. All network egress is mediated by an embedded HTTP proxy (HTTP/HTTPS) and SOCKS5 proxy (all other TCP), both listening on localhost/a Windows proxy port range, which enforce the configured domain allow/deny lists.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "A lightweight sandboxing tool for enforcing filesystem and network restrictions on arbitrary processes at the OS level, without requiring a container."
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "The sandboxed command runs as the `srt-sandbox` account, not as the calling user." (Windows)
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "A SOCKS5 proxy handles all other TCP connections"

### execution_locality
execution_locality: Local — the sandboxed process runs entirely on the developer's own machine using local OS kernel/security primitives (Seatbelt, bubblewrap, WFP); there is no remote/cloud execution mode and no self-hosted "server" deployment concept (nothing to host — it's a local npm package invoked as a CLI or embedded as a library). Code and any secrets the process touches never leave the local machine except via whatever network egress the configured proxy/domain rules permit.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "npm install -g @anthropic-ai/sandbox-runtime" (local package install, no account/API-key/remote-endpoint step documented)

### open_source (prose-only)
Apache-2.0 licensed, publicly hosted on GitHub (`anthropic-experimental/sandbox-runtime`), TypeScript/JavaScript. Self-hosting is not a meaningful concept for this tool — it has no server component to host; "self-hostable" reduces to "installable and runnable locally," which it is via `npm install -g`.
Sources:
- https://raw.githubusercontent.com/anthropic-experimental/sandbox-runtime/main/LICENSE — "Apache License, Version 2.0, January 2004"

### maturity (prose-only)
Anthropic frames the project explicitly as unfinished/experimental: "The Sandbox Runtime is a research preview developed for Claude Code to enable safer AI agents. It's being made available as an early open source preview." As of the research date: 4.7k stars, 363 forks, 29 releases (latest v0.0.66, 2026-07-17), 618 commits on main, 74 open issues. Windows support is explicitly Alpha; macOS/Linux are the more mature paths.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "The Sandbox Runtime is a research preview developed for Claude Code to enable safer AI agents."
- https://github.com/anthropic-experimental/sandbox-runtime — repo metadata (stars/forks/releases/commits, fetched 2026-07-18)

## B. Threat protection
### host_fs_damage
host_fs_damage: Partial — writes are deny-by-default everywhere (nothing writable unless explicitly allowed, `denyWrite` always wins over `allowWrite`), which prevents destructive writes outside a granted scope. But reads are allow-by-default everywhere (`denyRead` is empty by default = full read access, nothing denied) — so the sandboxed process can read the entire host filesystem unless the user explicitly adds paths to `denyRead`. A hardcoded "mandatory deny paths" list (`.bashrc`, `.gitconfig`, `.git/hooks/`, `.mcp.json`, IDE dirs, etc.) blocks *writes* to those specific paths only — it does not restrict reads of them or of anything else.
The criterion ("agent can't destroy/read host filesystem outside granted workspace") is only half-satisfied by defaults: destruction is well-contained, but reads are not restricted until the user manually configures `denyRead`/`allowRead` allowlisting.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "By default, read access is allowed everywhere. You can deny broad regions (e.g., `/Users`) and then re-allow specific paths."
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "Empty `denyRead: []` = full read access (nothing denied)."
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "Mandatory Deny Paths (Auto-Protected Files)" — "always blocked from writes" (shell configs, git configs, `.git/hooks/`, `.mcp.json`, IDE dirs)

### credential_theft
credential_theft: No (by default) — the only built-in default protection for sensitive files is a *write*-deny list; there is no default *read*-deny list. SSH private keys, cloud credential files, `.netrc`, and any other dotfile-based secret are fully readable by the sandboxed process out of the box unless the developer manually adds them to `denyRead` (the docs' own example config demonstrates doing this for `~/.ssh`, implying it is not automatic). There is no ssh-agent/gpg-agent-style mediated-forwarding mechanism documented; Unix domain sockets (which would include an SSH agent socket) are blocked by default and require explicit `allowUnixSockets`/`allowAllUnixSockets`, so ambient agent-socket access is opt-in, but file-based credentials are exposed unless separately denied.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — `"denyRead": ["~/.ssh"]` shown only as a manual example, not a default
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "Unix domain sockets: Blocked by default; `allowUnixSockets` permits specific paths (macOS)" (per repo summary of config reference)

### data_exfiltration
data_exfiltration: Partial — network egress is deny-by-default (empty `allowedDomains` = zero network access), which is a strong default posture. But the docs self-disclose real bypass surfaces: domain filtering does not inspect traffic content ("does not otherwise inspect the traffic passing through the proxy"), domain fronting is called out as a possible filter bypass, broad allowlists like `github.com` are explicitly flagged as exfiltration vectors, and DNS resolution itself is NOT fenced on any platform (only the subsequent `connect()` is blocked) — meaning a process can always resolve arbitrary hostnames even when it can't connect to them, though raw UDP/53 tools (`nslookup`, `dig`) are separately fenced.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "By default, all network access is denied. You must explicitly allow domains. An empty allowedDomains list means no network access."
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "Network Sandboxing Limitations: ... does not otherwise inspect the traffic passing through the proxy and users are responsible for ensuring they only allow trusted domains"
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "it may be possible to bypass the network filtering through domain fronting"
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "DNS resolution via the system resolver is not fenced. ... This mirrors the macOS behaviour."

### malicious_execution
malicious_execution: Partial — blast radius is narrowed by default-deny writes and default-deny network, but the sandboxed process shares the host kernel (no VM/container-namespace-equivalent full isolation) and, per `host_fs_damage` above, can read the entire host filesystem by default. Several explicit escape/bypass vectors are self-documented: `allowUnixSockets` misconfiguration reaching `/var/run/docker.sock` "would effectively grant access to the host system"; macOS `allowAppleEvents` "removes code-execution isolation, not just weakens it" (launched apps run fully outside the sandbox); and the `enableWeakerNestedSandbox`/`enableWeakerNetworkIsolation` compatibility flags are labeled as considerably weakening security.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "if it is used to allow access to `/var/run/docker.sock` this would effectively grant access to the host system through exploiting the docker socket"
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "this option removes code-execution isolation, not just weakens it" (Apple Events)

### escape_resistance
escape_resistance: Partial — stronger than an unconfined/plain process (OS-native mandatory access control: Seatbelt profiles, bubblewrap namespaces + seccomp BPF syscall filtering, WFP kernel-level egress filter + dedicated low-priv Windows account), but structurally weaker than a container or microVM boundary: same host kernel, default-allow filesystem reads, and no memory/process isolation equivalent to namespaces-for-everything or hardware virtualization. The project's own "Security Limitations" section lists five concrete bypass/weakening surfaces (Unix-socket privilege escalation, filesystem write escalation via `$PATH`/shell configs, Linux `enableWeakerNestedSandbox`, macOS `enableWeakerNetworkIsolation` trustd exfil vector, macOS `allowAppleEvents` full isolation removal) — an unusually candid self-disclosure of escape surface for a "research preview."
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "### Security Limitations" (5 enumerated bypass/weakening vectors, each with its own bullet)

### resource_abuse
resource_abuse: No — no CPU, memory, disk, or process-count quotas are mentioned or configurable anywhere in the documentation; no `ulimit`/cgroups integration is described. The tool's scope is filesystem and network access control only.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — no section, field, or mention of resource limits, CPU/memory caps, cgroups, or ulimit anywhere in the document (confirmed by full-document search)

## C. Feature set & granularity
### network_default_posture
network_default_posture: deny-by-default (allowlist mode). An unconfigured sandbox (empty `allowedDomains`) has zero outbound network access; every destination must be explicitly allowlisted.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "By default, all network access is denied. You must explicitly allow domains. An empty allowedDomains list means no network access."

### egress_allowlist
egress_allowlist: Partial — domain-name-only allow/deny lists with subdomain wildcard support (`*.example.com`); deny lists are checked first and take precedence over allow lists. No IP address or CIDR-block matching is documented anywhere in the network config schema, and no port-number scoping exists — a domain rule covers the domain regardless of port. Granularity ladder present: binary on/off (empty list) → domain list → subdomain wildcards → allow/deny precedence. Missing rungs: IP/CIDR, port scoping, path/method rules (see `http_path_rules`).
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "`network.allowedDomains` — Array of allowed domains (supports wildcards like `*.example.com`)"
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "`network.deniedDomains` — Array of denied domains (checked first, takes precedence over allowedDomains)"

### dns_level_blocking
dns_level_blocking: No — DNS name resolution is explicitly NOT fenced on any platform; `getaddrinfo()`-style resolution succeeds even for domains that are not allowlisted, and only the subsequent `connect()` is blocked by the proxy/OS-firewall layer. The one exception: tools that perform their own raw UDP/53 queries (`nslookup`, `dig`) ARE fenced (blocked outright), which is the opposite of DNS-sinkhole-style filtering.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "DNS resolution via the system resolver is not fenced. ... name resolution succeeds even though the subsequent connect() from the sandboxed process is blocked. Tools that do their own UDP/53 (nslookup, dig) are fenced. This mirrors the macOS behaviour."

### tls_mitm_inspection
tls_mitm_inspection: Partial — an experimental, opt-in `network.tlsTerminate` mode exists (intercepts HTTPS CONNECTs, supports `excludeDomains` to tunnel specific hosts opaquely and `extraCaCertPaths` for a custom trust bundle; on Windows requires a separate `windowsTrustCa()` call to install the MITM CA into the sandbox account's cert store). This is not the default mode: standard domain filtering operates on SNI/Host-header inspection without decrypting/inspecting payload content, per the docs' own limitation notice.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — `network.tlsTerminate.excludeDomains` / `extraCaCertPaths` fields (experimental TLS termination)
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "does not otherwise inspect the traffic passing through the proxy" (default, non-MITM mode)

### http_path_rules
http_path_rules: No — the network rule schema is domain-only; no path, URL, or HTTP-method matching field exists anywhere in the documented `network` configuration surface.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — full `network` field reference contains only `allowedDomains`/`deniedDomains`/`allowLocalBinding`/`allowUnixSockets`/`allowAllUnixSockets`/`tlsTerminate` — no path or method fields

### proto_coverage
proto_coverage: Partial — HTTP/HTTPS is proxied and domain-filtered via an embedded HTTP proxy; all other TCP (including SSH, databases) is proxied via SOCKS5 and presumably subject to the same domain allowlist (not explicitly confirmed for IP-based TCP targets). Unix domain sockets are blocked by default, opt-in via `allowUnixSockets` (macOS path-scoped) or `allowAllUnixSockets` (all-or-nothing; the docs describe `allowUnixSockets` as "Ignored" on Linux, i.e. Linux only supports the all-or-nothing toggle, not a path allowlist). DNS/UDP-53 is separately fenced (see above). No mention anywhere of ICMP, general UDP, QUIC/HTTP3, gRPC, or WebSockets — since the underlying OS mechanism removes/blocks all network access except the specific proxy channels, undocumented protocols (e.g. QUIC) most likely fail outright rather than being selectively filtered, but this is architectural inference, not a documented statement. No documented design for adding new/custom L7 protocols into the rule model (a "bring your own proxy" extension point is explicitly roadmapped, not shipped — see `extensibility`).
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "A SOCKS5 proxy handles all other TCP connections"
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — `network.allowUnixSockets` = "Allowlist of socket paths" (macOS) / "Ignored" (Linux); `network.allowAllUnixSockets` = "Allow all sockets"

### live_rule_reload
live_rule_reload: No — configuration is read once at sandbox initialization (`SandboxManager.initialize(config)` / settings file read at process start); no documented mechanism updates rules on an already-running sandbox without tearing down and reinitializing.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — sandbox lifecycle is `initialize(config)` at start; no reload/update API documented in the config or API reference sections

### firewall_escape_hatch
firewall_escape_hatch: No — no timed bypass or per-sandbox runtime disable/enable-with-auto-re-enforcement is documented. The closest analogs are static, pre-launch config flags (`enableWeakerNestedSandbox`, `enableWeakerNetworkIsolation`, `allowAppleEvents`) that permanently weaken isolation for that invocation once set — not a controlled, timed break-glass mechanism.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — weaker-isolation flags are static config set before launch, each with a standing "considerably weakens security" warning, not a timed/audited bypass

### enforcement_plane
enforcement_plane: OS kernel / OS-native security framework — macOS Seatbelt (`sandbox-exec` MAC profile enforced by the XNU sandbox subsystem), Linux bubblewrap namespaces + seccomp BPF (kernel-enforced syscall filter), Windows WFP (kernel-level Windows Filtering Platform egress filter) combined with a dedicated low-privilege OS account. Network policy is enforced by two layers together: the OS-level mechanism restricts all outbound connections to only the local proxy port(s), and the HTTP/SOCKS5 proxy processes (running as part of/alongside `srt`) apply the domain allow/deny logic. The docs document specific ways the sandboxed process could tamper with or route around this (domain fronting, DNS-not-fenced, Unix-socket-to-docker.sock, Windows CRL/OCSP traffic bypassing the proxy entirely via WinHTTP under the caller's token — though that specific case is then blocked by the WFP fence itself, just with a broken-TLS-verification side effect). Traffic through the proxies is not logged as a persistent audit trail by default (see `network_audit`).
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "CryptoAPI's CRL/OCSP fetch goes out via WinHTTP under the caller's token, ignoring the proxy environment, so it is blocked by the WFP egress fence."

### fail_closed
fail_closed: Partial (architecture-inferred, not explicitly documented) — no section of the README addresses what happens if the `srt` process (which hosts the HTTP/SOCKS5 proxies) itself crashes while the sandboxed child is still running. Filesystem restrictions (Seatbelt/bubblewrap/WFP-ACL) are kernel/OS-enforced at launch and don't depend on any process staying alive, so those would remain in force. Network is proxy-mediated: the surrounding OS mechanism (Seatbelt allow-list to localhost proxy port / bwrap network-namespace removal / WFP block-all-except-proxy-range) permits egress ONLY to the proxy address — if the proxy process dies, the OS layer still denies every other destination, so egress would structurally fail closed (nothing reachable) rather than open. This is inferred from the stated architecture; no vendor statement confirms it.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — architecture description (Seatbelt/bwrap/WFP restrict egress to only the local proxy channel) — no explicit crash-behavior statement found; inference flagged as such

### network_audit
network_audit: Partial — macOS gets real-time, detailed violation (i.e., denied-attempt) logging by tapping the OS's own sandbox violation log store (`log stream --predicate 'process == "sandbox-exec"'`). Linux has no built-in violation reporting at all; the docs recommend a manual `strace -f -e trace=open,openat ... | grep EPERM` workaround. Windows exposes only generic `EPERM`-style errors with no centralized log documented. None of the three platforms document a full per-request audit trail of ALLOWED egress (only blocked/violation events are logged, and only on macOS is that logging built-in and real-time).
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "macOS: The sandbox runtime taps into macOS's system sandbox violation log store. This provides real-time notifications with detailed information about what was attempted and why it was blocked."
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "Linux: Bubblewrap doesn't provide built-in violation reporting." (strace workaround recommended)

### workspace_modes
workspace_modes: NA — `srt` is not a container/VM, so there is no "bind mount vs snapshot" workspace-population choice: the sandboxed process operates directly on the real host filesystem, gated live by the configured allow/deny read/write path rules. There is no copy-in/ephemeral-snapshot mode offered — access is always the live host filesystem, restricted rather than virtualized.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "without requiring a container" (no virtualization/mount layer exists to have modes)

### observability
observability: No — no dashboard, metrics API, or structured logging/monitoring surface is documented beyond the ad hoc, platform-inconsistent violation-logging behavior covered under `network_audit` (which is denial-only, not general activity observability, and is absent/manual on Linux and Windows).
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — only violation/denial-event surfaces documented (macOS log stream, Linux strace workaround); no metrics/dashboard/general-activity logging described

### supervision
supervision: No — there is no separate supervisor/control-plane process that observes the sandboxed command's behavior and can intervene; `srt` is a CLI that launches the OS sandbox and the target command as essentially a single invocation with no active-oversight or containment-dispatch layer described.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "`srt` wraps any command with security boundaries" / "Run a command in the sandbox" — no supervisor/control-plane/containment-command concept documented

### fleet_mgmt
fleet_mgmt: No — no concept of naming, registering, listing, or managing multiple concurrent sandboxes; each `srt` invocation wraps one command and is independent of any others.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — no fleet/registry/list/multi-sandbox management surface documented anywhere in the README

### snapshots_persistence
snapshots_persistence: NA — there is no sandbox "instance" that persists or is snapshotted; each invocation is a transient wrapper around a host process, operating directly on the live host filesystem (which persists trivially because it IS the host filesystem, not a separate volume).
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — no pause/resume/snapshot API or concept documented

## D. Setup
### setup
setup: Easy — `npm install -g @anthropic-ai/sandbox-runtime`, then prefix any command with `srt`. Platform prerequisites are minimal: macOS needs only `ripgrep`; Linux needs `bubblewrap`, `socat`, `ripgrep`; Windows needs no extra dependency but requires a one-time elevated `windows-install` step to set up the `srt-sandbox` account and WFP filters. No account, API key, or Docker/VM host is required (it IS the OS-level mechanism, not a wrapper around one).
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "npm install -g @anthropic-ai/sandbox-runtime"
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "Linux requires: bubblewrap, socat, ripgrep. macOS requires: ripgrep. Windows requires: No additional dependencies." (plus one-time `windows-install`)

## E. Daily use
### daily_use
daily_use: Moderate — invoking per command is trivial (`srt <command>`), but the secure-by-default posture means new workflows routinely hit unexpected `EPERM` denials that must be diagnosed and then explicitly allowlisted in the settings file, and the debugging experience is inconsistent across platforms: macOS gets real-time structured violation logs, Linux requires a manual `strace | grep EPERM` workaround, Windows surfaces only generic permission errors. There's also no live-reload (`live_rule_reload`: No), so iterating on a policy requires relaunching the wrapped command.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — Linux: "Bubblewrap doesn't provide built-in violation reporting" (manual `strace` recommended as the debugging path)

## F. Configuration
### config_depth
config_depth: Moderate — a JSON settings file (default `~/.srt-settings.json`, or a custom path via `--settings`) covers filesystem allow/deny read/write (glob patterns on macOS, literal paths only on Linux), network domain allow/deny + Unix-socket rules + experimental TLS termination, and per-command `ignoreViolations` exemptions. It is versionable as a plain file. However there is no build/image layer (nothing to build — `srt` wraps existing host binaries directly), no package-install config, no env-forwarding config, and no lifecycle hooks (post-init/pre-run) — the config surface is entirely about filesystem/network permission policy, not environment provisioning.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — settings loaded from `~/.srt-settings.json` or `--settings <path>`; fields for `filesystem.allowRead/denyRead/allowWrite/denyWrite`, `network.allowedDomains/deniedDomains/...`, `ignoreViolations`

### policy_model
policy_model: Moderate — secure-by-default (deny-write, deny-network) with per-invocation override via the settings file; several explicitly-labeled escape-hatch flags exist for specific compatibility needs (`enableWeakerNestedSandbox` for Docker-in-sandbox, `enableWeakerNetworkIsolation` for Go TLS verification, `allowAppleEvents` for URL/app-opening) — each documented with exactly what security property it gives up. But these are static, pre-launch choices, not a timed break-glass or per-session toggle (no `firewall_escape_hatch`, no `live_rule_reload`), so the "dial up/down without abandoning the tool" bar is only partially met: you can choose a weaker posture per-invocation, but not temporarily relax-then-restore an active sandbox's policy.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — `enableWeakerNestedSandbox`, `enableWeakerNetworkIsolation`, `allowAppleEvents` each documented as named, scoped, static config flags with an explicit security-tradeoff warning

## G. DX — host↔sandbox integration
### bind_mount_sharing
bind_mount_sharing: NA (trivially satisfied by architecture) — since `srt` has no container/VM layer, the sandboxed process operates directly on the real host filesystem; there is no copy step and no separate "sync back" concept. This is more direct than a bind mount (it never leaves the host), but correspondingly there's no way to get file-level isolation via a copy/snapshot mode either (see `workspace_modes`).
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "without requiring a container" (direct host-fs operation, gated by permission rules)

### cred_forwarding
cred_forwarding: No — no ssh-agent/gpg-agent/git-credential-manager mediation mechanism is documented. Because the sandboxed process runs locally as the same OS user (or a dedicated low-priv account on Windows), ambient file-based credentials are incidentally reachable (default-allow reads, per `credential_theft`) rather than deliberately forwarded/mediated; an SSH agent's Unix socket specifically requires explicit `allowUnixSockets`/`allowAllUnixSockets` opt-in, but this is a generic Unix-socket allowlist mechanism, not a purpose-built credential-forwarding feature.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — no ssh-agent/gpg/git-credential-manager feature documented anywhere in the README

### browser_auth
browser_auth: Unknown — no mention anywhere in the README of a host-browser-open proxy mechanism (OAuth/device-code flows, `gh auth login`-style callbacks). Searched the full document for auth-flow, browser, OAuth, and login-related content and found none addressing this.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — no browser-auth-proxy content found in full-document search

### shared_dirs
shared_dirs: Yes — since the model is direct allow/deny path rules against the real host filesystem rather than a fixed set of declared container mounts, arbitrary additional host directories are addressable simply by adding them to `allowRead`/`allowWrite`; there's no artificial ceiling on how many or which directories can be exposed beyond the workspace concept.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — `filesystem.allowRead`/`allowWrite` accept arbitrary path lists (glob on macOS, literal on Linux), not limited to a single declared workspace root

### git_worktrees
git_worktrees: Unknown — no mention of git worktrees anywhere in the README. (Git-related content found: `.gitconfig`/`.gitmodules`/`.git/hooks`/`.git/config` appear only in the mandatory-write-deny-path list, unrelated to worktree support.)
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — git-related mentions are limited to the mandatory-deny-paths list; no worktree concept present

### nested_containers
nested_containers: Partial — an explicit `enableWeakerNestedSandbox` mode exists on Linux specifically so `srt` can run "inside of Docker environments without privileged namespaces," but the docs state this "considerably weakens security and should only be used in cases where additional isolation is otherwise enforced." No equivalent nested-container accommodation is documented for macOS or Windows.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "enables it to work inside of Docker environments without privileged namespaces. This option considerably weakens security..."

### harness_agnostic
harness_agnostic: Yes — `srt` wraps any arbitrary command/executable, is explicitly positioned as general-purpose (its "key use case is sandboxing Model Context Protocol (MCP) servers," not one specific coding-agent CLI), and is usable as both a CLI and an embeddable npm library. It originated as infrastructure "developed for Claude Code" but the mechanism itself has no Claude/Anthropic-CLI-specific coupling.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "`srt` wraps any command with security boundaries"
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "A key use case is sandboxing Model Context Protocol (MCP) servers to restrict their capabilities."

## H. Performance
### performance
performance: Lightweight (architecture-implied, no published benchmarks) — because `srt` wraps the native process directly via OS-native primitives (Seatbelt/bubblewrap/WFP) rather than booting a container image or a VM, there is no image-build or boot-time overhead by design. No vendor-published startup-latency, throughput, or CPU-overhead benchmark numbers were found anywhere in the README.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "without requiring a container" (no VM/container boot step in the architecture); no benchmark figures present in the document

## I. Feasibility
### feasibility
feasibility: Adoptable today on macOS/Linux, cautious on Windows — macOS and Linux are the more developed platforms (both listed with concrete dependency requirements and working violation-diagnostics paths, however uneven), while Windows is explicitly Alpha. The vendor's own "research preview"/"early open source preview" framing signals it is not presented as production-hardened, and several security-relevant behaviors (crash/fail-closed semantics, resource limits, cross-platform audit logging) are undocumented gaps rather than confirmed strengths. Apache-2.0 licensing avoids vendor lock-in; a solo developer can adopt it today for local/CI sandboxing of MCP servers or agent CLIs with the caveat of doing their own review of the documented Security Limitations.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "The Sandbox Runtime is a research preview..."; Windows listed as Alpha with bundled `srt-win.exe`

## J. Price
No pricing or cost model exists — this is a free, Apache-2.0-licensed local npm package (`@anthropic-ai/sandbox-runtime`) with no hosted service, account, or paid tier documented anywhere.
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — install is a bare `npm install -g` with no account/billing step mentioned

## K. Extensibility
### extensibility
extensibility: Partial — per-command `ignoreViolations` path exemptions provide fine-grained customization, and the tool is usable as an embeddable library (`SandboxManager`) as well as a CLI, which is itself an extension surface for other tooling to build on. However, the one explicitly named extension point for network policy — "bring your own proxy" instead of the built-in HTTP/SOCKS5 proxies — is expressly roadmapped, not shipped: the docs state this "is not yet supported" and "will be added in a future release." No plugin/bundle/custom-image system exists (there is no image concept at all, per `config_depth`).
Sources:
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — "For more sophisticated network filtering, you can configure the sandbox to use your own proxy instead of the built-in ones" — followed by a note that this is not yet supported / planned for a future release
- https://github.com/anthropic-experimental/sandbox-runtime/blob/main/README.md — `ignoreViolations` — "Object mapping command patterns to arrays of paths where violations should be ignored"

## Unknowns & caveats
- **No `docs/` subdirectory exists in the repository** — the single README.md is the entirety of official documentation; all findings above are grounded in that one file (fetched via `raw.githubusercontent.com` for verbatim text, cited via the canonical `github.com/.../blob/main/README.md` URL per source-priority rules).
- **fail_closed** is architecture-inferred, not explicitly documented — no statement addresses what happens to network/filesystem enforcement if the `srt` process itself crashes mid-session. Flagged clearly as inference in its own section above, not presented as a sourced vendor claim.
- **proto_coverage for UDP/QUIC/ICMP/gRPC/WebSockets**: the README never mentions these protocols at all. Docs silence — held as Unknown/Partial per guidelines (not asserted "No" outright), with an architectural inference (undocumented protocols likely fail outright since only two proxy channels exist) explicitly labeled as inference, not fact.
- **SOCKS5 rule granularity for non-domain (bare-IP) TCP targets** could not be confirmed — the docs describe SOCKS5 handling "all other TCP connections" but don't state whether IP-literal destinations are matched against the domain allowlist at all, or blocked/allowed by some other rule.
- **Version-sensitive facts**: v0.0.66 / 4.7k stars / 618 commits reflect a live GitHub repo page fetched 2026-07-18 (pre-1.0, "research preview" framing suggests these facts, and possibly documented behaviors, may change quickly).
- **No blocked URLs encountered.** All WebFetch calls to github.com and raw.githubusercontent.com for this repo succeeded; no NXDOMAIN/connection-refused/firewall block was hit during this research.
- Not investigated (out of scope per guidelines' local-fetch instruction to ground in official docs only): third-party security-researcher writeups or independent escape/CVE disclosures against `srt` specifically — none were sought, consistent with "official docs > repo README > ... > third-party."

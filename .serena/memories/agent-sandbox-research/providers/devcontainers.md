# Dev Containers (devcontainers)
category: primitive (open spec + reference CLI + editor implementations; not itself a security product)
An open specification (containers.dev) for defining containerized dev environments via `devcontainer.json`, implemented by a reference TypeScript CLI (`devcontainers/cli`) and consumed by VS Code, GitHub Codespaces, JetBrains IDEs, Visual Studio, and others | built on plain Docker/OCI containers (runc, shared kernel) | spec docs CC-BY-4.0, CLI/tooling MIT | mature: spec+CLI under Microsoft-backed `devcontainers` GitHub org, CLI 5.6k★/482 forks/126 releases, active since ~2019, broad ecosystem adoption (VS Code, Codespaces, JetBrains, Cursor, Visual Studio, Gitpod/Ona, CodeSandbox, DevPod)
Provider note followed: assessed spec + reference CLI + VS Code implementation, plus the Anthropic-published Claude Code reference devcontainer (`.devcontainer/init-firewall.sh`) as the commonly-used agent-sandboxing pattern on top of this spec — that firewall is explicitly NOT part of the containers.dev spec itself, called out as such throughout.

## A. Identity
### built_on (prose-only)
Plain Docker/OCI containers — shared host kernel, `runc` (or whatever OCI runtime Docker is configured with). No microVM, no gVisor/Kata by default. Editor (VS Code, JetBrains, Codespaces) or the `devcontainer` CLI acts as the "control plane," driving `docker build`/`docker run`/`docker exec` per the resolved `devcontainer.json`. Claude Code's own docs rank it below a VM: "Virtual machine — Full operating system... strongest separation, with its own kernel."
Sources:
- https://code.claude.com/docs/en/sandbox-environments — "Dev container | Full development environment | ... | Virtual machine | Full operating system | ... provides the strongest separation, with its own kernel"

### execution_locality
execution_locality: Both — runs on the developer's local Docker Desktop/Engine, or remotely on a cloud host (GitHub Codespaces, Gitpod/Ona, CodeSandbox). Local mode: code and secrets stay on the dev's machine inside a local container. Remote mode (Codespaces etc.) is a separate hosted deployment — project files and any forwarded credentials live on the vendor's infrastructure, not the local disk.
Sources:
- https://containers.dev/ — "enabling both local and remote deployment across various cloud environments"
- https://containers.dev/supporting — "GitHub Codespaces — Commercial (GitHub/Microsoft). Cloud-hosted development environments"

### open_source (prose-only)
Spec docs: CC-BY-4.0. CLI and tooling: MIT. Fully self-hostable — it's just Docker plus a JSON config; no vendor account required for local use. Hosted variants (Codespaces, Ona/Gitpod, CodeSandbox) are separate commercial services layered on the same open spec.
Sources:
- https://github.com/devcontainers/spec — "CC-BY-4.0, MIT licenses found"

### maturity (prose-only)
CLI repo: 2.9k★, 426 forks, 126 release tags, 889 commits, active. Spec repo: 5.6k★, 482 forks, 279 commits, 150 open issues. Microsoft-backed (`devcontainers` GitHub org, copyright Microsoft Corporation) but positioned as a community-open spec with a public Slack. Broad ecosystem: VS Code, Visual Studio 2022 17.4+, JetBrains IDEs (early-stage), Emacs (community extension), GitHub Codespaces, CodeSandbox, DevPod, Ona/Gitpod.
Sources:
- https://github.com/devcontainers/cli — "Stars: 2.9k ... Releases: 126 tags"
- https://containers.dev/supporting — editor/service list with support levels

## B. Threat protection
### host_fs_damage
host_fs_damage: Partial — the container boundary isolates the host filesystem outside the mounted workspace, but the default sharing mode is a live bind mount, so anything inside the workspace is fully writable and changes land directly on the host disk; there's no default read-only or copy-on-write protection for the code being worked on. An alternate "clone in container volume" mode copies into an isolated Docker volume instead, avoiding host-FS writes for that use case.
Sources:
- https://code.claude.com/docs/en/devcontainer — "Claude can still modify any file in the bind-mounted workspace, which appears directly on your host"
- https://code.visualstudio.com/docs/devcontainers/containers — "Clone Repository in Container Volume" uses "isolated, local Docker volumes instead of binding to the local filesystem"

### credential_theft
credential_theft: Partial — mediation differs by credential type. SSH: agent forwarding (socket, not raw key file) is automatic if `ssh-agent` is running on host — the container never receives the private key material, only the forwarded agent socket. GPG: NOT mediated — the container is documented to read keys directly from the host's `~/.gnupg/` directory, i.e. actual key material is reachable inside the sandbox. Git HTTPS creds: reused transparently via the host's configured credential helper. Anthropic's own reference devcontainer explicitly warns against mounting `~/.ssh` or cloud credential files.
Sources:
- https://code.visualstudio.com/remote/advancedcontainers/sharing-git-credentials — "will automatically forward your local SSH agent if one is running"; "The container then accesses the keys from the host's `~/.gnupg/` directory"
- https://code.claude.com/docs/en/devcontainer — "Avoid mounting host secrets such as `~/.ssh` or cloud credential files into the container; prefer repository-scoped or short-lived tokens"

### data_exfiltration
data_exfiltration: No (as shipped) — the spec and CLI provide no network-egress control at all; a container gets whatever the underlying Docker network allows, which is unrestricted outbound by default. A default-deny egress firewall is achievable only via a documented, hand-maintained example (Anthropic's `init-firewall.sh`, an iptables/ipset script), not a spec or CLI feature — it requires manually adding `NET_ADMIN`/`NET_RAW` capabilities via `runArgs` and shipping/maintaining the script yourself.
Sources:
- https://code.claude.com/docs/en/devcontainer — "The firewall script and these capabilities are not required for Claude Code itself: you can leave them out and rely on your own network controls instead."
- https://code.claude.com/docs/en/sandbox-environments — "A custom Docker network plus a proxy env var is only a starting point... it does not restrict egress"

### malicious_execution
malicious_execution: Partial — blast radius is bounded by the standard Docker/runc container namespace boundary (shared kernel), same as any container — no additional sandboxing (seccomp beyond Docker's default profile, gVisor, etc.) is applied by the spec itself. `capAdd`/`securityOpt`/`privileged` are available as manual escape hatches that, if used (e.g. for Docker-in-Docker), widen rather than narrow the blast radius.
Sources:
- https://containers.dev/implementors/json_reference/ — "privileged: ... Required for things like Docker-in-Docker, but has security implications particularly when running directly on Linux"

### escape_resistance
escape_resistance: prose-heavy — shared-kernel container, not a microVM or hardware-virtualized boundary. Isolation boundary is standard Docker/runc: stronger than a bare host process but weaker than a VM. Anthropic's own sandbox-comparison docs explicitly place dev containers below "Virtual machine" for isolation strength and recommend a dedicated VM (or Firecracker microVM, or Claude Code's own Anthropic-hosted VM offering) specifically "when you are evaluating untrusted code" or "your security policy requires kernel-level separation." No CVE/escape-surface enumeration is offered by the devcontainers project itself (that's inherent to whatever container runtime Docker uses).
Sources:
- https://code.claude.com/docs/en/sandbox-environments — "Virtual machine ... provides the strongest separation, with its own kernel and, in cloud or microVM deployments, its own virtualized hardware... Use this approach when you are evaluating untrusted code, when your security policy requires kernel-level separation between the agent and the host"

### resource_abuse
resource_abuse: Partial — no default CPU/mem/disk limits and no first-class `devcontainer.json` property for them. `hostRequirements` only documents MINIMUM resources needed to run (informational, not an enforced ceiling). Actual limits are achievable by passing native Docker flags through the escape-hatch `runArgs` property (e.g. `"runArgs": ["--cpus=2", "--memory=4g"]`), which is Docker's own capability, not something the spec adds.
Sources:
- https://containers.dev/implementors/json_reference/ — general `runArgs`/`hostRequirements` property documentation confirms `runArgs` is raw Docker CLI passthrough
- https://github.com/microsoft/vscode-dev-containers/issues/640 — open community issue "Allow specifying dev containers resources (CPU, Memory)" (third-party/community evidence, not official docs — cited for corroboration only)

## C. Feature set & granularity
### network_default_posture
network_default_posture: open-by-default — the spec defines no network/egress policy concept; an unconfigured dev container gets full outbound internet access via Docker's normal bridge networking, identical to any `docker run` with no `--network` restrictions.
Sources:
- https://code.claude.com/docs/en/sandbox-environments — "A custom Docker network plus a proxy env var is only a starting point... by itself it does not restrict egress"

### egress_allowlist
egress_allowlist: Partial — not a spec/CLI feature; achievable only via the documented Anthropic example pattern (`init-firewall.sh`): iptables + ipset, default-deny (`OUTPUT DROP`), with an `allowed-domains` ipset populated once at container creation by resolving a fixed domain list (npm, api.anthropic.com, sentry.io, GitHub's published CIDR ranges via its meta API, VS Code marketplace, etc.) to A-records. Granularity: IP/CIDR-set only, ANY port/protocol to an allowlisted IP is accepted (`-m set --match-set allowed-domains dst -j ACCEPT` has no `--dport`/`-p` qualifier) — no subdomain-wildcard concept beyond whatever a domain resolves to, no deny-rule precedence system beyond default DROP, no path/method/regex layer. This is a hand-maintained shell script the user edits, not a declarative rule engine.
Sources:
- https://raw.githubusercontent.com/anthropics/claude-code/main/.devcontainer/init-firewall.sh — `iptables -P OUTPUT DROP`; `iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT`

### dns_level_blocking
dns_level_blocking: No — the reference firewall unconditionally allows DNS traffic (`-p udp --dport 53 -j ACCEPT` to any destination) and instead filters on the IP the domain resolves to, resolved once at setup time. It does not intercept or restrict DNS queries/resolution itself, so a disallowed domain still resolves successfully; only the resulting connection is dropped.
Sources:
- https://raw.githubusercontent.com/anthropics/claude-code/main/.devcontainer/init-firewall.sh — `iptables -A OUTPUT -p udp --dport 53 -j ACCEPT` (unconditional); domains "resolved once at container startup ... There is no continuous re-resolution mechanism"

### tls_mitm_inspection
tls_mitm_inspection: No — filtering happens at the network/IP layer only (iptables); the reference script does no TLS termination or certificate inspection, so it cannot make L7 (path/header) decisions.
Sources:
- https://raw.githubusercontent.com/anthropics/claude-code/main/.devcontainer/init-firewall.sh — rule set is entirely IP/port-set based; no proxy or cert material referenced

### http_path_rules
http_path_rules: No — no HTTP-layer control exists anywhere in the spec, CLI, or the commonly-used reference firewall; enforcement is IP-set based only.
Sources:
- https://raw.githubusercontent.com/anthropics/claude-code/main/.devcontainer/init-firewall.sh — no HTTP/path logic present in the rule set

### proto_coverage
proto_coverage: Partial — the reference firewall's default policy (`INPUT`/`FORWARD`/`OUTPUT` all `DROP`) is fail-closed across all protocols, but explicit allow rules are hand-coded per case: DNS (UDP/53, unconditional), SSH (TCP/22), loopback, host `/24` network, `ESTABLISHED,RELATED` state, and the allowlisted-IP set (any port/proto). No documented handling of ICMP, QUIC/HTTP-3 (UDP/443), gRPC, or WebSocket as distinct classes — they'd either fall under the catch-all IP-set rule (any-port/proto to an allowed IP) or be dropped by default if the destination IP isn't in the set. No documented extensibility model for adding new protocol types beyond hand-editing the shell script — it is not a rule-driven policy engine.
Sources:
- https://raw.githubusercontent.com/anthropics/claude-code/main/.devcontainer/init-firewall.sh — enumerated rules for UDP/53, TCP/22, loopback, host network, ESTABLISHED/RELATED, and the IP-set catch-all

### live_rule_reload
live_rule_reload: No — the script runs once during container creation/setup; there is no live-reload command or long-running process tracking rule changes. Adding a domain means editing the script and rebuilding (or manually re-`exec`ing it as root inside a running container with the `NET_ADMIN` capability already present — not a documented or supported workflow).
Sources:
- https://raw.githubusercontent.com/anthropics/claude-code/main/.devcontainer/init-firewall.sh — "resolves domains once at container startup... no continuous re-resolution mechanism"; "no persistence mechanism... recreated each time the script runs"

### firewall_escape_hatch
firewall_escape_hatch: No — all-or-nothing. There's no timed bypass or per-sandbox enable/disable toggle; the firewall is either wired into `devcontainer.json` (via `runArgs` capabilities + the script) or absent. Turning it off means editing the config and rebuilding the container, matching the guideline's explicit "all-or-nothing = No" case.
Sources:
- https://code.claude.com/docs/en/devcontainer — "you can leave them out and rely on your own network controls instead" (binary include/exclude, no mid-session toggle documented)

### enforcement_plane
enforcement_plane: kernel netfilter (iptables/ipset), running INSIDE the same container/kernel namespace as the agent process itself — not a separate proxy, sidecar, or host-level enforcement point. This requires granting the container `NET_ADMIN` and `NET_RAW` capabilities via `runArgs`. Because enforcement lives in the same namespace the agent operates in, an agent process with sufficient privilege could in principle modify or flush the very rules meant to contain it; the reference config mitigates this only by running the agent as a non-root `remoteUser` (so it lacks the capability at runtime), not by dropping the capability from the container after firewall setup. Traffic is not logged at this layer (see network_audit).
Sources:
- https://code.claude.com/docs/en/devcontainer — "Running a firewall inside a container requires extra permissions, so the reference adds the `NET_ADMIN` and `NET_RAW` capabilities through `runArgs`"
- https://code.claude.com/docs/en/devcontainer — "confirm `remoteUser` is set to a non-root account" (for `--dangerously-skip-permissions` use)

### fail_closed
fail_closed: Partial — once applied, the iptables rules are kernel state, not tied to a live supervising process, so they persist even if the setup script/process has since exited (no daemon needs to stay alive to keep enforcing). But this only holds after successful firewall initialization: the firewall is opt-in, not spec-enforced, so a config that omits it (or a setup failure before the `DROP` policies are set) leaves the container in its default fully-open state with nothing to fail closed. The script itself is fail-closed internally (`set -euo pipefail`; DNS/GitHub-API resolution failures abort setup).
Sources:
- https://raw.githubusercontent.com/anthropics/claude-code/main/.devcontainer/init-firewall.sh — `set -euo pipefail`; default policies set to `DROP` before allow rules are added; verification step confirms block/allow behavior

### network_audit
network_audit: No — the reference rule set uses plain `ACCEPT`/`DROP` actions; no `LOG` target or audit-log mechanism is present in the documented script, and neither the spec nor CLI provide one.
Sources:
- https://raw.githubusercontent.com/anthropics/claude-code/main/.devcontainer/init-firewall.sh — rule set enumerated in full; no logging rules present

### workspace_modes
workspace_modes: Yes — both live bind mount (default, changes reflect immediately on host) and an isolated named-Docker-volume mode ("Clone Repository in Container Volume," used to avoid host-FS pollution and improve I/O performance on Windows/macOS) are supported.
Sources:
- https://code.visualstudio.com/docs/devcontainers/containers — "Bind Mount (Local Development)... Named Volume (Isolated Development): The 'Clone Repository in Container Volume' feature uses isolated, local Docker volumes instead of binding to the local filesystem"

### observability
observability: No — the spec/CLI provide no built-in metrics/log/dashboard stack for observing agent activity; whatever visibility exists comes from the harness running inside the container (e.g. Claude Code's own optional OpenTelemetry export), not from devcontainers itself.
Sources:
- https://code.claude.com/docs/en/devcontainer — telemetry/monitoring is addressed only via links out to Claude Code's own docs ("Monitor usage and audit activity"), not a devcontainers feature

### supervision
supervision: No — no runtime supervisor process is defined by the spec. The editor client (VS Code, Codespaces, JetBrains) manages container lifecycle (start/stop/rebuild) but does not observe agent behavior or intervene mid-session; there is no containment/kill/quarantine primitive beyond a human manually stopping the container.
Sources:
- https://containers.dev/implementors/spec/ — lifecycle is limited to creation/start/attach hooks (`onCreateCommand`, `postCreateCommand`, `postStartCommand`, `postAttachCommand`); no active-monitoring concept described

### fleet_mgmt
fleet_mgmt: No — the spec is single-workspace/single-container oriented; no built-in naming/registry/lifecycle system for managing many concurrent agent sandboxes. Hosted services (GitHub Codespaces) provide their own instance listing at the platform level, but that is Codespaces' product surface, not a devcontainers-spec capability.
Sources:
- https://containers.dev/implementors/json_reference/ — properties are scoped to one container per configuration; no multi-instance registry construct in the spec

### snapshots_persistence
snapshots_persistence: Partial — named Docker volumes can persist specific paths (e.g. `~/.claude` config/auth/history) across container rebuilds, and containers can be stopped/restarted preserving state; but there is no built-in snapshot/pause-resume-diff command in the CLI — persistence is achieved by manually mounting a volume at the paths you want to survive a rebuild, not a general per-agent snapshot feature.
Sources:
- https://code.claude.com/docs/en/devcontainer — `"mounts": ["source=claude-code-config,target=/home/node/.claude,type=volume"]`; "the container's home directory is discarded on rebuild ... Mount a named volume at that path to keep this state"

## D. Setup (spectrum: trivial↔painful)
setup: Easy — install Docker Desktop/Engine + the VS Code Dev Containers extension (or `npm install -g @devcontainers/cli`), add a `devcontainer.json` (a one-image, few-line file suffices, or use a Template), then "Reopen in Container" / `devcontainer up`. For Claude Code specifically, adding one `features` block plus a rebuild is the full path to a sandboxed agent. The hardened reference container (firewall + persistent volumes) is a clone-and-reopen away.
Sources:
- https://code.claude.com/docs/en/devcontainer — 3-step "Create devcontainer.json → Rebuild → Sign in" walkthrough; reference container is "Clone → Reopen in Container → run `claude`"

## E. Daily use (spectrum: trivial↔painful)
daily_use: Easy-to-moderate — editor auto-attaches on folder open; day-to-day is "reopen in container" plus normal terminal/IDE use. Friction points are documented: config changes require an explicit "Rebuild Container" action (not automatic), bind-mount I/O has a real performance cost on Windows/macOS (mitigated by named volumes), and browser-based OAuth callbacks occasionally fail to route through forwarded ports, requiring a manual code-paste fallback.
Sources:
- https://code.visualstudio.com/docs/devcontainers/containers — bind-mount "performance overhead on Windows and macOS" vs named-volume "improved performance"
- https://code.claude.com/docs/en/devcontainer — "If the browser sign-in completes but the callback never reaches the container, copy the code shown in the browser and paste it..."

## F. Configuration
### config_depth
config_depth: Deep — `devcontainer.json` is a single versionable, repo-committed file covering base image/Dockerfile, Features, packages, `containerEnv`/`remoteEnv`, mounts, `forwardPorts`/`portsAttributes`, user (`containerUser`/`remoteUser`/`updateRemoteUserUID`), and a full lifecycle-hook set (`onCreateCommand`, `updateContentCommand`, `postCreateCommand`, `postStartCommand`, `postAttachCommand`). `runArgs` is an explicit escape hatch to arbitrary Docker CLI flags (capabilities, resource limits, custom networks, etc.) when the declarative surface isn't enough.
Sources:
- https://containers.dev/implementors/spec/ — full property set as fetched (mounts, env classes, lifecycle hooks, ports)
- https://containers.dev/implementors/json_reference/ — `runArgs`, `privileged`, `capAdd`, `securityOpt`, `containerUser`/`remoteUser`/`updateRemoteUserUID`

### policy_model
policy_model: Moderate, leaning rigid on security defaults — the config surface is very deep and per-project (each repo's `devcontainer.json` can differ freely, and `runArgs` provides a raw-Docker escape hatch), but there is no "secure by default" opinion anywhere in the spec (open network, no resource caps, no mandated non-root user) and no per-run policy toggle (e.g. "run this one session firewalled, this one not") — changing a security posture (add/remove the firewall, change `privileged`) means editing the file and rebuilding the container, not selecting a run-time flag.
Sources:
- https://containers.dev/implementors/json_reference/ — `privileged` defaults to false but is a static property, not a per-invocation switch
- https://code.claude.com/docs/en/sandbox-environments — "This is a convention rather than an enforcement boundary, because Claude Code does not require a container"

## G. DX — host↔sandbox integration
### bind_mount_sharing
bind_mount_sharing: Yes — live two-way bind mount is the default sharing mode; edits inside the container appear immediately on the host and vice versa. (An isolated named-volume mode is also offered — see workspace_modes.)
Sources:
- https://code.visualstudio.com/docs/devcontainers/containers — "workspace files are 'mounted from the local file system' into the container"

### cred_forwarding
cred_forwarding: Partial — Git HTTPS creds reused transparently via host credential helper; SSH via automatic agent-socket forwarding (mediated — no private key copied in); GPG via direct host `~/.gnupg/` directory access (unmediated — actual key material reachable inside the container), with a documented Windows caveat that keys configured only via Git Bash aren't visible to the container.
Sources:
- https://code.visualstudio.com/remote/advancedcontainers/sharing-git-credentials — "automatically forward your local SSH agent"; GPG "accesses the keys from the host's `~/.gnupg/` directory"; Windows Git-Bash caveat

### browser_auth
browser_auth: Yes — forwarded ports present as `localhost` to the containerized process, so OAuth/device-code flows that open a browser and expect a localhost callback work through VS Code's port-forwarding; documented fallback (manual code paste) exists for the case where the editor's forwarding doesn't route the callback correctly.
Sources:
- https://code.visualstudio.com/docs/devcontainers/containers — "Browser-based auth flows work through forwarded ports, which appear as localhost to containerized applications"
- https://code.claude.com/docs/en/devcontainer — callback-miss fallback instructions

### shared_dirs
shared_dirs: Yes — `mounts` accepts arbitrary additional bind mounts or named volumes beyond the workspace folder itself (used, e.g., to persist `~/.claude` across rebuilds).
Sources:
- https://containers.dev/implementors/spec/ — "Mounts allow containers to have access to the underlying machine, share data between containers and to persist information"

### git_worktrees
git_worktrees: Partial — not first-class; a linked worktree's `.git` is a file pointing outside the mounted folder, so plain "open this worktree folder" breaks git operations inside the container. A documented community workaround exists (mount the parent directory and mark the path `safe.directory`), and a native-support GitHub issue has been open on the reference CLI since it was filed, unresolved as of this research.
Sources:
- https://github.com/devcontainers/cli/issues/796 — open issue, "opening a devcontainer project within a git worktree fails because worktrees don't store `.git` as a directory"

### nested_containers
nested_containers: Yes — official `docker-in-docker` and `docker-outside-of-docker` Features exist specifically for this, using `${devcontainerId}`-scoped volumes for isolation between instances. Requires elevated container privileges (typically `privileged: true` or Docker-socket mounting) as a tradeoff.
Sources:
- https://containers.dev/implementors/features/ — "the `docker-in-docker` and `docker-outside-of-docker` Features as examples supporting nested container scenarios"

### harness_agnostic
harness_agnostic: Yes — the container is a plain Docker image; any coding-agent CLI can be installed and run inside it (Claude Code is one Feature among many possible installs, not a requirement). Note: the editor/orchestrator side (VS Code, Codespaces, JetBrains) must itself implement the Dev Containers protocol — plain editors without that support (e.g. Vim) are outside the workflow, per Claude Code's own docs.
Sources:
- https://code.claude.com/docs/en/devcontainer — "Editors without dev container support, such as plain Vim, are not part of this workflow"

## H. Performance
performance: Unknown quantitatively — no official startup-latency, RAM/CPU-overhead, or IO-throughput benchmarks were found in the spec, CLI, or VS Code docs. Qualitatively, VS Code's own docs acknowledge bind-mount I/O overhead specifically "on Windows and macOS" as a known cost, mitigated by the named-volume mode; no numbers are given for either mode.
Sources:
- https://code.visualstudio.com/docs/devcontainers/containers — bind mount "has performance overhead on Windows and macOS"; named volume "improved performance on Windows and macOS"

## I. Feasibility
feasibility: Adoptable today — cross-platform (macOS, Windows via Docker Desktop/WSL2, Linux via Docker CE/EE 18.06+), mature ecosystem (multiple IDEs, hosted services, MIT/CC-BY licensing, active repos since ~2019), low lock-in (it's a portable Docker image + JSON file, not a proprietary format — Codespaces/Ona/DevPod/CodeSandbox all consume the same config). Realistic prerequisite is simply "Docker installed," which is a real but common bar. Solo-dev adoption is straightforward per the setup walkthrough above.
Sources:
- https://code.visualstudio.com/docs/devcontainers/containers — platform/version prerequisite table (Docker Desktop 2.0+/CE 18.06+)
- https://containers.dev/supporting — multi-vendor ecosystem list

## J. Price (prose-only)
Spec, reference CLI, and VS Code's Dev Containers extension are all free and open source (MIT/CC-BY). Running locally against your own Docker install costs nothing beyond Docker itself (Docker Desktop has its own separate commercial-use licensing terms for large organizations, not a devcontainers cost). Hosted execution is a separate paid layer: GitHub Codespaces is commercial/usage-billed; CodeSandbox has a free tier (2 vCPU/2GB) plus paid tiers; Ona/Gitpod and JetBrains/Visual Studio IDE support are commercial products.
Sources:
- https://containers.dev/supporting — "Visual Studio Code — Free, open source"; "GitHub Codespaces — Commercial"; "CodeSandbox — Commercial... free tier includes 2 vCPUs + 2GB RAM"

## K. Extensibility
extensibility: Yes — Features (versioned, OCI-registry- or URL/local-path-distributed reusable install+config units with a `devcontainer-feature.json` + `install.sh` contract, dependency ordering via `dependsOn`/`installsAfter`) and Templates (starter configs) form a real package ecosystem; combined with arbitrary base images/Dockerfiles and the `runArgs` Docker-flag escape hatch, the customization surface is broad. No formal security vetting/sandboxing model is documented for third-party Features — `install.sh` runs as root with whatever the schema allows, so pulling an untrusted Feature is equivalent to running an untrusted root install script.
Sources:
- https://containers.dev/implementors/features/ — "self-contained, shareable units of installation code and development container configuration"; distribution via "OCI registries... HTTPS URIs... Local file paths"; "The specification does not detail a formal vetting or security model for third-party Features" (synthesized from fetched page, no vetting language present in spec text)

## Unknowns & caveats
- **network_audit / logging**: confirmed absent in the specific reference script fetched in full; cannot rule out that some other community firewall pattern adds logging — this assessment is scoped to the commonly-cited Anthropic reference per the provider note.
- **Capability-drop after firewall init**: docs do not describe whether `NET_ADMIN`/`NET_RAW` are stripped from the container after `init-firewall.sh` runs; treated as not-described rather than confirmed-absent, but flagged as a real tamper risk in the enforcement_plane writeup above.
- **Third-party Feature vetting**: containers.dev docs are silent on any registry-level scanning/signing for OCI-distributed Features; recorded as "no formal model documented" rather than a hard "No" for a hypothetical informal review process that may exist off-docs.
- **JetBrains/Visual Studio-specific behavior**: only VS Code's implementation was fetched in depth per the provider note ("VS Code implementation as commonly used"); other editors' exact mount/credential/port-forwarding behavior may differ and was not independently verified.
- **Performance numbers**: no quantitative benchmarks (cold/warm start time, RAM overhead, IO throughput) found anywhere in official docs — recorded as Unknown rather than estimated.
- No URLs were blocked; all fetches in this research succeeded (firewall bypass was active per the operational note for this run, but no destination required it — all sources were reachable).

# Clawker — self-hosted AI coding agent sandbox (run Claude Code, Codex & more in Docker)

<p align="center">
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go" alt="Go"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-AGPL%20v3-blue.svg" alt="License"></a>
  <a href="https://deepwiki.com/schmitthub/clawker"><img src="https://deepwiki.com/badge.svg" alt="Ask DeepWiki"></a>
  <a href="#"><img src="https://img.shields.io/badge/Platform-macOS-lightgrey?logo=apple" alt="macOS"></a>
  <a href="#"><img src="https://img.shields.io/badge/Platform-Linux-4DA3FF?logo=linux&logoColor=fff&labelColor=0057B8" alt="Linux"></a>
  <a href="#"><img src="https://img.shields.io/badge/Claude-D97757?logo=claude&logoColor=fff" alt="Claude"></a>
  <a href="#"><img src="https://img.shields.io/badge/Codex-000000?logo=openai&logoColor=fff" alt="Codex"></a>
  <img alt="Vibe coded with love" src="https://img.shields.io/badge/Vibe%20coded%20with-%F0%9F%92%97-1f1f1f?labelColor=ff69b4">
</p>

<p align="center">
<code>clawker</code> is a free, open-source, self-hosted <strong>AI coding agent sandbox</strong> — a cli that runs coding-agent harnesses (<code>Claude Code</code> and <code>OpenAI Codex</code> ship built-in, more on the way, and you can bring your own via harness bundles) in isolated <code>Docker</code> containers on your own machine, no cloud and no subscription. It pairs a deny-by-default egress firewall (Envoy + custom CoreDNS + eBPF) for prompt-injection and data-exfiltration protection with the convenience features you actually want: image building, monitoring, parallel git-worktree agents, and credential forwarding — a devcontainer alternative that's local, free, <em>and</em> security-deep, whatever model your harness talks to (including Anthropic's latest mythos-class <code>Fable 5</code>). It works on any MacOS/Linux host with docker installed. I wrote this because I didn't want to have to pay someone to run coding agents with <code>--dangerously-skip-permissions</code> when containers have been around for a decade, and the sandbox modes these harnesses ship are the temu version of a container. <code>clawker</code> offers many convenience features beyond just building and running an agent in a container (you never even have to write a Dockerfile, it's got you covered).
</p>

<div align="center">
  <img src="docs/assets/system-diagram.png" alt="system diagram" width="700">
</div>

## How clawker compares

clawker runs the coding-agent CLIs you already use — Claude Code, Codex, and more — inside a locked-down container: a deny-by-default egress firewall enforced in the kernel, network and agent observability, and host-seamless DX. Here's how that stacks up against the CLIs' own built-in sandboxing and other tools built to contain a coding agent.

Every ✅ is a capability the sandbox **itself** implements — hover it for the mechanism. ❌ means absent or undocumented. Assessed from vendor documentation (2026-07); cited notes in-repo. Wide table — scroll horizontally →

| Solution | Cost | Local | Open source | Self-hostable | Deny-by-default egress | Allowlist exists | Domain allowlist rules | Subdomain wildcard | IP/CIDR | Port scoping | Deny rules | HTTP path | HTTP method | Regex path | DNS-level block | Domain-native | TLS MITM | Kernel-level enforcement | Fail-closed firewall | Live firewall reload | Timed auto-bypass | Filters DNS | Filters TCP | Filters UDP | Filters QUIC | Filters ICMP | Filters SSH | Filters WebSocket | Per-request audit log | Metrics dashboard | Harness telemetry capture | Extensible monitoring | Active supervision | Fleet registry | SSH-agent fwd | GPG-agent fwd | Git-cred fwd | Cred-injection proxy | Host-browser auth | Live bind-mount | Ephemeral snapshot | Git-worktree mgmt | Harness seeding | Shared host state | Declarative config | Custom image/Dockerfile | Lifecycle hooks | Plugin/bundle system | Any agent CLI | Durable agent state |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **clawker** | Free (OSS) | <abbr title="Docker containers run on the user's local Docker host; code/creds never leave the machine">✅</abbr> | <abbr title="AGPL-3.0-or-later with dual-licensing CLA (#380)">✅</abbr> | <abbr title="Runs entirely on user's own Docker host; fully self-hosted by nature, no hosted tier">✅</abbr> | <abbr title="Deny-by-default egress: unconfigured container resolves/routes only allowlisted destinations">✅</abbr> | <abbr title="Egress allowlist in clawker.yaml security.firewall (add_domains + rules)">✅</abbr> | <abbr title="Domain/hostname allow rules enforced by CoreDNS + Envoy">✅</abbr> | <abbr title="Leading-dot wildcard rules (.example.com) match subdomains plus apex">✅</abbr> | <abbr title="Explicit IP/CIDR rule type in the firewall rule ladder">✅</abbr> | <abbr title="proto+port and inclusive port-range scoping per rule">✅</abbr> | <abbr title="Explicit deny rules with documented precedence (deny-wins-in-tier)">✅</abbr> | <abbr title="path_rules: literal prefix + path_default deny=allowlist mode (http/https/ws/wss)">✅</abbr> | <abbr title="methods enum per path rule gates HTTP verbs">✅</abbr> | <abbr title="'~'-prefixed full-anchored RE2 regex path matching">✅</abbr> | <abbr title="CoreDNS returns NXDOMAIN for unlisted domains">✅</abbr> | <abbr title="CoreDNS policy + Envoy SNI/Host + dynamic-forward-proxy; no resolve-once IP snapshot">✅</abbr> | <abbr title="Envoy always MITMs TLS with preset CA bundle for L7 inspection">✅</abbr> | <abbr title="eBPF cgroup connect/sendmsg/recvmsg/sock_create hooks pinned host-side; agent cannot detach">✅</abbr> | <abbr title="CP crash leaves pinned eBPF enforcing last ruleset (documented invariant)">✅</abbr> | <abbr title="clawker firewall add/refresh live-applies rules without restart">✅</abbr> | <abbr title="Timed bypass with auto-expiry plus per-agent disable/enable break-glass">✅</abbr> | <abbr title="CoreDNS enforces allow/deny DNS policy and logs queries">✅</abbr> | <abbr title="eBPF redirects TCP egress to Envoy for rule enforcement">✅</abbr> | <abbr title="UDP routing through envoy_udp path in eBPF/Envoy">✅</abbr> | <abbr title="QUIC routing handled via UDP/envoy_udp proto coverage">✅</abbr> | <abbr title="ICMP structurally blocked via eBPF raw-socket (sock_create) denial">✅</abbr> | <abbr title="ssh is a first-class L7 proto in firewall rules (proto+port scoping)">✅</abbr> | <abbr title="ws/wss first-class L7 protos with path rules">✅</abbr> | <abbr title="Per-request Envoy access logs + CoreDNS query logs via netlogger">✅</abbr> | <abbr title="OTel->Prometheus+OpenSearch monitor stack with preconfigured dashboards">✅</abbr> | <abbr title="Ingests Claude Code OTel: prompts, MCPs, skills, slash-cmds, tool decisions/outcomes, cost, model">✅</abbr> | <abbr title="Extensible monitoring via bundles; custom collectors/exporters/dashboards">✅</abbr> | <abbr title="CP↔clawkerd sessions: observation, command dispatch, containment">✅</abbr> | <abbr title="Hierarchical clawker.project.agent naming, project registry, lifecycle CLI">✅</abbr> | <abbr title="SSH agent socket bridge; key never enters container">✅</abbr> | <abbr title="GPG agent socket bridge; key never enters container">✅</abbr> | <abbr title="Git HTTPS forwarding via host proxy as a managed credential feature">✅</abbr> | ❌ | <abbr title="Host proxy round-trips browser-open + callback automatically (e.g. gh auth login)">✅</abbr> | <abbr title="bind workspace mode = live two-way host mount">✅</abbr> | <abbr title="snapshot workspace mode = ephemeral disposable copy">✅</abbr> | <abbr title="clawker worktree subcommands with a container per worktree">✅</abbr> | <abbr title="Host harness settings/plugins/creds copied into container at create time">✅</abbr> | <abbr title="Host state seeded/mounted (managed-config copy + CC memories mounted in sync)">✅</abbr> | <abbr title="clawker.yaml per-project config, versionable with JSON schema">✅</abbr> | <abbr title="Custom Dockerfile injection points (inject after_from/after_packages)">✅</abbr> | <abbr title="post_init / pre_run lifecycle hooks in agent config">✅</abbr> | <abbr title="Bundles (harnesses/stacks/monitoring) + plugin system">✅</abbr> | <abbr title="Harness-agnostic multi-harness via harness.yaml authoring (claude default)">✅</abbr> | <abbr title="Config + shell history persist in named Docker volumes across recreation">✅</abbr> |
| Docker Sandboxes | Free / $ org tier | <abbr title="microVM runs on the developer's own machine (macOS/Windows/Linux host), not Docker cloud">✅</abbr> | ❌ | ❌ | <abbr title="default Balanced mode blocks all outbound HTTP/HTTPS unless explicitly allowlisted (deny-by-default)">✅</abbr> | <abbr title="`sbx policy allow network` accepts hostname/domain/IP allowlist entries">✅</abbr> | <abbr title="allow/deny rules accept exact domains, e.g. example.com">✅</abbr> | <abbr title="wildcard subdomain rules `*.example.com` supported">✅</abbr> | <abbr title="IP and CIDR-block rules (CIDR matched against resolved IPs)">✅</abbr> | <abbr title="optional port suffix on rules, e.g. example.com:443">✅</abbr> | <abbr title="sbx policy deny network <host> — distinct deny verb alongside allow">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="host proxy matches Host header / CONNECT hostname against rules at request time">✅</abbr> | <abbr title="proxy terminates TLS, inspects host header, re-encrypts (MITM by design)">✅</abbr> | <abbr title="enforcement host-side outside the VM (proxy + net-layer UDP/ICMP block); in-guest agent can't route around">✅</abbr> | ❌ | <abbr title="local policy changes take effect immediately, no sandbox restart">✅</abbr> | ❌ | ❌ | <abbr title="non-HTTP TCP egress controlled via IP+port rules">✅</abbr> | <abbr title="UDP egress unconditionally blocked at the network layer">✅</abbr> | ❌ | <abbr title="ICMP egress unconditionally blocked at the network layer">✅</abbr> | <abbr title="SSH egress controlled via IP+port rules (use IP:22)">✅</abbr> | ❌ | <abbr title="`sbx policy log` records per-request allowed/blocked egress with a PROXY path column">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="`sbx ls` lists named sandboxes; per-sandbox run/stop/rm/exec lifecycle">✅</abbr> | <abbr title="host SSH_AUTH_SOCK forwarded; sandbox requests signatures, private key never leaves host">✅</abbr> | ❌ | ❌ | <abbr title="host proxy injects credential headers from host keychain; VM sees only a sentinel value">✅</abbr> | <abbr title="supported agents' OAuth login runs host-side; browser opens on host, token never enters the VM">✅</abbr> | <abbr title="direct mode = live two-way filesystem passthrough bind mount at same absolute path">✅</abbr> | <abbr title="clone mode (`--clone`) = read-only host mount + private in-VM clone copy">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="templates built from a Dockerfile (or saved from a running sandbox), pushed to a registry">✅</abbr> | <abbr title="kit install commands run at sandbox-creation time (setup commands); experimental">✅</abbr> | <abbr title="kits are shareable YAML extension bundles; community `sbx-kits-contrib` repo">✅</abbr> | <abbr title="six first-class agents plus documented custom-agent definitions">✅</abbr> | <abbr title="installed packages, config changes, and command history persist across sbx stop/restart cycles">✅</abbr> |
| Nono | Free (OSS) | <abbr title="OS-level sandbox (Landlock/Seatbelt) runs the agent process directly on the user's machine; no cloud">✅</abbr> | <abbr title="Apache-2.0 licensed (github.com/nolabs-ai/nono)">✅</abbr> | <abbr title="Executes entirely on user's own machine/OS; no vendor cloud dependency for running">✅</abbr> | ❌ | <abbr title="--allow-domain / network.allow_domain allowlist enforced by supervisor HTTP proxy">✅</abbr> | <abbr title="Hostname rules via --allow-domain (e.g. api.openai.com allows all paths on domain)">✅</abbr> | <abbr title="Wildcard suffix patterns e.g. *.internal.example">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Path-glob rules on domains/endpoints e.g. https://github.com/org/** and /repos/*/issues/**">✅</abbr> | <abbr title="Per-route HTTP method gating e.g. openai:POST:/v1/chat/completions">✅</abbr> | ❌ | ❌ | <abbr title="Supervisor forward proxy matches request hostname and resolves DNS itself per request (dynamic forward proxy)">✅</abbr> | <abbr title="Selective TLS interception when an endpoint/path L7 rule requires plaintext visibility">✅</abbr> | <abbr title="Landlock v4 per-port TCP / macOS Seatbelt restrict connect() to only the proxy port at kernel level">✅</abbr> | <abbr title="If proxy/supervisor dies, kernel connect()-restriction persists so child loses all egress (fail-closed)">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="All outbound TCP blocked at kernel except supervisor proxy, which applies domain/path allowlist">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Proxy logs every ALLOW/DENY egress decision with reason; cryptographic tamper-evident audit trail">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Unsandboxed parent supervisor intercepts syscalls via seccomp-notify + proxy, dynamically denies agent operations at runtime">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="Local reverse proxy injects real API keys into outbound requests; agent sees only a phantom token, secret held outside sandbox">✅</abbr> | <abbr title="--allow-launch-services opens host browser for OAuth; proxy captures token at boundary, stores it outside, injects phantom">✅</abbr> | <abbr title="Agent granted in-place read/write to host cwd via Landlock/Seatbelt; edits are the real host files (live)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="Declarative versionable JSONC profiles with extends inheritance (filesystem/network/credentials/commands sections)">✅</abbr> | ❌ | ❌ | <abbr title="Packs: signed bundles of profiles, hooks, plugins distributed via the nono registry">✅</abbr> | <abbr title="Harness-agnostic: runs Claude Code, Codex, opencode, openclaw, Goose, Copilot, Qwen, Pi, Hermes via profiles">✅</abbr> | ❌ |
| Agentbox (mattolson) | Free (OSS) | <abbr title="Agent runs in a local Docker/Colima container on the user's machine via agentbox exec">✅</abbr> | <abbr title="MIT License (repo mattolson/agent-sandbox)">✅</abbr> | <abbr title="Runs entirely on user's own Docker/Colima; no vendor cloud">✅</abbr> | <abbr title="Default-deny egress; explicit YAML allowlist, non-matching requests get 403">✅</abbr> | <abbr title="YAML allowlist (services+domains) enforced by mitmproxy sidecar enforcer.py">✅</abbr> | <abbr title="Policy 'domains' list matches exact hostnames">✅</abbr> | <abbr title="Wildcard host patterns *.example.com match apex and any subdomain">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Per-host path rules (exact/prefix) checked by mitmproxy on decrypted requests">✅</abbr> | <abbr title="Per-rule HTTP method gating (methods: [GET, POST])">✅</abbr> | ❌ | ❌ | <abbr title="mitmproxy checks HTTPS CONNECT tunnel host/SNI against policy at request time (forward proxy)">✅</abbr> | <abbr title="mitmproxy sidecar MITMs TLS to inspect decrypted HTTP/HTTPS requests">✅</abbr> | <abbr title="iptables firewall (init-firewall.sh) blocks all direct outbound from agent container">✅</abbr> | <abbr title="iptables default-deny persists in kernel; proxy death leaves agent with no egress path (fail-closed)">✅</abbr> | <abbr title="Policy hot-reload; agentbox proxy reload sends SIGHUP, no container restart">✅</abbr> | ❌ | ❌ | <abbr title="iptables blocks all direct outbound TCP; only proxied HTTP/HTTPS egress permitted">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="ADR 002: iptables blocks all outbound SSH (port 22); git URLs rewritten to HTTPS">✅</abbr> | ❌ | <abbr title="mitmproxy sidecar logs each proxied request/enforcement decision; view via agentbox proxy logs">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Proxy injects git credential at request time via git-askpass shim; token stays host-side">✅</abbr> | <abbr title="Proxy-side secret injection: API keys held in host dir, injected into outbound requests, never in agent container">✅</abbr> | ❌ | <abbr title="Repo workspace bind-mounted read/write into container; edits shared live">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Optional dotfiles/shell.d mount: host ~/.config/agent-sandbox/dotfiles symlinked into container home">✅</abbr> | <abbr title="Per-project .agent-sandbox/ layered policy YAML + compose files (versionable)">✅</abbr> | <abbr title="Local/custom images via ./images/build.sh + agentbox edit compose to reference them">✅</abbr> | ❌ | ❌ | <abbr title="Supports many agent CLIs (Claude Code, Codex, Gemini, OpenCode, Factory, Copilot...) via agentbox switch">✅</abbr> | <abbr title="Persistent per-agent state volume preserves auth/config across container restarts">✅</abbr> |
| Sculptor (Imbue) | Free (OSS) | <abbr title="Electron GUI + local FastAPI backend + agent-runner subprocess all run on the user's own machine">✅</abbr> | <abbr title="MIT licensed; source at github.com/imbue-ai/sculptor">✅</abbr> | <abbr title="Runs locally on the user's machine by default; opt-in remote/Docker backend is self-managed">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Workspace list UI + sculpt CLI workspace/agent scoping (-w, SCULPT_WORKSPACE_ID/AGENT_ID) + <user>/<slug> branches">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Workspace IS a git worktree by default; dedicated branch-naming/target/cleanup management">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="Per-repo setup commands (e.g. npm install) run automatically at workspace creation">✅</abbr> | <abbr title="JS UI extensions (folder/URL/`sculpt extension load`) + bundled skill bundles + Claude skill auto-discovery">✅</abbr> | ❌ | <abbr title="SQLite + append-only snapshot log persists every agent session; workspaces persist under ~/.sculptor/workspaces/">✅</abbr> |
| Dev Containers | Free (OSS) | <abbr title="Runs on developer's local Docker Desktop/Engine by default; code+secrets stay in a local container">✅</abbr> | <abbr title="Spec docs CC-BY-4.0; reference CLI and tooling MIT">✅</abbr> | <abbr title="Just Docker plus a devcontainer.json; no vendor account required for local/self-hosted use">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Automatic SSH-agent socket forwarding; container gets the agent socket, never the private key file">✅</abbr> | ❌ | <abbr title="Git HTTPS creds reused transparently via the host's configured credential helper">✅</abbr> | ❌ | <abbr title="VS Code port forwarding maps container localhost so OAuth browser callback routes back automatically">✅</abbr> | <abbr title="Live two-way bind mount is the default workspace sharing mode; edits reflect both ways">✅</abbr> | <abbr title="'Clone Repository in Container Volume' copies repo into an isolated Docker volume instead of binding">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="devcontainer.json is a single versionable, repo-committed per-project config file">✅</abbr> | <abbr title="devcontainer.json supports a custom base image or Dockerfile">✅</abbr> | <abbr title="onCreate/updateContent/postCreate/postStart/postAttach command lifecycle hooks">✅</abbr> | <abbr title="Features (versioned OCI/URL/local install units) plus Templates form a package ecosystem">✅</abbr> | <abbr title="Plain Docker image; any coding-agent CLI can be installed, not tied to one harness">✅</abbr> | <abbr title="Named-volume mount at config path (e.g. ~/.claude) persists config/history across container rebuilds">✅</abbr> |
| SmolVM | Free (OSS) | <abbr title="SmolVM runs entirely on the developer's own Linux/macOS host; no remote execution mode for SmolVM itself">✅</abbr> | <abbr title="Apache 2.0 licensed; source at github.com/CelestoAI/SmolVM">✅</abbr> | <abbr title="Self-hosted local install is its only mode (pip install smolvm / install script)">✅</abbr> | ❌ | <abbr title="internet_settings.allowed_domains outbound domain allowlist">✅</abbr> | <abbr title="allowed_domains entries are hostnames/domains (protocols/paths normalized off)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="host-side nftables (smolvm_nat/smolvm_filter) on per-sandbox TAP device, outside guest control">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="smolvm ui local dashboard: VM status, resource usage, logs">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="smolvm claude/codex start auto-forwards host git credentials into sandbox">✅</abbr> | ❌ | ❌ | <abbr title="--writable-mounts: writable host-dir mount, edits reflect on host live both ways">✅</abbr> | <abbr title="default read-only mount: writes go to VM overlay, never touch host (ephemeral)">✅</abbr> | ❌ | <abbr title="smolvm claude/codex/hermes/pi start preinstalls harness + forwards login creds">✅</abbr> | <abbr title="coding-agent presets forward host CLI login creds; opt-in env-var forwarding of host secrets">✅</abbr> | ❌ | <abbr title="DockerRootfsBuilder custom Dockerfile / BootImage prebuilt disk images">✅</abbr> | ❌ | ❌ | <abbr title="arbitrary harness via DockerRootfsBuilder custom image; not locked to one vendor CLI">✅</abbr> | ❌ |
| Claude Code sandbox | Free (needs Claude Code) | <abbr title="Sandboxed Bash runs in-place on developer's own machine via Seatbelt/bubblewrap; no code leaves host">✅</abbr> | <abbr title="`@anthropic-ai/sandbox-runtime` engine is Apache-2.0 on GitHub (Claude Code CLI itself proprietary)">✅</abbr> | <abbr title="OS sandbox runs entirely on the user's own machine/WSL2; no vendor-cloud dependency">✅</abbr> | <abbr title="No domains pre-allowed by default; first use of each new domain prompts for approval">✅</abbr> | <abbr title="`allowedDomains` egress allowlist in settings.json">✅</abbr> | <abbr title="`allowedDomains` entries are domain/hostname rules">✅</abbr> | <abbr title="`allowedDomains` supports `*.github.com` subdomain wildcard syntax">✅</abbr> | ❌ | ❌ | <abbr title="`deniedDomains` block even when a broader `allowedDomains` wildcard would permit (deny overrides)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="Built-in forward proxy enforces allowlist on client-supplied hostname at request time">✅</abbr> | ❌ | <abbr title="Seatbelt/bubblewrap + network namespaces deny direct sockets; agent can't route around external proxy">✅</abbr> | ❌ | ❌ | <abbr title="Per-command `dangerouslyDisableSandbox` break-glass; sandbox auto-re-applies to subsequent commands">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="`mask` holds secret outside sandbox; external proxy injects real secret only on requests to `injectHosts`">✅</abbr> | ❌ | <abbr title="Sandbox operates directly on the real working directory; allowed writes are immediately host files">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="Versionable per-project `.claude/settings.json` sandbox config">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ |
| Codex CLI sandbox | Free (needs Codex) | <abbr title="Codex CLI runs as an ordinary host process on the dev machine, sandboxed in-place by Seatbelt/Landlock">✅</abbr> | <abbr title="Apache-2.0 licensed source at github.com/openai/codex">✅</abbr> | <abbr title="CLI+sandbox run entirely on the user's own machine; only model inference calls reach OpenAI">✅</abbr> | <abbr title="workspace-write sandbox blocks all egress; sandbox_workspace_write.network_access defaults to false">✅</abbr> | <abbr title="opt-in features.network_proxy.domains map<host,allow\|deny> via loopback HTTP/SOCKS proxy">✅</abbr> | <abbr title="network_proxy.domains entries keyed by hostname, e.g. example.com=allow">✅</abbr> | <abbr title="network_proxy.domains supports *.example.com and **.example.com wildcard entries">✅</abbr> | ❌ | ❌ | <abbr title="domains map accepts deny entries with documented deny-wins-on-conflict precedence">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="userspace codex-network-proxy (HTTP/SOCKS) enforces domain policy on hostname at request time">✅</abbr> | ❌ | <abbr title="seccomp BPF + Landlock (Linux) / Seatbelt MAC gate the network on/off switch at kernel level">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="OpenTelemetry log export includes per-decision network_proxy allow/deny events (opt-in)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="config.toml (sandbox mode, permission profiles, network/domain policy) with system>user>project layering">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ |
| Anthropic srt | Free (OSS) | <abbr title="srt wraps the target process on the developer's own machine via OS primitives; no remote/cloud mode">✅</abbr> | <abbr title="Apache-2.0, public repo anthropic-experimental/sandbox-runtime">✅</abbr> | <abbr title="local npm package (npm i -g @anthropic-ai/sandbox-runtime); no vendor server/cloud component">✅</abbr> | <abbr title="empty allowedDomains = zero egress; all network denied until explicitly allowlisted">✅</abbr> | <abbr title="network.allowedDomains outbound allow-list">✅</abbr> | <abbr title="allowedDomains entries are domain/hostname rules">✅</abbr> | <abbr title="allowedDomains supports wildcards like *.example.com">✅</abbr> | ❌ | ❌ | <abbr title="network.deniedDomains checked first, take precedence over allowedDomains">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="HTTP/SOCKS5 proxy filters by domain (SNI/Host) at connect time, not a resolve-once IP snapshot">✅</abbr> | <abbr title="experimental opt-in network.tlsTerminate intercepts HTTPS CONNECTs; custom CA via extraCaCertPaths">✅</abbr> | <abbr title="macOS Seatbelt / Linux bubblewrap+seccomp BPF / Windows WFP kernel fence egress to proxy only">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="SOCKS5 proxy + OS egress fence gate all TCP to allowlisted domains">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="versionable JSON settings file (~/.srt-settings.json or --settings <path>)">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="srt <command> wraps any arbitrary CLI/executable; not tied to one vendor harness">✅</abbr> | ❌ |

<details>
<summary>How this was assessed</summary>

Each cell reflects the vendor's official documentation as of 2026-07. ✅ = the sandbox itself ships the capability (hover for the mechanism); ❌ = absent or undocumented. The comparison covers tools that sandbox a coding-agent CLI, plus the CLIs' own built-in sandboxing — code-execution sandboxes and programmatic SDKs are a separate category. Full per-provider notes with citations are in-repo under `.serena/memories/agent-sandbox-research/`.

</details>

> **Why "credential injection" isn't containment**
>
> Some sandboxes keep secrets on the host and inject them into outbound requests, so the agent never sees the raw value. It reads well — until you notice the agent-facing CLIs mint live tokens on demand: `gh auth token`, `aws configure export-credentials`, `az account get-access-token`, `gcloud auth print-access-token`. Now the agent holds a real, replayable credential.
>
> If egress is allowed at the *domain* level — all of `github.com`, all of `s3.amazonaws.com` — that token (and any repo it can read) goes straight to an attacker-controlled bucket, repo, or gist on the very same trusted domain. The injection layer was never in the path.
>
> Hiding the secret in transit is not the same as containing the agent. Containment means scoping **where** authenticated requests can go — `github.com/your-org/` with method gating and a per-request audit log — and mediating the primitive itself (SSH/GPG agent sockets) so no replayable token exists in the first place. clawker does both.

> Read more about clawker's threat model and security philosophy at [docs.clawker.dev/threat-model](https://docs.clawker.dev/threat-model)

> ! Clawker is in an early development stage, but it's usable and has a lot of features. Expect breaking changes and rough edges. I quickly patch regressions that were missed. If you want to contribute or have any feedback, please open an issue or a pull request! Give it a star if you find it useful so I can brag about them at parties

---

## Table of Contents

- [Clawker — self-hosted AI coding agent sandbox (run Claude Code, Codex \& more in Docker)](#clawker--self-hosted-ai-coding-agent-sandbox-run-claude-code-codex--more-in-docker)
  - [Table of Contents](#table-of-contents)
  - [High-Level Feature Overview](#high-level-feature-overview)
  - [Installation](#installation)
  - [Quick Start](#quick-start)
  - [Walkthrough](#walkthrough)
    - [Initialize a project](#initialize-a-project)
      - [Run a container](#run-a-container)
  - [Creating and Using Containers](#creating-and-using-containers)
  - [The `@` Image Shortcut](#the--image-shortcut)
  - [Command Aliases](#command-aliases)
  - [Working with Worktrees](#working-with-worktrees)
  - [Managing Resources](#managing-resources)
  - [Monitoring](#monitoring)
  - [Roadmap / Known Issues](#roadmap--known-issues)
  - [Contributing](#contributing)
  - [License](#license)

---

<details>
<summary>Boring TLDR manifesto</summary>
The rise of Agentic AI has been meteoric, but in the rush to ship model harnesses, the industry is skipping the risks and responsibilities that come with them. They’re avoiding dependency pain by shipping bare-metal software, when the harness itself needs a harness. LLMs are powerful, but they’re also unpredictable, naive, and easy to coerce—and handing one unrestricted code execution, network access, software install rights, internet reach, and full filesystem access to unsuspecting users is reckless. As a security engineer, I want my own machine protected, so clawker is the harness for the harness: an "agent-in-container" solution and a practical example of secure-by-default guardrails for agentic software. I hope this project inspires the industry to prioritize containerization natively in their agentic software offerings, and to build more tools that make it easy and seamless for users to run agents in containers with strong security defaults.
</details>

## High-Level Feature Overview

- **Multi-harness by design** — `Claude Code` and `OpenAI Codex` ship as embedded **harness bundles**, and any coding-agent CLI can be added by authoring a bundle (a manifest + Dockerfile fragment + optional assets) and declaring it in your project's `clawker.yaml`. Images are harness-keyed: `clawker build -t codex` builds a specific harness, `clawker run @:codex` runs it, and the default harness carries a `:default` alias so bare `@` just works
- **No Dockerfile to write** — images build on a pinned Debian substrate with common tools preinstalled (git, curl, vim, zsh, ripgrep, etc.): a shared per-project base image carries your `build.packages`, language **stacks** (go, node, python, rust, java, ruby, cpp, dotnet), and custom instructions, with a thin per-harness image layered on top. The per-container `clawkerd` daemon runs as PID 1, handles signal forwarding, drops privilege to the unprivileged `clawker` user kernel-side, and supervises the harness for the container's lifetime
- **Per-host clawker control plane** (`clawker-controlplane` container) runs as a long-lived supervisor — it owns the firewall lifecycle, eBPF program lifetime, agent identity registry (sqlite), mTLS auth, and the command channel to every agent's `clawkerd`. The CLI talks to it over mTLS gRPC + OAuth2; see `clawker controlplane status`, `clawker controlplane agents`
- **Injectable build-time instructions** to customize images per project: packages, environment variables, root run commands, user run commands, and more
- **Bind or snapshot workspace modes**: mount your repository to the container for live editing, or copy it at runtime for pure isolation
- **Fresh or copy agent mode**: start the harness with a clean slate, or stage your host settings, plugins, and skills into the container at create time for a seamless transition from doing work in a host instance to a container (the claude harness stages settings, CLAUDE.md, agents, skills, commands, and plugins). Credentials are never copied — you authenticate once inside the container (browser flows are proxied to your host) and the login persists in the harness's config volume across restarts and recreates
- **Seamless Git credential forwarding**: toggleable SSH agent, GPG agent forwarding from the host using muxrpc (just like devcontainers) for zero-config access to private repositories and commit signing
- **Host proxy service** sends events like "browser open" from the container to your host for browser authentication, then proxies the callback back to the container. Great for when you have to authenticate with your harness (`claude`, `codex`) or `gh`
- **Configurable environment variables**: set or copy environment variables and env files from the host into containers at runtime
- **Injectable post-initialization bash script** that runs after the container starts but before the harness launches, letting you set up MCPs, etc.
- **Envoy + custom CoreDNS + eBPF network firewall** enabled by default — Envoy and a custom CoreDNS build run as managed Docker containers on the shared `clawker-net` network, while eBPF cgroup programs (loaded and attached from outside agent containers by the control plane) redirect TCP to Envoy and DNS to CoreDNS. Provides DNS-level deny-by-default (unlisted domains return NXDOMAIN), per-domain TCP routing via a real-time BPF DNS cache, and TLS inspection with per-domain MITM certificates for path-level filtering. Agent containers themselves get **no Linux capabilities** — all enforcement happens kernel-side, outside the container's privilege scope. Each harness bundle ships its own egress floor (the claude harness allows the Anthropic API + OAuth domains, codex the OpenAI ones); project rules merge additively. Manage rules dynamically with `clawker firewall add/remove/list/status` (or `clawker firewall refresh` to live-apply project config egress edits), temporarily bypass with `clawker firewall bypass 5m --agent <agent_name>`, or disable entirely. A great security layer to mitigate runaway agents or prompt injections while giving them the network access they need.
- **Toggleable read-only global share**: volume mount from the host giving all containers real-time access to files you place in it
- **Project-based namespace isolation** of container resources. Clawker detects if it's in a project directory and automatically, via docker label prefixes, lets you filter for resources with re-usable names like "dev" or "main" that are scoped to the project. So you can have a "dev" container in multiple projects without conflict, and you can easily filter `clawker ps --filter agent=dev` to see all your dev containers across projects or `clawker ps --project myapp` to see all containers for a specific project.
- **Dedicated Docker network** that all containers run in
- **Jailed from host Docker resources** via `pkg/whail` (whale jail), a standalone package that decorates the moby SDK to prevent callers from seeing resources without the automatically applied management labels. I might use this package in other "agent in container" solutions. So I don't have to worry about accidentally deleting non-clawker managed containers/volumes/images, etc.
- **Command aliases** — one-word shortcuts expanded to full clawker invocations with `$1..$N` positional placeholders. Ships with `go` (disposable default-harness agent: `clawker go dev`), `wt` (agent on a fresh worktree: `clawker wt auth feature/auth:main`), and per-harness `claude`/`codex` (harness plus its auto-approve flag in one word) out of the box; define your own with `clawker alias set` and commit them to the project config with `clawker alias export` so the whole team gets them
- **Docker CLI-esque commands** for managing containers, Clawker isn't a passthrough to Docker CLI; it uses the moby SDK (via `pkg/whail`). This allowed me to add more flags, modify the behavior, etc over what docker cli offers
- **Git worktree management and commands**: pass a worktree flag to container run or create commands to automatically create a git worktree in the Clawker home project directory and bind mount it to the container workdir. Also has cli commands and flags to list and manage worktrees created by clawker, uses `go-git` under the hood to avoid relying on the host git binary. Worktree containers ship extra security lockdown for unattended sessions — see [worktree caveats](https://docs.clawker.dev/worktrees#worktree-caveats)
- **Optional monitoring stack** — OTel Collector + OpenSearch (logs) + OpenSearch Dashboards + Prometheus (metrics) on `clawker-net`. Every container has the environment variables baked in to push OTLP telemetry when the stack is running, and is silenced when it isn't
- **Interactive configuration editing**: TUI-based editors for project config (`clawker project edit`) and user settings (`clawker settings edit`) with tabbed field browsing, per-field type-appropriate editors (text, boolean, list, multiline), layer-aware provenance display showing which file each value comes from, and per-field save targeting to choose which config layer to write to

## Installation

**Prerequisites:** Docker must be installed and running on your machine. I've tested all features on macOS. I have confirmed it works on Linux just not extensively. Windows is not currently supported but I might in the future (yucky).

**Homebrew** (macOS):
```bash
brew install schmitthub/tap/clawker
```

**Install script** (macOS / Linux):
```bash
curl -fsSL https://raw.githubusercontent.com/schmitthub/clawker/main/scripts/install.sh | bash
```

<details>
<summary>More options</summary>

**Specific version:**
```bash
curl -fsSL https://raw.githubusercontent.com/schmitthub/clawker/main/scripts/install.sh | CLAWKER_VERSION=v0.1.3 bash
```

**Custom directory:**
```bash
curl -fsSL https://raw.githubusercontent.com/schmitthub/clawker/main/scripts/install.sh | CLAWKER_INSTALL_DIR=$HOME/.local/bin bash
```

**Build from source** (requires Go 1.25+):
```bash
git clone https://github.com/schmitthub/clawker.git
cd clawker && make clawker
export PATH="$PWD/bin:$PATH"
```
</details>

## Quick Start

The fastest path to a seamless containerized coding agent, with your host settings, plugins, and skills staged in so you can get to work right away. On first run you authenticate inside the container — the browser flow pops on your host automatically, and the login persists in the agent's config volume from then on.

```bash
cd your-project

# Optional but recommended: set up monitoring to get logs and metrics from your containers
clawker monitor init && clawker monitor up

clawker init 
clawker build
clawker go dev
```

> [!NOTE]
> The `go` command is a built-in alias for:  
>
> ```bash
> clawker run --rm -it --agent $1 @
> ```
>
> So `clawker go dev` expands to the full command above with `$1=dev`. The flags mean:  
>
> - `-it` — interactive mode with a terminal attached
> - `--rm` — removes the container when it finishes (recommended, volumes are preserved)
> - `--agent dev` — names this container `clawker.<project>.dev`
> - `@` — shortcut that resolves to your built default-harness image (`clawker-<project>:default`; outside a project it resolves to the global image from a global `clawker build`). Use `@:codex` to pick a specific harness instead
>
> Anything after the `@` is passed straight to the harness CLI, and arguments after an alias are appended — with the out-of-box claude default, `clawker go dev -c` continues your previous Claude Code session and `clawker go dev --dangerously-skip-permissions` hands it the infamous yolo flag, safe inside the container's isolation.
>
> The per-harness aliases `claude` and `codex` pick their harness and skip its permission prompts in one word: `clawker claude dev`, `clawker codex dev`. The other built-in alias `wt` spawns an agent container in a worktree automatically. For example: `clawker wt feat feat/feat` (use or create branch `feat/feat`, tracking a matching remote branch when one exists) or `clawker wt auth feature/auth:main` (to create it off a base branch)
>
> Clawker ships [command aliases](/aliases) that expand to full invocations, and you can define your own with `clawker alias set`. See the [Command Aliases](/aliases) guide.

If you want to learn more about image customization, worktree support, monitoring, and other bells and whistles, keep reading for the walkthrough below.

You can ask your coding agent to assist you in writing a more appropriate config file for the project using the support skill `clawker plugin install` (recommended) or this prompt:  

```text
create a `./.clawker.yaml` file appropriate for this repos stack. Clawker configuration can be understood here: https://docs.clawker.dev/configuration.md
```

## Walkthrough

Here are ways I'm using `clawker` today and how I'm finding it useful. 

### Initialize a project

```bash
cd your-project
clawker init            # Guided setup: pick a language preset → creates .clawker.yaml, .clawkerignore, registers project
```

`clawker init` walks you through a guided setup with language-based presets (Python, Go, Rust, TypeScript, Java, Ruby, C/C++, C#/.NET, Bare). Choose a preset or "Build from scratch" to customize every field. User settings (`~/.config/clawker/settings.yaml`) and XDG directories are bootstrapped automatically on first run.

> **Tip:** Install the **clawker-support plugin** to get hands-on help from a clawker specialist agent. It can walk you through configuration, MCP wiring, firewall rules, troubleshooting, and more — it reads the real build templates and config schema and gives you the exact YAML you need.
> ```bash
> # Via clawker CLI (recommended)
> clawker plugin install
>
> # Or manually
> claude plugin marketplace add schmitthub/clawker-plugin
> claude plugin install clawker-support@schmitthub-plugins
> ```
> You can also customize your image using `clawker project edit` or point your agent at the LLM-friendly [docs site](https://docs.clawker.dev/configuration) for the full config reference. I dogfood clawker to build clawker, so also check out my `clawker.yaml` to see how I customized the build config for golang development.

> **Tip** You can alternatively use `.clawker/clawker.yaml` (which takes precedence). You can also split the configs up into multiple files through your repository for merging, good for monorepos. A global clawker.yaml can also be created in `$CLAWKER_CONFIG_DIR` for system wide defaults. You can also create an uncomitted `.clawker.local.yaml|.clawker/clawker.local.yaml` for local-only overrides.

```bash
clawker build           # Builds your project's default-harness image (referenced as "@" when within a project directory)
clawker build -t codex  # Builds a specific harness instead; run it with @:codex
```

Builds are two-stage: a shared `clawker-<project>:base` image holds your packages, stacks, and custom instructions, and each harness image (`clawker-<project>:claude`, `clawker-<project>:codex`, ...) layers on top of it. The default harness build also stamps the `:default` alias that bare `@` resolves.

#### Run a container 

My workflow is a hybrid approach. I like having a claude code instance running on the host for real intensive interactive work while at the same time launching a few clawker managed containers in separate tabs and worktrees using `--dangerously-skip-permissions`. (Claude Code is my daily driver, so the walkthrough is narrated in claude terms — swap in `@:codex` / `clawker codex` and everything below works the same.)

So to do that let's say you're working on a feature branch with host claude code and inspiration strikes or you notice an issue / bug and say "shit i should address this". Or you've finished up a few PRDs and want to bang them out in parallel. I just quickly open a tab and have another claude agent via clawker get after it on the side without me having to approve anything over and over again so...

```bash
clawker run -it --rm --agent dev --worktree hotfix/example:main @ --dangerously-skip-permissions
# or with the shipped alias — arguments after it pass straight through to the harness:
clawker wt dev hotfix/example:main --dangerously-skip-permissions
```

This creates and attaches my terminal to a new claude instance isolated in a container environment with a git worktree dir created under `~/.local/share/clawker/worktrees/` (or honors the override `$CLAWKER_DATA_DIR`) off of my main branch. Since it has all my plugins, skills, git creds, mcps, build deps instantly (and my in-container login persisted in its config volume), it's just a matter of telling the little rascal what to do and letting it go bananas and create a pr about it. I'll periodically check in on it to see how it's doing in another tab. Or you can detach `ctrl p+q` and return to your terminal; to reattach to the same session use `clawker attach --agent dev`. Ez pz no ssh/tmux bullshit, no vscode devcontainer window, no VPS with heavy IO latency, or setting up dedicated servers, or having to pay someone to do it for you.  

> Worktree containers mask `.git/hooks` and `.git/config` read-only — a security measure that keeps unattended agents from planting host-executable git hooks/config. It changes a few git behaviors inside the container (notably `git push -u` won't persist upstream tracking). Read the [worktree caveats](https://docs.clawker.dev/worktrees#worktree-caveats) before your first session.

I can see my worktree paths and open them in an IDE if I want to do some manual work or review the code... or never care about where they are, `clawker` remembers and auto mounts them using branches as an identifier. You can use `clawker worktree` commands to manage them, or `git worktree`. 

```bash
$ clawker worktree list
BRANCH     PATH                                                                      HEAD     MODIFIED     STATUS
a/example  /Users/schmitthub/.local/share/clawker/worktrees/repo-project-uuidsha256  f20aa37  1 hour ago   healthy
```

When I'm done I easily remove the worktree 

```bash
clawker worktree remove a/example --delete-branch  # this deletes the worktree and the branch since it was only for this worktree, if you want to keep the branch just omit the flag. Delete won't work if the branch isn't fully merged
```

If I plan on having long sessions with many agents ripping through features and fixes and want a high level overview of my coding armada I start the monitoring stack (need to do this before starting the containers — Claude Code, notably, doesn't retry if it can't establish a telemetry connection)

```bash
clawker monitor init
clawker monitor up
clawker monitor status 
# stop it later on 
clawker monitor down
```

Now I can go to OpenSearch Dashboards at http://localhost:5601 and inspect logs from every agent — costs, tokens, tool executions, decisions, prompts, api calls — and pull metrics from Prometheus at http://localhost:9090. (you can also set env vars in your host shell and it will report to this stack)

```bash
# Host ENV var example
# Add these to your shell profile / .env etc
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
OTEL_METRICS_EXPORTER=otlp
OTEL_LOGS_EXPORTER=otlp
OTEL_TRACES_EXPORTER=otlp
OTEL_LOGS_EXPORT_INTERVAL=5000
OTEL_METRIC_EXPORT_INTERVAL=10000
OTEL_METRICS_INCLUDE_ACCOUNT_UUID=true
OTEL_METRICS_INCLUDE_SESSION_ID=true
CLAUDE_CODE_ENABLE_TELEMETRY=1
CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1
OTEL_LOG_TOOL_DETAILS=1
OTEL_LOG_USER_PROMPTS=1

# Add this to a project level .env 
PROJECT_NAME=MyGroundbreakingTodoApp
OTEL_RESOURCE_ATTRIBUTES=service.name=claude-code,project=$PROJECT_NAME,agent=host
```

When I'm done I can commit / push / open a PR right in the container terminal with all my creds and git access set up, or I can open the worktree in my IDE and do it from there. I can `/exit` out and the container will stop (or `ctrl c` in the terminal). I can use `--rm` flags just like docker cli to automatically remove containers when they stop, or I can start the same one back up again with `clawker start -a -i --agent example` to pick up right where I left off.

All containers get named volume mounts for the harness's config directories (declared by its bundle — `~/.claude` for claude, `~/.codex` for codex) and command history for persistence.

```bash
$ clawker volume ls
VOLUME NAME                                  DRIVER  MOUNTPOINT
clawker.clawker.example-claude.config        local   ...volumes/clawker.clawker.example-claude.config/_data
clawker.clawker.example-history              local   ...r/volumes/clawker.clawker.example-history/_data
# You can see the resources naming conventions here (clawker.{project}.{agent}). Labeling works
# similarly. Volumes a harness owns carry its name too, so each harness keeps its own config.
```

You can also see how clawker is jailed from other docker resource access...

```bash
$ docker create alpine:latest
6c6896073eb1a2baa91450d0b5b795808f0ea4a052f729383a2d166d87fa0c17
$ clawker ps -a
NAME                     STATUS                  PROJECT                AGENT                  IMAGE                    CREATED
clawker.clawker.example  exited                  clawker                example                clawker-clawker:default  9 hours ago
$ docker ps -a 
CONTAINER ID   IMAGE                     COMMAND                  CREATED         STATUS                    PORTS     NAMES
6c6896073eb1   alpine:latest             "/bin/sh"                7 seconds ago   Created                             great_dubinsky
73b4ac14c2b3   clawker-clawker:default   "/usr/local/bin/clawk…"   10 hours ago    Exited (0) 10 hours ago             clawker.clawker.example
```

## Creating and Using Containers

```bash
# Create a fresh container and connect interactively
# The @ symbol auto-resolves your project's default-harness image (clawker-<project>:default)
clawker run -it --agent main @

# Detach without stopping: Ctrl+P, Ctrl+Q

# Re-attach to the agent
clawker attach --agent main

# Stop the agent (Ctrl+C exits the agent and stops the container)
# Or from another terminal:
clawker stop --agent main

# Start a stopped agent and attach
clawker start -a -i --agent main
```

## The `@` Image Shortcut

Use `@` anywhere an image argument is expected to auto-resolve your project's image:

```bash
clawker run -it @                     # Uses clawker-<project>:default (the default harness)
clawker run -it --agent dev @         # Same, with agent name
clawker run -it --agent dev @:codex   # Pick a specific harness
clawker container create --agent test @
```

## Command Aliases

Aliases are shortcuts expanded before execution — the alias value is appended to `clawker` in place of the alias name, with `$1`..`$N` positional placeholders and extra arguments appended. Four ship as defaults:

```bash
clawker go dev                       # → clawker run --rm -it --agent dev @
clawker wt auth feature/auth:main    # → clawker run --rm -it --agent auth --worktree feature/auth:main @
clawker claude dev                   # → clawker run --rm -it --agent dev @:claude --dangerously-skip-permissions
clawker codex dev                    # → clawker run --rm -it --agent dev @:codex --yolo
```

Define your own and share them with your team via the project config:

```bash
clawker alias set lg "logs \$1 --tail \$2"   # personal alias (user-level clawker.yaml)
clawker lg web 50                            # → clawker logs web --tail 50

clawker alias list                           # NAME / EXPANSION / SOURCE
clawker alias export                         # publish active aliases into the project's .clawker.yaml
clawker alias delete lg                      # remove from every config file that defines it
```

Aliases defined in a repository's project config apply automatically to everyone working in that project. Full guide: [docs.clawker.dev/aliases](https://docs.clawker.dev/aliases)

## Working with Worktrees

Run separate agents per git worktree for parallel development. Worktree containers apply extra security lockdown (read-only `.git/hooks` + `.git/config` masks) to make unattended sessions safer — see [worktree caveats](https://docs.clawker.dev/worktrees#worktree-caveats) for the behavioral differences:

```bash
# Use the --worktree flag for automatic worktree creation and mounting in containers
clawker run --worktree feature/todo-apps-are-dope:main -it --agent todo-apps @ --dangerously-skip-permissions

# Create worktrees manually
clawker worktree add feature/todo-apps-are-dope
clawker worktree add feat-feet --base main

# list your worktrees
clawker worktree list
```

## Managing Resources

As close to docker CLI and its flags as I could make it, but remember they do different things under the hood. Adding all features is also still a WIP

```bash
clawker ps                          # List all clawker containers
clawker container ls                # Same thing
clawker container stop --agent NAME
clawker image ls                    # List clawker images
clawker volume ls                   # List clawker volumes

# Firewall management
clawker firewall status             # Health, rule count, running containers
clawker firewall list               # List active egress rules
clawker firewall add docs.clawker.dev # Allow a domain
clawker firewall remove docs.clawker.dev
clawker firewall refresh            # Live-apply project config egress edits (no restart)
clawker firewall disable --agent dev   # Unrestricted egress for one agent
clawker firewall enable --agent dev    # Re-apply firewall rules
clawker firewall bypass 5m --agent dev      # Temporary unrestricted egress with auto-re-enable
clawker firewall bypass --stop --agent dev  # End bypass early, re-enable firewall

# Control plane (break-glass — normally bootstrapped automatically)
clawker controlplane status             # Show CP health + firewall subsystem state
clawker controlplane up                 # Bring CP up (idempotent)
clawker controlplane down               # Stop CP cleanly (drains eBPF + Envoy/CoreDNS)
clawker controlplane agents             # List agents registered with the CP

# Auth material
clawker auth rotate                     # Rotate CA, server certs, and OAuth2 signing key

# Configuration editing
clawker project edit                    # Interactive TUI editor for .clawker.yaml
clawker settings edit                   # Interactive TUI editor for settings.yaml

# Plugin management (alias: clawker skill)
clawker plugin install                  # Install the clawker-support agent skills plugin
clawker plugin install --scope project  # Install with project scope
clawker plugin show                     # Show manual install commands
clawker plugin remove                   # Remove the clawker-support plugin
```

## Monitoring 

All containers have the environment variables to push logs and metrics to an OpenTelemetry collector by default. The optional monitoring stack runs four Docker Compose services on `clawker-net`: the **OTEL Collector** (receivers + routing), **OpenSearch** (logs), **OpenSearch Dashboards** (UI over OpenSearch), and **Prometheus** (metrics + UI). Agent containers push OTLP/HTTP to the collector (Claude Code ships first-class OTel telemetry), which writes logs to OpenSearch and exposes a Prometheus scrape endpoint. See [`docs/monitoring.mdx`](docs/monitoring.mdx) for the full pipeline reference.

```bash
clawker monitor init
clawker monitor up
clawker monitor status 
# stop it later on 
clawker monitor down
```

Once the stack is up:

- **OpenSearch Dashboards** — http://localhost:5601 — Discover view for log exploration
- **Prometheus UI** — http://localhost:9090 — metrics + ad-hoc PromQL
- **OpenSearch API** — http://localhost:9200 — REST access to the `claude-code` (Claude Code logs), `clawker-cli` (host CLI logs), `clawkercp` (control-plane logs), `clawker-envoy` (firewall egress access logs), `clawker-coredns` (firewall DNS query logs), and `clawker-ebpf-egress` (eBPF egress decisions) indices

> **Preconfigured out-of-box.** Every `monitor up` runs a one-shot `clawker-opensearch-bootstrap` container that applies index templates (with explicit field mappings per source), ingest pipelines, a default 7-day ISM retention policy, a `clawker_prometheus` direct-query datasource, and a **`Clawker` analytics workspace** with index patterns + example visualizations imported. `otel-collector` and `prometheus` don't start until bootstrap exits cleanly.
>
> **Get into the workspace:** from the OSD splash / welcome screen click **Clawker** under the **Analytics** panel on the far right. **See logs or metrics:** in the workspace UI's left navbar, under **Explore**, click **Logs** or **Metrics**.
>
> Three dashboards ship preinstalled under the workspace's **Dashboards** view: **Claude Code Cost & Usage** (sessions, cost, token counters), **Claude Code Activity** (tool usage, code edits, hooks, MCP, plugins), and **Clawker Networking** (Envoy access logs, CoreDNS query log, eBPF egress decisions). Build additional dashboards off the index patterns and Prometheus datasource as needed.

## Roadmap / Known Issues

- More shipped harness bundles are on the way — experimental versions under development (codex, opencode, pi) live in the [example bundle](https://github.com/schmitthub/clawker-bundle-example). Try them with `clawker bundle install schmitthub/clawker-bundle-example`, and fork the repo or use it as a reference to tweak your own with the clawker plugin's `bundle-creator` skill
- Linux works but hasn't been exercised as extensively as macOS

See [GitHub Issues](https://github.com/schmitthub/clawker/issues?q=is%3Aissue+is%3Aopen) for current known issues and limitations.

## Contributing

Contributions welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, testing, and PR process.

Please read our [Code of Conduct](CODE_OF_CONDUCT.md) before participating.

## License

Clawker is free software: GNU Affero General Public License v3.0 or later (AGPL-3.0-or-later) — see [LICENSE](LICENSE).

One subproject is the exception: the clawker-support plugin, tracked as the `clawker-plugin/` git submodule ([schmitthub/clawker-plugin](https://github.com/schmitthub/clawker-plugin)), is licensed separately under the MIT License — see its LICENSE. Everything else in this repository is AGPL-3.0-or-later as described below.

The AGPL's network-use clause (section 13) is deliberate: if you run a modified Clawker as a network service, you must offer its source to users of that service. This keeps Clawker free and open — for learning from and building on, not for closed SaaS wrappers.

**Commercial licensing.** Don't want the AGPL's copyleft and network-use obligations — for example, to embed Clawker in a closed-source product or service? A commercial license is available. Contact andrew@ajschmitt.io.

**Contributing.** Contributions are accepted under a Contributor License Agreement ([CLA.md](CLA.md)): you keep your copyright, your work is published under the AGPL, and you grant the maintainer the right to also offer it under a commercial license. This is what keeps dual-licensing possible.

> I feel obligated to state this... **Clawker** is a portmanteau of Claude + Docker, spelled phonetically because `claucker` violates the phonetic rules of English and just doesn't roll off the fingers. The name predates the `clawdbot` `openclaw` `clawthis` `clawthat` naming craze and has no relation to openclaw.  

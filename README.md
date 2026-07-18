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

Every cell is a yes/no fact assessed against the vendor's **official documentation** (2026-07; per-provider notes with citations in-repo). ✅ = the sandbox **itself implements** the capability — never merely "possible because it's a Linux box," and a harness/CLI feature surfacing inside a sandbox is credited to the harness, not the sandbox. Hover any ✅ for its implementing mechanism. ❌ = absent **or undocumented**. Wide table — scroll horizontally.

| Solution | Local | Open source | Self-hostable | Deny-by-default egress | Allowlist exists | Domain allowlist rules | Subdomain wildcard | IP/CIDR | Port scoping | Deny rules | HTTP path | HTTP method | Regex path | DNS-level block | Domain-native | TLS MITM | Kernel-level enforcement | Fail-closed firewall | Live firewall reload | Timed auto-bypass | Filters DNS | Filters TCP | Filters UDP | Filters QUIC | Filters ICMP | Filters SSH | Filters WebSocket | Per-request audit log | Agent-activity dashboard | Active supervision | Fleet registry | SSH-agent fwd | GPG-agent fwd | Git-cred fwd | Cred-injection proxy | Host-browser auth | Live bind-mount | Ephemeral snapshot | Git-worktree mgmt | Harness seeding | Shared host state | Extra host-dir mounts | Declarative config | Custom image/Dockerfile | Lifecycle hooks | Plugin/bundle system | Any agent CLI | Env snapshot/resume | Durable agent state |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| **clawker** | <abbr title="Docker containers run on the user's local Docker host; code/creds never leave the machine">✅</abbr> | <abbr title="AGPL-3.0-or-later with dual-licensing CLA (#380)">✅</abbr> | <abbr title="Runs entirely on user's own Docker host; fully self-hosted by nature, no hosted tier">✅</abbr> | <abbr title="Deny-by-default egress: unconfigured container resolves/routes only allowlisted destinations">✅</abbr> | <abbr title="Egress allowlist in clawker.yaml security.firewall (add_domains + rules)">✅</abbr> | <abbr title="Domain/hostname allow rules enforced by CoreDNS + Envoy">✅</abbr> | <abbr title="Leading-dot wildcard rules (.example.com) match subdomains plus apex">✅</abbr> | <abbr title="Explicit IP/CIDR rule type in the firewall rule ladder">✅</abbr> | <abbr title="proto+port and inclusive port-range scoping per rule">✅</abbr> | <abbr title="Explicit deny rules with documented precedence (deny-wins-in-tier)">✅</abbr> | <abbr title="path_rules: literal prefix + path_default deny=allowlist mode (http/https/ws/wss)">✅</abbr> | <abbr title="methods enum per path rule gates HTTP verbs">✅</abbr> | <abbr title="'~'-prefixed full-anchored RE2 regex path matching">✅</abbr> | <abbr title="CoreDNS returns NXDOMAIN for unlisted domains">✅</abbr> | <abbr title="CoreDNS policy + Envoy SNI/Host + dynamic-forward-proxy; no resolve-once IP snapshot">✅</abbr> | <abbr title="Envoy always MITMs TLS with preset CA bundle for L7 inspection">✅</abbr> | <abbr title="eBPF cgroup connect/sendmsg/recvmsg/sock_create hooks pinned host-side; agent cannot detach">✅</abbr> | <abbr title="CP crash leaves pinned eBPF enforcing last ruleset (documented invariant)">✅</abbr> | <abbr title="clawker firewall add/refresh live-applies rules without restart">✅</abbr> | <abbr title="Timed bypass with auto-expiry plus per-agent disable/enable break-glass">✅</abbr> | <abbr title="CoreDNS enforces allow/deny DNS policy and logs queries">✅</abbr> | <abbr title="eBPF redirects TCP egress to Envoy for rule enforcement">✅</abbr> | <abbr title="UDP routing through envoy_udp path in eBPF/Envoy">✅</abbr> | <abbr title="QUIC routing handled via UDP/envoy_udp proto coverage">✅</abbr> | <abbr title="ICMP structurally blocked via eBPF raw-socket (sock_create) denial">✅</abbr> | <abbr title="ssh is a first-class L7 proto in firewall rules (proto+port scoping)">✅</abbr> | <abbr title="ws/wss first-class L7 protos with path rules">✅</abbr> | <abbr title="Per-request Envoy access logs + CoreDNS query logs via netlogger">✅</abbr> | <abbr title="monitor stack: OTel to Prometheus + OpenSearch with preconfigured dashboards">✅</abbr> | <abbr title="CP↔clawkerd sessions: observation, command dispatch, containment">✅</abbr> | <abbr title="Hierarchical clawker.project.agent naming, project registry, lifecycle CLI">✅</abbr> | <abbr title="SSH agent socket bridge; key never enters container">✅</abbr> | <abbr title="GPG agent socket bridge; key never enters container">✅</abbr> | <abbr title="Git HTTPS forwarding via host proxy as a managed credential feature">✅</abbr> | ❌ | <abbr title="Host proxy round-trips browser-open + callback automatically (e.g. gh auth login)">✅</abbr> | <abbr title="bind workspace mode = live two-way host mount">✅</abbr> | <abbr title="snapshot workspace mode = ephemeral disposable copy">✅</abbr> | <abbr title="clawker worktree subcommands with a container per worktree">✅</abbr> | <abbr title="Host harness settings/plugins/creds copied into container at create time">✅</abbr> | <abbr title="Host state seeded/mounted (managed-config copy + CC memories mounted in sync)">✅</abbr> | ❌ | <abbr title="clawker.yaml per-project config, versionable with JSON schema">✅</abbr> | <abbr title="Custom Dockerfile injection points (inject after_from/after_packages)">✅</abbr> | <abbr title="post_init / pre_run lifecycle hooks in agent config">✅</abbr> | <abbr title="Bundles (harnesses/stacks/monitoring) + plugin system">✅</abbr> | <abbr title="Harness-agnostic multi-harness via harness.yaml authoring (claude default)">✅</abbr> | ❌ | <abbr title="Config + shell history persist in named Docker volumes across recreation">✅</abbr> |
| Docker Sandboxes | <abbr title="microVM runs on the developer's own machine (macOS/Windows/Linux host), not Docker cloud">✅</abbr> | ❌ | ❌ | <abbr title="default Balanced mode blocks all outbound HTTP/HTTPS unless explicitly allowlisted (deny-by-default)">✅</abbr> | <abbr title="`sbx policy allow network` accepts hostname/domain/IP allowlist entries">✅</abbr> | <abbr title="allow/deny rules accept exact domains, e.g. example.com">✅</abbr> | <abbr title="wildcard subdomain rules `*.example.com` supported">✅</abbr> | <abbr title="IP and CIDR-block rules (CIDR matched against resolved IPs)">✅</abbr> | <abbr title="optional port suffix on rules, e.g. example.com:443">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="host proxy matches Host header / CONNECT hostname against rules at request time">✅</abbr> | <abbr title="proxy terminates TLS, inspects host header, re-encrypts (MITM by design)">✅</abbr> | <abbr title="enforcement host-side outside the VM (proxy + net-layer UDP/ICMP block); in-guest agent can't route around">✅</abbr> | ❌ | <abbr title="local policy changes take effect immediately, no sandbox restart">✅</abbr> | ❌ | ❌ | <abbr title="non-HTTP TCP egress controlled via IP+port rules">✅</abbr> | <abbr title="UDP egress unconditionally blocked at the network layer">✅</abbr> | ❌ | <abbr title="ICMP egress unconditionally blocked at the network layer">✅</abbr> | <abbr title="SSH egress controlled via IP+port rules (use IP:22)">✅</abbr> | ❌ | <abbr title="`sbx policy log` records per-request allowed/blocked egress with a PROXY path column">✅</abbr> | ❌ | ❌ | <abbr title="`sbx ls` lists named sandboxes; per-sandbox run/stop/rm/exec lifecycle">✅</abbr> | <abbr title="host SSH_AUTH_SOCK forwarded; sandbox requests signatures, private key never leaves host">✅</abbr> | ❌ | ❌ | <abbr title="host proxy injects credential headers from host keychain; VM sees only a sentinel value">✅</abbr> | <abbr title="supported agents' OAuth login runs host-side; browser opens on host, token never enters the VM">✅</abbr> | <abbr title="direct mode = live two-way filesystem passthrough bind mount at same absolute path">✅</abbr> | <abbr title="clone mode (`--clone`) = read-only host mount + private in-VM clone copy">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="additional host dirs mountable alongside workspace, e.g. ~/shared:ro">✅</abbr> | <abbr title="kits: experimental YAML manifests declaring installs, file drops, network/cred rules">✅</abbr> | <abbr title="templates built from a Dockerfile (or saved from a running sandbox), pushed to a registry">✅</abbr> | ❌ | <abbr title="kits are shareable YAML extension bundles; community `sbx-kits-contrib` repo">✅</abbr> | <abbr title="six first-class agents plus documented custom-agent definitions">✅</abbr> | ❌ | ❌ |
| Claude Code sandbox | <abbr title="Sandboxed Bash runs in-place on developer's own machine via Seatbelt/bubblewrap; no code leaves host">✅</abbr> | <abbr title="`@anthropic-ai/sandbox-runtime` engine is Apache-2.0 on GitHub (Claude Code CLI itself proprietary)">✅</abbr> | <abbr title="OS sandbox runs entirely on the user's own machine/WSL2; no vendor-cloud dependency">✅</abbr> | <abbr title="No domains pre-allowed by default; first use of each new domain prompts for approval">✅</abbr> | <abbr title="`allowedDomains` egress allowlist in settings.json">✅</abbr> | <abbr title="`allowedDomains` entries are domain/hostname rules">✅</abbr> | <abbr title="`allowedDomains` supports `*.github.com` subdomain wildcard syntax">✅</abbr> | ❌ | ❌ | <abbr title="`deniedDomains` block even when a broader `allowedDomains` wildcard would permit (deny overrides)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="Built-in forward proxy enforces allowlist on client-supplied hostname at request time">✅</abbr> | ❌ | <abbr title="Seatbelt/bubblewrap + network namespaces deny direct sockets; agent can't route around external proxy">✅</abbr> | ❌ | ❌ | <abbr title="Per-command `dangerouslyDisableSandbox` break-glass; sandbox auto-re-applies to subsequent commands">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="`mask` holds secret outside sandbox; external proxy injects real secret only on requests to `injectHosts`">✅</abbr> | ❌ | <abbr title="Sandbox operates directly on the real working directory; allowed writes are immediately host files">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="`filesystem.allowRead`/`allowWrite` grant access to additional host paths beyond the workspace">✅</abbr> | <abbr title="Versionable per-project `.claude/settings.json` sandbox config">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Standalone `sandbox-runtime` npm package sandboxes arbitrary agents/processes, not just Claude Code">✅</abbr> | ❌ | ❌ |
| Codex CLI sandbox | <abbr title="Codex CLI runs as an ordinary host process on the dev machine, sandboxed in-place by Seatbelt/Landlock">✅</abbr> | <abbr title="Apache-2.0 licensed source at github.com/openai/codex">✅</abbr> | <abbr title="CLI+sandbox run entirely on the user's own machine; only model inference calls reach OpenAI">✅</abbr> | <abbr title="workspace-write sandbox blocks all egress; sandbox_workspace_write.network_access defaults to false">✅</abbr> | <abbr title="opt-in features.network_proxy.domains map<host,allow\|deny> via loopback HTTP/SOCKS proxy">✅</abbr> | <abbr title="network_proxy.domains entries keyed by hostname, e.g. example.com=allow">✅</abbr> | <abbr title="network_proxy.domains supports *.example.com and **.example.com wildcard entries">✅</abbr> | ❌ | ❌ | <abbr title="domains map accepts deny entries with documented deny-wins-on-conflict precedence">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="userspace codex-network-proxy (HTTP/SOCKS) enforces domain policy on hostname at request time">✅</abbr> | ❌ | <abbr title="seccomp BPF + Landlock (Linux) / Seatbelt MAC gate the network on/off switch at kernel level">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="OpenTelemetry log export includes per-decision network_proxy allow/deny events (opt-in)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="sandbox_workspace_write.writable_roots array grants sandbox access to additional dirs beyond workspace">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Anthropic srt | <abbr title="srt wraps the target process on the developer's own machine via OS primitives; no remote/cloud mode">✅</abbr> | <abbr title="Apache-2.0, public repo anthropic-experimental/sandbox-runtime">✅</abbr> | <abbr title="local npm package (npm i -g @anthropic-ai/sandbox-runtime); no vendor server/cloud component">✅</abbr> | <abbr title="empty allowedDomains = zero egress; all network denied until explicitly allowlisted">✅</abbr> | <abbr title="network.allowedDomains outbound allow-list">✅</abbr> | <abbr title="allowedDomains entries are domain/hostname rules">✅</abbr> | <abbr title="allowedDomains supports wildcards like *.example.com">✅</abbr> | ❌ | ❌ | <abbr title="network.deniedDomains checked first, take precedence over allowedDomains">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="HTTP/SOCKS5 proxy filters by domain (SNI/Host) at connect time, not a resolve-once IP snapshot">✅</abbr> | <abbr title="experimental opt-in network.tlsTerminate intercepts HTTPS CONNECTs; custom CA via extraCaCertPaths">✅</abbr> | <abbr title="macOS Seatbelt / Linux bubblewrap+seccomp BPF / Windows WFP kernel fence egress to proxy only">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="SOCKS5 proxy + OS egress fence gate all TCP to allowlisted domains">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="filesystem.allowRead/allowWrite expose arbitrary additional host dirs, no workspace ceiling">✅</abbr> | <abbr title="versionable JSON settings file (~/.srt-settings.json or --settings <path>)">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="srt <command> wraps any arbitrary CLI/executable; not tied to one vendor harness">✅</abbr> | ❌ | ❌ |
| Microsandbox | <abbr title="Local microVMs launched on developer's own machine via KVM/HVF/WHP; no external server or account needed">✅</abbr> | <abbr title="Apache 2.0 licensed runtime">✅</abbr> | <abbr title="Runtime runs on any Linux/macOS host: laptop, CI runner, VPC, on-prem, air-gapped">✅</abbr> | ❌ | <abbr title="NetworkPolicy ordered allow/deny rule engine via --net-rule / SandboxBuilder.network()">✅</abbr> | <abbr title="Exact-domain destination rules in NetworkPolicy">✅</abbr> | <abbr title="Destination.domainSuffix() matches apex plus all subdomains">✅</abbr> | <abbr title="Destination.cidr() CIDR-range rules">✅</abbr> | <abbr title="Protocol (TCP/UDP) plus port scoping in NetworkPolicy rules">✅</abbr> | <abbr title="Ordered allow/deny rules, explicit first-match-wins precedence, builder warns on shadowing">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="DNS interceptor returns synthetic NXDOMAIN for denied names on UDP/TCP 53">✅</abbr> | <abbr title="DNS interceptor plus TLS SNI/Host-vs-:authority validation enforce hostname at request time">✅</abbr> | <abbr title="Host terminates TLS with auto-generated CA in guest trust store, re-encrypts to upstream for L7 checks">✅</abbr> | <abbr title="All guest traffic forced through single host-side virtio-net userspace stack; guest has no other network path">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="DNS interceptor allows/denies/rewrites names as policy; synthetic NXDOMAIN for denied">✅</abbr> | <abbr title="Generic TCP egress scoped by protocol plus port in NetworkPolicy">✅</abbr> | <abbr title="Generic UDP egress scoped by protocol plus port in NetworkPolicy">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="msb metrics plus msb-metrics OTel sidecar ships to Grafana/Datadog/Prometheus dashboards">✅</abbr> | ❌ | <abbr title="msb ls/ps/inspect, --name persistent sandboxes, labels, fleet-wide metrics query">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Secret placeholder in guest; real value substituted host-side in TLS-intercepting proxy for allowlisted host">✅</abbr> | ❌ | <abbr title="Bind-mounted volumes give bidirectional live host<->guest sync via virtio-fs">✅</abbr> | <abbr title="OCI copy-on-write overlay (shared RO base + per-sandbox writable layer), ephemeral unless bind-mounted">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Additional bind-mounted directories and named volumes beyond primary workspace mount">✅</abbr> | ❌ | <abbr title="Custom OCI images from any registry (Docker Hub/GHCR/ECR/GCR) with per-registry auth">✅</abbr> | <abbr title="Pre-boot bootstrap scripts (/.msb/scripts/), rootfs patch ops, custom init selection before boot">✅</abbr> | ❌ | <abbr title="Built-in MCP server works with Claude, Codex, and other clients; generic 4-language exec/shell SDK">✅</abbr> | ❌ | <abbr title="Named persistent sandboxes retain config and state in local database across stop/restart">✅</abbr> |
| SmolVM | <abbr title="SmolVM runs entirely on the developer's own Linux/macOS host; no remote execution mode for SmolVM itself">✅</abbr> | <abbr title="Apache 2.0 licensed; source at github.com/CelestoAI/SmolVM">✅</abbr> | <abbr title="Self-hosted local install is its only mode (pip install smolvm / install script)">✅</abbr> | ❌ | <abbr title="internet_settings.allowed_domains outbound domain allowlist">✅</abbr> | <abbr title="allowed_domains entries are hostnames/domains (protocols/paths normalized off)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="host-side nftables (smolvm_nat/smolvm_filter) on per-sandbox TAP device, outside guest control">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="smolvm ui local dashboard (127.0.0.1:8080): VM status, resource usage, logs">✅</abbr> | ❌ | <abbr title="smolvm sandbox list name-keyed inventory + create/start/stop/delete lifecycle">✅</abbr> | ❌ | ❌ | <abbr title="smolvm claude/codex start auto-forwards host git credentials into sandbox">✅</abbr> | ❌ | ❌ | <abbr title="--writable-mounts: writable host-dir mount, edits reflect on host live both ways">✅</abbr> | <abbr title="default read-only mount: writes go to VM overlay, never touch host (ephemeral)">✅</abbr> | ❌ | <abbr title="smolvm claude/codex/hermes/pi start preinstalls harness + forwards login creds">✅</abbr> | <abbr title="coding-agent presets forward host CLI login creds; opt-in env-var forwarding of host secrets">✅</abbr> | <abbr title="multiple --mount host:guest at custom in-guest paths beyond default /workspace">✅</abbr> | ❌ | <abbr title="DockerRootfsBuilder custom Dockerfile / BootImage prebuilt disk images">✅</abbr> | <abbr title="Python API run() callback hooks (around run)">✅</abbr> | ❌ | <abbr title="arbitrary harness via DockerRootfsBuilder custom image; not locked to one vendor CLI">✅</abbr> | <abbr title="Full snapshot captures disk+memory+CPU state, resumes running sandbox exactly">✅</abbr> | ❌ |
| Dagger container-use | <abbr title="Local CLI (brew install) + MCP stdio server drive a local Docker/Dagger Engine on dev's machine">✅</abbr> | <abbr title="Apache 2.0 licensed, fully open source (github.com/dagger/container-use)">✅</abbr> | <abbr title="Runs entirely on user's own Docker host / optional CI runner; no managed-cloud variant exists">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="container-use log / watch / diff give per-env command history + real-time activity monitoring">✅</abbr> | ❌ | <abbr title="container-use list enumerates all environments/IDs; each addressable; delete --all lifecycle mgmt">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Agent works in isolated container with code copied in (not live bind); synced to host via git">✅</abbr> | <abbr title="Each environment = Dagger container + dedicated git worktree/branch, managed by the product">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Declarative .container-use/environment.json, committable/versionable for team sharing">✅</abbr> | <abbr title="environment.json configures base image + setup/install commands (defined-as-code per project)">✅</abbr> | <abbr title="setup commands (pre-copy) and install commands (post-copy) hooks in environment.json">✅</abbr> | ❌ | <abbr title="MCP stdio server (container-use stdio) works with any MCP-compatible agent; 17+ documented">✅</abbr> | ❌ | <abbr title="Environment (container + git branch + history) persists until deleted; agent resumes work intact">✅</abbr> |
| Dev Containers | <abbr title="Runs on developer's local Docker Desktop/Engine by default; code+secrets stay in a local container">✅</abbr> | <abbr title="Spec docs CC-BY-4.0; reference CLI and tooling MIT">✅</abbr> | <abbr title="Just Docker plus a devcontainer.json; no vendor account required for local/self-hosted use">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Automatic SSH-agent socket forwarding; container gets the agent socket, never the private key file">✅</abbr> | ❌ | <abbr title="Git HTTPS creds reused transparently via the host's configured credential helper">✅</abbr> | ❌ | <abbr title="VS Code port forwarding maps container localhost so OAuth browser callback routes back automatically">✅</abbr> | <abbr title="Live two-way bind mount is the default workspace sharing mode; edits reflect both ways">✅</abbr> | <abbr title="'Clone Repository in Container Volume' copies repo into an isolated Docker volume instead of binding">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="mounts property accepts arbitrary additional bind mounts or named volumes beyond the workspace">✅</abbr> | <abbr title="devcontainer.json is a single versionable, repo-committed per-project config file">✅</abbr> | <abbr title="devcontainer.json supports a custom base image or Dockerfile">✅</abbr> | <abbr title="onCreate/updateContent/postCreate/postStart/postAttach command lifecycle hooks">✅</abbr> | <abbr title="Features (versioned OCI/URL/local install units) plus Templates form a package ecosystem">✅</abbr> | <abbr title="Plain Docker image; any coding-agent CLI can be installed, not tied to one harness">✅</abbr> | ❌ | <abbr title="Named-volume mount at config path (e.g. ~/.claude) persists config/history across container rebuilds">✅</abbr> |
| Sculptor (Imbue) | <abbr title="Electron GUI + local FastAPI backend + agent-runner subprocess all run on the user's own machine">✅</abbr> | <abbr title="MIT licensed; source at github.com/imbue-ai/sculptor">✅</abbr> | <abbr title="Runs locally on the user's machine by default; opt-in remote/Docker backend is self-managed">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Electron GUI surfaces per-agent chat/task history, live diff/Changes panel, and PR review/CI status">✅</abbr> | ❌ | <abbr title="Workspace list UI + sculpt CLI workspace/agent scoping (-w, SCULPT_WORKSPACE_ID/AGENT_ID) + <user>/<slug> branches">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Workspace IS a git worktree by default; dedicated branch-naming/target/cleanup management">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Per-repo setup commands (e.g. npm install) run automatically at workspace creation">✅</abbr> | <abbr title="JS UI extensions (folder/URL/`sculpt extension load`) + bundled skill bundles + Claude skill auto-discovery">✅</abbr> | ❌ | ❌ | <abbr title="SQLite + append-only snapshot log persists every agent session; workspaces persist under ~/.sculptor/workspaces/">✅</abbr> |
| E2B | ❌ | <abbr title="Apache-2.0 SDKs/CLI + e2b-dev/infra self-host repo; managed control plane closed">✅</abbr> | <abbr title="e2b-dev/infra Terraform self-host on AWS/GCP/Azure/generic Linux">✅</abbr> | ❌ | <abbr title="allowOut allow-list via allowInternetAccess at create or updateNetwork">✅</abbr> | <abbr title="allowOut accepts domain names, matched on Host header/TLS SNI">✅</abbr> | <abbr title="wildcard subdomain rules e.g. *.mydomain.com in allowOut">✅</abbr> | <abbr title="IP/CIDR rules e.g. 8.8.8.0/24 in allowOut/denyOut">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="domain rules matched on TLS SNI (443) and HTTP Host header (80) at request time">✅</abbr> | ❌ | <abbr title="egress filtered at network edge outside the guest microVM; in-guest processes can't disable it">✅</abbr> | ❌ | <abbr title="updateNetwork()/PATCH sandbox-network applies rules to a running sandbox, no restart">✅</abbr> | <abbr title="allow_internet_access true/false live per-sandbox toggle instantly opens or locks egress">✅</abbr> | ❌ | <abbr title="CIDR-based egress filtering (allowOut/denyOut) applies to all TCP ports">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="getMetrics() CPU/mem/disk every 5s + lifecycle events API (created/paused/killed)">✅</abbr> | ❌ | <abbr title="sandboxes addressable by ID via API/SDK/CLI + team-wide lifecycle event aggregation + concurrency accounting">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="isolated ephemeral microVM filesystem (copy-populated); Snapshots checkpoint feature for reusable state">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="custom templates via Dockerfile (fromDockerfile) or builder API; e2b template build; Debian-only">✅</abbr> | <abbr title="template set_start_cmd (start command + readiness wait) and run_cmd build-time setup commands">✅</abbr> | ❌ | <abbr title="general-purpose code-execution sandbox via SDK/API; any LLM provider, not tied to a harness">✅</abbr> | <abbr title="pause()/resume() preserves filesystem + memory snapshot; auto-resume on next activity">✅</abbr> | <abbr title="Snapshots spawn new sandboxes from a checkpoint; Volumes persist storage independent of sandbox lifecycle">✅</abbr> |
| Modal | ❌ | ❌ | ❌ | ❌ | <abbr title="block_network / outbound_cidr_allowlist / outbound_domain_allowlist params set at Sandbox creation">✅</abbr> | <abbr title="outbound_domain_allowlist (Beta): allows TLS/port-443 to listed domain names">✅</abbr> | <abbr title="outbound_domain_allowlist supports *. subdomain wildcards">✅</abbr> | <abbr title="outbound_cidr_allowlist scopes egress to listed CIDR ranges (any protocol)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="_experimental_set_outbound_network_policy() replaces policy on running Sandbox, effective immediately (Alpha)">✅</abbr> | ❌ | ❌ | <abbr title="CIDR allowlist covers any protocol; non-allowlisted raw TCP egress is blocked">✅</abbr> | <abbr title="CIDR allowlist any-protocol; non-allowlisted UDP egress is blocked">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Per-sandbox dashboard: metrics/logs/status, Live Profiling, OTel/Datadog/Sentry integration">✅</abbr> | ❌ | <abbr title="Sandboxes under App namespace; unique name, key-value tags, Sandbox.list() filter, from_id/from_name">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Filesystem Snapshots capture full FS state, reusable as Image; workspace is one-way copy">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="volumes dict attaches multiple Modal Volumes/CloudBucketMounts at custom mount points">✅</abbr> | ❌ | <abbr title="Custom container Images via chained Image build steps or pulled from external registries">✅</abbr> | ❌ | ❌ | <abbr title="General SDK primitive, no vendor tie-in; used w/ Cursor, Copilot Workspace, LangGraph, Claude SDK, OpenCode">✅</abbr> | <abbr title="Memory Snapshots (Experimental) preserve running-process/RAM state so processes resume">✅</abbr> | <abbr title="Modal Volumes persist/sync state across sandboxes; Filesystem Snapshots reusable on recreation">✅</abbr> |
| Cloudflare Sandbox SDK | ❌ | <abbr title="SDK under Apache License 2.0 (github.com/cloudflare/sandbox-sdk)">✅</abbr> | ❌ | ❌ | <abbr title="allowedHosts host allow-list; becomes deny-by-default allowlist once set">✅</abbr> | <abbr title="allowedHosts/deniedHosts accept plain hostname entries">✅</abbr> | <abbr title="allowedHosts/deniedHosts glob where * matches any sequence (*.example.com)">✅</abbr> | <abbr title="allowedHosts/deniedHosts accept IP/CIDR entries e.g. 141.101.64.0/18">✅</abbr> | ❌ | <abbr title="deniedHosts blocklist evaluated with precedence over allowedHosts">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="allowedHosts/deniedHosts enforced by Host match at TPROXY HTTP-proxy at request time">✅</abbr> | <abbr title="interceptHttps default-on; ephemeral per-sandbox CA auto-trusted enables L7 inspection">✅</abbr> | <abbr title="sidecar applies TPROXY netfilter rules in sandbox netns redirecting HTTP/HTTPS egress to Workerd">✅</abbr> | ❌ | <abbr title="setAllowedHosts/setDeniedHosts/allowHost/denyHost adjust egress on live sandbox, no restart">✅</abbr> | ❌ | <abbr title="DNS egress forcibly pinned to Cloudflare's own resolvers, blocking arbitrary DNS destinations">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="outbound handler + MITM injects real credential in Worker; sandbox holds only short-lived JWT">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="wrangler.jsonc per-project config (containers/bindings/R2/vars), a versionable file">✅</abbr> | <abbr title="custom Dockerfile is a first-class, freely customizable generated project file">✅</abbr> | ❌ | ❌ | <abbr title="generic remote code-exec (exec/processes/files); any CLI installable via Dockerfile, no harness lock-in">✅</abbr> | ❌ | ❌ |
| Vercel Sandbox | ❌ | ❌ | ❌ | ❌ | <abbr title="networkPolicy deny-all + custom domain/CIDR allowlist restricting egress">✅</abbr> | <abbr title="networkPolicy domain allowlist matched via SNI hostname at TLS handshake">✅</abbr> | <abbr title="wildcard domain rules e.g. *.example.com match any subdomain (not apex)">✅</abbr> | <abbr title="subnets.allow / subnets.deny IP address-range (CIDR) rules">✅</abbr> | ❌ | <abbr title="CIDR deny rules take precedence over domain and CIDR allow entries">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="SNI-peeking on TLS ClientHello matches hostname at handshake time, not resolved IP set">✅</abbr> | <abbr title="per-sandbox CA terminates TLS for domains carrying transform/forwardURL rules">✅</abbr> | <abbr title="inline network gateway outside the microVM boundary; in-guest agent has no path to tamper">✅</abbr> | ❌ | <abbr title="sandbox.update({networkPolicy}) applies at runtime to current session, no restart">✅</abbr> | <abbr title="sandbox.update flips networkPolicy per-sandbox at runtime (deny-all<->allow-all)">✅</abbr> | ❌ | <abbr title="protocol-agnostic CIDR allow/deny rules gate raw TCP egress to address ranges">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Observability>Sandboxes dashboard, Usage metrics, per-sandbox Activity log, logs() streaming">✅</abbr> | ❌ | <abbr title="Sandbox.list() namePrefix/tags filtering, pagination, project-unique names">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="credentials brokering injects secrets onto egress HTTP at proxy; never enter sandbox scope">✅</abbr> | ❌ | ❌ | <abbr title="persistent:false ephemeral sandboxes discard filesystem on stop; snapshot/restore modes">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Drives: up to 4 configurable persistent volumes mounted per sandbox at absolute paths">✅</abbr> | ❌ | <abbr title="custom OCI images built from Dockerfiles pushed to Vercel Container Registry as boot base">✅</abbr> | <abbr title="onCreate / onResume lifecycle hooks configured at Sandbox.create()">✅</abbr> | ❌ | <abbr title="generic root Linux VM; docs run Claude Agent SDK, OpenCode, OpenClaw">✅</abbr> | <abbr title="persistent sandboxes stop then resume by name with filesystem snapshot+restore">✅</abbr> | <abbr title="persistent sandbox restores filesystem by name on resume; Drives persist independently">✅</abbr> |
| Daytona | ❌ | ❌ | ❌ | ❌ | <abbr title="domainAllowList (<=20 DNS domains) & networkAllowList (<=10 CIDR); networkBlockAll to deny all">✅</abbr> | <abbr title="domainAllowList accepts DNS domain entries">✅</abbr> | <abbr title="domainAllowList supports wildcard entries e.g. *.daytona.io">✅</abbr> | <abbr title="networkAllowList takes IPv4 CIDR blocks, each requiring /0-/32 prefix">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Tier 3/4 orgs change outbound firewall policy on a running sandbox; no stop/start required">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="OpenTelemetry multi-layer observability across API/runner/toolbox plus Dashboard interface">✅</abbr> | ❌ | <abbr title="Organizations RBAC + linked parent/child sandbox sets; dashboard and CLI daytona list">✅</abbr> | ❌ | ❌ | <abbr title="Git Provider OAuth integration (GitHub/GitLab/Bitbucket/Azure DevOps) mediates git-host auth">✅</abbr> | <abbr title="Org Secrets injected as opaque placeholder env vars; real value substituted on outbound requests to allowed hosts">✅</abbr> | ❌ | ❌ | <abbr title="Snapshots are reusable OCI base images; sandboxes ephemeral via auto-archive/delete lifecycle">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Volumes (FUSE, multi-sandbox mount) and External Storage (S3/GCS bucket) mounts beyond sandbox disk">✅</abbr> | ❌ | <abbr title="Snapshots built from a Dockerfile or any OCI-compatible registry image">✅</abbr> | ❌ | ❌ | <abbr title="General-purpose Linux sandbox via SSH/exec; any CLI installable; MCP for Claude/Cursor/Windsurf">✅</abbr> | <abbr title="Hot snapshots capture VM filesystem+memory via includeMemory; auto-pause/resume lifecycle">✅</abbr> | <abbr title="Volumes provide persistent storage mountable across sandboxes, surviving recreation; snapshots">✅</abbr> |
| CodeSandbox SDK | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="csb CLI dashboard (CPU/mem/storage monitor, real-time debug) + opt-in OpenTelemetry tracing">✅</abbr> | ❌ | <abbr title="csb sandboxes list/fork/hibernate/shutdown; tags(max10)/title/path-folder metadata + sandbox IDs">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="versioned .codesandbox/ dir: tasks.json + Dockerfile + docker-compose.yml in repo">✅</abbr> | <abbr title=".codesandbox/Dockerfile custom image + docker-compose.yml, built into templates via csb CLI">✅</abbr> | <abbr title=".codesandbox/tasks.json setupTasks (install/build) + tasks (long-running servers)">✅</abbr> | ❌ | <abbr title="general-purpose VM+command-exec primitive; arbitrary Dockerfile/shell, any agent CLI">✅</abbr> | <abbr title="memory+disk hibernate/resume snapshot; resume 1-3s, fork from hibernated snapshot">✅</abbr> | <abbr title="git-tracked /project/workspace persists across reboots + memory/disk snapshot persistence">✅</abbr> |
| Morph | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Instances/devboxes listable via API/CLI/dashboard w/ metadata tags; Branch launches N instances from a snapshot">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Immutable bootable snapshots; instances branched/restored from snapshots (Infinibranch); copy-based workspace">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Reusable snapshot-based base images bake OS/packages/config into a bootable state (image→snapshot→instance)">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Pause creates a snapshot + suspends VM preserving full memory/process state; resume continues exactly">✅</abbr> | <abbr title="Snapshots persist full state; EFS provides persistent mountable storage independent of instance lifecycle">✅</abbr> |
| Runloop | ❌ | ❌ | <abbr title="Enterprise 'Deploy to VPC' — Runloop stack deployed into customer's own AWS/GCP/Azure account">✅</abbr> | ❌ | <abbr title="Network Policy allowed_hostnames allowlist (allow_all=false)">✅</abbr> | <abbr title="Network Policy allowed_hostnames = DNS hostname rules">✅</abbr> | <abbr title="allowed_hostnames first-label wildcards e.g. *.github.com">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Updating a Network Policy propagates to running Devboxes without restart (eventually consistent)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Dashboard Log Viewer (real-time streaming) + Resource Monitoring CPU/mem/storage graphs">✅</abbr> | ❌ | <abbr title="Dashboard advanced search + Coordination multi-agent orchestration w/ shared state/secrets">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Agent Gateways / MCP Hub — real keys stay on Runloop servers, opaque devbox-bound token injected">✅</abbr> | ❌ | ❌ | <abbr title="Devbox disk snapshots — branchable, create new Devboxes from saved disk state">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Blueprints: custom Dockerfile builds (rli blueprint from-dockerfile) + private registries">✅</abbr> | <abbr title="Blueprint install/launch commands + code-mount install_command/setup_commands">✅</abbr> | ❌ | <abbr title="Generic Linux Devbox (SSH/exec/API); marketed 'Framework agnostic'">✅</abbr> | <abbr title="Devbox suspend/resume lifecycle (Pro tier) + disk snapshots">✅</abbr> | <abbr title="Disk snapshots persist indefinitely; new Devboxes created from saved disk state">✅</abbr> |
| Northflank | ❌ | ❌ | ❌ | ❌ | <abbr title="BYOC Cilium Network Policies: egress allow-list by IP/CIDR/FQDN/hostname destinations">✅</abbr> | <abbr title="Egress Network Policy rules accept FQDN/hostname destination targets">✅</abbr> | ❌ | <abbr title="Egress Network Policy rules accept IP address and CIDR range targets">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Cilium eBPF enforces network policy at kernel; Kata guest kernel puts it outside sandbox">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Container logs/metrics dashboard: 15s metrics, live-tail, external log sinks">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Persistent volumes (4GB-64TB, multi-RW) attachable beyond workspace via volumesToAttach">✅</abbr> | <abbr title="Infrastructure-as-Code declarative config plus deploymentPlan, versionable">✅</abbr> | <abbr title="Custom Docker images and Dockerfile/buildpack builds">✅</abbr> | ❌ | ❌ | <abbr title="Runs arbitrary container images with exec access; no specific agent-CLI tie">✅</abbr> | ❌ | ❌ |
| Blaxel | ❌ | ❌ | ❌ | ❌ | <abbr title="allowedDomains allowlist / forbiddenDomains denylist enforced via HTTP(S) forward proxy">✅</abbr> | <abbr title="allowedDomains/forbiddenDomains match domain patterns at proxy">✅</abbr> | <abbr title="wildcard domain patterns e.g. *.s3.amazonaws.com supported">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="HTTP(S) forward proxy (MITM) matches domain rules against request Host at request time">✅</abbr> | <abbr title="proxy MITM-intercepts outbound HTTPS, installs CA cert via NODE_EXTRA_CA_CERTS/SSL_CERT_FILE">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Blaxel Console per-sandbox logs + automatic OpenTelemetry metrics/logs/traces">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="proxy resolves {{SECRET:name}} server-side into outbound HTTPS; sandbox never sees raw value">✅</abbr> | ❌ | ❌ | <abbr title="default ephemeral in-RAM root (EROFS base + tmpfs/OverlayFS overlay)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="blaxel.toml declarative versionable per-project config (env, runtime memory, volumes)">✅</abbr> | <abbr title="custom Dockerfile-based sandbox images (require injected sandbox-api binary)">✅</abbr> | ❌ | ❌ | <abbr title="generic Dockerfile images; any coding-agent CLI installable (only sandbox-api binary required)">✅</abbr> | <abbr title="automatic full-state snapshot (filesystem+running process) on standby, sub-25ms resume">✅</abbr> | <abbr title="disk-backed root via storageMb + attachable persistent Volumes retain state across sessions">✅</abbr> |
| Beam (beta9) | ❌ | <abbr title="beta9 engine under AGPL-3.0 (github.com/beam-cloud/beta9); managed Beam is closed on top">✅</abbr> | <abbr title="self-host beta9 engine via Kubernetes+Helm + S3-compatible object store">✅</abbr> | ❌ | <abbr title="allow_list of up to 10 CIDR ranges; all other outbound traffic blocked">✅</abbr> | ❌ | ❌ | <abbr title="allow_list accepts IPv4/IPv6 CIDR ranges (e.g. 8.8.8.8/32, 10.0.0.0/8)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="update_network_permissions() applies network policy changes immediately without restart">✅</abbr> | ❌ | ❌ | <abbr title="block_network / CIDR allow_list govern outbound connections (TCP egress destinations)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="resource-usage graph (usage vs configured limits over time) + real-time per-process log streaming">✅</abbr> | ❌ | <abbr title="CLI app lifecycle (deploy/list/stop/start/delete) + beam machine list + named secrets/volumes">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="ephemeral per-run code sync into fresh container + filesystem/memory snapshots">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="named Volumes + external S3/R2/Tigris buckets mountable at arbitrary paths (ro/rw)">✅</abbr> | ❌ | <abbr title="from_dockerfile()/custom base image + add_python_packages()/add_commands()">✅</abbr> | ❌ | ❌ | <abbr title="generic Python/TS SDK; Sandbox API is harness-agnostic remote-execution primitive">✅</abbr> | <abbr title="snapshot_memory() preserves running processes/ports; create_from_memory_snapshot() restores">✅</abbr> | <abbr title="Distributed Storage Volumes persist files across container runs/recreation">✅</abbr> |
| OpenAI API sandboxes | <abbr title="UnixLocalSandboxClient/DockerSandboxClient execute on the developer's own machine (default no-install)">✅</abbr> | <abbr title="openai-agents-python / @openai/agents SDK is MIT-licensed (official openai GitHub org)">✅</abbr> | <abbr title="Unix-local and Docker clients run wherever the SDK process runs; no vendor cloud required">✅</abbr> | <abbr title="Responses API hosted container: no outbound network access by default">✅</abbr> | <abbr title="network_policy allowlist of allowed_domains on the Responses API container tool">✅</abbr> | <abbr title="allowed_domains accepts bare-domain/hostname entries">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="built-in Traces dashboard (platform.openai.com/traces) logs model/tool calls, handoffs, guardrails">✅</abbr> | <abbr title="guardrails/approval interruptions gate & reject risky tool calls mid-run, resumable via RunState">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="domain_secrets inject auth headers as placeholders; raw secrets kept off sandbox/API servers">✅</abbr> | ❌ | ❌ | <abbr title="Unix-local stages an ephemeral temporary workspace, cleaned up after the run">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="extra_path_grants for extra host paths + storage mounts (S3/GCS/R2/Azure Blob/Box)">✅</abbr> | ❌ | <abbr title="DockerSandboxClientOptions.image runs a custom Docker image">✅</abbr> | ❌ | <abbr title="pluggable Capabilities (Skills, Memory, custom tools) + per-agent MCP servers">✅</abbr> | ❌ | <abbr title="three-tier resolution: live session reuse, RunState session-state resumption, snapshot seeding">✅</abbr> | <abbr title="snapshots / persist_workspace() persist workspace root to seed a fresh session">✅</abbr> |
| K8s agent-sandbox | ❌ | <abbr title="Apache-2.0, kubernetes-sigs/agent-sandbox (official SIG Apps subproject)">✅</abbr> | <abbr title="Runs on any conformant Kubernetes cluster, self- or managed-hosted">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="k8s_agent_sandbox SDK OpenTelemetry traces/metrics + per-sandbox Metrics doc page">✅</abbr> | ❌ | <abbr title="SandboxWarmPool + SandboxClaim + SandboxTemplate CRDs; stable per-sandbox hostname/identity">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="volumeClaimTemplates in SandboxTemplate + FUSE CSI attach configurable volumes">✅</abbr> | <abbr title="Declarative Sandbox/SandboxTemplate CRD YAML manifests, GitOps-versionable">✅</abbr> | <abbr title="podTemplate.spec.containers[].image accepts any container image">✅</abbr> | ❌ | ❌ | <abbr title="podTemplate accepts any image; core API not tied to any vendor agent harness">✅</abbr> | <abbr title="suspend()/resume() snapshots filesystem + memory state (Python SDK)">✅</abbr> | <abbr title="PVC-backed volumeClaimTemplates persist filesystem across pod recreation/resume">✅</abbr> |
| agent-sandbox (org) | ❌ | <abbr title="Apache-2.0 licensed open-source (github.com/agent-sandbox/agent-sandbox)">✅</abbr> | <abbr title="self-hosted only; kubectl apply install.yaml onto operator's K8s cluster v1.26+">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="web UI dashboard (v0.6.0) + events/logs APIs + pkg/telemetry usage monitoring">✅</abbr> | ❌ | <abbr title="sandbox naming/list API, multi-tenant users, template/pool warm-sandbox mgmt">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="Blueprint volumeMounts/volumes attach additional NAS/NFS/OSS/S3 volumes">✅</abbr> | <abbr title="declarative versionable Template + Blueprint (Go-template ReplicaSet) YAML in ConfigMap">✅</abbr> | <abbr title="any custom container image via Template; static or regex dynamic image resolution">✅</abbr> | ❌ | ❌ | <abbr title="generic runtime via E2B SDK, native REST, MCP server, exec endpoints — harness-agnostic">✅</abbr> | <abbr title="pause/resume via ReplicaSet scale 0↔1 + process-roster snapshot (v0.7.0)">✅</abbr> | ❌ |
| OpenSandbox | <abbr title="Docker runtime backend runs lifecycle server + Docker daemon on developer's own machine">✅</abbr> | <abbr title="Apache 2.0; source at github.com/alibaba/OpenSandbox">✅</abbr> | <abbr title="Only deployment model is self-hosted Docker or Kubernetes; no vendor SaaS">✅</abbr> | ❌ | <abbr title="Egress sidecar allowlist (domain/wildcard/IP/CIDR) via per-sandbox networkPolicy">✅</abbr> | <abbr title="Exact-domain allow/deny rules in egress sidecar allowlist">✅</abbr> | <abbr title="Wildcard subdomain rules e.g. *.pypi.org">✅</abbr> | <abbr title="Literal IP and CIDR targets e.g. 10.0.0.0/8 in dns+nft mode">✅</abbr> | ❌ | <abbr title="deny action + deny.always file with unconditional top priority over allow rules">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="DNS proxy (127.0.0.1:15353) returns NXDOMAIN for denied domains">✅</abbr> | <abbr title="DNS proxy filters queries by hostname at resolution time (NXDOMAIN)">✅</abbr> | <abbr title="Experimental transparent HTTPS MITM via mitmproxy on outbound 80/443 in sidecar netns">✅</abbr> | <abbr title="Kernel iptables port-53 redirect + nftables IP/CIDR enforcement in sandbox netns">✅</abbr> | ❌ | <abbr title="always-rules hot-reloaded ~1/min; /policy API changes apply immediately">✅</abbr> | ❌ | <abbr title="DNS proxy filters/allows/denies queries, NXDOMAIN on deny">✅</abbr> | <abbr title="nftables IP/CIDR TCP egress enforcement (dns+nft mode)">✅</abbr> | <abbr title="nftables IP/CIDR UDP egress enforcement (dns+nft mode)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | ❌ | <abbr title="execd /metrics + /metrics/watch SSE + OpenTelemetry export; server diagnostics">✅</abbr> | ❌ | <abbr title="K8s distributed scheduling + per-sandbox IDs + SQLite metadata store">✅</abbr> | ❌ | ❌ | ❌ | <abbr title="Credential Vault injects secrets into MITM-proxied outbound requests, raw value hidden">✅</abbr> | ❌ | <abbr title="`host` volume mode bind-mounts allowlisted host paths live (two-way)">✅</abbr> | ❌ | ❌ | ❌ | ❌ | <abbr title="allowed_host_paths list (multiple dirs) + pvc/ossfs volume models">✅</abbr> | <abbr title="Sectioned TOML config (~/.sandbox.toml), declarative and versionable">✅</abbr> | <abbr title="Custom OCI images for workloads/execd; --image on sandbox create">✅</abbr> | ❌ | <abbr title="Sandbox Protocol (OpenAPI) for custom runtimes + pluggable secure_runtime/workload_provider">✅</abbr> | <abbr title="Harness-agnostic: MCP server + SDKs drive Claude Code, Cursor, Codex, Gemini, etc.">✅</abbr> | <abbr title="pause/resume commits rootfs to OCI image, resume recreates preserving sandbox ID">✅</abbr> | ❌ |

<details>
<summary>Methodology & selected nuances</summary>

Assessed against official documentation only, as of 2026-07. Each column is a single yes/no fact; a criterion that resisted a clean yes/no was split into narrower columns rather than recorded as a middle value. A ✅ means the sandbox **product itself** implements the capability — a feature a competent user gets out of the box — not something merely *possible* because the sandbox is a general-purpose machine, and not a harness/CLI feature (e.g. an agent's own git-worktree fan-out, a CLI's device-code login) that happens to run inside it. ❌ covers both confirmed absence and undocumented capability. Per-provider research notes with per-claim citations live in `.serena/memories/agent-sandbox-research/`.

- **Domain-native vs IP-pinned**: `Domain-native` is ✅ only when rules match the hostname at request time (DNS-layer policy, SNI/Host matching, dynamic forward proxy). Sandboxes that resolve domains to IP sets once and enforce as CIDR (Docker Sandboxes, Dev Containers' reference firewall) are ❌ — that model breaks when load-balanced services rotate IPs and over-opens shared CDN/Cloudflare ranges.
- **Kernel-level enforcement**: ✅ requires policy the in-sandbox agent cannot route around (eBPF/netfilter, or an out-of-VM boundary). A userspace proxy the agent could sidestep with a direct socket is ❌.
- **Worktree / browser-auth / remote**: credited only to the sandbox that implements them. "A VM with git installed" has no worktree feature; a CLI that prints a URL to paste is not host-browser auth; a local CLI is not remote because a separate cloud product exists.

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

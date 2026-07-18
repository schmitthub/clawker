# Microsandbox
category: local
Local-first microVM runtime for untrusted/agent code | built on libkrun + smoltcp (hardware-virtualized microVMs) | Apache 2.0 | beta, 7k+ GitHub stars

Note: seed URL `github.com/zerocore-ai/microsandbox` now resolves to `github.com/superradcompany/microsandbox` — the org (Zerocore AI → Super Rad Company) and possibly repo location renamed; project identity/codebase continuity otherwise confirmed (same README, same docs site). Treat both URLs as the same project.

## A. Identity

### built_on (prose-only)
Rust project (86.2% per GitHub language stats) built on **libkrun** (VMM) with a guest kernel from **libkrunfw**, and an embedded userspace network stack (**smoltcp**) providing an in-process network gateway rather than host kernel routing. Each sandbox is scheduled by a hardware hypervisor: KVM on Linux, Apple Hypervisor.framework on macOS, Windows Hypervisor Platform (WHP) on Windows. Host-side architecture: your application/CLI talks to a host-side "sandbox process" which relays requests to a guest agent running inside the VM over a small set of virtio devices (console, net, fs, blk, rng) — no general-purpose host passthrough.
Sources:
- https://github.com/zerocore-ai/microsandbox — "Microsandbox is built on libkrun and smoltcp, utilizing microVM technology"
- https://docs.microsandbox.dev/security/isolation.md — "Its own Linux kernel, supplied by microsandbox (built from libkrunfw)... scheduled by a hardware hypervisor (KVM on Linux, Apple's Hypervisor.framework on macOS) through the libkrun VMM"

### execution_locality
Local — determination: **Local** (primary/default mode). Sandboxes run as local microVMs launched directly on the developer's machine, CI runner, or self-managed server; no required external infrastructure or account for local use ("no Docker account or external server needed"). A separate hosted "microsandbox cloud" exists but is in closed beta with a waitlist as of the sources found — it is a distinct remote deployment option, not the default usage mode, and no public pricing/GA date was found.
Sources:
- https://docs.microsandbox.dev/getting-started/quickstart.md (via WebFetch) — "No Docker account or external server needed—microsandbox runs entirely locally"
- https://microsandbox.dev/ (via WebFetch) — "works on your laptop, in your VPC, on-prem, or in the microsandbox cloud"
- WebSearch (github SELF_HOSTING.md / microsandbox.dev, summarized) — "As of June 2026 the cloud platform is in closed beta with a waitlist"

### open_source (prose-only)
Apache 2.0 license. Fully self-hostable — the runtime is described as "a library you embed and ship anywhere you can run a Linux or macOS host: your laptop, a CI runner, a VM in your VPC, regulated data centers, or fully air-gapped servers," and local/self-hosted use is free. The prospective cloud tier will carry "its own commercial license" (not yet published).
Sources:
- https://github.com/zerocore-ai/microsandbox — "Licensed under Apache 2.0"
- WebSearch (github SELF_HOSTING.md, summarized) — "Running locally or on your own infrastructure is free, forever"

### maturity (prose-only)
7,000+ GitHub stars, 351 forks, 49 total releases, latest tag v0.6.6 as of research date (2026-07-18). Explicitly labeled beta software by the maintainers.
Sources:
- https://github.com/zerocore-ai/microsandbox — "7,000+ stars and 351 forks... Currently beta software with 'breaking changes, missing features, and rough edges' expected... Latest release: v0.6.6 (49 total releases)"

## B. Threat protection

### host_fs_damage
Yes — each sandbox gets a private root filesystem (read-only cached image layers plus a per-sandbox writable overlay); the guest sees only the image plus whatever is explicitly mounted, no implicit host path exposure. The host-side broker additionally enforces path containment on bind mounts using `openat2` with `RESOLVE_BENEATH` (Linux 5.6+) to atomically block `..` traversal and symlink escapes, and enforces read-only mounts host-side so "even a privileged guest process...still cannot write through it."
Sources:
- https://docs.microsandbox.dev/security/filesystem.md — "openat2 with RESOLVE_BENEATH, which atomically blocks .. traversal, symlink escapes"; "Read-only mount is enforced on the host side, so even a privileged guest process...still cannot write through it"

### credential_theft
Yes — secrets never enter the guest VM. The guest environment receives a meaningless placeholder (`$MSB_<env_var>` by default); the real value is substituted host-side, in the network proxy, only when ALL of: the destination matches the secret's allowed-host pattern, the DNS answer was legitimately resolved through the interceptor (pinned), the connection is TLS-intercepted, and the HTTP Host/`:authority` matches the SNI (anti domain-fronting). Caveat: this mechanism only covers env-var-shaped secrets flowing over intercepted TLS to an allowlisted host — it is not a general ssh-agent/gpg-agent forwarding model (see G/cred_forwarding).
Sources:
- https://docs.microsandbox.dev/security/secrets.md — "The guest's environment receives a placeholder ($MSB_<env_var> by default), never the real value"; "a secret requires intercepted TLS, so it is never substituted over a connection the host can't see into"

### data_exfiltration
Yes — network egress is enforced by a host-side userspace proxy that terminates every guest packet before it reaches any real network; policy can be tightened to full deny-by-default with an explicit allowlist, and DNS-level blocking plus TLS SNI/Host validation prevent hostname/IP mismatch bypasses (rebind protection rewrites private/loopback/link-local DNS answers to NXDOMAIN). Caveat: the out-of-the-box **default** posture is allow-public-internet / deny-private (not deny-by-default) — see network_default_posture.
Sources:
- https://docs.microsandbox.dev/security/network.md — "a user-space stack terminates every packet and checks it against policy before anything leaves. There is no host kernel routing or NAT in the path"
- https://docs.microsandbox.dev/networking/dns.md — "denied names get a local NXDOMAIN response"

### malicious_execution
Yes — untrusted/hallucinated code runs inside a real guest kernel under hardware virtualization, so guest kernel exploits or malicious packages are contained to the guest kernel rather than reaching the host (no shared-kernel namespace/cgroup escape surface as in containers). vCPU/memory caps enforced by the VMM bound the blast radius further, and an optional "restricted security profile" adds `no_new_privs`, drops the mount-admin capability, and forces `nosuid,nodev` on user mounts — though this profile is explicitly "incompatible with workloads such as sudo and Docker-in-Docker."
Sources:
- https://docs.microsandbox.dev/security/isolation.md — "kernel exploits compromise only the guest kernel, not the host"
- https://docs.microsandbox.dev/security/hardening.md — "a restricted security profile, which sets no_new_privs, drops the mount-admin capability, and forces nosuid,nodev on user mounts"; "This profile is incompatible with workloads such as sudo and Docker-in-Docker"

### escape_resistance
Partial — the isolation boundary is a genuine hardware-virtualized microVM (own kernel, own memory/vCPUs, hypervisor-mediated), which is structurally stronger than a shared-kernel container/namespace/seccomp boundary; the vendor's own stated threat model is "the guest is untrusted, the host is trusted, and a hardware hypervisor sits between them." Marked Partial rather than Yes because the vendor docs themselves name the one residual attack class they explicitly do NOT defend against: a VMM/hypervisor escape bug. No independent (non-vendor) security audit or CVE history for libkrun/microsandbox's VMM was found in this research.
Sources:
- https://docs.microsandbox.dev/security/isolation.md — "A bug that lets a guest break out of the VM into its host process, a VMM or hypervisor escape, is the one class this boundary cannot defend against"
- https://docs.microsandbox.dev/security/overview.md — "The guest is untrusted, the host is trusted, and a hardware hypervisor sits between them"

### resource_abuse
Yes — vCPU count and memory are capped at VM creation and enforced by the VMM ("a guest cannot allocate beyond its memory ceiling or use more vCPUs than assigned"); `idle_timeout` and `max_duration` lifecycle settings auto-reclaim abandoned sandboxes. Caveat: docs don't detail disk-IO throughput limiting granularity beyond noting virtio-blk/virtio-fs paravirtualized I/O.
Sources:
- https://docs.microsandbox.dev/security/isolation.md — "vCPU count and memory are capped at VM creation and enforced by the VMM"
- https://docs.microsandbox.dev/security/overview.md — "A workload can exhaust its own CPU and memory within the limits you set. That doesn't affect other sandboxes or the host"

## C. Feature set & granularity

### network_default_posture
Determination: **open-with-carve-outs**, not deny-by-default. Out of the box, sandboxes can reach the public internet but private ranges (RFC1918, IPv6 ULA `fc00::/7`), loopback, link-local, cloud metadata (`169.254.169.254`), and the host itself are denied by default. A full deny-by-default allowlist mode is available but must be explicitly configured (`--net-default deny` / `defaultEgress: "deny"`).
Sources:
- https://docs.microsandbox.dev/networking/overview.md — "By default, sandboxes can reach the public internet but cannot reach private networks, loopback, link-local addresses, or cloud metadata endpoints"

### egress_allowlist
Yes — granularity ladder confirmed: named groups (`public`, `private`, `host`, `loopback`, `link-local`, `metadata`, `multicast`) → exact domains → domain suffixes (subdomain wildcard, matches apex + subdomains) → CIDR ranges → protocol (TCP/UDP) + port scoping → ordered allow/deny rules with explicit first-match-wins precedence (builder warns on shadowing). No HTTP path/method/regex granularity inside this rule engine itself (see http_path_rules).
Sources:
- https://docs.microsandbox.dev/sdk/typescript/networking.md (via WebFetch) — "A policy has two defaults and an ordered list of rules. The first matching rule wins"; `Destination.domainSuffix("example.com")`, `Destination.cidr("10.0.0.0/8")`, `Destination.group("public"...)`

### dns_level_blocking
Yes — unlisted/denied domains resolve to a synthetic local NXDOMAIN at the DNS interceptor (UDP/TCP 53 intercepted directly); this is reinforced by rebind protection that rewrites any answer resolving a name to a private/loopback/link-local address to NXDOMAIN as well.
Sources:
- https://docs.microsandbox.dev/networking/dns.md — "denied names get a local NXDOMAIN response (a synthetic negative, so stub resolvers fail fast)"
- https://docs.microsandbox.dev/security/network.md — "when the DNS interceptor sees an answer resolving a name to a private, loopback, or link-local address, it rewrites the response to NXDOMAIN"

### tls_mitm_inspection
Yes — host terminates TLS using an auto-generated CA (`~/.microsandbox/tls/ca.{crt,key}`) installed into the guest trust store, then re-encrypts to the real upstream, enabling policy checks and Host/`:authority`-vs-SNI alignment on plaintext request data. Caveats: `trust_host_cas` (trusting the host's own CA bundle for non-web protocols) is off by default; mTLS client certificates are "not supported through interception"; interception explicitly does not touch QUIC/HTTP3 (see proto_coverage).
Sources:
- https://docs.microsandbox.dev/networking/tls.md — "microsandbox terminates TLS on the host side and re-encrypts to the upstream server"; "mTLS client certificates: Not supported through interception"

### http_path_rules
Unknown — the TLS-interception page states interception enables "URL-level policy checks" and HTTP Host/`:authority` alignment verification, but no fetched page (networking/tls.md, security/network.md, networking/overview.md, sdk/typescript/networking.md) documented an explicit per-path or per-method allow/deny rule syntax — the documented `NetworkPolicy` rule model covers domain/domain-suffix/CIDR/group + protocol/port only. Docs are silent on whether path-level rules exist as a first-class construct, so kept Unknown rather than No.

### proto_coverage
Partial — confirmed controlled: DNS (UDP/TCP 53, intercepted directly), DoT (TCP/853, when paired with TLS interception), generic TCP/UDP (protocol+port scoped in the policy engine), HTTPS/TLS (L7-inspected via MITM). Confirmed gaps: QUIC/HTTP3 explicitly NOT touched by interception ("It doesn't touch QUIC / HTTP/3 flows"); DoH (over TCP/443) is "not distinguishable from regular traffic (you must filter via network policy)" — i.e. not protocol-aware, only reachable via generic domain/port rules; gRPC and other custom L7 protocols fall outside default interception and need host-CA-trust rather than native rule support; ICMP was not mentioned in any fetched page; WebSocket-specific handling was not documented. No documented extensibility mechanism for adding new/custom L7 protocol parsers into the rule model was found.
Sources:
- https://docs.microsandbox.dev/networking/tls.md — "It doesn't touch QUIC / HTTP/3 flows"
- https://docs.microsandbox.dev/networking/dns.md — "DNS over HTTPS (DoH, TCP/443) is not distinguishable from regular traffic (you must filter via network policy)"

### live_rule_reload
No — network policy is set via `SandboxBuilder.network(...)` / CLI `--net-default`/`--net-rule` at sandbox creation time; the `modify()` API (used for live changes) explicitly enumerates its own scope — live (cpus, memory, labels), future-execution-only (env, workdir), restart-required (max_cpus, max_memory) — and network policy appears in none of these categories. The CLI reference independently confirms restrictions persist until restart.
Sources:
- https://docs.microsandbox.dev/sandboxes/tuning.md — modify() classification: "Live: cpus, memory, labels. Future executions: env, workdir. Restart required: max_cpus, max_memory" (network absent from all categories)
- https://docs.microsandbox.dev/cli/sandbox-commands.md (via WebFetch) — "Network restrictions are applied at sandbox creation and persist until the sandbox restarts"

### firewall_escape_hatch
No — no documented timed-bypass-with-automatic-re-enforcement, and no per-sandbox live disable/enable of the network policy while running. Changing network posture (e.g. from restrictive to `--no-net`/allow-all) requires stopping and recreating the sandbox with different creation-time flags — an all-or-nothing "tear down and recreate" pattern, which the assessment guidelines' own escape-hatch example classifies as No.
Sources:
- https://docs.microsandbox.dev/cli/sandbox-commands.md — `--no-net` (airgap) and `--net-default`/`--net-rule` documented only as `msb run`/`msb create` flags, no runtime toggle command found
- https://docs.microsandbox.dev/security/hardening.md — no bypass mechanism mentioned; only "switch from allow-public to deny-by-default" as a creation-time posture choice

### enforcement_plane
Determination: **userspace proxy at the VM boundary** (not host-kernel netfilter/eBPF, not cloud infra). A host-side userspace network stack (built on smoltcp) terminates every guest packet via the virtio-net device and checks it against policy before anything leaves; there is explicitly "no host kernel routing or NAT in the path." Because the guest's only network path is this single virtio-net device (no host PCI passthrough, no shared memory beyond the five documented virtio devices: console/net/fs/blk/rng), the guest cannot route around the enforcement point from inside the sandbox. Traffic visibility for policy/logging purposes is limited to what the proxy can see (full for intercepted TLS/plaintext; opaque for QUIC/HTTP3 — see proto_coverage); no dedicated persistent traffic log was found (see network_audit).
Sources:
- https://docs.microsandbox.dev/security/network.md — "all sandbox traffic flows through a host-side network stack...a user-space stack terminates every packet and checks it against policy before anything leaves. There is no host kernel routing or NAT in the path"
- https://docs.microsandbox.dev/security/isolation.md — "There is no general-purpose passthrough. No host PCI devices, no host sockets, and no shared memory beyond these devices"

### fail_closed
Unknown — no fetched official page (security/network.md, security/hardening.md, security/overview.md, troubleshooting/linux.md) states what happens to network enforcement if the host-side broker/proxy process crashes while a sandbox is running. A plausible structural inference — the guest's only network path is the host-terminated virtio-net device with no host-kernel routing/NAT fallback, so a dead broker would likely leave the guest with no network at all rather than an open one — is architecture-based reasoning, not a documented guarantee, so kept Unknown per guidelines (docs silence ≠ No, and this isn't a confirmed Yes either).

### network_audit
Partial — sandbox-level `msb metrics`/`msb logs` expose CPU/memory/filesystem usage and captured guest stdout, and an OTel-compatible sidecar (`msb-metrics`) can ship metrics to Grafana/Datadog/Prometheus for dashboards. However, no fetched official documentation (sandboxes/metrics.md, cli/sandbox-commands.md, security/network.md) describes a dedicated per-request egress log (which domain/IP was allowed or denied, per-connection verdict, timestamp) as a first-class feature — `msb logs` and `msb logs --source system` cover process/runtime diagnostics, not network-policy decisions specifically.
Sources:
- https://docs.microsandbox.dev/sandboxes/metrics.md — "Need continuous shipping to Grafana, Datadog, Prometheus, or any other OTel-compatible backend? See msb-metrics"
- https://docs.microsandbox.dev/cli/sandbox-commands.md — `msb logs`/`msb logs --source system` documented for captured output/runtime diagnostics, no network-audit-specific command found

### workspace_modes
Yes — both live-bind-mount and ephemeral/copy modes are offered. Bind-mounted volumes give bidirectional live sync via virtio-fs ("changes inside the sandbox are reflected on the host, and vice versa"). OCI images use a copy-on-write overlay (shared read-only cached base layers + per-sandbox private writable layer) that is inherently ephemeral/isolated from the host unless explicitly bind-mounted.
Sources:
- https://docs.microsandbox.dev/sandboxes/volumes.md (via WebFetch) — "Changes inside the sandbox are reflected on the host, and vice versa"
- https://docs.microsandbox.dev/security/filesystem.md — "Read-only image layers from a shared, content-addressed cache" + "A per-sandbox writable layer on top"

### observability
Yes — point-in-time (`metrics()`) and streaming (`metrics_stream()`) resource metrics (CPU%, memory bytes, optional guest/host filesystem usage), a fleet-wide metrics query "useful for dashboards or capacity planning," `msb logs` for guest output, and an OTel-compatible `msb-metrics` sidecar for shipping to Grafana/Datadog/Prometheus.
Sources:
- https://docs.microsandbox.dev/sandboxes/metrics.md — "Get the latest metrics for every running sandbox at once. Useful for dashboards or capacity planning"

### supervision
Partial — a host-side sandbox process actively supervises each VM's lifecycle (Creating/Running/Draining/Stopped/Crashed states), can gracefully drain/stop it, and lifecycle policies (`idle_timeout`, `max_duration`) auto-drain abandoned sandboxes without user action — this is genuine intervention capability, not just passive metrics. However, no documentation describes automated threat-triggered containment (e.g., an anomaly detector that quarantines a sandbox mid-run based on observed behavior) — the interventions documented are resource/lifecycle-driven, not security-behavior-driven.
Sources:
- https://docs.microsandbox.dev/sandboxes/lifecycle.md — five-state lifecycle description; "idle_timeout=300 triggers draining after 5 minutes of inactivity"

### fleet_mgmt
Yes — `msb ls`/`msb ps`/`msb inspect` for listing/inspecting sandboxes, labels for "metric attribution" and bulk selection, named persistent sandboxes via `--name`, and a fleet-wide metrics endpoint returning "the latest metrics for every running sandbox at once." No dedicated multi-host/cross-machine registry was documented — fleet management is scoped to sandboxes known to the local `msb` state.
Sources:
- https://docs.microsandbox.dev/cli/overview.md — `msb ls`, `msb ps`, `msb inspect`, `--name` documented
- https://docs.microsandbox.dev/sandboxes/metrics.md — fleet-wide metrics query

### snapshots_persistence
Yes — snapshots capture the writable filesystem layer plus pinned image identity (explicitly NOT memory contents, running processes, or network state); restoring performs a cold boot into a new, independent sandbox with its own writable copy. Snapshots are portable (scp-able, archivable as `.tar.zst`, exportable with cached OCI images for offline use). Separately, named/persistent sandboxes retain config and state in the local database across stop/restart (not just snapshot-explicit state).
Sources:
- https://docs.microsandbox.dev/sandboxes/snapshots.md — "Stopped and crashed sandboxes can be snapshotted; running, draining, and paused sandboxes are rejected"; excludes "memory contents," "running processes," "network state"
- https://docs.microsandbox.dev/sandboxes/lifecycle.md — "Stopped - ...Sandbox configuration and state are persisted to the database and can be restarted"

## D. Setup

### setup
Easy — a single installer command (`curl -fsSL https://install.microsandbox.dev | sh` on Linux/macOS, `irm .../windows | iex` on Windows) or a one-line SDK install (`npm install microsandbox` / `pip install microsandbox` / `cargo add microsandbox` / `go get`), plus `msb doctor` to verify prerequisites. Docs claim "under 5 minutes from installation to running code" for a first `msb run python -- python3 -c "..."`. No account or API key required for local use. Caveat: hardware-virtualization prerequisites are real and platform-limiting — glibc-based Linux with KVM, Apple Silicon (M-series, no Intel Mac support) for macOS, or Windows 11 with WHP enabled.
Sources:
- https://docs.microsandbox.dev/getting-started/quickstart.md — install commands; "Under 5 minutes from installation to running code"; platform prerequisites list

## E. Daily use

### daily_use
Moderate — `msb run` gives ephemeral, auto-removed sandboxes with reportedly fast boots (README claims sub-100ms on M1; a vendor benchmark claims ~320ms cold boot on bare-metal Linux) and no image-rebuild step for cached OCI images; `msb start/stop/rm/ls/ps/inspect` manage named persistent sandboxes; `msb ssh`/exec give interactive attach without an in-guest SSH daemon. Friction point: network-policy changes require restarting/recreating the sandbox rather than a live edit (see live_rule_reload), which interrupts iterative allowlist tuning mid-session.
Sources:
- https://docs.microsandbox.dev/cli/overview.md — `msb run` "one-off command (ephemeral, auto-removed)"; command list
- https://github.com/zerocore-ai/microsandbox — "Boot times average under 100 milliseconds on M1 machines"

## F. Configuration

### config_depth
Deep — per-sandbox config spans image, CPU/memory (soft `cpus`/`memory` + hard `max_cpus`/`max_memory` caps), env vars, workdir, labels, mount types (bind volume, named volume, disk image), network policy (default posture + ordered allow/deny rules), secret bindings (host-allowlist-scoped), and lifecycle (`idle_timeout`, `max_duration`). A separate global `~/.microsandbox/config.json` covers home directory, log level, named backend profiles (local vs cloud, for switching deployment target), and per-registry auth (keyring/env/secret-file). Configuration is API/CLI-driven (SDK builder pattern, CLI flags) rather than a single declarative project-wide manifest file — no evidence of a version-controlled project-level config (equivalent to a checked-in `clawker.yaml`) was found; pre-boot bootstrap scripts and rootfs patches (`/.msb/scripts/`, patch operations, custom init system selection) serve as escape hatches for baking in setup logic without rebuilding the base image.
Sources:
- https://docs.microsandbox.dev/sandboxes/tuning.md — "A sandbox's configuration is the host-side record that says how to run it: image, CPU and memory, environment, labels, mounts, network policy, secrets, and lifecycle settings"
- https://docs.microsandbox.dev/configuration.md — global config file fields (home, log_level, active_profile, registries, database, paths, sandbox_defaults)
- https://docs.microsandbox.dev/sandboxes/bootstrap.md — scripts/patches/init-system pre-boot customization

### policy_model
Moderate, not fully policy-driven — secure-by-default carve-outs exist even under the permissive default posture (private ranges/loopback/link-local/metadata/host denied regardless), and a real allow/deny rule engine lets a user choose per-sandbox, at creation time: fully airgapped (`--no-net`), deny-by-default with explicit allow rules, or allow-public (default) — plus an optional restricted guest security profile for extra hardening. It falls short of "fully policy-driven" on two counts: network policy is locked at creation with no live tightening/loosening without recreating the sandbox (see live_rule_reload, firewall_escape_hatch), and workspace mode (bind-sync vs copy-on-write) is a per-mount choice rather than a single project-wide dial.
Sources:
- https://docs.microsandbox.dev/security/hardening.md — "switch from allow-public to deny-by-default and allow exactly what's required"
- https://docs.microsandbox.dev/cli/sandbox-commands.md — `--no-net`, `--net-default`, `--net-rule` creation-time flags

## G. DX — host↔sandbox integration

### bind_mount_sharing
Yes — bind-mounted volumes are bidirectional live sync via virtio-fs ("changes inside the sandbox are reflected on the host, and vice versa"), with read-only mounts also supported and host-side-enforced even against a privileged guest process.
Sources:
- https://docs.microsandbox.dev/sandboxes/volumes.md — "Changes inside the sandbox are reflected on the host, and vice versa"

### cred_forwarding
Partial — a bespoke secret-placeholder-substitution mechanism exists for env-var-shaped credentials scoped to specific allowed hosts (see credential_theft), which is a narrower/stronger model than raw agent-socket forwarding — the guest never sees the real value at all, even during use. No fetched documentation (sandboxes/ssh.md, security/secrets.md) describes ssh-agent or gpg-agent socket forwarding, or an automatic host git-credential-helper bridge, into the guest.
Sources:
- https://docs.microsandbox.dev/sandboxes/ssh.md — "The documentation provided does not contain information on: SSH agent forwarding, Git credential forwarding" (confirmed absent from this page's content)
- https://docs.microsandbox.dev/security/secrets.md — placeholder/substitution mechanism described in full

### browser_auth
Unknown — no fetched documentation (sandboxes/ssh.md, security/secrets.md, cli/overview.md, getting-started/quickstart.md) describes a host-browser-open-and-callback-forward mechanism for OAuth/device-code login flows triggered from inside a sandbox (the pattern `gh auth login` or `claude` login depend on). Docs are silent either way, so kept Unknown rather than No.

### shared_dirs
Yes — arbitrary additional bind-mounted directories and named volumes (directory-backed via virtiofs, disk-backed via virtio-blk) beyond the primary workspace mount are supported and can be mounted into multiple sandboxes over time.
Sources:
- https://docs.microsandbox.dev/sandboxes/volumes.md — named volumes "persist independently of any sandbox, so you can create a volume, populate it, and mount it into different sandboxes over time"

### git_worktrees
Unknown — no fetched documentation (sandboxes/filesystem.md, sandboxes/volumes.md, sandboxes/bootstrap.md, cli/overview.md) mentions git worktree support, positively or negatively. An ordinary bind mount of a host worktree directory would presumably function as a generic directory mount, but no first-class worktree feature (auto-detection, per-worktree sandbox naming/lifecycle) was documented.

### nested_containers
Partial — the volumes documentation names a `docker:dind` example image directly (`msb create docker:dind`), and the hardening docs separately note the optional restricted security profile "is incompatible with workloads such as sudo and Docker-in-Docker" (implying such workloads are expected to run under the default/unrestricted profile). However, no fetched page confirmed Docker-in-Docker actually functions end-to-end inside the microVM, and no docker-socket-passthrough mechanism analogous to bind-mount/virtio-fs was documented.
Sources:
- https://docs.microsandbox.dev/sandboxes/volumes.md — `msb create docker:dind` example referenced
- https://docs.microsandbox.dev/security/hardening.md — "This profile is incompatible with workloads such as sudo and Docker-in-Docker"

### harness_agnostic
Yes — a built-in MCP server works with "Claude, Codex, and other clients" per the official marketing site, plus a generic exec/shell/filesystem SDK surface (Rust/TypeScript/Python/Go) that can wrap any coding-agent CLI, not just one vendor's. An Agent Skills index (`.well-known/agent-skills/index.json`) is also referenced without vendor-lock-in language.
Sources:
- https://microsandbox.dev/ — "Use the microsandbox MCP server from Claude, Codex, and other clients"

## H. Performance

### performance
Lightweight, per vendor-published figures only — README claims boot times "average under 100 milliseconds on M1 machines"; a separate vendor benchmark (via WebSearch summary of microsandbox.dev content) claims ~320ms cold boot on bare-metal Linux versus the vendor's own Docker comparison (463ms) and "2.5x faster than Firecracker," using a vendor-authored `sandbox-bench` tool. A vendor blog post additionally claims a 47x guest-filesystem-throughput improvement from an OCI-filesystem rewrite. All performance figures found are vendor-published (README + microsandbox.dev blog/benchmarks) — no independent third-party benchmark was fetched or verified in this research, so these numbers should be read as vendor claims, not confirmed independently. Docs separately caveat that CPU-bound guest/host mode switching and virtio paravirtualized I/O are "not entirely zero-cost" versus native host syscalls.
Sources:
- https://github.com/zerocore-ai/microsandbox — "Boot times average under 100 milliseconds on M1 machines" (vendor)
- WebSearch summary of microsandbox.dev — "~320ms on bare-metal Linux — faster than Docker (463ms) and 2.5x faster than Firecracker per official benchmarks" (vendor benchmark, not independently fetched in full)

## I. Feasibility

### feasibility
Adoptable today with caveats — cross-platform (glibc Linux+KVM, macOS Apple Silicon, Windows 11+WHP), Apache-2.0, no account required for local use, 4-language SDK coverage, single-command CLI install, `msb doctor` prerequisite check. Caveats: explicitly labeled "beta software" with breaking changes/missing features/rough edges expected by the maintainers; no Intel Mac support; requires hardware virtualization capability (KVM/HVF/WHP), which is unavailable in some nested-virtualization or restricted CI environments without KVM passthrough; the project underwent an organization rename (Zerocore AI → Super Rad Company, repo moved zerocore-ai → superradcompany) during its life, a minor signal of organizational flux though repo/doc continuity appears intact.
Sources:
- https://github.com/zerocore-ai/microsandbox — "Currently beta software with breaking changes, missing features, and rough edges expected"
- https://docs.microsandbox.dev/getting-started/quickstart.md — platform prerequisites (glibc Linux+KVM, Apple Silicon, Windows 11+WHP)
- WebSearch (rename confirmation) — "The GitHub repositories reflect this rebranding—while the organization was originally zerocore-ai, the microsandbox project is now hosted at superradcompany/microsandbox"

## J. Price (prose-only)

### pricing
Local/self-hosted runtime is free under Apache 2.0 with no license fee or usage metering documented for local use ("Running locally or on your own infrastructure is free, forever," per a WebSearch-summarized quote from the repo's SELF_HOSTING.md — not directly WebFetched in full). A separate hosted "microsandbox cloud" is in closed beta with a waitlist as of the sources found (~June 2026); no public pricing has been published for the cloud tier, and it will carry "its own commercial license" distinct from the Apache-2.0 local runtime.
Sources:
- WebSearch summary citing github.com/microsandbox/microsandbox/blob/main/SELF_HOSTING.md — "Running locally or on your own infrastructure is free, forever" (WebSearch-summarized, not independently WebFetched as a full page in this research)
- WebSearch summary citing microsandbox.dev — "the cloud platform has moved from 'launching soon' to a closed beta with a waitlist"; "cloud pricing arrives with the beta"

## K. Extensibility

### extensibility
Yes — custom images from any OCI-compatible registry (Docker Hub, GHCR, ECR, GCR) with per-registry auth config; pre-boot customization primitives including bundled scripts mounted at `/.msb/scripts/`, rootfs patch operations (file/dir creation, copy, symlink, removal) applied sequentially before boot, and custom init system selection (systemd/OpenRC/s6/auto, with args/env passed to PID 1); a built-in MCP server and Agent Skills index for agent-tooling integration; four-language SDKs (Rust/TypeScript/Python/Go) as programmatic extension points; named backend "profiles" in the global config for swapping local vs cloud execution targets. No plugin/bundle marketplace or third-party extension registry (analogous to a package-manager ecosystem) was found in the fetched documentation.
Sources:
- https://docs.microsandbox.dev/sandboxes/bootstrap.md — scripts/patches/init-system customization detail
- https://docs.microsandbox.dev/configuration.md — registries config, backend profiles
- https://microsandbox.dev/ — MCP server, Agent Skills index reference

## Unknowns & caveats

- **http_path_rules**: Unknown — docs mention interception enables "URL-level policy checks" but no explicit per-path/method rule syntax was found in the documented `NetworkPolicy` model (domain/CIDR/group/port/protocol only).
- **fail_closed**: Unknown — no official page addresses host-broker-crash behavior for network enforcement; a structural inference (single virtio-net path, no host-kernel fallback → likely fails closed) is offered in the writeup but is NOT a documented guarantee.
- **browser_auth**: Unknown — docs-silent on host-browser OAuth/device-code proxying; not confirmed present or absent.
- **git_worktrees**: Unknown — docs-silent on first-class git worktree support.
- **nested_containers**: Partial/unconfirmed — a `docker:dind` example image is named and the restricted-profile incompatibility note implies DinD is expected to work under the default profile, but no page confirmed it functions end-to-end.
- **network_audit**: Partial — metrics/logs and an OTel sidecar exist, but no dedicated per-request egress audit log (allow/deny verdicts per connection) was documented.
- **performance figures**: all vendor-published (README + microsandbox.dev blog/benchmark tool); no independent third-party benchmark was located/fetched.
- **pricing / SELF_HOSTING.md**: sourced via WebSearch summarization rather than a direct WebFetch of the full page — the exact wording of the "free forever" claim and cloud-beta details should be re-verified by fetching `https://github.com/microsandbox/microsandbox/blob/main/SELF_HOSTING.md` and `https://microsandbox.dev/` directly if higher confidence is needed.
- **docs.microsandbox.dev/guides/mcp/**: returned HTTP 404 on direct WebFetch (not a firewall block — a content-not-found). A WebSearch snippet suggests an older/parallel doc path structure (`references/cli`, `references/api`, `guides/`) may exist alongside the `llms.txt`-enumerated path set (`sandboxes/`, `security/`, `networking/`, `cli/`, `sdk/`) used for the bulk of this research; not reconciled. MCP capability claims in this writeup instead rely on the https://microsandbox.dev/ homepage (directly fetched) which independently confirms MCP server + Agent Skills support.
- **Org/repo rename**: `zerocore-ai/microsandbox` (seed URL) → `superradcompany/microsandbox` (current). No firewall blocks occurred (operational bypass was active); this is a redirect/rename, not a blocked URL.
- No URLs were blocked by the egress firewall during this research (firewall was bypassed per operational note); the only fetch failure was the 404 above.

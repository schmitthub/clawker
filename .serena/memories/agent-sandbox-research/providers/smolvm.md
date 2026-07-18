# SmolVM
category: local (primitive/self-hosted microVM runtime; sibling hosted-cloud product exists but is a separate offering, see A.execution_locality)
Open-source microVM sandbox runtime for AI coding/browser/computer-use agents | built on Firecracker (Linux) / QEMU (macOS) / libkrun | Apache 2.0 | 701 stars, 55 forks, v0.0.27 (63 releases), backed by Celesto AI (London) — as of 2026-07-18

**Identity note:** Two distinct GitHub projects are named "SmolVM": `CelestoAI/SmolVM` (AI-agent sandbox infra — HN launch title "open-source sandbox for coding and computer-use agents") and `smol-machines/smolvm` (generic OCI-image-booting portable VM, not AI-agent-specific). Given this comparison's scope (AI-coding-agent sandboxes), `CelestoAI/SmolVM` is the confidently identified subject — its own framing, docs, and HN launch explicitly target coding/computer-use agents. `smol-machines/smolvm` was not assessed.

## A. Identity

### built_on (prose-only)
Unified Python API wrapping three VMMs: Firecracker (native on Linux), QEMU (native on macOS, also used for Windows guests), and libkrun (mentioned in repo description, less documented). Each sandbox is a real hardware-virtualized microVM with its own guest kernel (KVM on Linux, presumably HVF-equivalent via QEMU on macOS) — not a shared-kernel container. Host-side networking (NAT, port-forward, inter-VM isolation) is implemented via nftables on a per-sandbox TAP device; no separate control-plane daemon is documented beyond an optional local HTTP API server (`smolvm server`) and dashboard (`smolvm ui`).
Sources:
- https://github.com/CelestoAI/SmolVM — "Open-source AI sandbox infrastructure with unified API for VMMs -- Firecracker, QEMU and libkrun."
- https://docs.celesto.ai/smolvm/concepts/networking.md — "SmolVM uses nftables to manage NAT and firewall rules."

### execution_locality
Local — determination: Local. SmolVM itself always executes on the developer's own machine (Linux or macOS host); there is no managed/remote execution mode for SmolVM proper. Celesto AI (same company) separately offers "Celesto," a hosted cloud platform built on the same open-source stack (SmolVM/SmolFS/Agentor) where agent execution happens on Celesto's managed servers instead — but this is an explicitly distinct product/deployment, not a mode of SmolVM itself. Project code and host directories mounted into a local SmolVM sandbox never leave the local machine by default (mounts are host-directory mounts into a local VM, not uploads to a remote service).
Sources:
- https://docs.celesto.ai/getting-started/hosted-and-open-source.md — "SmolVM provides open-source microVM sandboxes with fast boot and hardware-level isolation, running entirely under your control on local infrastructure... Everything executes locally; no data transmits to external services by default." / "many teams use local SmolVM during development and hosted Celesto in production"

### open_source (prose-only)
Apache 2.0 license, fully self-hostable (that is in fact its only mode — see execution_locality). Source at github.com/CelestoAI/SmolVM.
Sources:
- https://raw.githubusercontent.com/CelestoAI/SmolVM/main/README.md — "Apache 2.0 — see [LICENSE](LICENSE) for details."

### maturity (prose-only)
701 GitHub stars, 55 forks, 63 releases, latest tag v0.0.27 (July 17 2026) — pre-1.0 versioning signals an actively-developed but early-stage project. Backed by Celesto AI, a company also selling a hosted product built on the same stack. Has a CONTRIBUTING.md, SECURITY.md with a private-advisory vulnerability process, and CI.
Sources:
- https://github.com/CelestoAI/SmolVM — stars: 701, forks: 55, latest release v0.0.27

## B. Threat protection

### host_fs_damage
Yes — guest cannot touch host filesystem outside explicitly configured mounts, and mounts are read-only by default. Documentation states malicious guest code cannot access the host filesystem except through configured shared volumes, and that unmounted host paths are simply unreachable (separate VM disk/kernel). Writable mounts are opt-in per directory via `--writable-mounts`, with an explicit trust warning in the docs.
Sources:
- https://docs.celesto.ai/smolvm/concepts/security.md — "Malicious guest code cannot: Access host filesystem except through configured shared volumes"
- https://docs.celesto.ai/smolvm/features/host-mounts.md — "Writable mounts give the sandbox full write access to the mounted host directories. Make sure you trust the code running inside the sandbox before enabling this flag."

### credential_theft
Partial — no host secrets are baked into images or auto-inherited from the host shell, but the project ships explicit convenience paths that intentionally pull host credentials into the guest, and the transport mechanism for those paths is not documented. Env vars require deliberate opt-in code (`os.getenv(...)` then pass to `env_vars`); some coding-agent presets auto-forward specific API-key env vars. Separately, the `smolvm claude start` / `smolvm codex start` / `smolvm pi start` one-command flows state that existing host CLI login credentials ("from `claude login`, `codex login`, etc.") are "forwarded into the sandbox automatically" — official docs assert this happens but do not specify whether via a mediated agent-socket-style forwarding or a copied token/file, so the isolation strength of that specific mechanism is unclear.
Sources:
- https://docs.celesto.ai/smolvm/guides/environment-variables.md — "you can forward environment variables from your host machine into the sandbox" / "Collect API keys from host environment."
- https://docs.celesto.ai/smolvm/features/coding-agents.md — "Your existing credentials (from `claude login`, `codex login`, etc.) are forwarded into the sandbox automatically — no re-authentication needed."

### data_exfiltration
Partial — an outbound domain allowlist exists and can restrict egress, but it is opt-in (default is fully open), domain-granularity only, and does not apply to several of the tool's own networking modes. See axis C network sub-criteria for full detail. Docs explicitly caution the allowlist is "not a complete security boundary for untrusted code."
Sources:
- https://docs.celesto.ai/smolvm/features/network-controls.md — "By default, sandboxes have full internet access."
- https://raw.githubusercontent.com/CelestoAI/SmolVM/main/docs/guides/networking.md — "Treat an allow-list as a network control, not as a complete security boundary for untrusted code."

### malicious_execution
Yes — blast radius of hallucinated/malicious code is contained to hardware-isolated VM boundary rather than shared-kernel container boundary. See escape_resistance below for the isolation-strength argument; combined with host_fs_damage (=Yes) and default-open network (partial), a compromised/malicious process inside the guest cannot directly touch host processes, files outside mounts, or other sandboxes, but can freely reach the internet unless an allowlist was configured.
Sources:
- https://celesto.ai/blog/posts/smolvm/safely-run-ai-generated-code/ — "containers share the host kernel... SmolVM uses a real hypervisor, a real guest kernel, and a hardware virtualization boundary"

### escape_resistance
Isolation boundary stronger than plain process/container — hardware-virtualized microVM (Firecracker/QEMU/KVM), not a shared-kernel container. Docs frame the security delta explicitly against Docker: a container escape/kernel-privesc CVE compromises the host directly because containers are host processes wrapped in namespaces/cgroups sharing one kernel, whereas a SmolVM sandbox has its own guest kernel and would require a hypervisor exploit (not just a guest-kernel bug) to reach the host. No independent (non-vendor) escape-testing evidence was found; this determination rests on architectural description, not third-party red-team results.
Sources:
- https://docs.celesto.ai/smolvm/concepts/security.md — "its own kernel" via KVM (Linux) or HVF (macOS), requiring "a hypervisor exploit, not just a kernel vulnerability" for escape
- https://celesto.ai/blog/posts/smolvm/safely-run-ai-generated-code/ — "a process inside a container is just a normal Linux process with some namespaces and cgroups wrapped around it"

### resource_abuse
Yes — per-VM CPU/memory/disk caps are configurable at creation. `vcpu_count` and `memory` (MiB) are set via VMConfig / constructor kwargs (example shows vcpu_count max 2, memory max 512 MiB in one config), `disk_size` controls root filesystem size (default 512 MiB). Docs don't describe a default cap applied automatically if the caller doesn't set one, nor cgroup-level enforcement details beyond "dedicated, capped resources per VM."
Sources:
- https://docs.celesto.ai/smolvm/concepts/security.md — "Each VM has dedicated, capped resources" (example: vcpu_count max 2, memory max 512 MiB)
- https://docs.celesto.ai/smolvm/api/smolvm.md — `disk_size` (int | None) — "Root filesystem size in MiB for auto-config mode. Default is 512 MiB"

## C. Feature set & granularity

### network_default_posture
No — default is fully open, not deny-by-default. An unconfigured sandbox has unrestricted outbound internet access; the allowlist is an explicit opt-in the caller must configure via `internet_settings`.
Sources:
- https://docs.celesto.ai/smolvm/features/network-controls.md — "By default, sandboxes have full internet access."
- https://raw.githubusercontent.com/CelestoAI/SmolVM/main/docs/guides/networking.md — 'Use "*" to allow all domains, which is the default.'

### egress_allowlist
Partial — domain-level allowlist exists, but granularity stops there: no subdomain-wildcard syntax, no IP/CIDR rules, no port scoping, and no deny-rule/precedence semantics are documented — the primitive is a single `allowed_domains` list (hostnames or bare URLs, path component discarded). "Entries may be hostnames or URLs without a path" — so no path-scoping either (see http_path_rules).
Sources:
- https://docs.celesto.ai/smolvm/api/internetsettings.md — "`allowed_domains` (list[str], required) — List of domain names the sandbox is allowed to connect to. SmolVM normalizes entries by removing protocols and trailing slashes"
- https://raw.githubusercontent.com/CelestoAI/SmolVM/main/docs/guides/networking.md — "Entries may be hostnames or URLs without a path; SmolVM stores their hostnames."

### dns_level_blocking
Unknown — docs describe the allowlist enforcement only as nftables-based NAT/firewall rules on the host side of the TAP device; no page explicitly states whether blocking happens via DNS interception/resolution-time filtering or purely IP/rule-based post-resolution filtering. Searched concepts/networking.md, features/network-controls.md, and the raw networking guide; none specify the resolution-time mechanism.
Sources:
- https://docs.celesto.ai/smolvm/concepts/networking.md — "SmolVM uses nftables to manage NAT and firewall rules."

### tls_mitm_inspection
Unknown / leaning No — no TLS interception or MITM proxy is described anywhere in the docs; the only enforcement mechanism documented is nftables (a network/L3-L4 tool, not a TLS-terminating proxy), and the allowlist matches on hostname/domain, not on inspected request content. No explicit "no MITM" statement exists, but nothing in the architecture (host-side nftables on a TAP device) supports TLS termination, so this leans No rather than true Unknown.
Sources:
- https://docs.celesto.ai/smolvm/concepts/networking.md — "SmolVM uses nftables to manage NAT and firewall rules" (no proxy/CA component described anywhere in docs)

### http_path_rules
No — path- and method-scoped rules are explicitly not implemented. The networking guide states allowlist entries cannot include a path (only hostnames), and HTTP method restrictions are explicitly called out as unimplemented.
Sources:
- https://raw.githubusercontent.com/CelestoAI/SmolVM/main/docs/guides/networking.md — "HTTP method restrictions are reserved for future work and are not enforced."

### proto_coverage
Partial — only domain-based (implicitly HTTP/HTTPS-style) egress is documented in the allowlist feature; there is no mention of DNS-, ICMP-, UDP-, QUIC/HTTP3-, or opaque-L7-protocol-specific rule scoping, and no documented extensibility model for adding new protocols to the rule system. Separately, SSH access into the guest is a first-class, well-documented protocol path (forwarded/DNAT'd by nftables), but that's inbound control-plane access, not an outbound-rule protocol category.
Sources:
- https://docs.celesto.ai/smolvm/features/network-controls.md — allowlist examples only show `curl https://...` traffic
- https://docs.celesto.ai/smolvm/concepts/networking.md — port-forwarding described as TCP DNAT rules; no UDP/ICMP/QUIC egress-control mention found

### live_rule_reload
Unknown — `internet_settings`/`allowed_domains` is documented only as a sandbox-creation-time constructor parameter (`SmolVM(internet_settings={...})`); no documentation describes changing the allowlist on an already-running sandbox without recreating it, in contrast to `set_env_vars()` which is explicitly documented as a runtime operation.
Sources:
- https://docs.celesto.ai/smolvm/features/network-controls.md — allowlist shown only at `SmolVM(internet_settings=...)` construction time

### firewall_escape_hatch
Unknown — no timed-bypass or per-sandbox disable/re-enable mechanism for an already-configured allowlist is documented. (Note: since the default posture is open, most workflows simply never configure `internet_settings`; but for a sandbox that has one configured, no break-glass mechanism is documented.)
Sources:
- https://raw.githubusercontent.com/CelestoAI/SmolVM/main/docs/guides/networking.md — full guide reviewed, no bypass/disable mechanism mentioned

### enforcement_plane
Kernel-level, host-side, outside the guest — nftables rules on the host apply to the sandbox's TAP network device; the guest never has visibility into or control over the host's nftables tables, so it structurally cannot tamper with or route around the enforcement point from inside the VM (this is a stronger guarantee than an in-guest agent or userspace-in-guest proxy would give). Two nftables tables are used: `ip smolvm_nat` (masquerade/DNAT) and `inet smolvm_filter` (inter-sandbox isolation + forwarding). No traffic-logging-at-this-layer is documented (see network_audit).
Sources:
- https://docs.celesto.ai/smolvm/concepts/networking.md — "`ip smolvm_nat` — handles NAT (masquerade for outbound traffic, DNAT for port forwarding)" and "`inet smolvm_filter` — handles forwarding rules and sandbox isolation"

### fail_closed
Unknown — no documentation describes what happens to an active `allowed_domains` restriction if the host-side SmolVM process/CLI that set up the nftables rules is killed or crashes (e.g., whether nftables rules persist independently since they're kernel state, or whether some supervisor is required to keep them applied).
Sources:
- (searched) https://docs.celesto.ai/smolvm/concepts/networking.md and networking guide — no mention of supervisor-crash behavior for firewall rules

### network_audit
Unknown — no per-request egress logging feature is documented anywhere in the network-controls, networking-concepts, or CLI docs reviewed.
Sources:
- (searched) https://docs.celesto.ai/smolvm/features/network-controls.md, https://docs.celesto.ai/smolvm/concepts/networking.md — neither mentions request-level audit logs

### workspace_modes
Yes — both live bind-mount and ephemeral/copy-on-write modes are offered. Host directories can be mounted read-only (default, changes stay in the VM's overlay and never touch the host — effectively ephemeral from the host's perspective) or explicitly writable (`--writable-mounts`, changes reflect on host immediately, i.e., live bind mount). Multiple directories can be mounted at custom in-guest paths simultaneously.
Sources:
- https://docs.celesto.ai/smolvm/features/host-mounts.md — "the sandbox can read every file, but changes stay inside the sandbox and never touch the originals" (default) vs. "Changes made inside the sandbox immediately reflect on your host" (`--writable-mounts`)

### observability
Yes — a local dashboard (`smolvm ui`) provides VM status, resource usage, and logs; a local HTTP API server (`smolvm server`) exposes sandbox state via REST/OpenAPI. Scope is local-only by default (dashboard binds `127.0.0.1:8080`); no mention of a hosted/centralized monitoring stack for SmolVM itself (that would be Celesto, the separate hosted product).
Sources:
- https://docs.celesto.ai/smolvm/cli/ui.md — "View VM status, resource usage, and logs" / dashboard "binds to `127.0.0.1:8080`" by default

### supervision
Partial — the dashboard/local API allow a human to inspect and forcibly stop/delete a running sandbox (manual containment), but no automated behavioral supervisor is documented: nothing observes agent activity and automatically intervenes (kill/quarantine) based on detected misbehavior. This is human-in-the-loop management tooling, not an active oversight layer per the criterion's stricter definition.
Sources:
- https://docs.celesto.ai/smolvm/cli/ui.md — "Create, start, stop, and delete VMs" / "inspect running sandboxes, view logs, and manage common actions"

### fleet_mgmt
Partial — `smolvm sandbox list` gives a local inventory (name, status, pid, IP, ssh port, warnings; `--all`/`--status` filters; JSON output for scripting), but this is single-host, name-keyed listing — no documented multi-host registry, hierarchical naming, or project-scoped grouping concept.
Sources:
- https://docs.celesto.ai/smolvm/cli/list.md — "shows the sandboxes on your machine. Use it to find a sandbox name, check whether one is still running, or feed sandbox data into a script."

### snapshots_persistence
Yes — three snapshot types are documented: Full (disk+memory+CPU state, exact resume), Diff (changed-disk-only, smaller), and Disk (filesystem-only, cold boot on restore). Stop/start (as opposed to snapshot) also persists root-disk state across a stop. Limitations: Windows guests, workspace mounts, and extra drives cannot be snapshotted; QEMU raw disks built with `grow_filesystem=True` can't be snapshotted; a given snapshot restores only once by default (`force` flag needed for repeat restores); a snapshot can't be deleted while a VM restored from it is still running.
Sources:
- https://docs.celesto.ai/smolvm/features/snapshots.md — "a complete, self-contained disk copy plus the guest's memory and CPU state" (Full); "resume the sandbox exactly where you left off"

## D. Setup
setup: Easy — no Docker required; either a single install script (`curl -sSL https://celesto.ai/install.sh | bash`) or three manual commands (`pip install smolvm && smolvm setup && smolvm doctor`). Prerequisites are OS-native: Linux needs KVM (`/dev/kvm`) and may need `sudo` to install Firecracker/nftables/iproute2 and configure KVM group permissions (session restart or `newgrp kvm` required after); macOS needs Homebrew (installs QEMU). Python 3.10+ required both platforms. No account/API-key/cloud dependency for the local OSS path.
Sources:
- https://docs.celesto.ai/smolvm/installation.md — quick install `curl -sSL https://celesto.ai/install.sh | bash`; manual `pip install smolvm`, `smolvm setup`, `smolvm doctor`; "Linux may require sudo access during setup... After Linux setup, activate KVM group with `newgrp kvm` or restart session"

## E. Daily use
daily_use: Unknown/light-to-moderate (low confidence) — the CLI surface documented (`sandbox create/shell/stop/delete`, `sandbox port`, `sandbox env`, `sandbox snapshot`) suggests a lightweight day-to-day loop (create once, shell in, expose ports as needed, snapshot before risky steps), and coding-agent presets reduce it to one command (`smolvm claude start`). However, no documentation or third-party report was found describing real iteration friction (e.g., rebuild time on dependency/image changes, behavior on host file edits under a writable mount, restart cadence for long sessions) to ground a confident spectrum call.
Sources:
- https://docs.celesto.ai/smolvm/cli/overview.md — command list (`sandbox`, `sandbox snapshot`, `sandbox file`, `sandbox env`, `sandbox port`, `server`, `ui`, `doctor`)
- https://docs.celesto.ai/smolvm/features/coding-agents.md — `smolvm claude start` / `smolvm codex start` / `smolvm pi start`

## F. Configuration

### config_depth
Moderate — configuration is programmatic (Python `SmolVM`/`VMConfig` constructor kwargs) or CLI-flag based rather than a single declarative project-config file. Documented tunable scope: image/OS selection (`BootImage`, custom Dockerfile via `DockerRootfsBuilder`, `ImageBuilder`, existing qcow2/ext4/Windows images), packages (via custom Dockerfile), resource caps (`memory`, `disk_size`, `vcpu_count`), env vars (boot-time + runtime `set_env_vars`), mounts (multiple, custom in-guest paths, per-mount writability), network (`internet_settings.allowed_domains`, bridged-mode escape hatch), backend selection (`firecracker`/`qemu`/`auto`), and callbacks around `run()`. No single versionable manifest ties all of this together the way a devcontainer.json or similar would — it's assembled in code or via CLI flags per invocation. Custom Docker-based images are cacheable/reusable and are the closest thing to a versionable declarative config.
Sources:
- https://docs.celesto.ai/smolvm/api/smolvm.md — constructor params (memory, disk_size, backend, ssh_user, callbacks, etc.)
- https://docs.celesto.ai/smolvm/guides/custom-images.md — `DockerRootfsBuilder` params: `dockerfile`, `context`, `rootfs_size_mb`, `build_args`, `ssh_capable`; caching under `~/.smolvm/images/`

### policy_model
Moderate, inconsistent defaults, not fully policy-driven — individual controls are independently dial-able (mount read-only vs writable per directory; network open vs allowlisted; VMM backend auto vs pinned; snapshot type full/diff/disk), but there is no single security-policy object/profile that ties them together, and the secure-by-default posture is inconsistent across dimensions: mounts default to the safe choice (read-only) while network defaults to the permissive choice (fully open, must opt in to restrict). No documented per-sandbox break-glass/bypass toggle for an already-restricted network policy (see firewall_escape_hatch=Unknown).
Sources:
- https://docs.celesto.ai/smolvm/features/network-controls.md — network defaults open
- https://docs.celesto.ai/smolvm/features/host-mounts.md — mounts default read-only

## G. DX

### bind_mount_sharing
Yes — see workspace_modes (C). Live writable bind-mount (`--writable-mounts`) and read-only live-view mount are both supported; changes under a writable mount reflect on the host immediately, not just at session end.
Sources:
- https://docs.celesto.ai/smolvm/features/host-mounts.md — "Changes made inside the sandbox immediately reflect on your host."

### cred_forwarding
Partial — official docs assert host coding-agent CLI logins (`claude login`, `codex login`, etc.) and "git credentials" are automatically forwarded into one-command coding-agent sandboxes, but the underlying transport mechanism (mediated agent-socket forwarding vs. copied token/config file) is not documented, so the isolation strength of the forwarding can't be confirmed from official sources. General env-var-based secret forwarding (e.g., API keys) is also supported but requires explicit opt-in code, not automatic host-shell inheritance.
Sources:
- https://docs.celesto.ai/smolvm/features/coding-agents.md — "Your existing credentials (from `claude login`, `codex login`, etc.) are forwarded into the sandbox automatically — no re-authentication needed."
- https://raw.githubusercontent.com/CelestoAI/SmolVM/main/README.md — "With a single command you get a claude/codex pre-installed sandbox ready with git credential"

### browser_auth
Unknown — SmolVM documents an in-guest browser (Chromium inside the VM, reachable via a viewer URL / CDP / VNC) for browser-automation/computer-use workloads, but this is a different capability than proxying a host-browser-open event (e.g., an OAuth device-code or `gh auth login`-style callback triggered from inside the sandbox and completed in the developer's actual host browser). No documentation found describing such a host-browser-proxy mechanism for SmolVM itself.
Sources:
- (searched) https://docs.celesto.ai/smolvm/features/coding-agents.md, https://docs.celesto.ai/smolvm/features/port-forwarding.md, https://docs.celesto.ai/getting-started/browser-agents.md-adjacent pages — no host-browser-proxy / OAuth-callback-forwarding mechanism documented; only in-guest-browser viewing was found

### shared_dirs
Yes — multiple host directories can be mounted simultaneously at distinct custom in-guest paths in one invocation (`--mount ~/Projects/my-app:/code --mount ~/data:/mnt/data`), beyond the default single `/workspace` mount.
Sources:
- https://docs.celesto.ai/smolvm/features/host-mounts.md — `smolvm sandbox create --mount ~/Projects/my-app:/code --mount ~/data:/mnt/data`

### git_worktrees
Unknown — no documentation found describing first-class git-worktree awareness or handling; SmolVM's mount feature operates on arbitrary host directories generically, with no worktree-specific behavior documented either way.
Sources:
- (searched) host-mounts.md, cli/create.md, ai-agent-integration guide — no worktree mention found

### nested_containers
Unknown — no documentation found stating whether a container runtime (Docker/Podman) is available or supportable inside a SmolVM guest (docker-socket opt-in, DinD, or otherwise). Each sandbox is a full VM with its own kernel and root access, which would technically permit installing/running a container runtime inside via a custom image, but this is inference from the architecture, not a documented, supported feature.
Sources:
- (searched) concepts/security.md, guides/custom-images.md — neither confirms nor denies an in-guest container runtime

### harness_agnostic
Partial — three coding-agent CLIs get first-class, one-command preinstalled treatment (`smolvm claude start`, `smolvm codex start`, `smolvm pi start`; a fourth, `smolvm hermes start`, is also documented), which is narrower than fully harness-agnostic, but arbitrary other harnesses/tools can be installed via custom Dockerfile-based images (`DockerRootfsBuilder`), so the tool is not hard-locked to those four either.
Sources:
- https://docs.celesto.ai/smolvm/features/coding-agents.md — `smolvm claude start`, `smolvm codex start`, `smolvm hermes start`, `smolvm pi start`
- https://docs.celesto.ai/smolvm/guides/custom-images.md — Dockerfile-based custom image builder for arbitrary tooling

## H. Performance
performance: Lightweight (vendor-claimed, unverified by third party) — vendor docs state VMs are "ready in ~413 ms," idle guest overhead around "~128MB of RAM and a few hundred MB of disk," and that "dozens of microVMs" can run on a single host. No independent/third-party benchmark was located to corroborate these figures, and no macOS-specific bind-mount IO throughput numbers were found (a known weak point for VM/container file-sharing on macOS in general).
Sources:
- https://docs.celesto.ai/smolvm/introduction — "VMs ready in ~413 ms" (vendor claim)
- https://celesto.ai/blog/posts/smolvm/safely-run-ai-generated-code/ — "~128MB of RAM and a few hundred MB of disk" / "You can run dozens of microVMs on a single host" (vendor blog)

## I. Feasibility
feasibility: Adoptable today for Linux/macOS solo devs, with early-stage-maturity caveats — platform support is Linux (native, best-supported, Firecracker) and macOS (QEMU, Apple Silicon or Intel); there is no Windows host support (Windows is only available as a guest OS, and only on a Linux host). Apache 2.0 licensing avoids vendor lock-in and the codebase is inspectable/self-hostable. The pre-1.0 version scheme (v0.0.27) and terse SECURITY.md disclaiming any fix-timeline commitments both signal a young project where API/behavior stability risk is real for production adoption, even though release cadence (63 releases) suggests active maintenance.
Sources:
- https://docs.celesto.ai/smolvm/installation.md — Linux (Ubuntu/Debian/Fedora, x86_64) and macOS (Apple Silicon/Intel) prerequisites; Windows 11 listed only as a guest OS
- https://github.com/CelestoAI/SmolVM/blob/main/SECURITY.md — "policy explicitly disclaims contractual obligations regarding response timelines, fix commitments, or compensation"

## J. Price (prose-only)
SmolVM itself (the OSS runtime assessed here) is free — Apache 2.0, self-hosted, no license fee, no account required for the local install path. Celesto AI separately sells "Celesto," a hosted/managed version of the same stack; its pricing page (celesto.ai/pricing) returned no extractable tier/cost information when fetched (likely a JS-rendered SPA shell) — hosted-product pricing is Unknown, not zero.
Sources:
- https://raw.githubusercontent.com/CelestoAI/SmolVM/main/README.md — "Apache 2.0"
- https://docs.celesto.ai/getting-started/hosted-and-open-source.md — describes hosted Celesto as the commercial counterpart to open-source SmolVM

## K. Extensibility
Yes — multiple documented extension points: custom sandbox images via Dockerfile (`DockerRootfsBuilder`) or pre-built disk images (`BootImage`, including qcow2 cloud images and Windows images); pluggable VMM backend selection (firecracker/qemu/auto, plus libkrun per repo description); a local HTTP API server (`smolvm server`, OpenAPI-documented) for external tooling to drive sandboxes without the Python SDK; documented integration patterns for OpenAI Agents SDK, LangChain, PydanticAI, and Celesto's own "Agentor" framework; and callback hooks around `run()` in the Python API.
Sources:
- https://docs.celesto.ai/smolvm/guides/custom-images.md — Dockerfile/BootImage/ImageBuilder custom image paths
- https://docs.celesto.ai/smolvm/cli/server.md — local HTTP API, "REST endpoints with OpenAPI documentation available at `/openapi.json`"

## Unknowns & caveats
- **Identity**: assessed `CelestoAI/SmolVM`; a same-named, unrelated project `smol-machines/smolvm` exists and was NOT assessed — do not conflate the two in the published comparison.
- **DNS-level blocking, TLS/MITM, live rule reload, firewall escape hatch, fail-closed behavior, network audit logging**: all Unknown due to docs silence, not confirmed absence — the official docs are simply thin on enforcement-implementation detail beyond "nftables on the host, domain-only allowlist."
- **cred_forwarding mechanism** (SSH-agent-style mediated forwarding vs. copied secret) for the one-command coding-agent flows: asserted by docs but not explained; could not verify strength of isolation.
- **nested_containers, git_worktrees, browser_auth (host-browser-proxy for OAuth)**: no documentation found either way; architecture makes some of these plausible (full VM, own kernel) but nothing is confirmed/supported as a documented feature.
- **Celesto (hosted) pricing**: page fetched but contained no pricing content (JS SPA); not treated as a blocked URL (it did resolve), just informationally empty.
- No blocked URLs encountered (no NXDOMAIN/connection-refused/Envoy-403s) — all fetches resolved to content, some just informationally thin.
- **Third-party sourcing note**: celesto.ai/blog performance figures and the escape_resistance Docker-comparison framing are vendor blog content (marked as such above), not independent benchmarks or third-party security audits — no independent escape-testing or performance benchmark was located for SmolVM.

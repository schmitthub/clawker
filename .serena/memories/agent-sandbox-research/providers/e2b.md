# E2B
category: cloud
Managed cloud platform for running AI-agent-generated code in isolated, ephemeral Firecracker microVMs, accessed via SDK/API/CLI | built on Firecracker microVMs (KVM) | Apache-2.0 (SDKs/client open-source; managed control plane closed, self-host infra repo available) | founded 2023, $43.8M raised incl. $21M Series A (Insight Partners, Jul 2025), ~13k GitHub stars on e2b-dev/E2B, cites "94% of Fortune 100" usage on marketing site

As-of date: 2026-07-18. All claims below sourced from official E2B docs (e2b.dev / e2b.mintlify.app) unless marked third-party.

## A. Identity
### built_on
(prose-only) Each sandbox is a Firecracker microVM: "Each sandbox is powered by Firecracker, a microVM made to run untrusted workflows." Sandboxes are created/managed via E2B's cloud control plane (orchestrators, edge controllers) reachable through REST API / SDK / CLI. An in-sandbox "sandbox controller" (envd) exposes filesystem/exec APIs consumed by the SDK, gated by an access token issued at creation.
Sources:
- https://e2b.dev/ — "Each sandbox is powered by Firecracker, a microVM made to run untrusted workflows."
- https://e2b.mintlify.app/docs/sandbox/secured-access.md — "each call to the sandbox controller must include an additional header `X-Access-Token`"

### execution_locality
execution_locality: Remote — sandboxes run on E2B-managed cloud infrastructure (or, for Enterprise, a customer-owned VPC via BYOC); code, files, and any forwarded credentials transit to that remote VM, they do not execute on the developer's own machine. BYOC keeps traffic inside the customer's own cloud account rather than E2B's, but it is still a separate remote deployment, not local execution: "All potentially sensitive traffic...is transmitted directly from the client to the customer's BYOC VPC without ever touching the E2B Cloud infrastructure." Default/self-serve tiers (Hobby/Pro) run on E2B's own multi-tenant cloud.
Sources:
- https://e2b.dev/ — "a fast, secure Linux VM created on demand for your agent"
- https://e2b.mintlify.app/docs/byoc.md — "BYOC (Bring Your Own Cloud) allows you to deploy E2B sandboxes to your own cloud infrastructure within your VPC."

### open_source
(prose-only) Apache-2.0 licensed. github.com/e2b-dev/E2B contains the JS/TS and Python SDKs (incl. Code Interpreter variants), CLI, docs and examples — not the full managed-service backend. A separate `e2b-dev/infra` repository provides Terraform-based self-hosting for AWS, GCP, Azure, and generic Linux machines per the main README.
Sources:
- https://github.com/e2b-dev/E2B — "Open-source, secure environment with real-world tools for enterprise-grade agents." (License: Apache-2.0)

### maturity
(prose-only) Founded 2023; ~13k GitHub stars on the main SDK repo; $43.8M total raised including a $21M Series A led by Insight Partners (Jul 2025) with participation from Decibel, Sunflower Capital, Kaya, and angels including Docker's former CEO. Marketing materials claim adoption by "94% of Fortune 100 companies" (unverified, vendor claim).
Sources:
- https://e2b.dev/blog/series-a — Series A announcement (third-party search summary; not independently re-fetched verbatim)
- https://e2b.dev/ — "94% of Fortune 100 companies" (vendor marketing claim)

## B. Threat protection
### host_fs_damage
host_fs_damage: Yes — each sandbox is its own Firecracker microVM with its own kernel and filesystem, structurally isolated from the E2B host and from other tenants; there is no host filesystem exposed to the agent to damage (execution is remote, not on the developer's machine at all).
Sources:
- https://e2b.mintlify.app/docs/filesystem.md — "Each E2B Sandbox has its own isolated filesystem"
- https://e2b.dev/ — Firecracker microVM basis (see built_on)

### credential_theft
credential_theft: Partial — no host secrets are exposed by default (nothing is auto-forwarded from the developer's machine; only what the developer explicitly writes/passes into the sandbox, e.g. API keys as env vars, or git credentials via the git integration, is present). Git credentials are stripped from the remote URL after clone by default, but an explicit "dangerously" opt-in stores plaintext credentials on disk inside the sandbox for reuse — the docs flag this itself as risky. No SSH-agent-style credential mediation is documented for git; only inline username/token or an explicit credential-helper file.
Sources:
- https://e2b.mintlify.app/docs/sandbox/git-integration.md — "By default, credentials are stripped from the remote URL after cloning." / "dangerouslyAuthenticate()... Stores credentials on disk inside the sandbox."

### data_exfiltration
data_exfiltration: Partial — network egress is controllable (see axis C network block) but the DEFAULT posture is fully open outbound internet access; exfiltration protection exists only if the developer explicitly configures `allowInternetAccess`/allow-deny lists per sandbox. Domain-based filtering only inspects HTTP Host header (port 80) and TLS SNI (port 443); other ports fall back to CIDR-only filtering, and the docs explicitly warn blocked connections can appear to succeed at the TCP level from inside the sandbox (silent-fail risk for anyone relying on the client-observed result).
Sources:
- https://e2b.mintlify.app/docs/network/internet-access.md — "Every sandbox has outbound access to the internet by default." / "blocked connections may appear successful from inside the sandbox"

### malicious_execution
malicious_execution: Yes — the entire premise of the product is running untrusted/hallucinated AI-generated code with the blast radius contained to a disposable, isolated microVM; sandboxes are ephemeral (killed or paused per lifecycle policy) and each is a separate Firecracker instance, so a compromised sandbox doesn't naturally reach other tenants or the host.
Sources:
- https://e2b.dev/ — "Each sandbox is powered by Firecracker, a microVM made to run untrusted workflows."

### escape_resistance
escape_resistance: Yes — isolation boundary is a hardware-virtualized microVM (KVM-based Firecracker), stronger than a shared-kernel container: each sandbox has its own guest kernel, and Firecracker's minimal device model (no unnecessary emulated devices) reduces attack surface versus a general-purpose VM/hypervisor. E2B's own docs do not publish a syscall-filter/seccomp profile or a security-audit report; a third-party paper reports Firecracker's 2026 first Escape-class CVEs (CVE-2026-5747 virtio-pci OOB write, CVE-2026-1386 jailer symlink host-write) and claims E2B applies a 55-syscall seccomp allowlist under Firecracker's mode-2 filter — both are third-party, not confirmed against E2B's own docs, and noted here as such.
Sources:
- https://e2b.dev/ — "Each sandbox is powered by Firecracker, a microVM made to run untrusted workflows."
- (third-party, unverified against E2B docs) arxiv.org/pdf/2606.08433 — "2026 produced Firecracker's first two Escape-class CVEs (CVE-2026-5747... CVE-2026-1386...)"; "E2B inherits a tight seccomp ceiling with 55 syscalls allowlisted under Firecracker's mode-2 filter"

### resource_abuse
resource_abuse: Yes — CPU/memory/disk are capped per plan and per-sandbox, and are tunable per template. Hobby tier caps at 8 vCPU/8GB RAM/10GB disk with 1-hour max continuous runtime and 20 concurrent sandboxes; Pro tier allows more (8+ vCPU/8+ GB/20+ GB by request) with 24-hour runtime and up to 1,100 concurrent sandboxes. Default sandbox size is 2 vCPU/512MiB RAM unless customized at template-build time via `cpuCount`/`memoryMB`.
Sources:
- https://e2b.mintlify.app/docs/billing.md — "Hobby Plan:... 8 vCPUs max, 8GB memory, 10GB disk, 1-hour continuous runtime, 20 concurrent sandboxes"; "Pro Plan:... $150/month..."
- https://e2b.mintlify.app/docs/template/base-image.md — "2 vCPU and 512 MiB of RAM" default

## C. Feature set & granularity

### network_default_posture
network_default_posture: Open-by-default — an unconfigured sandbox has full outbound internet access; restriction is opt-in per sandbox via `allowInternetAccess`/allow-deny lists at creation or via a live `updateNetwork` call.
Sources:
- https://e2b.mintlify.app/docs/network/internet-access.md — "Every sandbox has outbound access to the internet by default."

### egress_allowlist
egress_allowlist: Yes — ladder covers binary on/off (`allowInternetAccess: false` blocks all outbound), then IP/CIDR (`8.8.8.0/24`), then domain names including wildcard subdomains (`*.mydomain.com`), combinable in one rule set. Deny rules exist (`denyOut`) but allow rules always take precedence over deny on conflict — i.e. no fully general precedence/priority system, allow simply wins. No documented port-range scoping or path/method-level rule syntax at the allow/deny-list layer (path-level exists only via the separate, beta per-host "transform" rules — see http_path_rules below).
Sources:
- https://e2b.mintlify.app/docs/network/internet-access.md — "'Deny all traffic except specific IPs' by setting denyOut: [allTraffic] with allowOut lists"; "Allow rules always take precedence" over deny rules

### dns_level_blocking
dns_level_blocking: Unknown — docs describe the egress mechanism as accepting the TCP connection first and evaluating Host header/SNI/CIDR after, which is a proxy/L4-L7 filtering model, not necessarily a DNS-resolution-time block; the docs do not state whether unlisted domains fail to resolve at the DNS layer at all, only that "blocked connections may appear successful from inside the sandbox" before being cut off at the filtering layer. This actually suggests the opposite of DNS-level blocking (resolution + connect succeed, then traffic is filtered), but nothing explicit rules DNS blocking in or out, so kept Unknown rather than inferred No.
Sources:
- https://e2b.mintlify.app/docs/network/internet-access.md — "blocked connections may appear successful from inside the sandbox" because "the firewall accepts connections before filtering"

### tls_mitm_inspection
tls_mitm_inspection: Partial — filtering reads TLS SNI to make allow/deny decisions on port 443 traffic, but the docs describe SNI-based routing/matching, not full TLS termination/MITM re-encryption for deep L7 payload inspection. This gives host-level control over HTTPS destinations without decrypting request bodies.
Sources:
- https://e2b.mintlify.app/docs/network/internet-access.md — domain filtering operates on "HTTP (port 80 via Host headers) and TLS (port 443 via SNI)"

### http_path_rules
http_path_rules: Partial — a beta "per-host request transform" feature (`network.rules`) allows registering rules keyed to a host to inject/override HTTP headers on outbound requests, but this is header transformation, not a documented path-prefix/method/regex allow-deny rule system, and registering a rule does not itself grant egress — the host must still separately appear in `allowOut`. No path-level allow/deny or method-gating is documented.
Sources:
- https://e2b.mintlify.app/docs/network/internet-access.md — per-host rules "register under network.rules" for header injection; "does not grant egress on its own" — host "must be explicitly listed in allowOut"

### proto_coverage
proto_coverage: Partial — domain-based rules only cover HTTP (port 80, Host header) and TLS (port 443, SNI); "traffic on other ports uses CIDR-based filtering only" and "UDP-based protocols like QUIC/HTTP3 are not supported" for domain filtering. DNS itself is auto-permitted to `8.8.8.8` when domain filtering is active (not user-controllable per docs found). No documented control/visibility for ICMP. A separate Shadowsocks-based proxy-tunneling feature exists to route sandbox egress (SOCKS5 for selected traffic, or transparent iptables-redirect for all TCP traffic) through a customer-run proxy VM for dedicated-IP use cases — that's IP-masking infrastructure, not protocol-level policy. No documented extensibility model for adding new L7 protocols to the rule engine.
Sources:
- https://e2b.mintlify.app/docs/network/internet-access.md — "Traffic on other ports uses CIDR-based filtering only. UDP-based protocols like QUIC/HTTP3 are not supported." / DNS auto-allows `8.8.8.8`
- https://e2b.mintlify.app/docs/network/ip-tunneling.md — Shadowsocks-based proxy, "Local proxy" (SOCKS5, port 1080, selected traffic) vs "Transparent proxy" (all TCP via iptables redirect)

### live_rule_reload
live_rule_reload: Yes — `updateNetwork()`/`update_network()` (SDK) and the `PATCH` sandbox-network API apply new egress rules to a running sandbox without restart, returning HTTP 204 on success. Caveat: the call **replaces** the entire rule set rather than merging — a partial update must resend the full desired configuration.
Sources:
- https://e2b.mintlify.app/docs/api-reference/sandboxes/update-sandbox-network.md — "Replaces the current egress rules with the provided configuration. Omitting field clears it."

### firewall_escape_hatch
firewall_escape_hatch: Partial — there is a live, per-sandbox toggle (`allow_internet_access: true/false` via the same runtime network-update API) that can fully open or fully close a sandbox's egress instantly without recreating it, functioning as an immediate break-glass/lockdown switch. However, no documented TIMED bypass with automatic re-enforcement (no auto-expiring "open for N minutes then re-lock" primitive) — reverting to a restricted policy after an open period requires another explicit API call.
Sources:
- https://e2b.mintlify.app/docs/api-reference/sandboxes/update-sandbox-network.md — "allow_internet_access (boolean): ... When set to false, it behaves the same as specifying denyOut to 0.0.0.0/0"

### enforcement_plane
enforcement_plane: Partial/Unknown — enforcement happens outside the sandbox's own guest kernel (the guest cannot simply disable it by editing in-VM state, since sandboxed processes don't control the hypervisor/network edge), consistent with a proxy/edge-level firewall in front of the microVM's egress path. E2B's docs do not name the specific dataplane technology (no confirmed eBPF/nftables/proxy implementation detail found in official docs); the "accept-then-filter" behavior described suggests a stateful inline proxy rather than a pure L3/L4 packet filter. Whether traffic is logged at the enforcement layer is not documented (see network_audit below).
Sources:
- https://e2b.mintlify.app/docs/network/internet-access.md — "the firewall accepts connections before filtering" (implies a stateful inline enforcement point, not confirmed as eBPF/proxy/etc. by name)

### fail_closed
fail_closed: Unknown — no documentation found describing what happens to a sandbox's network policy if E2B's control plane / orchestrator has an outage while the sandbox keeps running. Since enforcement appears to sit outside the guest VM (see enforcement_plane), a supervisor outage's effect on already-applied rules is not addressed either way in official docs.

### network_audit
network_audit: Unknown — no per-request egress audit log (e.g. per-connection allow/deny record with destination, timestamp, verdict) is documented. E2B does document sandbox-level lifecycle event logging (created/paused/resumed/updated/killed with metadata, retained 7 days) and periodic resource metrics (CPU/mem/disk, every 5s), and Enterprise-only OTel export of those same lifecycle/resource signals — but none of these are network-request-level audit trails. Docs silence, not a confirmed absence, so kept Unknown.
Sources:
- https://e2b.mintlify.app/docs/sandbox/lifecycle-events-api.md — event types are `sandbox.lifecycle.{created,updated,paused,resumed,killed}`; "no mention" of per-request network activity
- https://e2b.mintlify.app/docs/sandbox/otel-telemetry-export.md — "Metrics are emitted under the e2b.* namespace" for "sandbox resource usage, such as CPU and memory usage"; logs cover "sandbox lifecycle events"

### workspace_modes
workspace_modes: No — there is no live host bind-mount mode; E2B sandboxes are remote VMs with their own isolated filesystem, populated via explicit `files.write`/upload, `git.clone`, or template build-time `COPY`. State is preserved across pause/resume (filesystem + memory snapshot) and reusable via snapshots, but this is remote-VM persistence, not a "live bind mount of a local directory" workspace mode as offered by local-first tools.
Sources:
- https://e2b.mintlify.app/docs/filesystem.md — SDK offers read/write, upload, download; no bind-mount option documented
- https://e2b.mintlify.app/docs/sandbox/persistence.md — pause preserves "the sandbox's filesystem and memory state"

### observability
observability: Partial — passive visibility exists via periodic resource metrics (CPU/mem/disk, 5s interval, via SDK `getMetrics()`/CLI `e2b sandbox metrics`) and a lifecycle event API/webhooks (7-day retention), plus Enterprise-only OTel export of those metrics/logs to an external collector. No dashboard/UI product is documented in these pages, and no network-level or process-level (syscall, file-access) observability is documented — coverage is resource + lifecycle only.
Sources:
- https://e2b.mintlify.app/docs/sandbox/metrics.md — "The metrics are collected every 5 seconds." CPU/mem/disk only; "documentation does not mention network metrics"
- https://e2b.mintlify.app/docs/sandbox/otel-telemetry-export.md — Enterprise-only; "This feature is available for Enterprise customers only."

### supervision
supervision: Partial — the control plane can act on any sandbox by ID (pause/kill/update-network) via API, so containment primitives exist, but E2B does not document a built-in behavioral-supervisor that observes agent activity and autonomously decides to intervene; the "observe and act" loop (watch metrics/lifecycle events, decide, call kill/pause/update-network) must be built by the developer on top of E2B's primitives. This is oversight infrastructure, not an active supervisor product.
Sources:
- https://e2b.mintlify.app/docs/sandbox/lifecycle-events-api.md — event retrieval/filtering "via REST endpoints" for external consumption
- https://e2b.mintlify.app/docs/api-reference/sandboxes/update-sandbox-network.md — network can be updated/locked down programmatically by ID at any time

### fleet_mgmt
fleet_mgmt: Yes — sandboxes are addressable by ID through the API/SDK/CLI, team-wide event aggregation is available via the lifecycle events API, and concurrency limits are tracked per plan (20 concurrent on Hobby, up to 1,100 on Pro with add-ons), implying a registry-like accounting of concurrently running sandboxes per team/org.
Sources:
- https://e2b.mintlify.app/docs/billing.md — "up to 1,100 concurrent sandboxes (with add-ons at $500/month)"
- https://e2b.mintlify.app/docs/sandbox/lifecycle-events-api.md — "Team-wide event aggregation across all sandboxes"

### snapshots_persistence
snapshots_persistence: Yes — pause/resume preserves full filesystem + memory state indefinitely ("Paused sandboxes are kept indefinitely; there is no automatic deletion or time-to-live limit"); a separate Snapshots feature creates reusable, one-to-many checkpoints (spawn many new sandboxes from one snapshot) without stopping the original sandbox; a lighter filesystem-only snapshot mode (`keepMemory: false`) trades instant-resume for a smaller/cheaper snapshot that cold-boots on resume. Auto-resume can wake a paused sandbox automatically on the next SDK/HTTP activity.
Sources:
- https://e2b.mintlify.app/docs/sandbox/persistence.md — "Paused sandboxes are kept indefinitely; there is no automatic deletion or time-to-live limit."
- https://e2b.mintlify.app/docs/sandbox/snapshots.md — "snapshot can spawn many new sandboxes" (one-to-many) vs pause/resume's one-to-one

## D. Setup
### setup
setup: Easy — sign up at e2b.dev (free, $100 credit), grab an API key, `npm i @e2b/code-interpreter dotenv` (or `pip install e2b-code-interpreter python-dotenv`), write a few lines to create a sandbox and run code, execute the script. No Docker/Kubernetes prerequisite on the developer's machine since execution is remote; only a language runtime + internet access is needed. Docs estimate isn't explicit but the described flow is a handful of commands.
Sources:
- https://e2b.mintlify.app/docs/quickstart.md — install commands `npm i @e2b/code-interpreter dotenv` / `pip install e2b-code-interpreter python-dotenv`; API key via `.env`

## E. Daily use
### daily_use
daily_use: Moderate — day-to-day interaction is entirely through SDK calls (create sandbox, run commands/files, pause/kill) rather than an attach/detach terminal-first workflow; because sandboxes are billed per-second and Hobby-tier sandboxes cap at 1 hour continuous runtime (24h on Pro), longer-running work requires actively managing pause/resume or auto-resume/timeout settings to avoid unwanted billing or unexpected termination. SSH access is possible but requires installing and wiring `websocat` as a WebSocket-to-TCP bridge on both ends rather than a native `ssh` connection — added friction for anyone wanting a plain terminal feel.
Sources:
- https://e2b.mintlify.app/docs/billing.md — "Enable auto-pause... use pause() and kill() functions strategically"
- https://e2b.mintlify.app/docs/sandbox/ssh-access.md — requires `websocat -b --exit-on-eof ws-l:0.0.0.0:8081 tcp:127.0.0.1:22` on the sandbox side and a `ProxyCommand`-based websocat bridge locally

## F. Configuration
### config_depth
config_depth: Deep for image/build, shallow for network policy as versionable config. Templates are defined programmatically (Python/JS builder API or Dockerfile-derived) covering base image selection (Debian-family only), package installs (pip/npm/bun/apt), env vars (build-time only), working directory, file copy/remove/symlink, arbitrary run commands, git clone, user, and a start command with a readiness wait condition — all buildable/versionable via the E2B CLI (`e2b template build`) and tagged (`template/tags`). Network egress rules, however, are set via SDK/API calls at sandbox-creation or update time (not a declarative project config file format that was found in docs), so network policy isn't captured in the same versionable template artifact as the image/build config.
Sources:
- https://e2b.mintlify.app/docs/template/defining-template.md — `pip_install(...)`, `apt_install(...)`, `set_envs(...)`, `set_start_cmd(...)`, `run_cmd(...)`, `git_clone`, `set_user`
- https://e2b.mintlify.app/docs/template/base-image.md — "E2B currently supports only Debian-based images."; Dockerfile import via `fromDockerfile()`, "Multi-stage Dockerfiles are not supported."

### policy_model
policy_model (spectrum): Moderate — network policy is genuinely adjustable per sandbox and live-updatable (open by default, lockable to allowlist, or fully closed, changeable at runtime without recreating the sandbox), and lifecycle policy (kill vs pause-and-preserve-state on timeout, auto-resume) is also configurable per sandbox. But there's no single declarative "security profile" file with sane secure-by-default posture that a project checks in — the secure default is "open," and tightening is an opt-in imperative API call per sandbox, which is more DIY-scriptable-policy than a curated policy system with built-in secure defaults.
Sources:
- https://e2b.mintlify.app/docs/network/internet-access.md — default "outbound access to the internet by default"
- https://e2b.mintlify.app/docs/sandbox/auto-resume.md — `onTimeout: "kill"|"pause"`, `autoResume: true/false` per-sandbox lifecycle config

## G. DX — host↔sandbox integration
### bind_mount_sharing
bind_mount_sharing: No — no live host-directory bind mount is documented; changes are one-way, mediated through explicit SDK file upload/download, `files.write`/`files.read`, or `git.clone`/`git.push`, consistent with the remote-VM execution model (there is no shared host filesystem to bind into).
Sources:
- https://e2b.mintlify.app/docs/filesystem.md — filesystem operations documented are read/write/upload/download; no bind-mount option

### cred_forwarding
cred_forwarding: Partial — no ssh-agent/gpg-agent-style live credential mediation is documented. Git HTTP(S) credentials can be passed inline per-command (username/token) or, via an explicitly-named `dangerouslyAuthenticate()`/`dangerouslyStoreCredentials` opt-in, written to disk inside the sandbox (the docs' own naming signals this is discouraged). No SSH key forwarding or GPG signing forwarding is documented anywhere in the fetched docs set.
Sources:
- https://e2b.mintlify.app/docs/sandbox/git-integration.md — "store them in the git credential helper inside the sandbox using dangerouslyAuthenticate()... Stores credentials on disk inside the sandbox."

### browser_auth
browser_auth: Unknown — no documentation found describing a mechanism for a process running inside the sandbox to trigger a browser-open on the developer's local machine (e.g., for an OAuth/device-code login flow) with the callback proxied back into the sandbox. The only browser-open flow documented is for authenticating the E2B CLI itself (`e2b auth login` opens a local browser against E2B's own account system), which is a different thing from forwarding an in-sandbox tool's auth flow to the host. Given the remote execution model, this class of feature is structurally less natural than for a local-container tool, but docs don't explicitly rule it out either way.
Sources:
- (third-party search summary, not independently re-fetched) e2b.dev/docs/cli/auth — "`auth login` command opens your default browser and prompts you to authenticate with your E2B account"

### shared_dirs
shared_dirs: Partial — beyond the per-sandbox filesystem, a separate "Volumes" feature (currently private beta) provides persistent storage mountable into multiple sandboxes at a chosen mount path, usable as a shared or per-sandbox-dedicated data directory. Not a host-shared directory — this is cloud-side shared storage between sandboxes, still remote.
Sources:
- https://e2b.mintlify.app/docs/volumes.md — "Persistent storage that exists independently of sandboxes and can be mounted across multiple sandboxes." / "Volumes are currently in private beta."

### git_worktrees
git_worktrees: Unknown — the documented git integration covers clone (default/branch/shallow), push, and credential handling, but git worktree support specifically is not mentioned in the fetched git-integration docs.
Sources:
- https://e2b.mintlify.app/docs/sandbox/git-integration.md — clone/push/credential methods documented; no mention of `git worktree`

### nested_containers
nested_containers: Yes — Docker (and Docker Compose) can be installed and run inside an E2B sandbox, since each sandbox is a full Firecracker microVM with its own kernel (not a shared-kernel container needing a docker-socket passthrough). Official example templates exist for both plain Docker and Docker Compose. Minimum recommended sizing is 2 CPU / 2GB RAM; under-provisioned sandboxes can run out of memory running Docker workloads.
Sources:
- https://e2b.mintlify.app/docs/template/examples/docker.md — "We recommend at least 2 CPUs and 2 GB of RAM for running Docker containers. With lower RAM, your sandbox might run out of memory."

### harness_agnostic
harness_agnostic: Yes — E2B is a general-purpose code-execution sandbox consumed via SDK/API, not tied to any specific coding-agent CLI or vendor; marketing/docs advertise compatibility with "any LLM provider" (OpenAI, Anthropic, Mistral, Llama cited as examples) and the SDK just runs arbitrary code/commands the caller supplies.
Sources:
- https://e2b.dev/ — "Support for any LLM provider"; integration examples with OpenAI, Anthropic, Mistral, Llama

## H. Performance
### performance
performance: Lightweight startup, cited by the vendor. "Sub-200ms startup in same-region deployments" per marketing page (vendor-claimed, not independently benchmarked in these docs). No independently-sourced benchmark for disk footprint, RAM overhead, or bind-mount-style IO throughput was found (not applicable in the same way since there's no host bind mount) — cold-start-from-snapshot vs pause/resume-with-memory latency numbers were not found in the fetched pages either.
Sources:
- https://e2b.dev/ — "Sub-200ms startup" (vendor marketing claim, same-region)

## I. Feasibility
### feasibility
feasibility: Adoptable today for cloud-first / hosted-agent use cases — no local Docker/Firecracker/KVM prerequisite for the developer, since execution is entirely remote; only network access + API key needed, works from macOS/Linux/Windows equally since nothing runs locally. Platform lock-in risk is real for the managed-service path (proprietary control-plane API, template format, network-rule API) though the SDK/client side is Apache-2.0 and a self-host path (`e2b-dev/infra`, Terraform-based) exists for AWS/GCP/Azure/generic Linux, mitigating full vendor lock-in for organizations willing to operate that infra themselves. BYOC (customer-VPC deployment) is Enterprise-only, gating the "keep everything in my own cloud" path behind a sales conversation.
Sources:
- https://github.com/e2b-dev/E2B — self-hosting guide reference to `e2b-dev/infra`, Terraform for AWS/GCP/Azure/generic Linux
- https://e2b.mintlify.app/docs/byoc.md — "This feature is available for Enterprise customers only."

## J. Price
### pricing
(prose-only) Usage-based, billed per-second of running-sandbox compute. Hobby (free) tier: $100 one-time signup credit, 8 vCPU/8GB RAM/10GB disk max, 1-hour max continuous sandbox runtime, 20 concurrent sandboxes, 1 sandbox-creation/sec rate limit. Pro tier: $150/month base (no included credits), same-or-higher resource ceilings on request, 24-hour continuous runtime, up to 1,100 concurrent sandboxes (extra concurrency add-on $500/month), 5 sandbox-creations/sec. Enterprise: custom pricing, includes BYOC and OTel export. Cost-control levers: `pause()`/`kill()`, auto-pause on timeout, and sizing `cpuCount`/`memoryMB` per template.
Sources:
- https://e2b.mintlify.app/docs/billing.md — "you pay per second for compute resources while your sandbox is running"; "$150/month" Pro base; "New users receive $100 in free credits"

## K. Extensibility
### extensibility
extensibility: Yes — custom templates (own Dockerfile or E2B's builder API) let users define arbitrary base images (Debian-family only), package sets, env vars, lifecycle/start commands, and are versionable/taggable via the CLI (`e2b template build`); templates can extend other templates (`fromTemplate()`); private registries are supported as a base-image source for enterprise package management. No documented plugin/extension-hook system beyond the template/build mechanism and the SDK/API surface itself (e.g., no custom-protocol-plugin model for the network layer — see proto_coverage).
Sources:
- https://e2b.mintlify.app/docs/template/defining-template.md — builder API for base image, packages, env, commands
- https://e2b.mintlify.app/docs/template/base-image.md — "Existing templates can be extended using fromTemplate()."; "E2B currently supports only Debian-based images."

## Unknowns & caveats
- **dns_level_blocking**: docs describe an accept-then-filter model that suggests connections/DNS may succeed before being cut off, but nothing explicitly confirms or denies DNS-resolution-time blocking — kept Unknown per guidelines (torn between No and Unknown → Unknown).
- **fail_closed**: no documentation found on what happens to enforced network policy if E2B's control plane has an outage while sandboxes keep running.
- **network_audit**: no per-request egress audit log documented; only sandbox-lifecycle events and periodic resource metrics are documented, neither of which is a network-request audit trail.
- **enforcement_plane** exact dataplane technology (eBPF/nftables/proxy) not named in official docs; inferred as a stateful inline proxy from the "accept before filter" description only.
- **git_worktrees**: not mentioned in the fetched git-integration docs; could not confirm presence or absence.
- **browser_auth**: no documentation found on host-browser-open proxying for in-sandbox tool OAuth flows (distinct from E2B's own CLI login flow, which was found). Structurally atypical for a remote-execution product but not explicitly ruled out.
- **maturity/Series A source** (e2b.dev/blog/series-a) and **CLI auth page** (e2b.dev/docs/cli/auth) were characterized via WebSearch result summaries rather than a direct WebFetch of the page — flagged as slightly lower-confidence than the directly-fetched Mintlify pages, though both are official E2B domains.
- **escape_resistance** CVE and seccomp-syscall-count claims are from a third-party arXiv comparative study, not from E2B's own docs — explicitly marked third-party and unverified against official E2B sources.
- No blocked URLs (NXDOMAIN/connection-refused) encountered during this research session; all fetches to e2b.dev / e2b.mintlify.app / github.com succeeded.

# Modal Sandboxes
category: cloud
Serverless cloud sandbox platform for running untrusted/agent-generated code | built on gVisor (default) or full VM (beta, "VM Sandboxes") | proprietary SaaS, client SDKs open source | GA product, SOC2 Type 2, used by multiple coding-agent vendors
As-of date: 2026-07-18

## A. Identity

### built_on (prose-only)
Two runtime modes. Default: gVisor, "the sandboxing technology developed at Google and used in their Google Cloud Run and Google Kubernetes Engine cloud services" — gVisor intercepts guest syscalls via a virtual kernel, preventing direct host access. Beta alternative: "VM Sandboxes" run "on top of a full virtual machine rather than on top of gVisor," giving "each Sandbox a real Linux kernel" — enables systemd, eBPF, cgroups, Docker/nested-container workloads that gVisor's syscall interception blocks. The underlying hypervisor for VM Sandboxes (Firecracker/KVM/other) is not named in docs. Control plane is Modal's serverless orchestration layer (Apps, scheduler, resource allocator) — architecture internals not published.
Sources:
- https://modal.com/docs/guide/security — "Compute jobs at Modal are containerized and virtualized using gVisor, the sandboxing technology developed at Google..."
- https://modal.com/docs/guide/vm-sandboxes — "Sandboxes can be run on top of a full virtual machine rather than on top of gVisor... a real Linux kernel"

### execution_locality
execution_locality: Remote — Sandboxes execute entirely on Modal's cloud infrastructure; the local machine only runs the Python/JS/Go client SDK or CLI that issues API calls. No local execution mode exists. No self-hosting option is documented anywhere (pricing page lists Starter/Team/Enterprise cloud plans only, no on-prem/self-host tier). Project code and any secrets passed to a Sandbox necessarily leave the developer's machine and run on Modal-operated infrastructure (with region selection available on Team/Enterprise plans to constrain which cloud region).
Sources:
- https://modal.com/pricing — plan tiers are all hosted/billed cloud usage; no self-host tier listed
- https://modal.com/docs/guide/region-selection — "Cloud region selection is available on the Team plan... and the Enterprise plan"

### open_source (prose-only)
Modal's client libraries (Python/JS/Go SDKs) are open source, but the Modal platform itself (scheduler, control plane, gVisor/VM orchestration backend) is closed-source proprietary SaaS. No self-hosting is offered.

### maturity (prose-only)
Sandboxes are GA ("Modal Sandboxes are generally available"). Modal has completed a SOC 2 Type 2 audit and offers HIPAA BAAs on Enterprise. Multiple third-party coding-agent products are documented/blogged as using Modal Sandboxes (Cursor, GitHub Copilot Workspace, LangGraph, Claude Agent SDK, OpenCode example). JS/Go SDKs are newer than the Python SDK (alpha-era version numbers, e.g. JS/Go SDK ≥0.7.6 required for some Sandbox filesystem features vs Python ≥1.4.0).
Sources:
- https://modal.com/blog/sandbox-launch — "Modal Sandboxes are generally available"
- https://modal.com/docs/guide/security — SOC 2 Type 2, HIPAA BAA details

## B. Threat protection

### host_fs_damage
host_fs_damage: Yes — gVisor's virtual-kernel syscall interception "prevents these processes from having any ability to affect the host outside of the permissions gVisor specifically gives them"; the sandbox filesystem is a container/VM image, not the host's. No caveats found for the default gVisor mode; VM Sandboxes trade gVisor's syscall interception for a full guest kernel inside a VM boundary instead (isolation moves to the hypervisor layer rather than the syscall layer).
Sources:
- https://modal.com/docs/guide/security — "gVisor intercepts syscalls from guest processes, providing a virtual kernel to the sandboxed container, which prevents these processes from having any ability to affect the host"

### credential_theft
credential_theft: Partial — Secrets are injected as plain environment variables into the sandbox (`Secret.from_dict()`, `.from_dotenv()`, dashboard-managed secrets), meaning any code running inside the sandbox can read them directly from `os.environ`; there is no ssh-agent or gpg-agent forwarding mechanism documented. Modal does document an advanced pattern — a reverse-proxy sidecar container sharing a private network with the sandbox, where "the real credential liv[es] in a Modal Secret mounted on the sidecar only" and the sandbox never sees it directly — but this is a build-it-yourself architecture pattern (via an example/recipe repo), not a first-class mediated-credential feature enabled by a flag.
Sources:
- https://modal.com/docs/guide/secrets — Secrets become accessible via `os.environ`; no ssh-agent/gpg-agent forwarding mentioned
- https://github.com/modal-labs/credential-injection — sidecar pattern: "network isolation is the auth boundary, with the real credential living in a Modal Secret mounted on the sidecar only" (third-party/example repo, not core docs)

### data_exfiltration
data_exfiltration: Partial — The mechanism to restrict egress exists (`block_network`, `outbound_cidr_allowlist`, `outbound_domain_allowlist`) but is opt-in per Sandbox and OFF by default: "Sandboxes can make outbound connections to any public IP address" unless the developer explicitly configures a restriction at creation time. A default/unconfigured Sandbox has unrestricted internet egress, so exfiltration protection is only as strong as each call site's configuration.
Sources:
- https://modal.com/docs/guide/sandbox-networking — "By default... Sandboxes can make outbound connections to any public IP address"

### malicious_execution
malicious_execution: Yes — gVisor containment limits blast radius to the sandbox; resource request/limit tuples (cpu, memory) and mandatory `timeout` (default 300s, max 24h) cap runaway workloads; `idle_timeout` reclaims abandoned sandboxes. A separate "Restricted Functions" primitive (`restrict_modal_access=True`, `single_use_containers=True`) adds a stateless, single-use execution mode specifically pitched for untrusted/LLM-generated code, denying access to other Modal resources (Queues, Dicts, other Functions) and raising `AuthError` on violation.
Sources:
- https://modal.com/docs/guide/restricted-access — "restrict_modal_access=True" prevents functions from accessing "Modal resources (Queues, Dicts, etc.)" or calling "other Functions"
- https://modal.com/docs/reference/modal.Sandbox — `timeout` default 300, `block_network` default False

### escape_resistance
escape_resistance: prose-heavy, no single verdict. Default isolation is gVisor: a user-space kernel that intercepts syscalls, which Modal's own docs frame as giving "stronger isolation than most other container runtimes" (i.e., stronger than a shared-kernel runc container, but gVisor is not hardware-virtualized and its syscall-emulation surface is a known general escape-research target in the wider industry — Modal's docs do not discuss CVE history or a threat model for gVisor escapes). Modal also runs "automated synthetic monitoring test applications that continuously check for network and application isolation." For workloads needing a stronger boundary, Modal now offers beta VM Sandboxes with "a real Linux kernel" inside a full VM — architecturally a harder boundary than syscall interception, at the cost of losing memory snapshots and GPU support. Docs do not name the hypervisor or provide any independent escape-resistance evaluation.
Sources:
- https://modal.com/docs/guide/security — "gVisor has custom logic to prevent Sandboxes from making malicious system calls, giving you stronger isolation than most other container runtimes"
- https://modal.com/docs/guide/vm-sandboxes — VM Sandboxes give "each Sandbox a real Linux kernel"

### resource_abuse
resource_abuse: Yes — `cpu`/`memory` accept `(request, limit)` tuples to cap consumption; docs explicitly frame this as protection "when an AI agent controls what runs inside the Sandbox, as it prevents misbehaving or adversarial workloads from consuming unbounded resources." GPUs are preemptible (workloads "must handle interruptions gracefully" rather than being hard-capped). `timeout` (max 24h) and OOM termination bound worst-case duration/memory.
Sources:
- https://modal.com/docs/guide/sandbox-resources — resource limit tuples "prevents misbehaving or adversarial workloads from consuming unbounded resources"

## C. Feature set & granularity

### network_default_posture
network_default_posture: Open-by-default (outbound) — "Sandboxes can make outbound connections to any public IP address" unless restricted. Inbound is deny-by-default: "a default Sandbox has no ability to accept incoming network connections." So the secure-by-default claim applies to inbound and to Modal-internal-resource access, not to internet egress.
Sources:
- https://modal.com/docs/guide/sandbox-networking — "By default, they have no ability to accept incoming network connections... Sandboxes can make outbound connections to any public IP address"

### egress_allowlist
egress_allowlist: Partial — Three configurable levels at Sandbox-creation time: full block (`block_network=True`, drops all outbound traffic), CIDR allowlist (`outbound_cidr_allowlist`, any protocol, IP-range granularity), and domain allowlist (`outbound_domain_allowlist`, Beta, TLS/port-443 only, supports `*.` subdomain wildcards). CIDR and domain allowlists combine additively. Granularity stops at domain/CIDR/port-443 — no documented path, method, or arbitrary-port scoping within the domain allowlist, and domain allowlisting is explicitly Beta. A runtime policy-replace API exists but is Alpha (see live_rule_reload).
Sources:
- https://modal.com/docs/guide/sandbox-networking — "outbound_domain_allowlist: Only allows TLS traffic (port 443) to the listed domain names" (Beta); "outbound_cidr_allowlist: Only allows traffic to the listed CIDR ranges (any protocol)"

### dns_level_blocking
dns_level_blocking: Unknown — Docs do not state whether DNS resolution itself fails for non-allowlisted domains or whether the domain name is resolved normally and only the subsequent TLS connection is blocked/matched. The described mechanism ("TLS (port 443) connections are allowed only to the listed domains") reads as connection/SNI-layer enforcement rather than a resolver-layer block, but this is not confirmed either way in the docs.
Sources:
- https://modal.com/docs/guide/sandbox-networking — no DNS-specific language found on this page

### tls_mitm_inspection
tls_mitm_inspection: No — No mention anywhere in the networking or security docs of a Modal-provided root CA, certificate injection, or TLS interception/decryption for domain filtering. The domain allowlist description ("TLS (port 443) connections are allowed only to the listed domains") is consistent with SNI/connection-based matching, not certificate-inspecting MITM; nothing in docs indicates traffic is decrypted for L7 rule evaluation.
Sources:
- https://modal.com/docs/guide/sandbox-networking — domain allowlist described purely in terms of allowed TLS connections to a domain, no CA/cert-injection language

### http_path_rules
http_path_rules: No — Egress control is domain- and CIDR-scoped only; no path, method, or regex-based HTTP rule is documented for outbound traffic.
Sources:
- https://modal.com/docs/guide/sandbox-networking — only `outbound_cidr_allowlist` / `outbound_domain_allowlist` / `block_network` documented, no path/method fields

### proto_coverage
proto_coverage: Partial — CIDR allowlist covers "any protocol" at the IP layer (implying TCP/UDP/ICMP pass if the destination IP matches), so raw-protocol coverage exists via CIDR rules. Domain allowlist is TLS/port-443 only; explicitly, "non-TLS traffic (HTTP, raw TCP, UDP) to IPs that are not on a CIDR allowlist is blocked" when a domain allowlist is active. DNS and ICMP are not separately named/discussed; QUIC/HTTP3 is not mentioned. Inbound protocol support (tunnels: `encrypted_ports`/TLS, `unencrypted_ports`/raw TCP, `h2_ports`/HTTP2) is documented but is an inbound-tunneling feature, not an egress rule dimension. No documented extensibility mechanism for adding new L7 protocols to the egress rule model.
Sources:
- https://modal.com/docs/guide/sandbox-networking — "Non-TLS traffic (HTTP, raw TCP, UDP) to IPs that are not on a CIDR allowlist is blocked"

### live_rule_reload
live_rule_reload: Yes (Alpha) — `_experimental_set_outbound_network_policy()` (Python) / `updateNetworkPolicy()` (JS) / `UpdateNetworkPolicy()` (Go) replaces the outbound policy on a running Sandbox without restart: "The new policy takes effect immediately. Established connections that the new policy no longer permits are terminated." Caveat: each allowlist type must have been set (even if empty) at Sandbox creation to be updatable later, and the feature is explicitly Alpha/experimental.
Sources:
- https://modal.com/docs/guide/sandbox-networking — "_experimental_set_outbound_network_policy()... The new policy takes effect immediately. Established connections that the new policy no longer permits are terminated."

### firewall_escape_hatch
firewall_escape_hatch: No — No timed/auto-expiring bypass is documented. The only way to change network policy on a live sandbox is the Alpha runtime policy-replace API above, which is a manual, persistent change (no automatic re-enforcement after a duration) rather than a break-glass bypass. Network settings are otherwise fixed at Sandbox creation.
Sources:
- https://modal.com/docs/guide/sandbox-networking — only creation-time parameters plus the Alpha runtime-replace API are documented; no bypass/timer concept present

### enforcement_plane
enforcement_plane: Unknown — Docs never state where CIDR/domain egress rules are technically enforced (kernel/eBPF, userspace proxy, hypervisor/VM network boundary, or cloud VPC/security-group infrastructure). gVisor's syscall interception is documented for general isolation, but its role (if any) in enforcing the outbound allowlist is not specified.
Sources:
- https://modal.com/docs/guide/sandbox-networking — no enforcement-architecture language found
- https://modal.com/docs/guide/security — describes gVisor for general isolation, not specifically for network-policy enforcement

### fail_closed
fail_closed: Unknown — No documentation describes what happens to an already-applied network policy if Modal's control plane experiences an incident. Given policy is presented as parameters passed at Sandbox creation (versus a separately-running supervisor process the agent could detect dying), the fail-open/fail-closed behavior on control-plane failure is not addressed either way.
Sources:
- https://modal.com/docs/guide/sandbox-networking — no discussion of control-plane-failure behavior

### network_audit
network_audit: Partial — Blocked domain connections are explicitly logged: "Connections to non-allowlisted domains are securely blocked and logged to the Sandbox's system output stream." This is a stdout log line, not a structured/queryable per-request egress log (allowed + denied) comparable to a dedicated audit feed. Enterprise-plan "Audit Logs" exist but per the Audit Logs page cover platform/account actions, not confirmed to include per-request sandbox network egress detail.
Sources:
- https://modal.com/docs/guide/sandbox-networking — "Connections to non-allowlisted domains are securely blocked and logged to the Sandbox's system output stream"

### workspace_modes
workspace_modes: Partial — No live host bind-mount exists (Sandboxes run remotely; there is no local host filesystem to mount). The Filesystem API (`copy_from_local()`/`copy_to_local()`, `read_text()`/`write_text()`) is explicitly one-way, on-demand copy. Modal Volumes offer a persistence layer that's synced periodically ("background commits that run every few seconds while the Sandbox executes, with a final commit when the Sandbox terminates," plus an explicit `sync` command in Volumes v2) — closer to "ephemeral snapshot" than to a live bind mount, and shared across multiple sandboxes rather than being a live host↔sandbox link.
Sources:
- https://modal.com/docs/guide/sandbox-files — Filesystem API is one-way copy; Volumes background-sync "every few seconds"

### observability
observability: Yes — Per-sandbox dashboards with metrics/logs/status; "Live Profiling" to inspect what's executing in a stuck container in real time; readiness-probe timing surfaced for startup-phase visibility; log export and OpenTelemetry integration (compatible with any OTel-HTTP provider) plus named Datadog/Sentry.io integrations.
Sources:
- https://modal.com/docs/guide/otel-integration — "compatible with any observability provider that supports the OpenTelemetry HTTP APIs"
- https://modal.com/docs/guide/developing-debugging — Live Profiling in the Containers tab for stuck containers

### supervision
supervision: Partial — Modal's control plane manages sandbox lifecycle end-to-end (scheduling, readiness probes, idle/timeout-based termination, OOM termination) and can terminate a sandbox or (Alpha) replace its network policy. However, no documented behavioral/security supervisor watches sandbox activity and dispatches containment actions in response to suspicious behavior specifically — the "interventions" documented are all lifecycle/resource-bound (timeout, idle, OOM, manual policy replace), not detection-triggered.
Sources:
- https://modal.com/docs/guide/sandboxes — lifecycle: "Created → Scheduled → Started → Ready → Finished"; idle/OOM termination documented

### fleet_mgmt
fleet_mgmt: Yes — Sandboxes are grouped under an "App" namespace; each Sandbox can be given a unique `name` (unique among *running* sandboxes in an App), arbitrary key-value `tags` for filtering via `Sandbox.list()`, and retrieved later via `from_id()`/`from_name()`.
Sources:
- https://modal.com/docs/guide/sandboxes — "Sandboxes can also be tagged with arbitrary key-value pairs. These tags can be used to filter results in Sandbox.list"; "Each name must be unique within an App"

### snapshots_persistence
snapshots_persistence: Yes — Filesystem Snapshots (default 30-day TTL, configurable/indefinite) capture full filesystem state differentially and can be used as an Image to launch new Sandboxes (`Sandbox.create(image=snapshot_image)`), production-ready. Memory Snapshots (Experimental) additionally preserve running-process/RAM state so processes resume, but are capped at a non-extendable 7-day TTL and carry "multiple limitations."
Sources:
- https://modal.com/docs/guide/sandbox-snapshots — Filesystem Snapshots "30 days default TTL... configurable"; Memory Snapshots "7 days (non-extendable)... Experimental, multiple limitations"

## D. Setup (spectrum)
setup: Easy — `pip install modal && modal setup` (or npm/go equivalents) handles auth; no local Docker/K8s prerequisite since execution is remote. Requires a Modal account/API token. First Sandbox run is a few lines of SDK code (`modal.App`, `Sandbox.create()`).
Sources:
- https://modal.com/docs/reference/cli/setup — install + `modal setup` flow
- https://modal.com/docs/reference/cli/token — token id/secret configuration, env var alternative (`MODAL_TOKEN_ID`/`MODAL_TOKEN_SECRET`)

## E. Daily use (spectrum)
daily_use: Moderate — Not a single-command wrapper; usage is SDK-code-driven (create App → create Sandbox → `exec()`/filesystem calls → terminate), though `modal shell` gives a quick interactive terminal into a sandbox (including `--experimental-option vm_runtime=1` for VM Sandboxes). Dashboards provide live status without extra tooling. No documented single "attach to running sandbox from a fresh terminal session" shortcut beyond `from_id()`/`from_name()` lookups in code.
Sources:
- https://modal.com/docs/guide/docker-in-sandboxes — `modal shell --experimental-option vm_runtime=1` for quick interactive access

## F. Configuration

### config_depth
config_depth: Deep, but code-based not declarative-file-based — no YAML/TOML project config; all tuning (image, packages via chained Image-build calls, env/secrets, network rules, volume mounts, cpu/memory/gpu, timeout/idle_timeout, readiness probes, custom domains, ports) is expressed as SDK call arguments/method chains in Python/JS/Go, which is versionable as ordinary source code but not a single portable config artifact.
Sources:
- https://modal.com/docs/reference/modal.Sandbox — full parameter list (cpu, memory, gpu, timeout, volumes, secrets, block_network, allowlists, ports, etc.) all passed to `Sandbox.create()`

### policy_model
policy_model: Moderate, spectrum toward policy-driven at the call-site — Nearly every security/resource knob (network posture, resource request/limit, timeout, port exposure) is a parameter on `Sandbox.create()`, so callers can dial security/resources per-sandbox. But there's no single declarative security-profile object reused across sandboxes, no bind-vs-copy toggle (bind mount doesn't exist), and the escape hatch for network policy (Alpha runtime replace) is not a first-class, generally-available control.
Sources:
- https://modal.com/docs/reference/modal.Sandbox — per-call parameters for network/resource/lifecycle policy

## G. DX — host↔sandbox integration

### bind_mount_sharing
bind_mount_sharing: No — Confirmed one-way only; see workspace_modes above. No live two-way bind mount between a local host directory and a running remote Sandbox exists.
Sources:
- https://modal.com/docs/guide/sandbox-files — Filesystem API and Volumes are copy/sync-based, not a live bind mount

### cred_forwarding
cred_forwarding: No — No ssh-agent or gpg-agent forwarding mechanism is documented. The documented pattern for GitHub auth is copying a token value at sandbox-creation time (e.g. `$(gh auth token)` substituted into a Secret), which is credential copying, not agent-socket mediation. A more mediated sidecar-proxy pattern exists but only as an example/recipe repo, not a built-in feature (see credential_theft).
Sources:
- (search synthesis, official-docs-adjacent) modal.com search result on GitHub auth: "you can pass a GitHub personal access token, and if you use the gh CLI, you can use shell command substitution to pass your current auth using $(gh auth token)" — could not re-verify by direct WebFetch of a single doc page; treat as Unknown-strength but consistent with the plain env-var Secrets model documented at https://modal.com/docs/guide/secrets

### browser_auth
browser_auth: No — No mechanism is documented for a sandboxed process to trigger a host-machine browser-open event (OAuth/device-code flow) that gets proxied back to the sandbox. The documented auth patterns are all explicit token/secret passing at creation time; nothing resembling a socket-bridge or browser-proxy is described for interactive OAuth logins run from inside a Sandbox.
Sources:
- https://modal.com/docs/guide/secrets — secrets model is inject-at-creation env vars, no browser-proxy concept present

### shared_dirs
shared_dirs: Yes — Multiple Volumes and CloudBucketMounts can be attached to a single Sandbox via the `volumes` dict parameter, each at its own mount point, independent of the root filesystem; a Volume subdirectory can also be mounted via `with_mount_options`.
Sources:
- https://modal.com/docs/reference/modal.Sandbox — `volumes: dict` — "Mount points for Modal Volumes and CloudBucketMounts"

### git_worktrees
git_worktrees: Unknown — No dedicated git-worktree feature is documented. The docs mention Sandboxes are useful to "check out a git repository and run a command against it" but this is describing generic shell-command execution (`sb.exec("git", "clone", ...)`), not a first-class worktree integration.
Sources:
- https://modal.com/docs/guide/sandboxes — "Check out a git repository and run a command against it, like a test suite" (generic exec use-case, no worktree-specific API found)

### nested_containers
nested_containers: Partial — Docker-in-Sandbox is supported but only on the Beta "VM Sandboxes" runtime (`experimental_options={"vm_runtime": True}`), not on default gVisor sandboxes (gVisor's syscall interception blocks the operations Docker needs). VM Sandboxes carry their own limitations: no GPU support, statically-provisioned memory, no `reload_volumes()`, no Memory Snapshots, 512 GiB max root image.
Sources:
- https://modal.com/docs/guide/docker-in-sandboxes — "VM Sandboxes provide each Sandbox a real Linux kernel, which makes certain workloads (e.g. Docker systems) behave the way they would on a normal Linux host"
- https://modal.com/docs/guide/vm-sandboxes — limitations list (no GPU, static memory, no reload_volumes, no Memory Snapshots, 512 GiB image cap)

### harness_agnostic
harness_agnostic: Yes — Modal Sandboxes are a general-purpose SDK/primitive with no coding-agent vendor tie-in; Modal's own docs/blog resources document usage with multiple different agent products/frameworks (Cursor, GitHub Copilot Workspace, LangGraph, Claude Agent SDK, an OpenCode example server), consistent with a generic execution primitive rather than a vendor-specific wrapper.
Sources:
- https://modal.com/docs/examples/opencode_server — worked example running the OpenCode agent inside a Modal Sandbox
- https://modal.com/resources/best-sandbox-claude-agent-sdk , https://modal.com/resources/best-code-execution-sandbox-github-copilot-workspace — vendor blog pages addressing multiple different agent products (vendor content, marked as such)

## H. Performance (spectrum)
performance: Lightweight-leaning but workload-dependent — Modal states "containers boot in about one second" generally, and a blog post (vendor source, not the docs proper) claims sandbox-creation latency "less than half a second at the median" on their newer scheduling system with "scheduling only tak[ing] tens of milliseconds." The official cold-start guide gives no Sandbox-specific benchmark numbers and notes warm-up time "can range from seconds to minutes" depending on image size and first-invocation work; Modal also "aggressively spins down idle containers," which the docs themselves don't quantify as a tradeoff but third-party benchmarks note increases effective cold-start frequency versus platforms that keep warm pools. No independent (non-Modal) benchmark was fetched/verified in this pass.
Sources:
- https://modal.com/docs/guide/cold-start — "Containers boot in about one second." / warm-up "can range from seconds to minutes"
- https://modal.com/blog/scaling-to-1-million-concurrent-sandboxes-in-seconds — vendor blog, "less than half a second at the median... scheduling only takes tens of milliseconds" (mark as vendor-claimed, not independently verified)

## I. Feasibility (spectrum)
feasibility: Adoptable today, with cloud lock-in — GA product, SOC2 Type 2, usable from any OS since only the client SDK/CLI runs locally (Python mature; JS/Go SDKs explicitly newer/alpha-era per version gating seen in docs, e.g. minimum JS/Go SDK versions required for newer Sandbox filesystem features). No offline mode — every Sandbox operation requires reaching Modal's cloud, and there is no self-host escape valve if that dependency is unacceptable. Solo developers can adopt immediately via the $30/month free-credit Starter plan.
Sources:
- https://modal.com/docs/reference/cli/token — cloud-account-based auth required
- https://modal.com/pricing — Starter plan "$30/month in free compute credits"

## J. Price (prose-only)
Usage-based, per-second billing: "billed by the second based on whichever is higher: your resource request or your actual usage" (`max(request, actual)`), no idle charges. Listed base rates: CPU ≈$0.0000131/physical-core/sec (0.125-core minimum), memory ≈$0.00000222/GiB/sec; GPUs priced per-second per model (e.g., H100 ≈$0.001097/sec, A100-80GB ≈$0.000694/sec, T4 ≈$0.000164/sec). Sandbox+Notebooks workloads carry higher listed rates than standard Functions (CPU ≈$0.00003942/core/sec, memory ≈$0.00000667/GiB/sec). Plans: Starter ($0 base + compute, $30/mo free credit, 100 containers, 3 seats), Team ($250 base + compute, $100/mo credit, 1000 containers, unlimited seats), Enterprise (custom, volume discounts, SSO, HIPAA). No self-hosting/on-prem tier.
Sources:
- https://modal.com/pricing — plan table and per-unit rates as summarized above

## K. Extensibility
extensibility: Yes, moderate — Custom container images via chained `Image` build steps or pulled from external registries; "named Images" let teams publish/reuse pre-built images to avoid rebuild-blocking on Sandbox creation; Filesystem Snapshots double as reusable Images; readiness probes (TCP/exec) and custom entrypoint commands act as lifecycle hooks; OpenTelemetry integration lets any compatible observability backend plug in; multiple official SDKs (Python, JS, Go) rather than a single-language lock-in. No bundle/plugin marketplace or third-party extension-package system comparable to a dedicated harness/bundle model was found.
Sources:
- https://modal.com/docs/guide/sandboxes — named Images: "it's recommended to use Modal's named Images with sandboxes, rather than using inline Image definitions"
- https://modal.com/docs/guide/otel-integration — "compatible with any observability provider that supports the OpenTelemetry HTTP APIs"

## Unknowns & caveats
- dns_level_blocking, tls_mitm_inspection mechanism, enforcement_plane, and fail_closed are all Unknown/inferred-absent from docs silence on architecture — Modal does not publish how/where the CIDR/domain egress rules are technically enforced (no eBPF/proxy/hypervisor detail), unlike some competitors that document this explicitly.
- cred_forwarding evidence for the `$(gh auth token)` pattern came from a WebSearch synthesis rather than a directly-quoted, re-verified doc page; treat with slightly lower confidence than other findings (all other findings in this writeup were pulled from direct WebFetch of the cited official URL).
- No blocked URLs — all WebFetch calls to modal.com succeeded in this research pass.
- Performance numbers are partly vendor-blog-sourced (modal.com/blog), not the docs proper, and no third-party independent benchmark was verified; flagged inline above.
- git_worktrees and firewall_escape_hatch verdicts rest on docs silence for a feature that would likely be mentioned if it existed (git worktree: no code-execution platform is expected to name every possible shell workflow; firewall bypass: the docs are otherwise thorough about network config, so the absence of a break-glass/bypass concept is treated as a documented no rather than pure silence) — noted as slightly softer inferences than direct positive/negative statements.

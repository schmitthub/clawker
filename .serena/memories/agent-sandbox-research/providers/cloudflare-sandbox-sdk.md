# Cloudflare Sandbox SDK
category: cloud
Run untrusted/agent code in isolated containers on Cloudflare's edge, orchestrated via Workers + Durable Objects | built on Cloudflare Containers (Firecracker-class VM isolation per official docs; specific hypervisor confirmed only by third-party sources) | Apache License 2.0 | GA as of 2026-04-13, backed by Cloudflare, active development

## A. Identity

### built_on (prose-only)
Three-layer architecture per official docs: (1) Worker (developer's application logic, workerd runtime) → (2) a Sandbox Durable Object that "routes requests & maintains state" → (3) an "Isolated Ubuntu container" that "executes untrusted code safely." The Sandbox SDK's own security page states "Each sandbox runs in a separate VM, providing complete isolation," "separate network stacks," and enforced "CPU, memory, and disk quotas per sandbox" — i.e. Cloudflare's own docs commit to VM-level isolation but do not name the hypervisor. Neither the Sandbox docs nor the general Containers overview page name Firecracker, gVisor, runc, or KVM anywhere I could find. Third-party technical sources (not Cloudflare-authored) identify the underlying tech as Firecracker microVMs with KVM: independent write-ups describe "Cloudflare Containers runs on AWS-developed open-source Firecracker microVM with KVM isolation." Two Workers products exist that are easy to conflate: Containers (VM-isolated, what Sandbox SDK is built on) vs. the newer isolate-based "Dynamic Workers" (a separate, lighter-weight code-execution product) — Sandbox SDK uses the former. Communication transport between Worker and container is either per-call HTTP or a multiplexed WebSocket.
Sources:
- https://developers.cloudflare.com/sandbox/ — "Each sandbox operates as an independent container with a full Linux environment, providing strong security boundaries while maintaining performance."
- https://developers.cloudflare.com/sandbox/concepts/security/ — "Each sandbox runs in a separate VM, providing complete isolation" / "separate network stacks"
- https://developers.cloudflare.com/sandbox/concepts/architecture/ — "Isolated Ubuntu container executes untrusted code safely" (three-layer diagram: Workers → Durable Object → Containers)
- https://www.ernestchiang.com/en/posts/2025/firecracker-powered-containers-arrive-on-cloudflare/ — third-party: Cloudflare Containers built on Firecracker microVM/KVM (NOT an official Cloudflare source; used only to corroborate the VM claim Cloudflare's own docs already make without naming the hypervisor)

### execution_locality
execution_locality: Remote — all sandbox code runs in containers scheduled on Cloudflare's global network, addressed via Durable Objects. There is no "local execution" mode for the sandbox itself (only `wrangler dev`, which still runs the container via local Docker as a dev-loop convenience, not a supported production locality). Requires a Cloudflare account and, per the GA changelog, the Workers Paid plan. Project code, files, and any credentials placed inside the sandbox exist entirely on Cloudflare's infrastructure once the sandbox starts; nothing about the SDK ships a genuinely local/offline execution target. Not self-hostable outside Cloudflare's platform (see open_source below) — a "self-host" reading would require running Cloudflare's own Containers/Workers stack, which is not what the platform offers to third parties.
Sources:
- https://developers.cloudflare.com/sandbox/ — "Available on Workers Paid plan"
- https://developers.cloudflare.com/sandbox/get-started/ — "Sandbox SDK uses Docker to build container images alongside your Worker" then "npx wrangler deploy" pushes to "Cloudflare's Container Registry, and deploy[s] globally"

### open_source (prose-only)
Apache License 2.0, confirmed by reading `packages/sandbox/LICENSE` directly (repo root `LICENSE` is a symlink to it). Repository: github.com/cloudflare/sandbox-sdk. The SDK code is open source, but it is a client for Cloudflare's proprietary Containers/Durable Objects/Workers platform — there is no documented path to self-host the execution backend outside Cloudflare's own infrastructure.
Sources:
- https://github.com/cloudflare/sandbox-sdk/blob/main/packages/sandbox/LICENSE — "Apache License Version 2.0, January 2004"
- https://github.com/cloudflare/sandbox-sdk — repo root, Apache-2.0 badge

### maturity (prose-only)
Reached General Availability 2026-04-13 (Containers and Sandbox SDK together), after a public beta that per the GA changelog added "persistent code interpreters, PTY terminals, backup/restore APIs, and file-watching" plus platform-side "Active-CPU pricing," Docker Hub registry support, and SSH debug access during the beta period. Backed directly by Cloudflare (first-party product, not a community project). GitHub star count and adoption figures found via search were not independently verified against the live repo page (search results are unreliable for exact live counts) and are not repeated here as a hard number — treat as "actively maintained first-party product," not quantified adoption.
Sources:
- https://developers.cloudflare.com/changelog/post/2026-04-13-containers-sandbox-ga/ — "Containers and Sandboxes are now generally available." (2026-04-13)

## B. Threat protection

### host_fs_damage
host_fs_damage: Yes — there is no traditional "host" the agent shares a kernel/filesystem with (execution is remote, in a dedicated VM per sandbox); the docs state sandboxes "cannot access other sandboxes' files" and each sandbox's filesystem is isolated at the VM boundary. Caveat: within a single sandbox, "all processes see the same files," so multiple co-located processes/users sharing one sandbox instance can read/overwrite each other's files — isolation is per-sandbox, not per-process.
Sources:
- https://developers.cloudflare.com/sandbox/concepts/containers/ — "Each sandbox is a separate container" — "filesystem, memory and network are all isolated" / "All processes see the same files"
- https://developers.cloudflare.com/sandbox/concepts/security/ — "Sandboxes cannot access other sandboxes' files"

### credential_theft
credential_theft: Partial — the SDK documents and encourages a proxy pattern where the sandbox only ever holds a short-lived JWT, and a Worker-side proxy validates that JWT and injects the real credential before forwarding to the external API, so "real credentials never enter the sandbox" when this pattern is followed. However this is an opt-in convention the developer must implement (target/validate/transform functions), not an enforced default. The Git guide documents the un-mediated alternative too — embedding a personal access token directly in the clone URL (`https://${token}@github.com/user/private-repo.git`) — and only recommends (does not require) the proxy approach instead. No ssh-agent-style host-credential forwarding/mediation is documented anywhere in the SDK.
Sources:
- https://developers.cloudflare.com/sandbox/guides/proxy-requests/ — "The Worker validates the JWT and injects the real credential before forwarding the request. Real credentials never enter the sandbox."
- https://developers.cloudflare.com/sandbox/guides/git-workflows/ — "Use a personal access token in the URL" (documented alternative that "passes the credential directly into the sandbox")

### data_exfiltration
data_exfiltration: Partial — HTTP/HTTPS traffic (ports 80/443) can be restricted to an explicit host allowlist (`allowedHosts`, deny-by-default once set) and DNS is pinned to Cloudflare's own resolvers, which closes the DNS-tunnel exfil channel. But this control only covers ports 80/443: "Traffic on ports other than 80 and 443 is never routed through outbound or outboundByHost." With the default `enableInternet = true` and no allowedHosts configured, non-web TCP/UDP traffic is not filtered by the host-allowlist mechanism at all. The only way to close non-web ports is the binary `enableInternet = false`, which also caps web traffic to only what allowedHosts/outbound handlers explicitly permit.
Sources:
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — "When allowedHosts is set, it becomes a deny-by-default allowlist" / "Traffic on ports other than 80 and 443 is never routed through outbound or outboundByHost"
- https://developers.cloudflare.com/containers/platform-details/outbound-traffic/ — "By default, containers have unrestricted internet access"

### malicious_execution
malicious_execution: Yes — each sandbox is a separate VM with its own filesystem, network stack, and enforced CPU/memory/disk quota (instance types), so a compromised or hallucinated-code sandbox is contained to that VM's resource envelope; it cannot reach other sandboxes' state directly. Recommended mitigation for untrusted multi-tenant workloads is "use separate sandboxes per user," i.e. blast-radius containment is achieved by the caller choosing sandbox granularity, not by an additional in-sandbox sub-isolation layer.
Sources:
- https://developers.cloudflare.com/sandbox/concepts/security/ — protects against "sandbox-to-sandbox access (VM isolation)," "resource exhaustion (enforced quotas)," "container escapes (VM-based isolation)"; "use separate sandboxes per user"

### escape_resistance
escape_resistance: Partial — Cloudflare's own Sandbox docs commit to VM-level isolation ("Each sandbox runs in a separate VM, providing complete isolation," and sandboxes "Cannot: load kernel modules or access host hardware"), which is a materially stronger boundary than a shared-kernel container (runc-style). This is stronger than plain-process or shared-kernel-container isolation by design. Marked Partial rather than Yes because: (a) the specific hypervisor/isolation technology is not named by Cloudflare's own docs — the Firecracker/KVM identification comes only from third-party sources, not Cloudflare; (b) no discussion of known escape CVEs, security audit reports, or a documented threat model beyond the one-paragraph "Protects against" bullet list was found.
Sources:
- https://developers.cloudflare.com/sandbox/concepts/security/ — "Each sandbox runs in a separate VM, providing complete isolation" / "Cannot: load kernel modules or access host hardware"
- https://www.ernestchiang.com/en/posts/2025/firecracker-powered-containers-arrive-on-cloudflare/ — third-party corroboration of Firecracker/KVM as the specific mechanism (not confirmed by Cloudflare's own docs)

### resource_abuse
resource_abuse: Yes — six fixed instance types (lite/basic/standard-1..4) each with defined vCPU/memory/disk (e.g. lite = 1/16 vCPU, 256 MiB, 2 GB disk; standard-4 = 4 vCPU, 12 GiB, 20 GB disk), plus custom-instance bounds (max 4 vCPU, 12 GiB memory, 20 GB disk, minimum 3 GiB memory per vCPU). Account-level ceilings also apply: 1,500 concurrent vCPU, 6 TiB concurrent memory, 30 TB concurrent disk, 50 GB total image storage per account. Billing is metered per 10ms of active use, which is also a structural cap on unbounded runaway cost (not a runaway execution kill switch, but a cost-abuse limiter).
Sources:
- https://developers.cloudflare.com/containers/platform-details/limits/ — instance type table (lite..standard-4) and "Concurrent vCPU: 1,500" / "Concurrent memory: 6 TiB" / "Concurrent disk: 30 TB"
- https://developers.cloudflare.com/containers/pricing/ — "billed for every 10ms that they are actively running"

## C. Feature set & granularity

### network_default_posture
network_default_posture: open-by-default — an unconfigured sandbox has unrestricted internet access out of the box; restriction is opt-in via `enableInternet = false` (hard deny-all-except-explicit-allow) or via configuring `allowedHosts` (deny-by-default allowlist scoped to HTTP/HTTPS only).
Sources:
- https://developers.cloudflare.com/containers/platform-details/outbound-traffic/ — "By default, containers have unrestricted internet access"
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — "By default, a Sandbox allows internet access"

### egress_allowlist
egress_allowlist: Partial — `allowedHosts`/`deniedHosts` support host/IP allow and deny lists with simple glob patterns (`*` matches any sequence, e.g. `141.101.64.0/18` as a CIDR-style entry alongside plain hostnames), and setting `allowedHosts` flips the sandbox into deny-by-default allowlist mode. Granularity ladder reached: domain list + glob wildcards + IP/CIDR entries + explicit deny rules with documented precedence (deniedHosts → allowedHosts → per-host handler → catch-all handler → public internet). Ceiling: this whole mechanism is scoped to ports 80/443 only (see proto_coverage) — there is no port-range or non-HTTP protocol scoping, and no declarative path/method rule syntax (path/method logic is left to hand-written handler code, see http_path_rules).
Sources:
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — "Both allowedHosts and deniedHosts support simple glob patterns where * matches any sequence of characters" / example `deniedHosts = ["some-nefarious-website.com", "141.101.64.0/18"]`

### dns_level_blocking
dns_level_blocking: Partial — DNS resolution itself is pinned exclusively to Cloudflare's own resolvers ("DNS queries are the one exception, but they only go to Cloudflare's DNS servers. That prevents using arbitrary DNS destinations for data exfiltration"), which blocks DNS-tunneling-style exfiltration. But this is not the same mechanism as an unlisted-domain-fails-to-resolve (NXDOMAIN) allowlist: enforcement of `allowedHosts`/`deniedHosts` for denied destinations happens at the HTTP-proxy interception layer (TPROXY + Host match), not by causing the denied domain's DNS lookup to fail. No documentation found stating that a request to a denied host fails at the DNS step rather than at the HTTP-proxy step.
Sources:
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — "DNS queries are the one exception, but they only go to Cloudflare's DNS servers. That prevents using arbitrary DNS destinations for data exfiltration."

### tls_mitm_inspection
tls_mitm_inspection: Yes — HTTPS interception is enabled by default (`interceptHttps = true`). A unique ephemeral CA and private key are generated per sandbox instance; the CA is placed in the sandbox and trusted automatically at startup (runtime attempts auto-trust across common Linux distros), and the private key "never leaves the container runtime sidecar process and is never shared across instances." This is what makes `outbound`/`outboundByHost` handlers able to see and modify HTTPS request contents (e.g. inject auth headers) rather than just TCP-level host/SNI.
Sources:
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — "An ephemeral CA file is created at /etc/cloudflare/certs/cloudflare-containers-ca.crt once the sandbox starts"
- https://developers.cloudflare.com/changelog/post/2026-04-13-sandbox-outbound-workers-tls-auth/ — "A unique ephemeral certificate authority (CA) and private key are created for each sandbox instance... The ephemeral private key never leaves the container runtime sidecar process and is never shared across instances."

### http_path_rules
http_path_rules: No — there is no declarative path/method rule syntax (no path prefix/regex config, no method-gating config field). Path- and method-level control is achieved only by writing imperative logic inside a custom `outbound`/`outboundByHost` handler function (the documented example manually checks `request.method !== "GET"` and returns 405). This gives full programmatic power but no built-in declarative rule engine comparable to path-prefix/regex allow-deny rules.
Sources:
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — handler example: `if (request.method !== "GET") { return new Response("Method Not Allowed", { status: 405 }); }`

### proto_coverage
proto_coverage: Partial — controllable/inspectable: HTTP and HTTPS on ports 80/443 (host allow/deny + full MITM + programmatic path/method logic), and DNS (pinned to Cloudflare's resolvers). Not independently controllable: every other TCP/UDP port and protocol (raw TCP, UDP, SSH, custom L7, ICMP not mentioned anywhere) — these are governed only by the single binary `enableInternet` switch: fully open when true (default), fully closed when false, with no granularity in between and no documented extensibility mechanism for plugging a new/custom L7 protocol into the allow/deny + MITM rule model the way HTTP/HTTPS get it.
Sources:
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — "Traffic on ports other than 80 and 443 is never routed through outbound or outboundByHost. If you set enableInternet = false, that traffic is denied." / "Only ports 80, 443, and DNS are available" (when enableInternet=false)

### live_rule_reload
live_rule_reload: Yes — documented instance methods `setOutboundByHost()`, `setOutboundHandler()`, `setOutboundByHosts()`, `setAllowedHosts()`, `setDeniedHosts()`, `allowHost()`, `denyHost()`, `removeAllowedHost()`, `removeDeniedHost()` change egress policy on a live-running sandbox without a restart. Caveat: `enableInternet` itself is fixed at sandbox start and requires the sandbox to (re)start to change.
Sources:
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — "enableInternet takes effect when the sandbox starts. Changes to outbound handlers and related outbound policies can affect a live-running sandbox without restarting it."
- https://developers.cloudflare.com/changelog/post/2026-04-13-sandbox-outbound-workers-tls-auth/ — "changing egress policy for a running sandbox without restarting it"

### firewall_escape_hatch
firewall_escape_hatch: No — no timed/self-expiring bypass mechanism (e.g. "open everything for 15 minutes then re-enforce") was found anywhere in the docs. The only controls are: the binary `enableInternet` toggle (requires sandbox start/restart to change) and live add/remove of individual host rules via the setters above. Turning `enableInternet` off/on is closer to the "all-or-nothing" pattern the guidelines call out as disqualifying (toggle the whole feature / restart the sandbox), not a scoped break-glass window with automatic re-enforcement.
Sources:
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — (absence — page documents enableInternet, allowedHosts/deniedHosts, and the setter methods; no bypass/timed-disable feature described)

### enforcement_plane
enforcement_plane: prose-only. In local dev (`wrangler dev`), a sidecar process is spawned "inside the sandbox's network namespace" and applies TPROXY rules to route matching traffic to the local Workerd instance, "mirroring production behavior" — implying production enforcement is architecturally the same shape: a Cloudflare-controlled sidecar inside/adjacent to the sandbox's own network namespace applying kernel-level TPROXY redirection into the Workers runtime for HTTP/HTTPS, with the MITM CA's private key confined to that "container runtime sidecar process." This sits below the application but inside the sandbox's own VM/network-namespace boundary, not on a wholly separate network-perimeter appliance the agent has zero code-path proximity to. No documentation was found addressing whether a process with root inside the sandbox could tamper with or route around the TPROXY rules from inside — this is an open question, not a confirmed protection.
Sources:
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — "A sidecar process is spawned inside the sandbox's network namespace. It applies TPROXY rules to route matching traffic to the local Workerd instance, mirroring production behavior."
- https://developers.cloudflare.com/changelog/post/2026-04-13-sandbox-outbound-workers-tls-auth/ — "The ephemeral private key never leaves the container runtime sidecar process"

### fail_closed
fail_closed: Unknown — no documentation was found describing what happens to egress enforcement if the coordinating Durable Object, the container runtime sidecar, or Cloudflare's control plane fails or restarts mid-session. This is a fully managed platform (no user-operated supervisor process to inspect), and Cloudflare does not publish failure-mode behavior for this internal component.
Sources:
- (none found; searched https://developers.cloudflare.com/sandbox/guides/outbound-traffic/, /sandbox/concepts/architecture/, /sandbox/concepts/security/ — no failure-mode discussion in any)

### network_audit
network_audit: Partial — no first-class, automatic per-request egress log/audit trail ships with the SDK. What exists: general Cloudflare Workers Observability/Logs (request-level logs, metrics, tracing for the Worker itself), which a developer could use to self-instrument egress auditing by adding logging calls inside their own `outbound`/`outboundByHost` handler code — but that is developer-built, not a provided audit feature, and none of the Sandbox-specific docs mention an egress audit log.
Sources:
- https://developers.cloudflare.com/workers/observability/ — general Workers request logs/metrics/tracing (not sandbox-egress-specific)
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — (no audit/logging feature mentioned on the egress-control page itself)

### workspace_modes
workspace_modes: No — there is no live host-directory bind-mount concept, because there is no persistent "host" machine in this model (execution is remote by design). The closest analogues: (1) S3-compatible bucket mounting (R2/S3/GCS) as a sandbox filesystem path, which in local dev with `wrangler dev` and `localBucket: true` uses "periodic sync" between the R2 binding and the container rather than a direct mount, explicitly documented as not fully matching production's copy-on-write overlay behavior; (2) `createBackup()`/`restoreBackup()` snapshot/restore (see snapshots_persistence) as the persistence mechanism instead of a live bind mount. Sandbox filesystem state is otherwise lost on sleep/destroy and a fresh container starts clean.
Sources:
- https://developers.cloudflare.com/sandbox/guides/mount-buckets/ — bucket mounting; `localBucket` option syncs the R2 binding in dev rather than mounting
- https://developers.cloudflare.com/sandbox/concepts/sandboxes/ — "All files are deleted / All processes terminate / All shell state resets" on sleep/destroy; next request gets "a fresh container with a clean environment"

### observability
observability: Partial — no sandbox-specific activity dashboard (e.g., a view of in-container agent behavior, commands run, files touched) was found. What's available is the generic Cloudflare Workers Observability/Logs/Metrics product (request logs, error rates, CPU/wall time, execution duration) at the Worker layer, plus whatever the developer streams back themselves via `exec()` output, file-watching events, or WebSocket process output — passive visibility exists but is assembled by the developer, not delivered as a sandbox-specific monitoring stack.
Sources:
- https://developers.cloudflare.com/workers/observability/ — general Workers metrics/logs/tracing product
- https://developers.cloudflare.com/sandbox/api/ — "File watching," "Stream Output" as SDK primitives a developer wires up themselves

### supervision
supervision: No — no documented control-plane component observes in-sandbox agent behavior and can actively intervene (kill/quarantine/contain) beyond what the developer's own Worker code does by calling SDK methods (e.g., manually killing a process). The Durable Object layer supervises sandbox *lifecycle and routing* (start/sleep/destroy, request dispatch) rather than agent *behavior*; no anomaly-detection, containment-command, or security-supervision feature is documented.
Sources:
- https://developers.cloudflare.com/sandbox/concepts/architecture/ — Durable Object described as routing requests and maintaining state, not behavioral supervision
- https://developers.cloudflare.com/sandbox/concepts/sandboxes/ — lifecycle states (creation/active/idle/destruction) with no mention of behavioral intervention

### fleet_mgmt
fleet_mgmt: Partial — sandboxes are addressed by developer-chosen ID strings routed through Durable Objects, and the docs give explicit naming conventions for fleet-like usage (`user-${userId}` per-user, unique-ID per-session, `build-${repoName}-${commit}` per-task), which gives natural, collision-safe addressing at scale. But there is no documented first-class registry/listing feature (no "list all live sandboxes" API/dashboard) — the docs explicitly note multi-agent/fleet management beyond individual-sandbox naming and geographic routing is not addressed.
Sources:
- https://developers.cloudflare.com/sandbox/concepts/sandboxes/ — naming patterns (`user-${userId}`, `build-${repoName}-${commit}`); no fleet-listing feature documented

### snapshots_persistence
snapshots_persistence: Yes — `createBackup()`/`restoreBackup()` create point-in-time snapshots of a sandbox directory as a compressed squashfs archive uploaded directly to a developer-provided R2 bucket via presigned URL; production restore uses copy-on-write (backup mounted read-only as lower layer, writes go to an upper layer); backup handles are serializable (storable in KV/D1/Durable Object storage) and carry a TTL enforced at restore time. `useGitignore: true` can exclude `.gitignore`-matched paths (e.g. `node_modules/`) from a backup.
Sources:
- https://developers.cloudflare.com/sandbox/guides/backup-restore/ — "Snapshot and restore sandbox directories in seconds with the new createBackup() and restoreBackup() methods" / "restore uses copy-on-write semantics. The backup is mounted as a read-only lower layer, and new writes go to a writable upper layer."

## D. Setup
### setup
setup: Moderate — prerequisites are a Cloudflare account, Node.js ≥16.17.0, and a running local Docker (Sandbox SDK "uses Docker to build container images alongside your Worker"). Scaffold via `npm create cloudflare@latest -- my-sandbox --template=cloudflare/sandbox-sdk/examples/minimal`, then `npm run dev`. First local run builds the Docker image, documented at 2-3 minutes; production deploy is `npx wrangler deploy`, with a further "wait 2-3 minutes before making requests" after first deploy while the image propagates. Not "trivial" because it requires both a cloud account/paid plan and local Docker, and there's a multi-minute image-build/propagation wait on first use in both directions (local and prod); not "painful" because the scaffold command and subsequent steps are few and clearly documented.
Sources:
- https://developers.cloudflare.com/sandbox/get-started/ — "First run builds the Docker container (2-3 minutes)" / "After first deployment, wait 2-3 minutes before making requests"

## E. Daily use
### daily_use
daily_use: Moderate — day-to-day iteration is `wrangler dev` for local loop (backed by real Docker container builds/restarts, not a lightweight mock) and `wrangler deploy` for shipping; sandboxes themselves sleep after inactivity (10 min default) and lose all filesystem/process state unless `keepAlive: true` (which sends heartbeat pings every 30s to prevent eviction) or state is explicitly persisted via backups/R2 mounts. This means routine session friction includes remembering that a re-awoken sandbox is a "fresh container with a clean environment" — daily use requires designing around statelessness rather than assuming a persistent dev box.
Sources:
- https://developers.cloudflare.com/sandbox/concepts/sandboxes/ — "After a period of inactivity (10 minutes by default, configurable via sleepAfter), the container stops" / "Containers with keepAlive: true never enter the idle state. They automatically send heartbeat pings every 30 seconds"

## F. Configuration
### config_depth
config_depth: Deep for build/image/lifecycle, shallow for network policy declarativeness. Project config lives in `wrangler.jsonc` (name, entry point, compatibility flags, `containers` array pointing at a Dockerfile/`class_name`/`image`, Durable Object bindings, R2 bucket bindings, env vars/secrets) plus a full Dockerfile for the image itself — both are plain versionable files. Lifecycle knobs (`sleepAfter`, `keepAlive`, instance type) and network policy (`enableInternet`, `allowedHosts`, `deniedHosts`, `interceptHttps`, `outbound`/`outboundByHost` handlers) are set in TypeScript/JS code at the SDK call site rather than in the config file, and network rules in particular are imperative handler functions rather than a declarative rule block — escape hatches (custom Dockerfile, arbitrary handler code, Docker-in-Docker) are extensive but code-driven, not config-driven.
Sources:
- https://developers.cloudflare.com/sandbox/configuration/wrangler/ — `containers` array with `class_name`/`image`, `durable_objects.bindings`, `r2_buckets`, `vars`
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — network policy set via SDK method calls / handler functions, not declarative wrangler config

### policy_model
policy_model: Moderately policy-driven — secure-by-default is NOT the starting posture for networking (open internet by default; see network_default_posture), but once a developer opts in, the toggles are genuinely per-sandbox and can be dialed at multiple points: per-sandbox `enableInternet`/`allowedHosts`/`deniedHosts`/`interceptHttps` at start, live runtime adjustment via setters, and full custom logic via `outbound`/`outboundByHost` handlers. Workspace/persistence policy is similarly a-la-carte (ephemeral by default, opt into R2 mounts or backup/restore for persistence; opt into `keepAlive` to avoid sleep). What's missing for "fully policy-driven": no declarative security-profile file a team could review/version as a single policy artifact — network and lifecycle policy is scattered across wrangler config and imperative SDK/handler code.
Sources:
- https://developers.cloudflare.com/sandbox/guides/outbound-traffic/ — per-sandbox `enableInternet`, `allowedHosts`, `deniedHosts`, `interceptHttps` options set at `container.start()`
- https://developers.cloudflare.com/sandbox/concepts/sandboxes/ — per-sandbox `sleepAfter`/`keepAlive` lifecycle options

## G. DX — host↔sandbox integration

### bind_mount_sharing
bind_mount_sharing: No — see workspace_modes (axis C) for full detail. No live two-way bind mount of a host directory exists because there is no persistent host in this execution model; the closest features are R2/S3-compatible bucket mounts and backup/restore snapshots, neither of which is a live bind mount.
Sources:
- https://developers.cloudflare.com/sandbox/guides/mount-buckets/ — bucket mount as the closest analogue, explicitly a sync/mount-overlay model, not a host-directory bind mount

### cred_forwarding
cred_forwarding: Partial — (corrected 2026-07-18, attribution audit) no ssh-agent/GPG socket forwarding is documented, but pattern (c) — the JWT-proxy mechanism, where the sandbox holds only a short-lived JWT and "the Worker validates the JWT and injects the real credential before forwarding the request... real credentials never enter the sandbox" — is exactly the rule's "proxy header-injection with sentinel values that never expose the raw secret inside the sandbox" category, a real mediated-forwarding mechanism (opt-in, developer-implemented, HTTP-only — not general-purpose like ssh-agent/GPG, and doesn't cover git-over-SSH).
Sources:
- https://developers.cloudflare.com/sandbox/guides/git-workflows/ — "Use a personal access token in the URL" (no SSH key forwarding option documented)
- https://developers.cloudflare.com/sandbox/guides/proxy-requests/ — JWT-proxy pattern for HTTP APIs; no SSH/GPG equivalent documented

### browser_auth
browser_auth: No — no mechanism was found for a process inside the sandbox to trigger a browser-open on a developer's host machine and have an OAuth/device-code callback proxied back in (the pattern many coding-agent CLIs use for `login` flows). This is architecturally consistent with the platform being fundamentally remote/serverless (no fixed "host" the sandbox is paired with) — the documented alternative for any auth need is the JWT-proxy / injected-credential pattern (see cred_forwarding), which assumes headless, pre-provisioned credentials rather than an interactive login flow.
Sources:
- (absence — searched https://developers.cloudflare.com/sandbox/guides/proxy-requests/, /sandbox/guides/git-workflows/, /sandbox/concepts/security/; no browser-auth-proxying feature documented in any)

### shared_dirs
shared_dirs: Partial — beyond the sandbox's own filesystem, S3-compatible buckets (R2, S3, GCS) can be mounted as local filesystem paths inside the sandbox, and multiple such mounts are supported per the Storage API category. This is the SDK's "additional shared storage" mechanism, but it is object-storage-backed, not additional host directories.
Sources:
- https://developers.cloudflare.com/sandbox/api/ — "Storage" — "Mount S3-compatible buckets as local filesystems for persistent data"

### git_worktrees
git_worktrees: Unknown — the Git Workflows guide documents `gitCheckout()` with `branch`/`depth`/`targetDir` options for basic clone operations; no mention of `git worktree` support (add/list/remove) was found in that guide or elsewhere in the docs searched. Docs silence, not a confirmed absence.
Sources:
- https://developers.cloudflare.com/sandbox/guides/git-workflows/ — documents `gitCheckout()` clone options only; no worktree functionality mentioned

### nested_containers
nested_containers: Partial — Docker-in-Docker is supported via a documented pattern (base image `docker:dind-rootless` + Cloudflare's sandbox binary copied in + a startup script that launches the Docker daemon with iptables disabled), not a flip-a-flag opt-in — it requires building a custom Dockerfile from Cloudflare's template. Explicit caveats: "No iptables — network isolation features that rely on iptables are not available," "Rootless mode only — you cannot use privileged containers," "Ephemeral storage — built images and containers are lost when the sandbox sleeps," and inner containers needing network access must use `--network=host`, which the docs flag as sharing "the outer container's network stack" with an explicit warning to "understand the security implications."
Sources:
- https://developers.cloudflare.com/sandbox/guides/docker-in-docker/ — "No iptables... Rootless mode only... Ephemeral storage..." / "each inner container has access to your outer container's network stack. Ensure you understand the security implications"

### harness_agnostic
harness_agnostic: Yes — the SDK is a generic remote code-execution primitive (shell exec, background processes, file ops, code interpreter) with no coding-agent-specific API surface; any CLI or agent framework can be installed into the custom Dockerfile and driven via `exec()`/`startProcess()`. The GitHub README lists both a Claude Code integration example and an OpenAI Agents tools example as separate, equally-supported patterns, and the Cloudflare Agents docs integration is likewise framework-generic ("Agents can use Sandbox to run code in isolated container environments") with no exclusivity to Cloudflare's own Agents SDK.
Sources:
- https://github.com/cloudflare/sandbox-sdk — README references example projects "code interpreters, Claude Code integration, and OpenAI Agents tools"
- https://developers.cloudflare.com/agents/tools/sandbox/ — "Agents can use Sandbox to run code in isolated container environments" (generic framing, no CLI/vendor exclusivity stated)

## H. Performance
### performance
performance: Moderate/heavy relative to local-process execution, by design (real per-sandbox VM boot on every cold start). Official platform docs state container cold starts "can often be in the 1-3 second range, but this is dependent on image size and code execution time, among other factors" — no Sandbox-SDK-specific benchmark (as opposed to generic Containers) was found, and no first-party numbers for warm-start latency, disk footprint, RAM overhead, or bind-mount-style IO throughput were found (there is no bind-mount IO path to benchmark, see workspace_modes). Third-party benchmark write-ups found via search (e.g. a blog comparing Cloudflare Containers cold starts to AWS microVMs) exist but were not fetched/verified as official and are not cited here as fact — flagging their existence only as a pointer for further reading, not as a sourced claim.
Sources:
- https://developers.cloudflare.com/containers/platform-details/architecture/ — "Container cold starts can often be in the 1-3 second range, but this is dependent on image size and code execution time, among other factors."

## I. Feasibility
### feasibility
feasibility: Adoptable-today with real friction points — platform-agnostic for the *developer's* machine (any OS that runs Node ≥16.17.0 and Docker can build/deploy), but the *execution* platform is single-vendor (Cloudflare only; no self-host, no other cloud target), which is a hard lock-in for the runtime even though the client SDK is Apache-2.0. Requires a Workers Paid plan (no free tier for Containers/Sandbox — see pricing). Product reached GA 2026-04-13, so it is no longer beta, but is young (GA for roughly 3 months as of this writeup's 2026-07-18 as-of date) with an active-development changelog cadence, which carries some maturity/API-stability risk for teams wanting long-term stability guarantees beyond "GA."
Sources:
- https://developers.cloudflare.com/changelog/post/2026-04-13-containers-sandbox-ga/ — GA date 2026-04-13
- https://developers.cloudflare.com/sandbox/ — "Available on Workers Paid plan"
- https://developers.cloudflare.com/sandbox/get-started/ — Node.js ≥16.17.0 and Docker prerequisites (developer-machine requirements only, not execution-time requirements)

## J. Price (prose-only)
### pricing
No free tier for Containers/Sandbox — requires the Workers Paid plan ($5 USD/month base). Included-then-metered model on top of that: Memory 25 GiB-hours/month included, then $0.0000025 per additional GiB-second; vCPU 375 vCPU-minutes/month included, then $0.000020 per additional vCPU-second; Disk 200 GB-hours/month included, then $0.00000007 per additional GB-second. Billing granularity is "billed for every 10ms that they are actively running," and "Memory and disk usage are based on the provisioned resources for the instance type you select, while CPU usage is based on active usage only" (i.e. CPU is metered on actual use, memory/disk on reservation). Network egress is billed separately by region: North America & Europe $0.025/GB (1 TB included), Oceania/Korea/Taiwan $0.05/GB (500 GB included), everywhere else $0.04/GB (500 GB included). Worker requests and the one Durable Object per container are billed separately under their own pricing models. No self-host option (see execution_locality/open_source).
Sources:
- https://developers.cloudflare.com/containers/pricing/ — "Memory: 25 GiB-hours/month included +$0.0000025 per additional GiB-second" / "billed for every 10ms that they are actively running" / egress rates by region

## K. Extensibility
### extensibility
extensibility: Partial — the primary extensibility surface is the Dockerfile itself: any packages, language runtimes, or CLI tools (including third-party coding-agent CLIs, see harness_agnostic) can be baked into the custom image, and Docker-in-Docker is possible via a documented (if caveated) template. Network policy is extensible via arbitrary handler code (`outbound`/`outboundByHost`) rather than a plugin system. No bundle/marketplace/plugin-registry concept, no custom-harness-definition schema, and no documented API-hook system beyond the outbound handlers and the standard SDK method surface (exec, files, processes, git, ports, storage, backups, sessions, terminal).
Sources:
- https://developers.cloudflare.com/sandbox/get-started/ — Dockerfile is a first-class generated project file, freely customizable
- https://developers.cloudflare.com/sandbox/guides/docker-in-docker/ — documented (caveated) path to nested container runtimes as an extensibility example

## Unknowns & caveats
- **fail_closed**: no documentation found on what happens to egress enforcement if the Durable Object/control plane/sidecar fails mid-session — genuinely undocumented for this managed platform, not just unsearched.
- **git_worktrees**: docs silence only (not confirmed absent) — the Git guide covers clone/checkout options only.
- **enforcement_plane tamper-resistance**: no documentation found on whether a root process inside the sandbox could interfere with the TPROXY rules the sidecar installs in the sandbox's own network namespace; flagged as an open question, not resolved either way.
- **exact hypervisor**: Cloudflare's own Sandbox/Containers docs commit to "separate VM" isolation but never name Firecracker/KVM explicitly in any official page found; the Firecracker/KVM identification in this writeup rests on third-party sources only, consistent with the task's "provisional" framing.
- **GitHub star count / adoption metrics**: search results returned inconsistent/unverifiable numbers (also an inconsistent license claim — BSD vs Apache-2.0 — that was resolved by reading the LICENSE file directly, Apache-2.0 confirmed); no live GitHub API call was made (out of scope for research-tools-only constraint), so no star count is asserted as fact in this writeup.
- **No URLs were blocked by network/firewall issues during this research** — all WebFetch/WebSearch calls succeeded (some 404s were guessed URLs, immediately corrected by search, not firewall blocks).

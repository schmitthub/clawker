# Vercel Sandbox
category: cloud
Managed remote compute primitive for running untrusted/agent-generated code | built on Firecracker microVMs (Amazon Linux 2023) | SDK/CLI client (Apache-2.0) is open source, runtime is a closed managed service | backed by Vercel; actively developed (advanced egress firewall shipped Feb 2026, Drives in private beta, v2 SDK w/ persistent sandboxes)
As-of: 2026-07-18. Sources fetched from vercel.com/docs (last_updated dates noted per-page where shown).

## A. Identity

### built_on (prose-only)
Each sandbox runs in its own Firecracker microVM with a dedicated kernel, booting a built-in Amazon Linux 2023 runtime image (or a custom VCR image / saved snapshot). Control plane is Vercel's own infrastructure; sandboxes currently provision only in the `iad1` region. Network egress is enforced by an inline proxy/gateway performing "SNI-peeking" (inspecting the unencrypted ClientHello of the TLS handshake to read the target hostname) plus CIDR-based rules; TLS is only terminated (MITM) for domains with explicit transformation/forwarding rules, via a unique per-sandbox CA.
Sources:
- https://vercel.com/docs/sandbox — "Each sandbox runs in a secure Firecracker microVM with its own filesystem and network."
- https://vercel.com/docs/sandbox/concepts — "Unlike Docker containers, each sandbox runs in its own Firecracker microVM with a dedicated kernel."
- https://vercel.com/changelog/advanced-egress-firewall-filtering-for-vercel-sandbox — "inspects the initial unencrypted bytes of a TLS handshake to extract the target hostname" (third-party WebFetch summary of the changelog; treat wording as paraphrase, not verbatim vendor text)

### execution_locality
Remote — sandboxes run entirely on Vercel's managed infrastructure (`iad1` region only, as of this writing); no self-host option exists. Project code, files, and any credentials passed into the sandbox (env vars, git tokens) leave the developer's machine and execute on Vercel's servers. Local dev only obtains an OIDC token via `vercel link` / `vercel env pull`; the actual code execution and filesystem never touch the local disk except for whatever the SDK explicitly writes/reads through its file APIs.
Sources:
- https://vercel.com/docs/sandbox/concepts — "The sandbox runs on Vercel's global infrastructure, so you don't need to manage servers... Sandboxes automatically provision in `iad1` region."
- https://vercel.com/docs/sandbox/pricing — "Currently, Vercel Sandbox is only available in the `iad1` region."

### open_source (prose-only)
The `@vercel/sandbox` SDK and `sandbox` CLI (github.com/vercel/sandbox) are Apache-2.0 licensed, but this repo is client/API tooling only — it does not contain the sandbox runtime, Firecracker orchestration, or firewall implementation. The actual execution infrastructure is closed-source and not self-hostable; it runs only as a managed Vercel service ("the same infrastructure that powers 2M+ builds a day at Vercel").
Sources:
- https://github.com/vercel/sandbox — repo description: "Vercel Sandbox is an ephemeral compute primitive designed to safely run untrusted or user-generated code." License: Apache-2.0. Contents: SDK + CLI only.

### maturity (prose-only)
Backed by Vercel, a well-funded, widely-adopted hosting platform vendor — not a small/independent project. The client SDK repo has a modest 164 GitHub stars, but that undercounts real usage since most consumption happens through the Vercel dashboard/platform rather than direct repo interaction. The product is under active development: "Advanced egress firewall filtering" (SNI/CIDR policies, credentials brokering, request proxying) shipped as a changelog item in Feb 2026; a v2 SDK introduced persistent-by-default sandboxes; Drives (persistent volume mounts) are in private beta as of this writing.
Sources:
- https://github.com/vercel/sandbox — 164 stars (per WebFetch tool result)
- https://vercel.com/changelog/advanced-egress-firewall-filtering-for-vercel-sandbox — dated Feb 11, 2026
- https://vercel.com/docs/sandbox/concepts/drives — "🔒 Permissions Required: Drives" / "Once you are added to the private beta"

## B. Threat protection

### host_fs_damage
Yes — each sandbox has a dedicated private filesystem inside its own Firecracker microVM with a dedicated kernel; the docs state code in one sandbox "cannot access or interfere with others or the underlying host system."
Sources:
- https://vercel.com/docs/sandbox/concepts — "Each sandbox runs in its own lightweight virtual machine with a dedicated kernel, ensuring that code in one sandbox cannot access or interfere with others or the underlying host system."

### credential_theft
Partial — a "credentials brokering" feature exists specifically to keep secrets out of sandbox scope: it injects credentials onto egress HTTP traffic to matched domains so the API key/token itself never enters the sandbox. However this only covers HTTP(S) domain-rule traffic and is a permission-gated (paid/restricted) feature. For git access to private repos, the documented pattern is the opposite: personal access tokens or GitHub App installation tokens are passed directly as plaintext credentials (`password` field) into `Sandbox.create()`'s `source` config, i.e. real credential material does enter the sandbox for that use case — the guide does not describe any agent-forwarding/mediation mechanism for it.
Sources:
- https://vercel.com/docs/sandbox/concepts/firewall — "Credentials brokering allows the injection of credentials on egressing traffic, while ensuring those secrets never enter the sandbox scope, preventing exfiltration."
- https://vercel.com/kb/guide/sandbox-private-github-repositories — token passed as `password: process.env.GIT_ACCESS_TOKEN!` directly to sandbox creation (per WebFetch summary of guide)

### data_exfiltration
Yes, but opt-in — sandboxes support `deny-all` and custom domain/CIDR allowlist network policies, updatable live without restart, specifically pitched to "prevent data exfiltration." However this protection is NOT the default (see network_default_posture below) — an unconfigured sandbox has open egress.
Sources:
- https://vercel.com/docs/sandbox/concepts/firewall — "Network firewall allows users to restrict egress traffic from their sandbox. It is a critical tool to prevent data exfiltration."

### malicious_execution
Yes — product is explicitly designed for this threat model: "Sandboxes vs containers" comparison states containers are "suitable for trusted code; container escapes are possible" while sandboxes are "designed for untrusted code; microVM boundary prevents escapes."
Sources:
- https://vercel.com/docs/sandbox/concepts — comparison table, "Security" row: "Designed for untrusted code; microVM boundary prevents escapes."

### escape_resistance
Yes (strong, prose-heavy) — isolation boundary is a dedicated-kernel Firecracker microVM per sandbox, explicitly contrasted against shared-kernel container isolation (namespaces/cgroups) which the docs themselves say is escapable. No known-escape-surface disclosure was found (expected — vendors don't publish this), but the architecture is a hypervisor/VM boundary, not a syscall filter or shared-kernel container.
Sources:
- https://vercel.com/docs/sandbox/concepts — table row "Isolation: Docker containers = Shares host kernel; relies on namespaces and cgroups" vs "Vercel Sandboxes = Dedicated kernel per sandbox; full VM isolation."

### resource_abuse
Yes — every sandbox has enforced CPU, memory, and disk limits plus automatic timeouts, tiered by plan (e.g. Hobby: 4 vCPU/8GB max, 45 min max duration, 10 concurrent; Pro: 8 vCPU/16GB, 24h max, 2000 concurrent; Enterprise: 32 vCPU/64GB, 24h max). Disk is fixed at 32GB ephemeral NVMe regardless of plan. Rate limits also cap vCPU allocation and control-plane request rate per minute.
Sources:
- https://vercel.com/docs/sandbox/pricing — resource/runtime/concurrency/rate-limit tables (e.g. "Hobby | 4 | 8GB | 15 | 32 GB", "Hobby | 45 minutes")

## C. Feature set & granularity

### network_default_posture
Open-by-default. The default network policy is `allow-all`, giving the sandbox "unrestricted access to the public Internet" out of the box (needed to install packages from npm/PyPI etc.). Restriction is opt-in via `deny-all` or a custom domain/CIDR allowlist.
Sources:
- https://vercel.com/docs/sandbox/concepts/firewall — "`allow-all` ... Default policy. This gives the sandbox unrestricted access to the public Internet."
- https://vercel.com/docs/sandbox/sdk-reference — "`networkPolicy` ... Defaults to `\"allow-all\"`."

### egress_allowlist
Yes, with a documented granularity ladder: (1) binary `allow-all`/`deny-all`; (2) domain allowlist with wildcard support (`*.example.com` matches any subdomain but not the apex; a mid-segment wildcard like `www.*.com` matches only that one segment); (3) IP/CIDR address-range allow rules (bypass domain matching entirely, needed for non-TLS traffic); (4) IP/CIDR deny rules that take precedence over CIDR/domain allow rules; (5) for the subset of rules that also carry a `transform` (credentials brokering) or `forwardURL` (proxying) action, additional path/method/query-string/header matchers (exact, prefix, or RE2 regex) scope which requests get transformed/forwarded — but plain allow/deny rules do not get this path-level granularity, only the transform/proxy feature does.
Sources:
- https://vercel.com/docs/sandbox/concepts/firewall — "a list of domains to allow traffic to... Wildcard support (`*`)... a list of address ranges to allow traffic to... a list of address ranges to deny traffic to. Those range will take precedence to block traffic."
- https://vercel.com/docs/sandbox/sdk-reference — "if the domain starts with a wildcard `*` (e.g. `*.google.com`), any subdomain is matched. It will not match the parent domain" / CIDR deny "will always take precedence over `subnets.allow` and domain-based `allow` entries."

### dns_level_blocking
No — enforcement happens at the TLS handshake via SNI hostname inspection ("SNI-peeking"), not by blocking DNS resolution of disallowed domains. The one documented DNS-specific behavior is under `deny-all` mode, where "all outbound network access, including DNS" is blocked wholesale (not selective per-domain DNS filtering).
Sources:
- https://vercel.com/docs/sandbox/concepts/firewall — "`deny-all`... Denies all outbound network access, including DNS."
- https://vercel.com/changelog/advanced-egress-firewall-filtering-for-vercel-sandbox — "Outbound TLS connections are matched against your policy at the handshake, unauthorized destinations are rejected before any data is transmitted" (per WebFetch summary)

### tls_mitm_inspection
Partial — TLS is only intercepted/terminated for domains that have an explicit `transform` (credentials brokering) or `forwardURL` (proxying) rule attached, using a unique per-sandbox CA installed into the system trust store. For plain allow-listed domains with no transform/forward rule, traffic passes through un-terminated: "Encryption is not intercepted if no transformation or forwarding rules are defined, allowing end-to-end data confidentiality."
Sources:
- https://vercel.com/docs/sandbox/concepts/firewall — "In order to apply transformation and forwarding rules within requests, the firewall needs to terminate TLS connections. Only connections targeting domains with defined transformation rules are terminated in the proxy."
- https://vercel.com/docs/sandbox/sdk-reference — "Encryption is not intercepted if no transformation or forwarding rules are defined, allowing end-to-end data confidentiality."

### http_path_rules
Partial — path/method/query-string/header matchers exist (exact, prefix, RE2-regex) but are only usable as a scoping condition attached to a `transform` or `forwardURL` rule (which require TLS termination), not as a standalone path-level allow/deny gate on a domain. There is no way to allow `example.com/public/` while denying `example.com/private/` without also routing that traffic through a transform/proxy rule.
Sources:
- https://vercel.com/docs/sandbox/concepts/firewall — "Each rule can define a set of matchers... on the path, method, query parameters, and headers. When defined, only requests matching the specified dimensions will be transformed or forwarded."

### proto_coverage
Partial, prose-heavy. Domain-based rules match "the hostname negotiated during the TLS handshake," so plain HTTPS (and any protocol that does a standard TLS handshake with SNI) is covered by domain rules. Postgres gets special-cased handling: the firewall explicitly parses the Postgres wire protocol's pre-TLS negotiation before applying domain policy, with caveats (`sslmode=require`+ needed, GSSAPI-encrypted connections unsupported, `sslmode=prefer` won't downgrade, and transform/forward rules don't apply to Postgres). Plain-text HTTP and any non-TLS protocol CANNOT be filtered by domain — only by IP/CIDR range. CIDR allow/deny rules are protocol-agnostic (any TCP to that range). No documentation was found addressing ICMP, generic UDP, QUIC/HTTP3, SSH, WebSocket, or gRPC specifically — these are undocumented gaps, not confirmed-blocked or confirmed-passthrough. On extensibility: the `forwardURL`/`transform` framework is HTTP/1.1-specific ("must be a URL pointing to an HTTP/1.1-capable server") — there is no documented mechanism for plugging in arbitrary new L7 protocols into the rule model.
Sources:
- https://vercel.com/docs/sandbox/concepts/firewall — "HTTPS traffic is matched using the SNI... Plain-text HTTP cannot be filtered by domain, and must be allowed by IP range instead." / "Postgres connections to hosted databases are supported when the database host is added to a sandbox's allowed domains... TLS is required... GSSAPI-encrypted connections are not supported."
- https://vercel.com/docs/sandbox/concepts/firewall — "The `forwardURL` field must be a URL pointing to an HTTP/1.1-capable server."

### live_rule_reload
Yes — "Sandboxes can use three distinct modes, which can be updated at runtime, without restarting the process," and `sandbox.update({ networkPolicy })` applies to the current session as well as future sessions.
Sources:
- https://vercel.com/docs/sandbox/concepts/firewall — "Sandboxes can use three distinct modes, which can be updated at runtime, without restarting the process."
- https://vercel.com/docs/sandbox/sdk-reference — "`networkPolicy` is applied to the current session as well as future sessions."

### firewall_escape_hatch
Yes, in the sense of a live per-sandbox toggle (not a timed auto-expiring bypass): `sandbox.update({ networkPolicy: 'allow-all' })` (or the deprecated `updateNetworkPolicy('allow-all')`) can flip a running, restricted sandbox back to unrestricted egress at any time without recreating it, and the docs explicitly recommend this pattern ("start with Internet access, get required data, lock access and start untrusted process"). No documented auto-expiring/timed bypass with automatic re-enforcement was found — the escape hatch is manual (you must explicitly flip it back).
Sources:
- https://vercel.com/docs/sandbox/concepts/firewall — "Dynamic policies for multi-step work: Start with Internet access, get required data, lock access and start untrusted process."
- https://vercel.com/docs/sandbox/sdk-reference — "await sandbox.updateNetworkPolicy('allow-all'); // Allow all egress from the sandbox"

### enforcement_plane
Prose — enforcement sits in an inline network proxy/gateway in Vercel's infrastructure path (outside the guest microVM), performing SNI-peeking on the TLS handshake and CIDR matching, with selective TLS termination for transform/forward rules via a per-sandbox CA. This is a network/hypervisor-boundary enforcement point, not an in-guest mechanism (not eBPF/netfilter running inside the sandbox's own kernel) — the agent, running inside its own microVM, has no path to the enforcement point to tamper with it, since it sits on the host/network side of the microVM boundary. Whether traffic is logged at that layer beyond the aggregate "Data Transfer" billing metric is not documented (see network_audit).
Sources:
- https://vercel.com/docs/sandbox/concepts/firewall — "In order to apply transformation and forwarding rules within requests, the firewall needs to terminate TLS connections... in the proxy." / "A unique, per-sandbox CA is added to the system certificates."
- https://vercel.com/docs/sandbox/concepts — "Each sandbox has its own network namespace with controlled outbound access."

### fail_closed
Unknown — no documentation was found describing what happens to an active network policy if Vercel's control plane / enforcement proxy fails. Since this is a fully closed, managed service with no visibility into the control-plane architecture, this could not be determined from official docs; searched the firewall, concepts, and changelog pages plus WebSearch with no result addressing control-plane-failure behavior for network policy.
Sources: none found (searched https://vercel.com/docs/sandbox/concepts/firewall, https://vercel.com/docs/sandbox/concepts, https://vercel.com/changelog/advanced-egress-firewall-filtering-for-vercel-sandbox)

### network_audit
Partial — the Sandboxes dashboard shows a per-sandbox "Activity" log of lifecycle events (create/stop/resume) and an aggregate "Sandbox Data Transfer" billing metric (bytes sent to internet + exposed-port traffic), but no documentation describes a per-request egress audit log (i.e., a list of individual allowed/denied destination hostnames/requests). Searched the firewall page, working-with-sandbox page, changelog, and WebSearch specifically for network/egress audit logging; found only the lifecycle Activity log and aggregate metrics.
Sources:
- https://vercel.com/docs/sandbox/concepts/persistent-sandboxes — "**Activity**: A log of sandbox lifecycle events."
- https://vercel.com/docs/sandbox/working-with-sandbox — "**Sandbox Data Transfer**: Data your sandboxes send to the internet, plus all traffic to and from exposed ports, is billable."

### workspace_modes
Partial — there is no host bind-mount concept at all since execution is remote (see execution_locality); "workspace modes" instead maps to: (a) persistent sandboxes (default) — filesystem auto-snapshotted on stop and restored on resume, or (b) non-persistent/ephemeral sandboxes (`persistent: false`) — filesystem discarded on stop. Additionally, Drives provide a separate persistent-volume-mount primitive orthogonal to sandbox persistence. No live two-way sync between a local dev machine and the sandbox filesystem exists; transfer is via explicit `writeFiles`/`readFile` API calls, git clone, or a tarball mount source.
Sources:
- https://vercel.com/docs/sandbox/concepts/persistent-sandboxes — "Persistent (default): State on stop: Automatically snapshotted" / "Non-persistent: State on stop: Discarded unless manually snapshotted"

### observability
Yes — dashboard (Observability > Sandboxes) shows total/running/stopped sandbox counts, command history, sandbox URLs, per-sandbox Activity log, and a project-wide Usage dashboard tracking Provisioned Memory, Data Transfer, Active CPU, Creations, and Snapshot Storage. The SDK also exposes a real-time `logs()` streaming API for stdout/stderr of running commands.
Sources:
- https://vercel.com/docs/sandbox/working-with-sandbox — "View your sandboxes in the Sandboxes dashboard... Total sandboxes created, Currently running sandboxes, Stopped sandboxes, Command history and sandbox URLs."
- https://vercel.com/docs/sandbox/sdk-reference — "Call `logs()` to stream structured log entries in real time."

### supervision
Partial/No — Vercel does not provide a built-in autonomous supervisor that observes agent behavior and intervenes on its own; the developer's own application code is expected to call `stop()`, `update({ networkPolicy: 'deny-all' })`, etc. as needed. This is active control available to the calling application (a real capability beyond passive observability), but it is not a product-provided supervisory layer watching for misbehavior and containing it — no such feature was found documented.
Sources:
- https://vercel.com/docs/sandbox/sdk-reference — `sandbox.update()` / `sandbox.stop()` as caller-invoked control methods (no autonomous/automatic policy-violation response documented)

### fleet_mgmt
Yes — `Sandbox.list()` supports filtering by `namePrefix` and `tags`, cursor-based pagination, and sort by name/createdAt/statusUpdatedAt; every sandbox has a project-unique `name`; key-value `tags` categorize sandboxes by environment/team/etc.
Sources:
- https://vercel.com/docs/sandbox/concepts/persistent-sandboxes — "`Sandbox.list` supports cursor-based pagination... `namePrefix: 'user-a'`... `tags: { env: 'production' }`"

### snapshots_persistence
Yes — persistence is the default: filesystem auto-snapshotted on stop, restored on resume by name, with configurable `snapshotExpiration` and `keepLastSnapshots` retention. Manual `snapshot()` calls, `Sandbox.fork()` (spawn a new sandbox seeded from another's latest snapshot), and snapshot ancestry-walking APIs are also available. Drives (beta) add a second, independent persistent-storage primitive (up to 4 drives/sandbox, up to 1TiB each) for data that should survive independent of sandbox snapshots.
Sources:
- https://vercel.com/docs/sandbox/concepts/persistent-sandboxes — "Persistent sandboxes automatically save their filesystem state when stopped and restore it when resumed."
- https://vercel.com/docs/sandbox/concepts/drives — "Drives provide persistent storage that can be mounted into sandboxes... up to 4 drives into a sandbox, with up to 1 TiB of storage per drive."

## D. Setup
### setup
Easy — prerequisites are a Vercel account, Vercel CLI, and Node 22+/Python 3.10+. Flow: create a directory, `vercel link` (create/select a project), `vercel env pull` to get an OIDC token into `.env.local`, `npm i @vercel/sandbox`, write ~10 lines of code to call `Sandbox.create()` + `runCommand()`. No Docker/Kubernetes prerequisite, no manual firewall/network setup needed to get a first run working (default policy is open).
Sources:
- https://vercel.com/docs/sandbox/quickstart — "Prerequisites: A Vercel account... Vercel CLI installed... Node.js 22+ or Python 3.10+" and the numbered setup/install/write/run steps.

## E. Daily use
### daily_use
Easy to moderate — named, persistent sandboxes mean `Sandbox.get({name})`/`getOrCreate` auto-resumes without manual snapshot bookkeeping, and `sandbox run --name X -- <cmd>` / `sandbox connect <name>` give quick CLI access including an "SSH-like" interactive shell. Friction comes from the default 5-minute auto-timeout (must set/extend `timeout` for longer sessions) and the fact that all interaction is via SDK/CLI calls to a remote API rather than a local terminal/editor integration — there's no live filesystem sync back to a local editor.
Sources:
- https://vercel.com/docs/sandbox/working-with-sandbox — "Debug with an interactive shell... `sandbox connect <name>`... Once connected, you have full shell access."
- https://vercel.com/docs/sandbox/pricing — "The default timeout is 5 minutes."

## F. Configuration
### config_depth
Deep, but API/parameter-driven rather than a single declarative project file. Tunable at `Sandbox.create()`/`update()`/CLI-flag level: runtime or custom VCR image, vCPU/memory (`resources`), `timeout`, `persistent`, `snapshotExpiration`/`keepLastSnapshots`, `networkPolicy` (with domain/CIDR/matcher rules), `ports`, `env`, `tags`, drive `mounts`, and lifecycle hooks (`onCreate`, `onResume`). No single versionable config file equivalent (e.g. no `clawker.yaml`-style manifest) was found — configuration is expressed as SDK call arguments or CLI flags, which a project could of course wrap in its own script/file.
Sources:
- https://vercel.com/docs/sandbox/sdk-reference — `Sandbox.create()` parameter table (`ports`, `networkPolicy`, `mounts`, etc.)
- https://vercel.com/docs/sandbox/concepts/persistent-sandboxes — `onCreate`/`onResume` lifecycle hooks.

### policy_model
Moderately policy-driven — network egress has a real 3-mode + live-update policy model with sane secure defaults available (`deny-all`/custom allowlist) though NOT the out-of-box default (`allow-all` is default). Persistence, resource sizing, and timeout are all per-sandbox toggles settable at creation or via `update()`. However, there's no unified "profile" or single-file policy object covering workspace mode + network + credentials together — each dimension (network policy, persistence, resources) is configured independently via separate SDK parameters/CLI flags, with no evidence of a composed named-policy system.
Sources:
- https://vercel.com/docs/sandbox/sdk-reference — `sandbox.update()` accepting `resources`, `timeout`, `persistent`, `networkPolicy`, `ports`, `tags` all independently.

## G. DX — host↔sandbox integration

### bind_mount_sharing
No — there is no live bind mount between a host/local machine and the sandbox; this follows structurally from remote execution (see execution_locality). File transfer is one-way-at-a-time via the SDK's `writeFiles`/`readFile`/`readFileToBuffer` API, a `tarball` mount `source` at creation, or `git clone` run inside the sandbox. No filesystem-watch/live-sync API was found ("APIs such as file handles and watchers are not currently included" for the `fs` compatibility surface).
Sources:
- https://vercel.com/docs/sandbox/sdk-reference — "`FileSystem` gives you a `node:fs/promises`-compatible surface... APIs such as file handles and watchers are not currently included."

### cred_forwarding
Partial — no ssh-agent or gpg-agent socket-forwarding mechanism is documented. For git access to private repos, the documented pattern passes a personal access token or GitHub App installation token directly as sandbox creation config (`source.password` style), meaning the raw credential is delivered into the sandbox rather than mediated through an agent/socket bridge. Separately, the firewall's "credentials brokering" feature (permission-gated) does mediate credentials for HTTP(S) calls to specific allow-listed domains, injecting them at the proxy so the secret itself never reaches the sandbox — but this is domain-rule-scoped, not a general-purpose credential-forwarding mechanism.
Sources:
- https://vercel.com/kb/guide/sandbox-private-github-repositories — PAT/App-token passed as `password` to `Sandbox.create()`'s `source` (per WebFetch summary)
- https://vercel.com/docs/sandbox/concepts/firewall — "Credentials brokering allows the injection of credentials on egressing traffic, while ensuring those secrets never enter the sandbox scope."

### browser_auth
Unknown — no documentation was found describing a host-browser-proxy mechanism (sandboxed process triggers an OAuth/device-code browser flow that opens on a "host" machine and forwards the callback back into the sandbox). This may be structurally less applicable given Sandbox is a remote/cloud primitive with no persistent "host" the way a local dev tool has one, but the docs don't explicitly rule the pattern in or out, so Unknown rather than No.
Sources: none found (searched https://vercel.com/docs/sandbox/working-with-sandbox, https://vercel.com/docs/sandbox/concepts/authentication)

### shared_dirs
Partial — Drives (beta) provide Vercel-managed persistent volumes that can be mounted into a sandbox at an absolute path (up to 4 per sandbox, up to 1TiB each, read-write or read-only), and can be shared/reused across separate sandbox runs. This is conceptually similar to "additional shared dirs" but the storage is a Vercel-managed cloud volume, not a host directory, and is currently single-reader/single-writer (no concurrent multi-sandbox read/write sharing yet).
Sources:
- https://vercel.com/docs/sandbox/concepts/drives — "Mount up to 4 drives into a sandbox, with up to 1 TiB of storage per drive." / "Drives are currently single reader, single writer."

### git_worktrees
No — no first-class/product-level git-worktree integration is documented anywhere in the Sandbox docs, CLI reference, or SDK reference. `git` is installed as a base package so `git worktree` commands would function as generic Linux/git capability inside a sandbox, but there is no dedicated Sandbox feature (flag, API, or workflow) built around worktrees.
Sources:
- https://vercel.com/docs/sandbox/concepts/runtimes — base package list includes `git`, with no worktree-specific tooling mentioned.

### nested_containers
Yes — the docs explicitly document running Docker/other container runtimes inside the sandbox under `sudo`, isolated by the microVM boundary, as a first-class "system-privileged process" use case (also documents VPN clients and FUSE filesystems in the same category). Caveat: containers run inside the sandbox do not inherit the sandbox's proxy CA certificate automatically — it must be manually mounted/trusted inside the container for firewall-terminated HTTPS traffic to verify.
Sources:
- https://vercel.com/docs/sandbox/concepts/runtimes — "Container runtimes: Run Docker and other container engines inside the sandbox to build images or run containerized workloads." / "Containers do not inherit the proxy CA."

### harness_agnostic
Yes — Sandbox is a generic Linux VM with full root access, not tied to any specific coding-agent CLI. Official Vercel Knowledge Base guides document running Claude Agent SDK, OpenCode, and OpenClaw inside Sandbox.
Sources:
- https://vercel.com/docs/sandbox/working-with-sandbox — "Use with Claude Agent SDK... Run OpenClaw in Vercel Sandbox... Run OpenCode securely with the Vercel Sandbox."

## H. Performance
### performance
Lightweight, per vendor framing — "Fast startup: Sandboxes start in milliseconds" and Firecracker itself is marketed as optimized for fast boot; resuming from a snapshot is stated to be faster than a fresh boot. No independent (non-vendor) benchmark numbers were found for cold/warm startup latency, disk IO throughput, or CPU overhead — all performance claims found are vendor statements, not measured/cited third-party numbers. Disk is a fixed 32GB ephemeral NVMe volume regardless of plan tier.
Sources:
- https://vercel.com/docs/sandbox — "Fast startup: Sandboxes start in milliseconds, making them ideal for real-time user interactions and latency-sensitive workloads."
- https://vercel.com/docs/sandbox/concepts — comparison table: "Startup time: Docker containers = Sub-second; Vercel Sandboxes = Milliseconds (Firecracker optimized for fast boot)."

## I. Feasibility
### feasibility
Adoptable today for teams already on or willing to adopt Vercel, moderate elsewhere. Positives: no infrastructure to stand up, generous free Hobby tier (5 CPU-hrs/mo, 10 concurrent sandboxes, 45-min max duration) suitable for prototyping/solo use, Pro tier ($20/mo credit) unlocks 24h runtime and 2000 concurrent sandboxes. Negatives: fully managed/closed (vendor lock-in, no self-host), single region (`iad1`) only as of this writing (latency for non-US-east workloads), platform-tied auth model (OIDC via Vercel project, or Vercel access token) ties usage to a Vercel account/team.
Sources:
- https://vercel.com/docs/sandbox/pricing — Hobby/Pro/Enterprise tables (limits, concurrency, rate limits).
- https://vercel.com/docs/sandbox/pricing — "Currently, Vercel Sandbox is only available in the `iad1` region."

## J. Price (prose-only)
Usage-metered across five dimensions: Active CPU ($0.128/vCPU-hour on Pro/Enterprise; time waiting on I/O is not billed), Provisioned Memory ($0.0212/GB-hour, 1-minute minimum increments, 2GB RAM per vCPU), Sandbox Creations ($0.60 per 1M), Data Transfer (outbound + exposed-port traffic; $0.15/GB; inbound/download traffic is free), and Snapshot Storage ($0.08/GB-month). Hobby plan includes a free monthly allotment (5 Active-CPU-hours, 420 GB-hours memory, 5,000 creations, 20GB transfer, 15GB lifetime snapshot storage) with no overage charges — sandbox creation simply pauses once exceeded, for 30 days from first use. Pro plan usage draws down a $20/month platform credit before metered billing kicks in. Enterprise is custom-priced (contact sales). No self-host / on-prem pricing option exists — this is a managed-service-only product.
Sources:
- https://vercel.com/docs/sandbox/pricing — full pricing table and "Understanding the metrics" section (Active CPU, Provisioned Memory, Network, Snapshot Storage definitions and formulas).

## K. Extensibility
### extensibility
Partial/Yes — sandboxes can boot from custom OCI images pushed to Vercel Container Registry (built from arbitrary Dockerfiles, though Sandbox does not run the image's `ENTRYPOINT`/`CMD` — you must start processes explicitly via `runCommand()` after boot). Drives and tags add configurable storage/categorization dimensions. The firewall's `forwardURL` hook (paired with the `defineSandboxProxy` SDK helper) lets you route matched traffic through your own proxy for custom logging/transformation/auth logic — a genuine extension point, though scoped to HTTP/1.1 domain rules only. Lifecycle hooks (`onCreate`/`onResume`) allow custom setup/teardown code. No broader plugin/bundle/marketplace system (e.g. for packaging reusable environment or policy definitions) was found documented.
Sources:
- https://vercel.com/docs/sandbox/concepts/images — "Vercel Sandbox does not run Docker `ENTRYPOINT` or `CMD` for custom images. Start processes with `sandbox.runCommand()` after the sandbox is created."
- https://vercel.com/docs/sandbox/concepts/firewall — "Requests proxying allows forwarding traffic toward specific domains to a proxy you control, for logging, debugging, or transformation purposes."

## Unknowns & caveats
- **fail_closed**: not documented — no source found describing network-policy behavior if Vercel's control plane/enforcement proxy fails (docs silence, not confirmed absence).
- **network_audit**: no per-request egress audit log found documented; only aggregate Data Transfer billing metric and lifecycle Activity log confirmed. Could not fully rule out an undocumented/dashboard-only feature.
- **browser_auth**: no documentation found either way on a host-browser-proxied OAuth flow; marked Unknown rather than No per guidelines since docs are silent, not explicit.
- **proto_coverage**: DNS-protocol-level, ICMP, generic UDP, QUIC/HTTP3, SSH, WebSocket, and gRPC handling under the firewall are all undocumented gaps — neither confirmed filtered nor confirmed passthrough beyond the general SNI/TLS and Postgres-specific statements.
- **git_worktrees**: absence inferred from a full silence across Sandbox docs + a general WebSearch turning up no Sandbox-specific worktree feature; git itself is present as a base package so worktree commands are usable generically, just not a product feature.
- Third-party WebFetch tool summarized two long pages (`concepts/firewall`'s changelog counterpart, `kb/guide/sandbox-private-github-repositories`) — those quotes are flagged inline as paraphrase/summary rather than guaranteed-verbatim vendor text; the core firewall mechanics were independently corroborated on the primary `concepts/firewall` and `sdk-reference` pages fetched directly.
- No blocked URLs — all planned official-doc fetches succeeded (no NXDOMAIN/connection-refused encountered; research tooling had full internet access per this run's operational note).

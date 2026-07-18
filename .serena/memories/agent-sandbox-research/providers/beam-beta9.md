# Beam (beta9)
category: cloud
Serverless cloud runtime (Beam, managed) built on open-source engine beta9; sandboxes/functions/endpoints/pods execute as remote containers in Beam's cloud or a self-hosted beta9 cluster | built on custom Go container runtime + gVisor/runc isolation | AGPL-3.0 (beta9 OSS) | ~1.7k GitHub stars, active (latest gateway release 0.1.722, 2026-07-17)

## A. Identity

### built_on
prose-only. Beta9 is "a fast, open-source runtime for serverless AI workloads" with its own custom container runtime: "our own container runtime in Go that could spin up containers directly from a root filesystem. Essentially what Docker does under the hood" (2021-era architecture blog). A distinguishing feature is a FUSE-based lazy-loading image format for fast cold starts. A 2026 Beam blog post comparing self-host sandbox options states Beam's own isolation choice explicitly: "Isolation: gVisor + runc — a user-space kernel intercepts guest syscalls before they reach the host," characterized as "Stronger than a plain container; lighter than a full microVM" — contrasted against Firecracker microVMs (used by competitor E2B, "the strongest isolation tier") and bare runc/namespaces+cgroups (weaker). Control plane: a "beta9-gateway" service (gRPC :1994 / HTTP :1993) plus worker nodes; self-hosted deployment is Kubernetes+Helm based, requiring an S3-compatible object store for the filesystem layer. Nodes authenticate to the control plane with short-lived tokens ("trustless binary" model, no persistent on-node credentials), per a Beam blog post referencing their SOC 2 audit.
Sources:
- https://beam.cloud/blog/what-is-a-container-really — "our own container runtime in Go that could spin up containers directly from a root filesystem. Essentially what Docker does under the hood"
- https://beam.cloud/blog/how-to-self-host-code-sandbox — "Isolation: gVisor + runc — a user-space kernel intercepts guest syscalls before they reach the host"
- https://docs.beam.cloud/v2/self-hosting/local-machine.md — "kubectl port-forward svc/beta9-gateway 1993 1994"

### execution_locality
execution_locality: Remote — all code (functions, endpoints, sandboxes, pods) runs in Beam-managed cloud containers (or a self-hosted beta9 cluster the team stands up separately), never on the developer's own machine.
Self-hosting is offered (`v2/self-hosting/overview`, AWS and "local machine" [Kubernetes/k3d-style] guides) using the AGPL-3.0 beta9 engine, but this is a separate deployment the operator runs and manages — it does not make default usage "local"; the developer's laptop is still a client talking to a gateway over gRPC/HTTP, not the execution host. Workspace files are synced/uploaded to the remote container ("Beam syncs your code, launches a container, runs the function, and streams the result back to your shell") rather than executed in-place; Sandbox filesystem access is via an explicit upload/download API (`sb.fs.upload_file` / `download_file`), not a live host mount. Project code and any secrets referenced by the job are transmitted to Beam's (or the self-hosted cluster's) infrastructure to execute.
Sources:
- https://docs.beam.cloud/v2/getting-started/quickstart.md — "Beam syncs your code, launches a container, runs the function, and streams the result back to your shell"
- https://docs.beam.cloud/v2/sandbox/filesystem.md — "a built-in file system API available at `sb.fs`" (upload_file/download_file)
- https://docs.beam.cloud/v2/self-hosting/overview.md — "Beta9 is the open source project that powers Beam"

### open_source
prose-only. beta9 (the engine) is open source under AGPL-3.0, hosted at github.com/beam-cloud/beta9, and is self-hostable (Kubernetes + Helm, requires an S3-compatible object store). The managed "Beam" cloud product is closed/commercial and layered on top. "Beta9 is the open-source engine powering Beam, our fully-managed cloud platform. You can self-host Beta9 for free or choose managed cloud hosting through Beam."
Sources:
- https://github.com/beam-cloud/beta9 — LICENSE: AGPL-3.0
- https://docs.beam.cloud/v2/self-hosting/overview.md — "Beta9 is the open source project that powers Beam"

### maturity
prose-only. ~1.7k GitHub stars, 150 forks as of 2026-07-18; predominantly Go (79.9%) with a Python (19.1%) SDK. Actively released (gateway v0.1.722, dated 2026-07-17). Backed by a funded company (Beam Cloud) offering commercial managed hosting, GPU inventory, and enterprise/BYOC tiers; SOC 2 Type II compliance claimed on the marketing site.
Sources:
- https://github.com/beam-cloud/beta9 — 1.7k stars, 150 forks, latest release 0.1.722 (2026-07-17)
- https://www.beam.cloud — "SOC 2 Type II" compliance claim

## B. Threat protection

### host_fs_damage
host_fs_damage: NA — there is no "host" in the local sense; execution is remote. The relevant boundary is isolation between the sandbox container and the underlying node/other tenants' sandboxes, covered under escape_resistance below. A local developer's own filesystem is never exposed to the sandbox except via explicit, one-directional upload/download calls.
Sources:
- https://docs.beam.cloud/v2/sandbox/filesystem.md — file API is upload/download only, no host mount described

### credential_theft
credential_theft: Partial — secrets are stored server-side in Beam's secret manager and injected into containers as named environment variables (`beam secret create KEY VALUE`, then declared in the function/sandbox decorator); no plaintext secret material needs to live in the user's repo. However, docs show no ssh-agent or GPG-agent forwarding mechanism from the developer's machine into the sandbox — the only documented "credential" path is Beam's own env-var secrets store, plus separate secrets for mounting external S3-compatible buckets. Because secrets are copied into the remote container's environment (not proxied/mediated per-call), a compromised sandbox process can read every secret env var granted to it, same as a container getting a copied API key.
Sources:
- https://docs.beam.cloud/v2/environment/secrets.md — "Secrets and environment variables can be injected into the containers that run your apps"
- https://docs.beam.cloud/v2/reference/cli.md — `beam secret create/list/show/modify/delete`

### data_exfiltration
data_exfiltration: Partial — outbound network can be fully blocked (`block_network=True`) or restricted to an allowlist of up to 10 CIDR ranges (`allow_list`); "All other outbound traffic will be blocked" when an allow list is set. This is real but coarse control: allowlisting is IP/CIDR-only (no domain names, no DNS-based rules, no path/method scoping seen in docs), capped at 10 entries, and is opt-in — see network_default_posture below for the default-open concern. Full detail under axis C (network control block).
Sources:
- https://docs.beam.cloud/v2/sandbox/networking.md — "Maximum of 10 CIDR entries per Sandbox"; "All other outbound traffic will be blocked"

### malicious_execution
malicious_execution: Yes — blast radius of untrusted code is contained by gVisor's user-space kernel (syscall interception) layered over runc, per Beam's own comparison of self-host sandbox isolation options. This is stronger than a bare container but the same Beam post states it is weaker than microVM isolation (Firecracker), i.e. still shares the host kernel image conceptually (gVisor intercepts syscalls rather than running a separate guest kernel).
Sources:
- https://beam.cloud/blog/how-to-self-host-code-sandbox — "Isolation: gVisor + runc — a user-space kernel intercepts guest syscalls before they reach the host... Stronger than a plain container; lighter than a full microVM"

### escape_resistance
escape_resistance: Partial — gVisor (user-space kernel intercepting guest syscalls) + runc, per Beam's own vendor comparison post. Beam explicitly self-ranks this below Firecracker microVMs: "Running adversarial, multi-tenant code from untrusted users? Prefer microVM... so a kernel exploit in one sandbox can't reach another" — implying gVisor+runc is not their recommendation for the highest-adversary-risk tier, and is a shared-kernel-adjacent model (gVisor mediates but does not eliminate host kernel exposure the way a separate microVM kernel does). No CVE/escape track record was found in official docs (would require deeper gVisor project history search, out of scope here).
Sources:
- https://beam.cloud/blog/how-to-self-host-code-sandbox — "Isolation: gVisor + runc"; "Prefer microVM...so a kernel exploit in one sandbox can't reach another"

### resource_abuse
resource_abuse: Partial — CPU, memory, and GPU are configurable per function/sandbox/pod (`cpu=`, `memory=`, GPU type) and billed by the second, implying scheduler-level accounting, but the docs found do not state hard enforcement mechanics (e.g., what happens on OOM, whether CPU is a hard cgroup cap or a soft/burstable share) or disk quotas. One resources doc mentions usage-vs-limit graphing ("The graph will also show the periods when your resource usage exceeded the resource limits set on your app") implying limits are tracked/enforced, but no explicit ceiling values or kill/throttle behavior are documented.
Sources:
- https://docs.beam.cloud/v2/environment/resources.md — "The graph will also show the periods when your resource usage exceeded the resource limits set on your app"

## C. Feature set & granularity

### network_default_posture
network_default_posture: Open-by-default (inferred) — Sandbox/Pod networking docs describe `block_network` (deny all egress) and `allow_list` (CIDR allowlist, deny-the-rest) as opt-in parameters a user must set; nothing in the fetched docs states an implicit default-deny stance, and the API shape (both restriction mechanisms require an explicit call/flag) only makes sense if the baseline is unrestricted egress. This is an inference from API structure, not an explicit doc statement — flagged as such; see Unknowns.
Sources:
- https://docs.beam.cloud/v2/sandbox/networking.md — block_network and allow_list both presented as opt-in configuration, no default-deny statement found
- https://docs.beam.cloud/v2/pod/networking.md — same pattern for Pods

### egress_allowlist
egress_allowlist: Partial — allowlisting exists but is IP/CIDR-only: "specify an allow list of CIDR ranges that your Pod/Sandbox is permitted to connect to," "Maximum of 10 CIDR entries," supports IPv4 and IPv6, proper CIDR notation required (e.g. `8.8.8.8/32`, `10.0.0.0/8`). No domain-name allowlisting, no subdomain wildcards, no port/port-range scoping, and no deny-rule/precedence semantics beyond the binary allow_list-vs-block_network choice were found. `allow_list` and `block_network` are mutually exclusive (can't combine partial-allow with a base full-block).
Sources:
- https://docs.beam.cloud/v2/sandbox/networking.md — "Must use proper CIDR notation (e.g., \"8.8.8.8/32\"... \"10.0.0.0/8\")"; "Maximum of 10 CIDR entries per Sandbox"; "Cannot use allow_list and block_network together" (pod/networking.md wording)

### dns_level_blocking
dns_level_blocking: Unknown — no documentation found describing DNS-layer enforcement (vs. IP-layer). Since the allowlist mechanism is CIDR/IP-based rather than domain-based, DNS resolution itself does not appear to be the enforcement point, but no explicit statement either confirms or denies DNS-level blocking exists as a separate layer.
Sources:
- https://docs.beam.cloud/v2/sandbox/networking.md — no DNS mention found

### tls_mitm_inspection
tls_mitm_inspection: No — the documented network control primitives (block_network, IP/CIDR allow_list) operate at the IP/port level, not L7; there is no mention of TLS interception, MITM, or any L7-aware routing for egress control. (Inbound preview URLs are separately "SSL-terminated" for exposing a sandbox's own service to the internet, which is unrelated to inspecting the sandbox's outbound TLS traffic.)
Sources:
- https://docs.beam.cloud/v2/sandbox/networking.md — mechanisms described are CIDR-based and process-level (block all / allow specific IP ranges), no TLS/L7 terms present

### http_path_rules
http_path_rules: No — no path- or method-based rule mechanism is documented anywhere in sandbox/pod networking; the only egress control granularity is destination IP/CIDR (plus the full block_network toggle).
Sources:
- https://docs.beam.cloud/v2/sandbox/networking.md — CIDR-only control surface, no path/method rule syntax present

### proto_coverage
proto_coverage: Unknown/Partial — docs describe outbound control as "outbound connections"/"outbound network access" without naming protocols; the pod/networking page is explicit that documented port exposure is "TCP only; no mention of UDP, DNS, or other protocols is provided." Whether the egress allowlist/block covers UDP, ICMP, or only TCP is not stated. No mention of custom/extensible L7 protocol rule support.
Sources:
- https://docs.beam.cloud/v2/pod/networking.md — inbound port exposure documented as TCP; no UDP/ICMP/DNS coverage statement found for outbound control either

### live_rule_reload
live_rule_reload: Yes — `update_network_permissions()` changes take effect without restarting the sandbox: "Changes take effect immediately without requiring a restart," while port accessibility is preserved across the update.
Sources:
- https://docs.beam.cloud/v2/sandbox/networking.md — "Changes take effect immediately without requiring a restart"

### firewall_escape_hatch
firewall_escape_hatch: Partial — network policy (block_network / allow_list) can be toggled per-sandbox at runtime via `update_network_permissions()`, which is not an all-or-nothing "tear down the sandbox" model. However, no documented mechanism provides a *timed*, auto-re-enforcing bypass (e.g. "open for 10 minutes then re-lock") — any loosening via `update_network_permissions()` appears to be a manual, indefinite state change until reversed by another call.
Sources:
- https://docs.beam.cloud/v2/sandbox/networking.md — `update_network_permissions()` described, no timed/auto-expiring bypass documented

### enforcement_plane
enforcement_plane: Unknown — no documentation found on where network policy is actually enforced (kernel eBPF/netfilter on the node, a userspace proxy, cloud VPC/security-group infra, or the gVisor sandbox boundary itself). No statement on whether traffic is logged at the enforcement point or whether it can be tampered with/routed around from inside the sandbox.
Sources:
- https://docs.beam.cloud/v2/sandbox/networking.md — mechanism not disclosed

### fail_closed
fail_closed: Unknown — no documentation found describing what happens to an active `block_network`/`allow_list` policy if Beam's control plane (gateway) or the enforcing component becomes unavailable.
Sources:
- (no source found; searched sandbox/networking.md, pod/networking.md, self-hosting docs)

### network_audit
network_audit: Unknown — no per-request egress log/audit feature was found in the fetched docs (sandbox/networking, pod/networking, resources). General resource-usage graphing is documented (see resource_abuse) but nothing egress-specific.
Sources:
- (no source found; searched sandbox/networking.md, pod/networking.md, environment/resources.md)

### workspace_modes
workspace_modes: Partial — no live bind mount is offered (execution is remote), but two persistence-oriented modes exist: (1) ephemeral per-run sync/upload of code+deps into a fresh container each run, and (2) explicit "Distributed Storage Volumes" mounted into containers for persistence across runs, plus filesystem/memory Snapshots to fork or resume state. Volumes are eventually-consistent, not live-synced: "It can take up to 60 seconds for any files written to a distributed volume to become available to other containers."
Sources:
- https://docs.beam.cloud/v2/data/volume.md — "It can take up to 60 seconds for any files written to a distributed volume to become available to other containers"
- https://docs.beam.cloud/v2/sandbox/snapshots.md — filesystem and memory snapshot/restore workflow

### observability
observability: Partial — per-process real-time log/output streaming exists within a sandbox (`process.logs`, separate stdout/stderr streams, "Execute code and commands with real-time output streaming in your sandbox"), and a resource-usage graph exists showing usage vs. configured limits over time. No dedicated fleet-wide metrics/dashboard/logging product was found documented (the managed platform likely has a web dashboard at platform.beam.cloud, but no page describing it was found/fetchable in this pass).
Sources:
- https://docs.beam.cloud/v2/sandbox/processes.md — "Execute code and commands with real-time output streaming in your sandbox"
- https://docs.beam.cloud/v2/environment/resources.md — usage-vs-limit graph mentioned

### supervision
supervision: Unknown — no documentation found describing an active supervisory layer that can intervene in/kill/quarantine a misbehaving sandbox beyond the user's own SDK calls (`process.kill()`, sandbox termination, TTL timeout). Those are user-invoked controls, not an independent observe-and-contain system; whether Beam's control plane itself performs automated anomaly detection/containment is not documented.
Sources:
- https://docs.beam.cloud/v2/sandbox/processes.md — user-level kill/monitor primitives only

### fleet_mgmt
fleet_mgmt: Partial — CLI/SDK provide naming and lifecycle primitives (deploy/list/stop/start/delete for apps; `beam machine list`; secrets and volumes are named, listable resources), and Sandboxes can be fanned out "into thousands of concurrent isolated runs." No dedicated fleet registry/dashboard doc (e.g. list-all-running-sandboxes-with-status) was found in the fetched pages.
Sources:
- https://docs.beam.cloud/v2/reference/cli.md — deploy/list/stop/start/delete app lifecycle commands
- https://www.beam.cloud — "restore [sandboxes] into thousands of concurrent isolated runs, each with realtime streaming output"

### snapshots_persistence
snapshots_persistence: Yes — filesystem snapshots (`sandbox.create_image_from_filesystem()` → `Image.from_id()`) and memory snapshots (`sandbox.snapshot_memory()` → `create_from_memory_snapshot()`, preserving "all running processes and exposed ports") are both documented, used for forking, faster cold starts, and returning to a saved environment later. Restore-speed benchmarks and long-horizon persistence guarantees are not documented.
Sources:
- https://docs.beam.cloud/v2/sandbox/snapshots.md — "the state of the sandbox's memory - including all running processes and exposed ports"

## D. Setup
### setup
setup: Easy — sign up for a free account ($30/mo credit), grab an API token from the web dashboard, `uv tool install beam-client`, `beam configure default --token ...`, write a decorated Python function, `python app.py`. No Docker/Kubernetes/account infra required client-side for the managed product; self-hosting (separate path) requires Kubernetes + Helm + an S3-compatible object store, which is materially more involved.
Sources:
- https://docs.beam.cloud/v2/getting-started/quickstart.md — install/configure/run steps as above
- https://docs.beam.cloud/v2/self-hosting/local-machine.md — "Kubernetes", "Helm and kubectl", S3-compatible object storage prerequisites

## E. Daily use
### daily_use
daily_use: Moderate — the SDK model is decorator-based (`@function`, `@endpoint`, explicit `Sandbox()` object) rather than "run my existing dev environment": for Sandboxes specifically, code/files must be uploaded via `sb.fs.upload_file()` and commands run via `sb.process.exec()`, i.e. explicit remote-orchestration calls rather than a transparent local-like shell. No live bind-mount means edit-in-place workflows aren't native; iteration relies on upload/exec cycles or Volumes (up to 60s propagation delay).
Sources:
- https://docs.beam.cloud/v2/sandbox/filesystem.md — upload_file/download_file API
- https://docs.beam.cloud/v2/sandbox/processes.md — exec()-based command execution

## F. Configuration

### config_depth
config_depth: Deep, but code-based (Python decorator/object kwargs) rather than a standalone declarative project file. Configurable surface per sandbox/function/pod includes: CPU, memory, GPU type/count, custom container images (`from_dockerfile()`, base image override, `add_python_packages()`/`add_commands()`), env vars, secrets, volumes (with mount paths), external S3-compatible bucket mounts (read-only or read-write), network policy (block/allowlist), port exposure (static + dynamic), and timeouts/keep-warm. No project-level YAML config file equivalent was found (config lives in Python source + CLI-managed named resources like secrets/volumes).
Sources:
- https://docs.beam.cloud/v2/sandbox/configuration.md — CPU/memory/GPU/env/secrets/volumes/timeout options
- https://docs.beam.cloud/v2/environment/custom-images.md — `from_dockerfile()`, `add_python_packages()`, `add_commands()`, `build_with_gpu()`

### policy_model
policy_model: Moderate — sensible per-resource defaults exist (private-by-default authenticated endpoints, opt-in network restriction) and several controls are overridable per-instance (block_network vs allow_list vs open, authorized=False to make an endpoint public, read_only S3 mounts, per-run CPU/memory/GPU). But some choices are binary/exclusive rather than layered (allow_list XOR block_network, no combined "allow these CIDRs AND log/deny everything else with more nuance"), and there's no evidence of a broader declarative policy file (e.g. org-wide default posture) — policy is set per resource in code/CLI, not centrally templated.
Sources:
- https://docs.beam.cloud/v2/sandbox/networking.md — allow_list/block_network mutual exclusivity
- https://docs.beam.cloud/v2/topics/public-endpoints.md — "By default, endpoints are private and require a bearer token to access"

## G. DX — host↔sandbox integration

### bind_mount_sharing
bind_mount_sharing: No — execution is remote (see execution_locality); there is no live bind mount of a host directory into the sandbox. File sharing is explicit upload/download (`sb.fs.upload_file/download_file`) or Volumes with up to ~60s write-propagation delay, both fundamentally copy-semantics, not shared-view.
Sources:
- https://docs.beam.cloud/v2/sandbox/filesystem.md — upload/download API
- https://docs.beam.cloud/v2/data/volume.md — "up to 60 seconds for any files written to a distributed volume to become available"

### cred_forwarding
cred_forwarding: No — no ssh-agent or GPG-agent socket forwarding into sandboxes was found documented. The only credential mechanism is Beam's own secrets manager, which copies named env vars into the container (see credential_theft above); this is fundamentally different from mediated forwarding of a live host agent socket.
Sources:
- https://docs.beam.cloud/v2/environment/secrets.md — env-var injection model only, no ssh-agent/gpg mention

### browser_auth
browser_auth: No — no host-browser-proxy mechanism (sandboxed process triggers an OAuth browser flow on the developer's machine, response forwarded back) is documented anywhere in the fetched docs. Beam's own CLI auth is a manual token-copy flow (grab token from platform.beam.cloud dashboard, `beam configure default --token ...`), not a browser-launch/callback flow, and given the remote-execution architecture there is no described tunnel for a process inside a cloud sandbox to open a URL on the developer's local browser.
Sources:
- https://docs.beam.cloud/v2/getting-started/quickstart.md — "Get your API token: Navigate to Settings → API Keys on platform.beam.cloud and copy your token" (manual, not browser-flow)
- https://docs.beam.cloud/v2/reference/cli.md — no `beam login`/OAuth flow found; config is token-based

### shared_dirs
shared_dirs: Partial — beyond the primary workspace, Sandboxes/Pods/Functions can mount named Volumes at arbitrary paths and external S3-compatible buckets (AWS S3, Cloudflare R2, Tigris) at arbitrary mount paths, read-only or read-write. This is host-independent shared storage (not additional host directories, since there's no "host" in the local sense).
Sources:
- https://docs.beam.cloud/v2/data/volume.md — Volume(name=..., mount_path=...)
- https://docs.beam.cloud/v2/data/external-storage.md — CloudBucketConfig, read_only flag, S3/R2/Tigris support

### git_worktrees
git_worktrees: Unknown — no mention of git-specific integration (worktrees, branch-aware workspace provisioning) found anywhere in the fetched docs. The workflow model (Python decorators + explicit file upload/Volumes) has no described git-awareness at all; docs are simply silent rather than structurally precluding it (a user could still run git manually inside a sandbox against uploaded files).
Sources:
- (no source found; searched sandbox/filesystem.md, sandbox/configuration.md, getting-started docs)

### nested_containers
nested_containers: Yes — full Docker daemon supported inside sandboxes per a Beam blog comparison post: "Beam supports running the full Docker daemon inside its containers, so these environments work without workarounds," positioned as necessary for RL workloads mirroring CI/CD pipelines. Not found in the core sandbox docs (docs-silent there), sourced from vendor blog only — flagged as such.
Sources:
- https://beam.cloud/blog/best-sandbox-providers-reinforcement-learning-2026 — "Beam supports running the full Docker daemon inside its containers, so these environments work without workarounds" (vendor blog, not core docs)

### harness_agnostic
harness_agnostic: Yes — Beam ships a generic Python/TypeScript SDK plus a documentation MCP server (`https://docs.beam.cloud/mcp`) usable from Cursor, Claude Code (`claude mcp add --transport http beam https://docs.beam.cloud/mcp`), and Claude Desktop; nothing in the product ties Sandboxes to a specific coding-agent harness — the Sandbox API is a general-purpose remote-execution primitive any tool or agent can call.
Sources:
- https://docs.beam.cloud/v2/getting-started/add-to-cursor-claude.md — Cursor/Claude Code/Claude Desktop MCP setup instructions, generic docs-search MCP not an agent-specific product

## H. Performance
### performance
performance: Lightweight-leaning, per vendor-cited benchmarks (mark as vendor benchmarks, not independently verified): "Sandboxes cold boot in 1–3 seconds, even with dependencies included," attributed to image caching of base-image dependencies. Memory snapshots are claimed to "restore containers up to 35× faster than a traditional cold boot" (marketing site). No independent/third-party benchmark was found; no disk footprint, RAM overhead, or bind-mount IO throughput figures apply (no bind mount) or were found documented.
Sources:
- https://docs.beam.cloud/v2/sandbox/overview.md — "Sandboxes cold boot in 1–3 seconds, even with dependencies included" (vendor claim)
- https://www.beam.cloud — "restore [sandboxes]...up to 35× faster than a traditional cold boot" (vendor claim)

## I. Feasibility
### feasibility
feasibility: Adoptable-today for teams comfortable with a remote-execution/cloud-billing model; not applicable for users who need local, offline, or air-gapped execution. Platform support is irrelevant client-side (any OS with Python/Node + network access can drive the SDK/CLI); the constraint is architectural (remote-only) rather than OS support. Self-hosting is possible (AGPL beta9, Kubernetes-based) for teams wanting to avoid vendor lock-in, but that path requires standing up and operating Kubernetes + S3-compatible storage + the beta9 control plane themselves — a materially higher bar than the managed product. Maturity/backing: actively maintained (see axis A), commercially backed with enterprise/BYOC tiers and stated SOC 2 Type II compliance.
Sources:
- https://docs.beam.cloud/v2/self-hosting/local-machine.md — Kubernetes/Helm/S3-compatible storage prerequisites for self-host
- https://www.beam.cloud — SOC 2 Type II claim; BYOC / multi-cloud options

## J. Price
### pricing
prose-only. New accounts get "$30 Free Credit, Every Month." Serverless (functions/endpoints) billed per-second: CPU $0.0000125/sec/core, RAM $0.0000021/sec/GiB, GPUs per-second (e.g. RTX 4090 $0.000191667/sec, H100 PCIE $0.000986/sec, H200 SXM5 $0.001136/sec, B200 SXM6 $0.001561/sec). Sandboxes have separate (slightly higher) per-second CPU/RAM rates: CPU $0.0000375/sec/core, RAM $0.0000064/sec/GiB — example given: "A 1-core (2 vCPU) / 8 GiB sandbox costs $0.319/hr." On-demand flat-rate machines also available (e.g. RTX 4090 from $0.42/hr, H100 PCIE from $1.74/hr, B200 SXM6 from $3.93/hr). Storage: up to 1TB included, then $0.021/GB/month. "Bring Your Own Cloud" (BYOC) charges only a management fee ($0.019/hr per vCPU, $0.009/hr per GB) on top of the customer's own cloud spend. Self-hosting beta9 (fully independent of Beam's billing) is free (AGPL-3.0 OSS), operator bears infra cost.
Sources:
- https://www.beam.cloud/pricing — per-second rates and BYOC management-fee figures as quoted above
- https://docs.beam.cloud/v2/self-hosting/overview.md — self-host = free OSS engine

## K. Extensibility
### extensibility
extensibility: Yes — custom container images via Dockerfile (`from_dockerfile()` with build context, arbitrary base images e.g. NVIDIA CUDA images), programmatic image composition (`add_python_packages()`, `add_commands()` for apt-level deps, `build_with_gpu()` for GPU-requiring builds), custom container registries supported (`environment/custom-registries`), pluggable external storage backends (S3/R2/Tigris via CloudBucketConfig), and a documented public API/CLI/Python/TypeScript SDK surface for programmatic control. No plugin/bundle marketplace concept was found (extensibility is "bring your own image/registry/storage," not a shareable-bundle ecosystem).
Sources:
- https://docs.beam.cloud/v2/environment/custom-images.md — from_dockerfile(), add_python_packages(), add_commands(), build_with_gpu()
- https://docs.beam.cloud/v2/data/external-storage.md — pluggable S3-compatible backends (AWS S3, Cloudflare R2, Tigris)

## Unknowns & caveats
- **network_default_posture** is inferred from API shape (block_network/allow_list both opt-in), not an explicit doc statement of "sandboxes are open by default" — flagged, not confirmed.
- **enforcement_plane, fail_closed, network_audit, dns_level_blocking, supervision** — all Unknown; no official docs page found covering these (searched sandbox/networking, pod/networking, resources, and the general docs index/llms.txt without finding relevant content). Distinct from confirmed absence.
- **proto_coverage** beyond TCP (UDP/ICMP/DNS enforcement scope) is not documented; only inbound port exposure is explicitly stated as TCP-only.
- **git_worktrees** — pure docs silence, no structural reasoning either way.
- **nested_containers (DinD)** and parts of **fleet_mgmt/observability** rely on Beam's own blog posts (vendor blog, marked explicitly) rather than core reference docs, since the reference docs were silent on these points.
- **GitHub code search** for isolation-tech keywords (firecracker/gvisor/runc in source) required auth and could not be executed; escape_resistance and built_on rely on Beam's own 2026 comparison blog post rather than source inspection.
- **WebSearch tool exhausted** partway through this session (session budget), all subsequent research used WebFetch only against known/guessed doc URLs — a few plausible doc paths (e.g. a dedicated monitoring/observability page, an explicit self-hosting ARCHITECTURE.md) returned 404 and could not be located without search; their absence in this writeup reflects "not found," not "does not exist."
- No blocked/firewalled URLs encountered — all fetches either succeeded or 404'd (page doesn't exist at guessed path), no NXDOMAIN/connection-refused observed.

# Agent-Sandbox (agent-sandbox/agent-sandbox)
category: cloud/self-hosted (Kubernetes-native, E2B-API-compatible)
Open-source, Kubernetes-native runtime for AI-agent sandboxes, exposing an E2B-protocol-compatible API on top of Kubernetes ReplicaSets. Built on: Kubernetes (v1.26+) + Go control plane + MCP server. License: Apache-2.0. Maturity: v0.8.0 as of 2026-07-10 (10 releases since v0.1.1), 172 stars / 17 forks / 4 open issues — early-stage, single-org project, no evidence of commercial backing or managed cloud offering.

Do NOT confuse with kubernetes-sigs/agent-sandbox — this is the agent-sandbox/agent-sandbox org project.

## A. Identity

### built_on (prose-only)
Kubernetes-native control plane (Go) that translates "Blueprint" Go-templates into standard Kubernetes `ReplicaSet` objects (not Pods directly, not a CRD/operator with a custom resource — the control plane directly manages ReplicaSets and stores sandbox config in ReplicaSet annotations). No custom container runtime shipped; sandbox containers run under whatever `RuntimeClass` the cluster provides (default = cluster's default, typically containerd/runc). Control plane exposes a native REST API (`/api/v1`), an E2B-compatible API (`/e2b/v1`), an MCP server, and a web UI (`/ui`). Auth is a flat bearer-token scheme (`SYSTEM_TOKEN` / `API_TOKENS_RAW`), no mTLS/OAuth found in source.
Sources:
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/pkg/config/config.go — "SandboxBlueprint" as "the Go-template ReplicaSet YAML"
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/pkg/sandbox/rs_manager.go — `buildReplicaSet()` constructs a ReplicaSet from the Sandbox object and blueprint template
- https://agent-sandbox.github.io/env/ — `SYSTEM_TOKEN`, `API_TOKENS_RAW` env vars for auth

### execution_locality
execution_locality: Remote — agent code runs as pods inside a Kubernetes cluster the operator provisions and manages; there is no local/native execution mode and no vendor-hosted cloud offering was found. Project code and any data passed to the sandbox (env vars, files) leave the developer's machine and reside on cluster nodes / cluster-attached storage (NAS/OSS/S3). A developer could point this at a local single-node cluster (kind/minikube) to keep execution physically on their laptop, but that is not a documented or intended usage mode — the Quick Start assumes an existing reachable cluster.
Sources:
- https://agent-sandbox.github.io/quickstart/ — prerequisites: "Kubernetes cluster (version 1.26 or higher)", `kubectl apply -n agent-sandbox -f install.yaml`, then port-forward or Ingress to reach the API

### open_source (prose-only)
Apache License 2.0, fully self-hostable (that is the only deployment mode — install via `kubectl apply` of `install.yaml`). No dual-license or paid-tier gating found in docs.
Sources:
- https://github.com/agent-sandbox/agent-sandbox — license: Apache-2.0

### maturity (prose-only)
172 stars, 17 forks, 4 open issues, 10 tagged releases from v0.1.1 through v0.8.0 (latest tagged 2026-07-10, i.e. within days of this assessment). Single GitHub org (`agent-sandbox`), two repos (`agent-sandbox` + docs site `agent-sandbox.github.io`); no evidence of corporate/commercial backing or a managed hosted product. Contributor count could not be determined (GitHub contributors graph did not finish loading during research).
Sources:
- https://github.com/agent-sandbox/agent-sandbox/releases — "V0.8.0 ... Add: Leader election for the Sandbox Controller to support multi-instance (HA) deployment"

## B. Threat protection

### host_fs_damage
host_fs_damage: Yes — each sandbox is its own Kubernetes pod with its own container filesystem; the default Blueprint defines no `hostPath` volumes, so a sandboxed process cannot reach the underlying node's or a developer's host filesystem out of the box. Caveat: Blueprints are operator-editable Go templates and could add `hostPath` mounts, RBAC is broad (see credential_theft), and no `securityContext`/capability-dropping is applied by default, so container-breakout-to-node risk is bounded only by the default container runtime, not by explicit hardening.
Sources:
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/config/sandbox.yaml — default blueprint has no `securityContext`, `privileged`, `capabilities`, `NetworkPolicy`, `hostNetwork`, or `dnsPolicy` stanzas (confirmed absent by direct read)

### credential_theft
credential_theft: Partial — because execution is remote (K8s cluster, not the developer's laptop), host credentials (SSH keys, cloud CLI configs, dotfiles) are never automatically present in a sandbox; anything the sandbox sees must be explicitly passed via the `envVars` field or uploaded files. However, the control-plane-side ServiceAccount used by the platform itself has broad RBAC (create/update/patch/delete on pods, pods/exec, configmaps, services), and the Quick Start documents a literal default bearer token (`sys-<REDACTED-default-token, see quickstart>`) as the out-of-the-box credential — a shared-secret pattern that is a real operational risk if not rotated.
Sources:
- https://agent-sandbox.github.io/quickstart/ — "Default API key: `sys-<REDACTED-default-token, see quickstart>`"
- (install.yaml RBAC, read via WebFetch) — Role grants get/list/watch/create/update/patch/delete on pods, pods/exec, pods/log, configmaps, services, events, leases, replicasets

### data_exfiltration
data_exfiltration: No — see full Network control section below (axis C). Egress is unrestricted by default and the API's opt-in restriction fields (`allowOut`, `denyOut`, `allowInternetAccess`) are accepted by the E2B-compat API layer but, as of v0.8.0, are never read by the code path that builds the pod/ReplicaSet — confirmed by reading the conversion function and the internal `Sandbox`/`SandboxKube` structs, neither of which carries any network field.
Sources:
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/pkg/api/e2b/sandbox.go — `CreateSandbox` conversion copies `Template`, `Metadata`, `EnvVars`, `Timeout`, `AutoPause`/`AutoResume` from the request; no `Network`/`AllowOut`/`DenyOut`/`AllowInternetAccess` field is copied
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/pkg/sandbox/sandbox.go — internal `Sandbox`/`SandboxKube` structs (used to render the ReplicaSet) have no network-related fields at all

### malicious_execution
malicious_execution: Partial — blast radius is bounded by the standard Kubernetes pod/container boundary (own filesystem, own network namespace, CPU/mem limits — see resource_abuse), which contains crashes and resource exhaustion from hallucinated/malicious code. It does NOT add sandboxing beyond a default container (no seccomp/AppArmor/capability-dropping in the default blueprint, no default microVM/gVisor isolation — see escape_resistance), and network egress is unrestricted by default, so exfiltration or C2-callback blast radius is not contained.
Sources: (see host_fs_damage and escape_resistance sources)

### escape_resistance
escape_resistance: Partial — isolation boundary out of the box is a plain, shared-kernel Kubernetes pod on the cluster's default container runtime (typically containerd/runc); the default blueprint sets no `securityContext`, drops no capabilities, and applies no seccomp profile. The blueprint template does support an optional per-sandbox `runtimeClassName` (read from `Sandbox.Metadata.runtimeClassName`), which is the standard Kubernetes mechanism for plugging in a hardened runtime (gVisor/Kata) — but this requires the cluster operator to have already registered such a RuntimeClass, is not configured by default, and is not shipped/documented as a first-class hardening feature.
Sources:
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/config/sandbox.yaml — `{{ if index .Sandbox.Metadata "runtimeClassName" }} runtimeClassName: {{.Sandbox.Metadata.runtimeClassName}} {{ end }}`; no `securityContext` present anywhere in the file

### resource_abuse
resource_abuse: Yes — every sandbox has CPU/memory request AND limit fields with sane defaults (`cpu: 100m` request / `1000m` limit, `memory: 128Mi` request / `1024Mi` limit), rendered directly into the pod spec via the blueprint. A separate capacity-management layer (`pkg/config/capacity.go`, release-noted in v0.5.0) adds "global and per-token limits."
Sources:
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/pkg/sandbox/sandbox.go — `CPU string ... default:"100m"`, `Memory ... default:"128Mi"`, `CPULimit ... default:"1000m"`, `MemoryLimit ... default:"1024Mi"`
- https://github.com/agent-sandbox/agent-sandbox/releases — v0.5.0: "Capacity management with global and per-token limits"

## C. Feature set & granularity

### network_default_posture
network_default_posture: No (open-by-default, and the opt-in restriction is unimplemented) — a freshly created sandbox has full, unrestricted outbound network access. The install manifest deploys no `NetworkPolicy` for sandbox pods, the default blueprint has no network-restricting stanza, and standard Kubernetes pod networking is open-egress unless a NetworkPolicy says otherwise. The E2B-compat API's `allowInternetAccess` (default `true`) and `network{allowOut,denyOut,...}` fields exist in the request schema but are dropped during request→internal-struct conversion (see data_exfiltration sourcing) — they are accepted for E2B SDK compatibility, not enforced.
Sources:
- (install.yaml, read via WebFetch) — "NetworkPolicy Resources: Not present. No NetworkPolicy definitions are included, leaving network traffic unrestricted."
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/pkg/api/e2b/api/types.go — `AllowInternetAccess *bool` comment: "Internet access; false behaves like denyOut 0.0.0.0/0" (documented behavior, not wired to enforcement per sandbox.go trace above)

### egress_allowlist
egress_allowlist: No — the schema for it exists (`SandboxNetworkConfig{AllowOut []string, DenyOut []string, ...}`, described as CIDR/IP blocks, not domains) but is never consumed by the code that builds the sandbox pod (confirmed by reading `pkg/api/e2b/sandbox.go` conversion, `pkg/sandbox/sandbox.go`, `pkg/sandbox/rs_manager.go`, and the default blueprint — none reference these fields). Granularity, if it were implemented, would be CIDR/IP-list only per the field comments — no domain list, no subdomain wildcard, no port scoping, no path/method rules documented anywhere.
Sources:
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/pkg/api/e2b/api/types.go — `AllowOut *[]string` "Allowed CIDR blocks or IP addresses for egress traffic"; `DenyOut *[]string` "Denied CIDR blocks or IP addresses for egress traffic"
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/pkg/sandbox/rs_manager.go — `buildReplicaSet()` has no reference to any network field

### dns_level_blocking
dns_level_blocking: No — no CoreDNS customization, DNS proxy, or DNS-tier filtering component exists anywhere in the codebase (`pkg/` has no `dns` package); nothing in docs or source suggests unlisted domains fail resolution.
Sources:
- https://github.com/agent-sandbox/agent-sandbox/tree/main/pkg — package listing has no `dns`/`network`/`firewall`/`egress`/`proxy`/`security` package

### tls_mitm_inspection
tls_mitm_inspection: No — no TLS-terminating proxy or MITM component found; the only TLS-adjacent surface is the platform's own API server (`secure`/HTTPS-only mode for reaching the control plane), not an outbound-traffic interception layer.
Sources:
- https://agent-sandbox.github.io/api/ — `secure` field described only as "Enable HTTPS-only mode" for the sandbox's own exposed service, not outbound inspection

### http_path_rules
http_path_rules: No — no per-path allow/deny, method gating, or regex rule surface exists for egress traffic anywhere in the API or source. (Note: there IS an inbound-facing `allowPublicTraffic` flag gating whether a sandbox's own exposed URL requires auth — that is access control to the sandbox, not path-level egress control, and is out of scope for this criterion.)
Sources:
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/pkg/api/e2b/api/types.go — `AllowPublicTraffic *bool` "Sandbox URLs accessible only with authentication" (inbound access, not egress path rules)

### proto_coverage
proto_coverage: No — there is no protocol-aware egress control at all (no DNS, ICMP, TCP/UDP, QUIC/HTTP3, or L7 proto distinction in any enforcement path); the unimplemented `allowOut`/`denyOut` fields are CIDR/IP-only, which is not protocol-aware even on paper. No extensibility design for adding protocol rules was found.
Sources: (same as egress_allowlist/network_default_posture — no enforcement code path exists to have protocol coverage)

### live_rule_reload
live_rule_reload: NA — moot; there is no live enforcement mechanism to reload rules into. (Note: the Blueprint/Template config itself DOES hot-reload via ConfigMap without controller restart — that is a config-management capability, not a network-rule-reload capability, so it's tracked under config_depth instead.)
Sources:
- https://agent-sandbox.github.io/blueprint/ — "The blueprint stored in a ConfigMap supports hot-reloading without controller restarts" (general config reload, not network-specific)

### firewall_escape_hatch
firewall_escape_hatch: NA — no firewall/egress-control feature exists to have a break-glass/bypass mechanism for.

### enforcement_plane
enforcement_plane: No — none. Standard Kubernetes pod networking (CNI-level, whatever the cluster provides) is the only thing between a sandbox and the internet, and agent-sandbox itself installs no policy at that layer. An agent inside a sandbox has ordinary, unmediated outbound access; nothing is logged at a network-policy layer by this project (see network_audit).
Sources: (same as network_default_posture)

### fail_closed
fail_closed: NA — there is no network enforcement to fail open or closed; a controller crash has no bearing on network reachability either way since sandboxes were never network-restricted to begin with.

### network_audit
network_audit: No — no per-request egress log was found. The project's `Events`/`Logs` APIs (`GET /api/v1/events`, `GET /api/v1/logs/sandbox/{name}`) and `pkg/telemetry` cover sandbox lifecycle events and container stdout/stderr logs, not network-request-level audit trails.
Sources:
- https://agent-sandbox.github.io/api/ — Events: `GET /api/v1/events`; Logs: `GET /api/v1/logs/sandbox/{name}` (lifecycle/log endpoints, no per-request network log documented)

### workspace_modes
workspace_modes: Partial — no host-machine bind-mount mode exists (execution is remote — see execution_locality). The platform instead offers a "unified storage layer (NAS/OSS/S3)" pluggable into Blueprints as `volumeMounts`/`volumes`, giving persistent, shared, or ephemeral workspace options at the cluster-storage level, but this is an operator-configured Blueprint customization, not a simple per-run "bind vs snapshot" toggle exposed to the end user/agent.
Sources:
- https://agent-sandbox.github.io/blueprint/ — "Volumes: NAS/NFS mounts via `volumeMounts` and `volumes`"

### observability
observability: Yes — sandbox events (`GET /api/v1/events`), per-sandbox logs (`GET /api/v1/logs/sandbox/{name}`), a telemetry package (`pkg/telemetry`), and a web UI dashboard (added v0.6.0, "UI Dashboard for sandbox creation status overview") provide passive visibility into activity and status.
Sources:
- https://agent-sandbox.github.io/api/ — Logs/Events endpoints listed
- https://github.com/agent-sandbox/agent-sandbox/releases — v0.6.0: "UI Dashboard for sandbox creation status overview"; v0.7.0: "Added telemetry reporting for usage monitoring"

### supervision
supervision: Unknown — docs and source show lifecycle control (create/pause/resume/delete via API) and a `Controller`/`activator`/`scaler` internal architecture, but nothing describes an active behavioral-monitoring layer that observes in-sandbox agent actions and can independently intervene/contain (beyond an operator or the calling API explicitly issuing a delete/pause). Could not confirm or rule out a supervisory containment capability distinct from ordinary lifecycle API calls.
Sources:
- https://agent-sandbox.github.io/overview/ — lists "sandbox lifecycle management: create, list, connect, delete" without describing autonomous intervention

### fleet_mgmt
fleet_mgmt: Yes — native sandbox naming/listing (`GET /api/v1/sandbox`, `GET /e2b/v1/v2/sandboxes`), multi-tenant isolation ("system and regular users"), and template/pool management for allocating warm sandboxes across a fleet.
Sources:
- https://agent-sandbox.github.io/api/ — `GET /api/v1/sandbox` (list), `GET /e2b/v1/v2/sandboxes` (list)
- https://agent-sandbox.github.io/overview/ — "Multi-tenant access control with system and regular users"

### snapshots_persistence
snapshots_persistence: Partial — pause/resume is implemented by scaling the sandbox's ReplicaSet to 0 replicas and back to 1, combined with a "process snapshot" that captures only the envd-managed process list (command/config/tag) as a base64-encoded ReplicaSet annotation, then replays `process.Start` calls on resume. This is a lightweight process-roster restore, not a full state snapshot: it does NOT restore open file descriptors, network connections, in-memory process state, or working-directory context beyond what's in the captured config. Filesystem persistence across pause/resume depends entirely on whether the Blueprint attaches a persistent (NAS/OSS/S3) volume — ephemeral-storage sandboxes would lose disk state when the pod is descheduled at 0 replicas.
Sources:
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/pkg/sandbox/pause_resume.go — `Pause()` sets `rsCopy.Spec.Replicas = &replicas` (0) after `captureProcessSnapshot(sb)`; `Resume()` scales back to 1 and calls `restoreProcessSnapshot`
- https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/pkg/sandbox/process_snapshot.go — snapshot payload = `{CapturedTime, Processes []e2bapi.ProcessInfo}` captured via envd's `/process.Process/List`; restore replays `/process.Process/Start` per process
- https://github.com/agent-sandbox/agent-sandbox/releases — v0.7.0: "Introduced snapshot feature preserving commands on sandbox resumption"

## D. Setup

### setup
setup: Involved — Easy only if you already operate a Kubernetes cluster; a real barrier otherwise. Steps: create namespace, `kubectl apply -f install.yaml`, expose via Ingress or `kubectl port-forward`, verify with `/healthz`, then create a sandbox via the E2B Python SDK or a raw `curl` to `/e2b/v1/sandboxes` using the documented default bearer token. No installer provisions a cluster for you — "Kubernetes cluster v1.26+" is a stated prerequisite, not something the tool sets up. Documentation gives no time estimate.
Sources:
- https://agent-sandbox.github.io/quickstart/ — full command sequence and "Prerequisites: Kubernetes cluster (version 1.26 or higher), kubectl configured"

## E. Daily use

### daily_use
daily_use: Moderate — once the cluster-hosted platform is running, day-to-day interaction is lightweight: create/list/connect/exec/delete sandboxes via the E2B SDK, native REST API, an E2B-CLI-compatible CLI (`e2b sandbox list/create/connect/exec`), or MCP tools. No rebuild step is documented for code changes (workspace persistence depends on the Blueprint's storage config — see workspace_modes). Friction concentrates at the "operate a cluster" layer rather than per-session use.
Sources:
- https://agent-sandbox.github.io/cli/ — `e2b sandbox list|create|connect <id>|exec <id> <cmd>` with `--cwd`, `--user`, `-e`, `--background` flags
- https://agent-sandbox.github.io/api/ — Terminal/Files/Logs endpoints for interactive operation

## F. Configuration

### config_depth
config_depth: Deep for compute/image/scheduling, shallow-to-none for security/network. Declarative, versionable YAML/JSON: a `Template` (image, resources, static or regex-pattern-based dynamic image resolution, pool sizing) and a `Blueprint` (Go-template ReplicaSet spec — env vars, CPU/mem request+limit, command/args, volumes, `nodeSelector`/`tolerations`, `imagePullSecrets`, sidecar/`initContainers`, optional per-sandbox `runtimeClassName`), both stored in a ConfigMap with hot-reload. No config surface exists for network policy, security context, or egress rules (see axis C) — those exist only as unenforced API request fields, not as blueprint/template config.
Sources:
- https://agent-sandbox.github.io/blueprint/ — field table (Image, Port, EnvVars, CPU/Memory/Limits, Cmd/Args, Name) plus "Volumes... Node scheduling... Private registries... Sidecars"
- https://agent-sandbox.github.io/templates/ — static vs. dynamic (regex-pattern) template resolution

### policy_model
policy_model: Rigid for security/network, moderately policy-driven for compute/scheduling/image. There is no dial for "tighten or loosen egress per sandbox" that actually does anything (the API exposes the knob; nothing consumes it), and no default-secure posture to opt out of — the only posture is open. Compute (CPU/mem requests vs limits), scheduling (nodeSelector/tolerations), storage (volume type per Blueprint), and image source (static vs. dynamic template) ARE genuinely per-case configurable with sane defaults. Net effect: a user cannot dial security up or down within this tool — only Kubernetes-native controls added by the cluster operator outside agent-sandbox (e.g., a cluster-wide NetworkPolicy or a RuntimeClass) would do that.
Sources: (same as config_depth + axis C sources)

## G. DX — host↔sandbox integration

### bind_mount_sharing
bind_mount_sharing: No — there is no live host-filesystem bind mount; execution is remote (see execution_locality). Workspace persistence, where configured, goes through Blueprint-attached NAS/NFS/OSS/S3 volumes, not a live sync with the developer's local disk.
Sources: https://agent-sandbox.github.io/blueprint/ — volumes described as "NAS/NFS mounts"

### cred_forwarding
cred_forwarding: No — no ssh-agent, GPG, or git-credential forwarding mechanism found in docs, CLI, or source (`pkg/auth` contains only a flat bearer-token implementation, `token.go`). Any credentials a sandbox needs must be passed explicitly as `envVars` at sandbox-creation time.
Sources:
- https://github.com/agent-sandbox/agent-sandbox/tree/main/pkg/auth — single file `token.go`
- https://agent-sandbox.github.io/api/ — `envVars` is the only documented credential-injection path

### browser_auth
browser_auth: Unknown — no mention of a host-browser-open/OAuth-callback proxy mechanism was found in the CLI docs, API docs, or overview; docs are silent rather than explicitly ruling it out, and as a server-side remote platform (not a local CLI wrapper) this class of feature is architecturally less likely to exist, but absence of evidence is not confirmed absence per the guidelines.
Sources: https://agent-sandbox.github.io/cli/ — CLI docs cover only sandbox list/create/connect/exec, no auth-flow proxying mentioned

### shared_dirs
shared_dirs: Partial — additional NAS/NFS/OSS/S3 volumes can be attached via Blueprint `volumeMounts`/`volumes`, but this requires an operator to hand-edit the cluster-wide Blueprint template; it is not a self-service per-sandbox flag.
Sources: https://agent-sandbox.github.io/blueprint/ — "Volumes: NAS/NFS mounts via volumeMounts and volumes"

### git_worktrees
git_worktrees: Unknown — no mention anywhere in docs, README, CLI, or examples of git-worktree-aware handling. Docs silence, not confirmed absence.
Sources: (absence across all fetched docs/CLI/README pages)

### nested_containers
nested_containers: Unknown — no mention of a Docker socket opt-in, DinD, or any container-runtime-inside-the-sandbox capability in docs or the default Blueprint (which sets no privileged flag either way, since it sets no securityContext at all — see escape_resistance). Could not confirm or rule out.
Sources: (absence in config/sandbox.yaml and all docs pages fetched)

### harness_agnostic
harness_agnostic: Yes — the platform is a generic sandbox runtime, not tied to any one coding-agent CLI: it's reachable via the E2B SDK, a native REST API, an MCP server, and terminal/exec endpoints, so any agent or tool capable of speaking one of those protocols can drive it.
Sources: https://agent-sandbox.github.io/overview/ — "isolated, stateful, multi-tenant sandboxes for code execution, browser/computer tasks, and shell workflows" reachable via API/MCP/CLI/SDK

## H. Performance

### performance
performance: Unknown/lightweight-claimed — the only performance data found is a vendor release-note claim, not an independently verified benchmark: v0.5.0 added a "Fast startup mode achieving 1-3 second initialization." No disk footprint, RAM overhead, or bind-mount IO throughput figures were found anywhere in docs or release notes (moot for bind-mount IO since no host bind-mount mode exists).
Sources: https://github.com/agent-sandbox/agent-sandbox/releases — v0.5.0: "Fast startup mode achieving 1-3 second initialization" (vendor claim, unverified)

## I. Feasibility

### feasibility
feasibility: Involved, not solo-dev-adoptable-today without existing K8s access — Client tooling (kubectl, E2B SDK, CLI) is cross-platform, but the mandatory prerequisite — operating or having access to a Kubernetes cluster v1.26+ — is a real barrier for an individual developer who doesn't already run one; this is infrastructure aimed at teams/platform-engineering contexts, not a single-command local install. Maturity risk is real: v0.8.0, 172 stars, no independently-confirmed commercial backing, and (per direct source read) a documented API surface (network egress controls) that is not actually implemented yet — a sign of a fast-moving, pre-1.0 project where the docs/API-contract can outpace the implementation. No Windows/macOS/Linux distinction applies since it's a remote/cluster-side platform; the client side works from any OS with kubectl.
Sources: (aggregate of maturity, setup, and network_default_posture sources above)

## J. Price (prose-only)

### pricing
Free and open-source (Apache-2.0), self-hosted only. No managed/hosted cloud tier, no pricing page, and no commercial entity found associated with the org. Cost to the user is entirely the Kubernetes cluster infrastructure they must already operate or stand up themselves.
Sources: https://github.com/agent-sandbox — Apache-2.0 license, no pricing/cloud-offering links in the org's repos

## K. Extensibility

### extensibility
extensibility: Moderate — custom container images (any image, plus static or regex-pattern "dynamic templates" that resolve `templateID` strings to image references without pre-registration), fully swappable Blueprint (Go-template ReplicaSet spec) with hot-reload via ConfigMap, sidecar/`initContainers` injection, `imagePullSecrets` for private registries, `nodeSelector`/`tolerations` for scheduling, and an MCP-based "skills" directory in the repo (`skills/e2b-sandbox`, `skills/e2b-code-interpreter`) for packaging agent-facing tool definitions. All extensibility is at the infrastructure/template layer administered by whoever runs the cluster — there is no end-user-facing plugin/marketplace system.
Sources:
- https://agent-sandbox.github.io/templates/ — dynamic template regex example: `"faas-code-(?P<name>.+)\\.(?P<version>.+)$"` → `"ghcr.io/agent-sandbox/<name>:<version>"`
- https://agent-sandbox.github.io/blueprint/ — sidecars, imagePullSecrets, nodeSelector/tolerations, ConfigMap hot-reload
- https://github.com/agent-sandbox/agent-sandbox/tree/main/skills — `e2b-code-interpreter`, `e2b-sandbox`, `e2b` skill directories

## Unknowns & caveats
- **Network/egress control is the single most load-bearing finding of this assessment**: the E2B-compatible API documents `allowOut`/`denyOut`/`allowInternetAccess`/`network{...}` fields, but direct source inspection (request struct → conversion function → internal `Sandbox`/`SandboxKube` struct → blueprint template → rendered ReplicaSet YAML) shows these fields are parsed and then silently dropped — never enforced. This is confirmed absence (source-verified), not docs-silence, and is the primary reason the network axis is marked No/NA throughout. This is version-specific (v0.8.0, 2026-07-10) and could change in a future release; re-verify before citing as permanent.
- `git_worktrees`, `nested_containers`, `browser_auth` — genuine docs-silence; marked Unknown per guidelines rather than No.
- `supervision` — could not confirm or rule out an active-intervention layer distinct from ordinary lifecycle API calls (pause/delete).
- Contributor count / team size behind the org could not be determined (GitHub contributors graph did not finish loading).
- No blocked URLs — all fetches to github.com, raw.githubusercontent.com, and agent-sandbox.github.io succeeded.

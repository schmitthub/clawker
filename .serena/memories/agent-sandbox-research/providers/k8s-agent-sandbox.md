# Kubernetes agent-sandbox (kubernetes-sigs)
category: orchestration (self-hosted Kubernetes-native sandbox orchestration primitive; not a turnkey local dev tool)
Kubernetes CRD + controller for isolated, stateful, singleton pod workloads, with pluggable isolation runtimes (gVisor/Kata) | built on Kubernetes pods/CRDs | Apache-2.0 | official Kubernetes SIG Apps subproject, pre-1.0, ~3.2k GitHub stars

Provider identity note: this is the `kubernetes-sigs/agent-sandbox` project (agent-sandbox.sigs.k8s.io), distinct from the separate `agent-sandbox/agent-sandbox` GitHub org. All facts below are sourced from kubernetes-sigs/agent-sandbox and its official docs site unless marked GKE-specific or third-party.

## A. Identity
### built_on (prose-only)
Kubernetes CRD (`Sandbox`, plus extension CRDs `SandboxTemplate`, `SandboxClaim`, `SandboxWarmPool`) + controller following the standard Kubernetes controller/reconciliation pattern. Users declare a `Sandbox` custom resource; the controller manages an underlying pod with a stable hostname/network identity ("a lightweight, single-container VM experience"). Isolation strength is delegated to a pluggable container `runtimeClassName`: default is a normal (shared-kernel) container runtime, with gVisor (userspace-kernel syscall interception) or Kata Containers (per-sandbox VM/kernel via QEMU) as opt-in hardened runtimes. agent-sandbox itself is not a hypervisor/microVM technology — it is a lifecycle/orchestration layer on top of whatever isolation the cluster's node runtime provides.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox — "agent-sandbox enables easy management of isolated, stateful, singleton workloads... long-running, stateful, singleton container with a stable identity, much like a lightweight, single-container VM experience"
- https://agent-sandbox.sigs.k8s.io/docs/use-cases/gvisor-isolation/ — "a userspace kernel that intercepts application system calls to protect the host"
- https://agent-sandbox.sigs.k8s.io/docs/use-cases/kata-containers-isolation/ — "Kata Containers runs each sandbox in a lightweight virtual machine with its own dedicated kernel — there is no shared kernel between the host and the guest workload"

### execution_locality
Remote. Sandboxes always run as pods on a Kubernetes cluster (self-hosted or managed, e.g. GKE) — never as a local process on the developer's own machine (unless the dev machine is itself acting as the sole cluster node via kind/minikube, a degenerate case). Code and any credentials placed in the sandbox execute on cluster nodes; access is via SDK/kubectl over the network (port-forward, in-cluster tunnel, or the Sandbox Router component). Self-hosting your own cluster does not change this classification — it is still a separate managed deployment target, not local execution.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/getting_started/overview/ — install is `kubectl apply -f https://github.com/.../sandbox-with-extensions.yaml` against a cluster; access is via "its stable hostname" over the cluster network
- https://docs.cloud.google.com/kubernetes-engine/docs/how-to/agent-sandbox — dev access documented as "user credentials via `kubectl port-forward`" (GKE-specific doc, but confirms the general remote-access model)

### open_source (prose-only)
Apache-2.0, hosted at github.com/kubernetes-sigs/agent-sandbox under the official Kubernetes SIGs GitHub org (SIG Apps). Fully self-hostable on any conformant Kubernetes cluster.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox — license: Apache-2.0
- InfoQ (third-party) https://www.infoq.com/news/2026/05/gke-agent-sandbox-hypercluster/ — "any Kubernetes cluster can run Agent Sandbox, not just GKE"

### maturity (prose-only)
Pre-1.0: API has coexisting `v1alpha1`/`v1beta1` versions. ~3.2k GitHub stars, 410 forks, 15 releases, latest v0.5.2 (as of 2026-07-18). Official Kubernetes SIG Apps subproject launched at KubeCon NA 2025 — institutional backing beyond a single vendor. The Kubernetes project's own blog still frames it as "currently in development under SIG Apps." Google built a managed layer on top (GKE Agent Sandbox, announced at Google Cloud Next '26, claiming "300 sandboxes per second at sub-second latency"), and cites Lovable as a production adopter of GKE's sandboxing specifically. 111 open issues / 105 open PRs at time of research — active but sizable backlog.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox — 3.2k stars, 410 forks, 15 releases (latest v0.5.2), 111 open issues, 105 open PRs
- https://kubernetes.io/blog/2026/03/20/running-agents-on-kubernetes-with-agent-sandbox/ — "currently in development under SIG Apps"
- https://www.infoq.com/news/2026/05/gke-agent-sandbox-hypercluster/ — "300 sandboxes per second at sub-second latency"; Lovable co-founder: "GKE's cutting-edge sandboxing capabilities allow us to reliably scale to hundreds of secure sandboxes per second" (third-party report; Lovable's quote is about GKE's managed offering, not confirmed as the vanilla OSS controller)

## B. Threat protection
### host_fs_damage
Yes — the sandbox's filesystem is a pod filesystem/PVC scoped to that sandbox; the agent has no path to the underlying node/host filesystem beyond what the operator explicitly mounts. With gVisor/Kata enabled, the container process has no direct host-kernel filesystem-syscall path at all. Caveat: with the default (non-hardened) runtime and no `runtimeClassName` override, isolation is standard shared-kernel container isolation — same caveats as any container-based sandbox.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/use-cases/gvisor-isolation/ — "the container never directly touch[es] the host kernel" under gVisor
- https://agent-sandbox.sigs.k8s.io/ — "Strong Isolation: Supporting different runtimes...to provide enhanced security and isolation between the sandbox and the host, including both kernel and network isolation"

### credential_theft
Partial — in-cluster identity is mediated via Kubernetes ServiceAccount tokens + RBAC, can be scoped to a distinct KSA per sandbox, and `automountServiceAccountToken: false` is documented as a hardening option to keep the token out of the pod entirely. But there is no documented ssh-agent/GPG-agent style mediated forwarding for a developer's own git/signing credentials — external secrets (git tokens, API keys) are provided via standard Kubernetes Secrets mounted as env vars or files, i.e. copied into the pod, not proxied through a live agent socket.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/sandbox-ksa — "Each sandboxed pod can use a distinct KSA, allowing them to have distinct identities and permissions"
- https://agent-sandbox.sigs.k8s.io/docs/use-cases/anthropic-managed-agents/ — "`automountServiceAccountToken: false` keeps the Kubernetes service-account token out of the pod"; "the only Anthropic credential in the pod is the per-environment key"

### data_exfiltration
Partial — achievable but not out of the box. Core agent-sandbox ships no egress control of its own; it composes with whatever the cluster's CNI provides. Plain Kubernetes `NetworkPolicy` only matches IP CIDRs, not domains. The project's own documented example demonstrates domain-level allowlisting, but only via adopting Cilium as the CNI and authoring `CiliumNetworkPolicy` with `toFQDNs` — a CNI-specific dependency, not a controller-level feature. See Axis C for full granularity detail.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/demo-cilium-egress — "Upstream Kubernetes `NetworkPolicy` can only match IP CIDRs — not domains. Allowing `github.com` by name requires a CNI extension"

### malicious_execution
Yes — pluggable hardened isolation (gVisor/Kata) is explicitly positioned for "running untrusted code or multi-tenant scenarios," bounding the blast radius of hallucinated/malicious code or compromised packages to the userspace-kernel or per-sandbox-VM boundary. With the default runtime (no gVisor/Kata configured), blast radius is only standard container isolation.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/use-cases/kata-containers-isolation/ — "Ideal for multi-tenant environments or when running highly untrusted code"

### escape_resistance
Yes, opt-in — configurable isolation boundary stronger than a plain shared-kernel process/container when gVisor or Kata is explicitly enabled. gVisor: "even if a container escape vulnerability exists, the attacker only reaches the gVisor userspace kernel, not the host" (defense-in-depth framing, not a claim of impermeability). Kata: full per-sandbox VM with a dedicated kernel via QEMU, "hardware-level isolation." Caveat: this is opt-in via `runtimeClassName` — a bare/default `Sandbox` uses whatever the cluster's default container runtime is, typically plain runc (shared host kernel), unless the operator explicitly sets `gvisor` or `kata-qemu`.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/use-cases/gvisor-isolation/ — "the attacker only reaches the gVisor userspace kernel, not the host"
- https://agent-sandbox.sigs.k8s.io/docs/use-cases/kata-containers-isolation/ — "there is no shared kernel between the host and the guest workload"

### resource_abuse
Yes — `podTemplate` accepts a full standard Kubernetes PodSpec, so ordinary `resources.limits`/`requests` (CPU, memory, ephemeral-storage) apply exactly as with any Kubernetes workload. GKE's hardening guidance shows an explicit `memory: 1Gi` limit as part of a secure-by-default template.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/api/ — `podTemplate` (Required) "describes the pod spec that will be used to create an agent sandbox" (standard K8s PodSpec, which carries `resources`)
- https://docs.cloud.google.com/kubernetes-engine/docs/how-to/agent-sandbox — SandboxTemplate example enforcing `memory: 1Gi` limits (GKE-specific hardening doc, illustrates the mechanism)

## C. Feature set & granularity
### network_default_posture
No (open-by-default) for the core OSS project — a bare `Sandbox`, as shown in the official getting-started YAML, has no NetworkPolicy attached and gets standard, unrestricted Kubernetes pod networking. The project's own network-policy documentation frames NetworkPolicy as something you optionally add: "NetworkPolicy can be used to control who can connect to the Sandbox and limit the Sandbox outgoing connections to other pods or the internet" — opt-in, not shipped. Caveat: GKE's *managed* Agent Sandbox addon layers its own deny-by-default posture on top (ingress blocked except via Sandbox Router; egress open to internet minus RFC1918/metadata-server/CoreDNS) — but that is a GKE platform feature, not core OSS controller behavior.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/getting_started/overview/ — first-sandbox YAML has no network policy field at all
- https://agent-sandbox.sigs.k8s.io/docs/use-cases/examples/network-policies/ — "NetworkPolicy can be used to control who can connect to the Sandbox and limit the Sandbox outgoing connections to other pods or the internet"
- https://docs.cloud.google.com/kubernetes-engine/docs/how-to/agent-sandbox — GKE-specific: "Ingress is blocked from all sources except the designated Sandbox Router. Egress is allowed to the public internet, but egress to private LAN ranges (RFC 1918)... is explicitly blocked"

### egress_allowlist
Partial — domain/FQDN-level allowlisting with port scoping is documented and demonstrated with working YAML, but only via adopting Cilium as the cluster's CNI and hand-authoring `CiliumNetworkPolicy` (`toFQDNs`). Plain upstream `NetworkPolicy` (works with any CNI) is CIDR-only — no domain matching. So the granularity ladder tops out at domain+port, but it is a CNI-specific dependency (not built into agent-sandbox's own API/controller), and each identity/team needs its own explicit policy (demonstrated with per-team allow rules, e.g. team-a allowed `github.com:443`, team-b denied by default).
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/demo-cilium-egress — "This demo uses real `CiliumNetworkPolicy` with `toFQDNs`"; "Allowing `github.com:443` for team-a only... team-b has no such rule → its github traffic is dropped"

### dns_level_blocking
Partial — the Cilium example enables an L7 DNS proxy so `toFQDNs` rules can resolve allowed names ("`10-cilium-allow-dns.yaml` must be applied or even the allowed clone fails"), and once any egress rule selects an endpoint it becomes default-deny egress. But enforcement for non-allowlisted domains is by blocking the post-resolution IP connection, not by failing the DNS query itself (NXDOMAIN-style) — different mechanism from a DNS-resolver-level block.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/demo-cilium-egress — "In Cilium, once any egress rule selects an endpoint it becomes default-deny egress — so this both (a) locks egress down to DNS only and (b) enables the L7 DNS proxy that `toFQDNs` needs to resolve domains"

### tls_mitm_inspection
No — no TLS interception or L7 HTTPS content inspection is documented anywhere in the project. The one documented network-control mechanism (Cilium `toFQDNs`) enforces at the DNS+IP layer, not by decrypting/inspecting HTTPS traffic.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/demo-cilium-egress — mechanism described is DNS-snoop + IP-based enforcement, no TLS/certificate handling mentioned

### http_path_rules
Unknown — not demonstrated in any agent-sandbox doc or example found. Cilium (the CNI used in the one documented egress example) has its own general L7 HTTP policy capability, but agent-sandbox's own docs never show path- or method-level rules, so crediting this would mean citing Cilium's general capability rather than a demonstrated agent-sandbox pattern.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/demo-cilium-egress — policy shown scopes to `toFQDNs` + port only, no path/method fields present

### proto_coverage
Partial — the only documented network-control example scopes to HTTPS:443 plus DNS. No ICMP, UDP/QUIC, SSH, WebSocket, gRPC coverage documented anywhere in agent-sandbox's own docs, and no statement about protocol extensibility (fixed vs. pluggable) was found.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/demo-cilium-egress — "Allowing `github.com:443`" and DNS allow rule are the only protocols/ports shown

### live_rule_reload
Yes — inherent to Kubernetes `NetworkPolicy`/`CiliumNetworkPolicy` semantics: the CNI agent watches policy objects and enforces changes continuously; applying/editing a policy does not require recreating the Sandbox pod. This is standard Kubernetes networking architecture rather than an agent-sandbox-specific feature, and no agent-sandbox doc makes this claim explicitly for its own workflow — flagged as architectural inference rather than a doc-confirmed statement.
Sources:
- (self-evident from standard Kubernetes/Cilium NetworkPolicy reconciliation architecture; no agent-sandbox-specific doc quote found making this claim explicitly)

### firewall_escape_hatch
No — no timed-bypass or per-sandbox disable/re-enable mechanism is documented. The only lever shown is manually editing or deleting the `NetworkPolicy`/`CiliumNetworkPolicy` object, which is an all-or-nothing manual policy change with no automatic re-enforcement — matches the guideline's "all-or-nothing = No" bar.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/demo-cilium-egress — only mechanism shown is applying/removing policy YAML files by hand, no bypass/timer construct

### enforcement_plane
Determination (prose, not binary): entirely delegated — agent-sandbox itself has no enforcement plane of its own; it inherits whatever the cluster's CNI provides. In the one documented pattern (Cilium), enforcement is kernel-level eBPF. With a non-Cilium CNI limited to plain `NetworkPolicy`, the underlying mechanism (iptables/nftables/other eBPF) depends entirely on that CNI's own implementation and is undocumented by agent-sandbox. An unprivileged pod cannot trivially route around node-level CNI/eBPF enforcement, same as any other Kubernetes workload on that cluster.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/demo-cilium-egress — "enables the L7 DNS proxy" / Cilium eBPF dataplane implied by CiliumNetworkPolicy usage

### fail_closed
Unknown — not documented for agent-sandbox's own controller or for the CNI enforcement layer it depends on. Structurally, CNI-level enforcement (e.g. Cilium eBPF programs) is a node-level component logically independent of the agent-sandbox controller process, so it plausibly survives an agent-sandbox controller crash — but this is architectural inference, not a doc-confirmed claim; no source found explicitly addresses "what happens to network policy enforcement if the agent-sandbox controller dies."
Sources:
- (no source found; docs silent on controller-crash behavior for network enforcement)

### network_audit
Partial — a roadmap item ("Detailed Logs Falco Configuration Extension" for gVisor auditing) is planned but not shipped. The Cilium egress example instructs troubleshooting via "Hubble" (Cilium's flow-observability tool), which does give per-flow visibility, but that is Cilium's own tool, not something agent-sandbox ships or documents as a first-class, built-in per-request egress log.
Sources:
- https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/main/roadmap.md — "Detailed Logs Falco Configuration Extension" for gVisor auditing capabilities (planned, not shipped)
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/demo-cilium-egress — "check Hubble" for troubleshooting denied traffic

### workspace_modes
Partial — no "live host bind mount" concept exists at all (execution is always remote/in-cluster, so there is no local host filesystem to bind-mount from). Persistent storage is via PVCs (`volumeClaimTemplates`, FUSE CSI). Sandbox filesystem is either ephemeral (container layer, wiped on pod deletion) or PVC-backed and survives pod recreation/suspend-resume. This doesn't map onto the bind-mount-vs-snapshot dichotomy the criterion assumes for local tools.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/volumes/ — `volumeClaimTemplates` in `SandboxTemplate`; FUSE CSI volume support
- https://agent-sandbox.sigs.k8s.io/docs/sandbox/snapshots/ — PVC-backed suspend/resume preserving "filesystem changes and memory state"

### observability
Yes — OpenTelemetry integration is documented for the client SDK (traces/metrics via `OTEL_TRACES_EXPORTER`/`OTEL_METRICS_EXPORTER` env vars, `opentelemetry-instrument` wrapper), plus a dedicated per-sandbox "Metrics" doc page, and standard Kubernetes-level observability (kubectl, any cluster monitoring/logging stack) applies to Sandbox pods like any other workload.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/sandbox/metrics/ — "The `k8s_agent_sandbox` SDK integrates seamlessly with OpenTelemetry (OTel)"; "rich metric and trace data directly in your local console for rapid debugging"

### supervision
Partial — the Kubernetes controller pattern provides an active reconciliation loop that can enforce lifecycle policy (e.g. `shutdownTime`/`shutdownPolicy` TTL-based deletion) and roadmap items point toward deeper containment (dynamic network-policy attach at claim time, Falco-based gVisor auditing). But no documented capability to observe an agent's live behavior mid-session and intervene (kill/quarantine based on observed actions, analogous to a security supervisor watching for compromise) was found — the controller supervises pod lifecycle/identity, not agent behavior.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/api/ — `shutdownTime`/`shutdownPolicy` fields (lifecycle enforcement only)
- https://raw.githubusercontent.com/kubernetes-sigs/agent-sandbox/main/roadmap.md — "Network Policy 'Attach' at Claim Time" and Falco auditing are roadmap (planned), not shipped

### fleet_mgmt
Yes — `SandboxWarmPool` (pre-warmed pool of pods for fast allocation), `SandboxClaim` (claim/bind a pod from a pool, abstracting the underlying Sandbox), and `SandboxTemplate` (reusable Sandbox definitions) together give first-class multi-agent fleet primitives, plus a stable per-sandbox hostname/network identity for agent-to-agent discovery.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox — `SandboxWarmPool` "Manages a pool of pre-warmed Sandboxes that can be quickly allocated to users"; `SandboxClaim` enables "Sandboxes from a SandboxWarmPool, abstracting away the details of the underlying Sandbox configuration"
- https://kubernetes.io/blog/2026/03/20/running-agents-on-kubernetes-with-agent-sandbox/ — "Every Sandbox is given a stable hostname and network identity, allowing distinct agents to discover and communicate with each other"

### snapshots_persistence
Yes, with caveats — PVC-based suspend/resume is shipped: `suspend()` "takes a snapshot and sets the Sandbox's operatingMode to Suspended," preserving "filesystem changes and memory state," and `resume()` "automatically restores the latest state." Caveats: documented as "Python-only for now" (the Go SDK lacks suspend/resume/snapshot support), and "a sandbox can only be restored from its own previous snapshots" — no cross-sandbox snapshot cloning documented.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/sandbox/snapshots/ — "allows you to manually 'freeze' a gVisor-protected sandbox and restore that state upon resuming"; "Snapshot/suspend/resume support is Python-only for now"; "a sandbox can only be restored from its own previous snapshots"

## D. Setup (spectrum)
setup: Involved — the project provides no cluster of its own; a working Kubernetes cluster is an assumed prerequisite. Given a cluster, install is a single `kubectl apply -f <release-manifest-url>` and the first Sandbox is a ~10-line YAML `kubectl apply`, both genuinely simple. But "have a Kubernetes cluster" is a materially higher bar than a Docker-only local tool, and getting real isolation (gVisor/Kata) additionally requires cluster nodes provisioned with those container runtimes/`runtimeClass`es, which agent-sandbox's own docs do not walk through for a generic cluster.
2-4 sentences expanding: what exists, granularity, ALL caveats/limits. Factual, no marketing language.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/getting_started/overview/ — `kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${VERSION}/sandbox-with-extensions.yaml`; first-Sandbox YAML shown

## E. Daily use (spectrum)
daily_use: Moderate — Go/Python SDKs and a Sandbox Router component abstract raw `kubectl exec`/port-forward for exec, file, and metrics access ("interacting with the sandbox filesystem...without needing `kubectl exec`"). There's no rebuild-on-code-change friction for the sandbox itself (that's scoped to the user's own container image). But day-to-day operation means working against a cluster via kubectl/SDK rather than a single local CLI wrapper — more operational overhead than a `docker run`-style tool for a solo developer, appropriate friction for a team already operating Kubernetes.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/filesystem/ — "read and write files, list directories, check if paths exist, and upload or download data — all through the SDK"; "interacting with the sandbox filesystem...without needing `kubectl exec`"

## F. Configuration
### config_depth
Partial — `podTemplate` exposes the full depth of a standard Kubernetes PodSpec (image, env, resources, volumes, service accounts, security context, `runtimeClassName`), which is genuinely deep, versionable (YAML/GitOps-friendly), and has real escape hatches (arbitrary PodSpec fields). But there is no coding-agent-specific declarative config surface (no equivalent of packaged build instructions, dependency injection points, or named lifecycle hooks) — "Custom Environment" and "Image" doc pages exist but their detailed content (beyond a nav listing) could not be retrieved in this research pass.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/api/ — `podTemplate` (Required): "describes the pod spec that will be used to create an agent sandbox"
- https://agent-sandbox.sigs.k8s.io/docs/sandbox/custom_sandbox/ — "Create a Sandbox with custom environment variables"; "Create a Sandbox with custom dependencies" (nav-level only, sub-pages not retrieved)

### policy_model
policy_model: moderate, composable-but-unbundled — Kubernetes-layer policy composition is well documented (RBAC, NetworkPolicy, PodDisruptionBudget, and admission-control examples for OPA Gatekeeper, Kyverno, and ValidatingAdmissionPolicy all appear as first-class example directories), and a "Secure Sandbox Admission Policy (VAP)" example explicitly "enforces secure-by-default posture for all sandbox workloads." But none of this is bundled or opinionated by agent-sandbox itself — the operator must assemble and choose the policy stack (which CNI, which admission controller, which `runtimeClass`) rather than getting per-sandbox dial-able controls (e.g. "bind vs. copy," "tighten or bypass firewall for this run") natively from the Sandbox API.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/policy — subdirectories: kyverno, network-policy-management, opa-gatekeeper, policy-controller, vap
- https://agent-sandbox.sigs.k8s.io/docs/use-cases/examples/ — "Secure Sandbox Admission Policy (VAP)": "enforces secure-by-default posture for all sandbox workloads"

## G. DX
### bind_mount_sharing
No — there is no live host↔sandbox bind mount; execution is remote (a Kubernetes pod), so a local "host" doesn't exist in the sense this criterion assumes. Data movement is via explicit SDK calls (`files.read`/`files.write`/upload/download) or PVCs, not a live filesystem mirror.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/filesystem/ — filesystem access modeled as discrete `read`/`write`/`list`/`exists`/upload/download SDK calls, not a mount

### cred_forwarding
No — (corrected 2026-07-18, attribution audit) the in-cluster identity documented (Kubernetes ServiceAccount tokens + RBAC, distinct KSA per pod, Workload Identity Federation for GCP) mediates the *sandbox's own* access to cluster/cloud resources — it does not forward a developer's own git/ssh/gpg credentials into the pod, so it doesn't satisfy the cred_forwarding sharp test. The mechanism actually documented for a developer's own credentials is copied Kubernetes Secrets (env vars/files), which the rule explicitly excludes as "creds you pass yourself," not forwarding.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/sandbox-ksa — distinct KSA per pod, verified via `/var/run/secrets/kubernetes.io/serviceaccount/token`
- https://docs.cloud.google.com/kubernetes-engine/docs/how-to/agent-sandbox — "use an IAM policy with Workload Identity Federation for GKE" (GKE-specific)

### browser_auth
Unknown — no mechanism for proxying a sandboxed process's browser-open/OAuth callback back to the developer's local browser was found in any fetched doc (SDK docs, filesystem docs, Sandbox Router docs). Docs are silent on this rather than confirming its absence.
Sources:
- (no source found; searched Python/Go client and Sandbox Router documentation, no browser-auth-proxy mechanism mentioned)

### shared_dirs
Partial — `volumeClaimTemplates` and FUSE CSI let an operator attach additional PVC-backed or FUSE-backed volumes beyond the primary workspace, but these are Kubernetes-native volumes, not arbitrary host directories (no local "host" exists to share from in this remote-execution model).
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/volumes/ — "use `volumeClaimTemplates` in `SandboxTemplate`"; "Use Volumes with `Agent Sandbox` and with `FUSE CSI`"

### git_worktrees
No — no worktree concept anywhere in the CRD/SDK surface; git operations are entirely the responsibility of whatever tooling runs inside the sandbox's own container image. This is architecturally out of scope for a generic Kubernetes workload-orchestration primitive.
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/api/ — SandboxSpec fields (podTemplate, volumeClaimTemplates, shutdownTime, shutdownPolicy, replicas, service, operatingMode) contain no git/worktree-related field

### nested_containers
Unknown — no documentation found addressing whether a container runtime (Docker socket, DinD, sysbox-style nesting) is available or supported inside a Sandbox pod. Docs are silent.
Sources:
- (no source found; searched project docs and web for Docker-socket/DinD/privileged-mode mentions specific to agent-sandbox, none found)

### harness_agnostic
Yes — `podTemplate.spec.containers[].image` accepts any container image, so any coding-agent CLI/harness can be packaged and run inside a Sandbox; nothing in the core API is tied to a specific vendor's agent. (The "Anthropic Managed Agents" use case is one first-class documented integration among several — Code Execution, Coding Agents, Computer Use, CI/CD, OpenClaw — not exclusivity.)
Sources:
- https://agent-sandbox.sigs.k8s.io/docs/getting_started/overview/ — `podTemplate.spec.containers[].image: <IMAGE>` — arbitrary user-supplied image
- https://agent-sandbox.sigs.k8s.io/docs/use-cases/ — multiple independent, vendor-neutral use cases listed (Code Execution, Coding Agents, Computer Use, CI/CD, OpenClaw, Anthropic Managed Agents, gVisor/Kata isolation)

## H. Performance (spectrum)
performance: Heavier / cluster-dependent, no neutral benchmark found — no vendor-neutral latency/footprint numbers exist for the OSS controller itself. `SandboxWarmPool` exists specifically because cold-starting a hardened (gVisor/Kata) pod is nontrivial ("pre-warms a pool of pods...so it can be assigned in milliseconds rather than waiting for a cold pod to schedule and start"). GKE's managed offering claims "300 sandboxes per second at sub-second latency" and "up to 30% better price-performance when running on Axion" — these are GKE/Axion-specific vendor benchmarks, not representative of a generic self-hosted cluster. No local bind-mount-style IO benchmarks apply (no local bind mount exists).
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox — `SandboxWarmPool`: "Manages a pool of pre-warmed Sandboxes that can be quickly allocated to users"
- https://www.infoq.com/news/2026/05/gke-agent-sandbox-hypercluster/ — vendor benchmark: "300 sandboxes per second at sub-second latency"; "up to 30% better price-performance when running on Axion" (GKE-specific)

## I. Feasibility (spectrum)
feasibility: Involved — requires an existing, real Kubernetes cluster (self-managed or a managed offering like GKE); not adoptable by a solo developer in the way a Docker-only local CLI is, without either standing up a cluster or paying for a managed one. API is pre-1.0 (coexisting v1alpha1/v1beta1), and the Kubernetes project's own blog describes it as "currently in development." Strong institutional backing (official SIG Apps subproject, a Google-built managed layer, at least one named production adopter) offsets some single-vendor-abandonment risk relative to an independent side project, but this is not a same-day install for an individual developer.
Sources:
- https://kubernetes.io/blog/2026/03/20/running-agents-on-kubernetes-with-agent-sandbox/ — "currently in development under SIG Apps"
- https://agent-sandbox.sigs.k8s.io/docs/api/ — coexisting `agents.x-k8s.io/v1alpha1` and `v1beta1` API versions

## J. Price (prose-only)
Core project: free, Apache-2.0, self-hosted — cost is whatever the underlying Kubernetes cluster costs (compute/storage); no agent-sandbox-specific licensing fee found anywhere in the docs or repo. Google offers a managed "GKE Agent Sandbox" addon on top of standard GKE; exact incremental pricing for that addon beyond standard GKE compute/node pricing was not found in the fetched docs.
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox — Apache-2.0, no pricing/licensing terms beyond the license itself
- https://docs.cloud.google.com/kubernetes-engine/docs/how-to/agent-sandbox — describes the "GKE Agent Sandbox addon" without a dedicated incremental price line in the fetched content

## K. Extensibility
Yes — CRD-based extension model (`SandboxTemplate`, `SandboxClaim`, `SandboxWarmPool`) layered on the core `Sandbox` CRD; documented composition with third-party policy/admission tooling (OPA Gatekeeper, Kyverno, ValidatingAdmissionPolicy, PodDisruptionBudget); pluggable isolation runtime via standard Kubernetes `runtimeClassName` (gVisor, Kata, or others a cluster supports); Go and Python SDKs for programmatic control; and an explicit project design stance to "encourage applications and agents to programmatically consume the Sandbox API."
Sources:
- https://github.com/kubernetes-sigs/agent-sandbox — `SandboxTemplate`, `SandboxClaim`, `SandboxWarmPool` extension CRDs; "encourages applications and agents to programmatically consume the Sandbox API"
- https://github.com/kubernetes-sigs/agent-sandbox/tree/main/examples/policy — kyverno, opa-gatekeeper, vap, policy-controller subdirectories

## Unknowns & caveats
- **browser_auth, nested_containers, fail_closed (controller-crash behavior), http_path_rules, proto_coverage beyond HTTPS/DNS, exact GKE-addon incremental pricing, and detailed `custom_environment`/`image` config-hook semantics**: all docs-silent, not confirmed absent — marked Unknown/Partial per the "silence ≠ No" rule rather than assumed negative.
- **Go SDK / Python client doc pages** (`/docs/go_client/`, `/docs/python_sdk/` and related) returned 404 or failed to fetch on this pass despite being listed in the site nav; equivalent facts were recovered via the overview, filesystem, snapshots, and metrics pages instead, but the Go/Python SDK pages' full content was not directly reviewed. Not a firewall/network block — these were wrong-guessed or currently-unresolvable doc paths on the live site, worth a follow-up pass with correct URLs.
- **GKE-specific facts are explicitly flagged inline throughout** (default-deny network posture, Workload Identity Federation, `memory: 1Gi` hardening example, 300 sandboxes/sec benchmark) and should not be read as core OSS-project defaults — they describe Google's managed layer built on top of the OSS controller.
- No URLs were blocked by network/firewall failures (NXDOMAIN/connection-refused) during this research pass; all gaps above are either doc-site 404s from incorrect guessed paths or genuine documentation silence on the topic.
- **network_audit and supervision** rely partly on roadmap items (Falco auditing extension, dynamic network-policy attach at claim time) that are explicitly planned/not-yet-shipped per `roadmap.md` — treated as absent-today, noted as directionally planned.

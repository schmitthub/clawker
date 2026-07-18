# Northflank Sandboxes
category: cloud
Managed/BYOC cloud platform offering microVM-isolated ephemeral or persistent compute for untrusted code, AI agents, and CI/CD | built on Kata Containers (Cloud Hypervisor) primary, gVisor fallback | proprietary (not open source), BYOC deployment option | venture-backed (Bain Capital, MongoDB Ventures, angel investors from Docker/Datadog/Snyk), SOC 2 Type 2 + HIPAA compliant, "millions of microVMs monthly since 2021"

as-of: 2026-07-18

## A. Identity
### built_on (prose-only)
Northflank sandboxes are microVM-backed workloads. Primary isolation is **Kata Containers with Cloud Hypervisor** — each workload gets a dedicated Linux guest kernel enforced by hardware virtualization (KVM). Where nested virtualization is unavailable (e.g. some GPU nodes/cloud regions), Northflank falls back to **gVisor** for syscall-level (user-space kernel) isolation. Firecracker is also referenced as an available VMM option in vendor blog content. Control plane runs on Kubernetes (Cilium as CNI/network-policy engine); deployable as Northflank-managed multi-tenant PaaS or Bring-Your-Own-Cloud (BYOC) on the customer's own AWS/GCP/Azure/Oracle/Civo/CoreWeave/bare-metal cluster.
Sources:
- https://northflank.com/product/sandboxes — "MicroVMs with Kata Containers or gVisor" / "VM-level isolation keeps malicious code away from host systems and other tenants"
- https://northflank.com/security — "Kata Containers used as default runtime, applying KVM and micro-VMs for workload isolation from host kernel" / "gVisor available as alternative when nested virtualization unavailable"
- https://northflank.com/blog/best-sandboxes-for-coding-agents — "Kata Containers with Cloud Hypervisor, Firecracker, and gVisor" applied per workload threat model

**Provider-note verdict**: Claim of Kata/gVisor microVMs is CONFIRMED via two directly-fetched official pages (product page + security page), consistent with a directly-fetched vendor blog post.

### execution_locality
execution_locality: Remote — sandboxes run either on Northflank's managed multi-tenant cloud (GCP-hosted Kubernetes) or on a customer-provisioned BYOC cluster in the customer's own cloud account. Both are the platform's servers/cloud infra, never the developer's own machine; BYOC changes *whose* cloud account the workload runs in (and keeps data inside that VPC) but does not make execution local. No local/on-machine execution mode is documented.
Sources:
- https://northflank.com/docs/v1/application/sandboxes/deploy-sandboxes-in-your-cloud — "Deploy sandboxes with microVM isolation in your own cloud account"
- https://northflank.com/security — "Workloads run on customer's own cloud account, VPC, and Kubernetes cluster" (BYOC)

### open_source (prose-only)
Northflank the platform is proprietary — no self-hostable open-source control plane is documented or advertised. BYOC lets workloads execute inside the customer's own cloud account/VPC (data residency benefit) but the orchestration/control plane remains Northflank's managed software; there is no offline/self-hosted Northflank binary.
Sources:
- https://northflank.com/pricing — no open-source/self-host edition advertised; BYOC framed as "connect your cloud provider," not "run Northflank yourself"

### maturity (prose-only)
Company operating "since 2021" per product-page claim ("Millions of microVMs monthly since 2021"). Venture-backed: Bain Capital, Vertex Ventures, Kindred Ventures, Pebblebed, Tapestry, Uncorrelated, MongoDB Ventures, Expa, Stride VC, The Family, plus angel investors from Docker, Datadog, Snyk, Databricks, Aiven, GitHub. SOC 2 Type 2 and HIPAA compliant; "no security breaches since April 1, 2019" claimed on security page. No public customer count, revenue, or named-customer case-study detail was found in the pages fetched.
Sources:
- https://northflank.com/product/sandboxes — "Millions of microVMs monthly since 2021"
- https://northflank.com/security — SOC 2 Type 2, HIPAA, "no security breaches since April 1, 2019"
- https://northflank.com/about — funding/investor list

## B. Threat protection
### host_fs_damage
host_fs_damage: Yes — each sandbox runs in its own microVM (Kata: dedicated guest kernel via KVM; gVisor: syscall interception), structurally separating it from the underlying node's host filesystem and from other tenants.
Isolation is VM-level, not shared-kernel-container-level, so a compromised sandbox cannot directly reach host files or sibling workloads' files without a hypervisor/kernel escape.
Sources:
- https://northflank.com/product/sandboxes — "VM-level isolation keeps malicious code away from host systems and other tenants"
- https://northflank.com/docs/v1/application/sandboxes/deploy-sandboxes-on-northflank — "Boot times under 1 second with VM-level isolation for running untrusted code"

### credential_theft
credential_theft: Partial — secrets/credentials are explicitly injected into a sandbox via environment variables (`runtimeEnvironment`) or Northflank secret groups (with a beta external-secret-manager option that avoids storing the secret value in Northflank's own database); nothing is automatically inherited from a developer's host machine since execution is remote. However, once injected, a secret is available in full to any code running in the sandbox (no documented per-process/per-tool scoping or credential-forwarding mediation, e.g. no ssh-agent-style proxying).
Because execution is remote, "host dotfiles/SSH keys" in the local-tool sense don't apply — the exposure surface is whatever the developer chooses to inject, in full.
Sources:
- https://northflank.com/docs/v1/application/sandboxes/sandboxes-on-northflank — secrets injected via `runtimeEnvironment` object at service creation
- https://northflank.com/security — "Secret injection prevents storing secrets in Kubernetes"; external secret managers supported (beta)

### data_exfiltration
data_exfiltration: Partial — see axis C network criteria below for full detail. Outbound network restriction (Network Policies) exists only for BYOC clusters, is off/open by default until a rule is added, and is scoped to IP/CIDR/FQDN/hostname destinations without documented port or protocol granularity. Northflank's own managed multi-tenant cloud has no documented sandbox-specific egress control at all.
Sources:
- https://northflank.com/docs/v1/application/network/configure-network-policies — "If no rules are defined, all traffic is allowed in that direction" (BYOC-only feature)

### malicious_execution
malicious_execution: Yes — blast radius of untrusted/hallucinated code is contained by the microVM boundary (dedicated guest kernel or gVisor Sentry), the stated purpose of the product ("ideal for running untrusted code like LLM-generated code, user-submitted code, AI agents, and CI/CD pipelines").
Sources:
- https://northflank.com/product/sandboxes — "Built for executing untrusted code safely"
- https://northflank.com/blog/best-sandboxes-for-coding-agents — Kata/gVisor "applied based on individual workload threat models"

### escape_resistance
escape_resistance: prose-only per guidelines, but stated plainly — isolation boundary is stronger than a plain shared-kernel container. Kata Containers gives each sandbox a dedicated Linux guest kernel with hardware-virtualization enforcement (KVM) — a VM-level boundary, the strongest tier short of bare-metal-per-tenant. gVisor (used as fallback when nested virtualization is unavailable) is a weaker but still meaningful syscall-interception boundary (a user-space kernel/Sentry mediates syscalls rather than passing them to the host kernel), commonly used for GPU nodes on Northflank's own cloud. No public escape-CVE history for Northflank's deployment specifically was found in the pages reviewed (out of scope given WebSearch budget exhaustion this run).
Sources:
- https://northflank.com/security — "Each Kata workload gets its own dedicated Linux guest kernel, enforced by hardware virtualisation via KVM" (paraphrase of fetched content); gVisor as fallback
- https://northflank.com/blog/kata-containers-vs-gvisor — comparative architecture (title/topic only; not independently re-fetched with verbatim quote this run — treat as directionally consistent with the security page)

### resource_abuse
resource_abuse: Yes — CPU/memory/GPU are bundled and enforced via `deploymentPlan` (e.g. `nf-compute-200` = 2 vCPU/4GB RAM), with horizontal autoscaling and bin-packing; GPU plans similarly bundle allocation. Enforcement is at the Kubernetes/VM resource-request layer typical of the platform.
Sources:
- https://northflank.com/docs/v1/application/sandboxes/sandboxes-on-northflank — `deploymentPlan` CPU/memory bundling
- https://northflank.com/product/sandboxes — "Horizontal autoscaling with intelligent bin-packing"

## C. Feature set & granularity

### network_default_posture
network_default_posture: No — default is open/permissive, not deny-by-default. On Northflank's managed multi-tenant cloud, no sandbox-specific egress control is documented at all (traffic appears unrestricted by default). On BYOC, the dedicated Network Policies feature is explicitly permissive until configured: "if no rules are defined, all traffic is allowed in that direction" — only once a rule is added does that direction (ingress or egress) become allowlist-only.
Deny-by-default egress is achievable only by a developer/operator opting in to Network Policies, and only on a BYOC cluster — never the platform default.
Sources:
- https://northflank.com/docs/v1/application/network/configure-network-policies — "If no ingress rules exist, all inbound traffic from resources in the same project is allowed" (egress default stated symmetrically as permissive-until-ruled)

### egress_allowlist
egress_allowlist: Partial — capability exists but is BYOC-cluster-only (not available on Northflank's managed cloud). Granularity: destination-based only — "IP addresses, CIDR ranges, FQDNs, or hostnames" and workload/project tags. No documented port-level or protocol-level scoping specific to egress rules, no path/method/regex rules for egress (path-based rules elsewhere in the docs are ingress-only — see http_path_rules below).
A companion feature, static egress IPs (also BYOC), gives a sandbox a fixed outbound IP for third parties to allowlist — this helps *identify* the sandbox's traffic to external services but is not itself a control on what the sandbox can reach.
Sources:
- https://northflank.com/docs/v1/application/network/configure-network-policies — egress destinations = "IP/CIDR/FQDN"
- https://northflank.com/docs/v1/application/bring-your-own-cloud/configure-static-egress-ips — "All egress traffic from pods in the private subnet will route through the NAT gateway using your static Elastic IP"

### dns_level_blocking
dns_level_blocking: Unknown — no documentation found describing DNS-layer enforcement (blocking resolution of non-allowlisted domains) for either managed-cloud or BYOC sandboxes. Network Policies allow FQDN/hostname as an egress-rule target type, which implies some DNS-aware routing/matching under the hood, but the docs never state the enforcement mechanism (DNS-response blocking vs. downstream IP/SNI filtering).
Sources:
- https://northflank.com/docs/v1/application/network/configure-network-policies — FQDN/hostname listed as a rule target, mechanism not described

### tls_mitm_inspection
tls_mitm_inspection: Unknown/No — no TLS interception/MITM capability is documented for egress traffic. The only TLS-termination behavior documented is for *inbound* public endpoints ("HTTPS requests are terminated at the edge load-balancer"), which is unrelated to outbound inspection.
Sources:
- https://northflank.com/docs/v1/application/network/networking-on-northflank — "HTTPS requests are terminated at the edge load-balancer and the request is then routed internally"

### http_path_rules
http_path_rules: Partial — robust path-based rules exist (Exact / Prefix / RegEx path matching, plus IP/CIDR allow-deny) but are documented only for **inbound** traffic to a sandbox's exposed public ports, not for outbound/egress traffic. No method-based (GET/POST) gating is mentioned even on the inbound side. No egress equivalent is documented.
Sources:
- https://northflank.com/docs/v1/application/network/create-path-based-security-policies — "Exact will route only the specific path... Prefix will route the given path and all subpaths... RegEx will route paths that match the given regular expression"; scope confirmed inbound: "all requests to the service's port using the public endpoints will trigger the security policies"

### proto_coverage
proto_coverage: Partial — inbound networking documents broad protocol support (HTTP, HTTP/2, WebSockets, gRPC, TCP, UDP). Egress (Network Policies) is destination-based (IP/CIDR/FQDN/hostname) with no documented protocol- or port-level scoping. DNS, ICMP, and QUIC/HTTP3 are not mentioned in either direction. No statement of extensibility to custom/opaque L7 protocols was found.
Sources:
- https://northflank.com/docs/v1/application/network/networking-on-northflank — "HTTP, HTTP2, Websockets, gRPC, TCP and UDP networking protocols" (inbound)
- https://northflank.com/docs/v1/application/network/configure-network-policies — egress rules are destination-only, no protocol/port fields documented

### live_rule_reload
live_rule_reload: Unknown — no documentation found stating whether Network Policy or port-security-policy changes apply live without restarting/redeploying the sandbox.
Sources: none found; searched configure-network-policies, add-security-policies-for-ports, networking-on-northflank pages — silent on reload behavior.

### firewall_escape_hatch
firewall_escape_hatch: Unknown — no break-glass/timed-bypass or per-sandbox disable/re-enable mechanism for Network Policies is documented. Removing the feature would require deleting the policy entirely (not confirmed as a distinct "bypass" primitive vs. permanent removal).
Sources: none found; docs silent.

### enforcement_plane
enforcement_plane: kernel-level (eBPF via Cilium), on Kubernetes — Northflank's security page states Cilium is deployed by default to enforce network-policy restriction between project namespaces on the managed cloud; Network Policies (the BYOC egress/ingress feature) are documented as a Kubernetes-cluster-level construct, consistent with the same Cilium-based enforcement layer, though the Network Policies page itself does not name the enforcement technology explicitly. Whether an in-sandbox process could tamper with or route around this layer is not discussed — Kata's dedicated guest kernel would put the enforcement point outside the sandbox's kernel (harder to tamper with); for gVisor sandboxes the relationship isn't documented. Traffic logging at this layer is not documented (see network_audit).
Sources:
- https://northflank.com/security — "Cilium is deployed by default providing network-policy restriction between namespaces (projects)"
- https://northflank.com/docs/v1/application/network/configure-network-policies — policies operate at Kubernetes-cluster/project scope (mechanism not explicitly named on this page)

### fail_closed
fail_closed: Unknown — no documentation addresses what happens to enforced network policy when Northflank's control plane or a cluster component is unavailable. Given Cilium is a Kubernetes CNI plugin (policy state typically lives in kernel/eBPF maps independent of the API server), a fail-closed-by-architecture outcome is plausible but not confirmed by any fetched source — not asserted as fact.
Sources: none found; inference flagged as unconfirmed, not stated as determination.

### network_audit
network_audit: Unknown — general container logs/metrics are documented (see observability below), but no per-request egress log, connection log, or network-specific audit trail is described for Network Policies or port security policies.
Sources:
- https://northflank.com/docs/v1/application/observe/observability-on-northflank — logs/metrics are container-centric (stdout/stderr, resource metrics), not network-egress-specific

### workspace_modes
workspace_modes: Partial — the local-tool framing of "live bind mount vs ephemeral snapshot" doesn't map cleanly onto a remote platform. What's documented: `ephemeralStorage` (lost on restart) vs. persistent volumes (explicitly created and attached, survive pause/scale-to-zero). There is no live bidirectional bind-mount between a developer's local machine and the sandbox — code enters via git-based build or a container image, not a synced host directory.
Sources:
- https://northflank.com/docs/v1/application/sandboxes/sandboxes-on-northflank — ephemeralStorage vs. volumesToAttach at container init; "data survives across pauses and scale-to-zero events"

### observability
observability: Yes — live and historical logs/metrics for builds, deployments, jobs, and addons; metrics on a 15-second scale with 30-minute live-tail default; log sinks to forward logs to external analysis/alerting/audit tooling.
Sources:
- https://northflank.com/docs/v1/application/observe/observability-on-northflank — "view live and historical logs and metrics for builds, deployments, jobs, and addons"
- https://northflank.com/docs/v1/application/observe/configure-log-sinks — log sinks for external forwarding/alerting/audit

### supervision
supervision: Unknown — no documented runtime supervisor process that observes agent behavior and can actively intervene (containment/kill/quarantine commands) beyond standard platform lifecycle operations (pause/scale/delete) triggered manually or via API/CLI. Observability (logs/metrics) exists; an active, automated intervention layer is not described.
Sources: none found describing an automated supervisory/containment layer distinct from manual API lifecycle calls.

### fleet_mgmt
fleet_mgmt: Partial — projects/services/jobs are individually addressable and listable via API/CLI/SDK, with RBAC and tag-based targeting (used by Network Policies to select workload groups), but no sandbox-specific fleet registry, naming scheme, or multi-agent orchestration primitive is documented.
Sources:
- https://northflank.com/docs/v1/application/sandboxes/sandboxes-on-northflank — services managed individually via service name/ID
- https://northflank.com/docs/v1/application/network/configure-network-policies — workload-tag targeting for policy scope

### snapshots_persistence
snapshots_persistence: Partial — persistent volumes (4GB–64TB, multi-read-write) retain data across pause and scale-to-zero, and scaling to 0 stops compute billing while keeping storage intact. No sandbox snapshot/checkpoint export or restore-to-new-sandbox feature is documented.
Sources:
- https://northflank.com/product/sandboxes — "Fast persistent volumes (4GB-64TB) with multi-read-write support"; "Ephemeral by default: No forced time limits"
- https://northflank.com/docs/v1/application/sandboxes/deploy-sandboxes-on-northflank — "Scale to zero to pause compute billing while keeping storage intact"

## D. Setup
### setup
setup: Moderate — for managed cloud: sign up, generate API token, create a project, install the JS SDK (`npm install @northflank/js-client`) or CLI, create a service/sandbox deployment, poll for `COMPLETED` status. No Docker/Kubernetes prerequisite on the client side (it's an API/SDK call to a remote platform). For BYOC, setup is more involved: connect a cloud-provider account, provision a BYOC cluster, optionally select a sandbox technology (Kata/gVisor) per node pool — this step is not time-quantified in docs.
Sources:
- https://northflank.com/docs/v1/application/sandboxes/deploy-sandboxes-on-northflank — signup → project → deploy workload steps
- https://northflank.com/docs/v1/application/sandboxes/deploy-sandboxes-in-your-cloud — "connect your cloud provider and deploy a BYOC cluster"

## E. Daily use
### daily_use
daily_use: Moderate — sandbox lifecycle (create/exec/pause/scale/delete) is driven through API/SDK/CLI calls rather than a single local dev-loop command; the exec API/CLI (`northflank exec service --cmd "..."`) opens interactive or one-off command sessions with streamed stdout/stderr. No documented single-binary "attach and go" workflow comparable to a local container CLI; every session is a network round trip to the platform.
Sources:
- https://github.com/northflank/skills/blob/master/skills/northflank/references/cli.md — `northflank exec service|job` with `--cmd`, `--user`, `--instance`; noted TTY-absence caveat in CI

## F. Configuration
### config_depth
config_depth: Deep — Infrastructure-as-Code support, declarative `deploymentPlan` (CPU/mem/GPU bundles), env/secret injection (`runtimeEnvironment`, secret groups, external secret manager beta), volume attachment, BYOC Network Policies, build engine choice (BuildKit/Kaniko), sandbox technology selection (Kata vs gVisor per node pool on BYOC). No lifecycle-hook primitives equivalent to "post-init/pre-run" scripts were found documented specifically for sandboxes.
Sources:
- https://northflank.com/docs/v1/application/infrastructure-as-code/infrastructure-as-code — IaC support
- https://northflank.com/docs/v1/application/sandboxes/deploy-sandboxes-in-your-cloud — "select a sandbox technology during setup"

### policy_model
policy_model: Moderate — secure default (microVM isolation is on by default for both deployment modes) plus several opt-in dials (Kata vs gVisor selection on BYOC node pools, IP allow/deny port security policies, path-based inbound security policies, BYOC Network Policies for egress). Not fully policy-driven across all deployment modes, though: the most consequential dial for this comparison — egress allowlisting — is entirely unavailable on Northflank's own managed cloud and only exists once a customer stands up a BYOC cluster, so "dial security up or down without abandoning the tool" only fully holds for BYOC users.
Sources:
- https://northflank.com/docs/v1/application/network/configure-network-policies — BYOC-only scoping stated explicitly
- https://northflank.com/security — Kata (default) vs gVisor (fallback/BYOC-selectable) isolation choice

## G. DX
### bind_mount_sharing
bind_mount_sharing: No — execution is remote; no live bidirectional bind mount between a developer's local filesystem and a running sandbox is documented. Code reaches the sandbox via git-based builds, container images, or explicit volume attachment/exec-based file operations, not a synced host directory.
Sources:
- https://northflank.com/docs/v1/application/build/build-code-from-a-git-repository — git-based build path as the documented code-delivery mechanism

### cred_forwarding
cred_forwarding: Unknown — no ssh-agent, GPG-agent, or git-credential forwarding/mediation feature is documented for sandboxes. Secrets are supplied by explicit env-var/secret-group injection instead (see credential_theft above), which is a different mechanism (copied value vs. mediated/forwarded agent).
Sources: none found describing agent forwarding; docs describe only secret injection.

### browser_auth
browser_auth: Unknown — no documentation found describing a host-browser-proxying mechanism (sandboxed process triggers an OAuth/device-code browser flow on the developer's machine). Not structurally ruled out, but undocumented.
Sources: none found.

### shared_dirs
shared_dirs: Yes — persistent volumes (4GB–64TB) can be attached beyond the primary workspace, with multi-read-write support enabling shared storage across workloads (e.g. multiple sandbox instances or services).
Sources:
- https://northflank.com/product/sandboxes — "Fast persistent volumes (4GB-64TB) with multi-read-write support"

### git_worktrees
git_worktrees: Unknown — no first-class git-worktree feature is documented for Northflank sandboxes specifically. (A Docker-blog third-party source discusses worktrees as a general agent-sandboxing pattern, but that is not a Northflank feature claim and is excluded here per evidence rules.)
Sources: none found on Northflank's own docs/blog.

### nested_containers
nested_containers: Unknown — Kata Containers gives each sandbox a dedicated guest kernel, which is structurally the kind of boundary that typically supports running a nested container runtime/Docker socket safely (unlike gVisor, which historically has limited/no support for nested virtualization). However, no Northflank documentation was found explicitly stating that Docker-in-sandbox, a docker socket, or DinD is offered or supported inside a sandbox. Not confirmed either way.
Sources: none found stating this explicitly; structural inference only, not asserted as fact.

### harness_agnostic
harness_agnostic: Yes — sandboxes run arbitrary container images/base images (e.g. `ubuntu:22.04`) with exec-session access, with no vendor tie to a specific coding-agent CLI. Marketing/blog content frames the product generically for "LLM-generated code, user-submitted code, AI agents, and CI/CD pipelines" rather than integrating with a named agent product; no Claude Code/Cursor/Codex-specific integration was found (positive for agnosticism, but also means no purpose-built glue for any one harness).
Sources:
- https://northflank.com/product/sandboxes — "ideal for running untrusted code like LLM-generated code, user-submitted code, AI agents, and CI/CD pipelines"
- https://northflank.com/blog/best-sandboxes-for-coding-agents — no agent-CLI-specific integration mentioned despite being a coding-agent-focused post

## H. Performance
### performance
performance: Lightweight (per vendor claim, not independently verified) — "sub-second cold starts" is the headline performance claim, attributed to the microVM boot path. No independently-run or third-party benchmark numbers were found in the sources fetched; disk footprint, RAM overhead, and bind-mount IO throughput are not documented (moot for the latter given no bind-mount mode exists).
Sources:
- https://northflank.com/product/sandboxes — "Sub-second cold starts" (vendor claim)

## I. Feasibility
### feasibility
feasibility: Adoptable-today — being a remote/API-driven platform, there are no client-OS constraints (works from any machine with network access to the API/CLI/UI); no local Docker/Kubernetes prerequisite for managed-cloud use. Free tier ("Sandbox tier," $0/month, always-on compute, 2 free services, 1 free database, 2 free cron jobs) allows immediate solo-dev adoption. BYOC raises the bar considerably (own cloud account, cluster provisioning) and is where the differentiating egress-control feature lives. Platform is young-ish (since ~2021) relative to some incumbents but carries SOC2/HIPAA compliance signaling production maturity.
Sources:
- https://northflank.com/pricing — free "Sandbox tier" details
- https://northflank.com/docs/v1/application/sandboxes/deploy-sandboxes-in-your-cloud — BYOC setup requirements

## J. Price (prose-only)
### pricing
Consumption-based, per-second billing. Free tier: always-on compute, 2 free services, 1 free database, 2 free cron jobs, $0/month. Paid usage: CPU $0.01667/vCPU-hour, memory $0.00833/GB-hour, storage $0.15/GB-month, GPUs from $0.80/hour (L4) up to ~$3.14/hour (H200)/$2.74/hour (H100 "all-in" per product page). No seat-based pricing ("teams included for free"). Enterprise tier (custom pricing, 24/7 SLA support, SSO/SAML/OIDC, audit logging, "run in your VPC"/BYOC) is contact-sales gated, though BYOC itself is stated as self-serve without enterprise-only gatekeeping per a vendor blog post. Not open-source/self-hostable as a product.
Sources:
- https://northflank.com/pricing — free tier + per-second usage pricing + enterprise gating
- https://northflank.com/product/sandboxes — GPU/CPU pricing figures

## K. Extensibility
### extensibility
extensibility: Yes — custom Docker images and Dockerfile/buildpack builds, GitOps (GitHub/GitLab/Bitbucket) with built-in CI/CD and automatic rollbacks, Infrastructure-as-Code, addon deployment (Redis/Postgres/MySQL/MongoDB, S3-compatible object storage/MinIO) alongside sandboxes, log sinks for external tooling, REST API/JS SDK/CLI as programmatic surfaces, and BYOC for custom cloud/infra targets.
Sources:
- https://northflank.com/product/sandboxes — deployment options, CI/CD, addons, GitOps
- https://northflank.com/docs/v1/application/build/build-with-a-dockerfile — custom image builds

## Unknowns & caveats
- **WebSearch budget exhausted mid-research**: several planned searches (company founding-date detail beyond "since 2021," explicit nested-container/Docker-in-sandbox confirmation, independent escape-CVE history) could not be run; those criteria are marked Unknown rather than guessed.
- **egress_allowlist / network_default_posture / most of the network-control block are BYOC-only**: Northflank's own managed multi-tenant cloud (the lowest-friction, free-tier entry point) has no documented sandbox-specific egress control at all. This is the single most consequential caveat for this comparison — the network-hardening story only applies once a customer has stood up a BYOC cluster.
- **No verbatim quote independently re-confirmed** for the Kata-vs-gVisor comparison blog (`northflank.com/blog/kata-containers-vs-gvisor`) or the "how to spin up a secure code sandbox" blog — these appeared only in WebSearch AI-summaries, not a direct WebFetch this run, so they are referenced as directionally consistent but not cited as sourced quotes per evidence rules. The core Kata/gVisor claim is still confirmed independently via the directly-fetched product and security pages.
- **fail_closed** marked Unknown despite a plausible eBPF/Cilium-architecture inference (policy state generally persists in-kernel independent of a control-plane process) — inference is noted in the section but not asserted as a determination, per the "never guess" rule.
- No blocked URLs (NXDOMAIN/connection-refused) were encountered — all attempted WebFetch calls to northflank.com and github.com succeeded.

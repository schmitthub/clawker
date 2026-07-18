# Agent Sandbox Comparison — Plan (rev 7, 2026-07-18)

Goal: marketing comparison table for clawker README (done draft) + clawker.dev splash (not started). Clawker vs 24 competitors. Honest, cited, official-docs-only. Criteria doc: `agent-sandbox-research/assessment-guidelines` (CANONICAL).

## CURRENT TASK (in flight): full-boolean table rebuild
User wants the README comparison table rebuilt as ALL-BINARY (zero "partial" values). Rule: if a cell wants to be partial, SPLIT it into more narrow columns until each is a checkable yes/no fact. GitHub horizontal scroll is acceptable ("overflows with scrolling its fine").

### Column set agreed (~44 cols, FLAT single table, no group headers, no separator rows):
Local · Open source · Self-hostable · Deny-by-default egress · Allowlist exists · Domain allowlist rules · Subdomain wildcard · IP/CIDR · Port scoping · Deny rules · HTTP path · HTTP method · Regex path · DNS-level block · Domain-native (no IP pin) · TLS MITM · Kernel-level enforcement · Fail-closed firewall · Live firewall reload · Timed auto-bypass · Filters DNS · Filters TCP · Filters UDP · Filters QUIC · Filters ICMP · Filters SSH · Filters WebSocket · Per-request audit log · Agent-activity dashboard · Active supervision · Fleet registry · SSH-agent fwd · GPG-agent fwd · Git-cred fwd · Cred-injection proxy · Host-browser auth · Live bind-mount · Ephemeral snapshot mode · Git-worktree mgmt · Harness seeding · Shared host state (mounted) · Extra host-dir mounts · Declarative config · Custom image/Dockerfile · Lifecycle hooks · Plugin/bundle system · Any agent CLI · Env snapshot/resume · Durable agent state

Naming rule learned: header = the CAPABILITY not the category ("Filters DNS" not "DNS"/"Proto: DNS"). "Runs local" collapsed to just "Local" (remote is trivially implied). "Live reload"→"Live firewall reload".

### Fill rules (in guidelines rev 7):
- BINARY only. Unknown/undocumented → ❌ (legend "no or not documented"). NO partials.
- FEATURE test (the systemic error to avoid): ✅ only if the SANDBOX implements it. "Can apt install git / can curl" ≠ ✅. Harness/CLI feature surfacing inside sandbox ≠ sandbox feature (attribution rule).
- Every ✅ carries implementing mechanism in `<abbr title="...">` hover ≤110 chars, so USER can audit each ✅ at a glance. No nameable mechanism → ❌.

### STATUS 2026-07-18 (rev 10) — SCOPE LOCKED
- CLASSIFICATION (governing): table = "why run a vendored coding-agent CLI (Claude Code/Codex/…) inside clawker vs bare metal w/ the CLI's own sandboxing, or another tool for the same job." Peer class = SECURITY SANDBOXES that contain a prompt-injected or mistaken coding agent doing real work on a project. NOT: programmatic sandbox SDKs (build-your-own security), NOT services executing AI-generated code / handoff envs.
- FINAL roster = 8 rows (pushed commit a783e5a4): clawker, docker-sandboxes, sculptor-imbue, devcontainers, smolvm, claude-code-sandboxing, codex-cli-sandbox, anthropic-sandbox-runtime(srt, only borderline kept). clawker 48/50.
- DROPPED and WHY: code-exec cohort (e2b, vercel, modal, daytona, cloudflare, codesandbox, morph, runloop, northflank, blaxel, beam) = run generated code, different class. SDK/build-your-own (openai-api-sandboxes=Agents SDK, k8s-agent-sandbox=CRD, agent-sandbox-org=control plane+E2B API, opensandbox=protocol runtime) = program-it-yourself. Handoff/runtime (dagger-container-use=MCP env, microsandbox=microVM+SDK). All research memories retained in-repo.
- Observability column split into 3 (metrics_dashboard | harness_telem | ext_monitoring). metrics_dashboard ✅ = clawker+smolvm; harness_telem + ext_monitoring = clawker-only. dashboard key removed.
- Cols now 50. final matrix: scratchpad/final-matrix.json (still holds all 25 providers' cells; render only emits the 8). render: scratchpad/final-section.md.
- clawker ❌ only on: cred_proxy (by design/facade), env_resume (no memory checkpoint).

### (history) STATUS 2026-07-18 (rev 9)
- 3-pass validation DONE (A=wcuh07ph1, B=wwjb7k2ar, C=wrweflx3z; each 25 agents, blind). 96.4% unanimous (1181/1225). 44 disagreements all 2-1 → majority applied. 320 flags collated → most were "weaker-form correctly=no", not real splits.
- FINAL table PUSHED: commit 5dba3cbd, branch feat/comparison-chart. 48 cols × 25 providers (dropped extra_mounts). clawker 46/48. final matrix: scratchpad/final-matrix.json; render: scratchpad/final-section.md; triangulation: scratchpad/triangulate.json.
- Changes applied on top of majority: (1) supervision strict=external sandbox-layer container that can quarantine/kill running agent — NOT stoppable-process, NOT harness permission loop; openai→no; clawker SOLE yes. (2) drop extra_mounts col (not a feature; user: overlaps bind/shared). (3) k8s-agent-sandbox + agent-sandbox-org local→yes (kind/k3s/minikube override). (4) rename display agent-sandbox-org → "agent-sandbox/agent-sandbox" (repo slug; DISTINCT from kubernetes-sigs/agent-sandbox). (5) dashboard flips e2b/morph/beam→no. (6) fleet flips smolvm/codesandbox/beam→no. (7) any_agent claude-code-sandboxing→no.
- GOVERNING RULE reinforced hard by user: assess SANDBOX features ONLY, never harness features. Harness = agent's own permission/approval loop, worktree fan-out, device-code login, OTel telemetry. Conflation is THE failure mode; user distrusts model's independent feature calls — surface, don't assert.
- OPEN / user now scrutinizing pushed table. Conflation-risk columns to re-audit if asked: dashboard (harness telemetry), browser_auth (CLI login vs sandbox proxy), harness_seed + any_agent (must be sandbox property not harness capability). Focused ✅-cell attribution re-audit was offered; user chose to eyeball first.
- Backlog: splash variant (clawker.dev separate repo), TOC entry, per-provider notes trim.

### (history) STATUS 2026-07-18 (rev 8)
- Pass A (25-agent workflow, one per provider reading its writeup, binary+mechanism schema) DONE. 25×49 cells, 0 anomalies (no yes-without-mechanism). Data: scratchpad/bool-matrix.json + committed into README (values + <abbr> mechanism hovers). clawker=46/49 (no on cred_proxy by design, extra_mounts, env_resume).
- README all-binary table COMMITTED+PUSHED (commit 589b3142, branch feat/comparison-chart), replacing the old partial table. Kept credential-injection moat blockquote + added methodology <details>.
- Cross-validation IN FLIGHT: passes B (wf_3a626f89-def) + C (wf_cdb94cca-091) = two fresh independent 25-agent sweeps, blind to A and each other, same schema + new optional per-cell `f` flag (surfaces genuine partials as missing-column candidates instead of fabricated binaries — user's concern). NEXT: triangulate A/B/C per cell → consensus vs disagreement; surface disagreements + flags for user review; correct cells; re-render README.
- Column-candidate concern (user): agents forced to binary may crush a true partial silently. Fix = the `f` flag added to B/C. Review flags before finalizing.

### USER OVERRIDES + queued edits (apply at NEXT table write, after B/C triangulation — user said do it then, not now)
- Row rename: `agent-sandbox-org` display label → `agent-sandbox/agent-sandbox` (its git repo slug). Reason: it collides with the SIG one; the two ARE DISTINCT projects (kubernetes-sigs/agent-sandbox = CRD+controller SIG Apps subproject; agent-sandbox/agent-sandbox = separate org, Blueprint→ReplicaSet control plane w/ E2B-compat API). Keep K8s row label as "K8s agent-sandbox".
- local OVERRIDE = yes for BOTH k8s-agent-sandbox AND agent-sandbox-org (mechanism: "Deploys to any conformant cluster incl. local kind/k3s/minikube on the dev machine"). Agent passes score these Remote/no from the writeups; USER OVERRIDE WINS — k8s runs locally (kind/k3s/minikube/VMs). Overrides win over A/B/C consensus. Logged in scratchpad/user-overrides.json. Already patched into scratchpad/bool-matrix.json (pass A working copy).

### (superseded) NEXT STEP: generate the boolean matrix.
DO NOT trust a single subagent pass blind — this session the subagent made ≥3 feature-vs-capability errors caught by user eye (Docker Sandboxes worktrees=partial for a VM with git=WRONG→No; Sculptor browser_auth=yes for gh device-flow on host process=WRONG→No; Codex CLI "remote"=conflated separate Codex Cloud product=WRONG, Codex is local CLI only). Main-thread must NOT make independent feature/not-feature judgment calls — user catches what model misses. Approach: generate values but every ✅ must cite mechanism; present for user audit before/at commit; expect user to correct cells.

## DONE
- 24 provider writeups + clawker self-assessment in `agent-sandbox-research/providers/<slug>` (official-docs-cited).
- Attribution audit ran (14/96 cells corrected). Corrected: docker-sandboxes worktrees→No, sculptor browser_auth→No, + cred_forwarding downgrades.
- domain_native_enforcement criterion added; clawker=Yes.
- Draft README table committed+pushed on branch feat/comparison-chart (currently PARTIAL-based, 25 providers × 21 cols + Cost first col + credential-injection callout). THIS TABLE IS BEING REPLACED by the all-boolean version.
- Prior matrix (partial-based) saved at `/tmp/claude-1001/-Users-andrew-Code-clawker/4f24b15f-b4ac-4202-9208-b6f6a844113c/scratchpad/matrix.json` — 25×21 with hovers, source data for the boolean split.

## Branch state
feat/comparison-chart — commits: README table + research memories + attribution corrections + domain-native + cred callout, all pushed. README section "How clawker compares" after system-diagram img.

## Providers (FROZEN roster = 8; 17 out-of-scope writeups DELETED)
clawker, docker-sandboxes, nono, mattolson-agent-sandbox, devcontainers, claude-code-sandboxing, codex-cli-sandbox, anthropic-sandbox-runtime. (8 rows; Cost is first column.) DROPPED 2026-07-18: SmolVM (microVM runtime, not an agent-sandbox product) and sculptor-imbue (NOT a sandbox — git-worktree on host FS, agent runs as ordinary host process, zero egress/FS/cred containment; a parallel-agent orchestration UI, same class as a harness; writeup deleted, recoverable in git). nono+mattolson added via github-stars sweep 2026-07-18.
Classification criteria for who qualifies (agent-harness security sandbox vs code-exec/SDK/primitive) now live in `agent-sandbox-research/assessment-guidelines` → "Classification" section. Do not re-add dropped entries.

## Backlog after boolean table
- † domain-native cells verification (many "tbv" in prior matrix)
- splash variant (clawker.dev, separate repo, not tracked here)
- TOC entry in README

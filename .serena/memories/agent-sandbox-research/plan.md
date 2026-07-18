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

### NEXT STEP: generate the boolean matrix.
DO NOT trust a single subagent pass blind — this session the subagent made ≥3 feature-vs-capability errors caught by user eye (Docker Sandboxes worktrees=partial for a VM with git=WRONG→No; Sculptor browser_auth=yes for gh device-flow on host process=WRONG→No; Codex CLI "remote"=conflated separate Codex Cloud product=WRONG, Codex is local CLI only). Main-thread must NOT make independent feature/not-feature judgment calls — user catches what model misses. Approach: generate values but every ✅ must cite mechanism; present for user audit before/at commit; expect user to correct cells.

## DONE
- 24 provider writeups + clawker self-assessment in `agent-sandbox-research/providers/<slug>` (official-docs-cited).
- Attribution audit ran (14/96 cells corrected). Corrected: docker-sandboxes worktrees→No, sculptor browser_auth→No, + cred_forwarding downgrades.
- domain_native_enforcement criterion added; clawker=Yes.
- Draft README table committed+pushed on branch feat/comparison-chart (currently PARTIAL-based, 25 providers × 21 cols + Cost first col + credential-injection callout). THIS TABLE IS BEING REPLACED by the all-boolean version.
- Prior matrix (partial-based) saved at `/tmp/claude-1001/-Users-andrew-Code-clawker/4f24b15f-b4ac-4202-9208-b6f6a844113c/scratchpad/matrix.json` — 25×21 with hovers, source data for the boolean split.

## Branch state
feat/comparison-chart — commits: README table + research memories + attribution corrections + domain-native + cred callout, all pushed. README section "How clawker compares" after system-diagram img.

## Providers (25 slugs)
clawker, docker-sandboxes, claude-code-sandboxing, codex-cli-sandbox, anthropic-sandbox-runtime, microsandbox, smolvm, dagger-container-use, devcontainers, sculptor-imbue, e2b, modal-sandboxes, cloudflare-sandbox-sdk, vercel-sandbox, daytona, codesandbox-sdk, morph, runloop, northflank-sandboxes, blaxel, beam-beta9, openai-api-sandboxes, k8s-agent-sandbox, agent-sandbox-org, opensandbox

## Backlog after boolean table
- † domain-native cells verification (many "tbv" in prior matrix)
- splash variant (clawker.dev, separate repo, not tracked here)
- TOC entry in README

# Multi-Harness — Current State (branch feat/multi-harness-support)

The multi-harness feature is IMPLEMENTED and committed. Planning artifacts (design/migration/coupling-inventory/descriptor-schema/template-refactor/substrate-stacks/upgrade-guide) were pruned 2026-07-07 — code + shipped docs are the source of truth now:

- User docs: `docs/harness-bundles.mdx`, `docs/stacks.mdx`, `docs/custom-images.mdx`, `docs/upgrading/v0.13.mdx` (version-scoped upgrade guides — one page per breaking release, never an evergreen "Upgrading" page), CHANGELOG `[0.13.0]`
- Author skill: `claude-plugin/clawker-support/skills/harness-stack-dev/` (bundle/stack authoring reference — second skill in the clawker-support plugin)
- Code: `internal/harness/` (bundle types/load/compose — materialize.go DELETED in stack-contract Task 2), `internal/stack/`, `internal/bundler/` (harness.go, stack.go, egress.go, basehash.go, assets/harnesses/{claude,codex}, assets/stacks/{go,node,python,rust}), `internal/cmd/{stack,harness}/` (register/list/remove front-door), `internal/cmdutil/registry.go`, `internal/consts/name.go` (unified naming rule), `internal/config/{schema.go,validate.go,migrations.go}` (project stacks:/harnesses:/build.harnesses: registries + front-door validation + settings-registry strip)

## What shipped (one-paragraph summary)

Harness = file-backed bundle (harness.yaml + Dockerfile.harness.tmpl + assets/) embedded for claude+codex, resolving straight from the binary (NO materialization/config-dir copies as of stack-contract Task 2); custom bundles registered per-project in clawker.yaml `harnesses:` (name→path). Built-in default = claude (no registry default flag). Images: pinned Debian substrate → shared base (packages + project stacks + instructions/inject) → per-harness image (`clawker-<project>:<harness>` + `:default` alias for claude; `:latest` legacy fallback warns). Language stacks file-backed (stack.yaml + root/user fragments), declared never installed, registered per-project in clawker.yaml `stacks:`; per-lineage lookup (project registry → bundle stacks/ → shipped), closer wins wholesale, provenance printed. No BYO base image / custom Dockerfile. No host credential copying — in-container auth, browser proxied to host. Egress floor composes from the selected bundle's `egress:`. Legacy claude-only chain (clawker generate, DockerfileManager, master Dockerfile.tmpl, BuildDefaultImage) DELETED. Load-time migrations strip build.image/dockerfile/context + use_host_auth and move agent.claude_code→harnesses.claude with stderr notices.

## Remaining files in this folder

| file | purpose |
|---|---|
| `blast-radius.md` | Schema-migration blast radius assessment + gap fixes (all gaps CLOSED on branch 2026-07-07 except noted UAT items) |
| `stack-unit-contract-model.md` | THE design model (brainstorm 2026-07-07, r9): engine sovereignty, roles, authority map, clause register, opaque-token graph, per-lineage binding, composition rules, user stories, open leaf decisions |
| `stack-unit-contract-design.md` | Brainstorm log: prior art (CNB/devcontainer/nix, live-fetched), design axes, revision history r1–r6 |
| `harness-research-opencode-pi.md` | Live-verified recon for opencode + pi bundles — DEFERRED by user 2026-07-07: adding them is the planned UAT exercise for the harness-stack-dev skill |

## Open items (post-branch / host-side)

- **~~BLOCKS MERGE~~ RESOLVED (2026-07-08): stack unit-contract redesign IMPLEMENTED.** The r10 design (`stack-unit-contract-model`) shipped as the 6-task `stack-contract-implementation` initiative on this branch: project-side keyed registries in clawker.yaml (settings registry + materialization DELETED), per-lineage lookup with wholesale override + provenance, cross-stratum dedup killed (fragments self-guard), unified naming rule, `clawker stack|harness register/list/remove` CLI, per-harness build overlay, base-declared build-arg folded into freshness hash. Placement topology preserved (project→base, harness→harness image) but earliest-stage-wins REPLACED by always-render. NO provides/requires/version-constraint algebra — user rejected the capability-token model (see model doc §1 DEAD vocabulary). Renamed toolchains→stacks (`cd72803b`).

- **BLOCKS MERGE (user 2026-07-07):** the skills plugin goes multi-agent — no longer Claude Code-only packaging; use established all-in-one multi-agent harness plugin approaches (research first). User docs already say "agent skills plugin" (never "Claude Code plugin", no future-tense framing); dev CLAUDE.mds still describe the Claude Code plugin spec and get updated when the packaging lands.

- Host-side UAT of migrations + staleness warnings on a real pre-upgrade config; host e2e run (`go test ./test/e2e/...` — never in-container)
- Block names still placeholder `block_1..6` (`harness.DeclaredBlocks` — final event-centric names TBD by user; renaming ripples docs + skill + bundles)
- opencode/pi bundles via harness-stack-dev skill UAT (research memo ready)
- `npx mintlify dev` render pass over new/rewritten pages not yet done

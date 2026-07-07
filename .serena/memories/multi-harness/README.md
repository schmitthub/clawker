# Multi-Harness — Current State (branch feat/multi-harness-support)

The multi-harness feature is IMPLEMENTED and committed. Planning artifacts (design/migration/coupling-inventory/descriptor-schema/template-refactor/substrate-stacks/upgrade-guide) were pruned 2026-07-07 — code + shipped docs are the source of truth now:

- User docs: `docs/harness-bundles.mdx`, `docs/stacks.mdx`, `docs/custom-images.mdx`, `docs/upgrading/v0.13.mdx` (version-scoped upgrade guides — one page per breaking release, never an evergreen "Upgrading" page), CHANGELOG `[0.13.0]`
- Author skill: `claude-plugin/clawker-support/skills/harness-stack-dev/` (bundle/stack authoring reference — second skill in the clawker-support plugin)
- Code: `internal/harness/` (bundle types/load/compose/materialize + staleness stamps), `internal/stack/`, `internal/bundler/` (harness.go, stack.go, egress.go, assets/harnesses/{claude,codex}, assets/stacks/{go,node,python,rust}), `internal/config/migrations.go` (legacy-key strip + agent.claude_code→harnesses.claude rewrite)

## What shipped (one-paragraph summary)

Harness = file-backed bundle (harness.yaml + Dockerfile.harness.tmpl + assets/) embedded for claude+codex, materialized copy-if-missing to `<config>/harnesses/<name>` with content-hash staleness stamps, registered in settings `harnesses:` (explicit path, one default). Images: pinned Debian substrate → shared base (packages + project stacks + instructions/inject) → per-harness image (`clawker-<project>:<harness>` + `:default` alias; `:latest` legacy fallback warns). Language stacks file-backed (stack.yaml + root/user fragments), declared never installed. No BYO base image / custom Dockerfile. No host credential copying — in-container auth, browser proxied to host. Egress floor composes from the selected bundle's `egress:`. Legacy claude-only chain (clawker generate, DockerfileManager, master Dockerfile.tmpl, BuildDefaultImage) DELETED. Load-time migrations strip build.image/dockerfile/context + use_host_auth and move agent.claude_code→harnesses.claude with stderr notices.

## Remaining files in this folder

| file | purpose |
|---|---|
| `blast-radius.md` | Schema-migration blast radius assessment + gap fixes (all gaps CLOSED on branch 2026-07-07 except noted UAT items) |
| `harness-research-opencode-pi.md` | Live-verified recon for opencode + pi bundles — DEFERRED by user 2026-07-07: adding them is the planned UAT exercise for the harness-stack-dev skill |

## Open items (post-branch / host-side)

- **BLOCKS MERGE (user 2026-07-07): stack unit-contract redesign.** The shipped unit model (flat name-string dedup, declaration-order rendering, no unit-to-unit requires, no version/capability constraints, conflicts undetected below the name) is not acceptable to merge — it forces strata violations for any non-leaf need (e.g. typescript-requires-node) and has no enforceable cross-stratum standard. Placement topology stays (project→base, harness→harness image, earliest-stage-wins). Direction: provides/requires + version constraints, resolver closure, conflict-with-provenance errors; prior art CNB build-plan + devcontainer Features (possible distribution adapter). Design doc in this folder for user teardown before any code. Renamed toolchains→stacks (`cd72803b`).

- **BLOCKS MERGE (user 2026-07-07):** the skills plugin goes multi-agent — no longer Claude Code-only packaging; use established all-in-one multi-agent harness plugin approaches (research first). User docs already say "agent skills plugin" (never "Claude Code plugin", no future-tense framing); dev CLAUDE.mds still describe the Claude Code plugin spec and get updated when the packaging lands.

- Host-side UAT of migrations + staleness warnings on a real pre-upgrade config; host e2e run (`go test ./test/e2e/...` — never in-container)
- Block names still placeholder `block_1..6` (`harness.DeclaredBlocks` — final event-centric names TBD by user; renaming ripples docs + skill + bundles)
- opencode/pi bundles via harness-stack-dev skill UAT (research memo ready)
- `npx mintlify dev` render pass over new/rewritten pages not yet done

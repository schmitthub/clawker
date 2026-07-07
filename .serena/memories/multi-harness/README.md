# Multi-Harness — Current State (branch feat/multi-harness-support)

The multi-harness feature is IMPLEMENTED and committed. Planning artifacts (design/migration/coupling-inventory/descriptor-schema/template-refactor/substrate-toolchains/upgrade-guide) were pruned 2026-07-07 — code + shipped docs are the source of truth now:

- User docs: `docs/harness-bundles.mdx`, `docs/toolchains.mdx`, `docs/custom-images.mdx`, `docs/upgrading.mdx`, CHANGELOG `[0.13.0]`
- Author skill: `claude-plugin/clawker-harness-dev/` (bundle/toolchain authoring reference)
- Code: `internal/harness/` (bundle types/load/compose/materialize + staleness stamps), `internal/toolchain/`, `internal/bundler/` (harness.go, toolchain.go, egress.go, assets/harnesses/{claude,codex}, assets/toolchains/{go,node,python,rust}), `internal/config/migrations.go` (legacy-key strip + agent.claude_code→harnesses.claude rewrite)

## What shipped (one-paragraph summary)

Harness = file-backed bundle (harness.yaml + Dockerfile.harness.tmpl + assets/) embedded for claude+codex, materialized copy-if-missing to `<config>/harnesses/<name>` with content-hash staleness stamps, registered in settings `harnesses:` (explicit path, one default). Images: pinned Debian substrate → shared base (packages + project toolchains + instructions/inject) → per-harness image (`clawker-<project>:<harness>` + `:default` alias; `:latest` legacy fallback warns). Language toolchains file-backed (toolchain.yaml + root/user fragments), declared never installed. No BYO base image / custom Dockerfile. No host credential copying — in-container auth, browser proxied to host. Egress floor composes from the selected bundle's `egress:`. Legacy claude-only chain (clawker generate, DockerfileManager, master Dockerfile.tmpl, BuildDefaultImage) DELETED. Load-time migrations strip build.image/dockerfile/context + use_host_auth and move agent.claude_code→harnesses.claude with stderr notices.

## Remaining files in this folder

| file | purpose |
|---|---|
| `blast-radius.md` | Schema-migration blast radius assessment + gap fixes (all gaps CLOSED on branch 2026-07-07 except noted UAT items) |
| `harness-research-opencode-pi.md` | Live-verified recon for opencode + pi bundles — DEFERRED by user 2026-07-07: adding them is the planned UAT exercise for the clawker-harness-dev skill |

## Open items (post-branch / host-side)

- Host-side UAT of migrations + staleness warnings on a real pre-upgrade config; host e2e run (`go test ./test/e2e/...` — never in-container)
- Marketplace entry for `clawker-harness-dev` in schmitthub/claude-plugins repo (CI matrix in main.yml already targets it)
- Block names still placeholder `block_1..6` (`harness.DeclaredBlocks` — final event-centric names TBD by user; renaming ripples docs + skill + bundles)
- opencode/pi bundles via harness-dev-skill UAT (research memo ready)
- `npx mintlify dev` render pass over new/rewritten pages not yet done

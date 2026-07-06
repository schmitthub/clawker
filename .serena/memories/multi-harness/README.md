# Multi-Harness Planning

Planning artifacts for making clawker support any coding agent harness
(claude-code, codex, opencode, pi, + user-custom), not just Claude Code.
Branch: `feat/multi-harness-support`.

| file | purpose |
|---|---|
| `design.md` | **Source of truth for decisions** — image model, build→runtime join key, config model, `Harness` interface + registry, extensibility tiers, open questions |
| `migration.md` | **Execution spec — locked decisions + phases.** Supersedes design.md where they conflict (extensibility tiers) |
| `master-template-refactor.md` | **Phase 1 blueprint** — line-by-line Dockerfile.tmpl disposition (master vs claude block), block positions, gotchas |
| `descriptor-schema.md` | **Phase 0 output** — harness.yaml schema draft + claude-code.yaml + codex.yaml pressure test |
| `claude-coupling-inventory.md` | **Coupling evidence** — every Claude-specific operation by subsystem, with file:line. Mechanic-level for the load-bearing paths; remaining recon tracked at the end |
| `upgrade-guide.md` | **User-facing migration notes** — breaking changes across the multi-harness merge (rebuild --no-cache, fresh volumes, CP restart, marker-path move). Living doc fed by UAT findings |

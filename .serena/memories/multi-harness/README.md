# Multi-Harness Planning

Planning artifacts for making clawker support any coding agent harness
(claude-code, codex, opencode, pi, + user-custom), not just Claude Code.
Branch: `feat/multi-harness-support`.

| file | purpose |
|---|---|
| `design.md` | **Source of truth for decisions** — image model, build→runtime join key, config model, `Harness` interface + registry, extensibility tiers, open questions |
| `claude-coupling-inventory.md` | **Coupling evidence** — every Claude-specific operation by subsystem, with file:line. Mechanic-level for the load-bearing paths; remaining recon tracked at the end |

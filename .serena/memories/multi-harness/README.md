# Multi-Harness — Current State (branch feat/multi-harness-support)

**2026-07-10: the extensibility DESIGN was rejected and is being redesigned from
scratch.** The active design lives in serena `brainstorm_clawker-package-install-model`
(bundle/install-model pivot). Its GOVERNING MANDATE applies here: the former
registry/walkup/materialize design was branch-only experimentation — never
shipped, no user ever saw it. It is not precedent for anything. Do NOT cite its
decisions, mirror its schemas, or reuse its plumbing as a shortest path. The old
design-model memory (`stack-unit-contract-model`) was deliberately deleted so it
cannot bias future sessions.

## What exists (facts about code, not design law)

Branch code implements the now-rejected experiment. Treat it as inventory for
the rewrite — things to delete, gut, or (only after first-principles
re-justification against the new model) keep:

- `internal/bundler/` — bundle/stack/harness types, loaders, resolution, egress
  floor, basehash, embedded `assets/harnesses/{claude,codex}` +
  `assets/stacks/{go,node,python,rust}`, monitoring unit loading/validation.
- `internal/config/` — manifest schemas (`Manifest`/`StackManifest`/`EgressRule`,
  `MonitoringUnitManifest`), project `stacks:`/`harnesses:` registries (DEAD in
  the new model), front-door validation.
- `internal/cmd/{stack,harness}/` register/list/remove front doors (DEAD shape —
  new model replaces with bundle verbs + convention dirs).
- `internal/monitor/` units resolution/routing/seeding (`UnitsMarkerFile` ledger
  survives conceptually as the seeded-set union ledger in the new model).
- `internal/consts/name.go` — unified naming rule.
- `docs/stack-contract.mdx` (E1–E22/A1–A11 + conformance badges) and user docs —
  will be rewritten during implementation of the new model; do not treat as law.

## Files in this folder

| file | purpose |
|---|---|
| `harness-research-opencode-pi.md` | Live-verified recon for opencode + pi harness bundles (research facts — still valid). |

## Open work

- Implement the new bundle/install model (see the brainstorm memory — decisions,
  testing mandate, open items).
- Skills-plugin multi-agent packaging — merge blocker for the branch.
- Harness template block names still placeholder `block_1..6` — final names undecided (user's call).
- opencode/pi bundles (research memo ready) — after the new model lands.

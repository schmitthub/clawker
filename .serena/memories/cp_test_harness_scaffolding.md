# CP Test Harness Scaffolding

> **Status:** In Progress — Design finalized, scaffolding compiles, user fleshing out
> **Parent memories:** `clawkerd-container-control-plane`, `clawkerd-cp-testing-infrastructure`
> **Brainstorm memory:** `brainstorm_controlplane-testing-infrastructure`
> **Branch:** `feature/clawkerd`
> **Last rebased:** 2026-02-17 onto latest `main` (clean linear history, force pushed)

## End Goal

Scaffold the new CP test harness in `test/controlplane/harness/` that lets tests interact with clawker the same way users do — through the real CLI command chain.

## What Was Done

### Brainstorm & Design (COMPLETE)
- Researched K8s, containerd, testcontainers-go testing patterns via deepwiki
- Established tier model: **tiers are lifecycle stages**, not infrastructure layers
  - Tier 1: resource creation scheduling (CLI → CP → Docker)
  - Tier 2: startup operations (registration, init, ready signals, final state)
  - Tier 3+: TBD (runtime updates, shutdown, log streaming, peer comms)
- Key principle: **we don't modify ANYTHING prod code does**. Each tier decides when to use test doubles/seams.
- All decisions captured in `clawkerd-cp-testing-infrastructure` memory
- Brainstorm memory `brainstorm_controlplane-testing-infrastructure` synced with decisions

### Harness Scaffolding (COMPILES, user fleshing out)
- **`test/controlplane/harness/harness.go`** — DONE, compiles
  - `ClawkerCLI` struct wrapping `*cobra.Command` from `root.NewCmdRoot`
  - `NewClawkerCLI(f, version, buildDate)` constructs it identically to production entry point
  - Callers navigate subcommands and call Execute() — same as real user
- **`test/controlplane/harness/factory/default.go`** — DONE, compiles
  - `factory.New(version, opts...)` returns single `*cmdutil.Factory`
  - Test IOStreams set on `f.IOStreams` (NOT returned separately)
  - Real lazy Docker client with BuildKit, real config from cwd, host proxy, socket bridge
  - `WithConfig()` option for caller override
  - Mirrors production `internal/cmd/factory/default.go` wiring

### Design Decisions Finalized (2026-02-17 session)
- **No god-object harness** — tests wire what they need directly
- **Factory is a direct-use API** — tests call `factory.New()` themselves, not through a harness struct
- **`ClawkerCLI` is a thin type** — just wraps `*cobra.Command`, no methods yet
- **No `Harness` struct with `*testing.T`** — don't add until pain is real (passing same 3-4 things to every helper = signal to promote to struct)
- **Standalone functions + factory** — add helpers alongside as patterns emerge
- **Promote to struct when pain is real** — not before

## Pending TODOs

1. [ ] **User fleshing out ClawkerCLI** — User is coding this. Add convenience methods, subcommand access patterns as needed.
2. [ ] **Testdata config files** — Need `test/controlplane/testdata/clawker.yaml` and `testdata/settings.yaml` for the factory's config loader. Currently factory uses `config.NewConfig()` (loads from cwd).
3. [ ] **Migrate POC test** — Decompose existing `controlplane_test.go` into Tier 1 and Tier 2 test functions using new harness.
4. [ ] **Dockerfile.tmpl clawkerd stage** — Add multistage builder for clawkerd binary (production change). Same pattern as `callback-forwarder-builder` and `socket-server-builder`. Until this lands, test image is built via hand-rolled `testdata/Dockerfile`.
5. [ ] **Switch image build to real pipeline** — Once clawkerd stage is in Dockerfile.tmpl, image build goes through `cli.Run("image", "build")` instead of hand-rolled Dockerfile.
6. [ ] **CP `WaitForRegistration` API** — Expose event-driven registration wait on Server (currently tests poll with `assert.Eventually`).
7. [ ] **Tier 3+ design** — Defer until Tier 2 is solid.

## Key Design Decisions (DO NOT DEVIATE)

- **NO imports from `test/harness`** — Copy what's needed ctrl+c/ctrl+v style, never import the old harness
- **No god-object harness** — Tests wire dependencies directly via `factory.New()` + `NewClawkerCLI()`
- **Factory is direct-use API** — Tests call it themselves, not through a wrapper
- **Don't add `Harness` struct until pain is real** — Signal: passing same args to every helper
- **ClawkerCLI wraps real `*cobra.Command`** — Callers access subcommands directly and call Execute()
- **Factory returns single value** — `*cmdutil.Factory` only. IOStreams is a field on Factory, not a separate return
- **Factory mirrors production** — Same lazy closures, same wiring as `internal/cmd/factory/default.go`
- **Real build pipeline** — Harness builds images through full Cobra chain (once Dockerfile.tmpl has clawkerd stage)
- **Tiers are lifecycle stages** — Not test infrastructure layers. See `clawkerd-cp-testing-infrastructure` memory for full details

## Lessons Learned

- Do NOT cargo-cult patterns from the old harness. The new harness is its own thing with its own design.
- Do NOT return test IOStreams separately — it's a field on Factory.
- Do NOT inject `testing.T` into core harness construction. Production code doesn't take `testing.T`.
- Do NOT build a comprehensive harness upfront. Scaffold minimal, let user flesh it out, copy from old harness as needed.
- Do NOT create a god-object `Harness` struct — option hell (`WithThis WithThat`) is worse than a few lines of wiring.
- Promote standalone functions to a struct only when you find yourself passing the same 3-4 things everywhere.
- User is extremely direct. Don't hedge, don't over-engineer, don't be "cautious". Just build what's asked for.

## IMPERATIVE

Always check with the user before proceeding with the next todo item. If all work is done, ask the user if they want to delete this memory.

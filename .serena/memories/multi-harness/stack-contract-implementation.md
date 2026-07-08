# Stack Contract Implementation Initiative

**Branch:** `feat/multi-harness-support`
**Parent memory:** `multi-harness/README`
**Design reference:** serena `multi-harness/stack-unit-contract-model` (r10 — THE authoritative design; read §1–§6c before ANY task). History/rationale: `multi-harness/stack-unit-contract-design` (superseded vocabulary — rationale only). Blast radius: `multi-harness/blast-radius`.

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Config schema — registries + per-harness overlay + naming validator | `complete` (2026-07-08) | subagent |
| Task 2: Resolution rewiring — lineage lookup, override semantics, bundled stacks, provenance | `pending` | — |
| Task 3: Per-harness overlay rendering — overlay stacks/packages/inject in harness image | `pending` | — |
| Task 4: CLI front-door — `clawker stack|harness register/list/remove` | `pending` | — |
| Task 5: BaseContentHash build-arg fix | `pending` | — |
| Task 6: Docs + contract surface | `pending` | — |

## Key Learnings

### Task 1 (2026-07-08)

- **Naming-collision resolved by folding, not a new top-level key**: `Project.Harnesses map[string]HarnessConfig` (top-level `harnesses:`) ALREADY EXISTED pre-initiative for per-harness init config (mount_projects/env/post_init/pre_run/config). The design's "HARNESS REGISTRY: name → path" was drafted without accounting for this. Resolution: added `Path string` to the EXISTING `HarnessConfig` struct rather than inventing a second `harnesses:` node (which would have collided on the same YAML key at the same level in the same file). One map now serves both registration (`path:`) and per-harness init config — they're both scoped by the same harness name in the same file, so unifying is natural, not a hack. `Project.Stacks map[string]StackRegistryEntry` (new `stacks:` top-level) had no such collision — added fresh. **Task 2/3/4 implementers: read `harnesses.<name>.path` via the existing `HarnessConfig`, not a separate type.**
- **Settings-side `Settings.Stacks`/`Settings.Harnesses` NOT hard-cut in Task 1** — deliberately deferred. Task 1's memory text says "hard-cut acceptable... note in CHANGELOG" but Task 2's own Implementation Phase step 1 explicitly owns "remove settings-registry seeding from EnsureStacks/EnsureHarnesses". Doing the schema-level removal in Task 1 without Task 2's lineage-lookup replacement ready would leave `internal/bundler` non-compiling (or require pulling Task 2's resolution rewrite into Task 1) and would functionally break harness/stack resolution mid-initiative. Task 1 is additive-only: new project-side schema lands beside the untouched settings-side registry. **Task 2 must perform the actual settings-schema removal + consumer rewire, and whichever task lands that removal must add the CHANGELOG hard-cut note** — it was NOT added in Task 1 since nothing was actually cut yet.
- **Shared naming validator lives in `internal/consts`** (`consts.ValidateName` / `consts.ValidateHarnessName`), not in `internal/bundler` or `internal/stack` — those two packages, plus the new `internal/config` front-door, all need it and `internal/config` sits below both in the import DAG (bundler/stack import config, not vice versa), so `internal/consts` (stdlib-only leaf) is the only cycle-free common home. `bundler.ValidateHarnessKey` and `stack.ValidateName` are now thin wrappers delegating to it — call sites unchanged. Regex: `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, max 32 (`consts.NameMaxLength`). `ValidateHarnessName` additionally rejects the 3 reserved image-tag aliases (`consts.ImageTagDefaultAlias/Latest/Base`).
- **Unknown-key rejection required new front-door code — `internal/storage` has no hook for it.** Confirmed via research: storage's generic node merge deliberately lets unknown keys survive project-wide (`internal/storage` CLAUDE.md: "Unknown keys survive"); there's no `WithValidate` option and `yaml.Node.Decode` isn't strict. Built `internal/config/validate.go::validateProjectRegistries(*storage.Store[Project])`, wired into `NewConfig`/`NewFromString`/`NewBlankConfig`, which walks `Store[Project].Layers()` (`[]LayerInfo{Filename, Path, Data map[string]any}`) PER LAYER — giving real file provenance for free without any storage-engine changes — and checks `stacks.<name>`, `harnesses.<name>`, `build.harnesses.<name>` (+ nested `.inject`) against explicit known-field allowlists. This is a deliberate, narrow exception to the project-wide permissive-unknown-keys rule, scoped only to these 3 new/touched nodes.
- **JSON Schema generation (`cmd/gen-docs`) already recurses fully into `KindStructMap` fields** (unlike the markdown doc generator, which renders them as an opaque `<value>`) — the generated `clawker.schema.json` for `stacks:`/`harnesses:`/`build.harnesses:` already carries `additionalProperties: false` at every nesting level, independently corroborating the front-door validator's known-field lists (cross-checked against the generated schema and matched exactly).
- **Path shape validation is load-time-only, not project-root-anchored** — `validatePathValue` rejects empty/`~`/`$VAR` but does NOT resolve or check existence against a project root (config doesn't reliably have one at validate-time, and "parsing stays dumb" per design). Actual resolution + existence checking against project root is Task 2/4's job (bundler consumption, `clawker stack register` CLI validation).
- `stack.ValidateName`'s old regex (`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,40}$`) and `bundler.ValidateHarnessKey`'s old regex (docker-tag grammar, `^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`) were both STRICTER-replaced by the unified rule — verified no existing test or shipped name (`go`/`node`/`python`/`rust`/`claude`/`codex`) breaks under the new rule before landing.
- **YAML null nodes are valid empty mappings — every raw-node shape check must tolerate them.** A bare `build:` line (key with no content) decodes as nil in `LayerInfo.Data`, and the typed decode accepts it as the zero struct. The first cut of the front-door validator rejected it as "must be a mapping" and broke an existing bundler test fixture. Fixed with a `nodeMapping(raw) (map[string]any, bool)` helper (nil → empty mapping) used at every mapping assertion. Any future raw-node validation (Task 2's resolution errors, Task 4's register front-door) must do the same.
- **Shape-error tests for the per-layer validator need a two-layer fixture.** With a single layer (NewFromString), storage's typed decode of the merged tree fails FIRST on a malformed shape — the validator never runs. The validator's load-bearing scenario is a malformed value in a LOSING layer shadowed by a valid winning layer: the merged tree decodes cleanly and only the per-layer walk flags the losing file. `TestValidateProjectRegistries_MalformedShapes` builds exactly that (two `WithPaths` dirs, valid winner shadowing every malformed key).
- **Review-round fixes applied** (all 6 subagents run, findings fixed): malformed `build:` node now errors instead of silently skipping (silent-failure CRITICAL); `NewProjectStoreFromPreset` now also calls `validateProjectRegistries` (was the one asymmetric constructor); `harnesses.<name>.config` sub-block now validated (unknown fields + `strategy` ∈ {copy, fresh}; a typo'd strategy previously decoded silently to the default); `required:"true"` added to `StackRegistryEntry.Path` (tag contract now matches runtime enforcement, JSON schema gets `"required": ["path"]`); drift-guard test `TestKnownFieldSets_MatchSchemaTags` reflects each `known*Fields` allowlist against its struct's yaml tags; phantom-coverage test deleted; `layerLabel` comment mechanism-framed; CLAUDE.md initiative-pointer replaced with self-contained statement.
- **Review findings deliberately NOT adopted (with reasons)**: (a) type-design's suggestion to embed a `HarnessRegistryEntry` via `yaml:",inline"` in `HarnessConfig` — storage's `NormalizeFields`/`normalizeStruct` has no anonymous-field handling, so an inline embed would derive the wrong field path (`harnesses.harnessregistryentry.path`) and corrupt Fields()/schema/doc generation; doing it right means a storage-engine change, out of Task 1 scope (candidate if Task 2 wants a shared registry-entry type). (b) comment-analyzer's request to mark schema doc comments "not yet implemented" — those comments state the r10 contract the schema declares (render order, no-dedupe, per-harness scoping); adding current-state markers violates the no-current-state-in-comments convention and would rot mid-branch; the branch is merge-blocked until Tasks 2–3 implement the contract, and this memory is the implementation-state ledger. **Task 2/3 agents: the schema doc comments in `internal/config/schema.go` on Stacks/Harnesses/HarnessBuildOverlay/HarnessOverlayInject/HarnessConfig.Path are the authored contract you implement — comment-analyzer verified none of it is wired yet.**
- **`validateProjectRegistries` is NOT on the `Set`/`Write` mutation path.** Task 4's `clawker stack|harness register` commands must invoke it (or the per-value validators) themselves before `Write()` — the store engine has no validation hook.
- **Registry maps merge last-wins across layers, not union** (no `merge:"union"` tag on `Project.Stacks`/`Project.Harnesses`/`Build.Harnesses`) — a closer clawker.yaml's map replaces a parent's wholesale, consistent with the pre-existing `Harnesses` behavior. Task 2 should confirm this is the intended registry resolution semantics (it matches "matching key at closer layer wins wholesale" at the map-entry level only per-file, not per-entry across files).
- Full acceptance criteria green after review fixes: `go build ./...`, `make test` (5576 tests, 6 pre-existing environment-gated skips, 0 failures), `go test ./internal/config/... -v` (89 passing), `go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas` + `TestConfigSchemasUpToDate` green, doc diffs reviewed (`docs/configuration.mdx`, `docs/schemas/clawker.schema.json`).

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents to review this task's changes, then fix any and all findings
5. Commit all changes from this task with a descriptive commit message
6. Present the handoff prompt from the task's Wrap Up section to the user
7. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

r10 stack-contract design (serena `multi-harness/stack-unit-contract-model`) replaces the settings-registry + collision-error model with: **named keys + layered store + per-lineage scope**. Core laws:

- Lookup chain per lineage: base = project > shipped; base+harness X = project (incl. `build.harnesses.X` overlay) > X bundle's `stacks/` dir > shipped. Settings layer is DEAD for this subsystem.
- Matching key at closer layer wins WHOLESALE (never merge). Same-layer collision blocked at register front-door only.
- **Cross-stratum dedup KILLED**: project-declared stacks render in base; harness-declared stacks ALWAYS render in the harness image with their lineage-resolved definition. Engine NEVER judges whether a base render "satisfies" a harness declaration — satisfaction logic lives inside fragments (self-guards, apt idempotence, PATH shadowing).
- Ordering: declaration order, top to bottom, no dep graph, no topo sort. Within harness image: bundle installer stacks → project overlay stacks.
- Registration = path reference in clawker.yaml (relative-from-project-root or absolute; NO env/~ expansion). No remote fetch, ever.
- Parameterization = Docker `--build-arg` against author-declared ARGs, nothing in config schema.
- Manifest minimalism: harness.yaml only declares what the engine must know; anything expressible as Dockerfile content gets NO manifest key.
- Provenance ALWAYS printed in build output when a closer layer shadows a farther one.
- Naming: one rule for stacks + harnesses — `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, max 32, dir name IS the name.

Target config schema (design §6):

```yaml
# clawker.yaml
stacks:                          # STACK REGISTRY: name → path
  my-rust: { path: ./stacks/my-rust }
harnesses:                       # HARNESS REGISTRY: name → path
  codex: { path: ./tools/codex-bundle }
build:
  packages: [...]                # base apt
  stacks: [go, my-rust]          # base declaration, renders in this order
  harnesses:                     # per-harness overlay: same primitive trio
    claude:
      stacks: [bun]              # after bundle's installer stacks
      packages: [libnss3]        # apt RUN in claude image; NO dedupe vs base (apt idempotence)
      inject: { after_harness_install: [...], before_entrypoint: [...] }
```

### Key Files

- `internal/bundler/stack.go` — resolveStack/resolveProjectStacks/resolveHarnessStacks/splitFragments/renderStackSteps, EnsureStacks, stackCollisionError, checkBundleShadow
- `internal/bundler/harness.go` — EnsureHarnesses, ResolveHarnessName, ValidateHarnessKey, LoadHarness, HarnessBundleDir
- `internal/bundler/dockerfile.go` — ProjectGenerator, DockerfileContext, filterBasePackages
- `internal/bundler/basehash.go` — BaseContentHash
- `internal/bundler/assets/Dockerfile.base.tmpl`, `Dockerfile.harness-image.tmpl` — render slots
- `internal/bundler/assets/harnesses/{claude,codex}/`, `assets/stacks/{go,node,python,rust}/` — shipped defs (become the virtual base layer)
- `internal/config/` — schema types, ProjectStore/SettingsStore, WithProjectRoot anchor (read `internal/config/CLAUDE.md` first)
- `internal/harness/` — Bundle/manifest types, Compose, consts (block names, inject point names)
- `internal/stack/` — stack Definition loading
- `internal/cmd/` — command packages; Factory pattern (`internal/cmd/factory/`)
- `internal/docker/` — Builder (base-skip decision consuming BaseContentHash)
- `.claude/docs/DESIGN.md`, `.claude/docs/ARCHITECTURE.md` — update if touched surfaces change

### Design Patterns

- Factory noun pattern for commands (`NewCmd(f, runF)`); typed config mutation via `ProjectStore().Set/Write()`
- Tests: `configmocks.NewFromString(projectYAML, settingsYAML)`, bundler's `testConfig(t, projectYAML)` helper; golden files for rendered Dockerfiles (`GOLDEN_UPDATE=1`)
- moq mocks via `go generate`; black-box tests + subpackage mocks standard
- Validation at write front-door with provenance-naming errors, never silent ignore
- Output: stdout data / stderr warnings, `cs.WarningIcon()` style (`.claude/rules/code-style.md`)

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package `CLAUDE.md` before starting
- Read serena `multi-harness/stack-unit-contract-model` FIRST — it is the spec; do not resurrect token/provider vocabulary or cross-stratum dedup
- Use Serena tools for code exploration — read symbol bodies only when needed
- TDD — tests before code; all tests must pass. **NEVER `go test ./...` in a clawker container — use `make test` or targeted packages**
- `make clawker` (embeds) before pre-commit; never `--no-verify`
- All new code must compile; golden files regenerated deliberately, diffs reviewed

---

## Task 1: Config schema — registries + per-harness overlay + naming validator

**Creates/modifies:** `internal/config/` (schema types + accessors), `internal/bundler/harness.go` (ValidateHarnessKey → shared rule), new shared name validator, JSON schema regen
**Depends on:** —

### Implementation Phase

1. Add top-level `stacks:` and `harnesses:` registry maps to the clawker.yaml project schema (name → `{ path: string }`). Path validation: relative (resolved from project root) or absolute; reject env/`~` forms with a clear error.
2. Add `build.harnesses.<name>.{stacks,packages,inject}` overlay schema — `inject` reuses the existing harness-image inject point names (`after_harness_install`, `before_entrypoint`).
3. Unify the name rule: one validator, `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$` max 32, used for stack names, harness names, registry keys, and overlay keys. Replace/extend `ValidateHarnessKey`.
4. Config accessors per `internal/config/CLAUDE.md` conventions (no hardcoded key strings in consumers). Settings-side `stacks:`/`harnesses:` registry schema: mark deprecated/removed — decide with the store layer whether reads fall back during migration or hard-cut (alpha: hard-cut acceptable, note in CHANGELOG).
5. Validation front-door: unknown keys under the new nodes = error with provenance (file + key path). Registered name failing the slug rule = error at load.
6. Regenerate JSON schemas (`go run ./cmd/gen-docs --schemas`).

### Acceptance Criteria

```bash
go build ./... && make test
go test ./internal/config/... -v
go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas && git diff --stat docs/
```

### Wrap Up

1. Update Progress Tracker: Task 1 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 2. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the stack-contract implementation initiative. Read the Serena memory `multi-harness/stack-contract-implementation` — Task 1 is complete. Begin Task 2: Resolution rewiring."

---

## Task 2: Resolution rewiring — lineage lookup, override semantics, bundled stacks, provenance

**Creates/modifies:** `internal/bundler/stack.go`, `internal/bundler/harness.go`, `internal/stack/`, `internal/harness/` (bundle `stacks/` dir loading)
**Depends on:** Task 1 (schema accessors)

### Implementation Phase

1. Rewrite stack resolution to the lineage lookup chain: base = project `stacks:` registry > shipped embedded; harness lineage = project (incl. overlay) > bundle's `stacks/<name>/` dir > shipped. Shipped defs resolve directly from the embedded FS (virtual base layer) — remove settings-registry seeding from `EnsureStacks`/`EnsureHarnesses`. Decide the fate of config-dir materialization: keep ONLY as fork convenience if cheap, else drop (materialized copies are no longer part of resolution).
2. Add bundle `stacks/<name>/` dir discovery to bundle loading (dir name IS the stack name; validate against the slug rule).
3. Kill `stackCollisionError` flat-namespace semantics and `checkBundleShadow` — matching key at closer layer wins wholesale.
4. Kill `resolveHarnessStacks`' project-declared skip — harness-declared stacks ALWAYS render in the harness image with their lineage-resolved definition (cross-stratum dedup is dead; design §2 Placement).
5. Provenance output: every resolution where a closer layer shadows a farther one emits a build-output line (`stack node ← project (./stacks/node) shadows claude bundle`); every harness resolution names its source. Wire through to `clawker build` stderr.
6. Unresolvable name = error with guidance (`clawker stack register <path> --name <n>`), naming the declaring file.

### Acceptance Criteria

```bash
go build ./... && make test
go test ./internal/bundler/... ./internal/stack/... ./internal/harness/... -v
GOLDEN_UPDATE=1 go test ./internal/bundler/ -run TestGenerate_Golden && git diff internal/bundler/testdata/golden/  # review diffs deliberately
```

### Wrap Up

1. Update Progress Tracker: Task 2 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue the stack-contract implementation initiative. Read the Serena memory `multi-harness/stack-contract-implementation` — Tasks 1–2 are complete. Begin Task 3: Per-harness overlay rendering."

---

## Task 3: Per-harness overlay rendering — overlay stacks/packages/inject in harness image

**Creates/modifies:** `internal/bundler/dockerfile.go` (DockerfileContext + generator), `internal/bundler/assets/Dockerfile.harness-image.tmpl`
**Depends on:** Tasks 1–2

### Implementation Phase

1. Overlay stacks: render in the harness image AFTER the bundle's installer stacks (installer → overlay), declaration order within each source.
2. Overlay packages: apt RUN slot in the harness-image template (early root scope, blocks 1–2 region). NO dedupe against base/project lists — render as declared, apt idempotence handles overlap. Respect BuildKit cache-mount emission parity with the base template.
3. Per-harness inject: overlay `inject.after_harness_install` / `inject.before_entrypoint` render ONLY for that harness's image, appended after any global project inject at the same points (declaration order law).
4. Extend golden tests: a fixture project with overlay trio for one harness and not another; assert the other harness image stays clean.

### Acceptance Criteria

```bash
go build ./... && make test
go test ./internal/bundler/... -v
GOLDEN_UPDATE=1 go test ./internal/bundler/ -run TestGenerate_Golden && git diff internal/bundler/testdata/golden/
go test ./test/whail/... -v -timeout 5m   # Docker-backed render/build checks (host only; skip in-container)
```

### Wrap Up

1. Update Progress Tracker: Task 3 -> `complete`
2. Append key learnings
3. Run review subagents (same set), fix all findings.
4. Commit.
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue the stack-contract implementation initiative. Read the Serena memory `multi-harness/stack-contract-implementation` — Tasks 1–3 are complete. Begin Task 4: CLI front-door."

---

## Task 4: CLI front-door — `clawker stack|harness register/list/remove`

**Creates/modifies:** `internal/cmd/stack/` (new), `internal/cmd/harness/` (new or extend existing harness cmd tree), root command wiring, `internal/cmd/factory/` if a new noun is needed
**Depends on:** Tasks 1–2

### Implementation Phase

1. `clawker stack register <path> [--name <n>]`: validate dir (stack.yaml + ≥1 fragment), derive name from dir name unless `--name`, slug-validate, write `stacks.<name>.path` to clawker.yaml via `ProjectStore().Set` + `Write()`. Existing key = error; `--force` replaces and prints what was shadowed (old vs new path).
2. `clawker harness register <path> [--name <n>]`: same shape; validate bundle (harness.yaml + fragment), discover bundled `stacks/` and report them in output.
3. `list`: table via `f.TUI.NewTable` — name, path, source layer (project/shipped), shadows-what. `--format json` support.
4. `remove <name>`: deletes the registry entry (project layer only; shipped names can't be removed, only shadowed — error explains).
5. gh-style output conventions per `.claude/rules/code-style.md`; errors typed to Main(); `Example` fields; `PersistentPreRunE`.
6. Command tests with `configmocks.NewIsolatedTestConfig(t)`.

### Acceptance Criteria

```bash
go build -o bin/clawker ./cmd/clawker && make test
go test ./internal/cmd/stack/... ./internal/cmd/harness/... -v
./bin/clawker stack register --help && ./bin/clawker harness list --help
```

### Wrap Up

1. Update Progress Tracker: Task 4 -> `complete`
2. Append key learnings
3. Run review subagents (same set), fix all findings.
4. Commit.
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue the stack-contract implementation initiative. Read the Serena memory `multi-harness/stack-contract-implementation` — Tasks 1–4 are complete. Begin Task 5: BaseContentHash build-arg fix."

---

## Task 5: BaseContentHash build-arg fix

**Creates/modifies:** `internal/bundler/basehash.go`, `internal/docker/` Builder (build-arg plumbing to the hash), `internal/cmd/image/build/`
**Depends on:** — (independent; sequenced late to avoid churn under Tasks 2–3 golden updates)

### Implementation Phase

1. Problem (design §6b item 9): `--build-arg` targeting a base-Dockerfile ARG is silently eaten when BaseContentHash matches — builder skips `docker build` entirely; flag never reaches Docker. Vanilla BuildKit would honor it (arg values are cache-keyed). Clawker must not break Docker standards.
2. Fix: fold the EFFECTIVE values of build-args that the rendered base Dockerfile actually declares (parse `ARG` names from rendered bytes ∩ user-supplied `--build-arg` keys) into `BaseContentHash`. Harness-only args stay out so they never force base rebuilds.
3. Tests: same rendered Dockerfile + differing relevant build-arg → different hash; irrelevant build-arg → same hash; no build-args → hash unchanged from today (no gratuitous rebuild for existing users).

### Acceptance Criteria

```bash
go build ./... && make test
go test ./internal/bundler/ -run TestBaseContentHash -v
go test ./internal/docker/... -v
```

### Wrap Up

1. Update Progress Tracker: Task 5 -> `complete`
2. Append key learnings
3. Run review subagents (same set), fix all findings.
4. Commit.
5. **STOP.** Present handoff:

> **Next agent prompt:** "Continue the stack-contract implementation initiative. Read the Serena memory `multi-harness/stack-contract-implementation` — Tasks 1–5 are complete. Begin Task 6: Docs + contract surface."

---

## Task 6: Docs + contract surface

**Creates/modifies:** `docs/` (Mintlify), `internal/*/CLAUDE.md` touched by Tasks 1–5, `.claude/docs/DESIGN.md`/`ARCHITECTURE.md`, `claude-plugin/clawker-support/skills/clawker-support/` reference docs, CHANGELOG
**Depends on:** Tasks 1–5

### Implementation Phase

1. Contract docs: the versioned public surface — injection points (base + harness image, incl. per-harness scoping), stack slots, ordering law (declaration order; installer → overlay), placement law (two strata, zero interaction, fragments self-guard), lookup chain + provenance, naming rule, path rules, `--build-arg` parameterization pattern. This is the systemd-lesson surface: write it as the document users author stacks/bundles against.
2. CLI reference regen (`go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas`).
3. Update `internal/bundler/CLAUDE.md`, `internal/config/CLAUDE.md`, other touched package docs; sweep clawker-support skill reference (registration commands, new schema, removed settings registry) per `feedback_docs_sweep_includes_support_skill`.
4. CHANGELOG entry (flat bullets, curated user feed). Migration note for the settings-registry hard-cut (alpha-brief per `feedback_alpha_no_user_migration_docs`).
5. Update serena memories: `multi-harness/stack-unit-contract-model` (mark implemented), `multi-harness/README`, this memory.

### Acceptance Criteria

```bash
go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas && git status docs/
npx mintlify dev --docs-directory docs   # spot-check renders locally (user can verify)
make pre-commit
```

### Wrap Up

1. Update Progress Tracker: Task 6 -> `complete`
2. Append key learnings
3. Run review subagents (same set), fix all findings.
4. Commit.
5. **STOP.** Initiative complete — inform the user. Remaining OUT-OF-SCOPE follow-ups: monitoring breakout (`clawker monitor install` ledger — own design pass, see model doc §7 item 7), apt-into-stacks fold (carried, low), host UAT of the full flow.

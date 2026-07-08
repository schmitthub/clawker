# Stack Contract Implementation Initiative

**Branch:** `feat/multi-harness-support`
**Parent memory:** `multi-harness/README`
**Design reference:** serena `multi-harness/stack-unit-contract-model` (r10 — THE authoritative design; read §1–§6c before ANY task). History/rationale: `multi-harness/stack-unit-contract-design` (superseded vocabulary — rationale only). Blast radius: `multi-harness/blast-radius`.

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Config schema — registries + per-harness overlay + naming validator | `complete` (2026-07-08) | subagent |
| Task 2: Resolution rewiring — lineage lookup, override semantics, bundled stacks, provenance | `complete` (2026-07-08) | subagent |
| Task 3: Per-harness overlay rendering — overlay stacks/packages/inject in harness image | `complete` (2026-07-08) | subagent |
| Task 4: CLI front-door — `clawker stack|harness register/list/remove` | `complete` (2026-07-08) | subagent |
| Task 5: BaseContentHash build-arg fix | `complete` (2026-07-08) | subagent |
| Task 6: Docs + contract surface | `complete` (2026-07-08) | subagent |

## Post-initiative review (orchestrator, 2026-07-08)

Whole-initiative review of `889d561d..025ca272` completed after Task 6: every task independently gate-verified (full diff read + full suite in an isolated worktree at each task's commit + live CLI smoke for Task 4/6), goldens stable under regen at final HEAD, docs claims cross-checked against observed CLI behavior. Fixes landed as `ac59e0ed`: register commands print "(was <old>)" only when the path actually changed (old "(replaced)" wording was wrong for parent-layer shadowing and noisy for same-path --force); stacks/harness-bundles docs dropped the spurious `--force` from the override-a-shipped-name examples (shadowing shipped is an ordinary registration). Known accepted wart (documented in docs Note + Task 4 learnings): registering from an unregistered directory writes to the user-level clawker.yaml — surfaced via the "Written to" line; a parent-layer registration also blocks same-name project register without --force.

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
- **Lint gotchas for later tasks** (`.golangci.yml` uses `new-from-merge-base: main` — ALL new code is fully linted even where old code isn't): new package-level `var` maps trip gochecknoglobals (use functions returning the map); test files in `package config` trip testpackage unless named `*_internal_test.go`; empty struct literals trip exhaustruct (use `reflect.TypeFor[T]()` in reflection tests); adding a field to a schema struct ripples into every exhaustive test literal of that struct (`internal/cmd/container/shared/{agentenv,containerfs}_test.go` gained `Path: ""`); cross-package error returns trip wrapcheck (wrap `fmt.Errorf("harness %w", err)`); govulncheck hook blocks commits on ANY known vuln — goldmark bumped v1.7.13→v1.7.17 (GO-2026-5320) as part of this commit.
- Full acceptance criteria green after review fixes: `go build ./...`, `make test` (5576 tests, 6 pre-existing environment-gated skips, 0 failures), `go test ./internal/config/... -v` (all passing incl. new suites), `go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas` + `TestConfigSchemasUpToDate` green, doc diffs reviewed, full pre-commit hook chain green. Committed as `e0bfd3f4` on `feat/multi-harness-support` (not pushed).

### Task 2 (2026-07-08)

- **Config-dir materialization DROPPED entirely** (option "drop" from the task's decide-fate item): `EnsureStacks`/`EnsureHarnesses`, `harness.Materialize`/`ContentHash`/`MaterializedStale`, `ShippedStampFile`, `HarnessesSubdir`, `ShippedStackDefaultDir`/`ShippedBundleDefaultDir` all deleted (`internal/harness/materialize.go` removed). Shipped defs resolve straight from the embedded FS as the virtual base layer. Fork convenience will come from Task 4's `clawker stack|harness register` front-door instead (it can copy/scaffold if desired). `harness.FileMode` kept — build-context staging still uses it.
- **Bundle `stacks/<name>/` discovery already existed** (`Bundle.HasStack`/`Bundle.Stack` from the pre-initiative code, keyed by dir name via `stack.StacksSubdir`); Task 2 only rewired precedence around it — no new bundle-loading code was needed. `validateStackDecls` at bundle load enforces the slug rule on manifest declarations; dir names are validated by `stack.Load` → `stack.ValidateName` when a bundle stack is actually loaded.
- **New `config.Config.ProjectRoot() string` accessor** — returns the `WithProjectRoot` walk-up anchor ("" when walk-up disabled: CP/host-proxy/bridge daemons, in-memory test doubles). This is how relative registry paths resolve: `bundler.resolveRegistryPath(cfg, key, path)` joins relative entries onto it and **hard-errors when the root is empty and the path is relative** (silent-failure review finding: CWD fallback = silent-wrong-load risk). `configmocks.newMockFrom` wires `ProjectRootFunc`; tests override it (`cfg.ProjectRootFunc = func() string { return root }`) to exercise relative paths.
- **Registry default flag is GONE with the settings registry**: `ResolveHarnessName(cfg, "")` now returns the built-in `DefaultHarnessName` unconditionally (validated when explicit). `registryDefaultHarness`, `HarnessBundleDir`, `seedShippedEntries` deleted. `harnesses.<name>` project entries carry NO default flag — if a project-selectable default harness is wanted later it's a new design decision (candidate: Task 4 or a `build.harness` key).
- **`KnownHarnessNames`/`IsKnownHarness` (bundler) replaced build.go's `registeredHarnessNames`/`isKnownHarness`**: shipped ∪ project-registered (`harnesses.<name>.path != ""`). An init-config-only `harnesses.<name>` entry (mount_projects/env/... but no path) is NOT a registration — the path guard is load-bearing and has a dedicated test.
- **Settings-side registry hard-cut landed here** (deferred from Task 1): `Settings.Stacks`/`Settings.Harnesses` + `StackSettings`/`HarnessSettings` deleted from `internal/config/schema.go`; new settings migration `migrateRemoveLegacyRegistryKeys` strips leftover `stacks:`/`harnesses:` keys with a one-shot stderr notice (storage preserves unknown keys on re-save, so without the migration they'd linger looking live). CHANGELOG 0.13.0 bullets updated to the project-side model + a Changed bullet for the cut. `settings.schema.json` + `configuration.mdx` regenerated (registry blocks dropped).
- **Provenance plumbing**: `stackProvenance` (shadows []string, gated by `noteworthy()`) and `harnessProvenance` (shadows bool, ALWAYS printed) are deliberately separate types — reviewer-evaluated, unification rejected (different arity + gating). `ProjectGenerator.Provenance()` dedupes accumulated lines; `docker.Builder` snapshots it after GenerateBase (survives later failures) and again after GenerateHarness; `build.go printProvenance` prints to stderr with `cs.InfoIcon()` AFTER the TUI display tears down (both suppressed + progress paths). Line formats: `stack node ← other bundle shadows shipped`, `harness codex ← ./tools/codex (project registry) shadows shipped`, `harness claude ← shipped`.
- **`loadHarnessResolved` is package-private** (type-design review: exported func returning unexported type + no external caller). `LoadHarness` stays the public entry; Task 4's `list` command (needs source-layer info) should export a proper provenance surface deliberately if required — do not resurrect the accidental export.
- **Cross-stratum dedup removal is pinned by `TestGenerate_BothDeclared_BothRender`** (node in build.stacks + harness manifest → renders in BOTH images). `resolveHarnessStacks` no longer takes projectDecls; `stackCollisionError`/`checkBundleShadow` deleted — closer layer wins wholesale, collision errors replaced by shadow provenance.
- **Golden files show ZERO diff — expected, verified deliberately**: the golden scenarios declare no project stacks/harness registrations, and the default claude bundle resolves from the embedded FS byte-identically to the old materialized-copy path. Behavioral changes are covered by unit tests, not goldens.
- **Shared decl-resolution helper**: `resolveStackDecls(cfg, bundle, decls, dupIsError)` backs both `resolveProjectStacks` (dup = error) and `resolveHarnessStacks` (dup = render-once skip). Reviewer-driven consolidation.
- **Review findings deliberately declined**: (a) forward-referenced `clawker stack|harness register` commands in error text — Task 4 ships them before merge, branch is merge-blocked anyway; (b) provenance on stderr vs stdout — stderr is the initiative-specified surface ("wire through to clawker build stderr") and keeps stdout machine-clean; (c) no error sentinel for unresolvable harness (stacks have `ErrUnknownStack`) — mirrors pre-existing pattern, revisit only if a caller needs `errors.Is`; (d) provenance `source` strings are pre-formatted rather than structured — they exist solely to become one stderr line.
- **Gotcha for Task 3/4 agents**: `build.harnesses.<name>.stacks` overlay is STILL not rendered (`GenerateHarness` passes only `bundle.Manifest.Stacks`) — that is Task 3's whole job, not a Task 2 regression. Also `egress.go`'s floor composition still keys off `ResolveHarnessName(cfg, "")` = built-in default until per-container identity threading lands.
- Acceptance green: `go build ./...`, `make test` (5570 tests, 6 env-gated skips, 0 failures), targeted `-v` suites (85 pass), `GOLDEN_UPDATE=1` + golden diff empty, schemas/docs regenerated. All 6 review subagents run; every finding fixed or declined-with-reason above.

### Task 3 (2026-07-08)

- **Overlay consumption is a thin layer on Task 2's machinery — no new resolution code.** `GenerateHarness` reads `g.cfg.Project().Build.Harnesses[bundle.Name]` (value-typed map; missing key = zero overlay = correct no-op) and feeds `slices.Concat(bundle.Manifest.Stacks, overlay.Stacks)` into the EXISTING `resolveHarnessStacks` — one lineage lookup for both sources, so provenance keeps working and a name repeated across installer+overlay renders once at its installer position (`resolveStackDecls`' seen-map with `dupIsError=false`). Keyed by `bundle.Name` (the resolved name `harness.Load` stamps), NOT `g.Harness` (empty for default) — do the same in any future overlay consumer.
- **New `DockerfileContext.HarnessPackages []string`** — separate from `Packages` deliberately: reusing `Packages` would leak harness packages into the base render and perturb `BaseContentHash`. Template slot: early-root apt RUN in `Dockerfile.harness-image.tmpl` right after `USER root`, before `StackRootSteps`, gated `{{- if .HarnessPackages}}` (empty = zero bytes), BuildKit cache-mount parity with the base template's apt RUN. `tctx.HarnessPackages = overlay.Packages` aliases the config slice — safe because template only ranges it; if a future path appends, copy first (the inject path six lines below shows why).
- **Overlay inject appends via `slices.Concat`, not `append`** — `buildContext` sets `tctx.Inject.BeforeEntrypoint = inj.BeforeEntrypoint` as a DIRECT alias of the config-store slice; an in-place `append` could write through into the store's backing array. `slices.Concat` allocates fresh. (First cut used a bespoke `concatStrings` helper; simplifier review replaced it with the stdlib per `feedback_no_bespoke_wrapper_for_stdlib_capability`.) Global inject renders first, overlay appended — declaration-order law; `tctx.Inject` may be nil when the project has no global inject (overlay-only case needs the nil-guard, it is NOT dead code).
- **Silent-failure review found a real HIGH: an overlay keyed to an unknown harness was silently dead config.** `build.harnesses.claud:` (typo) or an unregistered name passed Task 1's load validation (which only checks well-formedness) and then vanished — packages/stacks/inject never rendered anywhere with zero output. Fixed in-task with `validateOverlayKeys(cfg)` (harness.go, next to `IsKnownHarness`): `GenerateHarness` hard-errors when ANY `build.harnesses` key is not in shipped ∪ project-registered, sorted-key deterministic, error names the key + `clawker harness register` remedy. The check could NOT live in `internal/config`'s validate.go — config can't see bundler's shipped-name list (import cycle) — so it sits at generation time in bundler. **Task 4's register front-door does not obviate it**: config files are hand-editable.
- **Golden strategy: one new `harness-overlay` scenario (buildKit on), existing goldens byte-identical.** The template's empty-overlay path initially added stray blank lines to every existing harness golden (first cut used a standalone `{{/* comment */}}` + `{{- if}}` block); restructured to hang the whole block off `{{- if .HarnessPackages}}` directly after `USER root` with a `#` comment INSIDE the guard so the empty case collapses to zero bytes. Any future harness-image template slot must follow this pattern or every golden churns.
- **Test-hunter verdicts applied**: the original `OverlayStacksAfterInstaller` test was DELETE-flagged (fully redundant with the golden's byte-lock) — rewritten as `OverlayStackRepeatedAcrossSources` (installer+overlay both declare node → renders exactly once, count via the once-per-render `ARG NODE_VERSION=24` line, not `nodejs.org/dist` which repeats 7×) which also closed code-reviewer's coverage-gap note on the renders-once claim. The BuildKit-on packages subtest was likewise dropped as golden-redundant; the legacy-builder subtest kept (no golden renders an overlay with BuildKit off). Kept: inject-after-global ordering (no golden combines global AND overlay inject) and cross-harness scoping (goldens only render the default harness — the codex-stays-clean negative is unlockable by a single-harness golden).
- **Review findings deliberately declined**: (a) silent-failure LOW — overlay `packages` string contents unvalidated (empty string renders a dangling continuation line): mirrors base `build.packages` which is equally unvalidated, apt fails loudly at build time, `&& true` terminator keeps an empty entry from breaking the RUN; adding validation only for the overlay would create asymmetry — revisit only if base-package validation lands. (b) comment-analyzer wording nit on a test doc comment — file was rewritten anyway in the test-hunter pass.
- **Lint gotchas confirmed live (per Task 1's warning, all hit at commit time)**: `&DockerfileInject{}` tripped exhaustruct (all six fields now explicit-nil); `if err := ...` inside GenerateHarness tripped govet shadow (renamed `overlayErr`); the overlay block pushed GenerateHarness over funlen 60 → extracted `applyHarnessOverlay(tctx, overlay)` (packages + inject application; stacks stay in GenerateHarness since they join the installer decls pre-resolution); golines 120-col broke the long fmt.Errorf and two test assert lines.
- Acceptance green after review fixes: `go build ./...`, `make test` (5576 tests, 6 env-gated skips, 0 failures), `go test ./internal/bundler/... -v` all pass, `GOLDEN_UPDATE=1` regen leaves existing goldens untouched (only the two new harness-overlay files). `test/whail` suite skipped — Docker-backed host-only, per task instructions. Full pre-commit hook chain green. Committed as `363889b2` on `feat/multi-harness-support` (not pushed).

### Task 4 (2026-07-08)

- **Two flat command packages, firewall-style — NOT per-subcommand subpackages.** `internal/cmd/stack/` and `internal/cmd/harness/` each hold `{stack,harness}.go` (parent, `NewCmdStack`/`NewCmdHarness`), `register.go`, `list.go`, `remove.go`, `helpers.go` in ONE package, mirroring `internal/cmd/firewall/`. Deliberately parallel (near-identical shape, separate packages) — reviewers were told not to propose merging them; the shared PURE logic lives in `cmdutil` instead. Wired into `internal/cmd/root/root.go` after `project` (`stackcmd.NewCmdStack(f)` / `harnesscmd.NewCmdHarness(f)`). No new Factory noun needed — commands use `f.Config`, `f.IOStreams`, `f.TUI`.
- **Shared pure helpers extracted to `internal/cmdutil/registry.go`** (tested once in `registry_test.go`): `ResolveRegistryPath(projectRoot, cwd, input) (ResolvedRegistryPath, error)` (abs+stored forms — type-design review upgraded the original `(string,string)` return to a named struct `{Abs, Stored}` so callers can't swap them); `MergeRegistryRows(shipped []string, registered map[string]string) []RegistryRow` (sorted union, shadow detection, **skips empty-path entries centrally** — the "empty path is not a registration" invariant enforced here, not per-caller); `RenderRegistryRows(ios, tui, format, rows, emptyMsg)` (-q/--json/--format/table, wraps internal errors for wrapcheck); `RegistryRow` DTO + `RegistrySourceProject`/`RegistrySourceShipped`/`RegistryBuiltinPath` consts; `PrimaryWritePath[T](store)` (WriteTargets()[0].Path). cmdutil now imports `internal/{iostreams,tui,storage}` — no cycle (cmdutil already depended on all three via Factory).
- **Registration write keys**: stack → `store.Set("stacks.<name>.path", stored)`, remove → `store.Remove("stacks.<name>")` (whole entry; a stack entry carries ONLY a path, so removing just `.path` leaves an empty entry that still reads as registered — pinned by `TestStackRemove_Registered`). Harness → `store.Set("harnesses.<name>.path", stored)` (targeted leaf, never clobbers per-harness init config); remove drops the WHOLE `harnesses.<name>` only when `hasInitConfig(entry)` is false, else removes just `.path` (preserves mount_projects/env/post_init/pre_run/config). `hasInitConfig` is a manual field enumeration of `HarnessConfig` guarded against schema drift by `TestHasInitConfig_DetectsEveryField` (populates all 8 fields, asserts NumField()==8 so adding a field breaks compilation).
- **Path storage is project-root-relative when inside `cfg.ProjectRoot()`, else absolute** (`ResolveRegistryPath` + `isEscaping`). **Load-bearing gotcha (surfaced by silent-failure review + live smoke test): `ProjectRoot()` is REGISTRY-anchored** — it's `""` for a project that has a `clawker.yaml` on disk but is NOT registered (`clawker init`/`project register` not run). When empty: paths store ABSOLUTE and `store.Write()` lands in the USER config-dir `clawker.yaml` (walk-up disabled → config-dir-only discovery), NOT the project file — and a CWD-local `clawker.yaml` is not even discovered. This is the same systemic property every project-config-mutating command has, not a Task 4 bug. **Fix applied: register prints `  Written to <path>` (`PrimaryWritePath`) so the destination is never invisible** (the HIGH silent-failure finding). NOT fixed: requiring a registered project (would force heavy test rework — `NewIsolatedTestConfig` has `ProjectRoot()==""`; the relativization is proven directly by `ResolveRegistryPath` unit tests with a non-empty root). Task 6 docs should state registration is meant to run inside an initialized project.
- **`harness.Bundle.BundledStacks() ([]string, error)`** added (register reports embedded `stacks/<name>/` dirs). Returns `(nil,nil)` for a missing `stacks/` dir (`fs.ErrNotExist`) but SURFACES any other read error (silent-failure + type-design both flagged the original swallow-to-nil). Register calls it right after `harness.Load` and treats an error as bundle-invalid.
- **`store.Remove` returns `(found bool, err error)` — DO NOT discard `found`** (MEDIUM silent-failure finding): both remove commands now error when `!found` (registration lives only in a non-writable parent/user layer → a no-op that would otherwise print success). The map-key existence check (`cfg.Project().Stacks[name]`) and the dotted-path delete are two different access mechanisms over the same tree; `found` is the guard against them diverging.
- **Tests: black-box `package {stack,harness}_test`** driving `NewCmd*(f, nil).Execute()` with `configmocks.NewIsolatedTestConfig(t)` (file-backed, real Set/Write/read-back) for mutation and `configmocks.NewFromString(projectYAML, "")` for read-only list tests. `hasInitConfig` drift test is `package harness` internal → named `helpers_internal_test.go` (testpackage lint). Test Factory literal needs `//nolint:exhaustruct // test factory` (13 lazy closures — filling all is absurd; the sanctioned forced-nolint case). **test-hunter DELETED both `*_RunFInjection` tests** (redundant — the runF seam is nil in prod and flag binding is already proven by the real-execution NewEntry/NameOverride/Force tests).
- **Lint gotchas hit (all fixed, no `.golangci.yml` edits)**: exhaustruct on Options struct literals → fully zero-initialize every field (`Path:"", Name:"", Force:false`, `Format:nil`); wrapcheck on internal-pkg error returns (`WriteJSON`/`ExecuteTemplate`/`tp.Render`/`RenderRegistryRows`) → `if err = fn(); err != nil { return fmt.Errorf(...) }` (never bare-wrap — `fmt.Errorf("%w", nil)` would fabricate a non-nil error); govet shadow on `if err := ...` when an outer `err` exists → use `err =`; gocognit>12 on the render switch → extracted `renderRegistryTable` + `wrapRender` nil-passthrough helper into cmdutil; modernize → `slices.Contains(bundler.Shipped*Names(), name)` for `isShipped*`; perfsprint → `errors.New` for arg-less error; nonamedreturns → unnamed returns. `golangci-lint fmt` on a whole PACKAGE reformats unrelated files (churned `worktree.go`/`worktree_test.go`) — target SPECIFIC file paths and `git checkout` any collateral to keep the diff scoped.
- **Review findings DECLINED (with reason)**: (a) `ResolveRegistryPath`'s `strings.Contains(~/$)` blunt rejection — DELIBERATE byte-for-byte parity with the load-time front-door (`config/validate.go` `validatePathValue`); code-reviewer confirmed identical, so the CLI can never write a value load would reject. (b) `RegistryRow.Source/Shadows` as a typed `RegistrySource` string — declined; it's a write-only JSON/table DTO never read back as domain values, typing buys nothing at the point of use. (c) `slices.Sorted(maps.Keys(...))` modernization of `MergeRegistryRows` — declined; `sort.Strings` is the codebase convention, `slices.Sorted` used nowhere in `internal/`. (d) requiring `ProjectRoot()!=""` hard-guard (see path-storage bullet) — declined for test cost + systemic scope; destination-print mitigates. (e) internal/stack/stack.go:8 stale "settings stacks registry" package doc — real but OUT of Task 4 scope (Task 2 leftover); noted for Task 6 docs sweep. code-reviewer found ZERO issues above its 80-confidence bar.
- Acceptance green after review fixes: `go build -o bin/clawker ./cmd/clawker`, `make test` (5611 tests, 6 env-gated skips, 0 failures), `go test ./internal/cmd/stack/... ./internal/cmd/harness/... -v` (23 pass), `./bin/clawker stack register --help` / `harness list --help` OK, live smoke test (register→list→remove, shadow detection, bundled-stacks report, destination line, shipped-removal error) all correct. Full new-code lint clean (`golangci-lint run` = 0 issues). Committed as `641ac525` on `feat/multi-harness-support` (not pushed). Commit also carries the auto-generated CLI reference pages (`docs/cli-reference/clawker_{stack,harness}*.md` + updated `clawker.md`) — the docs-freshness pre-commit hook enforces gen-docs output per commit; mintlify nav curation (`docs/docs.json`) is NOT touched by gen-docs and remains Task 6's job.

### Task 5 (2026-07-08)

- **Signature change: `BaseContentHash(baseDockerfile []byte)` → `BaseContentHash(baseDockerfile []byte, buildArgs map[string]*string)`.** `buildArgs` is moby's canonical `ImageBuildOptions.BuildArgs` shape — `docker/builder.go:122` threads `opts.BuildArgs` straight through, zero conversion (type-design review confirmed matching the Docker-shaped map is correct; a domain type would add an unpaid conversion layer). The ONLY prod caller is the Builder; test helper `docker/builder_test.go::expectedBaseHash` also updated (passes nil — it computes the arg-free base an existing image was cut from).
- **The fold, precisely** (`hashBaseBuildArgs` in `basehash.go`): parse the ARG names the RENDERED base Dockerfile declares (`baseDeclaredArgNames`), intersect with the user's `--build-arg` keys, sort, and write `arg:NAME=EFFECTIVE\x00` per relevant arg into the SAME sha256 that already hashes the Dockerfile bytes + copy srcs. **Two early returns (`len(buildArgs)==0`, then `len(relevant)==0`) write NOTHING when no supplied arg is base-declared → the arg-free hash is byte-identical to the pre-change format.** This is the hard upgrade guarantee (no gratuitous base rebuild for existing users); pinned by `TestBaseContentHash_NoBuildArgsMatchesLegacyFormat` asserting the arg-free hash equals a literal `sha256.Sum256(df)` (minimalProjectYAML = no copy srcs, so df bytes are the only input).
- **ARG parser edge cases** (`argInstructionName` + `baseDeclaredArgNames`): (a) **multi-stage FROM scoping** — collect EVERY stage's ARGs; a `--build-arg` targeting an ARG in ANY stage can change the base build, so all declarations count (over-inclusion only ever costs a spurious rebuild, never a wrong image). (b) **quoted defaults with spaces** (`ARG NAME="a b"`) — `strings.Fields` + first-`=` `IndexByte` extracts the name before the quote (the name never contains a space, so field-splitting the value is harmless). (c) **case sensitivity** — ARG NAMES are case-sensitive (BuildKit keys the value on the exact name), but the `ARG` KEYWORD is case-insensitive (`strings.EqualFold`, matching Dockerfile parsing). (d) **backslash line continuation** — `ARG \<newline>NAME` is collapsed (`strings.ReplaceAll("\\\n"," ")`) BEFORE splitting so the real name is seen; this closes a latent regression trap (silent-failure review LOW): without it a future template that wraps an ARG would silently re-introduce the exact bug this task fixes. Currently no rendered template uses continuation, but the collapse + `TestBaseDeclaredArgNames` `WRAPPED` case guard it. Parser is line-oriented over clawker's OWN rendered template (controlled input), NOT arbitrary user Dockerfiles — heredoc ARG-shaped lines are not special-cased (documented; no template emits one).
- **Effective value**: non-nil `*string` = explicit `--build-arg NAME=VALUE`; **nil = `--build-arg NAME` pass-through → `os.Getenv(NAME)`** (Docker reads the client env for a bare `--build-arg NAME`). This makes the hash env-sensitive for pass-through args — CORRECT (matches what Docker would build), and the direction is always fail-safe (spurious rebuild, never a stale-skip of a genuinely-different build). Pinned by `TestBaseContentHash_NilBuildArgUsesEnv` (`t.Setenv` two values → different hashes).
- **Review findings DECLINED (with reason)**: (a) silent-failure LOW — a bare `--build-arg NAME` with an UNSET env hashes `""` where Docker would fall back to the ARG's Dockerfile default; declined — matching that would require parsing ARG defaults for a safe-direction imprecision (only ever a spurious rebuild, and hash-time and build-time read the same process env so they never collide on a wrong image). (b) code-reviewer conf-40 note — clawker passes the nil build-arg pointer to the daemon without client-side env resolution (unlike the docker CLI); same bounded impact (spurious rebuild), the doc comment sanctions it. Both recorded here, not code-changed.
- **Review findings ADOPTED**: silent-failure LOW (line-continuation collapse — done, with test); comment-analyzer (removed `(design §6b item 9)` dangling memory-section refs from `basehash.go` + `builder_test.go`; removed "the fix"/"Without the fix" current-state framing per `feedback_no_current_state_in_comments`; fixed a CLAUDE.md line-leading `+` that rendered as a stray markdown bullet; softened "legacy"/"pre-arg"/"on upgrade" transitional framing to timeless invariant statements; generalized the `strptr` comment off the cross-package `parseBuildArgs` symbol reference); type-design (added the "line-oriented parser over controlled template input" doc note). code-reviewer, code-simplifier, test-hunter reported ZERO actionable findings (test-hunter KEEP on all 7 new tests — verified dual-guard: fold-neuter reddens "does-fold" tests, `declared`-filter deletion reddens "doesn't-fold" guards).
- **Lint gotchas** (new-from-merge-base=main): `modernize` wants `strings.SplitSeq` not `strings.Split` when ranging; `wastedassign` on `effective := ""` immediately overwritten in both if/else branches → `var effective string`; `unparam` surfaced on TWO pre-existing test helpers (`expectedBaseHash` harnessName, `setupInspectBaseWithHash` baseRef — both always one value) ONLY because Task 5 EDITS `builder_test.go` (they were latent on the branch, never linted since prior tasks didn't touch the file; pristine HEAD = 0 issues). Resolved with scoped `//nolint:unparam // fixture knob kept explicit for readability` on each — removing the params would inline magic strings across 5 call sites, more churn + less clarity (sanctioned forced-nolint per `feedback_nolint_only_when_forced`); NEVER edited `.golangci.yml`. `golangci-lint fmt <specific-file>` fixed a gofumpt trailing-comment-alignment nit — target the specific file, not the package (per Task 4's collateral-churn warning).
- **CLI plumbing verified end-to-end** (no code needed — Task 5 only had to wire the hash): `--build-arg` flag → `build.go::parseBuildArgs` (`KEY=VALUE`→`&value`, bare `KEY`→nil) → `BuilderOptions.BuildArgs` → `Builder.Build` → `gen.BaseContentHash(df, opts.BuildArgs)`. `TestBuild_RelevantBuildArgRebuildsBase` (TZ, a real `ARG TZ=UTC` in the base template → rebuild) + `TestBuild_HarnessOnlyBuildArgSkipsBase` (CLAUDE_CODE_VERSION, harness-only → skip) prove the wiring both directions. Harness build path already received `opts.BuildArgs` via `toBuildImageOpts` — unchanged, correct (harness ARGs like CLAUDE_CODE_VERSION are consumed at harness build).
- **Files changed**: `internal/bundler/basehash.go` (+fold/parser), `internal/bundler/basehash_test.go` (5 new tests + signature adaptations + strptr helper), `internal/docker/builder.go` (1-line caller), `internal/docker/builder_test.go` (2 new tests + expectedBaseHash + 2 nolints), `internal/bundler/CLAUDE.md` (freshness section). No golden files touched (freshness hash isn't a golden). No config/schema/CLI-surface change → no gen-docs regen needed.
- Acceptance green after review fixes: `go build ./...`, `make test` (5618 tests, 6 env-gated skips, 0 failures), `go test ./internal/bundler/ -run TestBaseContentHash -v` (all pass), `go test ./internal/docker/... -v` (all pass), new-from-merge-base lint = 0 issues. Red-under-mutation verified (neutering `hashBaseBuildArgs` reddens the relevant-arg + nil-env bundler tests AND `TestBuild_RelevantBuildArgRebuildsBase`). Committed as `14be8add` on `feat/multi-harness-support` (not pushed); full pre-commit chain (golangci-lint-full, unit tests, govulncheck, semgrep) green.

### Task 6 (2026-07-08) — INITIATIVE COMPLETE

- **Most package CLAUDE.md docs were already current** — Tasks 1–5 updated `internal/bundler/CLAUDE.md` and `internal/config/CLAUDE.md` in-flight (project registries, lineage resolution, per-harness overlay, basehash build-arg fold all already documented). Task 6's code-doc work was narrower than the plan implied: only `internal/stack/` package/const/Load doc comments were stale (the "three sources / flat namespace / materialized to config dir" model). The comment-analyzer subagent caught two MORE stale comments beyond the one the plan named (`stack.go:8` package doc): `stack.go:28` "flat-namespace key", `stack.go:61` "materialized/user-owned", `consts.go:18` StacksSubdir "materialized under user config dir" — all resurrected deleted concepts adjacent to the diffed lines. Lesson for doc-sweep tasks: grep the WHOLE touched package for the dead vocabulary, not just the one line the plan flags.
- **gen-docs CLI reference + schemas were already committed by Task 4** — `go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas` produced ZERO diff to `docs/cli-reference/` and `docs/schemas/` and `docs/configuration.mdx` (all regenerated in prior tasks). Task 6's only docs.json work was the NAV curation gen-docs does NOT do: added `Stack` + `Harness` CLI reference groups (the 8 `clawker_{stack,harness}*` pages existed but were unreachable in the sidebar).
- **The user-facing Mintlify guides were the real work** — `stacks.mdx` needed a near-full rewrite (settings registry → project registry, materialization → shipped-from-binary, flat namespace + collision errors → per-lineage lookup + wholesale shadow + provenance, old regex → unified naming, added --build-arg param + register CLI). `harness-bundles.mdx` + `custom-images.mdx` needed section rewrites (registry/materialization/`default` flag removed; per-harness overlay section added to custom-images with a stable `#per-harness-overlays` anchor). `upgrading/v0.13.mdx` (the ONE migration-narration-allowed page per `feedback_alpha_no_user_migration_docs`) had its materialization + settings-registry-seeding steps replaced with the strip-on-load migration + register-CLI direction.
- **MDX/JSX hazard is real and specific**: Mintlify parses every `.mdx` as JSX, so a bare `<name>`/`<harness>` in PROSE breaks the build — every angle-bracket placeholder must be backtick-wrapped or inside a code fence. Verified all new prose; code-reviewer independently confirmed zero stray `<...>`. (Generated files get `EscapeMDXProse`; hand-authored ones do not — you own it.)
- **Skill sweep (`feedback_docs_sweep_includes_support_skill`)**: `harness-stack-dev` skill was heavily stale (settings.yaml registry, materialization, flat namespace, collision errors, docker-tag naming grammar, a `settings.yaml` registration snippet in worked-example.md, a `~/.config/clawker/harnesses/` bundle-layout header). The `skill-doc-auditor` skill caught the worked-example.md snippet the manual grep sweep initially missed. `clawker-support/SKILL.md` had one stale registry line; its `CLAUDE.md` drift-gate table referenced the DELETED `internal/harness/materialize.go` — replaced with the live files. The reference `sample-{go,node}.yaml` were already correct (they use project-side `harnesses.<name>` init config + `build.stacks`, which survived unchanged). Left alone (correctly): `settings.md`/`troubleshooting.md` `settings.yaml` mentions (firewall/monitoring — a real separate schema) and `harness-manifest.md:76`'s `[a-zA-Z0-9]...{0,40}` (VOLUME name grammar, unchanged by this initiative — NOT the stack/harness name rule).
- **Plugin version NOT bumped** — branch already bumped `clawker-support` plugin 1.0.21→1.0.22 for the multi-harness work; per `feedback_plugin_version_bump_once_per_unmerged_release` one bump covers the whole unmerged branch (this overrides the plugin CLAUDE.md's per-change bump rule for the unmerged-release case).
- **CHANGELOG verification**: the Task 2 0.13.0 bullets held up post-3–5; ADDED the missing surfaces — `clawker stack|harness register/list/remove`, per-harness `build.harnesses.<name>` overlay, unified naming rule (Changed), and the base build-arg freshness Fixed bullet.
- **Review gate**: `code-reviewer` (docs-accuracy grounded against `internal/{bundler,consts,stack}`) = ZERO findings above conf-80 — verified every behavior claim incl. the two DIFFERENT provenance line formats (`harness … (project registry) shadows shipped` vs `stack … ← project (path) shadows shipped`) are both genuinely emitted. `comment-analyzer` = 3 stale-Go-comment fixes + 1 transitional-framing nit (`stack-authoring.md` "cross-stratum dedup is dead" → "there is no cross-stratum dedup" per `feedback_no_current_state_in_comments`), all applied. `skill-doc-auditor` = the worked-example.md miss, applied. No test-hunter (no tests added — docs task).
- Acceptance green: `go run ./cmd/gen-docs … --schemas` + `git status docs/` clean (only the 5 hand-edited guides/nav modified — cli-reference/schemas byte-identical), `make pre-commit` full chain (secrets, semgrep, go-mod-tidy, golangci-lint-full, govulncheck, unit tests, generated-docs-freshness, NOTICE-freshness, claude-doc-freshness) all Passed. `npx mintlify dev` SKIPPED (interactive; per task instructions — user verifies render locally). Committed as `8ed8d8b4` (docs/skill/CHANGELOG/stack-comments); serena memory updates in a follow-on `docs(serena)` commit. Not pushed.
- **OUT-OF-SCOPE follow-ups remaining after this initiative** (all deferred, none block the branch's stack-contract concern): (1) monitoring stack breakout — `clawker monitor install <bundle>` + host-global install ledger, own design pass (model doc §7 item 7); (2) apt `packages:` fold into stack vocabulary (carried, low priority — §7 item 6); (3) host UAT of the full register→build→run flow (in-container can't drive it). Separately still merge-blocking the BRANCH (not this initiative): the skills-plugin multi-agent packaging change (see `multi-harness/README` open items).

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

## Post-review follow-up (2026-07-08, after compaction)

- `ada1a5fe` — DELETED `migrateRemoveLegacyRegistryKeys` settings migration + test + stderr notice. Rationale: settings-side `stacks:`/`harnesses:` registry NEVER shipped (`StackSettings` exists only on this branch — `git log main -S StackSettings` empty, no tag contains it). Migration was speculative history addressing nobody. CHANGELOG entry, v0.13 upgrade bullet, config CLAUDE.md row, schema.go comment all cleaned.
- `2c639803` — fabricated-history sweep (4 Opus subagents, disjoint areas: docs/, config+bundler Go, cmd/docker/pkg Go, CLAUDE.md/.claude/plugin). Rule: docs/comments describe current state; historical framing legit ONLY when referenced past shipped on main. Found+fixed: v0.13.mdx "Alpha builds: drop stale config-dir copies" section (pure intra-branch churn, deleted); basehash_test.go + bundler CLAUDE.md "engine gaining arg-awareness"/"legacy format" framing (BaseContentHash is branch-new — no legacy exists); test renamed `TestBaseContentHash_NoBuildArgsMatchesLegacyFormat` → `TestBaseContentHash_NoBuildArgsIsDockerfileOnly`. Everything else (build.image, agent.claude_code, :latest tag, monitoring keys, `proto: tls`) verified shipped-on-main → kept.

## Phase 2: clause register + apt-fold closure (2026-07-08, same session)

- **§7 item 6 (apt fold) closed, zero code** — user: stacks don't need `packages:` (fragments apt-install directly); `packages:` is the YAML-only project surface — `build.packages` (base) + `build.harnesses.<name>.packages` (per-harness), both already shipped.
- **§7 item 5 (clause register) DONE** — 3-phase Opus fan-out, each gate-reviewed by orchestrator:
  1. Recon agent enumerated 22 engine guarantees + 11 author obligations from shipped code, every clause anchored file:line + coverage-marked (draft in session scratchpad). Orchestrator spot-verified anchors (resolveStack switch, egressFloor hardcode).
  2. `90872354` docs: `docs/stack-contract.mdx` (six theme groups, enforcement-tier legend, stable IDs) + docs.json nav. Gate caught 1 fabricated cross-link anchor (`#designing-the-egress-floor` → repointed `#egress`); all 14 other anchors verified against sibling pages.
  3. `0f823899` test: 71 `// Conformance: <ID>` badges + 2 gap-fills — `TestGenerateBase_StacksRenderInDeclarationOrder` (E1; test-hunter verified red under injected sort.Strings) and `TestFilterBasePackages` (E22; sole guard for survivor order — goldens blind to it). E7 marker on existing floor test (structurally untestable — field absent). test-hunter: KEEP/KEEP via live mutations. `make test` 5619 green.
- Gotchas repeated: silent pre-commit failure again (golangci-lint-full: goconst tripped by test's floor-package literals reaching 3 occurrences; godoclint `[os.Getenv]` link form) — always grep commit output for "Failed", never trust hook tail.
- E7 included as blessed clause (floor rules structurally cannot request TLS-skip); flagged to user, no objection. Seam recorded: validateProjectRegistries not on Set/Write path.

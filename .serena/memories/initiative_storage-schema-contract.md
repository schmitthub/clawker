# Storage Schema Contract Initiative

**Branch:** `feat/storage-schema-contract`
**Parent memory:** `brainstorm_config-field-descriptions`
**PRD Reference:** GitHub Issue #178

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Storage contract foundation — interfaces, types, normalizer | `complete` | opus-4.6 |
| Task 2: Config schema annotations — struct tags + Schema impl on Project and Settings | `complete` | opus-4.6 |
| Task 3: Remaining types — Schema impl on EgressRulesFile and ProjectRegistry | `complete` | opus-4.6 |
| Task 4: Store constraint + storeui refactor — `Store[T Schema]`, consume `Fields()`, slim adapters | `pending` | — |
| Task 5: Enforcement, documentation, brainstorm wrap-up | `pending` | — |

## Key Learnings

(Agents append here as they complete tasks)

- **Task 1**: Reuse existing `yamlTagName` from `merge.go` instead of duplicating. `NewFieldSet` should panic on duplicate paths (matching `ApplyOverrides` convention). `NormalizeFields` needs a nil guard before `reflect.TypeOf`. `FieldKind.String()` is valuable for test failure readability. `All()` should return a defensive copy. `NewField` should validate non-empty path. `yaml:",omitempty"` edge case is a known regression vector — always test it.
- **Task 2**: Do NOT add desc/label tags on struct-type fields — normalizer recurses into them, parent-level tags are silently discarded. DockerInstructions.Env is injected at runtime, not baked into image. Use "allow" not "whitelist" terminology. Parity tests should compare paths not just counts. Lost TODO comments must be restored when rewriting struct definitions.
- **Task 3**: Don't write enforcement tests for single-field wrapper structs (EgressRulesFile, ProjectRegistry) — the "all fields have descriptions" pattern only has value for schemas with many fields where forgetting one is a realistic risk.

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run `code-reviewer`, `silent-failure-hunter`, `code-simplifier`, `comment-analyzer`,`type-design-analyzer` subagents to review this task's changes, then fix any and all findings
5. **IMPERATIVE — test-hunter**: After addressing all findings from the first wave of review agents, run the `test-hunter` subagent as the ABSOLUTE FINAL review step. Its findings MUST be addressed before committing. This step CANNOT be skipped.
6. Commit all changes from this task with a descriptive commit message. All pre-commit hooks MUST pass — never use `--no-verify`.
7. Push the branch and continue to the next task immediately — do NOT stop, do NOT present a handoff prompt, do NOT wait for a new conversation.

---

## Context for All Agents

### Background

Config and settings field descriptions are scattered across YAML comments, MDX docs, CLAUDE.md, and inline Go comments. Issue #178 calls for a single source of truth. The brainstorm concluded that `storage` should own the contract as interfaces — `Field`, `FieldSet`, `Schema` — with struct tags (`desc`, `label`) as the source of truth on schema types. The normalizer in storage reads struct tags and produces concrete implementations of the interfaces. Consumers like storeui, docs generators, and CLI commands program against the interfaces.

**Key design decisions:**
- Storage is authoritative — it defines the contract, types conform
- Interfaces all the way down: `Field`, `FieldSet`, `Schema`
- Concrete implementations are unexported in storage
- `NormalizeFields[T]()` reads `yaml`/`label`/`desc` struct tags, returns `FieldSet`
- `Store[T any]` → `Store[T Schema]` in Task 4 (after all types implement Schema in Tasks 1–3) — compiler-enforced contract
- `storeui.Override` keeps only TUI-specific concerns (Hidden, Order, ReadOnly, Kind, Options)
- Domain adapters slim down — labels/descriptions come from schema tags

### Key Files

- `internal/storage/store.go` — Store[T] definition (Task 4: `T any` → `T Schema`)
- `internal/storage/` — NEW: `field.go` (interfaces + concrete impls + normalizer)
- `internal/config/schema.go` — all schema structs, gets `desc`/`label` tags + `Schema` impl
- `internal/storeui/field.go` — Field, Override, ApplyOverrides (refactored)
- `internal/storeui/reflect.go` — WalkFields + walkStruct (replaced by Schema.Fields())
- `internal/storeui/edit.go` — Edit[T] orchestrator (updated to use Schema)
- `internal/config/storeui/project/project.go` — domain adapter (slimmed)
- `internal/config/storeui/settings/settings.go` — domain adapter (slimmed)
- `internal/firewall/types.go` — EgressRulesFile (Schema impl)
- `internal/config/schema.go` — ProjectRegistry (Schema impl)

### Design Patterns

- **Interfaces as contracts**: Storage defines `Field`, `FieldSet`, `Schema` interfaces. Concrete implementations are unexported structs in storage. Callers only see interfaces.
- **Normalizer pattern**: `NormalizeFields[T](v T) FieldSet` is storage's tool for converting raw structs into the contract format via reflection + struct tags. Domain types call this in their `Fields()` method.
- **Override layering**: storeui's `Override` adds TUI-specific presentation concerns on top of `storage.Field` data. `ApplyOverrides` merges overrides onto schema-derived fields.
- **Existing storeui reflection**: `WalkFields()` in `reflect.go` walks structs via `yaml` tags, maps Go types to `FieldKind`, extracts runtime values. The normalizer replaces the schema metadata portion; runtime value extraction remains in storeui.

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package `CLAUDE.md` before starting
- Use Serena tools for code exploration — read symbol bodies only when needed
- All new code must compile and tests must pass
- Follow existing test patterns in the package
- `storage` is a leaf package (no internal imports) — new code must not add internal imports
- Interfaces must be testable — provide test helpers or constructors for fakes

---

## Task 1: Storage Contract Foundation

**Creates/modifies:** `internal/storage/field.go`, `internal/storage/field_test.go`, `internal/storage/CLAUDE.md`
**Depends on:** nothing

### Implementation Phase

1. **Read** `internal/storage/CLAUDE.md` and `internal/storage/store.go` to understand the package's current scope and conventions.

2. **Create `internal/storage/field.go`** with:
   - `FieldKind` type + constants: `KindText`, `KindBool`, `KindInt`, `KindStringSlice`, `KindDuration`, `KindComplex`, `KindSelect` (migrated from storeui, which defines these today). NOTE: storeui's deprecated `KindTriState` is NOT migrated — it stays in storeui as a local alias mapping to `storage.KindBool`.
   - `Field` interface: `Path() string`, `Kind() FieldKind`, `Label() string`, `Description() string`, `Default() string`
   - `FieldSet` interface: `All() []Field`, `Get(path string) Field`, `Group(prefix string) []Field`, `Len() int`
   - `Schema` interface: `Fields() FieldSet`
   - Unexported concrete `field` struct implementing `Field`
   - Unexported concrete `fieldSet` struct implementing `FieldSet` — backed by `[]Field` + `map[string]Field` index
   - Constructor: `NewFieldSet(fields []Field) FieldSet` (exported — needed by normalizer callers and tests)
   - `NormalizeFields[T any](v T) FieldSet` — generic normalizer that reflects over T's struct fields, reads `yaml`, `label`, `desc` struct tags, maps Go types to `FieldKind`, returns `FieldSet`. Handles: nested structs, pointer types (`*bool`, `*struct`), `time.Duration`, `[]string`, maps (→ KindComplex). Does NOT extract runtime values — only schema metadata.
   - `NewField(path string, kind FieldKind, label, desc, def string) Field` (exported — for building fields manually if struct tags aren't used)

3. **Write tests in `internal/storage/field_test.go`**:
   - Test `NormalizeFields` with a representative test struct covering all type mappings (string, bool, *bool, int, []string, time.Duration, nested struct, *struct, map)
   - Test that `desc` and `label` tags are read correctly
   - Test `FieldSet.Get()` returns correct field, nil for unknown path
   - Test `FieldSet.Group()` returns correct prefix matches
   - Test `FieldSet.All()` returns all fields in order
   - Test `NewField` constructor
   - Test panics on nil/non-struct input (matching existing WalkFields behavior)

4. **Update `internal/storage/CLAUDE.md`** with new public API documentation.

### Acceptance Criteria

```bash
go build ./internal/storage/...
go test ./internal/storage/... -v -run TestField
go vet ./internal/storage/...
```

### Wrap Up

1. Update Progress Tracker: Task 1 -> `complete`
2. Append key learnings
3. Run review agents, then test-hunter as final step (see Context Window Management above).
4. Commit and push. Continue to Task 2.

---

## Task 2: Config Schema Annotations

**Creates/modifies:** `internal/config/schema.go`, `internal/config/config_test.go`
**Depends on:** Task 1

### Implementation Phase

1. **Read** `internal/config/schema.go` to see all schema structs and their current tags.

2. **Add `desc` and `label` struct tags** to every exported field on:
   - `Project`, `BuildConfig`, `DockerInstructions`, `CopyInstruction`, `ExposePort`, `ArgDefinition`, `HealthcheckConfig`, `RunInstruction`, `InjectConfig`
   - `AgentConfig`, `ClaudeCodeConfig`, `ClaudeCodeConfigOptions`
   - `WorkspaceConfig`, `SecurityConfig`, `FirewallConfig`, `IPRangeSource`, `GitCredentialsConfig`
   - `LoopConfig`
   - `Settings`, `LoggingConfig`, `OtelConfig`, `MonitoringConfig`, `TelemetryConfig`, `HostProxyConfig`, `HostProxyManagerConfig`, `HostProxyDaemonConfig`
   - Use existing descriptions from `config/storeui/project/project.go` and `config/storeui/settings/settings.go` overrides as the source — they already have the right wording. Fill gaps for fields that have no override today.

3. **Implement `Schema` interface** on `Project` and `Settings`:
   ```go
   func (p Project) Fields() storage.FieldSet {
       return storage.NormalizeFields(p)
   }
   func (s Settings) Fields() storage.FieldSet {
       return storage.NormalizeFields(s)
   }
   ```

4. **Write tests**:
   - `TestProjectFields_AllFieldsHaveDescriptions` — walk all fields, assert none have empty Description
   - `TestSettingsFields_AllFieldsHaveDescriptions` — same for Settings
   - `TestProjectFields_Get` — verify specific field lookups return correct metadata
   - `TestProjectFields_Group` — verify prefix grouping works (e.g., "build" returns all build fields)
   - Cross-reference: assert field count from `Fields()` matches field count from current `storeui.WalkFields()` (parity check)

### Acceptance Criteria

```bash
go build ./internal/config/...
go test ./internal/config/... -v -run TestProject -run TestSettings
go test ./internal/storeui/... -v  # Ensure existing storeui tests still pass
```

### Wrap Up

1. Update Progress Tracker: Task 2 -> `complete`
2. Append key learnings
3. Run review agents, then test-hunter as final step (see Context Window Management above).
4. Commit and push. Continue to Task 3.

---

## Task 3: Remaining Types — EgressRulesFile and ProjectRegistry

**Creates/modifies:** `internal/firewall/types.go`, `internal/firewall/types_test.go` (or existing test file), `internal/config/schema.go`
**Depends on:** Task 1

### Implementation Phase

1. **Add `desc`/`label` tags and `Schema` impl to `EgressRulesFile`** in `internal/firewall/types.go`:
   - `EgressRulesFile` has one field: `Rules []config.EgressRule` (slice of struct = KindComplex)
   - `EgressRule` fields (in config/schema.go) need `desc`/`label` tags too for completeness
   - Implement `Fields() storage.FieldSet` on `EgressRulesFile` — minimal but satisfies the contract
   - NOTE: `[]struct` fields normalize to KindComplex. The Schema impl is about contract compliance, not necessarily rich introspection for every internal type.

2. **Add `desc`/`label` tags and `Schema` impl to `ProjectRegistry`** in `internal/config/schema.go`:
   - `ProjectRegistry` has one field: `Projects []ProjectEntry`
   - `ProjectEntry` and `WorktreeEntry` fields need `desc`/`label` tags
   - Implement `Fields() storage.FieldSet` on `ProjectRegistry`

3. **Write tests**:
   - All fields have descriptions
   - `Fields()` returns expected field count and paths

### Acceptance Criteria

```bash
go build ./internal/firewall/... ./internal/config/...
go test ./internal/firewall/... -v -run TestEgressRulesFile
go test ./internal/config/... -v -run TestProjectRegistry
```

### Wrap Up

1. Update Progress Tracker: Task 3 -> `complete`
2. Append key learnings
3. Run review agents, then test-hunter as final step (see Context Window Management above).
4. Commit and push. Continue to Task 4.

---

## Task 4: Storeui Refactor + Store Constraint Enforcement

**Creates/modifies:** `internal/storage/store.go`, `internal/storeui/field.go`, `internal/storeui/reflect.go`, `internal/storeui/edit.go`, `internal/storeui/field_test.go`, `internal/storeui/reflect_test.go`, `internal/storeui/integration_test.go`, `internal/config/storeui/project/project.go`, `internal/config/storeui/project/project_test.go`, `internal/config/storeui/settings/settings.go`, `internal/config/storeui/settings/settings_test.go`
**Depends on:** Task 1, Task 2, Task 3 (all types must implement Schema before constraint tightens)

This is the largest task — it rewires storeui to consume the storage contract AND tightens the Store constraint.

### Implementation Phase

0. **Tighten `Store[T]` constraint**: Change `Store[T any]` → `Store[T Schema]` in `internal/storage/store.go`. This is safe because Tasks 1–3 already ensured all Store-backed types implement Schema. Update `NewStore[T any]` → `NewStore[T Schema]` and `NewFromString[T any]` → `NewFromString[T Schema]`. Verify compilation of the full project before proceeding.

1. **Read** all storeui files and both domain adapters to understand the current data flow.

2. **Refactor `storeui.Field`** to wrap `storage.Field`:
   - `storeui.Field` keeps its existing fields but its schema metadata (Path, Label, Description, Kind, Default) is sourced from `storage.Field` instead of raw reflection.
   - `Value`, `Options`, `Validator`, `Required`, `ReadOnly`, `Order` remain storeui-specific (runtime/presentation concerns).
   - `FieldKind` in storeui becomes an alias or re-export of `storage.FieldKind` — avoid duplicating the constants. If that causes import issues, keep storeui's `FieldKind` but ensure the mapping from `storage.FieldKind` → `storeui.FieldKind` is trivial.

3. **Update `storeui.Edit[T]`** signature:
   - Change from `Edit[T any]` to `Edit[T storage.Schema]`
   - Inside Edit: call `snapshot.Fields()` to get `storage.FieldSet`, then convert to `[]storeui.Field` by extracting schema metadata + adding runtime values from the snapshot
   - This replaces the current `WalkFields(snapshot)` call

4. **Refactor `reflect.go`**:
   - `WalkFields` is no longer the primary entry point for Edit — Schema.Fields() is
   - `WalkFields` may still be useful as a standalone utility. Consider: deprecate or remove it. If removed, move any runtime value extraction logic (formatting current values from a snapshot) into a new helper that pairs schema fields with runtime values.
   - `SetFieldValue` in `value.go` is unaffected — it's the reverse direction (string → struct field)

5. **Slim domain adapters**:
   - `config/storeui/project/Overrides()`: Remove all `Label` and `Description` pointers. Keep only: `Hidden` (for complex types), `Kind`/`Options` (for select fields like workspace.default_mode), `Order` (if custom ordering is needed), `ReadOnly` (for managed fields).
   - `config/storeui/settings/Overrides()`: Same treatment.
   - `ApplyOverrides` continues working the same way — it merges presentation overrides onto the now-richer base fields.

6. **Update tests**:
   - Update `field_test.go`: test that `ApplyOverrides` correctly layers overrides onto schema-derived fields
   - Update `reflect_test.go`: adjust or remove tests for `WalkFields` as appropriate
   - Update `integration_test.go`: ensure full round-trip still works (Edit → save → reload → verify)
   - Update domain adapter tests: verify slimmed overrides still produce correct TUI output
   - Verify that field descriptions appear in the TUI output (schema-derived, not override-derived)

### Acceptance Criteria

```bash
go build ./internal/storeui/... ./internal/config/storeui/...
go test ./internal/storeui/... -v
go test ./internal/config/storeui/... -v
go test ./internal/tui/... -v  # Ensure TUI components still work
make test  # Full unit test suite
```

### Wrap Up

1. Update Progress Tracker: Task 4 -> `complete`
2. Append key learnings
3. Run review agents, then test-hunter as final step (see Context Window Management above).
4. Commit and push. Continue to Task 5.

---

## Task 5: Enforcement, Documentation, Brainstorm Wrap-up

**Creates/modifies:** `internal/storage/CLAUDE.md`, `internal/storeui/CLAUDE.md`, `internal/config/CLAUDE.md`, `.claude/rules/storeui.md`, `CLAUDE.md`
**Depends on:** Task 1–4

### Implementation Phase

1. **Add enforcement tests** (if not already covered in earlier tasks):
   - Test in `internal/storage/` that asserts `NormalizeFields` on a struct with missing `desc` tags produces fields with empty descriptions (this is the "detection" side)
   - Test in `internal/config/` that asserts ALL Project and Settings fields have non-empty descriptions (this is the "enforcement" side — CI will fail if someone adds a field without a `desc` tag)

2. **Update documentation**:
   - `internal/storage/CLAUDE.md`: Document `Field`, `FieldSet`, `Schema` interfaces, `NormalizeFields`, `NewField`, `NewFieldSet`. Document the struct tag contract (`yaml`, `desc`, `label`).
   - `internal/storeui/CLAUDE.md`: Update to reflect that schema metadata now comes from `storage.Schema`, not raw reflection. Document the new data flow.
   - `internal/config/CLAUDE.md`: Document that `Project` and `Settings` implement `storage.Schema`. Document the struct tag requirement.
   - `.claude/rules/storeui.md`: Update the architecture overview and data flow sections.
   - `CLAUDE.md`: Add `storage.Schema` / `storage.Field` / `storage.FieldSet` to the Key Concepts table. Update the storeui description.

3. **Update brainstorm memory**: Mark `brainstorm_config-field-descriptions` as `Completed`.

4. **Run full test suite** to confirm nothing is broken.

### Acceptance Criteria

```bash
make test  # Full unit test suite passes
go vet ./...
# Verify documentation is updated
grep -l "Schema" internal/storage/CLAUDE.md internal/config/CLAUDE.md internal/storeui/CLAUDE.md
```

### Wrap Up

1. Update Progress Tracker: Task 5 -> `complete`
2. Append key learnings
3. Run review agents, then test-hunter as final step (see Context Window Management above).
4. Commit and push.
5. **DONE.** Inform the user the initiative is complete. Present summary of all changes.

# Init Commands Grand Refactor

**Branch:** `feat/init-commands-refactor`
**Parent memory:** `brainstorm_init-commands-refactor`

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Language Preset Definitions | `complete` | — |
| Task 2: Store-Backed Wizard (`storeui.Wizard[T]`) | `complete` | — |
| Task 3: Refactor `project init` Command | `complete` | — |
| Task 4: `clawker init` Alias + Cleanup | `complete` | — |

## Key Learnings

(Agents append here as they complete tasks)

- **Task 1**: `storage.NewFromString[Project](presetYAML, WithDefaultsFromStruct[Project]())` is the canonical way to load a preset into a store. Future consumer (Task 3) must propagate the returned error — never swallow it.
- **Task 1**: The old `clawker init` command (`internal/cmd/init/init.go`) has a silent failure bug: `editErr` from settings editor is silently swallowed (line ~202). The `project init` command handles this correctly. This will be resolved when the file is gutted in Task 4.
- **Task 1**: Test-hunter caught tautological tests — preset tests should assert contract invariants (every preset parses, has required fields, defaults fill gaps) not recite YAML content as assertions.
- **Task 2**: `Wizard[T]` when submitted but unchanged returns `Result{Saved: false}` and does NOT call `store.Write`. Init-style callers that need a file written regardless must handle this case explicitly.
- **Task 2**: Unmappable fields (KindMap, KindStructSlice, consumer kinds) are silently skipped by `fieldToWizardField`. Callers passing paths via `WithWizardFields` that refer to complex types will see fewer wizard steps than expected.
- **Task 2**: The `store.Set` closure in Wizard write-back applies fields sequentially. If `SetFieldValue` fails mid-loop, earlier fields are already in-memory but Write is skipped. Low risk since wizard validators reject bad input before submission.
- **Task 3**: Removed `Prompter` from `ProjectInitOptions` — the post-wizard `prompter.Confirm("Customize?")` flow was replaced by the in-wizard "Save and get started" / "Customize this preset" action picker.
- **Task 3**: `performSetupInput` should NOT carry the full `*ProjectInitOptions` — narrow to `ios`, `tui`, `force` to prevent re-resolving factory nouns inside the function.
- **Task 3**: Extracted `resolveInitEnv()` + `initEnv` to deduplicate factory-noun resolution between `runInteractive` and `runNonInteractive`.
- **Task 3**: `os.Stat` non-NotExist errors must be surfaced, not silently swallowed. Applied to both `.clawkerignore` creation and `bootstrapSettings()`.
- **Task 3**: `bootstrapSettings()` failures should be user-visible warnings (not just debug logs) since they cause cascading failures later.
- **Task 3**: Project name validation (`validateProjectName`) lives as a chokepoint inside `performProjectSetup`, not in individual callers. Non-interactive path auto-lowercases in `resolveInitEnv`.
- **Task 3**: Preset names with special chars (C/C++, C#/.NET) need sanitization when used as test project names — `sanitizeTestName()` helper strips `+`, `#`, `/`.
- **Task 3**: Action labels extracted as `actionSave`/`actionCustomize` constants to avoid fragile string coupling between wizard definition and branching logic.
- **Task 4**: Chose Option A (thin command with delegation) over Option B (Cobra `Aliases` field) — gives control over the alias tip message and future deprecation path.
- **Task 4**: `NewCmdInit` reuses `projectinit.ProjectInitOptions` directly rather than a local thin wrapper — avoids type duplication since init IS project init.
- **Task 4**: The `runF` parameter type changed from `func(ctx, *InitOptions)` to `func(ctx, *projectinit.ProjectInitOptions)` — root.go passes `nil` so no wiring change needed.

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`,`type-design-analyzer` subagents to review this task's changes, then fix any and all findings
5. Commit all changes from this task with a descriptive commit message
6. Present the handoff prompt from the task's Wrap Up section to the user
7. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

Clawker's init commands (`clawker init`, `clawker project init`) are being refactored into a unified, user-friendly guided setup flow. The core idea: `clawker project init` becomes the single entry point for all initialization. It offers language-based presets (Python, Go, Rust, TypeScript, Java, Ruby, C/C++, C#/.NET, Bare) for quick setup, and a "Build from scratch" path that walks users through each config field step by step with pre-populated defaults. The flow leverages `storeui` and `tui` infrastructure — the wizard is just another presentation mode for store-backed fields.

**Key decisions from brainstorm:**
- `clawker init` becomes an alias that redirects to `clawker project init`
- User-level settings bootstrap (settings.yaml) happens silently on first `project init` if missing
- Presets are Go YAML string constants fed to `storage.NewFromString[Project]()` — schema defaults fill gaps
- `config.NewFromString(projectYAML, settingsYAML)` creates both stores in one call for first-run
- A new `storeui.Wizard[T]` function bridges store fields → wizard fields (generic, reusable)
- The base image build step is removed from init entirely — user runs `clawker build` separately
- Re-running init on existing project: overwrite with warning + confirmation
- Project name must be lowercase (Docker BuildKit constraint)
- "Build from scratch" is just Bare preset with `autoCustomize: true`
- Every wizard field is pre-populated with sensible defaults — never blank

**Flow:**
```
1. Project name? → [dirname] (lowercase enforced)
2. Preset picker → Python / Go / Rust / TS / Java / Ruby / C++ / .NET / Bare / Build from scratch
   ├─ Language/Bare → "Save and get started" / "Customize this preset"
   │   ├─ Save → write → register → done
   │   └─ Customize → wizard pre-filled with preset values → write → register → done
   └─ Build from scratch → wizard with Bare defaults → write → register → done
3. "To customize further: clawker project edit"
```

### Key Files

| File | Purpose |
|------|---------|
| `internal/cmd/project/init/init.go` | Project init command (MAJOR REWRITE) |
| `internal/cmd/init/init.go` | User init command (becomes alias) |
| `internal/cmd/root/root.go` | Root command wiring (line 62: `initcmd.NewCmdInit`) |
| `internal/storeui/edit.go` | Existing `Edit[T]` — reference for `Wizard[T]` pattern |
| `internal/storeui/reflect.go` | `WalkFields()` — field discovery |
| `internal/storeui/value.go` | `SetFieldValue()` — reverse reflection writer |
| `internal/storeui/field.go` | `Field` type, `FieldKind` constants |
| `internal/tui/wizard.go` | `WizardField`, `WizardResult`, wizard model |
| `internal/tui/tui.go` | `TUI.RunWizard()` entry point |
| `internal/tui/fields.go` | `SelectField`, `TextField`, `ConfirmField` |
| `internal/config/schema.go` | `Project` struct — the schema being edited |
| `internal/config/defaults.go` | `GenerateDefaultsYAML[T]()` wrappers, `DefaultIgnoreFile` (dead funcs `NewProjectWithDefaults`/`NewSettingsWithDefaults` deleted in Task 1) |
| `internal/config/presets.go` | Language preset definitions (Task 1) |
| `internal/config/config.go` | `NewFromString()`, `Config` interface |
| `internal/storage/store.go` | `Store[T]`, `NewFromString[T]()` |
| `internal/storage/defaults.go` | `GenerateDefaultsYAML[T]()` |
| `internal/project/manager.go` | `ProjectManager.Register()` |
| `internal/cmd/project/shared/discovery.go` | `HasLocalProjectConfig()` |

### Design Patterns

- **Factory DI**: Commands receive `*cmdutil.Factory` with lazy noun closures (`f.Config()`, `f.ProjectManager()`, `f.TUI`). Options structs capture function references.
- **Store-backed fields**: `WalkFields(snapshot)` discovers fields via reflection → `SetFieldValue(ptr, path, val)` writes back with type coercion → `store.Set(fn)` + `store.Write()` persists.
- **Schema contract**: `Store[T Schema]` — all stored types implement `Fields() FieldSet`. Struct tags (`yaml`, `label`, `desc`, `default`, `required`) are the single source of truth.
- **Wizard-StoreUI bridge**: The new `Wizard[T]` maps `storeui.Field` → `tui.WizardField` (Kind → FieldKind, Value → Default, label/desc from struct tags). Same write-back path as `Edit[T]`.
- **Preset overlay**: Preset YAML string → `storage.NewFromString[Project](presetYAML, WithDefaultsFromStruct[Project]())` → `GenerateDefaultsYAML[T]()` fills any gaps from struct tags → `store.Write(ToPath(...))`.
- **Domain adapters**: `internal/config/storeui/project/` is the reference example — `Overrides()` customizes field display, `LayerTargets()` builds save destinations. The init wizard is a new consumer, not a wrapper around the existing adapter.
- **No manual struct constructors**: `NewProjectWithDefaults()` / `NewSettingsWithDefaults()` are deleted — all defaults flow through the store layer via `WithDefaultsFromStruct[T]()`.

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package `CLAUDE.md` before starting
- Use Serena tools for code exploration — read symbol bodies only when needed
- All new code must compile and tests must pass
- Follow existing test patterns in the package
- Never hardcode config paths — use `Config` interface accessors
- Store-backed fields use struct tags as single source of truth — no parallel metadata

---

## Task 1: Language Preset Definitions

**Creates:** `internal/config/presets.go`, `internal/config/presets_test.go`
**Depends on:** Nothing (leaf task)

### Implementation Phase

1. **Read existing code for context:**
   - Read `internal/config/schema.go` fully — understand every field on `Project`, `BuildConfig`, `SecurityConfig`, `AgentConfig`, `WorkspaceConfig`, `FirewallConfig`
   - Read `internal/bundler/defaults.go` — understand `FlavorToImage()` mapping and available flavors
   - Read `internal/storage/defaults.go` — understand `GenerateDefaultsYAML[T]()` and how defaults fill gaps
   - Read `internal/storage/store.go` — understand `NewFromString[T]()` signature and options

2. **Design the preset type (in `internal/config/`):**
   - Presets are a config-layer concern — they define language-specific `Project` configurations using schema types
   - Create a `Preset` struct: `Name string`, `Description string`, `YAML string`, `AutoCustomize bool`
   - Create a `Presets()` function returning `[]Preset` (ordered for display in picker)
   - "Build from scratch" is a Bare preset entry with `AutoCustomize: true`
   - Each preset's `Name` doubles as the select option label; `Description` is the secondary text
   - Lives alongside other config factories in `internal/config/`

3. **Define preset YAML strings** — each is a partial YAML that only specifies fields differing from schema defaults. `WithDefaultsFromStruct[Project]()` fills the rest. Each preset must include:
   - `build.image` — language-appropriate Docker image
   - `build.packages` — essential system packages for that language
   - `security.firewall.add_domains` — package registry domains for that ecosystem
   - Language-specific extras where sensible (e.g., Go needs `proxy.golang.org`, Python needs `pypi.org`)
   
   **Preset list:**
   | Preset | Image | Key Packages | Firewall Domains |
   |--------|-------|-------------|------------------|
   | Python | `python:3.12-bookworm` | git, curl, ripgrep, build-essential | pypi.org, files.pythonhosted.org, github.com, api.github.com |
   | Go | `golang:1.23-bookworm` | git, curl, ripgrep, make | proxy.golang.org, sum.golang.org, github.com, api.github.com |
   | Rust | `rust:1-bookworm` | git, curl, ripgrep, build-essential, pkg-config | crates.io, static.crates.io, index.crates.io, github.com, api.github.com |
   | TypeScript | `node:22-bookworm` | git, curl, ripgrep | registry.npmjs.org, github.com, api.github.com |
   | Java | `eclipse-temurin:21-bookworm` | git, curl, ripgrep, maven | repo1.maven.org, central.sonatype.com, github.com, api.github.com |
   | Ruby | `ruby:3.3-bookworm` | git, curl, ripgrep, build-essential | rubygems.org, index.rubygems.org, github.com, api.github.com |
   | C/C++ | `buildpack-deps:bookworm` | git, curl, ripgrep, cmake, make, gcc, g++ | github.com, api.github.com |
   | C#/.NET | `mcr.microsoft.com/dotnet/sdk:9.0-bookworm` | git, curl, ripgrep | api.nuget.org, github.com, api.github.com |
   | Bare | `buildpack-deps:bookworm-scm` | git, curl, ripgrep | github.com, api.github.com |
   | Build from scratch | (same as Bare) | (same as Bare) | (same as Bare) + `AutoCustomize: true` |

4. **Delete `NewProjectWithDefaults()` and `NewSettingsWithDefaults()`:**
   - Remove both functions from `internal/config/defaults.go`
   - They're dead code (no callers in production) and a redundant path to defaults — `GenerateDefaultsYAML[T]()` / `WithDefaultsFromStruct[T]()` is the canonical store-backed way
   - Update `internal/config/CLAUDE.md` to remove references
   - Update `.claude/rules/storage-schema.md` to remove the `NewProjectWithDefaults` entry
   - Update `.claude/docs/DESIGN.md` to reflect the new defaults path (presets + `GenerateDefaultsYAML`)

5. **Write unit tests:**
   - `TestPresets_AllParseSuccessfully` — iterate all presets, `storage.NewFromString[config.Project](p.YAML, storage.WithDefaultsFromStruct[config.Project]())` must not error
   - `TestPresets_FieldAssertions` — each preset has non-empty `build.image`, non-empty `build.packages`, non-empty `security.firewall.add_domains`
   - `TestPresets_NoDuplicateNames` — no two presets share a name
   - `TestPresets_BuildFromScratchIsAutoCustomize` — the "Build from scratch" entry has `AutoCustomize: true`
   - `TestPresets_BareAndBuildFromScratchSameYAML` — verify they share the same underlying YAML
   - All unit tests live in `internal/config/presets_test.go`

6. **Write e2e tests** (`test/e2e/presets_test.go`):
   - Presets must be validated with real Docker — unit tests only prove YAML parses, not that the config produces a working container
   - Use the existing `test/e2e/harness` infrastructure: `Harness` + `NewIsolatedFS` + real Config/Client/ProjectManager
   - Table-driven test iterating each preset (skip "Build from scratch" — same YAML as Bare):
     - Write preset YAML to `testenv.ProjectConfig`
     - `h.Run("project", "register", "--yes", presetName)`
     - `h.Run("build")` — verify image builds successfully
     - `h.RunInContainer(agent, "sh", "-c", verifyCmd)` — verify language runtime is present
   - Verification commands per preset:
     - Python: `python3 --version`
     - Go: `go version`
     - Rust: `rustc --version`
     - TypeScript: `node --version && npm --version`
     - Java: `java --version`
     - Ruby: `ruby --version`
     - C/C++: `gcc --version && cmake --version`
     - C#/.NET: `dotnet --version`
     - Bare: `git --version`
   - Test also verifies firewall domains resolve (if firewall enabled): `h.RunInContainer(agent, "sh", "-c", "curl -sI https://<ecosystem-domain>")`
   - These tests are Docker-dependent and slow (~30-60s per preset); use `t.Parallel()` where possible
   - Follow existing e2e patterns from `test/e2e/firewall_test.go` and `test/e2e/workdir_mounts_test.go`

### Acceptance Criteria

```bash
go build ./cmd/clawker
go test ./internal/config/... -run TestPresets -v
go vet ./internal/config/...
go test ./test/e2e/... -run TestPreset -v -timeout 10m  # Docker required
```

### Wrap Up

1. Update Progress Tracker: Task 1 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`,`type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 2. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the init-commands-refactor initiative. Read the Serena memory `init-commands-refactor` — Task 1 is complete. Begin Task 2: Store-Backed Wizard (`storeui.Wizard[T]`)."

---

## Task 2: Store-Backed Wizard (`storeui.Wizard[T]`)

**Creates:** `internal/storeui/wizard.go`, `internal/storeui/wizard_test.go`
**Depends on:** Understanding of `storeui` and `tui` packages (no code dependency on Task 1)

### Implementation Phase

1. **Read existing code for context:**
   - Read `internal/storeui/edit.go` — understand `Edit[T]`'s full flow (the reference pattern for `Wizard[T]`)
   - Read `internal/storeui/reflect.go` — understand `WalkFields()` return type and `Field` struct
   - Read `internal/storeui/field.go` — understand `FieldKind` constants and `Field` type
   - Read `internal/storeui/value.go` — understand `SetFieldValue()` signature
   - Read `internal/tui/wizard.go` — understand `WizardField`, `WizardResult`, `WizardFieldKind`
   - Read `internal/tui/tui.go` — understand `TUI.RunWizard()` signature
   - Read `internal/config/storeui/project/project.go` — understand how domain adapters customize fields via `Overrides()`

2. **Design the `Wizard[T]` API:**
   ```
   func Wizard[T storage.Schema](ios *iostreams.IOStreams, tuiRunner *tui.TUI, store *storage.Store[T], opts ...WizardOption) (WizardResult, error)
   ```
   
   **Options pattern** (mirrors `Edit[T]`'s options):
   - `WithWizardFields(paths ...string)` — include only these field paths (filter). Order determines wizard step order.
   - `WithWizardOverrides(overrides ...Override)` — reuse existing `Override` type for kind/options/label customization
   - `WithWizardTitle(title string)` — stepper bar title
   
   **Return type:**
   - `WizardResult` struct: `Saved bool`, `Cancelled bool`, `SavedCount int` (mirrors `Edit[T]`'s `Result`)

3. **Implement the field-to-wizard mapping (`fieldToWizardField`):**
   - `KindText` → `FieldText` (default = current value from snapshot)
   - `KindBool` → `FieldConfirm` (default = current bool value)
   - `KindInt` / `KindDuration` → `FieldText` (default = formatted current value, validator for parse)
   - `KindSelect` (from override) → `FieldSelect` (options from override)
   - `KindStringSlice` → `FieldText` with comma-separated default (this is a simplification for wizard mode — full list editor is for `Edit[T]`)
   - `KindMap` → `FieldText` with `key=val,key=val` format or skip (read-only note)
   - `KindStructSlice` → skip (too complex for wizard, direct to `project edit`)
   - Consumer-defined kinds (`> KindLast`) → skip
   - Label and description from struct tags (via `enrichWithSchema`)
   - Use field `Path` as `WizardField.ID`

4. **Implement the write-back flow:**
   - After `RunWizard()` returns with `Submitted: true`:
   - For each wizard value where value != original snapshot value:
     - `store.Set(func(t *T) { SetFieldValue(t, path, value) })`
   - `store.Write(storage.ToPath(targetPath))` — caller provides target path
   - Return `WizardResult{Saved: true, SavedCount: changedCount}`
   - If wizard cancelled: return `WizardResult{Cancelled: true}`

5. **Handle the target path question:**
   - `Wizard[T]` does NOT handle layer picking (that's `Edit[T]`'s domain)
   - Caller passes `WithWizardWritePath(path string)` — the file to write to
   - This is appropriate for init (always writes to a known path) vs edit (user picks layer)

6. **Write unit tests:**
   - `TestFieldToWizardField_KindMapping` — verify each `FieldKind` maps to correct `WizardFieldKind`
   - `TestFieldToWizardField_DefaultValues` — verify snapshot values become wizard defaults
   - `TestFieldToWizardField_Filtering` — verify `WithWizardFields` includes only specified paths in order
   - `TestFieldToWizardField_OverrideApplied` — verify overrides change kind/options/label
   - `TestFieldToWizardField_ComplexKindsSkipped` — `KindStructSlice`, `KindMap`, consumer kinds excluded
   - `TestWizardWriteBack_RoundTrip` — create store from YAML, map fields, simulate wizard values, write back, reload, verify values match. Use `testenv.New(t)` for isolated dirs.
   - `TestWizardWriteBack_UnchangedFieldsNotWritten` — verify only changed fields trigger `store.Set`

### Acceptance Criteria

```bash
go build ./cmd/clawker
go test ./internal/storeui/... -run TestWizard -v
go test ./internal/storeui/... -run TestFieldToWizardField -v
go vet ./internal/storeui/...
```

### Wrap Up

1. Update Progress Tracker: Task 2 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`,`type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 3. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the init-commands-refactor initiative. Read the Serena memory `init-commands-refactor` — Tasks 1-2 are complete. Begin Task 3: Refactor `project init` Command."

---

## Task 3: Refactor `project init` Command

**Creates/modifies:** `internal/cmd/project/init/init.go` (major rewrite), `internal/cmd/project/init/init_test.go`
**Depends on:** Task 1 (presets), Task 2 (storeui.Wizard[T])

### Implementation Phase

1. **Read existing code:**
   - Read `internal/cmd/project/init/init.go` fully — understand current flow, Options struct, Factory wiring
   - Read `internal/config/presets.go` (from Task 1) — understand preset API
   - Read `internal/storeui/wizard.go` (from Task 2) — understand Wizard[T] API
   - Read `internal/cmd/project/shared/discovery.go` — understand `HasLocalProjectConfig()`
   - Read `internal/config/config.go` — understand `NewFromString()`, settings path resolution
   - Read `internal/project/manager.go` — understand `Register()` contract

2. **Rewrite `ProjectInitOptions`:**
   - Keep: `IOStreams`, `TUI`, `Prompter`, `Config`, `Logger`, `ProjectManager`, `Force`, `Yes`, `Name`
   - Remove: anything related to base image build (Client, build logic)
   - Options struct should have function fields matching Factory nouns

3. **Implement the new interactive flow:**

   **Phase A — Pre-checks:**
   - Get CWD
   - Check for existing config via `HasLocalProjectConfig(cfg, cwd)`
   - If exists and not `--force`: wizard confirm field "Config exists. Overwrite?" → decline = exit with message
   
   **Phase B — Settings bootstrap (silent):**
   - Check if `cfg.SettingsStore()` has any layers (files exist)
   - If no settings file exists: `GenerateDefaultsYAML[Settings]()` → write to `cfg.ConfigDir()/settings.yaml`
   - Also ensure `cfg.ShareSubdir()` exists
   - This is silent — no user prompt
   
   **Phase C — Project name:**
   - Wizard text field: "Project name" with default = `filepath.Base(cwd)` or positional arg
   - Validator: lowercase check, no spaces, valid Docker name chars
   - Validator should lowercase the input automatically or reject with clear message
   
   **Phase D — Preset picker:**
   - Wizard select field: "Choose a starting template" with all presets from `Presets()`
   - Each option: `Label: preset.Name`, `Description: preset.Description`
   
   **Phase E — Branching:**
   - If selected preset has `AutoCustomize: true` ("Build from scratch"):
     - Load Bare preset YAML into store
     - Run `storeui.Wizard[T]` with full field list → user walks through each field
   - If selected preset is Language/Bare:
     - Second wizard: "Save and get started" / "Customize this preset"
     - If "Save and get started": load preset YAML into store, skip wizard
     - If "Customize": load preset YAML into store, run `storeui.Wizard[T]` pre-filled
   
   **Phase F — Write:**
   - `store.Write(storage.ToPath(configPath))` — write clawker.yaml
   - Create `.clawkerignore` if not exists (using `config.DefaultIgnoreFile`)
   - `projectManager.Register(ctx, projectName, cwd)`
   
   **Phase G — Next steps:**
   - Print: project name, config path, "Next: clawker build && clawker run"
   - Print: "To customize further: clawker project edit"

4. **Implement non-interactive mode (`--yes` or non-TTY):**
   - Project name: dirname or positional arg (lowercased)
   - Preset: Bare (sensible default — no language assumption)
   - Auto-save, no wizard
   - Fail if config exists and no `--force`

5. **Define wizard field list for Customize path:**
   - Fields to include in `storeui.Wizard[T]` (in order):
     - `build.image` — base Docker image
     - `build.packages` — system packages (comma-separated in wizard)
     - `build.instructions.root_run` — root-level build commands
     - `build.instructions.user_run` — user-level build commands
     - `build.inject.after_from` — Dockerfile injections after FROM
     - `build.inject.after_packages` — Dockerfile injections after packages
     - `agent.env` — environment variables (may need KindMap handling or skip)
     - `security.firewall.enable` — firewall on/off
     - `security.firewall.add_domains` — allowed domains
     - `workspace.default_mode` — bind/snapshot (override to KindSelect)
   - Use `WithWizardOverrides` for fields needing kind customization (workspace mode → select)
   - Complex fields that can't fit wizard UX gracefully → omit, mention in next-steps output

6. **Update/write tests:**
   - Test the preset → store → write round trip
   - Test overwrite detection + confirmation
   - Test non-interactive mode defaults
   - Test project name lowercase enforcement
   - Test settings bootstrap (first-run creates settings.yaml)

### Acceptance Criteria

```bash
go build ./cmd/clawker
go test ./internal/cmd/project/init/... -v
go vet ./internal/cmd/project/init/...
make test  # full unit test suite passes
```

### Wrap Up

1. Update Progress Tracker: Task 3 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`,`type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 4. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the init-commands-refactor initiative. Read the Serena memory `init-commands-refactor` — Tasks 1-3 are complete. Begin Task 4: `clawker init` Alias + Cleanup."

---

## Task 4: `clawker init` Alias + Cleanup

**Creates/modifies:** `internal/cmd/init/init.go` (gutted → alias), `internal/cmd/root/root.go`, docs
**Depends on:** Task 3 (project init must be fully working)

### Implementation Phase

1. **Read existing code:**
   - Read `internal/cmd/init/init.go` — understand current structure
   - Read `internal/cmd/root/root.go` — understand how init is wired (line 62)
   - Read `.claude/docs/CLI-VERBS.md` — understand docs to update

2. **Convert `clawker init` to alias:**
   - Option A (preferred): Make `clawker init` a thin command that prints a deprecation/redirect notice and then runs the project init command programmatically. Use `cobra.Command.RunE` that calls the project init run function.
   - Option B: Make `clawker init` literally an alias via Cobra's `Aliases` on the project init command. Simpler but less control over messaging.
   - Choose Option A for user-friendliness — print "Running project init..." then delegate.
   - Preserve `--yes` and `--force` flags, forward them.
   - Accept optional positional arg (project name), forward it.

3. **Clean up old init code:**
   - Remove: `buildWizardFields()`, `flavorFieldOptions()`, `saveUserProjectConfig()`, `performSetup()`, build-related logic
   - Remove: old test code that tested the removed flows
   - Keep: the command registration in root.go (it now points to the alias)

4. **Update documentation:**
   - Update `CLAUDE.md` — reflect new init flow, remove references to old `clawker init` as separate command
   - Update `.claude/docs/CLI-VERBS.md` — mark `clawker init` as alias for `clawker project init`
   - Update `internal/cmd/init/CLAUDE.md` if it exists
   - Regenerate CLI docs: `go run ./cmd/gen-docs --doc-path docs --markdown --website`

5. **Update brainstorm memory:**
   - Mark `brainstorm_init-commands-refactor` status as `Completed`

6. **Write/update tests:**
   - Test that `clawker init` delegates to project init
   - Test flag forwarding
   - Remove old init-specific tests

### Acceptance Criteria

```bash
go build ./cmd/clawker
go test ./internal/cmd/init/... -v
go test ./internal/cmd/project/init/... -v
go vet ./...
make test  # full unit test suite passes
```

### Wrap Up

1. Update Progress Tracker: Task 4 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`,`type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Initiative complete. Inform the user:

> **Initiative complete.** All 4 tasks done. `clawker project init` is now a guided setup with language presets and store-backed wizard. `clawker init` redirects to it. Base image build removed from init. Run `make test` for final verification.

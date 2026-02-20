# Config Package Documentation Update

> **Status:** Complete
> **Branch:** `refactor/configapocalypse`
> **Last updated:** 2026-02-19

## End Goal

Update all documentation across the repo to reflect the current state of the config package refactor — specifically:
1. Validation via `viper.UnmarshalExact` is now implemented (not missing)
2. Constants in `consts.go` are now private (lowercase), accessible only through `Config` interface methods
3. The `Config` interface is the single interface all callers receive — no reaching into package internals

## What Was Done

### 1. `internal/config/CLAUDE.md` — UPDATED [DONE]
- **Files table**: `consts.go` description rewritten to reflect private constants
- **Constants section**: Replaced old exported `const` code block with table mapping private constant → Config method → value. Only `Mode`/`ModeBind`/`ModeSnapshot` remain exported
- **Config Interface section**: Added all 10 constant accessor methods (`Domain()`, `LabelDomain()`, `ConfigDirEnvVar()`, subdirs, etc.)
- **Pattern 2 (Validator)**: Rewritten from "removed, not yet rebuilt" to document `UnmarshalExact` validation
- **Pattern 5 (DataDir)**: Updated to use `cfg.LogsSubdir()` via interface, not hardcoded strings
- **Pattern 6 (Label/PID)**: Updated to reference Config interface methods
- **Gotchas**: "Viper ignores unknown fields" → "Unknown fields are rejected" with implementation details

### 2. `.claude/docs/ARCHITECTURE.md` — UPDATED [DONE]
- Added new `internal/config` section describing design principle (single interface, private internals, propose new methods)
- `config.Provider` → `config.Config` (all occurrences)
- `configtest/` → `mocks/` in test subpackages table
- Factory signature updated to `func() (config.Config, error)`
- Config wiring order updated from "Config gateway" to "Config (lazy, config.NewConfig() via sync.Once)"
- Constants described as private, exposed through interface

### 3. `.claude/docs/DESIGN.md` — UPDATED [DONE]
- Project resolution paragraph rewritten (removed old Provider API references)
- Factory lookup step updated
- Lazy field signature corrected to `func() (config.Config, error)`
- `config.Provider` → `config.Config` (all occurrences)

### 4. `.claude/docs/TESTING-REFERENCE.md` — UPDATED [DONE]
- All `config.Provider` → `config.Config`
- All `config.NewConfigForTest` → `configmocks.NewBlankConfig()` (import `configmocks "github.com/schmitthub/clawker/internal/config/mocks"`)
- All `configtest.*` references removed
- Config test section rewritten with `stubs.go` examples
- Factory wiring examples corrected to `func() (config.Config, error)`
- "When to Use Which" table rewritten for new test helpers

### 5. `.claude/rules/testing.md` — UPDATED [DONE]
- Config row in DAG test infrastructure table: `configtest/` → `mocks/`

### 6. `CLAUDE.md` (root) — UPDATED [DONE]
- `config.Config` key concept rewritten: single interface, private internals, UnmarshalExact validation

### 7. `.claude/memories/prompter-wizard.md` — UPDATED [DONE]
- Removed `configtest.NewInMemorySettingsLoader` reference

### 8. `.claude/templates/container-command-migration.md` — UPDATED [DONE]
- All Factory config wiring examples updated from `*config.Config`/`NewConfigForTest` to `(config.Config, error)`/`NewMockConfig()`

### 9. Serena memories — UPDATED [DONE]
- `configapocalypse-check-migration`: Updated validation status
- `configapocalypse-prd`: Updated validation status

## Remaining Work (from parent refactor)

The overall configapocalypse refactor still has consumer migrations pending — tracked in `configapocalypse-prd` memory. The documentation update work tracked by THIS memory is complete.

## IMPERATIVE

Always check with the user before proceeding with the next todo item. If all work is done, ask the user if they want to delete this memory.

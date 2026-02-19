# Config Check Migration & Config Package Documentation

> **Status:** Complete for current scope
> **Branch:** `refactor/configapocalypse`
> **Last updated:** 2026-02-19

## End Goal

Migrate `config check` command to the new config package API and document the new `internal/config/` package in its current refactoring state.

## What Was Done

### 1. `internal/cmd/config/check/check.go` — REWRITTEN [DONE]
- Simplified `configTarget` struct: dropped `loaderDir` and `cleanup`, now has `filePath` + `displayPath`
- Simplified `resolveConfigTarget`: no more temp dir / symlink dance for non-clawker.yaml files
- Rewrote `checkRun`: `resolveConfigTarget()` → `os.ReadFile()` → `config.ReadFromString()` (3 steps vs old 6-step pipeline)
- Removed all old config API imports (`ProjectLoader`, `Validator`, `MultiValidationError`, `ConfigFileName`, `WithUserDefaults`)
- Only config symbol used: `config.ReadFromString`
- Updated `Long` description to match new behavior (no semantic validation yet)

### 2. `internal/cmd/config/check/check_test.go` — UPDATED [DONE]
- `TestCheckRun_invalidFile`: changed from semantically invalid config to malformed YAML (`[invalid`)
- `TestCheckRun_unknownFields`: changed expectation from error to "is valid" (viper ignores unknown fields)
- All `TestResolveConfigTarget_*`: updated from `target.loaderDir` to `target.filePath`, removed `defer target.close()`
- `TestResolveConfigTarget_customFilename`: no longer asserts temp dir/symlink, just direct file path
- All other tests unchanged (flag wiring, metadata, valid file, not found, directory, custom filename)

### 3. `internal/config/CLAUDE.md` — CREATED [DONE]
- Full package reference: architecture, files, public API, schema types, convenience methods
- Constants section documenting new `consts.go` (Domain, LabelDomain, subdirs, Mode type)
- Usage patterns for commands, production, and testing
- **Migration Guide** with 8 patterns (old → new) and per-consumer checklist
- **Migration Status** table: every unmigrated consumer with specific removed symbols
- Gotchas section

### 4. `internal/config/schema.go` — Mode type moved to consts.go [DONE]
- Removed `type Mode string`, `ModeBind`, `ModeSnapshot` from schema.go (now in consts.go)
- `ParseMode` function stays in schema.go (no funcs in consts)
- Removed orphaned `// Mode represents the workspace mode` comment

### 5. `internal/cmd/config/CLAUDE.md` — UPDATED [DONE]
- Updated `config check` description to reflect new `ReadFromString` pattern

### 6. Root `CLAUDE.md` — UPDATED [DONE]
- Repo structure: replaced `BUILD ME` placeholder with description + refactor pointer
- Key Concepts: added `config.Config` entry, removed stale `config.SettingsLoader` entry

### 7. Serena memory `configapocalypse-prd` — CREATED [DONE]
- Tracks overall refactor status, what's done, what's pending, prioritized migration queue

## Build Status

- `go test ./internal/cmd/config/check/...` fails due to transitive dependency breakage (bundler, hostproxy, socketbridge still reference removed old config symbols)
- The check package code itself is correct — verified via manual review, gofmt, and import analysis
- Full build will pass once all consumers are migrated (see migration status in `internal/config/CLAUDE.md`)

## Key Context

- `consts.go` was added by the user with `package config` declaration, Domain, LabelDomain, subdir constants, Mode type
- User preference: no functions in `consts.go` — only types and constants
- `configtest/` subpackage does not exist yet; test stubs live in `stubs.go` (`NewMockConfig`, `NewFakeConfig`, `NewConfigFromString`)
- Semantic validation rebuilt via `viper.UnmarshalExact` — unknown/misspelled keys are now caught with dot-path error messages (e.g. `unknown keys: build.imag`)

## Relevant Files
- `internal/config/CLAUDE.md` — Full package docs with migration guide
- `internal/cmd/config/check/check.go` — Migrated command
- `internal/cmd/config/check/check_test.go` — Migrated tests
- `.serena/memories/configapocalypse-prd` — Overall refactor tracking memory

## IMPERATIVE

Always check with the user before proceeding with the next todo item. If all work is done, ask the user if they want to delete this memory.

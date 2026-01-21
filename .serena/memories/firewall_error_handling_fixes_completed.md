# Firewall Error Handling Fixes - COMPLETED

**Date:** 2026-01-20
**Status:** ✅ FULLY IMPLEMENTED AND TESTED

## Summary

Implemented two fixes to improve error handling and user feedback for firewall configuration:

1. **JSON marshaling failure** - Changed from silent fallback to fail-fast with proper error propagation
2. **override_domains validation** - Changed from error to warning since behavior is well-defined

## Change 1: JSON Marshaling - Fail Fast

### Problem
`domainsToJSON()` in `pkg/build/dockerfile.go` silently returned `[]` on error, which could result in a misconfigured firewall.

### Solution
Changed `domainsToJSON` to return `(string, error)` and propagate errors up the call chain.

### Files Modified
- `pkg/build/dockerfile.go`:
  - `domainsToJSON(domains []string) (string, error)` - now returns error
  - `createContext()` returns `(*DockerfileContext, error)`
  - `buildContext()` returns `(*DockerfileContext, error)`
  - `Generate()` propagates errors from `buildContext()`
  - Errors logged to file AND printed to stderr

- `pkg/build/firewall_test.go`:
  - Updated `TestDomainsToJSON` for new signature with `wantErr` field

## Change 2: override_domains - Warning Instead of Error

### Problem
`internal/config/validator.go` treated `override_domains` set alongside `add_domains`/`remove_domains` as an error, blocking builds unnecessarily.

### Solution
Changed to warning since behavior is well-defined (override wins).

### Files Modified
- `internal/config/validator.go`:
  - Added `warnings []string` field to Validator struct
  - Added `addWarning(field, message string)` method
  - Added `Warnings() []string` getter method
  - Changed line 178 from `addError` to `addWarning`

- `pkg/cmd/config/config.go`:
  - Added loop after validation to print warnings via `cmdutil.PrintWarning()`

- `pkg/cmd/image/build/build.go`:
  - Added loop after validation to print warnings via `cmdutil.PrintWarning()`

## Verification Completed

- ✅ All unit tests pass: `go test ./...`
- ✅ Build successful: `go build -o ./bin/clawker ./cmd/clawker`
- ✅ Config check with override_domains + add_domains shows WARNING (not error)
- ✅ Config check passes (is valid) with warning printed
- ✅ Warning logged to file AND printed to stderr

## Git Status

Branch: a/ralph-prep
Changes are unstaged - ready for commit.

## Key Code Locations

- `domainsToJSON`: `pkg/build/dockerfile.go:258-275`
- `Validator.addWarning`: `internal/config/validator.go:54-62`
- `Validator.Warnings`: `internal/config/validator.go:64-66`
- Warning check: `internal/config/validator.go:176-180`
- Config check warnings: `pkg/cmd/config/config.go:99-102`
- Build warnings: `pkg/cmd/image/build/build.go:119-122`

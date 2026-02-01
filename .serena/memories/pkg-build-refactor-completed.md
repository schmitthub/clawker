# pkg/build → internal/build Refactoring

**Status:** Completed (2026-01-31)

## What Changed
- Moved all of `pkg/build/` (files, subpackages, templates) into `internal/build/`
- Updated all import paths across the codebase
- Split `pkg/build/firewall_test.go`: pure unit tests → `internal/build/firewall_test.go`, Docker integration tests dropped (redundant with existing `test/internals/firewall_test.go`)
- Updated documentation: root CLAUDE.md, internal/build/CLAUDE.md, ARCHITECTURE.md, TESTING-REFERENCE.md, audit-memory REFERENCE.md
- `pkg/` now only contains `whail/`

## Verification
- `go build ./...` — clean
- `go vet ./...` — clean
- `make test` — 2683 tests pass, 0 failures
- No `pkg/build` references remain in any file

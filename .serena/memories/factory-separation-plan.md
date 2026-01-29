# Factory Separation Plan — COMPLETED

Phase 1 (Factory Separation) has been implemented:
- `internal/cmdutil/factory.go` — Pure struct with 16 closure fields, no methods, no constructor
- `internal/cmd/factory/default.go` — `New(version, commit)` wires all sync.Once closures
- `internal/clawker/cmd.go` — Updated to import `factory.New()`
- `internal/cmd/factory/default_test.go` — Moved from cmdutil, adapted for public API
- 11 test files updated to use `&cmdutil.Factory{IOStreams: tio.IOStreams}` struct literals
- All tests pass, build compiles cleanly

Next phases (from architecture-factory-pattern memory):
- Phase 2: Add runF to existing Options commands
- Phase 3: Refactor direct-call commands to Options pattern
- Phase 4: Lightweight cmdutil (interfaces)

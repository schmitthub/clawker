# Factory Separation Plan

## Completed Phases

### Phase 1: Factory Separation — COMPLETED (PR #74)
- `internal/cmdutil/factory.go` — Pure struct with 16 closure fields, no methods, no constructor
- `internal/cmd/factory/default.go` — `New(version, commit)` wires all sync.Once closures
- `internal/clawker/cmd.go` — Updated to import `factory.New()`
- All tests pass, build compiles cleanly

### Phase 4: Lightweight cmdutil — COMPLETED (branch: a/cmdutil-light)
- Extracted heavy deps from cmdutil to dedicated packages:
  - `internal/resolver/` — image resolution (ResolveImage, ResolveAndValidateImage, FindProjectImage)
  - `internal/build/defaults.go` — DefaultImageTag, FlavorToImage, BuildDefaultImage
  - `internal/project/` — RegisterProject
  - `internal/docker/` — ContainerNamesFromAgents (added), inlined ResolveContainerName
  - `internal/cmdutil/output.go` — duck-typed interface replaces docker import
- cmdutil now contains only: Factory struct, output utils, arg validators (no docker import)
- Decision tree documented in `.claude/rules/dependency-placement.md` and DESIGN.md §3.4

## Remaining Phases

- Phase 2: Add runF to existing Options commands
- Phase 3: Refactor direct-call commands to Options pattern

# Phase 4: Lightweight cmdutil — Implementation Plan

**Status: COMPLETED**
**Branch: a/cmdutil-light**
**Depends on: Phase 1 (factory separation) — COMPLETED in PR #74**

## Design Principle: Dependency Placement Decision Tree

```
"Where does my heavy dependency go?"
              │
              ▼
Can it be constructed at startup, before any command runs?
              │
       ┌──────┴──────┐
       YES            NO (needs CLI args, runtime context)
       │              │
       ▼              ▼
  3+ commands?    Lives in: internal/<package>/
       │          Constructed in: run function
  ┌────┴────┐     Tested via: inject mock on Options
  YES       NO
  │         │
  ▼         ▼
FACTORY   OPTIONS STRUCT
FIELD     (command imports package directly)
```

Rules:
- Implementation always lives in `internal/<package>/` — never in `cmdutil/`
- The only question is **who constructs it**: `factory.New()` at startup, or each command's run function
- `cmdutil/` contains only: Factory struct (DI container), output utilities, arg validators
- Heavy command helpers (resolution, building, registration) live in their own `internal/` packages

## Function Placement Decisions

| Function | Needs runtime context? | Shared? | Decision | Target |
|---|---|---|---|---|
| `ResolveContainerName` | No — pure wrapper | 17 cmds | **Inline** → `docker.ContainerName()` | Delete from cmdutil |
| `ResolveImage` et al. | Yes — Docker client, config | 2–3 cmds | Run function | `internal/resolver/` |
| `BuildDefaultImage` | Yes — flavor from CLI | 1–2 cmds | Run function | `internal/build/` |
| `FlavorToImage`, `DefaultFlavorOptions` | No — pure | 2–3 cmds | Co-locate with BuildDefaultImage | `internal/build/` |
| `RegisterProject` | Yes — workDir, name from CLI | 2 cmds | Run function | `internal/project/` |
| `HandleError` etc. | No — pure | 8+ cmds | Stay | `internal/cmdutil/` |
| `NoArgs`, `ExactArgs` etc. | No — pure | 14 cmds | Stay | `internal/cmdutil/` |
| `AgentArgsValidator` | No — pure | 3 cmds | Stay | `internal/cmdutil/` |

## Steps (execute in order, verify build+tests after each)

### Step 1: Remove docker import from output.go — COMPLETED
- Replace `*docker.DockerError` type assertion with duck-typed `userFormattedError` interface
- Use `errors.As()` instead of direct type assertion
- Files: `internal/cmdutil/output.go`, `internal/cmdutil/output_test.go`

### Step 2: Inline ResolveContainerName → docker.ContainerName — COMPLETED
- Replace all 17 container command call sites
- Also inline `ResolveContainerNames` and `ResolveContainerNamesFromAgents` (or move to `internal/docker/`)
- Delete functions from `internal/cmdutil/resolve.go`

### Step 3: Extract image resolution → internal/resolver/ — COMPLETED
- Create `internal/resolver/image.go` with: ResolveDefaultImage, FindProjectImage, ResolveImage, ResolveImageWithSource, ResolveAndValidateImage
- Create `internal/resolver/types.go` with: ImageSource, ResolvedImage, ImageValidationDeps
- Move tests from `internal/cmdutil/resolve_test.go` and `resolve_integration_test.go`
- Update consumers: container/create, container/run, container/run_test

### Step 4: Extract build utilities → internal/build/ — COMPLETED
- Create `internal/build/defaults.go` (package already has Builder, EnsureImage, Build)
- Move: DefaultImageTag, FlavorOption, DefaultFlavorOptions, FlavorToImage, BuildDefaultImage
- Move tests from `internal/cmdutil/image_build_test.go`
- Update consumers: init, project/init, resolver/image.go (cross-reference from step 3)

### Step 5: Extract project registration → internal/project/ — COMPLETED
- Create `internal/project/register.go` with RegisterProject()
- Move tests from `internal/cmdutil/register_test.go`
- Update consumers: project/init, project/register

### Step 6: Clean up resolve.go remnants — COMPLETED
- Delete `internal/cmdutil/resolve.go` (should be empty after steps 2–3)

### Step 7: Documentation — COMPLETED
- Create: `internal/resolver/CLAUDE.md`, `internal/project/CLAUDE.md`, `internal/build/CLAUDE.md`
- Update: `internal/cmdutil/CLAUDE.md` (prune removed sections)
- Update: `CLAUDE.md` root (repo structure + decision tree reference)
- Create: `.claude/rules/dependency-placement.md` (auto-loaded rule with decision tree)
- Update: `.claude/memories/DESIGN.md` (add decision tree under section 3.3)
- Update: `.serena/memories/factory-separation-plan` (mark Phase 4 complete)

## Resulting cmdutil (4 files)

```
internal/cmdutil/
  factory.go    — Factory struct (DI container, concrete types)
  output.go     — HandleError, PrintError, PrintNextSteps, etc. (iostreams only)
  required.go   — NoArgs, ExactArgs, AgentArgsValidator (cobra only)
  project.go    — ErrAborted (stdlib only)
```

## New packages

```
internal/resolver/       — Image resolution (runtime Docker + config)
internal/build/          — defaults.go added to existing package
internal/project/        — RegisterProject
```

## Verification

```bash
# After each step:
go build ./...
go test ./...
go vet ./...

# After all steps:
go test -tags=integration ./internal/cmd/... -v -timeout 10m
```

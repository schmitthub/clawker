# Factory runF Migration — Master Initiative

## Overview

Migrate all 48 CLI commands to the standardized runF pattern. Each command gets its own session. This memory is the single source of truth for the initiative.

## Naming Conventions

| Element | Pattern | Example (verb=`stop`) |
|---------|---------|----------------------|
| Options struct | `<Verb>Options` | `StopOptions` |
| Constructor | `NewCmd<Verb>` | `NewCmdStop` |
| Run function | `<verb>Run` | `stopRun` |
| Test hook parameter | `runF` (always) | constructor param, not on Options |

Special case: `run` command → `runRun` (no exceptions).

## Universal Workflow (per command)

### Phase 1: Migration

#### Step 0: Inventory & Blast Radius
- Use Serena `find_symbol` + `find_referencing_symbols` to map all references to constructor, Options, run function
- Document blast radius in command tracking memory
- Identify: parent registration site, test files, integration test files

#### Step 0.5: Package Extraction (if needed)
- Only for commands with **unexported constructors** (ralph/*, monitor/*, config/check)
- Create subpackage: `internal/cmd/<group>/<verb>/`
- Export constructor: `newCmdX` → `NewCmdX`
- Export Options: `xOptions` → `XOptions`  
- Export run: keep unexported but ensure proper signature
- Update parent to import subpackage and call exported constructor

#### Step 1: Naming Standardization
- Rename Options struct → `<Verb>Options` (if not already)
- Rename constructor → `NewCmd<Verb>` (if not already)
- Rename run function → `<verb>Run` (if not already)
- Use Serena `rename_symbol` for codebase-wide renames

#### Step 2: Options Factory Deps
- Options struct gets **only the Factory deps it actually uses**
- Assign deps to `opts` at the top of `NewCmd<Verb>`, before cmd declaration
- Pattern:
  ```go
  func NewCmdStop(f *cmdutil.Factory, runF func(context.Context, *StopOptions) error) *cobra.Command {
      opts := &StopOptions{
          IOStreams: f.IOStreams,
          Client:   f.Client,
      }
      // ... cmd declaration follows
  ```

#### Step 3: Positional Args & Flags
- All positional args and flag values must be assigned to `opts` fields
- Assignment happens **in RunE**, before the `runF` dispatch
- Pattern:
  ```go
  RunE: func(cmd *cobra.Command, args []string) error {
      opts.Names = args  // positional args → opts field
      if runF != nil {
          return runF(cmd.Context(), opts)
      }
      return stopRun(cmd.Context(), opts)
  },
  ```

#### Step 4: Add runF Test Hook
- Constructor signature: `NewCmdX(f *cmdutil.Factory, runF func(context.Context, *XOptions) error) *cobra.Command`
- RunE dispatches to `runF` if non-nil, otherwise calls `<verb>Run`
- Parent registration passes `nil`:
  ```go
  cmd.AddCommand(stop.NewCmdStop(f, nil))
  ```

#### Step 5: Rewrite Unit Tests
- Tests use runF capture pattern:
  ```go
  var gotOpts *StopOptions
  cmd := NewCmdStop(&cmdutil.Factory{IOStreams: ios}, func(_ context.Context, opts *StopOptions) error {
      gotOpts = opts
      return nil
  })
  ```
- **No RunE overrides** — test via runF capture only
- Assert opts fields populated correctly from args/flags
- If integration tests exist, update constructor calls to include `nil` runF parameter

#### Step 6: Verify

```bash
go build ./...
go test ./internal/cmd/<group>/<verb>/... -v -count=1
go test -tags=integration ./internal/cmd/<group>/<verb>/... -v -timeout 10m  # only if integration tests exist
go test ./... -count=1
```

---

## !! CRITICAL — MANDATORY REVIEW GATE !!

**You MUST stop here and present all changes to the user for review.**
**NEVER proceed to Phase 2 without explicit user approval.**
Do NOT update the inventory. Do NOT generate a handoff prompt.
Do NOT mark anything as DONE. **WAIT for the user.**

---

### Phase 2: Closure (after user approval only)

1. Update this memory's inventory (status → DONE)
2. Generate handoff prompt (see below)
3. Stop work

## Handoff Prompt Template

```
Continue the factory runF migration. Read the Serena memory
`factory-runf-migration` for the master initiative, find the
next NOT STARTED command, and execute the universal workflow.
```

## Acceptance Criteria (per command)

- [ ] Naming: `<Verb>Options`, `NewCmd<Verb>`, `<verb>Run`
- [ ] Options has only-used Factory deps, assigned at top of NewCmd
- [ ] Args/flags assigned to opts in RunE before runF dispatch
- [ ] runF test hook wired; parent passes nil
- [ ] Unit tests use runF capture (no RunE overrides)
- [ ] Integration tests updated with new constructor signature (if they exist)
- [ ] `go build ./...` passes
- [ ] `go test ./...` passes
- [ ] `go test -tags=integration` passes (if applicable)
- [ ] **User explicitly approved changes in this session**

---

## Command Inventory

Status values: `NOT STARTED` | `IN PROGRESS` | `DONE` | `SKIP`

### Container Commands (20)

| # | Package | Status | Session Memory |
|---|---------|--------|----------------|
| 1 | container/attach | DONE | — |
| 2 | container/cp | DONE | — |
| 3 | container/create | DONE | — |
| 4 | container/exec | DONE | — |
| 5 | container/inspect | DONE | — |
| 6 | container/kill | DONE | — |
| 7 | container/list | DONE | — |
| 8 | container/logs | DONE | — |
| 9 | container/pause | DONE | — |
| 10 | container/remove | DONE | — |
| 11 | container/rename | DONE | — |
| 12 | container/restart | DONE | — |
| 13 | container/run | DONE | — |
| 14 | container/start | DONE | — |
| 15 | container/stats | DONE | — |
| 16 | container/stop | DONE | — |
| 17 | container/top | DONE | — |
| 18 | container/unpause | DONE | — |
| 19 | container/update | DONE | — |
| 20 | container/wait | DONE | — |

### Image Commands (5)

| # | Package | Status | Session Memory |
|---|---------|--------|----------------|
| 21 | image/build | DONE | — |
| 22 | image/list | DONE | — |
| 23 | image/inspect | DONE | — |
| 24 | image/prune | DONE | — |
| 25 | image/remove | DONE | — |

### Volume Commands (5)

| # | Package | Status | Session Memory |
|---|---------|--------|----------------|
| 26 | volume/create | DONE | — |
| 27 | volume/list | DONE | — |
| 28 | volume/inspect | DONE | — |
| 29 | volume/prune | DONE | — |
| 30 | volume/remove | DONE | — |

### Network Commands (5)

| # | Package | Status | Session Memory |
|---|---------|--------|----------------|
| 31 | network/create | DONE | — |
| 32 | network/list | DONE | — |
| 33 | network/inspect | DONE | — |
| 34 | network/prune | DONE | — |
| 35 | network/remove | DONE | — |

### Ralph Commands (4) — NEEDS PACKAGE EXTRACTION

| # | Package | Status | Session Memory |
|---|---------|--------|----------------|
| 36 | ralph/run | DONE | — |
| 37 | ralph/status | DONE | — |
| 38 | ralph/reset | NOT STARTED | — |
| 39 | ralph/tui | NOT STARTED | — |

### Monitor Commands (4) — NEEDS PACKAGE EXTRACTION

| # | Package | Status | Session Memory |
|---|---------|--------|----------------|
| 40 | monitor/init | NOT STARTED | — |
| 41 | monitor/up | NOT STARTED | — |
| 42 | monitor/down | NOT STARTED | — |
| 43 | monitor/status | NOT STARTED | — |

### Config Commands (1) — NEEDS PACKAGE EXTRACTION

| # | Package | Status | Session Memory |
|---|---------|--------|----------------|
| 44 | config/check | NOT STARTED | — |

### Top-Level Commands (4)

| # | Package | Status | Session Memory |
|---|---------|--------|----------------|
| 45 | init | NOT STARTED | — |
| 46 | project/init | NOT STARTED | — |
| 47 | project/register | NOT STARTED | — |
| 48 | generate | NOT STARTED | — |

### Parent Command Registrations (update after children migrate)

| Parent | Children Count | Status |
|--------|---------------|--------|
| container/container.go | 20 | DONE |
| image/image.go | 5 | DONE |
| volume/volume.go | 5 | DONE |
| network/network.go | 5 | DONE |
| ralph/ralph.go | 4 | NOT STARTED |
| monitor/monitor.go | 4 | NOT STARTED |
| config/config.go | 1 | NOT STARTED |
| project/project.go | 2 | NOT STARTED |
| root/root.go | 2 | NOT STARTED |

---

## Group Classification

**Group A** — Commands that already exist in own subpackages with exported constructors. Standard workflow applies.

**Group B** — Commands with extra run parameters (positional args passed as separate params, or `*cobra.Command` for confirmation prompts). These need args folded into Options and confirmation moved to Prompter on Options.

**Needs Package Extraction** — Ralph, Monitor, Config commands have unexported constructors inline in parent. Step 0.5 required.

## Decision Tree: Extra Run Parameters

```
Does run() take params beyond (ctx, opts)?
    │
    ├─ Positional args (names, volumes, etc.)
    │   → Add field to Options, assign in RunE from args
    │
    ├─ *cobra.Command (for confirmation prompts)
    │   → Add Prompter to Options, use opts.Prompter.Confirm()
    │   → Remove cmd parameter from run function
    │
    └─ No extra params → standard workflow
```

## Key Learnings

### Agent flag tests need Resolution on Factory
Tests with `--agent` flag call `opts.Resolution().ProjectKey` in RunE before the runF dispatch. The Factory must have a Resolution function set, otherwise it panics with nil pointer dereference. Use:
```go
f := &cmdutil.Factory{
    Resolution: func() *config.Resolution {
        return &config.Resolution{ProjectKey: "testproject"}
    },
}
```

### Integration tests with build tags
Serena's `rename_symbol` does NOT reach files excluded by build tags (e.g., `//go:build integration`).
After renaming, manually update integration test files with `replace_all` edits.

### RunE override tests expose incorrect behavior
Old tests that override `RunE` and manually extract flags may have logic that diverges from the actual command's `RunE`.
The runF pattern captures actual command behavior, so test expectations may need fixing.
Example: `container/create` had tests treating args starting with `-` as Command (not Image), but the real RunE always treats `args[0]` as Image.

### Root aliases with runF
Aliases in `aliases.go` that use direct function references (`Command: pkg.NewCmd`) must be changed to wrapper closures (`Command: func(f *cmdutil.Factory) *cobra.Command { return pkg.NewCmdX(f, nil) }`) after adding the `runF` parameter.

### Commands with embedded ContainerOptions
For commands embedding `*copts.ContainerOptions`, the runF capture gives direct access to all flag values via the embedded struct. Tests assert on `gotOpts.ContainerOptions.Agent`, `gotOpts.ContainerOptions.NetMode.NetworkMode()`, etc. — no need for `cmd.Flags().GetString()` extraction.

### RunE override tests with custom arg-handling logic
Old tests that override RunE may implement their own arg-parsing logic that diverges from the real RunE. For example, `container/run` tests had special `strings.HasPrefix(args[0], "-")` logic treating dash-prefixed args as Command instead of Image, but the real RunE always treats `args[0]` as Image. The runF pattern captures actual command behavior, so test expectations must be updated to match real behavior (not the old override's behavior).

### Multiple commands per session
When commands in the same group are straightforward (no package extraction needed), migrating 2 commands in a single session is efficient. This is especially true when finishing a group — the parent registration update happens once at the end.

### Prune commands: bufio confirmation → Prompter
Volume/prune had manual `bufio.NewReader(cmd.InOrStdin())` confirmation. The migration replaces this with `opts.Prompter().Confirm()`, which removes the `*cobra.Command` dependency from the run function and the `bufio`/`strings` imports. Same pattern as image/prune.

### Alias wrapper closures for commands with many Factory deps
Commands like `container/run` that take many Factory deps on Options (IOStreams, Client, Config, Settings, Prompter, SettingsLoader, etc.) work fine with the standard closure wrapper pattern in aliases.go. The alias closure just passes `nil` for runF — no special handling needed regardless of how many Factory deps the command uses.

### Batching an entire command group in one session
When all commands in a group (e.g., network/*) follow the same straightforward pattern (own subpackages, exported constructors, no package extraction needed), migrating all 5 in a single session is efficient. The parent registration update happens naturally as each child is migrated, and the final `go test ./...` validates everything at once.

### Package extraction: iostreams test helper
The project uses `iostreams.NewTestIOStreams()` returning `*TestIOStreams` (which embeds `*IOStreams`). Access the embedded field via `tio.IOStreams` for `Factory.IOStreams`. There is no `iostreams.Test()` four-return function.

### Package extraction: import naming for `run` subpackage
When creating `internal/cmd/ralph/run/`, the Go package name is `run` which imports cleanly as `run.NewCmdRun(f, nil)` in the parent. No alias needed despite the common name.

## Decision Tree: Prune Commands (image/prune, volume/prune, network/prune)

Current pattern uses `cmd.OutOrStdout()` for confirmation. Migration:
1. Add `Prompter prompts.Prompter` to Options
2. Replace `fmt.Fprintf(cmd.OutOrStdout(), ...)` with `opts.Prompter.Confirm(...)`
3. Remove `*cobra.Command` from run signature
4. Assign Prompter from Factory in NewCmd

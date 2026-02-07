# Presentation Layer Refactor

## Branch & Context
**Branch**: `a/image-build-output`
**Nature**: Experimental branch to iron out the global presentation layer approach. `image build` is the testbed command — it exercises both `iostreams` (static output) and `tui` (live progress display), making it ideal for proving out patterns before rolling them across all commands.

## End Goal
Establish a clean, consistent presentation layer architecture across clawker:
1. TUI as a Factory noun (not a callback) — **DONE**
2. 4-scenario output model replacing the old either/or rule — **DONE**
3. Lifecycle hooks for UAT step-through — **DONE**
4. Output interface for abstracting simple output — **NEXT**

## Completed Work

### Part 1: TUI Lifecycle Hook Mechanism [DONE]
- `internal/tui/hooks.go` — `HookResult` + `LifecycleHook` function type
- `internal/tui/hooks_test.go` — 4 tests
- `internal/tui/progress.go` — `OnLifecycle` field, `fireHook()`, `viewFinished()` fix
- Hook wiring in both TTY and plain modes

### Part 2: Fawker UAT Hook Implementation [DONE]
- `cmd/fawker/root.go` — `--step` persistent flag
- `cmd/fawker/main.go` — `fawkerLifecycleHook()` (stdin Enter/q), wired in `PersistentPreRunE`

### Part 3: TUI Noun on Factory [DONE]
- `internal/tui/tui.go` (new) — `TUI` struct: `NewTUI`, `RegisterHooks`, `RunProgress`, `composedHook`
- `internal/tui/tui_test.go` (new) — 8 tests
- `internal/cmdutil/factory.go` — `TUI *tui.TUI` replaces `TUILifecycleHook`
- `internal/cmd/factory/default.go` — `tui.NewTUI(ios)` in `New()`
- `cmd/fawker/factory.go` — Same wiring
- `cmd/fawker/main.go` — `f.TUI.RegisterHooks(...)` replaces direct field assignment
- `internal/cmd/image/build/build.go` — `opts.TUI.RunProgress()`, removed `logger.SetInteractiveMode`
- All test files updated with `TUI: tui.NewTUI(tio.IOStreams)`

### Part 4: Design Rules & Documentation [DONE]
- `.claude/rules/code-style.md` — 4-scenario presentation model, zerolog file-only rule
- `CLAUDE.md` — Key Concepts (`tui.TUI`), Design Decision #10 (4-scenario), #13 (zerolog file-only)
- `internal/tui/CLAUDE.md` — TUI struct docs, pointer-sharing pattern
- `internal/cmdutil/CLAUDE.md` — TUI field semantics
- `internal/cmd/factory/CLAUDE.md` — `tui.NewTUI(ios)` helper

**All 3399 unit tests pass. Both clawker and fawker binaries compile.**

## Remaining TODO

### [x] Step 0: Investigate --step flag bug [RESOLVED]
Was a stale binary. After `go build`, `--step` works correctly:
- Hook fires at `before_complete` only when `--step` is passed
- `q` quits with exit 1, Enter continues, no `--step` = no hook
- `pauseForReview` (always-on unless `--no-pause`) is a separate mechanism — not a bug


### [x] Step 1: Manual UAT verification [PASSED]
All 7 scenarios tested in plain mode (simple, cached, multi-stage, error, large-log, many-steps, internal-only).
`--step` hook mechanism verified with piped stdin: `q` aborts, Enter continues, no `--step` = no hook.


### [ ] Step 2: Commit
All changes are unstaged on branch `a/image-build-output`. UAT passed — ready to commit.

### [ ] Step 3: `internal/output` package (next feature)
Experimental design for abstracting simple output:
- **Output interface** — `HandleError(err)`, `PrintWarning(format, args...)`, `PrintSuccess(format, args...)`, `PrintNextSteps(steps...)`, etc.
- **Replaces `cmdutil.Print*` free functions** — currently scattered across `cmdutil/output.go` as `func(ios, ...)` signatures
- **Testable output surface** — commands inject `Output` interface, tests use a capturing implementation
- **Relationship to TUI**: `Output` handles the "non-interactive / static" scenario (scenario 1 in the 4-scenario model). `TUI` handles scenarios 3-4. `prompter` handles scenario 2.
- **Design questions**: Should `Output` wrap `IOStreams` or live alongside it? Should it be a Factory field or constructed per-command? Consider how it interacts with `HandleError`'s duck-typing (`FormatUserError()` interface).

## Key Design Decisions (for reference)
- TUI is a Factory noun (`*tui.TUI`) — pointer sharing fixes `--step` TTY bug
- 4-scenario output model: static | static-interactive | live-display | live-interactive
- zerolog for file logging only — user output via `fmt.Fprintf` to IOStreams
- Hooks fire AFTER BubbleTea exits (no stdin conflict), BEFORE summary render
- `viewFinished()` must render ALL visual elements (header + steps + viewport) for BubbleTea final frame persistence

## Lessons Learned
- BubbleTea's renderer overwrites with `View()` return — `""` erases everything
- Viewport vanishing was the most visible bug; always include all elements in final frame
- Capturing callbacks eagerly (before flag parsing) causes silent bugs — pointer sharing is the fix
- `cmdutil → tui` import is safe (no cycle)

**IMPERATIVE: Always check with the user before proceeding with the next todo item. Present what you plan to do and get approval. If all work is done, ask the user if they want to delete this memory.**

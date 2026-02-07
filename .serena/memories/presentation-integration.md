# Presentation Layer Integration

## Branch and Status
**Branch**: `a/presentation-integration`
**Status**: Phases 1-7 complete. All 9 implementation steps done. Ready for PR.
**Tests**: 3188 unit tests pass. Both clawker and fawker binaries compile.

---

## Background: How We Got Here

### The Original Either/Or Model (Failed Experiment)
The initial plan was for `tui` to be used only by the `monitor` command family. `iostreams` would be the foundation for 99% of commands. TUI would re-export/forward all of iostreams (the "shim" -- `tui/iostreams.go`, 355 lines) so TUI-using commands would not need to import both. It was **either/or**: you import `iostreams` OR you import `tui` (which is `iostreams++`).

**This model broke down** when the 4-scenario output model emerged. Most commands need TUI for live-display scenarios (like `image build` progress). TUI is no longer the alternative to iostreams -- it is **additive**. Commands import both.

### The 4-Scenario Output Model
Commands fall into one of four output scenarios:

| Scenario | Packages | Example |
|----------|----------|---------|
| Non-interactive / static | `iostreams` + `fmt` | `container ls`, `image ls` |
| Static-interactive | `iostreams` + `prompter` | `init`, `image prune` |
| Live-display | `iostreams` + `tui` | `image build` progress |
| Live-interactive | `iostreams` + `tui` | `monitor up` |

A single command may support multiple scenarios (e.g., `image build` does live-display in TTY, plain text when piped).

### The Proving Ground
`internal/cmd/image/build/` is the test area for the new presentation layer. The TUI progress display, golden file testing, fawker demo CLI, lifecycle hooks -- all proven here first. **Other commands have not been adapted yet.**

---

## Completed Work (Phases 1-5)

### Phase 1: TUI Migration
Replaced raw ANSI cursor manipulation in `iostreams/buildprogress.go` with BubbleTea model in `internal/tui/`.

### Phase 2: Architecture Cleanup
Domain helpers to `pkg/whail/progress.go`. Generic progress to `internal/tui/progress.go`. Build command as composition root. whailtest fakes + dockertest wiring. Pipeline tests. Docs.

### Phase 3: Golden Files + Demo CLI
RecordedBuildEvent + EventRecorder + FakeTimedBuildKitBuilder. 7 JSON testdata files. TUI + command golden tests. Fawker demo CLI (`cmd/fawker/`, `make fawker`).

### Phase 4: TUI Factory Noun + Lifecycle Hooks
`tui.TUI` as Factory noun with `RegisterHooks`/`RunProgress`/`composedHook`. Fawker `--step` flag.

### Phase 5: Term Package Refactor
Split `internal/term` into three packages:
- `internal/term` -- **leaf** (stdlib + x/term only). Sole `golang.org/x/term` gateway.
- `internal/signals` -- **leaf** (stdlib only). `SetupSignalContext` + `ResizeHandler`.
- `internal/docker/pty.go` -- `PTYHandler` moved here (Docker session hijacking).

---

## Phase 6: Design Evaluation -- Output Interface (COMPLETED)

### The Question
Should we create an `Output` interface where commands call `opts.Output.Success()`, `opts.Output.ProgressStart()`, `opts.Output.Prompt()` and the implementation handles mode-specific rendering?

### The Verdict: NO
The Output Interface is **not a solid design choice**. Reasons:

1. **The 4 scenarios are not polymorphic** -- Static text, live TUI, JSON serialization, and interactive prompts are fundamentally different interaction patterns. A single interface either becomes a god-interface or forces awkward abstractions.
2. **BubbleTea progress is already solved** -- `TUI.RunProgress()` with callbacks is a better abstraction than an interface method because it embraces concurrent rendering complexity.
3. **Prompts are input, not output** -- Bundling them conflates concerns. Prompter is already cleanly separated.
4. **Tables do not polymorphize** -- TTY table, JSON array, and quiet ID list have different data shapes.
5. **Testing gets worse** -- Mock-based testing is weaker than buffer-capture testing. You would need both, doubling test overhead.
6. **Square peg, round hole** -- BubbleTea event loop and fmt.Fprintf are fundamentally different execution models.

### The Chosen Approach: gh-Style Output + Package Consolidation

**Output pattern**: Follow GitHub CLI (`gh`) conventions. Commands use fmt.Fprintf with ios.ColorScheme() directly:
```go
cs := ios.ColorScheme()
fmt.Fprintf(ios.ErrOut, "%s Pull request #%d cannot be closed\n", cs.FailureIcon(), pr.Number)
return cmdutil.SilentError
```

No PrintX() helper methods. No ios.PrintSuccess(). Traditional fmt.Fprintf with color scheme is clear, testable, and explicit.

**Package consolidation**: Instead of one Output interface, separate concerns into focused packages.

---

## Phase 7: Package Refactor (COMPLETED)

All steps implemented across 9 commits on `a/presentation-integration`:

### Completed Steps
1. **feat(text): extract text utilities from iostreams** — `internal/text` leaf package (stdlib only)
2. **feat(tableprinter): extract table printer from iostreams** — `internal/tableprinter` with TTY-aware rendering
3. **feat(cmdutil): add FlagError and SilentError error types** — typed error vocabulary for centralized rendering
4. **feat(clawker): centralized error rendering in Main()** — `printError()` dispatches on FlagError, userFormattedError, default; `SilenceErrors: true`
5. **refactor(tui): delete iostreams.go shim** — 355 lines removed; TUI uses qualified `iostreams.Style` and `text.Function` imports
6. **refactor(iostreams): delete dead code** — Removed message.go, render.go, time.go, tokens.go; trimmed layout.go to 4 functions (Stack, Row, FlexRow, CenterInRect)
7. **refactor(cmdutil): deprecate output helpers** — HandleError, PrintError, PrintWarning, PrintNextSteps marked deprecated
8. **refactor(image/build): adapt to gh-style output** — Reference implementation using fmt.Fprintf + ColorScheme, errors bubble to Main()
9. **docs: update CLAUDE.md files and memories** — All documentation updated

### Lines Removed
- tui/iostreams.go shim: 355 lines
- Dead code (message, render, time, tokens, layout trimming): ~1870 lines
- build.go cmdutil calls: ~11 lines
- **Total: ~2236 lines deleted**

### Package Dependency Model (After Refactor)
```
command -> iostreams     (always: streams, TTY, colors, styles, spinner)
         + tui           (when needed: BubbleTea, progress display)
         + prompter      (when needed: interactive prompts)
         + tableprinter  (when needed: table output)
         + text          (when needed: string manipulation)
```

Each is a direct import. No shims. No re-exports. Commands import exactly what they use.

---

## Error Handling Pattern (gh-style)

Commands never print their own errors. They return typed errors that bubble up to Main(), which is the single centralized error renderer.

### Error Types (in cmdutil)
- FlagError / FlagErrorf / FlagErrorWrap -- bad flags/args, triggers usage display
- ExitError -- container exit code propagation (allows deferred cleanup before os.Exit)
- SilentError -- error already displayed by command, root just exits non-zero
- ErrAborted -- user cancelled interactive operation

### Centralized Error Rendering (in internal/clawker/cmd.go)

```go
func Main() int {
    rootCmd.SilenceErrors = true
    cmd, err := rootCmd.ExecuteC()
    if err != nil {
        if !errors.Is(err, cmdutil.SilentError) {
            printError(f.IOStreams.ErrOut, err, cmd)
        }
        // ExitError -> return exitErr.Code
        // Default -> return 1
    }
}

func printError(out io.Writer, err error, cmd *cobra.Command) {
    // FlagError -> error + usage string
    // userFormattedError -> rich Docker error formatting
    // Default -> "Error: <message>"
    // Always: "Run '<cmd> --help' for more information"
}
```

### What Commands Do
- **Errors**: Return typed errors (`return fmt.Errorf(...)`, `return cmdutil.FlagErrorf(...)`)
- **Warnings**: Inline `fmt.Fprintf(ios.ErrOut, "%s ...\n", cs.WarningIcon(), msg)` -- not errors, not bubbled
- **Next steps**: Inline numbered guidance with `fmt.Fprintf(ios.ErrOut, ...)`
- **Never** call deprecated cmdutil.PrintError/PrintWarning/HandleError in new code

---

## Key Design Decisions (Current)

| Decision | Rationale |
|----------|-----------|
| **No Output Interface** | 4 scenarios are not polymorphic; god-interface or leaky abstraction |
| **gh-style fprintf** | fmt.Fprintf with cs.Icon() -- explicit, testable, proven |
| **TUI is additive, not alternative** | Either/or model failed; most commands need both iostreams + tui |
| **Shim deleted** | 355-line lipgloss laundromat from dead either/or model removed |
| **Centralized error rendering** | Commands return typed errors; Main() renders them once |
| **TUI is a Factory noun** | *tui.TUI pointer sharing fixes eager capture bugs |
| **zerolog for file logging only** | User output via fmt.Fprintf to IOStreams |
| **Hooks fire AFTER BubbleTea exits** | Before summary render, no stdin conflict |
| **internal/term is sole x/term gateway** | Enforced in code-style.md |
| **Channel closure = done signal** | No Done/BuildErr fields on progress events |
| **image build is the reference impl** | Other commands adapted command-by-command in follow-up PRs |
| **Prompter is already standalone** | internal/prompter/ -- no extraction needed |
| **Colors/styles stay in iostreams** | They depend on TTY detection; iostreams owns terminal capabilities |

---

## IOStreams Current State (After Refactor)

| File | Purpose |
|------|---------|
| iostreams.go | Core I/O, TTY, terminal size, pager, alt screen |
| colorscheme.go | Color scheme wraps TTY-aware rendering |
| styles.go | Color palette + lipgloss styles (terminal capability) |
| spinner.go | Goroutine spinner writes to ios.ErrOut |
| progress.go | Progress bar writes to ios.ErrOut |
| pager.go | Pager process management |
| layout.go | Stack, Row, FlexRow, CenterInRect (lipgloss-based) |

**Extracted**: text.go → `internal/text`, table.go → `internal/tableprinter`
**Deleted**: message.go, render.go, time.go, tokens.go

---

## TTY Visual Bugs (Noted, Not Blocking)
- Invisible lines printed in TTY mode
- Viewport lower-right border collapses in/out
- Summary/statusline sometimes duplicates
- Root causes likely: View() height instability, width floor, ANSI width miscounting

---

## Next Steps (Follow-up PRs)
- Adapt remaining commands one-by-one away from deprecated cmdutil helpers
- Delete deprecated cmdutil helpers once all callers are migrated
- Delete `userFormattedError` from cmdutil/output.go (now only needed in clawker/cmd.go)

---

## Testing Quick Reference
```bash
make test                                           # 3188 unit tests
make fawker && ./bin/fawker image build             # Visual UAT
GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeed -v
GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v
GOLDEN_UPDATE=1 go test ./internal/cmd/image/build/... -run TestBuildProgress_Golden -v
```

## IMPORTANT
- image build is the only command adapted to gh-style output -- other commands still use deprecated cmdutil helpers.
- The tui/iostreams.go shim has been DELETED -- TUI uses `iostreams.Style` and `text.Function` directly.
- No PrintX() helpers -- use gh-style fmt.Fprintf(ios.ErrOut, "...", cs.Icon()) pattern.
- Error handling is centralized in Main() -- commands return typed errors, never print them.
- cmdutil output helpers (HandleError, PrintError, PrintWarning, PrintNextSteps) are deprecated but not deleted -- too many callers for one PR.
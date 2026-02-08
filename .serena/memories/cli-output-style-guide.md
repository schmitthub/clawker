# CLI Output Style Guide

Authoritative reference for all CLI command output patterns. Follow these conventions when writing or modifying commands.

## 1. Core Principle

Follow GitHub CLI (`gh`) conventions: `fmt.Fprintf` with `ios.ColorScheme()` directly. No output wrappers, no abstraction layers — just formatted writes to the correct stream.

```go
cs := ios.ColorScheme()
fmt.Fprintf(ios.ErrOut, "%s Built image %s\n", cs.SuccessIcon(), cs.Bold(imageTag))
```

## 2. Stream Conventions

### Base Rules

| Stream | Field | Purpose |
|--------|-------|---------|
| **stdout** | `ios.Out` | Data output and final results (scriptable, pipeable) |
| **stderr** | `ios.ErrOut` | Status messages, warnings, errors, diagnostics |

Per output type:
- **Data** (tables, IDs, JSON, command results) → `ios.Out` (stdout) — always
- **Status** ("Created container X", "Removed 3 volumes") → `ios.ErrOut` (stderr) — visible to user, not captured by pipes
- **Errors** → `ios.ErrOut` (stderr) — via `printError()` in Main(), or pre-printed before `SilentError`
- **Warnings** → `ios.ErrOut` (stderr) — always visible regardless of piping
- **Next steps** → `ios.ErrOut` (stderr) — guidance/instructional, not machine-consumable
- **Prompts** → `ios.ErrOut` (stderr) — visible even when stdout is piped

These base rules apply to **static** (non-TUI) output. Live/interactive scenarios have their own rendering strategy — see per-scenario stream rules in Section 3.

### Machine-Readable Output

- `--format` flag controls output format, added per-command when needed
- Options vary by command: `table` (default), `json`, `TEMPLATE`
- Not every command needs `--format` — only add when there's a clear scripting use case
- When `--format` is active, only the formatted data goes to stdout; status/progress still goes to stderr
- Default output (no `--format`) is the best human-readable scenario for that command

## 3. The 4 Output Scenarios

### Decision Table

| Scenario | User Input? | Live Rendering? | Imports | Wiring |
|----------|:-----------:|:---------------:|---------|--------|
| Static | No | No | `iostreams` | `f.IOStreams` |
| Static-interactive | Mid-flow y/n | No | `iostreams` + `prompter` | `f.IOStreams` + `f.Prompter()` |
| Live-display | No | Yes (continuous) | `iostreams` + `tui` | `f.IOStreams` + `f.TUI` |
| Live-interactive | Full keyboard | Yes (stateful) | `iostreams` + `tui` | `f.IOStreams` + `f.TUI` |

### Scenario 1: Static (Non-Interactive)

Print and done. Data, status, results.

**Stream strategy:**
- Data output (tables, IDs, results) → `ios.Out` (stdout)
- Status messages, success confirmations → `ios.ErrOut` (stderr)
- Warnings, next steps → `ios.ErrOut` (stderr)
- Errors → returned to Main() → `ios.ErrOut` (stderr)

```go
func runList(opts *ListOptions) error {
    ios := opts.IOStreams
    cs := ios.ColorScheme()

    // Data output to stdout (pipeable)
    tp := tableprinter.New(ios, "NAME", "STATUS", "IMAGE")
    for _, c := range containers {
        tp.AddRow(c.Name, c.Status, c.Image)
    }
    return tp.Render()
}

func runRemove(opts *RemoveOptions) error {
    ios := opts.IOStreams
    cs := ios.ColorScheme()

    // ... perform removal ...

    // Status to stderr
    fmt.Fprintf(ios.ErrOut, "%s Removed container %s\n", cs.SuccessIcon(), name)
    return nil
}
```

### Scenario 2: Static-Interactive

Static output with y/n prompts mid-flow.

**Stream strategy:**
- Same as Static — data → stdout, status → stderr
- Prompts render to stderr (visible when stdout is piped)
- Confirmation results influence what data goes to stdout

```go
func runPrune(opts *PruneOptions) error {
    ios := opts.IOStreams
    prompter := opts.Prompter()

    ok, err := prompter.Confirm("Remove all stopped containers?", false)
    if err != nil { return err }
    if !ok { return nil }

    // ... perform action ...
    fmt.Fprintf(ios.ErrOut, "%s Removed %d containers\n", cs.SuccessIcon(), count)
    return nil
}
```

### Scenario 3: Live-Display

No user input, but continuous rendering with layout management.

**Stream strategy (TTY mode):**
- BubbleTea manages the terminal — live progress renders via the TUI framework
- After TUI exits, final summary renders as status (stderr)
- If the command produces capturable data (e.g., image tag), write to stdout separately

**Stream strategy (plain/non-TTY fallback):**
- Progress lines (`[run]`/`[ok]`/`[fail]`) → `ios.ErrOut` (stderr) — ephemeral status
- Final summary → `ios.ErrOut` (stderr) — status
- Capturable data → `ios.Out` (stdout)

```go
func runBuild(opts *BuildOptions) error {
    ch := make(chan tui.ProgressStep, 64)
    // ... set up OnProgress callback to send to ch ...
    go func() {
        buildErr = builder.Build(ctx, tag, buildOpts)
        close(ch) // channel closure = done signal
    }()

    result := opts.TUI.RunProgress(opts.Progress, tui.ProgressDisplayConfig{
        Title: "Building", Subtitle: tag,
        MaxVisible: 5, LogLines: 3,
        IsInternal: whail.IsInternalStep,
        CleanName:  whail.CleanStepName,
        ParseGroup: whail.ParseBuildStage,
    }, ch)
    return result.Err
}
```

### Scenario 4: Live-Interactive

Full keyboard/mouse input, stateful navigation. Uses `tui.RunProgram`.

**Stream strategy:**
- BubbleTea owns the full terminal (alternate screen)
- All rendering managed by the TUI framework
- Not pipeable — interactive commands require a TTY
- On exit, any final results → `ios.Out` (stdout)

```go
model := newMonitorModel(ios)
finalModel, err := tui.RunProgram(ios, model, tui.WithAltScreen(true))
```

## 4. ColorScheme API Reference

Access: `cs := ios.ColorScheme()`

### Icons

| Method | TTY Output | Non-TTY |
|--------|-----------|---------|
| `cs.SuccessIcon()` | `✓` (green) | `[ok]` |
| `cs.WarningIcon()` | `!` (yellow) | `[warn]` |
| `cs.FailureIcon()` | `✗` (red) | `[fail]` |
| `cs.InfoIcon()` | `ℹ` (blue) | `[info]` |

Each icon has a `*WithColor(text)` variant that applies the icon's color to the given text.

### Semantic Colors (preferred)

Each has a `*f(format, args...)` variant returning a formatted colored string.

| Method | Usage | Hex |
|--------|-------|-----|
| `cs.Primary(s)` | Brand, titles | `#7D56F4` |
| `cs.Secondary(s)` | Supporting text | `#6C6C6C` |
| `cs.Accent(s)` | Emphasis | `#FF6B6B` |
| `cs.Success(s)` | Positive outcomes | `#04B575` |
| `cs.Warning(s)` | Caution | `#FFCC00` |
| `cs.Error(s)` | Errors | `#FF5F87` |
| `cs.Info(s)` | Informational | `#87CEEB` |
| `cs.Muted(s)` | Dimmed/secondary | `#626262` |
| `cs.Highlight(s)` | Attention | `#AD58B4` |
| `cs.Disabled(s)` | Inactive | `#4A4A4A` |

### Concrete Colors

Use semantic colors when possible. Concrete colors for specific design needs:

`cs.Red/Redf`, `cs.Yellow/Yellowf`, `cs.Green/Greenf`, `cs.Blue/Bluef`, `cs.Cyan/Cyanf`, `cs.Magenta/Magentaf`, `cs.BrandOrange/BrandOrangef`

### Text Decorations

`cs.Bold/Boldf`, `cs.Italic/Italicf`, `cs.Underline/Underlinef`, `cs.Dim/Dimf`

### Query Methods

- `cs.Enabled() bool` — whether colors are active
- `cs.Theme() string` — `"dark"`, `"light"`, or `"none"`

## 5. Output Helpers — All Deprecated

`cmdutil/output.go` has **no active helpers**. All output functions are deprecated — commands use `fmt.Fprintf` with `ios.ColorScheme()` directly (gh-style). See section 10 for migration recipes.

## 6. Error Handling

### Error Flow

Commands **return** errors — they never print them directly. Centralized rendering happens in `Main()` → `printError()`.

```
Command RunE → return error → Main() → printError(ios.ErrOut, err, cmd)
```

### Error Types

| Type | Usage | Main() Behavior |
|------|-------|-----------------|
| `fmt.Errorf(...)` | Default error | Prints `"Error: <message>"` + help hint |
| `cmdutil.FlagErrorf(...)` | Bad flag/arg | Prints error + command usage + help hint |
| `cmdutil.FlagErrorWrap(err)` | Wrap existing as flag error | Same as FlagErrorf |
| `cmdutil.SilentError` | Already displayed | Exits non-zero silently |
| `&cmdutil.ExitError{Code: N}` | Container exit code propagation | Exits with code N (runs defers first) |
| `userFormattedError` interface | Rich error (e.g., Docker) | Calls `FormatUserError()` |

### Canonical Error Patterns

```go
// Default error — most common
return fmt.Errorf("container %q not found", name)

// Flag validation error — triggers usage display
return cmdutil.FlagErrorf("--timeout must be positive, got %d", timeout)

// Already displayed the error, just exit non-zero
fmt.Fprintf(ios.ErrOut, "%s\n", richErrorMessage)
return cmdutil.SilentError

// Container exit code propagation (lets defers run, unlike os.Exit)
return &cmdutil.ExitError{Code: exitCode}
```

### Anti-Pattern: Direct Error Printing

```go
// BAD — prints error AND returns it (double printing)
fmt.Fprintf(ios.ErrOut, "Error: %s\n", err)
return err

// BAD — cmdutil.HandleError + return SilentError (unnecessary indirection)
cmdutil.HandleError(ios, err)
return cmdutil.SilentError

// GOOD — just return the error
return fmt.Errorf("failed to start container: %w", err)
```

## 7. TablePrinter

`internal/tableprinter` — TTY-aware tabular output to `ios.Out`.

### API

```go
tp := tableprinter.New(ios, "NAME", "STATUS", "IMAGE")
tp.AddRow("web", "running", "nginx:latest")
tp.AddRow("db", "stopped", "postgres:16")
if tp.Len() == 0 {
    fmt.Fprintln(ios.ErrOut, "No containers found")
    return nil
}
return tp.Render()
```

### Rendering Modes

- **TTY + color**: Styled headers (bold + `ColorPrimary`), `─` divider, width-aware columns
- **Non-TTY / piped**: Plain `text/tabwriter` with 2-space column gaps, no styling

### Anti-Pattern: Raw tabwriter

```go
// BAD — loses TTY styling, inconsistent with rest of CLI
w := tabwriter.NewWriter(ios.Out, 0, 0, 2, ' ', 0)
fmt.Fprintln(w, "NAME\tSTATUS\tIMAGE")

// GOOD — use TablePrinter
tp := tableprinter.New(ios, "NAME", "STATUS", "IMAGE")
```

**Remaining raw tabwriter usages** (8 files, migration needed):
- `container/list`, `container/top`, `container/stats` (×2)
- `image/list`, `volume/list`, `network/list`, `worktree/list`

## 8. Prompter

`internal/prompter` — Interactive prompts via `f.Prompter()`. Access through Factory noun.

### API

```go
prompter := f.Prompter()

// String prompt with validation
name, err := prompter.String(prompter.PromptConfig{
    Message: "Project name", Default: "my-project", Required: true,
    Validator: func(s string) error { /* ... */ },
})

// Yes/No confirmation
ok, err := prompter.Confirm("Delete all volumes?", false)

// Selection from list
idx, err := prompter.Select("Base image", []prompter.SelectOption{
    {Label: "Debian Bookworm", Description: "Recommended"},
    {Label: "Alpine 3.22", Description: "Smaller"},
}, 0)
```

### Non-Interactive Fallback

In CI/non-TTY (checked via `ios.IsInteractive()`):
- `String` → returns `Default` (or error if `Required` with no default)
- `Confirm` → returns `defaultYes`
- `Select` → returns `defaultIdx`

### Deprecated: `PromptForConfirmation`

```go
// BAD — writes to os.Stderr directly, not testable
prompter.PromptForConfirmation(cmd.InOrStdin(), "Continue?")

// GOOD — uses IOStreams, testable, handles non-interactive
ok, err := f.Prompter().Confirm("Continue?", false)
```

## 9. TUI / Progress Display (Tree Display)

### When to Use

Use `RunProgress` (the "tree display") when a command has **streaming, multi-step operations** where:

1. **Steps arrive asynchronously** — work happens in a goroutine, progress streams via channel
2. **Multiple named steps with status transitions** — not a single spinner ("Loading...") but N concurrent steps transitioning through Pending -> Running -> Complete/Cached/Error
3. **Steps may be grouped** — e.g., Docker build stages, deploy targets, test suites
4. **Dual rendering matters** — TTY users see a live tree; CI/piped output gets sequential `[run]`/`[ok]`/`[fail]` lines

**Do NOT use for**:
- Single-operation wait (use `ios.RunWithSpinner` instead)
- Measurable byte progress (use `ios.NewProgressBar` instead)
- Static output with no streaming (use `fmt.Fprintf` directly)

### Decision Flow

```
Is work streaming + multi-step?
├─ YES → RunProgress (tree display)
│        ├─ Steps have groups? → set ParseGroup callback
│        ├─ Some steps should be hidden? → set IsInternal callback
│        └─ Custom duration format? → set FormatDuration callback
├─ Single operation, unknown duration → ios.RunWithSpinner
├─ Single operation, known total → ios.NewProgressBar
└─ One-shot result → fmt.Fprintf
```

### Visual Output

**TTY mode** — BubbleTea interactive tree with live updates:

```
━━ Building myproject (myproject:latest)
  ✓ builder ── 4 steps              ← collapsed complete stage
  ◐ runtime                          ← active stage (expanded)
    ├─ ✓ FROM alpine:3.19  0.2s
    ├─ ◐ RUN apk add git            ← running step with spinner
    │  ⎿ fetch https://...           ← inline log line
    └─   COPY --from=builder ...     ← pending step
```

Key elements:
- Stages are parent nodes, steps are children with tree connectors (`├─`/`└─`)
- Active stages expand; complete/pending/error stages collapse to `✓ name ── N steps`
- Running steps show a pulsing spinner; inline `⎿` log lines stream beneath
- Per-stage child window (`MaxVisible`) centers on running step with overflow indicators
- High-water mark frame padding prevents BubbleTea cursor drift

**Plain mode** — Sequential lines for CI/piped output:

```
━━ Building test-project (test-project:latest)
[run]  [stage-0 1/3] FROM node:20-slim
[ok]   [stage-0 1/3] FROM node:20-slim (0.0s)
[run]  [stage-0 2/3] RUN apt-get update
[ok]   [stage-0 2/3] RUN apt-get update (0.0s)
[run]  [stage-0 3/3] COPY . /app
[ok]   [stage-0 3/3] COPY . /app (0.0s)
[ok] Built test-project:latest 0.0s
```

**Error output** (both modes):

```
[fail] [stage-0 3/3] RUN npm install: process did not complete successfully
[error] Building test-project failed (0.0s)
```

**Cached summary**: `[ok] Built test-project:latest (4/5 cached) 0.0s`

### TUI Factory Noun

```go
// Access via Factory — set eagerly, hooks registered later
tui := f.TUI  // *tui.TUI

// Register hooks in PersistentPreRunE (after flag parsing)
tui.RegisterHooks(myHook)

// Run progress display
result := tui.RunProgress(mode, cfg, ch)
```

### Complete Wiring Pattern

```go
func runBuild(opts *BuildOptions) error {
    ch := make(chan tui.ProgressStep, 64)

    // Wire domain events → generic ProgressStep channel
    buildOpts.OnProgress = func(event whail.BuildProgressEvent) {
        ch <- tui.ProgressStep{
            ID: event.StepID, Name: event.StepName,
            Status:  progressStatus(event.Status), // explicit switch mapping
            Cached:  event.Cached,
            Error:   event.Error,
            LogLine: event.LogLine,
        }
    }

    // Producer goroutine — close(ch) is the done signal
    var buildErr error
    go func() {
        buildErr = builder.Build(ctx, tag, buildOpts)
        close(ch) // MUST close — signals completion to RunProgress
    }()

    // Consumer — blocks until channel closes
    result := opts.TUI.RunProgress(opts.Progress, tui.ProgressDisplayConfig{
        Title:          "Building " + project,
        Subtitle:       tag,
        CompletionVerb: "Built",       // success summary: "Built <tag>"
        MaxVisible:     5,
        LogLines:       3,
        IsInternal:     whail.IsInternalStep,
        CleanName:      whail.CleanStepName,
        ParseGroup:     whail.ParseBuildStage,
        FormatDuration: whail.FormatBuildDuration,
    }, ch)

    if result.Err != nil { return result.Err }
    return buildErr
}
```

### ProgressStep (channel event)

```go
type ProgressStep struct {
    ID      string             // unique step identifier
    Name    string             // display name (fed to CleanName, ParseGroup, IsInternal)
    Status  ProgressStepStatus // StepPending|StepRunning|StepComplete|StepCached|StepError
    LogLine string             // latest log output (streaming, shown inline under running step)
    Cached  bool               // step was cached (shown as "(cached)" suffix)
    Error   string             // error message if StepError
}
```

### ProgressDisplayConfig

```go
type ProgressDisplayConfig struct {
    Title          string // Header line: "━━ <Title> (<Subtitle>)"
    Subtitle       string // Appears in header and completion summary

    CompletionVerb string // Success summary verb. Default: "Completed"
                         // e.g., "Built" → "Built myimage:latest"
                         // e.g., "Deployed" → "Deployed staging"

    MaxVisible int // per-stage child window size (default: 5)
    LogLines   int // per-step log buffer capacity (default: 3)

    // Callbacks — all optional. nil = passthrough / no-op.
    IsInternal     func(string) bool         // hide internal steps (nil = show all)
    CleanName      func(string) string       // clean step names (nil = passthrough)
    ParseGroup     func(string) string       // extract stage name (nil = flat list)
    FormatDuration func(time.Duration) string // format elapsed (nil = "1.2s")

    OnLifecycle LifecycleHook // called at key moments (nil = no-op)
}
```

**Title/Subtitle**: Title should be a gerund ("Building", "Deploying", "Testing") — it's used in the failure summary as `"<Title> failed"`. Subtitle is the artifact name.

**CompletionVerb**: The verb for the success summary line. Must be past tense of the action. Defaults to `"Completed"` if unset — set it explicitly for better UX (e.g., `"Built"`, `"Deployed"`, `"Pulled"`).

**Callbacks**: These are how domain logic flows in without the tui package knowing about the domain. The build command provides `whail.IsInternalStep`, `whail.CleanStepName`, etc. A deploy command would provide its own.

### Mode Selection

```go
func RunProgress(ios, mode string, cfg, ch) ProgressResult
```

| Mode | Behavior |
|------|----------|
| `"auto"` | TTY if `ios.IsStderrTTY()`, plain otherwise. **Use this.** |
| `"tty"` | Force TTY mode (BubbleTea) |
| `"plain"` | Force plain mode (sequential lines) |

Mode is typically a `--progress` flag value on the command (`"auto"`, `"plain"`, `"tty"`).

### Lifecycle Hooks

```go
type LifecycleHook func(component, event string) HookResult

type HookResult struct {
    Continue bool   // false = quit execution
    Message  string // reason for quitting
    Err      error  // hook's own failure
}
```

Hooks fire AFTER BubbleTea exits, BEFORE summary renders. Multiple hooks compose in registration order; first abort or error short-circuits.

Hook events: `("progress", "before_complete")` — fired after all steps complete, before summary.

### IOStreams Spinner (Simpler Alternative)

For short single operations not needing step-by-step tracking:

```go
ios.StartSpinner("Loading...")
defer ios.StopSpinner()
// ... work ...

// Or auto start/stop:
err := ios.RunWithSpinner("Loading...", func() error {
    return doWork()
})
```

### IOStreams Progress Bar

For operations with measurable progress:

```go
pb := ios.NewProgressBar(totalBytes, "Downloading")
pb.Set(bytesRead)
pb.Increment()
pb.Finish()
```

### Testing Tree Display

**Golden file tests** (plain mode — deterministic):
```bash
go test ./internal/tui/... -run TestProgressPlain_Golden -v
GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v  # regenerate
```

**Unit tests** (model-level, no BubbleTea program):
```go
func newTestProgressModel(t *testing.T) (progressModel, *iostreams.TestIOStreams) {
    tio := iostreams.NewTestIOStreams()
    ch := make(chan ProgressStep, 10)
    cfg := ProgressDisplayConfig{
        Title: "Building myproject", Subtitle: "myproject:latest",
        CompletionVerb: "Built",
        // ... callbacks ...
    }
    m := newProgressModel(tio.IOStreams, cfg, ch)
    return m, tio
}
```

**Integration tests** (full pipeline — plays recorded events through RunProgress):
```bash
go test ./internal/cmd/image/build/... -run TestBuildProgress -v
```

**Visual verification** (fawker demo CLI):
```bash
make fawker && ./bin/fawker image build              # default multi-stage
./bin/fawker image build --scenario error             # error scenario
./bin/fawker image build --progress plain             # plain mode
```

## 10. Deprecated Methods & Migration Guide

### Summary

| Deprecated Function | Count | Files | Replacement |
|---------------------|:-----:|:-----:|-------------|
| `cmdutil.HandleError` | 69 | 36 | Return error to Main() |
| `cmdutil.PrintError` | 31 | 12 | Return `fmt.Errorf(...)` or `fmt.Fprintf` + `SilentError` |
| `cmdutil.PrintWarning` | 4 | 4 | `fmt.Fprintf(ios.ErrOut, "%s ...\n", cs.WarningIcon(), ...)` |
| `cmdutil.PrintNextSteps` | 23 | 12 | Inline `fmt.Fprintf(ios.ErrOut, ...)` numbered steps |
| `cmdutil.PrintStatus` | 0 | 0 | `if !quiet { fmt.Fprintf(ios.ErrOut, format+"\n", args...) }` |
| `cmdutil.OutputJSON` | 0 | 0 | Inline `json.NewEncoder(ios.Out)` with `SetIndent` |
| `cmdutil.PrintHelpHint` | 0 | 0 | `fmt.Fprintf(ios.ErrOut, "\nRun '%s --help'...\n", cmdPath)` |
| Raw `tabwriter.NewWriter` | 8 | 8 | `tableprinter.New(ios, headers...)` |
| `prompter.PromptForConfirmation` | — | — | `f.Prompter().Confirm(msg, false)` |
| **Total deprecated** | **135** | **~40** | |

### Migration Recipe: `cmdutil.HandleError`

This is the largest migration. The pattern is:
```go
// BEFORE (deprecated)
result, err := client.DoSomething(ctx)
if err != nil {
    cmdutil.HandleError(ios, err)
    return cmdutil.SilentError
}

// AFTER
result, err := client.DoSomething(ctx)
if err != nil {
    return fmt.Errorf("failed to do something: %w", err)
}
```

For cases where `HandleError` is used after a partial success (some items succeeded, some failed), use `SilentError` with explicit formatting:
```go
// AFTER (partial failure)
if len(errors) > 0 {
    for _, e := range errors {
        fmt.Fprintf(ios.ErrOut, "%s %s: %s\n", cs.FailureIcon(), e.Name, e.Err)
    }
    return cmdutil.SilentError
}
```

**Priority files** (highest call count):
- `container/exec/exec.go` (6 calls)
- `container/run/run.go` (5 calls)
- `container/start/start.go` (4 calls)
- `container/cp/cp.go` (3 calls)

### Migration Recipe: `cmdutil.PrintError`

```go
// BEFORE
cmdutil.PrintError(ios, "config file not found at %s", path)
return cmdutil.SilentError

// AFTER — if the error should be returned
return fmt.Errorf("config file not found at %s", path)

// AFTER — if you need custom formatting before exiting
fmt.Fprintf(ios.ErrOut, "%s Config file not found at %s\n", cs.FailureIcon(), path)
return cmdutil.SilentError
```

**Priority files** (highest call count):
- `ralph/run/run.go` (5 calls)
- `generate/generate.go` (4 calls)
- `ralph/status/status.go` (4 calls)
- `container/create/create.go` (4 calls)
- `container/run/run.go` (3 calls)
- `ralph/reset/reset.go` (3 calls)
- `config/check/check.go` (3 calls)

### Migration Recipe: `cmdutil.PrintWarning`

```go
// BEFORE
cmdutil.PrintWarning(ios, "BuildKit is not available, falling back to legacy builder")

// AFTER
cs := ios.ColorScheme()
fmt.Fprintf(ios.ErrOut, "%s BuildKit is not available, falling back to legacy builder\n", cs.WarningIcon())
```

**Files**: `config/check/check.go`, `container/start/start.go`, `container/create/create.go`, `container/run/run.go`

### Migration Recipe: `cmdutil.PrintNextSteps`

```go
// BEFORE
cmdutil.PrintNextSteps(ios,
    fmt.Sprintf("Run 'clawker run %s' to start the container", agent),
    "Run 'clawker config check' to verify your configuration",
)

// AFTER
fmt.Fprintln(ios.ErrOut, "\nNext steps:")
fmt.Fprintf(ios.ErrOut, "  1. Run 'clawker run %s' to start the container\n", agent)
fmt.Fprintln(ios.ErrOut, "  2. Run 'clawker config check' to verify your configuration")
```

**Priority files**:
- `container/create/create.go` (4 calls)
- `container/run/run.go` (4 calls)
- `config/check/check.go` (3 calls)

### Migration Recipe: Raw `tabwriter`

```go
// BEFORE
w := tabwriter.NewWriter(ios.Out, 0, 0, 2, ' ', 0)
fmt.Fprintln(w, "NAME\tSTATUS\tIMAGE")
for _, c := range containers {
    fmt.Fprintf(w, "%s\t%s\t%s\n", c.Name, c.Status, c.Image)
}
w.Flush()

// AFTER
tp := tableprinter.New(ios, "NAME", "STATUS", "IMAGE")
for _, c := range containers {
    tp.AddRow(c.Name, c.Status, c.Image)
}
return tp.Render()
```

**Files**: `container/list`, `container/top`, `container/stats` (×2), `image/list`, `volume/list`, `network/list`, `worktree/list`

### Migration Recipe: `cmdutil.PrintStatus`

```go
// BEFORE
cmdutil.PrintStatus(ios, opts.Quiet, "%s Container %s started", cs.SuccessIcon(), name)

// AFTER
if !opts.Quiet {
    fmt.Fprintf(ios.ErrOut, "%s Container %s started\n", cs.SuccessIcon(), name)
}
```

Zero production usages remain.

### Migration Recipe: `cmdutil.OutputJSON`

```go
// BEFORE
return cmdutil.OutputJSON(ios, data)

// AFTER
enc := json.NewEncoder(ios.Out)
enc.SetIndent("", "  ")
return enc.Encode(data)
```

Zero production usages remain.

### Migration Recipe: `cmdutil.PrintHelpHint`

```go
// BEFORE
cmdutil.PrintHelpHint(ios, cmd.CommandPath())

// AFTER
fmt.Fprintf(ios.ErrOut, "\nRun '%s --help' for more information.\n", cmd.CommandPath())
```

Zero production usages remain.

## 11. Anti-Patterns

### Direct Error Printing
```go
// BAD — bypass centralized error rendering
fmt.Fprintf(ios.ErrOut, "Error: %s\n", err)
return err  // Main() will print it AGAIN

// GOOD
return fmt.Errorf("context: %w", err)
```

### Direct `os.Exit` in Commands
```go
// BAD — skips defers (terminal restore, container cleanup)
os.Exit(1)

// GOOD — propagate via ExitError
return &cmdutil.ExitError{Code: exitCode}
```

### Logger for User Output
```go
// BAD — zerolog is for file logging only
logger.Warn().Msg("image not found")

// GOOD — user-visible output to IOStreams
fmt.Fprintf(ios.ErrOut, "%s Image not found\n", cs.WarningIcon())
```

Logger methods (`logger.Debug`, `logger.Warn`, `logger.Error`, `logger.Info`) write to `~/.local/clawker/logs/clawker.log` — they are invisible to the user. Use them for diagnostic file logging only, never for user-facing output. There are 14 command files in `internal/cmd/` that use logger calls — these are valid for diagnostic logging, not user output.

### Raw `tabwriter` in Commands
```go
// BAD — no TTY-aware styling, inconsistent with rest of CLI
w := tabwriter.NewWriter(ios.Out, 0, 0, 2, ' ', 0)

// GOOD
tp := tableprinter.New(ios, headers...)
```

## 12. Testing Output

### Test Setup

```go
tio := iostreams.NewTestIOStreams()
// tio.IOStreams — non-TTY, colors disabled by default
// tio.OutBuf   — captures stdout
// tio.ErrBuf   — captures stderr
// tio.InBuf    — provides stdin

// Customize for specific tests:
tio.SetInteractive(true)     // stdin + stdout TTY
tio.SetColorEnabled(true)    // enable colors
tio.SetTerminalSize(120, 40) // set terminal dimensions
tio.SetSpinnerDisabled(true) // disable spinner animation
```

### Command Test Pattern

```go
func TestCmdRun(t *testing.T) {
    fake := dockertest.NewFakeClient()
    fake.SetupContainerCreate()
    fake.SetupContainerStart()

    tio := iostreams.NewTestIOStreams()
    f := &cmdutil.Factory{
        IOStreams: tio.IOStreams,
        TUI:      tui.NewTUI(tio.IOStreams),
        Client:   func(ctx context.Context) (*docker.Client, error) { return fake.Client, nil },
    }

    cmd := NewCmdRun(f, nil)  // nil runF = real implementation
    cmd.SetArgs([]string{"--detach", "alpine"})
    cmd.SetIn(tio.InBuf)
    cmd.SetOut(tio.OutBuf)
    cmd.SetErr(tio.ErrBuf)

    err := cmd.Execute()
    assert.NoError(t, err)
    assert.Contains(t, tio.OutBuf.String(), "container-id")
}
```

### Asserting on Output

```go
// Check stdout (data)
assert.Contains(t, tio.OutBuf.String(), "expected-data")

// Check stderr (status messages)
assert.Contains(t, tio.ErrBuf.String(), "Container started")

// Check no output on wrong stream
assert.Empty(t, tio.OutBuf.String(), "status messages should go to stderr")
```

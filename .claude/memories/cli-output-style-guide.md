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

### Machine-Readable Output (Format/Filter Flags)

Format/filter flags are for **static list commands only** (Scenario 1). They produce one-shot tabular data that users pipe, grep, or script against. Do NOT add these to live-display or live-interactive commands — streaming output has its own `--progress` flag.

**When to add format/filter flags:**
- `* list` / `* ls` commands — `container list`, `image list`, `volume list`, `network list`, `worktree list`
- Any command whose primary output is a table of resources

**When NOT to add:**
- `* build`, `* run`, `* start` — these are live-display (Scenario 3) with streaming progress
- `* inspect`, `* logs` — single-resource detail or streaming logs, not tabular lists
- `* remove`, `* prune` — action commands, not data queries
- Live-interactive commands (Scenario 4) — BubbleTea owns the terminal

**Flag registration** (via `cmdutil`):
- `cmdutil.AddFormatFlags(cmd)` → registers `--format`, `--json`, `-q`/`--quiet` with PreRunE mutual exclusivity
- `cmdutil.AddFilterFlags(cmd)` → registers repeatable `--filter key=value`

**Output modes:**

| User Flag | Output Mode | Handler |
|-----------|-------------|---------|
| _(none)_ | Styled TTY table / plain tabwriter | `opts.TUI.NewTable(headers...)` |
| `--format table` | Same as default | Same |
| `--json` or `--format json` | Pretty-printed JSON | `cmdutil.WriteJSON(ios.Out, rows)` |
| `--format '{{.ID}}'` | Go template, one line per item | `cmdutil.ExecuteTemplate(ios.Out, format, items)` |
| `--format 'table {{.ID}}\t{{.Size}}'` | Go template through tabwriter | Same (tabwriter-wrapped) |
| `-q` / `--quiet` | IDs only, one per line | `fmt.Fprintln(ios.Out, id)` |

**Mutual exclusivity** (enforced in PreRunE):
- `--quiet` vs `--format`/`--json` → `FlagError`
- `--format` vs `--json` → `FlagError`

See Section 7 for the complete list command wiring pattern.

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
    tp := opts.TUI.NewTable("NAME", "STATUS", "IMAGE")
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

### Hybrid Scenario 3+4: Wizard + Live-Display

Some commands combine an interactive wizard (Scenario 4) with live progress display (Scenario 3). The `init` command is the canonical example: it runs a multi-step wizard for user choices, then a TUI progress display for the image build.

**Stream strategy:**
- Wizard phase: BubbleTea owns terminal (alt screen), wizard manages all rendering
- Progress phase: same as Scenario 3 (TUI manages terminal, summary to stderr)
- After both phases: static next steps to stderr

```go
func Run(ctx context.Context, opts *InitOptions) error {
    // Phase 1: Interactive wizard (Scenario 4)
    fields := buildWizardFields()
    result, err := opts.TUI.RunWizard(fields)
    if err != nil { return fmt.Errorf("wizard failed: %w", err) }
    if !result.Submitted { return nil }

    buildImage := result.Values["build"] == "Yes"
    flavor := result.Values["flavor"]

    // Phase 2: TUI progress display (Scenario 3)
    ch := make(chan tui.ProgressStep, 4)
    go func() {
        defer close(ch)
        ch <- tui.ProgressStep{ID: "build", Name: "Building base image", Status: tui.StepRunning}
        buildErr = client.BuildImage(ctx, buildContext, buildOpts)
        // ... send StepComplete or StepError ...
    }()
    opts.TUI.RunProgress("auto", tui.ProgressDisplayConfig{
        Title: "Building", Subtitle: tag, CompletionVerb: "Built",
    }, ch)

    // Phase 3: Static output (next steps to stderr)
    fmt.Fprintln(ios.ErrOut, "Next Steps:")
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

| Method | Usage | Color |
|--------|-------|-------|
| `cs.Primary(s)` | Brand, titles | `ColorBurntOrange` (`#E8714A`) |
| `cs.Secondary(s)` | Supporting text | `ColorDeepSkyBlue` (`#00BFFF`) |
| `cs.Accent(s)` | Emphasis | `ColorSalmon` (`#FF6B6B`) |
| `cs.Success(s)` | Positive outcomes | `ColorEmerald` (`#04B575`) |
| `cs.Warning(s)` | Caution | `ColorAmber` (`#FFCC00`) |
| `cs.Error(s)` | Errors | `ColorHotPink` (`#FF5F87`) |
| `cs.Info(s)` | Informational | `ColorSkyBlue` (`#87CEEB`) |
| `cs.Muted(s)` | Dimmed/secondary | `ColorDimGray` (`#626262`) |
| `cs.Highlight(s)` | Attention | `ColorOrchid` (`#AD58B4`) |
| `cs.Disabled(s)` | Inactive | `ColorCharcoal` (`#4A4A4A`) |

### Concrete Colors

Use semantic colors when possible. Concrete colors for specific design needs:

`cs.Red/Redf`, `cs.Yellow/Yellowf`, `cs.Green/Greenf`, `cs.Blue/Bluef`, `cs.Cyan/Cyanf`, `cs.Magenta/Magentaf`, `cs.BrandOrange/BrandOrangef` (deprecated, delegates to Primary/Primaryf)

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

## 7. List Commands: Tables + Format/Filter Flags

This section is the canonical recipe for implementing a list command with full format/filter support. It covers: Options struct, flag registration, display row struct, the format dispatch switch, TablePrinter, filters, and testing.

**Applies to**: Scenario 1 (static) list commands only. See Section 2 for when to use.

### 7.1 Options Struct Pattern

Every list command needs `FormatFlags` and optionally `FilterFlags` on its Options:

```go
type ListOptions struct {
    IOStreams *iostreams.IOStreams
    TUI      *tui.TUI
    Client   func(context.Context) (*docker.Client, error)

    Format *cmdutil.FormatFlags   // --format, --json, --quiet
    Filter *cmdutil.FilterFlags   // --filter key=value (optional)
    All    bool                   // command-specific flags
}
```

### 7.2 Flag Registration (NewCmd)

Register format/filter flags in the command constructor. They chain PreRunE automatically:

```go
func NewCmdList(f *cmdutil.Factory, runF func(context.Context, *ListOptions) error) *cobra.Command {
    opts := &ListOptions{
        IOStreams: f.IOStreams,
        TUI:      f.TUI,
        Client:   f.Client,
    }

    cmd := &cobra.Command{
        Use:     "list",
        Aliases: []string{"ls"},
        // ...
        RunE: func(cmd *cobra.Command, args []string) error {
            if runF != nil { return runF(cmd.Context(), opts) }
            return listRun(cmd.Context(), opts)
        },
    }

    opts.Format = cmdutil.AddFormatFlags(cmd)   // registers --format, --json, -q/--quiet
    opts.Filter = cmdutil.AddFilterFlags(cmd)   // registers --filter (repeatable)
    cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Show all resources")
    return cmd
}
```

### 7.3 Display Row Struct

Define a struct for template/JSON output. JSON tags are lowercase. Field names are what users see in `--format '{{.FieldName}}'`:

```go
type imageRow struct {
    Image   string `json:"image"`
    ID      string `json:"id"`
    Created string `json:"created"`
    Size    string `json:"size"`
}
```

Build rows from domain objects:
```go
func buildRows(items []whail.ImageSummary) []imageRow {
    var rows []imageRow
    for _, img := range items {
        rows = append(rows, imageRow{
            Image:   img.RepoTags[0],
            ID:      truncateID(img.ID),
            Created: formatCreated(img.Created),
            Size:    formatSize(img.Size),
        })
    }
    return rows
}
```

### 7.4 Format Dispatch Switch (listRun)

The run function follows this canonical structure:

```go
func listRun(ctx context.Context, opts *ListOptions) error {
    ios := opts.IOStreams

    // 1. Parse and validate filters
    filters, err := opts.Filter.Parse()
    if err != nil { return err }
    if err := cmdutil.ValidateFilterKeys(filters, validFilterKeys); err != nil {
        return err
    }

    // 2. Fetch data
    items, err := fetchItems(ctx, opts)
    if err != nil { return fmt.Errorf("listing resources: %w", err) }

    // 3. Apply local filters
    items = applyFilters(items, filters)

    // 4. Handle empty results
    if len(items) == 0 {
        fmt.Fprintln(ios.ErrOut, "No resources found.")
        return nil
    }

    // 5. Build display rows
    rows := buildRows(items)

    // 6. Format dispatch
    switch {
    case opts.Format.Quiet:
        for _, item := range items {
            fmt.Fprintln(ios.Out, item.ID)
        }
        return nil

    case opts.Format.IsJSON():
        return cmdutil.WriteJSON(ios.Out, rows)

    case opts.Format.IsTemplate():
        return cmdutil.ExecuteTemplate(ios.Out, opts.Format.Template(), cmdutil.ToAny(rows))

    default:
        tp := opts.TUI.NewTable("NAME", "ID", "STATUS")
        for _, r := range rows {
            tp.AddRow(r.Name, r.ID, r.Status)
        }
        return tp.Render()
    }
}

// cmdutil.ToAny converts typed slice to []any for ExecuteTemplate.
// Defined in cmdutil/format.go — use instead of local helpers.
```

**Key rules for the switch:**
- Quiet first (cheapest path, no row construction needed in theory, but rows are built before switch for simplicity)
- JSON second (structured data)
- Template third (user-defined format)
- Default last (styled table)
- Empty results handled BEFORE the switch — print to stderr, return nil

### 7.5 TablePrinter API

`internal/tui/table.go` — TTY-aware tabular output to `ios.Out`.

Access via Factory noun: `opts.TUI.NewTable(headers...)`

```go
tp := opts.TUI.NewTable("NAME", "STATUS", "IMAGE")
tp.AddRow("web", "running", "nginx:latest")
tp.AddRow("db", "stopped", "postgres:16")
return tp.Render()
```

**Rendering modes:**
- **TTY + color (styled)**: `lipgloss/table` with `StyleFunc`. Muted uppercase headers (`TableHeaderStyle`), primary color first column (`TablePrimaryColumnStyle`), no borders. Column widths auto-sized by median-based resizer.
- **Non-TTY / piped (plain)**: `text/tabwriter` with 2-space column gaps, no styling — machine-parseable.

**Style overrides** (optional, for commands needing custom column colors):
```go
tp := opts.TUI.NewTable("NAME", "STATUS")
tp.WithPrimaryStyle(func(s string) string { return cs.Success(s) })
```

### 7.6 Filter Implementation

Each list command defines its valid filter keys and matching logic:

```go
var validFilterKeys = []string{"reference", "status"}

func applyFilters(items []Item, filters []cmdutil.Filter) []Item {
    if len(filters) == 0 { return items }
    var result []Item
    for _, item := range items {
        if matchesFilters(item, filters) {
            result = append(result, item)
        }
    }
    return result
}

func matchesFilters(item Item, filters []cmdutil.Filter) bool {
    for _, f := range filters {
        switch f.Key {
        case "reference":
            if !matchGlob(item.Name, f.Value) { return false }
        case "status":
            if item.Status != f.Value { return false }
        }
    }
    return true  // all filters passed
}
```

Glob matching (trailing `*` only, Docker CLI convention):
```go
func matchGlob(s, pattern string) bool {
    if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
        return strings.HasPrefix(s, prefix)
    }
    return s == pattern
}
```

### 7.7 Anti-Patterns

```go
// BAD — raw tabwriter loses TTY styling
w := tabwriter.NewWriter(ios.Out, 0, 0, 2, ' ', 0)

// GOOD — use TablePrinter via TUI Factory noun
tp := opts.TUI.NewTable("NAME", "STATUS", "IMAGE")
```

```go
// BAD — format flags on a streaming command
opts.Format = cmdutil.AddFormatFlags(cmd) // in image build? NO!

// GOOD — format flags on list commands only
// Live-display commands use --progress flag instead
```

```go
// BAD — quiet mode via separate bool field
type ListOptions struct { Quiet bool }
cmd.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "...")

// GOOD — quiet is part of FormatFlags, validated for mutual exclusivity
opts.Format = cmdutil.AddFormatFlags(cmd)
// access via opts.Format.Quiet
```

**Remaining raw tabwriter usages** (7 files, migration needed):
- `container/list`, `container/top`, `container/stats` (x2)
- `volume/list`, `network/list`, `worktree/list`

### 7.8 Testing List Commands

**Flag parsing tests** (no Docker, no fakes):
```go
func TestNewCmdList_FormatFlags(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        wantErr string
    }{
        {name: "json flag", input: "--json"},
        {name: "quiet and json exclusive", input: "-q --json", wantErr: "mutually exclusive"},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tio := iostreamstest.New()
            f := &cmdutil.Factory{IOStreams: tio.IOStreams}
            cmd := NewCmdList(f, func(_ context.Context, _ *ListOptions) error { return nil })
            argv, _ := shlex.Split(tt.input)
            cmd.SetArgs(argv)
            cmd.SetIn(&bytes.Buffer{})
            cmd.SetOut(&bytes.Buffer{})
            cmd.SetErr(&bytes.Buffer{})
            _, err := cmd.ExecuteC()
            if tt.wantErr != "" {
                require.Contains(t, err.Error(), tt.wantErr)
            } else {
                require.NoError(t, err)
            }
        })
    }
}
```

**Rendering tests** (uses dockertest fakes, exercises full listRun):
```go
t.Run("json_output", func(t *testing.T) {
    fake := dockertest.NewFakeClient()
    fake.SetupImageList(dockertest.ImageSummaryFixture("myapp:latest"))
    f, tio := testFactory(t, fake)
    cmd := NewCmdList(f, nil)  // nil runF = real implementation
    cmd.SetArgs([]string{"--json"})
    cmd.SetIn(&bytes.Buffer{})
    cmd.SetOut(tio.OutBuf)
    cmd.SetErr(tio.ErrBuf)
    err := cmd.Execute()
    require.NoError(t, err)
    assert.Contains(t, tio.OutBuf.String(), `"image": "myapp:latest"`)
})

t.Run("filter_reference", func(t *testing.T) {
    fake := dockertest.NewFakeClient()
    fake.SetupImageList(
        dockertest.ImageSummaryFixture("clawker-demo:latest"),
        dockertest.ImageSummaryFixture("node:20-slim"),
    )
    f, tio := testFactory(t, fake)
    cmd := NewCmdList(f, nil)
    cmd.SetArgs([]string{"--filter", "reference=clawker*"})
    // ...
    assert.Contains(t, tio.OutBuf.String(), "clawker-demo:latest")
    assert.NotContains(t, tio.OutBuf.String(), "node:20-slim")
})
```

**Golden file tests** (for table output stability):
```bash
GOLDEN_UPDATE=1 go test ./internal/cmd/image/list/... -run TestImageList_Golden -v
```

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

### Wizard (Multi-Step Prompts)

For multi-step forms with back-navigation, use `TUI.RunWizard` instead of chaining multiple `Prompter` calls.

**Key types**: `WizardField` (field spec), `WizardResult` (collected values + submitted flag), `FieldOption` (label + description for select fields), `WizardFieldKind` (`FieldSelect`, `FieldText`, `FieldConfirm`)

**Entry point**: `f.TUI.RunWizard(fields []tui.WizardField) (tui.WizardResult, error)`

**Example** (based on init command):

```go
fields := []tui.WizardField{
    {
        ID: "build", Title: "Build Image", Prompt: "Build an initial base image?",
        Kind: tui.FieldSelect,
        Options: []tui.FieldOption{
            {Label: "Yes", Description: "Recommended"},
            {Label: "No", Description: "Skip for now"},
        },
        DefaultIdx: 0,
    },
    {
        ID: "flavor", Title: "Flavor", Prompt: "Select Linux flavor",
        Kind: tui.FieldSelect,
        Options: flavorOptions,
        SkipIf: func(vals tui.WizardValues) bool { return vals["build"] != "Yes" },
    },
    {
        ID: "confirm", Title: "Submit", Prompt: "Proceed?",
        Kind: tui.FieldConfirm, DefaultYes: true,
    },
}

result, err := opts.TUI.RunWizard(fields)
if err != nil { return fmt.Errorf("wizard failed: %w", err) }
if !result.Submitted { return nil } // user cancelled

buildImage := result.Values["build"] == "Yes"   // SelectField returns label
flavor := result.Values["flavor"]                // label string
confirmed := result.Values["confirm"] == "yes"   // ConfirmField returns "yes"/"no"
```

**Reference implementation**: `internal/cmd/init/init.go`

See `prompter-wizard` memory for full architecture, field types, navigation, SkipIf behavior, filterQuit internals, and testing patterns.

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

### Prompter Wizard

For multi-step interactive forms see `prompter-wizard` memory.

### Testing Tree Display

**Golden file tests** (plain mode — deterministic):
```bash
go test ./internal/tui/... -run TestProgressPlain_Golden -v
GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v  # regenerate
```

**Unit tests** (model-level, no BubbleTea program):
```go
func newTestProgressModel(t *testing.T) (progressModel, *iostreamstest.TestIOStreams) {
    tio := iostreamstest.New()
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
| Raw `tabwriter.NewWriter` | 7 | 7 | `f.TUI.NewTable(headers...)` |
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
- `loop/run/run.go` (5 calls)
- `generate/generate.go` (4 calls)
- `loop/status/status.go` (4 calls)
- `container/create/create.go` (4 calls)
- `container/run/run.go` (3 calls)
- `loop/reset/reset.go` (3 calls)
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
tp := opts.TUI.NewTable("NAME", "STATUS", "IMAGE")
for _, c := range containers {
    tp.AddRow(c.Name, c.Status, c.Image)
}
return tp.Render()
```

**Files**: `container/list`, `container/top`, `container/stats` (×2), `volume/list`, `network/list`, `worktree/list`

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

// AFTER — use the new centralized function
return cmdutil.WriteJSON(ios.Out, data)
```

Replaced by `cmdutil.WriteJSON` in `json.go`. Zero production usages of old `OutputJSON` remain.

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

Logger methods are now purely to write to logfile for developers and users to provide developers:
* `logger.Debug` write to `~/.local/clawker/logs/clawker.log`. We always write debug logs to file no matter what. the `--debug|-D` flag is for a future feature to additionally print the debug statements to stdout for power users and developers who want to see debug logs in real time, but its not a toggle for whether debug logs get written at all. We want to capture debug logs in the field by default to make it easier to troubleshoot issues that users are having without requiring them to enable debug mode ahead of time.
* `logger.error` write panics / failures / stacktraces (if available) to `~/.local/clawker/logs/clawker.log`. Concise user facing error messages get returned to Main() and handled. Main() will eventually have a decision tree on how to handle/print errors based on error type with a catchall printError. But currently only the catchall is in existence. if you don't know which error type to choose default to cmdutii.ExitError or ask the user for clarifying questions
* Existing (`logger.Warn`, `logger.Info`) get converted to using stdout(info)/stderr(warn) with appropriate icons and formatting as per the new guidelines for non-interactive mode, for live interactive modes they get integrated into whatever TUI component is appropriate


### Raw `tabwriter` in Commands
```go
// BAD — no TTY-aware styling, inconsistent with rest of CLI
w := tabwriter.NewWriter(ios.Out, 0, 0, 2, ' ', 0)

// GOOD — use TablePrinter via TUI Factory noun
tp := f.TUI.NewTable(headers...)
```

## 12. Testing Output

### Test Setup

```go
tio := iostreamstest.New()
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

    tio := iostreamstest.New()
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

# Cmdutil Package

Lightweight shared CLI utilities: Factory struct (DI container), output helpers, argument validators.

Heavy command helpers have been extracted to dedicated packages:
- Image resolution: `internal/docker/` (image_resolve.go)
- Build utilities: `internal/bundler/`
- Project registration: `internal/project/`
- Container naming/middleware: `internal/docker/`

## Key Files

| File | Purpose |
|------|---------|
| `factory.go` | `Factory` -- pure struct with closure fields (no methods, no construction logic) |
| `output.go` | Deprecated: `HandleError`, `PrintError`, `PrintWarning`, `PrintNextSteps`, `PrintStatus`, `OutputJSON`, `PrintHelpHint` |
| `errors.go` | `ExitError`, `FlagError`, `FlagErrorf`, `FlagErrorWrap`, `SilentError` — typed error vocabulary for centralized rendering |
| `required.go` | `NoArgs`, `ExactArgs`, `RequiresMinArgs`, `RequiresMaxArgs`, `RequiresRangeArgs`, `AgentArgsValidator`, `AgentArgsValidatorExact` |
| `project.go` | `ErrAborted` sentinel (stdlib only) |
| `format.go` | `Format`, `ParseFormat`, `FormatFlags`, `AddFormatFlags` -- reusable `--format`/`--json`/`--quiet` flag handling |
| `json.go` | `WriteJSON` -- pretty-printed JSON output (replaces deprecated `OutputJSON`) |
| `filter.go` | `Filter`, `ParseFilters`, `ValidateFilterKeys`, `FilterFlags`, `AddFilterFlags` -- reusable `--filter key=value` flag handling |
| `template.go` | `DefaultFuncMap`, `ExecuteTemplate` -- Go template execution for `--format TEMPLATE` output |
| `worktree.go` | `ParseWorktreeFlag`, `WorktreeSpec` -- git worktree flag parsing |

## Factory (`factory.go`)

Pure dependency injection container struct. 9 fields total: 3 eager values + 6 lazy nouns. Closure fields are wired by `internal/cmd/factory/default.go`.

```go
type Factory struct {
    // Eager (set at construction)
    Version  string
    IOStreams *iostreams.IOStreams
    TUI      *tui.TUI

    // Lazy nouns (each returns a thing; commands call methods on the thing)
    Client       func(context.Context) (*docker.Client, error)
    Config       func() *config.Config
    GitManager   func() (*git.GitManager, error)
    HostProxy    func() hostproxy.HostProxyService
    SocketBridge func() socketbridge.SocketBridgeManager
    Prompter     func() *prompter.Prompter
}
```

**Field semantics:**
- `Version`, `IOStreams` -- set eagerly at construction
- `TUI` -- eager `*tui.TUI` presentation layer noun; commands call `.RunProgress()` on it. Hooks are registered post-construction via `.RegisterHooks()` (pointer sharing ensures commands see hooks registered in PersistentPreRunE)
- `Client(ctx)` -- lazy Docker client (connects on first call)
- `Config()` -- returns `*config.Config` gateway (which itself lazy-loads Project, Settings, Resolution, Registry)
- `GitManager()` -- lazy git manager for worktree operations; uses project root from Config.Project.RootDir()
- `HostProxy()` -- returns `hostproxy.HostProxyService` (interface); commands call `.EnsureRunning()` / `.IsRunning()` / `.ProxyURL()` on it. Mock: `hostproxytest.MockManager`
- `SocketBridge()` -- returns `socketbridge.SocketBridgeManager` (interface); commands call `.EnsureBridge()` / `.StopBridge()` on it. Mock: `socketbridgetest.MockManager`
- `Prompter()` -- returns `*prompter.Prompter` for interactive prompts

**Config gateway pattern:** Instead of separate `f.Settings()`, `f.Registry()`, `f.Resolution()` fields, commands now use `f.Config().Settings()`, `f.Config().Registry()`, `f.Config().Resolution()`, etc.

**Testing:** Construct minimal Factory structs directly:
```go
tio := iostreams.NewTestIOStreams()
f := &cmdutil.Factory{
    IOStreams: tio.IOStreams,
    TUI:      tui.NewTUI(tio.IOStreams),
}
```

Commands that use `opts.TUI.RunProgress()` require the `TUI` field. Commands that only use `f.IOStreams` for static output don't need it.

## Error Handling & Output (`output.go`, `errors.go`)

### Active Functions

None — all output helpers are deprecated. Use `fmt.Fprintf` with `ios.ColorScheme()` directly.

**Deprecated** (`output.go`): `HandleError`, `PrintError`, `PrintWarning`, `PrintNextSteps`, `PrintStatus`, `OutputJSON`, `PrintHelpHint` — use `fmt.Fprintf` with `ios.ColorScheme()` directly.

### Error Types (`errors.go`)

```go
// FlagError triggers usage display in Main()'s centralized error rendering.
type FlagError struct{ err error }
func FlagErrorf(format string, args ...any) error
func FlagErrorWrap(err error) error  // nil-safe: returns nil for nil input

// SilentError signals the error was already displayed — Main() just exits non-zero.
var SilentError = errors.New("SilentError")
```

### ExitError

`ExitError{Code int}` — propagates non-zero container exit codes through Cobra. Commands return `&ExitError{Code: status}` instead of `os.Exit()`. Root command extracts the code after all defers run.

## Argument Validators (`required.go`)

All return `cobra.PositionalArgs` (except `NoArgs` which is one directly).

**Standard validators:**
- `NoArgs` -- error if any args provided (also handles "unknown command" for parent commands)
- `ExactArgs(n)` -- error if not exactly n args
- `RequiresMinArgs(n)` -- error if fewer than n args
- `RequiresMaxArgs(n)` -- error if more than n args
- `RequiresRangeArgs(min, max)` -- error if outside [min, max] range

**Agent-aware validators** (for commands with `--agent` flag):
- `AgentArgsValidator(minArgs)` -- `--agent` mutually exclusive with positional args; requires minArgs without `--agent`
- `AgentArgsValidatorExact(n)` -- same but requires exactly n args without `--agent`

All validators include binary name, command path, and usage line in error messages.

## Format Flags (`format.go`)

Reusable `--format`, `--json`, and `-q`/`--quiet` flag handling for list commands. Follows Docker CLI conventions.

**Mode constants**: `ModeDefault` (`""`), `ModeTable`, `ModeJSON`, `ModeTemplate`, `ModeTableTemplate`

**`Format` type**: `ParseFormat(s) (Format, error)`. Methods: `IsDefault()`, `IsJSON()`, `IsTemplate()`, `IsTableTemplate()`, `Template()`.

**`FormatFlags`**: `AddFormatFlags(cmd) *FormatFlags` — registers `--format`, `--json`, `--quiet` with PreRunE mutual exclusivity validation. Convenience delegates: `ff.IsJSON()`, `ff.IsTemplate()`, `ff.IsDefault()`, `ff.IsTableTemplate()`, `ff.Template()` — avoid `opts.Format.Format.IsJSON()` stutter.

**`ToAny[T any](items []T) []any`** — generic slice conversion for `ExecuteTemplate`.

## Template Execution (`template.go`)

**`DefaultFuncMap()`** — Docker CLI-compatible template functions: `json` (returns error), `upper`, `lower`, `title` (unicode-safe), `split`, `join`, `truncate` (negative n → empty).

**`ExecuteTemplate(w, format, items)`** — parses and executes Go template per item. Table-template mode uses tabwriter for column alignment. Stdlib only.

## JSON Output (`json.go`)

Pretty-printed JSON output for `--json` and `--format json` modes. Replaces deprecated `OutputJSON` from `output.go`.

```go
func WriteJSON(w io.Writer, data any) error
```

Writes indented JSON (2-space indent) followed by a newline. HTML escaping is disabled (`SetEscapeHTML(false)`) so values like `<none>:<none>` are written literally, not as `\u003cnone\u003e`. Accepts any JSON-serializable value (structs, slices, maps, nil).

## Filter Flags (`filter.go`)

Reusable `--filter key=value` flag handling for list commands. Supports repeatable `--filter` flags.

```go
type Filter struct { Key, Value string }

func ParseFilters(raw []string) ([]Filter, error)
func ValidateFilterKeys(filters []Filter, validKeys []string) error

type FilterFlags struct { raw []string }  // unexported raw field
func AddFilterFlags(cmd *cobra.Command) *FilterFlags
func (ff *FilterFlags) Parse() ([]Filter, error)
```

**`AddFilterFlags`** registers a repeatable `--filter` flag via `StringArrayVar` (not `StringSliceVar`, to handle commas in values).

**`ParseFilters`** splits on first `=` only (values may contain `=`). Empty keys return `FlagError`.

**`ValidateFilterKeys`** checks each filter's key against a command-specific allow list. Returns `FlagError` listing valid keys on mismatch.

**Usage pattern in commands:**
```go
opts.Filter = cmdutil.AddFilterFlags(cmd)
// In run function:
filters, err := opts.Filter.Parse()
cmdutil.ValidateFilterKeys(filters, []string{"reference"})
```

## Sentinels (`project.go`)

`ErrAborted` -- returned when user cancels an interactive operation

## Worktree Flag Parsing (`worktree.go`)

Utilities for parsing the `--worktree` flag used by container run/create commands.

```go
type WorktreeSpec struct {
    Branch string // Branch name to use/create
    Base   string // Base branch (empty if not specified)
}

func ParseWorktreeFlag(value, agentName string) (*WorktreeSpec, error)
```

**Flag syntax:**
- Empty string: auto-generate branch name (`clawker-<agent>-<timestamp>`)
- `"branch"`: use existing or create from HEAD
- `"branch:base"`: create branch from specified base

**Validation:**
- Branch names must match `^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`
- Rejects shell metacharacters (`;`, `` ` ``, `$`, etc.) for security
- Rejects git-special patterns (`.lock` suffix, `..`, `@{`)


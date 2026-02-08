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
| `worktree.go` | `ParseWorktreeFlag`, `WorktreeSpec` -- git worktree flag parsing |

## Factory (`factory.go`)

Pure dependency injection container struct. 10 fields total: 4 eager values + 6 lazy nouns. Closure fields are wired by `internal/cmd/factory/default.go`.

```go
type Factory struct {
    // Eager (set at construction)
    Version  string
    Commit   string
    IOStreams *iostreams.IOStreams
    TUI      *tui.TUI

    // Lazy nouns (each returns a thing; commands call methods on the thing)
    Client       func(context.Context) (*docker.Client, error)
    Config       func() *config.Config
    GitManager   func() (*git.GitManager, error)
    HostProxy    func() *hostproxy.Manager
    SocketBridge func() socketbridge.SocketBridgeManager
    Prompter     func() *prompter.Prompter
}
```

**Field semantics:**
- `Version`, `Commit`, `IOStreams` -- set eagerly at construction
- `TUI` -- eager `*tui.TUI` presentation layer noun; commands call `.RunProgress()` on it. Hooks are registered post-construction via `.RegisterHooks()` (pointer sharing ensures commands see hooks registered in PersistentPreRunE)
- `Client(ctx)` -- lazy Docker client (connects on first call)
- `Config()` -- returns `*config.Config` gateway (which itself lazy-loads Project, Settings, Resolution, Registry)
- `GitManager()` -- lazy git manager for worktree operations; uses project root from Config.Project.RootDir()
- `HostProxy()` -- returns `*hostproxy.Manager`; commands call `.EnsureRunning()` / `.Stop(ctx)` on it
- `SocketBridge()` -- returns `socketbridge.SocketBridgeManager` (interface); commands call `.EnsureBridge()` / `.StopBridge()` on it. Mock: `socketbridgetest.MockManager`
- `Prompter()` -- returns `*prompter.Prompter` for interactive prompts

**Config gateway pattern:** Instead of separate `f.Settings()`, `f.Registry()`, `f.Resolution()` fields, commands now use `f.Config().Settings()`, `f.Config().Registry()`, `f.Config().Resolution()`, etc.

**Testing:** Construct minimal Factory structs directly:
```go
tio := iostreams.NewTestIOStreams()
f := &cmdutil.Factory{
    Version:  "1.0.0",
    Commit:   "abc123",
    IOStreams: tio.IOStreams,
    TUI:      tui.NewTUI(tio.IOStreams),
}
```

Commands that use `opts.TUI.RunProgress()` require the `TUI` field. Commands that only use `f.IOStreams` for static output don't need it.

## Error Handling & Output (`output.go`, `errors.go`)

### Active Functions

None — all output helpers are deprecated. Use `fmt.Fprintf` with `ios.ColorScheme()` directly.

### Deprecated Functions (use gh-style fprintf instead)
`PrintStatus(ios, quiet, format, args...)` -- **Deprecated**: inline `if !quiet { fmt.Fprintf(ios.ErrOut, format+"\n", args...) }`
`OutputJSON(ios, data) error` -- **Deprecated**: inline `json.NewEncoder(ios.Out)` with `SetIndent`
`PrintHelpHint(ios, cmdPath)` -- **Deprecated**: inline `fmt.Fprintf(ios.ErrOut, "\nRun '%s --help'...\n", cmdPath)`
`HandleError(ios, err)` -- **Deprecated**: return errors to Main() for centralized rendering
`PrintError(ios, format, args...)` -- **Deprecated**: use `fmt.Fprintf(ios.ErrOut, "Error: "+format+"\n", args...)`
`PrintWarning(ios, format, args...)` -- **Deprecated**: use `fmt.Fprintf(ios.ErrOut, "%s "+format+"\n", cs.WarningIcon(), args...)`
`PrintNextSteps(ios, steps...)` -- **Deprecated**: inline next-steps output with `fmt.Fprintf(ios.ErrOut, ...)`

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

Type for propagating non-zero container exit codes through Cobra's error chain. Allows deferred cleanup (terminal restore, container removal) to run before `os.Exit()`.

```go
type ExitError struct { Code int }
func (e *ExitError) Error() string // "exit status <N>"
```

Commands return `&ExitError{Code: status}` instead of calling `os.Exit()` directly. The root command's `Execute()` checks for `ExitError` and calls `os.Exit(code)` after all defers have run. Critical because `os.Exit()` does **not** run deferred functions.

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

## Tests

- `required_test.go` -- unit tests for argument validators
- `worktree_test.go` -- unit tests for worktree flag parsing

# Image Command Package

Image management parent command.

## Files

| File | Purpose |
|------|---------|
| `image.go` | `NewCmdImage(f)` — parent command |
| `build/build.go` | `NewCmdBuild(f, runF)` — build project image |
| `list/list.go` | `NewCmdList(f, runF)` — list clawker images |
| `prune/prune.go` | `NewCmdPrune(f, runF)` — remove unused images |
| `remove/remove.go` | `NewCmdRemove(f, runF)` — remove specific images |

## Subcommands

- `image build` — build project image
- `image inspect` — inspect image details
- `image list` / `image ls` — list clawker images
- `image prune` — remove unused images
- `image remove` / `image rm` — remove specific images

## Key Symbols

```go
func NewCmdImage(f *cmdutil.Factory) *cobra.Command
```

Parent command only (no RunE). Aggregates subcommands from dedicated packages.

## Build Subcommand (`build/`)

```go
type BuildOptions struct {
    IOStreams      *iostreams.IOStreams
    TUI            *tui.TUI
    Config         func() (config.Config, error)
    Logger         func() (*logger.Logger, error)
    Client         func(context.Context) (*docker.Client, error)
    ProjectManager func() (project.ProjectManager, error)

    File      string   // -f, --file
    Tags      []string // -t, --tag
    NoCache   bool     // --no-cache
    Pull      bool     // --pull
    BuildArgs []string // --build-arg KEY=VALUE
    Labels    []string // --label KEY=VALUE
    Target    string   // --target
    Quiet     bool     // -q, --quiet
    Progress  string   // --progress
    Network   string   // --network
}
func NewCmdBuild(f *cmdutil.Factory, runF func(context.Context, *BuildOptions) error) *cobra.Command
```

Uses **live-display** output scenario: `BuildOptions` captures `IOStreams` and `TUI` from Factory plus lazy closures for `Config`, `Logger`, `Client`, and `ProjectManager`. Build progress is rendered via `opts.TUI.RunProgress(opts.Progress, cfg, ch)` — BubbleTea tree in TTY, plain text otherwise. BuildKit progress events flow through a `buildOpts.OnProgress` callback that forwards `whail.BuildProgressEvent` → `tui.ProgressStep` on a `chan tui.ProgressStep`. The builder runs in a goroutine; channel closure signals done. When `--quiet` or `--progress=none`, output is suppressed and `builder.Build` runs synchronously with no progress channel. Before building, the command calls `docker.BuildKitEnabled` and emits a warning if BuildKit is unavailable (cache mount directives are silently ignored in legacy mode).

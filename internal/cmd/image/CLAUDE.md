# Image Command Package

Image management parent command.

## Files

| File | Purpose |
|------|---------|
| `image.go` | `NewCmdImage(f)` — parent command |
| `build/build.go` | `NewCmdBuild(f, runF)` — build project image |
| `inspect/inspect.go` | `NewCmdInspect(f, runF)` — inspect image details |
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
    IOStreams       *iostreams.IOStreams
    TUI             *tui.TUI
    Config          func() (config.Config, error)
    Logger          func() (*logger.Logger, error)
    Client          func(context.Context) (*docker.Client, error)
    ProjectManager  func() (project.ProjectManager, error)
    ProjectRegistry func() (*project.Registry, error)
    HttpClient      func() (*http.Client, error)
    BundleManager   func() (*bundle.Manager, error)

    Tags      []string // -t, --tag (multiple allowed)
    NoCache   bool     // --no-cache
    Pull      bool     // --pull
    BuildArgs []string // --build-arg KEY=VALUE
    Labels    []string // --label KEY=VALUE (user labels)
    Target    string   // --target
    Quiet     bool     // -q, --quiet
    Progress  string   // --progress (output formatting)
    Network   string   // --network
    IIDFile   string   // --iidfile (write built image ID/digest to file)
}
func NewCmdBuild(f *cmdutil.Factory, runF func(context.Context, *BuildOptions) error) *cobra.Command
```

The run function opens with `cmdutil.RunBundleAutoUpdate(ctx, opts.BundleManager, ios)`
— the opt-in bundle auto-update hook (warn-and-proceed, never blocks the build).

Uses **live-display** output scenario: `BuildOptions` captures `IOStreams` and `TUI` from Factory plus lazy closures for `Config`, `Logger`, `Client`, `ProjectManager`, and `HttpClient`. Build progress is rendered via `opts.TUI.RunProgress(opts.Progress, cfg, ch)` — BubbleTea tree in TTY, plain text otherwise. BuildKit progress events flow through a `buildOpts.OnProgress` callback that forwards `whail.BuildProgressEvent` → `tui.ProgressStep` on a `chan tui.ProgressStep`. The builder runs in a goroutine; channel closure signals done. When `--quiet` or `--progress=none`, output is suppressed and `builder.Build` runs synchronously with no progress channel. Before building, the command calls `docker.BuildKitEnabled` and emits a warning if BuildKit is unavailable (cache mount directives are silently ignored in legacy mode). HttpClient is used at the start of every build to resolve @anthropic-ai/claude-code's latest dist-tag against the npm registry; the resolved version is baked into the rendered Dockerfile's ARG CLAUDE_CODE_VERSION default. Resolution failure is non-fatal — a warning prints and the "latest" literal is used. IIDFile, when set, writes the built image digest to the named file after a successful build.

## Inspect Subcommand (`inspect/`)

```go
type InspectOptions struct {
    IOStreams *iostreams.IOStreams
    Client    func(context.Context) (*docker.Client, error)

    Images []string // positional args (minimum 1)
}
func NewCmdInspect(f *cmdutil.Factory, runF func(context.Context, *InspectOptions) error) *cobra.Command
```

Calls `client.ImageInspect` for each named image and JSON-encodes results to `ios.Out` (indented, array). Errors per image are collected and reported via `cmdutil.HandleError`; partial success (some images found, some not) returns a final error listing the count.

# Clawker Architecture: Factory Pattern (DI Container)

## Three-Layer Architecture

Clawker follows the gh CLI's three-layer Factory pattern for dependency injection:

```
┌─────────────────────────────────────────────────────────────────────────┐
│  Layer 1: WIRING / ASSEMBLY                                             │
│  internal/cmd/factory/default.go                                        │
│                                                                         │
│  func New(version, commit string) *cmdutil.Factory                      │
│    → Creates IOStreams with TTY detection                                │
│    → Wires sync.Once closures for Docker client, config, settings,      │
│      registry, resolution, host proxy, prompter                         │
│    → Cross-dependency order: Registry → Resolution → Config/Settings    │
│                                                                         │
│  Knows about EVERYTHING. Imports all heavy packages.                    │
│  Called exactly ONCE at entry point (internal/clawker/cmd.go).          │
│  Tests NEVER import this package.                                       │
├─────────────────────────────────────────────────────────────────────────┤
│  Layer 2: SHARED TOOLKIT                                                │
│  internal/cmdutil/                                                      │
│                                                                         │
│  factory.go ─── Factory struct       Pure struct with closure fields    │
│                                      "What dependencies exist"          │
│                                      No methods, no construction logic  │
│                                                                         │
│  output.go ──── HandleError()        Error formatting utilities         │
│                 PrintNextSteps()                                        │
│                 ExitError             Container exit code propagation    │
│                                                                         │
│  resolve.go ─── ResolveImage()       Image/container name resolution    │
│                 ResolveContainerName()                                   │
│                                                                         │
│  register.go ── RegisterProject()    Project registration helper        │
│  required.go ── AgentArgsValidator   Cobra args validation              │
│  image_build.go FlavorToImage()      Image building utilities           │
│                                                                         │
│  Importable by everything without cycles.                               │
├─────────────────────────────────────────────────────────────────────────┤
│  Layer 3: COMMANDS                                                      │
│  internal/cmd/<group>/<subcommand>/                                     │
│                                                                         │
│  NewCmdFoo(f *cmdutil.Factory) *cobra.Command {                         │
│      opts := &FooOptions{                                               │
│          IOStreams: f.IOStreams,      ← cherry-pick from Factory         │
│          Client:   f.Client,         ← cherry-pick closure              │
│          Config:   f.Config,         ← cherry-pick closure              │
│      }                                                                  │
│      cmd := &cobra.Command{RunE: func() { fooRun(opts) }}              │
│  }                                                                      │
│                                                                         │
│  fooRun(opts *FooOptions) error {                                       │
│      // NEVER sees Factory — only its own Options                       │
│  }                                                                      │
└─────────────────────────────────────────────────────────────────────────┘
```

## Factory Struct Design

Factory is a **pure data struct** with closure fields — no methods.

```go
type Factory struct {
    // Value fields (set directly)
    WorkDir, BuildOutputDir string
    Debug                   bool
    Version, Commit         string
    IOStreams               *iostreams.IOStreams

    // Closure fields (wired by factory constructor, lazy internally)
    Client      func(context.Context) (*docker.Client, error)
    CloseClient func()
    Config      func() (*config.Config, error)
    ResetConfig func()
    // ... etc (16 closure fields total)
}
```

### Why Closure Fields, Not Methods
- **Testability**: Tests construct `&cmdutil.Factory{Client: mockFn}` — set only what's needed
- **Decoupling**: cmdutil doesn't contain construction logic; factory/ does
- **Transparent**: `f.Client(ctx)` syntax is identical for methods and closure fields
- **Assignable**: `opts.Client = f.Client` works naturally for Options injection

## Command Options Pattern

Every command declares its dependencies explicitly:

```go
type RunOptions struct {
    // From Factory (assigned in NewCmd constructor)
    IOStreams  *iostreams.IOStreams
    Client    func(context.Context) (*docker.Client, error)
    Config    func() (*config.Config, error)
    Prompter  func() *prompts.Prompter

    // From flags (bound by Cobra)
    Agent   string
    Mode    string
    Image   string

    // Test injection points
    // (production defaults set in constructor)
}
```

**Rule**: Run functions accept `*Options` only, never `*Factory`.

## Testing Pattern

```go
// Command registration tests — minimal Factory, no factory package import
ios, _, _, _ := iostreams.Test()
f := &cmdutil.Factory{
    Version:  "1.0.0",
    Commit:   "abc123",
    IOStreams: ios,
}
cmd := NewCmdFoo(f)
// Assert: subcommands registered, flags present

// Business logic tests — mock specific closures
f := &cmdutil.Factory{
    IOStreams: ios,
    Client: func(ctx context.Context) (*docker.Client, error) {
        return mockClient, nil
    },
    Config: func() (*config.Config, error) {
        return testConfig, nil
    },
}
```

## Dependency Wiring Order (in factory/default.go)

```
Level 0: IOStreams (no deps)
Level 1: RegistryLoader, Registry (filesystem only)
Level 2: Resolution (depends on Registry + WorkDir)
Level 3: ConfigLoader, Config (depends on Resolution + WorkDir)
Level 4: SettingsLoader, Settings (depends on Resolution)
Level 5: Client (depends on nothing — lazy Docker connection)
Level 6: HostProxy, EnsureHostProxy, StopHostProxy, HostProxyEnvVar
Level 7: Prompter (depends on IOStreams)
```

Cross-dependencies: Resolution reads Registry. Config reads Resolution for project root. Settings reads Resolution for project settings path.

## Command Pattern Standard

**Every command MUST follow this 4-step pattern:**

### Step 1: Declare Options struct
```go
type SomeOptions struct {
    // Factory dependencies (cherry-picked in constructor)
    IOStreams  *iostreams.IOStreams
    Client    func(context.Context) (*docker.Client, error)
    Config    func() (*config.Config, error)

    // Flag-bound values (populated by Cobra)
    Agent  string
    Force  bool

    // Test injection points (production defaults set in constructor)
    Now func() time.Time
}
```

### Step 2: NewCmd accepts Factory + runF test hook
```go
func NewCmdSome(f *cmdutil.Factory, runF func(*SomeOptions) error) *cobra.Command {
```

### Step 3: Immediately populate Options from Factory
```go
    opts := &SomeOptions{
        IOStreams: f.IOStreams,
        Client:   f.Client,
        Config:   f.Config,
        Now:      time.Now,  // production default for test injection
    }
```

### Step 4: RunE dispatches to runF or real run function
```go
    cmd := &cobra.Command{
        Use: "some",
        RunE: func(cmd *cobra.Command, args []string) error {
            // Validation
            if opts.Agent == "" {
                return cmdutil.FlagErrorf("--agent is required")
            }
            // Dispatch
            if runF != nil {
                return runF(opts)
            }
            return someRun(opts)
        },
    }
```

### Run function receives ONLY Options
```go
func someRun(opts *SomeOptions) error {
    client, err := opts.Client(context.Background())
    if err != nil {
        return err
    }
    // Business logic using only opts fields
}
```

### Testing with runF
```go
func TestNewCmdSome_FlagParsing(t *testing.T) {
    ios, _, _, _ := iostreams.Test()
    f := &cmdutil.Factory{IOStreams: ios}
    var gotOpts *SomeOptions
    cmd := NewCmdSome(f, func(opts *SomeOptions) error {
        gotOpts = opts
        return nil
    })
    cmd.SetArgs([]string{"--agent", "ralph"})
    cmd.Execute()
    assert.Equal(t, "ralph", gotOpts.Agent)
}
```

## Command Compliance Status

### Already follows Options pattern (needs runF addition):
- `internal/cmd/container/run/`
- `internal/cmd/container/create/`
- `internal/cmd/container/start/`
- `internal/cmd/container/stop/`
- `internal/cmd/container/pause/`
- `internal/cmd/container/unpause/`
- `internal/cmd/container/restart/`
- `internal/cmd/container/kill/`
- `internal/cmd/container/remove/`
- `internal/cmd/container/stats/`
- `internal/cmd/container/cp/`
- `internal/cmd/container/exec/`
- `internal/cmd/container/attach/`
- `internal/cmd/container/wait/`
- `internal/cmd/container/rename/`
- `internal/cmd/container/logs/`
- `internal/cmd/container/inspect/`
- `internal/cmd/container/top/`
- `internal/cmd/container/update/`
- `internal/cmd/ralph/run.go`
- `internal/cmd/project/init/`
- `internal/cmd/project/register/`
- `internal/cmd/init/`

### Uses direct Factory calls (needs full refactor to Options + runF):
- `internal/cmd/image/build/`
- `internal/cmd/image/list/`
- `internal/cmd/image/inspect/`
- `internal/cmd/image/prune/`
- `internal/cmd/image/remove/`
- `internal/cmd/volume/create/`
- `internal/cmd/volume/list/`
- `internal/cmd/volume/inspect/`
- `internal/cmd/volume/prune/`
- `internal/cmd/volume/remove/`
- `internal/cmd/network/create/`
- `internal/cmd/network/list/`
- `internal/cmd/network/inspect/`
- `internal/cmd/network/prune/`
- `internal/cmd/network/remove/`
- `internal/cmd/monitor/up.go`
- `internal/cmd/config/`

### Needs investigation:
- `internal/cmd/ralph/status.go`
- `internal/cmd/ralph/reset.go`
- `internal/cmd/ralph/tui.go`
- `internal/cmd/generate/`

## Anti-Patterns to Avoid

1. **Run function depending on Factory** — always use Options struct only
2. **Calling closures during construction** — defeats lazy initialization
3. **Tests importing factory package** — construct minimal `&cmdutil.Factory{}`
4. **Mutating Factory closures at runtime** — closures are set once in constructor
5. **Adding methods to Factory** — use closure fields for all dependency providers
6. **Skipping runF parameter** — every NewCmd MUST accept runF even if not yet tested
7. **Direct Factory method calls in run functions** — extract to Options first

## Refactoring Roadmap

### Phase 1: Factory Separation (this PR)
- Move constructor to `internal/cmd/factory/default.go`
- Convert Factory methods to closure fields
- Update tests to use minimal struct literals

### Phase 2: Add runF to Existing Options Commands
- Add `runF func(*XxxOptions) error` parameter to all NewCmd functions that already use Options
- Update RunE to dispatch via runF
- Update parent command registrations: `NewCmdFoo(f, nil)` for production

### Phase 3: Refactor Direct-Call Commands to Options Pattern
- Image, Volume, Network, Monitor, Config commands
- Create Options struct, extract Factory deps, add runF
- Write flag-parsing tests using runF capture

### Phase 4: Lightweight cmdutil (follow-up)
- Define interfaces for heavy types (DockerClient, ConfigProvider)
- Move resolve.go, register.go, image_build.go out of cmdutil
- cmdutil imports only interfaces and stdlib

## Future Work

- Extract interfaces for heavy types to make cmdutil fully lightweight
- Move resolve.go, register.go, image_build.go out of cmdutil
- Define cmdutil-local interfaces so Factory doesn't import config/docker/hostproxy/prompts
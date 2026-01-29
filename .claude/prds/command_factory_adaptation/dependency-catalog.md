# Dependency Catalog: Command Factory Pattern

> Generated for github.com/cli/cli/v2 command factory dependency injection system
> Feature Scope: Command Factory pattern - dependency injection for cobra CLI commands
> Entry Points: pkg/cmdutil/factory.go, pkg/cmd/factory/default.go, example consumer: pkg/cmd/pr/list/list.go

## Executive Summary

The GitHub CLI command factory pattern uses **dependency injection** to wire together components needed for cobra commands. The factory (`cmdutil.Factory` struct) centralizes configuration of 13+ dependencies and provides them to commands through function closures and interface injection. This architecture enables:

- **Loose coupling** between commands and their dependencies
- **Testability** through mock injection
- **Consistency** across all CLI commands
- **Lazy initialization** via function closures (HttpClient, Config, Remotes, etc.)

**Total Direct Dependencies**: 4 external framework/library packages + 8 internal packages
**Lock File**: go.mod/go.sum present
**Go Version**: 1.25.5

---

## Dependency Summary

```
External CLI Framework Dependencies: 2
  - github.com/spf13/cobra (v1.10.2)
  - github.com/spf13/pflag (v1.0.10)

External GitHub/API Integration: 2
  - github.com/cli/go-gh/v2 (v2.13.0)
  - github.com/cli/oauth (v1.2.1)

Internal Core Packages: 8
  - pkg/cmdutil (Factory definition)
  - api (GitHub API client)
  - internal/browser (Browser abstraction)
  - internal/config (Configuration management)
  - git (Git operations wrapper)
  - pkg/iostreams (Terminal I/O handling)
  - internal/prompter (User interaction)
  - context (Remote/repo resolution)

Internal Support Packages: 4
  - internal/featuredetection (GitHub feature detection)
  - pkg/extensions (Extension management)
  - internal/ghrepo (Repository abstraction)
  - internal/gh (Config wrapper interface)

Testing Dependencies: 1
  - github.com/stretchr/testify (v1.11.1)
```

---

## Core Dependencies

### cmdutil.Factory (Internal)

**Package**: `pkg/cmdutil`
**Definition Location**: `/Users/andrew/Code/vendor/github/cli/pkg/cmdutil/factory.go`
**Category**: Core Infrastructure
**Purpose**: Central dependency injection container for all CLI commands

**Struct Members** (the wireable dependencies):

```go
type Factory struct {
    // Metadata
    AppVersion     string
    ExecutableName string

    // Direct Dependencies (singletons/immutable)
    Browser          browser.Browser
    ExtensionManager extensions.ExtensionManager
    GitClient        *git.Client
    IOStreams        *iostreams.IOStreams
    Prompter         prompter.Prompter

    // Lazy-initialized Dependencies (function closures)
    BaseRepo        func() (ghrepo.Interface, error)
    Branch          func() (string, error)
    Config          func() (gh.Config, error)
    HttpClient      func() (*http.Client, error)
    PlainHttpClient func() (*http.Client, error)
    Remotes         func() (context.Remotes, error)
}
```

**Features Used**:
- Injected into every command via `NewCmd*` factory functions
- Commands read dependencies from factory via struct fields
- Lazy evaluation for expensive operations (HTTP, config parsing)
- Thread-safe caching via closures in `factory/default.go`

**Replaceability**: **Difficult** - This is the core orchestration point. Replacement would require:
- Reimplementing dependency graph wiring
- Maintaining same injection interface
- Ensuring all command factories still work

**Alternatives**:
- **Wire (Google's code generator)**: Compile-time DI
- **fx (Uber's DI framework)**: Reflection-based DI with lifecycle management
- **Manual wiring in main()**: Simpler but less elegant than current approach
- **Service locator pattern**: Less testable than current approach

**License**: (GitHub CLI main license)

---

### github.com/spf13/cobra

**Version**: 1.10.2
**Category**: CLI Framework
**Purpose**: Command structure, argument parsing, help generation, subcommand organization

**Features Used**:
- `*cobra.Command`: Represents each CLI command (pr, issue, repo, etc.)
- Command builders: `Use`, `Short`, `Long`, `Example`, `RunE`
- Flag management: `cmd.Flags()`, `PersistentFlags()`
- Subcommand nesting: Commands have parent-child relationships
- Argument validation: `Args` field (e.g., `cmdutil.NoArgsQuoteReminder`)
- Help generation: Auto-generated from Long, Example fields

**Code Example** (from `pkg/cmd/pr/list/list.go`):
```go
func NewCmdList(f *cmdutil.Factory, runF func(*ListOptions) error) *cobra.Command {
    opts := &ListOptions{
        IO:         f.IOStreams,
        HttpClient: f.HttpClient,
        Browser:    f.Browser,
        Now:        time.Now,
    }

    cmd := &cobra.Command{
        Use:   "list",
        Short: "List pull requests in a repository",
        RunE: func(cmd *cobra.Command, args []string) error {
            opts.BaseRepo = f.BaseRepo
            return runF(opts)
        },
    }

    // Flags are registered on cmd.Flags()
    cmd.Flags().StringVar(&opts.State, "state", "open", ...)

    return cmd
}
```

**Replaceability**: **Very Difficult** - Would require:
- Rewriting every command handler
- Different argument/flag parsing mechanism
- Different subcommand orchestration

**Alternatives**:
- **urfave/cli**: Simpler, less featureful
- **kingpin**: Go-idiomatic but less active
- **alecthomas/kong**: Struct-based parsing
- **python click**: If language change (not viable)

**License**: Apache 2.0

---

### github.com/spf13/pflag

**Version**: 1.0.10
**Category**: CLI Utilities
**Purpose**: POSIX-compatible command-line flag parsing, used by cobra

**Features Used**:
- Flag type definitions: `StringVar`, `IntVar`, `BoolVar`, etc.
- Flag registration with cobra: `cmd.Flags().StringVar(...)`
- Short and long flags: `-f` and `--flag`
- Flag validation and defaults

**Replaceability**: **Very Difficult** - Tightly integrated with cobra

**Alternatives**: Implied by cobra alternatives

**License**: BSD 3-Clause

---

### github.com/cli/go-gh/v2

**Version**: 2.13.0
**Category**: GitHub API Integration
**Purpose**: High-level GitHub API client, GraphQL interface, token management

**Features Used**:
- GraphQL query interface (wrapped by `api/client.go`)
- Color support detection: `pkg/x/color.IsAccessibleColorsEnabled()` (used in `factory/default.go:347`)
- Underlying HTTP transport for authenticated requests
- Token resolution from gh_token environment

**Key Dependencies**:
- Used transitively via `api.NewHTTPClient()` and `api.NewClientFromHTTP()`
- Provides foundation for `api/client.go` which wraps it

**Code Example** (from `factory/default.go:23`):
```go
import (
    xcolor "github.com/cli/go-gh/v2/pkg/x/color"
)
// Used in ioStreams function:
io.SetAccessibleColorsEnabled(xcolor.IsAccessibleColorsEnabled())
```

**Replaceability**: **Moderate** - Could be replaced with:
- Direct use of `google.golang.org/genproto` and `grpc` for GraphQL
- REST-only approach (loses GraphQL efficiency)
- Another GitHub API client library

**Alternatives**:
- **go-github (google/go-github)**: REST-only, mature
- **octokit.go**: REST with some GraphQL support
- **gql (99designs/gql)**: Generic GraphQL client
- **shurcooL/graphql**: Lightweight GraphQL

**License**: MIT

---

### github.com/cli/oauth

**Version**: 1.2.1
**Category**: Authentication
**Purpose**: OAuth 2.0 device flow implementation for GitHub login

**Features Used**:
- OAuth flow initialization and token handling
- Used by config/auth during `gh auth login`
- Manages authentication state

**Code Integration**:
- Referenced in `internal/config/config.go` during authentication
- Integrated with keyring for secure token storage

**Replaceability**: **Difficult** - Would require reimplementing OAuth device flow

**Alternatives**:
- **golang.org/x/oauth2**: Generic OAuth2 client
- **markbates/goth**: Multi-provider OAuth
- Manual OAuth implementation (not recommended)

**License**: MIT

---

## Internal Core Packages

### api (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/api/`
**Category**: GitHub API Client Wrapper
**Purpose**: Wraps go-gh and provides HTTP client factory with auth, logging, error handling

**Key Components**:
- `api.Client`: Main GraphQL/REST client wrapper
- `api.NewHTTPClient()`: Factory creates authenticated HTTP client
- `api.HTTPClientOptions`: Configuration for client behavior
- `api.ExtractHeader()`: Middleware to extract SSO headers
- `api.NewCachedHTTPClient()`: Caches responses for performance

**Features Used** (from `factory/default.go`):
```go
func httpClientFunc(f *cmdutil.Factory, appVersion string) func() (*http.Client, error) {
    return func() (*http.Client, error) {
        opts := api.HTTPClientOptions{
            Config:      cfg.Authentication(),  // Auth credentials
            Log:         io.ErrOut,             // Error logging
            LogColorize: io.ColorEnabled(),     // Colored output
            AppVersion:  appVersion,            // User-Agent header
        }
        client, err := api.NewHTTPClient(opts)
        if err != nil {
            return nil, err
        }
        client.Transport = api.ExtractHeader("X-GitHub-SSO", &ssoHeader)(client.Transport)
        return client, nil
    }
}
```

**Replaceability**: **Moderate** - Internal package, can be refactored

**Dependencies**: `go-gh/v2`, `net/http`

---

### pkg/cmdutil (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/pkg/cmdutil/`
**Category**: Command Utilities
**Purpose**: Provides `Factory` struct and command helper utilities

**Key Components**:
- `Factory` struct: Dependency injection container
- `DetermineEditor()`: Resolves text editor from config
- `Exporter`: Interface for JSON/CSV export
- `NoArgsQuoteReminder`: Argument validator
- `FlagErrorf()`: Error formatting for flag issues

**Replaceability**: **Difficult** - Core infrastructure

---

### pkg/cmd/factory/default.go (Internal)

**Location**: `/Users/andrew/Code/vendor/github/cli/pkg/cmd/factory/default.go`
**Category**: Dependency Wiring
**Purpose**: Initializes Factory with all dependencies, manages initialization order and caching

**Initialization Order** (lines 29-48):

```go
func New(appVersion string) *cmdutil.Factory {
    f := &cmdutil.Factory{
        AppVersion:     appVersion,
        Config:         configFunc(),           // ← First: cached config
        ExecutableName: "gh",
    }

    f.IOStreams = ioStreams(f)                   // ← Depends: Config
    f.HttpClient = httpClientFunc(f, appVersion)  // ← Depends: Config, IOStreams
    f.PlainHttpClient = plainHttpClientFunc(...)  // ← Depends: IOStreams
    f.GitClient = newGitClient(f)                 // ← Depends: IOStreams
    f.Remotes = remotesFunc(f)                   // ← Depends: Config, GitClient
    f.BaseRepo = BaseRepoFunc(f)                 // ← Depends: Remotes
    f.Prompter = newPrompter(f)                  // ← Depends: Config, IOStreams
    f.Browser = newBrowser(f)                    // ← Depends: Config, IOStreams
    f.ExtensionManager = extensionManager(f)    // ← Depends: Config, HttpClient, IOStreams
    f.Branch = branchFunc(f)                     // ← Depends: GitClient

    return f
}
```

**Key Functions**:
- `configFunc()`: Lazy-loaded, cached configuration
- `ioStreams()`: Sets up terminal capabilities from config
- `httpClientFunc()`: Creates authenticated HTTP client
- `remotesFunc()`: Resolves git remotes with filtering
- `BaseRepoFunc()`: Selects primary repository from remotes

**Replaceability**: **Easy** - Can be refactored/rewritten without affecting external API

---

### internal/config (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/internal/config/`
**Category**: Configuration Management
**Purpose**: Loads, caches, and provides access to gh config files

**Key Components**:
- `NewConfig()`: Loads config from `~/.config/gh/config.yml`
- `Authentication()`: OAuth token and host configuration
- `Hosts()`: Configured GitHub instances
- `Editor()`: Default text editor
- `Pager()`: Default pager command
- `Prompt()`: Prompt settings (disabled, enabled, etc.)
- `AccessiblePrompter()`: Accessibility settings
- `Browser()`: Default browser
- `ColorLabels()`: Enable/disable label colors

**Caching Strategy** (from `factory/default.go:252-261`):
```go
func configFunc() func() (gh.Config, error) {
    var cachedConfig gh.Config
    var configError error
    return func() (gh.Config, error) {
        if cachedConfig != nil || configError != nil {
            return cachedConfig, configError
        }
        cachedConfig, configError = config.NewConfig()
        return cachedConfig, configError
    }
}
```

**Replaceability**: **Moderate** - Interface-based, can swap implementations

**External Dependencies**: `gopkg.in/yaml.v3`, `zalando/go-keyring` (for token storage)

---

### git (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/git/`
**Category**: Git Operations Wrapper
**Purpose**: Wraps git CLI calls, provides repository info and branch operations

**Key Components**:
- `Client`: Wraps git CLI execution
- `Remotes()`: Lists git remotes from current directory
- `CurrentBranch()`: Gets current branch name
- `ShowRefs()`: Lists branches/tags
- `Config()`: Gets git config values
- `Commits()`, `LastCommit()`: Commit querying
- `PushDefault()`: Git push strategy

**Initialization** (from `factory/default.go:229-239`):
```go
func newGitClient(f *cmdutil.Factory) *git.Client {
    io := f.IOStreams
    ghPath := f.Executable()
    client := &git.Client{
        GhPath: ghPath,  // Path to gh binary (for credential helper)
        Stderr: io.ErrOut,
        Stdin:  io.In,
        Stdout: io.Out,
    }
    return client
}
```

**Replaceability**: **Moderate** - Could use go-git library instead of CLI

**Alternatives**:
- **go-git (go-git/go-git)**: Pure Go implementation
- **git2go (libgit2 bindings)**: C library bindings
- **Direct git CLI**: Current implementation

---

### pkg/iostreams (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/pkg/iostreams/`
**Category**: Terminal I/O Abstraction
**Purpose**: Manages stdin/stdout/stderr, terminal capabilities, paging, progress indicators

**Key Features**:
- Terminal detection: TTY, color support (256 colors, true color)
- Pager management: `StartPager()`, `StopPager()`
- Progress indicators: spinners and progress bars
- Alternate screen buffer for TUI
- Color scheme detection
- Accessibility features: accessible prompter, accessible colors

**Configuration** (from `factory/default.go:293-350`):
```go
func ioStreams(f *cmdutil.Factory) *iostreams.IOStreams {
    io := iostreams.System()  // Detect system capabilities
    cfg, err := f.Config()

    // Apply configuration from gh config
    if _, ghPromptDisabled := os.LookupEnv("GH_PROMPT_DISABLED"); ghPromptDisabled {
        io.SetNeverPrompt(true)
    }
    if prompt := cfg.Prompt(""); prompt.Value == "disabled" {
        io.SetNeverPrompt(true)
    }

    // Similar for: spinner, accessible prompter, pager, color labels

    return io
}
```

**Replaceability**: **Moderate** - Can be replaced with simpler implementation, but many features

**External Dependencies**: `mattn/go-isatty`, `mattn/go-colorable`, charmbracelet libraries

---

### internal/prompter (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/internal/prompter/`
**Category**: User Interaction
**Purpose**: Prompts for user input with two backends: survey-based or accessible forms

**Key Methods** (interface):
- `Select()`: Multiple choice prompt
- `MultiSelect()`: Multiple selection
- `Input()`: Text input
- `Password()`: Masked password input
- `Confirm()`: Yes/No prompt
- `MarkdownEditor()`: Editable markdown text

**Initialization** (from `factory/default.go:246-250`):
```go
func newPrompter(f *cmdutil.Factory) prompter.Prompter {
    editor, _ := cmdutil.DetermineEditor(f.Config)
    io := f.IOStreams
    return prompter.New(editor, io)
}
```

**Two Implementations**:
- `surveyPrompter`: Uses github.com/AlecAivazis/survey/v2 (full-featured TUI)
- `accessiblePrompter`: Uses charmbracelet/huh (accessible forms)

**Replaceability**: **Moderate** - Interface-based, alternative implementations possible

**External Dependencies**: `survey/v2`, `charmbracelet/huh`

---

### internal/browser (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/internal/browser/`
**Category**: Browser Integration
**Purpose**: Opens URLs in user's default browser

**Key Features**:
- Platform-specific browser detection
- Fallback to environment variable `BROWSER`
- URL opening with `open` (macOS), `xdg-open` (Linux), `start` (Windows)

**Initialization** (from `factory/default.go:241-244`):
```go
func newBrowser(f *cmdutil.Factory) browser.Browser {
    io := f.IOStreams
    return browser.New("", io.Out, io.ErrOut)
}
```

**Replaceability**: **Easy** - Simple interface

**Alternatives**:
- **skratchdot/open-golang**: Alternative implementation
- **pkg/browser (go-gh)**: Might have browser support
- Custom implementation

---

### context (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/context/`
**Category**: Repository Resolution
**Purpose**: Resolves git remotes to repositories, handles fork relationships

**Key Components**:
- `Remotes`: List of configured git remotes
- `ResolveRemotesToRepos()`: Maps remotes to repository objects using GitHub API
- `SmartBaseRepoFunc()`: Intelligent base repo selection for forks

**Used By Factory** (from `factory/default.go:179-187`):
```go
func remotesFunc(f *cmdutil.Factory) func() (ghContext.Remotes, error) {
    rr := &remoteResolver{
        readRemotes: func() (git.RemoteSet, error) {
            return f.GitClient.Remotes(context.Background())
        },
        getConfig: f.Config,
    }
    return rr.Resolver()
}
```

**Replaceability**: **Moderate** - Could simplify remote resolution

---

### internal/featuredetection (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/internal/featuredetection/`
**Category**: GitHub Feature Capability Detection
**Purpose**: Detects server capabilities (issue features, PR features, advanced search support)

**Key Features**:
- `IssueFeatures()`: Supported issue search/filter options
- `PullRequestFeatures()`: Supported PR operations
- `SearchFeatures()`: Advanced search syntax support
- `ProjectsV1()`: Determines if server supports Projects v1
- Version-aware: Different features for GHES vs github.com

**Optional Dependency** (not required in basic factory):
- Commands can inject `featuredetection.Detector` in their options struct
- Used to adapt behavior to GitHub server capabilities

**Example Usage** (from `pkg/cmd/pr/list/list.go:28`):
```go
type ListOptions struct {
    Detector   fd.Detector  // Optional feature detector
    // ...
}
```

**Replaceability**: **Easy** - Optional, commands work without it

---

### pkg/extensions (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/pkg/extensions/`
**Category**: Extension Management
**Purpose**: Manages gh extensions (external commands)

**Key Components**:
- `ExtensionManager`: Loads and manages extension commands
- Extension types: git-based, go binary, shell script
- Discovery and execution

**Initialization** (from `factory/default.go:274-291`):
```go
func extensionManager(f *cmdutil.Factory) *extension.Manager {
    em := extension.NewManager(f.IOStreams, f.GitClient)

    cfg, err := f.Config()
    if err != nil {
        return em
    }
    em.SetConfig(cfg)

    client, err := f.HttpClient()
    if err != nil {
        return em
    }

    em.SetClient(api.NewCachedHTTPClient(client, time.Second*30))

    return em
}
```

**Replaceability**: **Easy** - Optional extension system

---

### internal/ghrepo (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/internal/ghrepo/`
**Category**: Repository Abstraction
**Purpose**: Provides `Interface` for repository operations (owner, name, host)

**Key Interface Methods**:
- `RepoOwner()`: Repository owner (username/org)
- `RepoName()`: Repository name
- `RepoHost()`: GitHub host (github.com or GHES)

**Used By**:
- `BaseRepo` function returns `ghrepo.Interface`
- All commands that need repository context use this

**Replaceability**: **Moderate** - Could flatten to concrete struct

---

### internal/gh (Internal Package)

**Location**: `/Users/andrew/Code/vendor/github/cli/internal/gh/`
**Category**: Config Interface
**Purpose**: Config abstraction interface (`gh.Config`)

**Key Interface Methods**:
- `Authentication()`: OAuth token configuration
- `Hosts()`: Configured GitHub instances
- `Default values for settings**

**Replaceability**: **Moderate** - Interface-based abstraction

---

## External Testing Dependencies

### github.com/stretchr/testify

**Version**: 1.11.1
**Category**: Testing Framework
**Purpose**: Test assertions and mocking for unit tests

**Features Used**:
- `assert.Equal()`, `assert.Error()`, `require.NoError()`
- Mock expectations
- Used in `*_test.go` files throughout project

**Replaceability**: **Easy** - Only development dependency

**Alternatives**:
- Built-in `testing` package (more verbose)
- go-cmp for better diffs
- testify alternatives (gomega, etc.)

**License**: MIT

---

## Dependency Relationships & Initialization Graph

### Dependency Graph (Initialization Order)

```
┌─────────────────────────────────────────────────────────────┐
│              cmdutil.Factory Creation                        │
│                 (pkg/cmd/factory/default.go)                │
└─────────────────────────────────────────────────────────────┘

                    ▼ First: No dependencies
            ┌───────────────────────┐
            │ Config (cached)       │
            │ - Loads YAML config   │
            │ - Auth tokens         │
            └───────────────────────┘
                    │
        ┌───────────┴───────────┬──────────────────┐
        ▼                       ▼                  ▼
    ┌─────────┐         ┌────────────┐    ┌──────────────┐
    │IOStreams│         │HttpClient  │    │PlainHttp     │
    │from cfg │         │from Config │    │Client        │
    └─────────┘         │+ IOStreams │    │(no auth)     │
        │               └────────────┘    └──────────────┘
        │
        ├─────┬────────┬──────────┬──────────┐
        ▼     ▼        ▼          ▼          ▼
    ┌──────┐┌───────┐┌────────┐┌──────┐┌──────────┐
    │Git   ││Browser││Prompter││      ││Extension│
    │Client││       ││        ││      ││Manager  │
    └──────┘└───────┘└────────┘└──────┘└──────────┘
        │
        ▼
    ┌──────────┐
    │Remotes   │
    │(git list)│
    └──────────┘
        │
        ▼
    ┌──────────┐
    │BaseRepo  │
    │(first    │
    │ remote)  │
    └──────────┘
        │
        ▼
    ┌──────────┐
    │Branch    │
    │(current) │
    └──────────┘
```

### Dependency Matrix

| Dependency | Config | IOStreams | HttpClient | GitClient | Browser | Prompter | Remotes | BaseRepo | Branch | ExtMgr |
|-----------|--------|-----------|-----------|-----------|---------|----------|---------|----------|--------|--------|
| **Config** | - | ✓ | ✓ | ✗ | ✓ | ✓ | ✗ | ✗ | ✗ | ✓ |
| **IOStreams** | ✓ | - | ✓ | ✓ | ✓ | ✓ | ✗ | ✗ | ✗ | ✓ |
| **HttpClient** | ✓ | ✓ | - | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| **GitClient** | ✗ | ✓ | ✗ | - | ✗ | ✗ | ✓ | ✗ | ✓ | ✓ |
| **Remotes** | ✓ | ✗ | ✗ | ✓ | ✗ | ✗ | - | ✓ | ✗ | ✗ |
| **BaseRepo** | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | - | ✗ | ✗ |

### Lazy Initialization Pattern

The factory uses function closures for expensive operations:

```go
// Lazy: evaluated on-demand
HttpClient func() (*http.Client, error)      // Creates new client each call
Config func() (gh.Config, error)             // Returns cached after first call
BaseRepo func() (ghrepo.Interface, error)    // Queries git each call
Remotes func() (context.Remotes, error)      // Queries git each call
```

This allows:
- **Fast startup**: HTTP client not created until needed
- **Fresh data**: Git remotes queried on each invocation (respects env changes)
- **Caching where needed**: Config cached to avoid re-parsing YAML

---

## How Commands Consume Factory Dependencies

### Pattern: Command Creation

All commands follow this pattern:

```go
// 1. Define options struct with needed dependencies
type ListOptions struct {
    HttpClient func() (*http.Client, error)
    IO         *iostreams.IOStreams
    BaseRepo   func() (ghrepo.Interface, error)
    Browser    browser.Browser
    // ... plus command-specific flags and options
}

// 2. Create cobra command, receive factory
func NewCmdList(f *cmdutil.Factory, runF func(*ListOptions) error) *cobra.Command {
    opts := &ListOptions{
        // Wire factory dependencies into options
        IO:         f.IOStreams,
        HttpClient: f.HttpClient,
        Browser:    f.Browser,
        // ... more wiring
    }

    cmd := &cobra.Command{
        Use: "list",
        RunE: func(cmd *cobra.Command, args []string) error {
            // Override optional dependencies based on flags
            opts.BaseRepo = f.BaseRepo

            // Call implementation with injected dependencies
            return runF(opts)
        },
    }

    // Register flags for command-specific options
    cmd.Flags().StringVar(&opts.State, "state", "open", "Filter by state")

    return cmd
}

// 3. Implementation uses options
func listRun(opts *ListOptions) error {
    repo, err := opts.BaseRepo()  // Lazy-evaluated
    if err != nil {
        return err
    }

    client, err := opts.HttpClient()  // Creates authenticated client
    if err != nil {
        return err
    }

    apiClient := api.NewClientFromHTTP(client)
    // ... make API calls
}
```

### Example: PR List Command

From `/Users/andrew/Code/vendor/github/cli/pkg/cmd/pr/list/list.go`:

```go
type ListOptions struct {
    HttpClient func() (*http.Client, error)     // From factory
    IO         *iostreams.IOStreams             // From factory
    BaseRepo   func() (ghrepo.Interface, error) // From factory
    Browser    browser.Browser                   // From factory
    Detector   fd.Detector                      // Optional, usually nil

    // Command-specific options
    WebMode      bool
    State        string
    BaseBranch   string
    Labels       []string
    Author       string
    Search       string
}

func NewCmdList(f *cmdutil.Factory, runF func(*ListOptions) error) *cobra.Command {
    opts := &ListOptions{
        IO:         f.IOStreams,
        HttpClient: f.HttpClient,
        Browser:    f.Browser,
        Now:        time.Now,
    }

    cmd := &cobra.Command{
        Use:   "list",
        Short: "List pull requests in a repository",
        RunE: func(cmd *cobra.Command, args []string) error {
            opts.BaseRepo = f.BaseRepo

            if opts.LimitResults < 1 {
                return cmdutil.FlagErrorf("invalid value for --limit: %v", opts.LimitResults)
            }

            return runF(opts)
        },
    }

    cmd.Flags().StringVar(&opts.State, "state", "open", ...)
    cmd.Flags().StringVar(&opts.BaseBranch, "base", "", ...)
    cmd.Flags().StringSliceVar(&opts.Labels, "label", nil, ...)

    return cmd
}
```

---

## Key Design Patterns

### 1. Dependency Injection via Struct Fields

```go
type Factory struct {
    HttpClient func() (*http.Client, error)
    Browser    browser.Browser
    // ...
}

type CommandOptions struct {
    HttpClient func() (*http.Client, error)
    Browser    browser.Browser
}

// Injection happens in command creator
opts := &CommandOptions{
    HttpClient: f.HttpClient,  // Dependency passed
    Browser:    f.Browser,
}
```

### 2. Lazy Initialization via Closures

```go
// Factory closure captures dependencies and state
f.HttpClient = httpClientFunc(f, appVersion)

// Inside httpClientFunc:
func httpClientFunc(f *cmdutil.Factory, appVersion string) func() (*http.Client, error) {
    return func() (*http.Client, error) {  // Closure
        io := f.IOStreams                   // Captured from factory
        cfg, err := f.Config()              // Lazy call
        // ... create client ...
        return client, nil
    }
}
```

### 3. Caching Pattern

```go
func configFunc() func() (gh.Config, error) {
    var cachedConfig gh.Config
    var configError error
    return func() (gh.Config, error) {
        if cachedConfig != nil || configError != nil {
            return cachedConfig, configError  // Return cached
        }
        cachedConfig, configError = config.NewConfig()  // Load once
        return cachedConfig, configError
    }
}
```

### 4. Interface-Based Abstractions

```go
// Browser is an interface, not concrete type
type Browser interface {
    Open(url string) error
}

// Factory uses interface
type Factory struct {
    Browser Browser  // Interface, not concrete
}

// Enables easy testing and mocking
```

---

## Adaptation Recommendations

### For Adding New Language Support

**Option A: Direct Port (Recommended)**
- Maintain same Factory pattern
- Create equivalent dependency injection container
- Language-specific clients: GraphQL library, CLI framework

**Language-Specific Stacks**:

#### Python
- CLI Framework: `click` or `typer`
- GitHub API: `pygithub` or `gql` with `requests`
- Terminal: `rich` for TUI/colors
- Prompts: `questionary` or `prompt_toolkit`
- Git: `gitpython` or `subprocess`

```python
# Equivalent Python structure
class Factory:
    def __init__(self, app_version):
        self.app_version = app_version
        self.config = self._make_config()
        self.io_streams = self._make_io_streams()
        self.http_client = self._make_http_client()
        self.git_client = self._make_git_client()
        # ... etc

    def _make_config(self):
        # Cached config loading
        return Config()
```

#### Rust
- CLI Framework: `clap` (similar to cobra)
- GitHub API: `octocrab` or `github-gql`
- Terminal: `crossterm` or `termion`
- Prompts: `dialoguer` or `inquire`
- Git: `git2` or `libgit2-sys`

```rust
// Equivalent Rust structure
pub struct Factory {
    pub app_version: String,
    pub config: Config,
    pub io_streams: IOStreams,
    pub http_client: Rc<Client>,
    // Lazy-init via Rc<RefCell<Option<T>>>
}
```

#### JavaScript/Node
- CLI Framework: `commander.js` or `yargs`
- GitHub API: `@octokit/rest` or `graphql-request`
- Terminal: `chalk`, `ora` for colors/spinners
- Prompts: `inquirer` or `prompts`
- Git: `simple-git`

```javascript
// Equivalent Node structure
class Factory {
    constructor(appVersion) {
        this.appVersion = appVersion;
        this.config = loadConfig();  // Cached
        this.ioStreams = createIOStreams(this);
        this.gitClient = new GitClient();
        // ... etc
    }

    get httpClient() {
        // Lazy initialization
        if (!this._httpClient) {
            this._httpClient = createHTTPClient(this);
        }
        return this._httpClient;
    }
}
```

### Key Patterns to Preserve

1. **Dependency Injection**: Don't create dependencies inline in commands
2. **Lazy Evaluation**: Expensive resources (HTTP, Git) via closures
3. **Caching**: Config and similar immutable resources
4. **Interface Abstraction**: Browser, Prompter, Detector as interfaces
5. **Initialization Graph**: Respect dependency ordering

### Critical Dependencies for Any Port

| Component | Why Critical | Hard to Replace? |
|-----------|-------------|-----------------|
| GitHub API Client | All commands need repo/org data | Very |
| CLI Framework | Core command structure | Very |
| Terminal I/O | Colors, paging, progress | Moderate |
| Git Operations | Most commands inspect git | Moderate |
| Config Loading | Authentication, settings | Easy |
| Prompts | User interaction | Easy |

---

## Risk Areas & Considerations

### 1. Initialization Order Dependencies
**Risk**: Circular dependencies or missing initialization steps
**Mitigation**: Clear dependency graph in `factory/default.go`, tests verify order

### 2. Lazy Evaluation Side Effects
**Risk**: HTTP client creation or git operations can fail silently during factory creation
**Mitigation**: Errors returned at call time, not init time (expected behavior)

### 3. Config Caching
**Risk**: Config changes during execution won't be reflected
**Mitigation**: Config is designed to be immutable during command execution

### 4. Multiple HTTP Clients
**Risk**: `HttpClient` and `PlainHttpClient` both exist, easy to use wrong one
**Mitigation**: Clear naming, documentation on when to use each

### 5. Extension Manager Optional Behavior
**Risk**: ExtensionManager silently degrades if Config or HttpClient fail
**Mitigation**: Commands shouldn't rely on extension manager for core functionality

### 6. Feature Detection Version Differences
**Risk**: Commands assume features available that don't exist on GHES
**Mitigation**: Use `Detector` interface when server version matters

---

## Full Dependency List

| Name | Version | Category | Purpose | Essential |
|------|---------|----------|---------|-----------|
| cobra | 1.10.2 | CLI Framework | Command structure, args, help | **Yes** |
| pflag | 1.0.10 | CLI Framework | Flag parsing | **Yes** |
| go-gh | 2.13.0 | GitHub API | GraphQL, token mgmt | **Yes** |
| cli/oauth | 1.2.1 | Authentication | OAuth device flow | **Yes** |
| survey | 2.3.7 | Prompts | TUI prompts (survey-based) | No* |
| charmbracelet/huh | 0.8.0 | Prompts | Accessible prompts | No* |
| charmbracelet/glamour | 0.10.0 | Formatting | Markdown rendering | No |
| charmbracelet/lipgloss | 1.1.1 | Formatting | Terminal styling | No |
| go-cmp | 0.7.0 | Testing | Deep equality | Dev |
| testify | 1.11.1 | Testing | Assertions & mocks | Dev |
| yaml.v3 | 3.0.1 | Serialization | Config YAML parsing | No |
| go-colorable | 0.1.14 | Terminal | Color output | No |
| go-isatty | 0.0.20 | Terminal | TTY detection | No |
| spinner | 1.23.2 | UI | Progress spinners | No |
| heredoc | 1.0.0 | Utilities | Multi-line strings | No |
| clipboard | 0.1.4 | Utilities | Copy to clipboard | No |
| go-keyring | 0.2.6 | Security | Token storage | No |
| go-shellquote | 0.0.0 | Utilities | Shell argument quoting | No |
| go-version | 1.8.0 | Utilities | Version parsing | No |
| tcell | 2.13.4 | Terminal | TUI (via tview) | No |

*: survey OR huh required depending on accessibility mode

---

## Testing Support

### Testing Patterns Used

Commands are tested by:
1. Creating a mock `Factory` with injected test doubles
2. Using `pkg/iostreams.Test()` for capturing output
3. Using `pkg/httpmock` for stubbing API responses
4. Calling command's `runF` directly with test options

### Example Test Pattern

```go
func TestListRun(t *testing.T) {
    ios, stdin, stdout, stderr := iostreams.Test()

    factory := &cmdutil.Factory{
        IOStreams: ios,
        HttpClient: func() (*http.Client, error) {
            return httpmock.TestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                // Stub response
                w.Write([]byte(`{"data":[]}`))
            })), nil
        },
        // ...other mocks...
    }

    opts := &ListOptions{
        IO:         ios,
        HttpClient: factory.HttpClient,
        // ...
    }

    err := listRun(opts)
    require.NoError(t, err)
    assert.Contains(t, stdout.String(), "expected output")
}
```

---

## Conclusion

The GitHub CLI's command factory pattern provides a robust, testable dependency injection system for cobra CLI commands. The architecture:

- **Centralizes** dependency management in `cmdutil.Factory`
- **Decouples** commands from dependency creation
- **Optimizes** initialization order and caching
- **Enables** comprehensive testing through injection
- **Maintains** consistency across 100+ CLI commands

When adapting to other languages, preserve the core DI pattern while using language-idiomatic libraries for each component (CLI framework, API client, etc.).

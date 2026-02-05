# Command Factory Pattern: Dependencies Summary

## Architecture Overview

The GitHub CLI's command factory implements a **centralized dependency injection** pattern for cobra CLI commands. The `cmdutil.Factory` struct acts as a single source of truth for all command dependencies, enabling loose coupling, testability, and consistency across 100+ CLI commands.

## Key Dependencies

### Core Framework (2 packages)
- **github.com/spf13/cobra** (v1.10.2): CLI framework providing command structure, argument parsing, and subcommand orchestration
- **github.com/spf13/pflag** (v1.0.10): POSIX-compatible flag parsing for command-line arguments

### GitHub Integration (2 packages)
- **github.com/cli/go-gh/v2** (v2.13.0): High-level GitHub API client with GraphQL support and token management
- **github.com/cli/oauth** (v1.2.1): OAuth 2.0 device flow implementation for GitHub authentication

### Internal Core Components (8 packages)
1. **pkg/cmdutil/factory.go**: Factory struct definition with 13+ injectable dependencies
2. **pkg/cmd/factory/default.go**: Dependency initialization and wiring
3. **api**: GitHub API client wrapper with auth, logging, and error handling
4. **git**: Git CLI wrapper providing repository information and branch operations
5. **pkg/iostreams**: Terminal I/O abstraction with color detection, paging, and accessibility
6. **internal/config**: Configuration management for gh settings and authentication
7. **internal/prompter**: User interaction with survey-based or accessible prompt backends
8. **internal/browser**: Cross-platform browser integration

### Optional Components (4 packages)
- **internal/featuredetection**: GitHub server capability detection (for version-aware behavior)
- **pkg/extensions**: Extension system for external commands
- **internal/ghrepo**: Repository abstraction interface
- **context**: Remote-to-repository resolution with fork awareness

## Dependency Wiring Pattern

The factory uses **lazy initialization via closures** for expensive operations:

```
Config (cached)
    ↓
IOStreams ← HttpClient ← PlainHttpClient (no auth)
    ↓
Git, Browser, Prompter, ExtensionManager
    ↓
Remotes (from git)
    ↓
BaseRepo (first remote)
```

### Key Characteristics:
- **Lazy evaluation**: HTTP client and git operations created on-demand
- **Caching**: Config parsed once and cached for command duration
- **Interface-based**: Browser, Prompter, Detector use interfaces (testable)
- **Initialization order**: Respects dependency graph (Config → IOStreams → HttpClient → Git)

## Command Integration Pattern

All commands follow a consistent DI pattern:

1. Define options struct with needed dependencies from Factory
2. Create cobra command that receives Factory
3. Wire Factory fields into options struct
4. Implementation uses options.httpClient(), options.baseRepo(), etc.

This enables testing by injecting mock implementations without modifying command code.

## Replacement Implications

### Easy to Replace (Internal Implementation)
- Config loading mechanism
- Browser integration (already abstracted)
- Prompter backends
- Git CLI wrapper (could use go-git library)

### Difficult to Replace (Core Pattern)
- Cobra CLI framework (requires rewrite of all commands)
- Factory pattern itself (but easily refactored for other languages)
- GitHub API integration (core to all operations)

### Language-Agnostic Design
The Factory pattern is language-agnostic. For other languages:
- **Python**: Use Click + pygithub + questionary
- **Rust**: Use Clap + octocrab + inquire
- **JavaScript**: Use Commander.js + @octokit/rest + inquirer

Preserve the DI container pattern and lazy initialization strategy.

## Statistics

- **Total Go dependencies**: 61 (direct + indirect)
- **Critical framework dependencies**: 4
- **Internal factory-related packages**: 8
- **Optional/enhancement packages**: 4
- **Go version requirement**: 1.25.5+

## Testing Support

Factory enables comprehensive testing through:
- Mock injection without modifying command code
- iostreams.Test() for output capture
- httpmock for API response stubbing
- Interface-based abstraction for Browser, Prompter, Detector

The pattern ensures commands are decoupled from their dependencies and testable in isolation.

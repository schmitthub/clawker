# GitHub CLI Command Factory - Build & Infrastructure Summary

## Overview

The GitHub CLI uses a sophisticated **two-layer build system** that combines platform-agnostic Make targets with cross-platform Go build scripts. This architecture enables reproducible, platform-specific binary releases while maintaining a clean developer workflow.

## Build System Architecture

### Primary Build Flow

1. **Make Layer** (`Makefile`): Defines portable task names that work across Unix/Windows
2. **Go Script Layer** (`script/build.go`): Bootstraps itself as a binary, then orchestrates compilation with platform-specific handling
3. **Cobra Framework**: Registers 30+ top-level commands and subcommands dynamically through a factory pattern

### Key Build Characteristics

- **Single-file distribution**: Ships as standalone binary (no runtime dependencies)
- **Version injection via ldflags**: Version and build date embedded at compile time via `-X` linker flags
- **Reproducible builds**: Supports `SOURCE_DATE_EPOCH` for bit-for-bit identical builds
- **Cross-platform compilation**: GOOS/GOARCH overrides for Linux, macOS, Windows (amd64, arm64, 386)
- **Optimization**: Uses `-trimpath` for reproducibility and removes 30-40ms startup overhead via `TCELL_MINIMIZE`

## Command Factory Pattern

### Factory Initialization

The `pkg/cmd/factory/default.go` implements a **carefully-ordered dependency injection pattern** where each component initializes with its dependencies already resolved:

```
Config (no deps)
  → IOStreams → HttpClient, PlainHttpClient
    → GitClient → Remotes → BaseRepo
    → Branch, Prompter, Browser, ExtensionManager
```

This ensures every command receives a fully-initialized `*cmdutil.Factory` with all required services available.

### Command Registration

All commands are registered in `pkg/cmd/root/root.go` through direct imports and `cmd.AddCommand()` calls. This creates a static, compile-time-verified command tree with no runtime discovery mechanism.

## Development Workflow

### Standard Build Commands

```bash
make bin/gh              # Build platform-specific binary
make test                # Run unit tests
make clean               # Clean artifacts
golangci-lint run        # Lint with golangci-lint v2.6.0
make manpages            # Generate man pages
make completions         # Generate shell completions
```

### Testing

- **Unit tests**: `go test ./...` with race detection available
- **Acceptance tests**: Real GitHub API calls (require credentials)
- **Linting**: 22 active linters (security, style, correctness)
- **Vulnerability scanning**: govulncheck validates Go vulnerability database

## CI/CD Pipeline

### GitHub Actions Workflows

1. **go.yml** - Unit/integration tests on matrix (Ubuntu, Windows, macOS)
2. **lint.yml** - Linting, go.mod validation, license checks, govulncheck
3. **deployment.yml** - Release orchestration with GoReleaser v2.13.1 for all platforms

### Release Distribution

- **Multi-platform**: Linux (.tar.gz, .rpm), macOS (.tar.gz, .dmg), Windows (.zip, .msi)
- **Signatures**: SLSA provenance attestation via Cosign
- **Documentation**: Auto-generated man pages and website docs
- **Channels**: GitHub Releases, Homebrew, Scoop, apt/yum repositories

## Code Quality & Security

### Linting Configuration

- **22 enabled linters**: asasalint, asciicheck, bidichk, bodyclose, copyloopvar, durationcheck, exptostd, fatcontext, gocheckcompilerdirectives, gochecksumtype, gocritic, gomoddirectives, goprintffuncname, govet, ineffassign, nilerr, nolintlint, nosprintfhostport, reassign, unused
- **Disabled**: gosec, staticcheck, errcheck (too many issues - deferred)
- **Go version**: 1.25.5 (locked in go.mod)

### Security Measures

- OAuth credentials optionally injected at build time (never in source)
- Dependency scanning with govulncheck
- License compliance via go-licenses
- Configuration secrets stored in OS keyring

## Module Dependencies

### Key Dependencies

| Category | Purpose | Key Packages |
|----------|---------|--------------|
| CLI Framework | Command parsing | cobra v1.10.2, pflag v1.0.10 |
| GitHub API | API interactions | go-gh/v2 v2.13.0, githubv4 v0.0.0 |
| Terminal UI | Rich output | glamour, huh, lipgloss, tcell |
| User Input | Interactive prompts | survey/v2 v2.3.7 |
| Security | Signing/verification | sigstore-go v1.1.4, in-toto v1.1.2 |
| Testing | Test utilities | testify v1.11.1 |

**61 direct dependencies**, extensively tested for vulnerabilities and license compliance.

## Operational Considerations

### Infrastructure Requirements

- Go 1.25.5 runtime
- golangci-lint v2.6.0
- GoReleaser v2.13.1
- GitHub Actions (already deployed)
- git (for version detection)

### Performance

- **Build time**: ~30s (single binary, no container overhead)
- **Binary size**: ~20-30MB (stripped Go binary)
- **Startup overhead**: <100ms (optimized with TCELL_MINIMIZE)
- **Test coverage**: Unclear (no metrics in accessible files)

### Scaling Characteristics

- **GitHub Actions cost**: Estimated 10-20 min/build × 3 platforms = ~30 min/commit
- **Within free tier**: 5,000 min/month comfortably accommodates current CI/CD
- **Artifact retention**: 7 days (cleanup automated)

## Adaptation Guidance

### For Extending Command Factory

1. **New Commands**: Add to `pkg/cmd/<name>/`, register in root.go
2. **New Dependencies**: Initialize in factory/default.go in dependency order
3. **Testing**: Use test Factory with mocked IOStreams, HttpClient
4. **Documentation**: Auto-generated from cobra help text

### For Modifying Build

1. **Add build tags**: Set GO_BUILDTAGS environment variable
2. **Custom ldflags**: Set GO_LDFLAGS environment variable
3. **Version override**: Set GH_VERSION environment variable
4. **Cross-compile**: Set GOOS/GOARCH environment variables

### For Release Automation

- Releases triggered manually via GitHub UI (deployment.yml workflow dispatch)
- Requires tag name in format v#.#.# (validated by workflow)
- Artifacts automatically signed with Cosign
- Documentation published to cli.github.com

---

**Read the full analysis in `infrastructure.md` for detailed implementation specifics, file listings, and code examples.**

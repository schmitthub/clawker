# Build Package

Dockerfile generation, version management, and build configuration for clawker container images.

## TODO

- [ ] This imports from internal packages; consider refactoring to avoid this.

## Key Files

| File | Purpose |
|------|---------|
| `dockerfile.go` | Dockerfile templates, context generation, project scaffolding |
| `config.go` | Variant configuration (Debian/Alpine) |
| `versions.go` | Claude Code version resolution via npm registry |
| `errors.go` | Error types (`NetworkError`, `RegistryError`, `ErrVersionNotFound`, etc.) |

## Subpackages

| Package | Purpose |
|---------|---------|
| `semver/` | Semantic version parsing and comparison |
| `registry/` | npm registry client for version resolution |

## Dockerfile Generation (`dockerfile.go`)

```go
mgr := build.NewDockerfileManager(outputDir, config)
mgr.GenerateDockerfiles()             // Render all Dockerfiles
mgr.DockerfilesDir() string           // Output directory path

gen := build.NewProjectGenerator(config, workDir)
gen.Generate() error                  // Generate project Dockerfile
gen.GenerateBuildContext() error       // Create build context tar
gen.UseCustomDockerfile() bool         // Check for custom Dockerfile

// Standalone build context helper
build.CreateBuildContextFromDir(dir, dockerfilePath string) (io.Reader, error)  // Create tar from directory for custom Dockerfiles
```

### DockerfileContext

Template data for Dockerfile rendering:

```go
type DockerfileContext struct {
    BaseImage, Username, Shell, WorkspacePath, ClaudeVersion string
    Packages []string
    UID, GID int
    IsAlpine, EnableFirewall bool
    FirewallDomains []string
    Instructions DockerfileInstructions
    Inject DockerfileInject
    ImageLabels map[string]string
}
```

### Embedded Assets

`DockerfileTemplate`, `EntrypointScript`, `FirewallScript`, `StatuslineScript`, `HostOpenScript`, `CallbackForwarderScript`, `GitCredentialScript`, `SSHAgentProxySource` — embedded via `go:embed`.

## Version Management (`versions.go`)

```go
vm := build.NewVersionsManager(fetcher, config)
vm.ResolveVersions(opts) error        // Resolve semver patterns from npm registry

build.LoadVersionsFile(path) error    // Load pinned versions
build.SaveVersionsFile(path) error    // Save resolved versions
```

## Variant Configuration (`config.go`)

```go
cfg := build.DefaultVariantConfig()
cfg.IsAlpine(variant) bool
cfg.VariantNames() []string
```

## Error Types (`errors.go`)

```go
var ErrVersionNotFound error
var ErrInvalidVersion error
var ErrNoVersions error

type NetworkError struct { ... }       // Network-related failures
type RegistryError struct { ... }      // npm registry failures
```

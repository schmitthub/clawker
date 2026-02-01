# Build Package

Image building orchestration, Dockerfile generation, version management, and build configuration for clawker container images.

## Key Files

| File | Purpose |
|------|---------|
| `build.go` | `Builder` — project image building (EnsureImage, Build) |
| `hash.go` | Content-addressed hashing for Dockerfile + includes |
| `defaults.go` | Default image building, flavor selection |
| `dockerfile.go` | Dockerfile templates, context generation, project scaffolding |
| `config.go` | Variant configuration (Debian/Alpine) |
| `versions.go` | Claude Code version resolution via npm registry |
| `errors.go` | Error types (`NetworkError`, `RegistryError`, `ErrVersionNotFound`, etc.) |

## Subpackages

| Package | Purpose |
|---------|---------|
| `semver/` | Semantic version parsing and comparison |
| `registry/` | npm registry client for version resolution |
| `templates/` | Dockerfile template, entrypoint, firewall, and helper scripts |
| `callback-forwarder/` | Callback forwarder Go source |

## Builder (`build.go`)

```go
type Builder struct { client, config, workDir }

func NewBuilder(cli *docker.Client, cfg *config.Config, workDir string) *Builder
func (b *Builder) EnsureImage(ctx context.Context, imageTag string, opts Options) error  // Content-addressed: skips if hash matches, tags :latest
func (b *Builder) Build(ctx context.Context, imageTag string, opts Options) error         // Always build
```

## Build Options (`build.go`)

```go
type Options struct {
    ForceBuild     bool
    NoCache        bool
    Labels         map[string]string
    Target         string
    Pull           bool
    SuppressOutput bool
    NetworkMode    string
    BuildArgs      map[string]*string
    Tags           []string           // Additional tags (merged with imageTag)
}
```

## Content Hashing (`hash.go`)

```go
func ContentHash(dockerfile []byte, includes []string, workDir string) (string, error)
```

SHA-256 of rendered Dockerfile + sorted include file contents. Returns 12-char hex prefix. Used by `EnsureImage` to detect when rebuilds are actually needed. Images are tagged `clawker-<project>:sha-<hash>` with `:latest` aliased to the current hash.

## Default Image Utilities (`defaults.go`)

```go
const DefaultImageTag = "clawker-default:latest"

type FlavorOption struct { Name, Description string }

func DefaultFlavorOptions() []FlavorOption  // bookworm, trixie, alpine3.22, alpine3.23
func FlavorToImage(flavor string) string    // Maps flavor name to base image ref
func BuildDefaultImage(ctx, flavor) error   // Full build pipeline
```

`BuildDefaultImage` resolves latest version from npm, generates Dockerfile, creates Docker client, and builds the image with clawker labels.

## Dockerfile Generation (`dockerfile.go`)

```go
mgr := build.NewDockerfileManager(outputDir, config)
mgr.GenerateDockerfiles()             // Render all Dockerfiles
mgr.DockerfilesDir() string           // Output directory path

gen := build.NewProjectGenerator(config, workDir)
gen.Generate() error                  // Generate project Dockerfile
gen.GenerateBuildContext() error       // Create build context tar
gen.UseCustomDockerfile() bool         // Check for custom Dockerfile

build.CreateBuildContextFromDir(dir, dockerfilePath string) (io.Reader, error)
```

### Embedded Assets

`DockerfileTemplate`, `EntrypointScript`, `FirewallScript`, `StatuslineScript`, `HostOpenScript`, `CallbackForwarderScript`, `GitCredentialScript`, `SSHAgentProxySource` — embedded via `go:embed`.

## Version Management (`versions.go`)

```go
vm := build.NewVersionsManager()
vm.ResolveVersions(ctx, patterns, opts) (*registry.VersionsFile, error)

build.LoadVersionsFile(path) (*registry.VersionsFile, error)
build.SaveVersionsFile(path, vf) error  // vf is *registry.VersionsFile
```

## Variant Configuration (`config.go`)

```go
cfg := build.DefaultVariantConfig()
cfg.IsAlpine(variant) bool
cfg.VariantNames() []string
```

## Error Types (`errors.go`)

```go
var ErrVersionNotFound, ErrInvalidVersion, ErrNoVersions error
type NetworkError struct { ... }
type RegistryError struct { ... }
```

## Dependencies

Imports: `internal/config`, `internal/docker`, `internal/logger`, `internal/build/registry`, `internal/build/semver`

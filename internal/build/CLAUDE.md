# Build Package

Image building orchestration. Wraps `pkg/build` with Docker client integration.

## Key Files

| File | Purpose |
|------|---------|
| `build.go` | `Builder` — project image building (EnsureImage, Build) |
| `defaults.go` | Default image building, flavor selection |

## Builder (`build.go`)

```go
type Builder struct { client, config, workDir }

func NewBuilder(cli *docker.Client, cfg *config.Config, workDir string) *Builder
func (b *Builder) EnsureImage(ctx context.Context, imageTag string, opts Options) error  // Build if needed (skips if image exists unless ForceBuild)
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

## Default Image Utilities (`defaults.go`)

```go
const DefaultImageTag = "clawker-default:latest"

type FlavorOption struct { Name, Description string }

func DefaultFlavorOptions() []FlavorOption  // bookworm, trixie, alpine3.22, alpine3.23
func FlavorToImage(flavor string) string    // Maps flavor name to base image ref
func BuildDefaultImage(ctx, flavor) error   // Full build pipeline via pkg/build
```

`BuildDefaultImage` resolves latest version from npm, generates Dockerfile, creates Docker client, and builds the image with clawker labels.

## Dependencies

Imports: `internal/config`, `internal/docker`, `internal/logger`, `pkg/build`

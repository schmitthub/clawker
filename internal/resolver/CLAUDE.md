# Resolver Package

Image resolution for container creation. Resolves which Docker image to use based on project images, config, and user settings.

## Key Files

| File | Purpose |
|------|---------|
| `types.go` | `ImageSource`, `ResolvedImage` — source tracking types |
| `image.go` | Resolution chain + validation with interactive rebuild |

## Types (`types.go`)

```go
type ImageSource string // "explicit", "project", "default"

type ResolvedImage struct {
    Reference string      // e.g., "clawker-myproject:latest"
    Source    ImageSource  // Where it was resolved from
}
```

## Resolution Functions (`image.go`)

### Resolution Chain

```go
// Simple resolution — returns just the image reference
func ResolveImage(ctx, dockerClient, cfg, settings) (string, error)

// Resolution with source tracking
func ResolveImageWithSource(ctx, dockerClient, cfg, settings) (*ResolvedImage, error)
```

**Resolution order**: Project image (label lookup) → Default image (config/settings)

### Validation + Interactive Rebuild

```go
type ImageValidationDeps struct {
    IOStreams                *iostreams.IOStreams
    Prompter                func() *prompts.Prompter
    SettingsLoader          func() (*config.SettingsLoader, error)
    InvalidateSettingsCache func()
}

func ResolveAndValidateImage(ctx, deps, dockerClient, cfg, settings) (*ResolvedImage, error)
```

Validates default images exist, prompts to rebuild if missing (interactive mode). Uses `internal/build.BuildDefaultImage` for rebuild.

### Helpers

```go
func ResolveDefaultImage(cfg, settings) string           // Config precedence over settings
func FindProjectImage(ctx, dockerClient, project) string  // Label-based lookup for :latest
```

## Usage Pattern (Container Commands)

```go
if containerOpts.Image == "@" {
    resolvedImage, err := resolver.ResolveAndValidateImage(ctx, resolver.ImageValidationDeps{
        IOStreams:                opts.IOStreams,
        Prompter:                opts.Prompter,
        SettingsLoader:          opts.SettingsLoader,
        InvalidateSettingsCache: opts.InvalidateSettingsCache,
    }, client, cfg, settings)
    if err != nil { return err }
    containerOpts.Image = resolvedImage.Reference
}
```

## Dependencies

Imports: `internal/build` (for rebuild), `internal/config`, `internal/docker`, `internal/iostreams`, `internal/logger`, `internal/prompts`

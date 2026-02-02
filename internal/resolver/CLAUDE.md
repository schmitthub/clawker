# Resolver Package

Image resolution for container creation. Resolves which Docker image to use based on project images, config, and user settings.

## Key Files

| File | Purpose |
|------|---------|
| `types.go` | `ImageSource`, `ResolvedImage` -- source tracking types |
| `image.go` | Resolution chain + validation with interactive rebuild |

## Types (`types.go`)

```go
type ImageSource string

const (
    ImageSourceExplicit ImageSource = "explicit" // User specified via CLI or args
    ImageSourceProject  ImageSource = "project"  // Found via project label search
    ImageSourceDefault  ImageSource = "default"  // From config/settings default_image
)

type ResolvedImage struct {
    Reference string      // e.g., "clawker-myproject:latest"
    Source    ImageSource  // Where it was resolved from
}
```

## Resolution Functions (`image.go`)

### Resolution Chain

`ResolveImage()` -- returns just the image reference string
`ResolveImageWithSource()` -- returns `*ResolvedImage` with source tracking

Both take `(ctx, dockerClient, cfg, settings)`. Resolution order:
1. Project image with `:latest` tag (by label lookup via `FindProjectImage`)
2. Merged default_image from config/settings (via `ResolveDefaultImage`)

Note: Explicit image handling happens at the call site (commands check for `@` sentinel), not inside these functions.

### Validation + Interactive Rebuild

```go
type ImageValidationDeps struct {
    IOStreams                *iostreams.IOStreams
    Prompter                func() *prompts.Prompter
    SettingsLoader          func() (*config.SettingsLoader, error)
    InvalidateSettingsCache func()  // May be nil
}

func ResolveAndValidateImage(ctx, deps, dockerClient, cfg, settings) (*ResolvedImage, error)
```

Validates default images exist in Docker, prompts to rebuild if missing (interactive mode).
For explicit and project images, no validation is performed.
In non-interactive mode, prints next-steps and returns error if default image missing.
Uses `internal/build.BuildDefaultImage` for rebuild, updates settings on success.

### Helpers

`ResolveDefaultImage(cfg, settings) string` -- config takes precedence over user settings, returns "" if unconfigured
`FindProjectImage(ctx, dockerClient, project) (string, error)` -- label-based lookup for `:latest` tag; returns "" if not found

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

Imports: `internal/build` (rebuild), `internal/config`, `internal/docker`, `internal/iostreams`, `internal/logger`, `internal/prompts`

## Tests

`image_test.go` -- unit tests for resolution chain and helpers (no Docker required)

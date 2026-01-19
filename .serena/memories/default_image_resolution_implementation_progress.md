# Default Image Resolution & Validation - Implementation Progress

## Status: COMPLETED

## Summary
Changed image resolution order and added validation for default images. All implementations complete and tests pass.

## What Was Done

### 1. Created `pkg/cmdutil/image_build.go` ✅ COMPLETE
Extracted shared build logic from init.go:
- `DefaultImageTag` constant = "clawker-default:latest"
- `FlavorOption` struct with Name and Description fields
- `DefaultFlavorOptions()` function returning slice of flavors
- `BuildDefaultImage(ctx, flavor)` function - full implementation

### 2. Updated init.go ✅ COMPLETE
- Removed `DefaultImageTag` constant
- Removed `flavorOption` struct
- Removed `flavorOptions` var
- Removed `buildDefaultImage` function
- Updated to use `cmdutil.DefaultImageTag`, `cmdutil.DefaultFlavorOptions()`, `cmdutil.BuildDefaultImage()`

### 3. Updated init_test.go ✅ COMPLETE
- Tests now use `cmdutil.DefaultImageTag` and `cmdutil.DefaultFlavorOptions()`

### 4. Updated resolve.go ✅ COMPLETE
- Changed `ResolveImage()` order: explicit → project → default (was: explicit → default → project)
- Added `ImageSource` type and constants (explicit, project, default)
- Added `ResolvedImage` struct with Reference and Source fields
- Added `ResolveImageWithSource()` function for source tracking
- Added `ResolveAndValidateImage()` function that:
  - Resolves image using new order
  - For default images: validates they exist via `client.ImageExists()`
  - If missing in interactive mode: prompts to rebuild
  - If missing in non-interactive: returns helpful error
  - Updates settings after successful rebuild

### 5. Updated run.go and create.go ✅ COMPLETE
- Both files now use `cmdutil.ResolveAndValidateImage()` instead of `cmdutil.ResolveImage()`
- Error handling simplified (validation function handles printing)

## Key Details for Resume

### Resolution Order Change in resolve.go
```go
// OLD order in ResolveImage():
// 1. Explicit image
// 2. Default from config/settings
// 3. Project image lookup

// NEW order:
// 1. Explicit image
// 2. Project image lookup (Docker label search)
// 3. Default from config/settings
```

### New ResolveAndValidateImage function signature
```go
type ResolveAndValidateImageOptions struct {
    ExplicitImage string
}

type ResolveAndValidateImageResult struct {
    Image  string
    Source string // "explicit", "project", or "default"
}

func ResolveAndValidateImage(
    ctx context.Context,
    f *Factory,
    client *docker.Client,
    cfg *config.Config,
    settings *config.Settings,
    opts ResolveAndValidateImageOptions,
) (*ResolveAndValidateImageResult, error)
```

### Validation Logic
- Explicit images: no validation (user knows what they want)
- Project images: no validation (Docker already confirmed they exist via FindProjectImage)
- Default images: validate with `client.ImageExists()`, prompt to rebuild if missing

### Helper methods already available
- `client.ImageExists(ctx, imageRef)` - in internal/docker/client.go:78-90
- `f.SettingsLoader()` - returns loader for saving updated settings
- `f.InvalidateSettingsCache()` - clears cached settings after update
- `f.IOStreams.IsInteractive()` - check if can prompt user
- `f.Prompter()` - for prompts

## Tests to Update
- `pkg/cmd/init/init_test.go` - tests `DefaultImageTag` and `flavorOptions` which are being moved
- Need to update tests to reference `cmdutil.DefaultImageTag` and `cmdutil.DefaultFlavorOptions()`

## Branch
`a/user-vs-project-init`

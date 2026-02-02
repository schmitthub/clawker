package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	prompterpkg "github.com/schmitthub/clawker/internal/prompter"
)

// ImageSource indicates where an image reference was resolved from.
type ImageSource string

const (
	ImageSourceExplicit ImageSource = "explicit" // User specified via CLI or args
	ImageSourceProject  ImageSource = "project"  // Found via project label search
	ImageSourceDefault  ImageSource = "default"  // From config/settings default_image
)

// ResolvedImage contains the result of image resolution with source tracking.
type ResolvedImage struct {
	Reference string      // The image reference (name:tag)
	Source    ImageSource // Where the image was resolved from
}

// FlavorOption represents a Linux flavor choice for image building.
type FlavorOption struct {
	Name        string
	Description string
}

// ResolveDefaultImage returns the default_image from merged config/settings.
// Local project config takes precedence over user settings.
// Returns empty string if not configured.
func ResolveDefaultImage(cfg *config.Project, settings *config.Settings) string {
	// Local project config takes precedence
	if cfg != nil && cfg.DefaultImage != "" {
		return cfg.DefaultImage
	}

	// Fall back to user settings
	if settings != nil && settings.DefaultImage != "" {
		return settings.DefaultImage
	}

	return ""
}

// FindProjectImage searches for a clawker-managed image matching the project label
// with the :latest tag. Returns the image reference (name:tag) if found,
// or empty string if not found.
func FindProjectImage(ctx context.Context, dockerClient *Client, project string) (string, error) {
	if dockerClient == nil || project == "" {
		return "", nil
	}

	// Build filter for project label
	// Images built by clawker have com.clawker.project=<project>
	f := Filters{}.
		Add("label", LabelManaged+"="+ManagedLabelValue).
		Add("label", LabelProject+"="+project)

	result, err := dockerClient.ImageList(ctx, ImageListOptions{
		Filters: f,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list images: %w", err)
	}

	// Look for an image with :latest tag
	for _, img := range result.Items {
		for _, tag := range img.RepoTags {
			if strings.HasSuffix(tag, ":latest") {
				return tag, nil
			}
		}
	}

	// No image with :latest tag found
	return "", nil
}

// ResolveImage resolves the image to use for container creation.
// Resolution order:
// 1. Explicitly provided image (from CLI or opts)
// 2. Project image with :latest tag (by label lookup)
// 3. Merged default_image from config/settings
//
// Returns the resolved image reference and an error if no image could be resolved.
func ResolveImage(ctx context.Context, dockerClient *Client, cfg *config.Project, settings *config.Settings) (string, error) {
	result, err := ResolveImageWithSource(ctx, dockerClient, cfg, settings)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	return result.Reference, nil
}

// ResolveImageWithSource resolves the image with source tracking.
// See ResolveImage for resolution order details.
func ResolveImageWithSource(ctx context.Context, dockerClient *Client, cfg *config.Project, settings *config.Settings) (*ResolvedImage, error) {

	// 2. Try to find a project image with :latest tag
	if cfg != nil && cfg.Project != "" {
		projectImage, err := FindProjectImage(ctx, dockerClient, cfg.Project)
		if err != nil {
			// Log for debugging but don't fail - fallback means no auto-detect
			logger.Debug().Err(err).Str("project", cfg.Project).Msg("failed to auto-detect project image")
		} else if projectImage != "" {
			return &ResolvedImage{Reference: projectImage, Source: ImageSourceProject}, nil
		}
	}

	// 3. Try merged default_image from config/settings
	if defaultImage := ResolveDefaultImage(cfg, settings); defaultImage != "" {
		return &ResolvedImage{Reference: defaultImage, Source: ImageSourceDefault}, nil
	}

	return nil, nil
}

// ImageValidationDeps holds the dependencies needed by ResolveAndValidateImage.
type ImageValidationDeps struct {
	IOStreams       *iostreams.IOStreams
	Prompter       func() *prompterpkg.Prompter
	SettingsLoader func() (*config.SettingsLoader, error)

	// InvalidateSettingsCache clears the cached settings so the next
	// Settings() call reloads from disk. May be nil.
	InvalidateSettingsCache func()

	// DefaultImageTag is the tag used for the user's default base image
	// (e.g. "clawker-default:latest"). Injected to avoid import cycle with
	// internal/build.
	DefaultImageTag string

	// DefaultFlavorOptions returns the available Linux flavors for base images.
	// Injected to avoid import cycle with internal/build.
	DefaultFlavorOptions func() []FlavorOption

	// BuildDefaultImage builds the default clawker base image with the given
	// flavor. Injected to avoid import cycle with internal/build.
	BuildDefaultImage func(ctx context.Context, flavor string) error
}

// ResolveAndValidateImage resolves an image and validates it exists (for default images).
// For explicit and project images, no validation is performed.
// For default images, checks if the image exists in Docker and prompts to rebuild if missing.
//
// Returns an error if:
// - No image could be resolved
// - Default image doesn't exist and rebuild fails or is declined
func ResolveAndValidateImage(
	ctx context.Context,
	deps ImageValidationDeps,
	dockerClient *Client,
	cfg *config.Project,
	settings *config.Settings,
) (*ResolvedImage, error) {
	ios := deps.IOStreams

	// Resolve the image
	result, err := ResolveImageWithSource(ctx, dockerClient, cfg, settings)
	if err != nil {
		return nil, err
	}

	// No image resolved
	if result == nil {
		return nil, nil
	}

	// Only validate default images
	if result.Source != ImageSourceDefault {
		return result, nil
	}

	// Check if the default image exists
	exists, err := dockerClient.ImageExists(ctx, result.Reference)
	if err != nil {
		logger.Debug().Err(err).Str("image", result.Reference).Msg("failed to check if image exists")
		// Proceed anyway - Docker will error during run if image doesn't exist
		return result, nil
	}

	if exists {
		return result, nil
	}

	// Default image doesn't exist - prompt to rebuild or error
	if !ios.IsInteractive() {
		fmt.Fprintf(ios.ErrOut, "Error: Default image %q not found\n", result.Reference)
		fmt.Fprintln(ios.ErrOut, "\nNext Steps:")
		fmt.Fprintln(ios.ErrOut, "  1. Run 'clawker init' to rebuild the base image")
		fmt.Fprintln(ios.ErrOut, "  2. Or specify an image explicitly: clawker run IMAGE")
		fmt.Fprintln(ios.ErrOut, "  3. Or build a project image: clawker build")
		return nil, fmt.Errorf("default image %q not found", result.Reference)
	}

	// Interactive mode - prompt to rebuild
	prompter := deps.Prompter()
	options := []prompterpkg.SelectOption{
		{Label: "Yes", Description: "Rebuild the default base image now"},
		{Label: "No", Description: "Cancel and fix manually"},
	}

	idx, err := prompter.Select(
		fmt.Sprintf("Default image %q not found. Rebuild now?", result.Reference),
		options,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for rebuild: %w", err)
	}

	if idx != 0 {
		fmt.Fprintln(ios.ErrOut, "\nNext Steps:")
		fmt.Fprintln(ios.ErrOut, "  1. Run 'clawker init' to rebuild the base image")
		fmt.Fprintln(ios.ErrOut, "  2. Or specify an image explicitly: clawker run IMAGE")
		fmt.Fprintln(ios.ErrOut, "  3. Or build a project image: clawker build")
		return nil, fmt.Errorf("default image %q not found", result.Reference)
	}

	// User chose to rebuild - get flavor selection
	flavors := deps.DefaultFlavorOptions()
	flavorOptions := make([]prompterpkg.SelectOption, len(flavors))
	for i, opt := range flavors {
		flavorOptions[i] = prompterpkg.SelectOption{
			Label:       opt.Name,
			Description: opt.Description,
		}
	}

	flavorIdx, err := prompter.Select("Select Linux flavor", flavorOptions, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to select flavor: %w", err)
	}

	selectedFlavor := flavors[flavorIdx].Name

	defaultImageTag := deps.DefaultImageTag
	fmt.Fprintf(ios.ErrOut, "Building %s...\n", defaultImageTag)

	if err := deps.BuildDefaultImage(ctx, selectedFlavor); err != nil {
		fmt.Fprintf(ios.ErrOut, "Error: Failed to build image: %v\n", err)
		return nil, fmt.Errorf("failed to rebuild default image: %w", err)
	}

	fmt.Fprintf(ios.ErrOut, "Build complete! Using image: %s\n", defaultImageTag)

	// Update settings with the built image
	settingsLoader, err := deps.SettingsLoader()
	if err != nil {
		logger.Warn().Err(err).Msg("failed to load settings loader; default image will not be persisted")
	} else if settingsLoader != nil {
		currentSettings, loadErr := settingsLoader.Load()
		if loadErr != nil {
			logger.Warn().Err(loadErr).Msg("failed to load existing settings; skipping settings update to avoid data loss")
		} else {
			currentSettings.DefaultImage = defaultImageTag
			if saveErr := settingsLoader.Save(currentSettings); saveErr != nil {
				logger.Warn().Err(saveErr).Msg("failed to update settings with default image")
			}
		}
		if deps.InvalidateSettingsCache != nil {
			deps.InvalidateSettingsCache()
		}
	}

	// Return the rebuilt image
	return &ResolvedImage{Reference: defaultImageTag, Source: ImageSourceDefault}, nil
}

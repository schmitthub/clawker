package cmdutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompts"
	"github.com/spf13/cobra"
)

// ResolveDefaultImage returns the default_image from merged config/settings.
// Local project config takes precedence over user settings.
// Returns empty string if not configured.
func ResolveDefaultImage(cfg *config.Config, settings *config.Settings) string {
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
func FindProjectImage(ctx context.Context, dockerClient *docker.Client, project string) (string, error) {
	if dockerClient == nil || project == "" {
		return "", nil
	}

	// Build filter for project label
	// Images built by clawker have com.clawker.project=<project>
	f := docker.Filters{}.
		Add("label", docker.LabelManaged+"="+docker.ManagedLabelValue).
		Add("label", docker.LabelProject+"="+project)

	result, err := dockerClient.ImageList(ctx, docker.ImageListOptions{
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

// ResolveImage resolves the image to use for container creation.
// Resolution order:
// 1. Explicitly provided image (from CLI or opts)
// 2. Project image with :latest tag (by label lookup)
// 3. Merged default_image from config/settings
//
// Returns the resolved image reference and an error if no image could be resolved.
func ResolveImage(ctx context.Context, dockerClient *docker.Client, cfg *config.Config, settings *config.Settings) (string, error) {
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
func ResolveImageWithSource(ctx context.Context, dockerClient *docker.Client, cfg *config.Config, settings *config.Settings) (*ResolvedImage, error) {

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
	Prompter       func() *prompts.Prompter
	SettingsLoader func() (*config.SettingsLoader, error)

	// InvalidateSettingsCache clears the cached settings so the next
	// Settings() call reloads from disk. May be nil.
	InvalidateSettingsCache func()
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
	dockerClient *docker.Client,
	cfg *config.Config,
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
		PrintError(ios, "Default image %q not found", result.Reference)
		PrintNextSteps(ios,
			"Run 'clawker init' to rebuild the base image",
			"Or specify an image explicitly: clawker run IMAGE",
			"Or build a project image: clawker build",
		)
		return nil, fmt.Errorf("default image %q not found", result.Reference)
	}

	// Interactive mode - prompt to rebuild
	prompter := deps.Prompter()
	options := []prompts.SelectOption{
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
		PrintNextSteps(ios,
			"Run 'clawker init' to rebuild the base image",
			"Or specify an image explicitly: clawker run IMAGE",
			"Or build a project image: clawker build",
		)
		return nil, fmt.Errorf("default image %q not found", result.Reference)
	}

	// User chose to rebuild - get flavor selection
	flavors := DefaultFlavorOptions()
	flavorOptions := make([]prompts.SelectOption, len(flavors))
	for i, opt := range flavors {
		flavorOptions[i] = prompts.SelectOption{
			Label:       opt.Name,
			Description: opt.Description,
		}
	}

	flavorIdx, err := prompter.Select("Select Linux flavor", flavorOptions, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to select flavor: %w", err)
	}

	selectedFlavor := flavors[flavorIdx].Name

	fmt.Fprintf(ios.ErrOut, "Building %s...\n", DefaultImageTag)

	if err := BuildDefaultImage(ctx, selectedFlavor); err != nil {
		PrintError(ios, "Failed to build image: %v", err)
		return nil, fmt.Errorf("failed to rebuild default image: %w", err)
	}

	fmt.Fprintf(ios.ErrOut, "Build complete! Using image: %s\n", DefaultImageTag)

	// Update settings with the built image
	settingsLoader, err := deps.SettingsLoader()
	if err == nil && settingsLoader != nil {
		currentSettings, loadErr := settingsLoader.Load()
		if loadErr != nil {
			currentSettings = config.DefaultSettings()
		}
		currentSettings.DefaultImage = DefaultImageTag
		if saveErr := settingsLoader.Save(currentSettings); saveErr != nil {
			logger.Warn().Err(saveErr).Msg("failed to update settings with default image")
		}
		if deps.InvalidateSettingsCache != nil {
			deps.InvalidateSettingsCache()
		}
	}

	// Return the rebuilt image
	return &ResolvedImage{Reference: DefaultImageTag, Source: ImageSourceDefault}, nil
}

// AgentArgsValidator creates a Cobra Args validator for commands with --agent flag.
// When --agent is provided, no positional arguments are allowed (mutually exclusive).
// When --agent is not provided, at least minArgs positional arguments are required.
func AgentArgsValidator(minArgs int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		agentFlag, _ := cmd.Flags().GetString("agent")
		if agentFlag != "" && len(args) > 0 {
			return fmt.Errorf("--agent and positional container arguments are mutually exclusive")
		}
		if agentFlag == "" && len(args) < minArgs {
			if minArgs == 1 {
				return fmt.Errorf("requires at least 1 container argument or --agent flag")
			}
			return fmt.Errorf("requires at least %d container arguments or --agent flag", minArgs)
		}
		return nil
	}
}

// AgentArgsValidatorExact creates a Cobra Args validator for commands with --agent flag
// that require exactly N positional arguments when --agent is not provided.
func AgentArgsValidatorExact(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		agentFlag, _ := cmd.Flags().GetString("agent")
		if agentFlag != "" && len(args) > 0 {
			return fmt.Errorf("--agent and positional container arguments are mutually exclusive")
		}
		if agentFlag == "" && len(args) != n {
			if n == 1 {
				return fmt.Errorf("requires exactly 1 container argument or --agent flag")
			}
			return fmt.Errorf("requires exactly %d container arguments or --agent flag", n)
		}
		return nil
	}
}

// ResolveContainerName resolves an agent name to a full container name.
// Returns the container name in format: clawker.<project>.<agent> (or clawker.<agent> if project is empty).
func ResolveContainerName(project, agentName string) string {
	return docker.ContainerName(project, agentName)
}

// ResolveContainerNames resolves container names based on agent flag or positional args.
// If agentName is non-empty, it resolves it to a container name and returns a single-element slice.
// Otherwise, it returns the containerArgs as-is (they're expected to be full container names).
func ResolveContainerNames(project, agentName string, containerArgs []string) []string {
	if agentName != "" {
		return []string{docker.ContainerName(project, agentName)}
	}
	return containerArgs
}

// ResolveContainerNamesFromAgents resolves a slice of agent names to container names.
// If no agents are provided, returns an empty slice.
func ResolveContainerNamesFromAgents(project string, agents []string) []string {
	if len(agents) == 0 {
		return agents
	}
	containers := make([]string, len(agents))
	for i, agent := range agents {
		containers[i] = docker.ContainerName(project, agent)
	}
	return containers
}

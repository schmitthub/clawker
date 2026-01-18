package cmdutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/logger"
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
	if settings != nil && settings.Project.DefaultImage != "" {
		return settings.Project.DefaultImage
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
	f := client.Filters{}.
		Add("label", docker.LabelManaged+"="+docker.ManagedLabelValue).
		Add("label", docker.LabelProject+"="+project)

	result, err := dockerClient.ImageList(ctx, client.ImageListOptions{
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
// 2. Merged default_image from config/settings
// 3. Project image with :latest tag (by label lookup)
//
// Returns the resolved image reference and an error if no image could be resolved.
func ResolveImage(ctx context.Context, dockerClient *docker.Client, cfg *config.Config, settings *config.Settings, explicitImage string) (string, error) {
	// 1. Explicit image takes precedence
	if explicitImage != "" {
		return explicitImage, nil
	}

	// 2. Try merged default_image from config/settings
	if defaultImage := ResolveDefaultImage(cfg, settings); defaultImage != "" {
		return defaultImage, nil
	}

	// 3. Try to find a project image with :latest tag
	if cfg != nil && cfg.Project != "" {
		projectImage, err := FindProjectImage(ctx, dockerClient, cfg.Project)
		if err != nil {
			// Log for debugging but don't fail - fallback means no auto-detect
			logger.Debug().Err(err).Str("project", cfg.Project).Msg("failed to auto-detect project image")
			return "", nil
		}
		if projectImage != "" {
			return projectImage, nil
		}
	}

	return "", nil
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

// ResolveContainerName resolves an agent name to a full container name using the project config.
// Returns the full container name in format: clawker.<project>.<agent>
func ResolveContainerName(f *Factory, agentName string) (string, error) {
	cfg, err := f.Config()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}
	if cfg.Project == "" {
		return "", fmt.Errorf("project name not configured in clawker.yaml")
	}
	return docker.ContainerName(cfg.Project, agentName), nil
}

// ResolveContainerNames resolves container names based on --agent flag or positional args.
// If agentName is provided, it returns a single-element slice with the resolved container name.
// Otherwise, it returns the containerArgs as-is.
func ResolveContainerNames(f *Factory, agentName string, containerArgs []string) ([]string, error) {
	if agentName != "" {
		containerName, err := ResolveContainerName(f, agentName)
		if err != nil {
			PrintError("Failed to resolve agent name: %v", err)
			PrintNextSteps(
				"Run 'clawker init' to create a configuration",
				"Or ensure you're in a directory with clawker.yaml",
			)
			return nil, err
		}
		return []string{containerName}, nil
	}
	return containerArgs, nil
}

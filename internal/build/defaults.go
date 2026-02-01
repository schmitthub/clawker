package build

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
)

// DefaultImageTag is the tag used for the user's default base image.
const DefaultImageTag = "clawker-default:latest"

// FlavorOption represents a Linux flavor choice for image building.
type FlavorOption struct {
	Name        string
	Description string
}

// DefaultFlavorOptions returns the available Linux flavors for base images.
func DefaultFlavorOptions() []FlavorOption {
	return []FlavorOption{
		{Name: "bookworm", Description: "Debian stable (Recommended)"},
		{Name: "trixie", Description: "Debian testing"},
		{Name: "alpine3.22", Description: "Alpine Linux 3.22"},
		{Name: "alpine3.23", Description: "Alpine Linux 3.23"},
	}
}

// FlavorToImage maps a flavor name to its full Docker image reference.
// For known flavors, it returns the appropriate base image.
// For unknown flavors (custom images), it returns the input as-is.
func FlavorToImage(flavor string) string {
	switch flavor {
	case "bookworm":
		return "buildpack-deps:bookworm-scm"
	case "trixie":
		return "buildpack-deps:trixie-scm"
	case "alpine3.22":
		return "alpine:3.22"
	case "alpine3.23":
		return "alpine:3.23"
	default:
		return flavor // Custom image passed through as-is
	}
}

// BuildDefaultImage builds the default clawker base image with the given flavor.
// It resolves the latest Claude Code version from npm, generates a Dockerfile,
// and builds the image with clawker's managed labels.
func BuildDefaultImage(ctx context.Context, flavor string) error {
	// 1. Get build output directory
	buildDir, err := config.BuildDir()
	if err != nil {
		return fmt.Errorf("failed to get build directory: %w", err)
	}

	// 2. Resolve "latest" version from npm
	logger.Debug().Msg("resolving latest Claude Code version from npm")
	mgr := NewVersionsManager()
	versions, err := mgr.ResolveVersions(ctx, []string{"latest"}, ResolveOptions{})
	if err != nil {
		return fmt.Errorf("failed to resolve latest version: %w", err)
	}

	// 3. Generate dockerfiles
	logger.Debug().Str("output_dir", buildDir).Msg("generating dockerfiles")
	dfMgr := NewDockerfileManager(buildDir, nil)
	if err := dfMgr.GenerateDockerfiles(versions); err != nil {
		return fmt.Errorf("failed to generate dockerfiles: %w", err)
	}

	// 4. Find the dockerfile for selected flavor
	// Get the version key (should be only one since we requested "latest")
	var latestVersion string
	for v := range *versions {
		latestVersion = v
		break
	}
	if latestVersion == "" {
		return fmt.Errorf("no version resolved")
	}

	dockerfileName := fmt.Sprintf("%s-%s.dockerfile", latestVersion, flavor)
	dockerfilesDir := dfMgr.DockerfilesDir()
	dockerfilePath := filepath.Join(dockerfilesDir, dockerfileName)

	logger.Debug().
		Str("dockerfile", dockerfilePath).
		Str("version", latestVersion).
		Str("flavor", flavor).
		Msg("building image")

	// 5. Create Docker client
	client, err := docker.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer client.Close()

	// 6. Create build context from dockerfiles directory
	buildContext, err := CreateBuildContextFromDir(dockerfilesDir, dockerfilePath)
	if err != nil {
		return fmt.Errorf("failed to create build context: %w", err)
	}

	// 7. Build the image
	err = client.BuildImage(ctx, buildContext, docker.BuildImageOpts{
		Tags:       []string{DefaultImageTag},
		Dockerfile: dockerfileName,
		Labels: map[string]string{
			"com.clawker.managed":    "true",
			"com.clawker.base-image": "true",
			"com.clawker.flavor":     flavor,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}

	logger.Info().Str("image", DefaultImageTag).Msg("base image built successfully")
	return nil
}

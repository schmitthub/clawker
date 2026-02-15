package docker

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail"
)

// DefaultImageTag is the tag used for the user's default base image.
const DefaultImageTag = "clawker-default:latest"

// BuildDefaultImage builds the default clawker base image with the given flavor.
// It resolves the latest Claude Code version from npm, generates a Dockerfile,
// and builds the image with clawker's managed labels.
func (c *Client) BuildDefaultImage(ctx context.Context, flavor string, onProgress whail.BuildProgressFunc) error {
	// Allow override for fawker/tests.
	if c.BuildDefaultImageFunc != nil {
		return c.BuildDefaultImageFunc(ctx, flavor, onProgress)
	}

	// 1. Get build output directory
	buildDir, err := config.BuildDir()
	if err != nil {
		return fmt.Errorf("failed to get build directory: %w", err)
	}

	// 2. Resolve "latest" version from npm
	logger.Debug().Msg("resolving latest Claude Code version from npm")
	mgr := bundler.NewVersionsManager()
	versions, err := mgr.ResolveVersions(ctx, []string{"latest"}, bundler.ResolveOptions{})
	if err != nil {
		return fmt.Errorf("failed to resolve latest version: %w", err)
	}

	// 3. Ensure BuildKit is wired on this client
	WireBuildKit(c)

	// 4. Check BuildKit availability (cache mounts require it)
	buildkitEnabled, bkErr := BuildKitEnabled(ctx, c.APIClient)
	if bkErr != nil {
		logger.Warn().Err(bkErr).Msg("BuildKit detection failed")
	} else if !buildkitEnabled {
		logger.Warn().Msg("BuildKit is not available — cache mount directives will be omitted and builds may be slower")
	}

	// 5. Generate dockerfiles (with BuildKit-conditional cache mounts)
	logger.Debug().Str("output_dir", buildDir).Msg("generating dockerfiles")
	dfMgr := bundler.NewDockerfileManager(buildDir, nil)
	dfMgr.BuildKitEnabled = buildkitEnabled
	if err := dfMgr.GenerateDockerfiles(versions); err != nil {
		return fmt.Errorf("failed to generate dockerfiles: %w", err)
	}

	// 6. Find the dockerfile for selected flavor
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

	// 7. Create build context from dockerfiles directory
	buildContext, err := bundler.CreateBuildContextFromDir(dockerfilesDir, dockerfilePath)
	if err != nil {
		return fmt.Errorf("failed to create build context: %w", err)
	}

	// 8. Build the image
	err = c.BuildImage(ctx, buildContext, BuildImageOpts{
		Tags:       []string{DefaultImageTag},
		Dockerfile: dockerfileName,
		Labels: map[string]string{
			LabelManaged:   ManagedLabelValue,
			LabelBaseImage: ManagedLabelValue,
			LabelFlavor:    flavor,
		},
		BuildKitEnabled: buildkitEnabled,
		ContextDir:      dockerfilesDir,
		OnProgress:      onProgress,
	})
	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}

	logger.Debug().Str("image", DefaultImageTag).Msg("base image built successfully")
	return nil
}

// TestLabelConfig returns a LabelConfig that adds the test label
// to all resource types. Use with WithLabels in test code to ensure
// CleanupTestResources can find and remove test-created resources.
//
// When testName is provided (typically t.Name()), the test name label
// is also set, enabling per-test resource debugging:
//
//	docker ps -a --filter label=dev.clawker.test.name=TestMyFunction
//	docker volume ls --filter label=dev.clawker.test.name=TestMyFunction
func TestLabelConfig(testName ...string) whail.LabelConfig {
	labels := map[string]string{
		LabelTest: ManagedLabelValue,
	}
	if len(testName) > 0 && testName[0] != "" {
		labels[LabelTestName] = testName[0]
	}
	return whail.LabelConfig{Default: labels}
}

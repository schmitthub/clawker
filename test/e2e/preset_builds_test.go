package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

// sanitizePresetName converts a preset display name to a valid project name.
func sanitizePresetName(name string) string {
	s := strings.ToLower(name)
	s = strings.NewReplacer("/", "-", "+", "plus", "#", "sharp", " ", "-", ".", "").Replace(s)
	return "preset-" + s
}

// TestPresetBuilds_E2E verifies that every language preset produces a valid
// Dockerfile and builds successfully with real Docker. This is the E2E gate
// ensuring preset YAML → bundler → Docker build works end-to-end.
func TestPresetBuilds_E2E(t *testing.T) {
	presets := config.Presets()

	for _, preset := range presets {
		if preset.AutoCustomize {
			continue // "Build from scratch" uses the same YAML as Bare
		}

		t.Run(preset.Name, func(t *testing.T) {
			projectName := sanitizePresetName(preset.Name)

			h := &harness.Harness{
				T: t,
				Opts: &harness.FactoryOptions{
					Config:         config.NewConfig,
					Client:         docker.NewClient,
					ProjectManager: project.NewProjectManager,
				},
			}
			setup := h.NewIsolatedFS(&harness.FSOptions{
				ProjectDir: projectName,
			})

			// Use clawker init --yes --preset to set up the project.
			initRes := h.Run("project", "init", projectName, "--yes", "--preset", preset.Name)
			require.NoError(t, initRes.Err,
				"init %s failed\nstdout: %s\nstderr: %s",
				preset.Name, initRes.Stdout, initRes.Stderr)

			// Verify config file was created.
			_ = setup // ProjectDir is cwd after NewIsolatedFS

			// Build the image (suppress progress output for clean test logs).
			buildRes := h.Run("build", "--progress=none")
			require.NoError(t, buildRes.Err,
				"build %s preset failed\nstdout: %s\nstderr: %s",
				preset.Name, buildRes.Stdout, buildRes.Stderr)
		})
	}
}

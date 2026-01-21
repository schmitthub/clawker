package lifecycle

import (
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

// TestClawkerBuild tests building clawker images with various configurations.
// These tests verify that:
// - Images are built with correct labels
// - Scripts are included/excluded based on config
// - Different base images work (Debian, Alpine)
func TestClawkerBuild(t *testing.T) {
	SkipIfNoDocker(t)

	tests := []struct {
		name          string
		projectConfig func(cfg *config.Config)
		verify        func(t *testing.T, env *TestEnv, imageID string)
	}{
		{
			name: "debian-defaults",
			// No mutation - test default debian build
			projectConfig: nil,
			verify: func(t *testing.T, env *TestEnv, imageID string) {
				env.VerifyImageLabels(imageID)
				env.VerifyImageUser(imageID, "claude")
				env.VerifyImageWorkdir(imageID, "/workspace")
				env.VerifyImageHasScripts(imageID, []string{
					"entrypoint.sh",
					"host-open",
					"git-credential-clawker",
					"init-firewall.sh", // Firewall enabled by default
				})
			},
		},
		{
			name: "debian-firewall-disabled",
			projectConfig: func(cfg *config.Config) {
				cfg.Security.Firewall = &config.FirewallConfig{Enable: false}
				cfg.Security.CapAdd = nil // No NET_ADMIN needed
			},
			verify: func(t *testing.T, env *TestEnv, imageID string) {
				env.VerifyImageLabels(imageID)
				// init-firewall.sh should NOT be present when firewall is disabled
				env.VerifyImageMissingScripts(imageID, []string{"init-firewall.sh"})
				// But other scripts should still be there
				env.VerifyImageHasScripts(imageID, []string{
					"entrypoint.sh",
					"host-open",
					"git-credential-clawker",
				})
			},
		},
		{
			name: "debian-custom-packages",
			projectConfig: func(cfg *config.Config) {
				cfg.Build.Packages = append(cfg.Build.Packages, "vim", "htop")
			},
			verify: func(t *testing.T, env *TestEnv, imageID string) {
				env.VerifyImageLabels(imageID)
				// Verify packages are installed by checking binaries exist
				containerID := env.createThrowawayContainer(imageID)
				defer env.removeContainer(containerID)

				_, err := env.ExecInContainer(containerID, "which", "vim")
				require.NoError(t, err, "vim should be installed")

				_, err = env.ExecInContainer(containerID, "which", "htop")
				require.NoError(t, err, "htop should be installed")
			},
		},
		{
			name: "alpine-defaults",
			projectConfig: func(cfg *config.Config) {
				cfg.Build.Image = "alpine:3.22"
			},
			verify: func(t *testing.T, env *TestEnv, imageID string) {
				env.VerifyImageLabels(imageID)
				env.VerifyImageUser(imageID, "claude")
				// Verify Alpine-specific package manager works
				containerID := env.createThrowawayContainer(imageID)
				defer env.removeContainer(containerID)

				_, err := env.ExecInContainer(containerID, "apk", "--version")
				require.NoError(t, err, "apk should be available on Alpine")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultTestConfig("clawker-test-build-" + sanitizeTestName(tt.name))
			if tt.projectConfig != nil {
				tt.projectConfig(cfg)
			}

			env := NewTestEnvWithOptions(t, TestEnvOptions{
				ProjectConfig:   func(c *config.Config) { *c = *cfg },
				UseClawkerImage: true,
			})
			defer env.Cleanup()

			// Image was built during env setup, verify it
			if tt.verify != nil {
				tt.verify(t, env, env.ImageTag)
			}
		})
	}
}

// TestClawkerBuildImageLabels specifically tests that built images have correct clawker labels.
func TestClawkerBuildImageLabels(t *testing.T) {
	SkipIfNoDocker(t)

	projectName := "clawker-test-build-labels"
	cfg := defaultTestConfig(projectName)

	env := NewTestEnvWithOptions(t, TestEnvOptions{
		ProjectConfig:   func(c *config.Config) { *c = *cfg },
		UseClawkerImage: true,
	})
	defer env.Cleanup()

	// Verify all labels are present
	env.VerifyImageLabels(env.ImageTag)
}

package bundler //nolint:testpackage // shares in-package test helpers (testConfig, newTestProjectGenerator) with dockerfile_test.go

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGenerate_Golden locks the rendered Dockerfile output byte-for-byte,
// per image of the base/harness split: <name>.base.Dockerfile from
// GenerateBase and <name>.harness.Dockerfile from GenerateHarness.
//
// Regenerate: GOLDEN_UPDATE=1 go test ./internal/bundler/ -run TestGenerate_Golden
func TestGenerate_Golden(t *testing.T) {
	scenarios := []struct {
		name        string
		projectYAML string
		buildKit    bool
	}{
		{
			name:        "minimal",
			projectYAML: minimalProjectYAML(),
			buildKit:    false,
		},
		{
			name:        "minimal-buildkit",
			projectYAML: minimalProjectYAML(),
			buildKit:    true,
		},
		{
			name: "packages-instructions-inject",
			projectYAML: `
version: "1"
build:
  packages: ["ripgrep", "jq"]
  instructions:
    copy:
      - src: "./scripts"
        dst: "/opt/scripts"
    args:
      - name: MY_ARG
        default: "hello"
    root_run:
      - "echo root-step"
    user_run:
      - "echo user-step"
  inject:
    after_from:
      - "RUN echo after-from"
    after_packages:
      - "RUN echo after-packages"
    after_user_setup:
      - "RUN echo after-user-setup"
    after_user_switch:
      - "RUN echo after-user-switch"
    after_claude_install:
      - "RUN echo after-claude-install"
    before_entrypoint:
      - "RUN echo before-entrypoint"
`,
			buildKit: true,
		},
		{
			name: "telemetry-flags-off",
			projectYAML: minimalProjectYAML() + `
monitoring:
  telemetry:
    log_tool_details: false
    log_user_prompts: false
    include_account_uuid: false
    include_session_id: false
`,
			buildKit: false,
		},
	}

	for _, tc := range scenarios {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t, tc.projectYAML)
			gen := newTestProjectGenerator(cfg, t.TempDir())
			gen.BuildKitEnabled = tc.buildKit

			base, err := gen.GenerateBase()
			require.NoError(t, err)
			harnessImg, err := gen.GenerateHarness()
			require.NoError(t, err)

			renders := []struct {
				suffix string
				got    []byte
			}{
				{".base.Dockerfile", base},
				{".harness.Dockerfile", harnessImg},
			}
			for _, r := range renders {
				goldenPath := filepath.Join("testdata", "golden", tc.name+r.suffix)
				if os.Getenv("GOLDEN_UPDATE") == "1" {
					require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
					require.NoError(t, os.WriteFile(goldenPath, r.got, 0o644))
					continue
				}

				want, readErr := os.ReadFile(goldenPath)
				require.NoError(t, readErr, "golden file missing — run with GOLDEN_UPDATE=1")
				require.Equal(t, string(want), string(r.got))
			}
		})
	}
}

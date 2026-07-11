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
			// Per-harness overlay trio (stacks/packages/inject) scoped to the
			// default claude harness: overlay content lands in the harness
			// image (go stack after the bundle's node installer, libnss3 apt
			// RUN, harness-scoped inject), never in the shared base.
			name: "harness-overlay",
			projectYAML: `
version: "1"
build:
  harnesses:
    claude:
      stacks: [go]
      packages: ["libnss3"]
      inject:
        after_harness_install:
          - "RUN echo overlay-after-harness-install"
        before_entrypoint:
          - "RUN echo overlay-before-entrypoint"
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
				checkGolden(t, filepath.Join("testdata", "golden", tc.name+r.suffix), r.got)
			}
		})
	}
}

// checkGolden compares got against the golden file at goldenPath, or rewrites
// the golden when GOLDEN_UPDATE=1.
func checkGolden(t *testing.T, goldenPath string, got []byte) {
	t.Helper()
	if os.Getenv("GOLDEN_UPDATE") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, got, 0o644))
		return
	}
	want, readErr := os.ReadFile(goldenPath)
	require.NoError(t, readErr, "golden file missing — run with GOLDEN_UPDATE=1")
	require.Equal(t, string(want), string(got))
}

// TestGenerate_QualifiedStackGolden locks the base render byte-for-byte when a
// build.stacks entry names a qualified installed bundle stack: the stack is
// resolved out of the host cache and its root fragment composed into the base,
// exactly as the shipped-floor scenarios above but through the installed tier.
//
// Regenerate: GOLDEN_UPDATE=1 go test ./internal/bundler/ -run TestGenerate_QualifiedStackGolden
func TestGenerate_QualifiedStackGolden(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
build:
  stacks: [acme.tools.node]
`)
	// A cached bundle shipping one stack under the qualified address the project
	// selects. testConfig isolated the XDG cache; this plants content into it.
	writeInstalledStack(t, "acme", "tools", "1.0.0", "node", "RUN echo installed-acme-node\n")

	gen := newTestProjectGenerator(cfg, t.TempDir())
	gen.BuildKitEnabled = true

	base, err := gen.GenerateBase()
	require.NoError(t, err)

	checkGolden(t, filepath.Join("testdata", "golden", "qualified-stack.base.Dockerfile"), base)
}

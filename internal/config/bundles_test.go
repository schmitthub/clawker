package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

// TestBundleSource_YamlTagDecode pins the yaml tag ↔ struct field mapping for
// every BundleSource field. This is the only test that can catch a tag typo
// (e.g. yaml:"auto-update"): validation reads the raw per-layer maps, never
// the decoded struct, so a mistagged field silently decodes to zero everywhere
// else while validation still passes.
func TestBundleSource_YamlTagDecode(t *testing.T) {
	cfg, err := config.NewFromString(`
bundles:
  - url: git@github.com:acme/mono.git
    ref: v1.2.0
    sha: 4f2a1b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f90
    path: bundles/tools
    auto_update: true
`, "")
	require.NoError(t, err)

	bundles := cfg.Project().Bundles
	require.Len(t, bundles, 1)
	assert.Equal(t, config.BundleSource{
		URL:        "git@github.com:acme/mono.git",
		Ref:        "v1.2.0",
		SHA:        "4f2a1b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f90",
		Path:       "bundles/tools",
		AutoUpdate: true,
	}, bundles[0])
}

// TestProjectDefaults_MonitorExtensions proves the defaults layer ships
// monitor.extensions: [claude-code] so a fresh project keeps monitoring
// selecting the built-in claude-code extension.
func TestProjectDefaults_MonitorExtensions(t *testing.T) {
	cfg, err := config.NewBlankConfig()
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-code"}, cfg.Project().Monitor.Extensions)
}

// TestMonitorExtensions_OverridesDefault proves monitor.extensions is a
// selection key with override merge (like build.stacks), NOT a union: a layer
// that sets it wins wholesale, so a project can deselect the claude-code
// default. Under a union merge this would resolve to
// [claude-code, prometheus] and fail.
func TestMonitorExtensions_OverridesDefault(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	require.NoError(t, cfg.ProjectStore().Set("monitor.extensions", []string{"prometheus"}))
	assert.Equal(t, []string{"prometheus"}, cfg.Project().Monitor.Extensions)
}

// --- Front-door validation of the bundles: list. ---

func TestValidateBundles_Table(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string // empty = valid
	}{
		{
			"remote url with ref",
			"bundles:\n  - url: https://x/y.git\n    ref: main\n",
			"",
		},
		{
			"remote url with sha",
			"bundles:\n  - url: https://x/y.git\n    sha: 4f2a1b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f90\n",
			"",
		},
		{
			"remote url subdir with sha",
			"bundles:\n  - url: https://x/y.git\n    path: sub/dir\n    sha: 4f2a1b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f90\n",
			"",
		},
		{
			"local relative path alone (project layer)",
			"bundles:\n  - path: ./vendor/b\n",
			"",
		},
		{
			"neither url nor path",
			"bundles:\n  - auto_update: true\n",
			"must set url",
		},
		{
			// Both ref and sha are legal on a remote source; sha takes
			// precedence over ref when both are set (locked spec).
			"url with both ref and sha",
			"bundles:\n  - url: https://x/y.git\n    ref: main\n    sha: 4f2a1b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f90\n",
			"",
		},
		{
			// Unpinned remote source: tracks the repository's default branch.
			"url with neither ref nor sha",
			"bundles:\n  - url: https://x/y.git\n",
			"",
		},
		{
			// An explicit empty ref is not an unpinned source — the key is
			// present but unusable at fetch.
			"url with empty ref",
			"bundles:\n  - url: https://x/y.git\n    ref: \"\"\n",
			"bundles[0].ref: must not be empty",
		},
		{
			"sha not 40 hex",
			"bundles:\n  - url: https://x/y.git\n    sha: deadbeef\n",
			"40-character hex commit SHA",
		},
		{
			"sha wrong charset",
			"bundles:\n  - url: https://x/y.git\n    sha: zzzz1b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f90\n",
			"40-character hex commit SHA",
		},
		{
			"ref on local path source",
			"bundles:\n  - path: ./b\n    ref: main\n",
			"ref and sha require a url",
		},
		{
			"unknown field (typo'd rev)",
			"bundles:\n  - url: https://x/y.git\n    rev: main\n",
			"bundles[0].rev: unknown field",
		},
		{
			// yaml coerces the int 5 into the string url field at decode, so
			// the merged tree decodes; the map-view type check surfaces it.
			"url not a string",
			"bundles:\n  - url: 5\n    ref: main\n",
			"bundles[0].url: must be a string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.NewFromString(tc.yaml, "")
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestValidateBundles_NullEntry — a bare "bundles:" key (null node) contributes
// no entries and must not be rejected.
func TestValidateBundles_NullNode(t *testing.T) {
	_, err := config.NewFromString("bundles:\n", "")
	require.NoError(t, err)
}

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
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

// TestProjectDefaults_MonitorExtensions proves the defaults layer ships the
// claude-code monitoring extension, so a config that never sets
// monitor.extensions keeps the shipped monitoring without any migration; an
// explicit empty list opts out.
func TestProjectDefaults_MonitorExtensions(t *testing.T) {
	cfg, err := config.NewBlankConfig()
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-code"}, cfg.Project().Monitor.Extensions)
}

// TestMonitorExtensions_OverrideMergeNotUnion proves monitor.extensions is a
// selection key with override merge (like build.stacks), NOT a union: two
// layers set it — the user config-dir layer and the project layer — and the
// project layer wins WHOLESALE. Under a union-merge mutation on the field's
// tag this resolves to both entries and fails.
func TestMonitorExtensions_OverrideMergeNotUnion(t *testing.T) {
	env := testenv.New(t)
	require.NoError(t, os.MkdirAll(consts.ConfigDir(), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(consts.ConfigDir(), consts.ProjectConfigFile),
		[]byte("monitor:\n  extensions: [claude-code]\n"), 0o644))

	projDir := filepath.Join(env.Dirs.Base, "proj")
	require.NoError(t, os.MkdirAll(projDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(projDir, "."+consts.ProjectConfigFile),
		[]byte("monitor:\n  extensions: [prometheus]\n"), 0o644))

	t.Chdir(projDir)
	cfg, err := config.NewConfig(config.WithProjectRoot(projDir))
	require.NoError(t, err)
	assert.Equal(t, []string{"prometheus"}, cfg.Project().Monitor.Extensions,
		"the highest layer that sets the selection wins wholesale")
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

// TestBundleDeclarationsAt covers the roots-side declaration loader: reading
// one registered project root's bundle declarations without a full config
// load, using the same dual-placement discovery a walk-up level gets (a
// .clawker/ dir form, or flat dotted files). It is what makes the bundle
// cache's GC roots exact across projects the current process is not running
// in.
func TestBundleDeclarationsAt(t *testing.T) {
	t.Run("flat dotted main and local files both contribute", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "."+consts.ProjectConfigFile),
			[]byte("bundles:\n  - url: https://x/main.git\n    ref: v1\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(root, "."+consts.ProjectLocalConfigFile),
			[]byte("bundles:\n  - url: https://x/local.git\n"), 0o644))

		decls, err := config.BundleDeclarationsAt(root)
		require.NoError(t, err)
		require.Len(t, decls, 2)
		// Local override layer outranks the main file, mirroring config load
		// order; each declaration names its actual declaring file.
		assert.Equal(t, "https://x/local.git", decls[0].Source.URL)
		assert.Equal(t, filepath.Join(root, "."+consts.ProjectLocalConfigFile), decls[0].File)
		assert.Equal(t, "https://x/main.git", decls[1].Source.URL)
		assert.Equal(t, "v1", decls[1].Source.Ref)
	})

	t.Run("dir-form .clawker/clawker.yaml contributes", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, consts.DotClawkerDir), 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(root, consts.DotClawkerDir, consts.ProjectConfigFile),
			[]byte("bundles:\n  - url: https://x/dirform.git\n    auto_update: true\n"), 0o644))

		decls, err := config.BundleDeclarationsAt(root)
		require.NoError(t, err)
		require.Len(t, decls, 1)
		assert.Equal(t, "https://x/dirform.git", decls[0].Source.URL)
		assert.True(t, decls[0].Source.AutoUpdate)
	})

	t.Run("nested layers under the root contribute", func(t *testing.T) {
		// Walk-up discovery makes every directory between a CWD and the
		// project root a potential declaring layer, so the roots-side loader
		// must probe the whole tree — a nested declaration roots its cache
		// entry no matter where the pruning process runs.
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "."+consts.ProjectConfigFile),
			[]byte("bundles:\n  - url: https://x/top.git\n"), 0o644))
		svc := filepath.Join(root, "svc")
		require.NoError(t, os.MkdirAll(svc, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(svc, "."+consts.ProjectConfigFile),
			[]byte("bundles:\n  - url: https://x/nested.git\n    ref: v1\n"), 0o644))
		deep := filepath.Join(svc, "api", consts.DotClawkerDir)
		require.NoError(t, os.MkdirAll(deep, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(deep, consts.ProjectConfigFile),
			[]byte("bundles:\n  - url: https://x/deep.git\n"), 0o644))

		decls, err := config.BundleDeclarationsAt(root)
		require.NoError(t, err)
		urls := make([]string, 0, len(decls))
		for _, d := range decls {
			urls = append(urls, d.Source.URL)
		}
		assert.ElementsMatch(t,
			[]string{"https://x/top.git", "https://x/nested.git", "https://x/deep.git"}, urls)
	})

	t.Run("nested malformed bundles node fails", func(t *testing.T) {
		// Roots must be computable before anything is collected — a broken
		// nested layer fails the load the same way a broken root layer does.
		root := t.TempDir()
		svc := filepath.Join(root, "svc")
		require.NoError(t, os.MkdirAll(svc, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(svc, "."+consts.ProjectConfigFile),
			[]byte("bundles: notalist\n"), 0o644))

		_, err := config.BundleDeclarationsAt(root)
		require.Error(t, err)
	})

	t.Run("dot-directory layers are not discovered", func(t *testing.T) {
		// The documented bound of the walk: dot-directories are not descended
		// into (each level's .clawker/ dir form is probed via dual placement
		// from its parent), so a config file inside e.g. .git contributes
		// nothing.
		root := t.TempDir()
		hidden := filepath.Join(root, ".hidden")
		require.NoError(t, os.MkdirAll(hidden, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(hidden, "."+consts.ProjectConfigFile),
			[]byte("bundles:\n  - url: https://x/hidden.git\n"), 0o644))

		decls, err := config.BundleDeclarationsAt(root)
		require.NoError(t, err)
		assert.Empty(t, decls)
	})

	t.Run("missing root yields no declarations and no error", func(t *testing.T) {
		decls, err := config.BundleDeclarationsAt(filepath.Join(t.TempDir(), "gone"))
		require.NoError(t, err)
		assert.Empty(t, decls)
	})

	t.Run("root without config files yields no declarations", func(t *testing.T) {
		decls, err := config.BundleDeclarationsAt(t.TempDir())
		require.NoError(t, err)
		assert.Empty(t, decls)
	})

	t.Run("malformed bundles node fails naming the root", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "."+consts.ProjectConfigFile),
			[]byte("bundles: notalist\n"), 0o644))

		_, err := config.BundleDeclarationsAt(root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), root)
	})

	t.Run("malformed bundle entry fails naming the file", func(t *testing.T) {
		// A shape the struct decode tolerates but the bundles validator
		// rejects — roots must be computable before anything is collected.
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "."+consts.ProjectConfigFile),
			[]byte("bundles:\n  - path: ./b\n    ref: main\n"), 0o644))

		_, err := config.BundleDeclarationsAt(root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ref and sha require a url")
	})

	t.Run("unrelated invalid keys elsewhere do not block declarations", func(t *testing.T) {
		// GC roots need only the bundles node; a foreign project's mistake in
		// an unrelated key must not make every prune fail.
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "."+consts.ProjectConfigFile),
			[]byte("build:\n  harness: \"NOT/valid ref\"\nbundles:\n  - url: https://x/y.git\n"), 0o644))

		decls, err := config.BundleDeclarationsAt(root)
		require.NoError(t, err)
		require.Len(t, decls, 1)
		assert.Equal(t, "https://x/y.git", decls[0].Source.URL)
	})
}

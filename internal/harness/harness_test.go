package harness_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/harness"
)

func mapFile(data string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(data), Mode: 0, ModTime: time.Time{}, Sys: nil}
}

func bundleFS(tmpl string) fstest.MapFS {
	return fstest.MapFS{
		harness.ManifestFile: mapFile(`
version: { resolver: none }
volumes: [{ name: config, path: .test }]
`),
		harness.TemplateFile: mapFile(tmpl),
		"assets/seed.sh":     mapFile("#!/bin/sh\n"),
	}
}

func manifestFS(manifest string) fstest.MapFS {
	return fstest.MapFS{
		harness.ManifestFile: mapFile(manifest),
		harness.TemplateFile: mapFile(`{{define "block_6"}}CMD ["x"]{{end}}`),
	}
}

func TestLoad_ToolchainDeclarations(t *testing.T) {
	b, err := harness.Load("test", manifestFS(`
version: { resolver: none }
toolchains: [node, nvm]
`))
	require.NoError(t, err)
	assert.Equal(t, []string{"node", "nvm"}, b.Manifest.Toolchains)
}

func TestLoad_ToolchainDeclarations_DuplicateRejected(t *testing.T) {
	_, err := harness.Load("test", manifestFS(`
version: { resolver: none }
toolchains: [node, node]
`))
	require.ErrorContains(t, err, `duplicate toolchain declaration "node"`)
}

func TestLoad_ToolchainDeclarations_InvalidNameRejected(t *testing.T) {
	_, err := harness.Load("test", manifestFS(`
version: { resolver: none }
toolchains: ["bad/name"]
`))
	require.ErrorContains(t, err, "bad/name")
}

func TestBundleToolchain_EmbeddedDefinition(t *testing.T) {
	fsys := manifestFS("version: { resolver: none }\n")
	fsys["toolchains/mytool/toolchain.yaml"] = mapFile("description: embedded tool\n")
	fsys["toolchains/mytool/Dockerfile.toolchain-user.tmpl"] = mapFile("RUN echo embedded\n")

	b, err := harness.Load("test", fsys)
	require.NoError(t, err)

	assert.True(t, b.HasToolchain("mytool"))
	assert.False(t, b.HasToolchain("other"))

	def, err := b.Toolchain("mytool")
	require.NoError(t, err)
	assert.Empty(t, def.RootFragment)
	assert.Contains(t, def.UserFragment, "embedded")
}

func TestCompose_OverridesDeclaredBlock(t *testing.T) {
	b, err := harness.Load("test", bundleFS(`{{define "block_6" -}}
CMD ["testtool"]
{{- end}}`))
	require.NoError(t, err)

	tmpl, err := harness.Compose("FROM scratch\n{{block \"block_6\" .}}{{end}}\n", b)
	require.NoError(t, err)

	var out strings.Builder
	require.NoError(t, tmpl.Execute(&out, nil))
	assert.Equal(t, "FROM scratch\nCMD [\"testtool\"]\n", out.String())
}

func TestCompose_RejectsUnknownAndReservedDefines(t *testing.T) {
	unknown, err := harness.Load("test", bundleFS(`{{define "not_a_block"}}RUN true{{end}}`))
	require.NoError(t, err)
	_, err = harness.Compose("FROM scratch\n", unknown)
	require.ErrorContains(t, err, `unknown block "not_a_block"`)

	reserved, err := harness.Load("test", bundleFS(`{{define "after_packages"}}RUN true{{end}}`))
	require.NoError(t, err)
	_, err = harness.Compose("FROM scratch\n", reserved)
	require.ErrorContains(t, err, `reserved name "after_packages"`)
}

func TestMaterialize_NeverClobbersUserEdits(t *testing.T) {
	src := bundleFS(`{{define "block_6"}}CMD ["testtool"]{{end}}`)
	dest := t.TempDir()

	require.NoError(t, harness.Materialize(src, dest))

	// User edits the materialized template, then deletes an asset.
	tmplPath := filepath.Join(dest, harness.TemplateFile)
	require.NoError(t, os.WriteFile(tmplPath, []byte("user-edited"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(dest, "assets", "seed.sh")))

	// Re-materializing restores the missing file but never overwrites edits.
	require.NoError(t, harness.Materialize(src, dest))

	edited, err := os.ReadFile(tmplPath)
	require.NoError(t, err)
	assert.Equal(t, "user-edited", string(edited))

	restored, err := os.Stat(filepath.Join(dest, "assets", "seed.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), restored.Mode().Perm(), "restored script keeps exec mode")
}

// TestMaterialize_ShippedStamp: a fresh materialize stamps the copy with the
// shipped tree's content hash; a pre-existing (non-empty) copy is never
// stamped — its provenance is unknown, and stamping it with the current hash
// would mask exactly the staleness the stamp exists to catch.
func TestMaterialize_ShippedStamp(t *testing.T) {
	src := bundleFS(`{{define "block_6"}}CMD ["testtool"]{{end}}`)

	fresh := t.TempDir() // exists but empty → fresh
	require.NoError(t, harness.Materialize(src, fresh))
	want, err := harness.ContentHash(src)
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(fresh, harness.ShippedStampFile))
	require.NoError(t, err)
	assert.Equal(t, want+"\n", string(raw))
	stale, err := harness.MaterializedStale(src, fresh)
	require.NoError(t, err)
	assert.False(t, stale, "freshly stamped copy is not stale")

	pre := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(pre, "user.txt"), []byte("x"), 0o644))
	require.NoError(t, harness.Materialize(src, pre))
	assert.NoFileExists(t, filepath.Join(pre, harness.ShippedStampFile),
		"pre-existing copy must not be retroactively stamped")
	stale, err = harness.MaterializedStale(src, pre)
	require.NoError(t, err)
	assert.True(t, stale, "unstamped copy has unknown provenance")
}

// TestContentHash_Sensitivity: stable across calls, flips on both content and
// path changes, and excludes the stamp file itself.
func TestContentHash_Sensitivity(t *testing.T) {
	base := fstest.MapFS{"a.txt": mapFile("one"), "b/c.txt": mapFile("two")}
	h1, err := harness.ContentHash(base)
	require.NoError(t, err)
	h2, err := harness.ContentHash(base)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "hash must be deterministic")

	hContent, err := harness.ContentHash(fstest.MapFS{"a.txt": mapFile("ONE"), "b/c.txt": mapFile("two")})
	require.NoError(t, err)
	assert.NotEqual(t, h1, hContent, "content change must flip the hash")

	hPath, err := harness.ContentHash(fstest.MapFS{"a2.txt": mapFile("one"), "b/c.txt": mapFile("two")})
	require.NoError(t, err)
	assert.NotEqual(t, h1, hPath, "path change must flip the hash")

	hStamp, err := harness.ContentHash(fstest.MapFS{
		"a.txt": mapFile("one"), "b/c.txt": mapFile("two"),
		harness.ShippedStampFile: mapFile("junk"),
	})
	require.NoError(t, err)
	assert.Equal(t, h1, hStamp, "the stamp file must not influence the hash")
}

// seedBundleFS builds a loadable bundle whose manifest is supplied verbatim.
func seedBundleFS(manifest string) fstest.MapFS {
	return fstest.MapFS{
		harness.ManifestFile:    mapFile(manifest),
		harness.TemplateFile:    mapFile(`{{define "block_6"}}CMD ["x"]{{end}}`),
		"assets/statusline.sh":  mapFile("#!/bin/sh\n"),
		"assets/cfg.json":       mapFile("{}\n"),
		"assets/sub/nested.txt": mapFile("n\n"),
	}
}

// TestLoad_SeedsReferenceAssets: seed sources must be declared as explicit
// assets/-relative paths — the assets/ tree is what rides the build context.
func TestLoad_SeedsReferenceAssets(t *testing.T) {
	b, err := harness.Load("t", seedBundleFS(`
version: { resolver: none }
volumes: [{ name: config, path: .test }]
seeds:
  - { file: assets/statusline.sh, dest: .test/statusline.sh, apply: copy-if-missing }
  - { file: assets/cfg.json, dest: .test/sub/.config.json, apply: copy-if-missing-or-empty }
`))
	require.NoError(t, err)
	assert.Equal(t, "assets/statusline.sh", b.Manifest.Seeds[0].File)
}

// TestWalkAssets pins the build-context staging contract: every file under
// assets/ is visited with its assets/-prefixed path, and a bundle without
// an assets/ dir is a valid no-op.
func TestWalkAssets(t *testing.T) {
	b, err := harness.Load("t", seedBundleFS("version: { resolver: none }\n"))
	require.NoError(t, err)

	var got []string
	require.NoError(t, b.WalkAssets(func(relPath string, content []byte) error {
		require.NotEmpty(t, content)
		got = append(got, relPath)
		return nil
	}))
	assert.ElementsMatch(t, []string{"assets/statusline.sh", "assets/cfg.json", "assets/sub/nested.txt"}, got)

	noAssets, err := harness.Load("t", fstest.MapFS{
		harness.ManifestFile: mapFile("version: { resolver: none }\n"),
		harness.TemplateFile: mapFile(`{{define "block_6"}}CMD ["x"]{{end}}`),
	})
	require.NoError(t, err)
	require.NoError(t, noAssets.WalkAssets(func(string, []byte) error {
		t.Fatal("must not be called for a bundle without assets/")
		return nil
	}))
}

func TestLoad_SeedValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{
			name: "unknown apply strategy",
			manifest: `
seeds:
  - { file: assets/cfg.json, dest: .test/cfg.json, apply: overwrite }
`,
			wantErr: "unknown apply strategy",
		},
		{
			name: "seed file outside assets tree",
			manifest: `
seeds:
  - { file: cfg.json, dest: .test/cfg.json, apply: copy-if-missing }
`,
			wantErr: "under assets/",
		},
		{
			name: "seed file escaping bundle root",
			manifest: `
seeds:
  - { file: ../cfg.json, dest: .test/cfg.json, apply: copy-if-missing }
`,
			wantErr: "under assets/",
		},
		{
			name: "seed dest outside every declared volume",
			manifest: `
seeds:
  - { file: assets/cfg.json, dest: .elsewhere/cfg.json, apply: copy-if-missing }
`,
			wantErr: "not under any declared volume",
		},
		{
			name: "seed file missing from bundle",
			manifest: `
seeds:
  - { file: assets/nope.json, dest: .test/cfg.json, apply: copy-if-missing }
`,
			wantErr: "nope.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := harness.Load("t", seedBundleFS("volumes: [{ name: config, path: .test }]\n"+tt.manifest))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestLoad_StagingValidationErrors pins the load front door: volumes are
// explicit and well-formed, every directive names src and dest
// deliberately, dests fall under a declared volume, and filter verbs match
// their shapes.
func TestLoad_StagingValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{
			name: "copy missing dest",
			manifest: `
volumes: [{ name: config, path: .test }]
staging:
  copy:
    - src: ~/.test/a.json
`,
			wantErr: "require explicit src and dest",
		},
		{
			name: "copy dest outside every volume",
			manifest: `
volumes: [{ name: config, path: .test }]
staging:
  copy:
    - { src: ~/.test/a.json, dest: .other/a.json }
`,
			wantErr: "not under any declared volume",
		},
		{
			name: "copy dest with no volumes declared",
			manifest: `
staging:
  copy:
    - { src: ~/.test/a.json, dest: .test/a.json }
`,
			wantErr: "not under any declared volume",
		},
		{
			name: "json_keys on a glob src",
			manifest: `
volumes: [{ name: config, path: .test }]
staging:
  copy:
    - { src: "~/.test/*.json", dest: .test/, json_keys: [k] }
`,
			wantErr: "json_keys requires a single-file src",
		},
		{
			name: "glob mount src",
			manifest: `
volumes: [{ name: config, path: .test }]
staging:
  mounts:
    - { src: "~/.test/proj*", dest: .test/projects }
`,
			wantErr: "must be a literal path, not a glob",
		},
		{
			name: "volume name reserved for infrastructure",
			manifest: `
volumes: [{ name: history, path: .test }]
`,
			wantErr: "reserved for clawker infrastructure",
		},
		{
			name: "volume name reserved for clawker lifecycle volume",
			manifest: `
volumes: [{ name: clawker, path: .test }]
`,
			wantErr: "reserved for clawker infrastructure",
		},
		{
			name: "volume name invalid for docker",
			manifest: `
volumes: [{ name: "bad name", path: .test }]
`,
			wantErr: "must match",
		},
		{
			name: "duplicate volume name",
			manifest: `
volumes:
  - { name: config, path: .a }
  - { name: config, path: .b }
`,
			wantErr: "duplicate volume name",
		},
		{
			name: "duplicate volume path",
			manifest: `
volumes:
  - { name: a, path: .test }
  - { name: b, path: .test }
`,
			wantErr: "duplicate volume path",
		},
		{
			name: "volume path escaping home",
			manifest: `
volumes: [{ name: config, path: ../up }]
`,
			wantErr: "container-home-relative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := harness.Load("t", seedBundleFS("version: { resolver: none }\n"+tt.manifest))
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestExpandPaths pins the host-side expansion vocabulary and the
// container-side path normalization.
func TestExpandPaths(t *testing.T) {
	t.Run("host side resolves env default fallback", func(t *testing.T) {
		t.Setenv("CLAWKER_TEST_EXPAND", "")
		got, err := harness.ExpandHostPath("${CLAWKER_TEST_EXPAND:-~/.codex}/prompts")
		require.NoError(t, err)
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(home, ".codex", "prompts"), got)

		t.Setenv("CLAWKER_TEST_EXPAND", "/opt/state")
		got, err = harness.ExpandHostPath("${CLAWKER_TEST_EXPAND:-~/.codex}/prompts")
		require.NoError(t, err)
		assert.Equal(t, "/opt/state/prompts", got)

		got, err = harness.ExpandHostPath("$CLAWKER_TEST_EXPAND/x")
		require.NoError(t, err)
		assert.Equal(t, "/opt/state/x", got)
	})

	t.Run("host side absolutizes relative results", func(t *testing.T) {
		// A relative env value (multi-account workflows) resolves against
		// the current working directory.
		got, err := harness.ExpandHostPath("prompts")
		require.NoError(t, err)
		wd, err := os.Getwd()
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(wd, "prompts"), got)
	})

	t.Run("container side normalizes", func(t *testing.T) {
		assert.Equal(t, ".codex/prompts", harness.NormalizeContainerPath("./.codex/prompts/"))
		assert.Equal(t, ".codex", harness.NormalizeContainerPath(".codex"))
	})

	t.Run("glob meta ignores env references", func(t *testing.T) {
		assert.False(t, harness.HasGlobMeta("${CLAUDE_CONFIG_DIR:-~/.claude}/settings.json"))
		assert.True(t, harness.HasGlobMeta("${CLAUDE_CONFIG_DIR:-~/.claude}/*.json"))
	})
}

package bundle

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile writes content to root/rel, creating parent directories.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

// writeManifest writes a bundle manifest into root's marker dir.
func writeManifest(t *testing.T, root, yaml string) {
	t.Helper()
	writeFile(t, root, MarkerDir+"/"+ManifestFile, yaml)
}

func loadDir(t *testing.T, root string) (*Bundle, error) {
	t.Helper()
	return LoadBundleDir(os.DirFS(root), root)
}

func TestLoadBundleDir_MultiComponent(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "namespace: acme\nname: tools\nversion: 1.2.0\n")
	writeFile(t, root, "harnesses/codex/harness.yaml", "version: { resolver: none }\n")
	writeFile(t, root, "stacks/node/stack.yaml", "description: node\n")

	b, err := loadDir(t, root)
	require.NoError(t, err)
	assert.Equal(t, BundleID{Namespace: "acme", Name: "tools"}, b.ID)
	assert.Empty(t, b.Warnings)
	require.Len(t, b.Components, 2)

	h, ok := b.Component(ComponentHarness, "codex")
	require.True(t, ok)
	assert.Equal(t, "acme.tools.codex", h.Address.String())
	assert.Equal(t, ComponentHarness, h.Type)

	s, ok := b.Component(ComponentStack, "node")
	require.True(t, ok)
	assert.Equal(t, "acme.tools.node", s.Address.String())
}

// A single-component bundle is one convention dir plus the marker dir — there is
// no bare-manifest-at-root special case.
func TestLoadBundleDir_SingleComponent(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "namespace: acme\nname: onestack\n")
	writeFile(t, root, "stacks/mystack/stack.yaml", "description: x\n")

	b, err := loadDir(t, root)
	require.NoError(t, err)
	require.Len(t, b.Components, 1)
	assert.Equal(t, "acme.onestack.mystack", b.Components[0].Address.String())
}

func TestLoadBundleDir_ManifestHardFails(t *testing.T) {
	t.Run("missing manifest", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "stacks/node/stack.yaml", "description: x\n")
		_, err := loadDir(t, root)
		var me *ManifestError
		require.ErrorAs(t, err, &me)
	})

	t.Run("malformed yaml", func(t *testing.T) {
		root := t.TempDir()
		writeManifest(t, root, "namespace: [not a scalar\n")
		_, err := loadDir(t, root)
		var me *ManifestError
		require.ErrorAs(t, err, &me)
	})

	t.Run("missing namespace", func(t *testing.T) {
		root := t.TempDir()
		writeManifest(t, root, "name: tools\n")
		_, err := loadDir(t, root)
		var me *ManifestError
		require.ErrorAs(t, err, &me)
		assert.Contains(t, err.Error(), "namespace")
	})

	t.Run("missing name", func(t *testing.T) {
		root := t.TempDir()
		writeManifest(t, root, "namespace: acme\n")
		_, err := loadDir(t, root)
		var me *ManifestError
		require.ErrorAs(t, err, &me)
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("reserved namespace", func(t *testing.T) {
		root := t.TempDir()
		writeManifest(t, root, "namespace: clawker\nname: tools\n")
		_, err := loadDir(t, root)
		var me *ManifestError
		require.ErrorAs(t, err, &me)
		assert.Contains(t, err.Error(), "reserved")
	})

	t.Run("bad component name", func(t *testing.T) {
		root := t.TempDir()
		writeManifest(t, root, "namespace: acme\nname: tools\n")
		writeFile(t, root, "stacks/Bad_Name/stack.yaml", "description: x\n")
		_, err := loadDir(t, root)
		require.Error(t, err)
	})
}

func TestLoadBundleDir_Warnings(t *testing.T) {
	t.Run("unknown dir with typo suggestion", func(t *testing.T) {
		root := t.TempDir()
		writeManifest(t, root, "namespace: acme\nname: tools\n")
		writeFile(t, root, "stacks/node/stack.yaml", "description: x\n")
		writeFile(t, root, "stack/oops/keep.txt", "x") // typo of "stacks"

		b, err := loadDir(t, root)
		require.NoError(t, err)
		require.Len(t, b.Warnings, 1)
		assert.Contains(t, b.Warnings[0].Message, "unknown top-level directory")
		assert.Contains(t, b.Warnings[0].Message, "did you mean stacks/?")
	})

	t.Run("empty convention dir", func(t *testing.T) {
		root := t.TempDir()
		writeManifest(t, root, "namespace: acme\nname: tools\n")
		writeFile(t, root, "harnesses/codex/harness.yaml", "version: { resolver: none }\n")
		require.NoError(t, os.MkdirAll(filepath.Join(root, "stacks"), 0o755))

		b, err := loadDir(t, root)
		require.NoError(t, err)
		require.Len(t, b.Warnings, 1)
		assert.Contains(t, b.Warnings[0].Message, "stacks/ is empty")
	})

	t.Run("stray top-level files do not warn", func(t *testing.T) {
		root := t.TempDir()
		writeManifest(t, root, "namespace: acme\nname: tools\n")
		writeFile(t, root, "stacks/node/stack.yaml", "description: x\n")
		writeFile(t, root, "README.md", "hi")
		writeFile(t, root, "LICENSE", "MIT")

		b, err := loadDir(t, root)
		require.NoError(t, err)
		assert.Empty(t, b.Warnings)
	})

	t.Run("dot-prefixed dirs do not warn", func(t *testing.T) {
		// An in-place dev-loop bundle is a git working tree; its repository
		// plumbing must not generate unknown-dir noise on every resolve.
		root := t.TempDir()
		writeManifest(t, root, "namespace: acme\nname: tools\n")
		writeFile(t, root, "stacks/node/stack.yaml", "description: x\n")
		writeFile(t, root, ".git/HEAD", "ref: refs/heads/main\n")
		writeFile(t, root, ".github/workflows/ci.yaml", "on: push\n")

		b, err := loadDir(t, root)
		require.NoError(t, err)
		assert.Empty(t, b.Warnings)
	})

	t.Run("stray file inside a convention dir is skipped", func(t *testing.T) {
		// One innocent file beside component dirs (a README, a .DS_Store) must
		// not hard-fail the bundle — Bundles() memoizes a load error, so a
		// hard failure here would block ALL qualified resolution.
		root := t.TempDir()
		writeManifest(t, root, "namespace: acme\nname: tools\n")
		writeFile(t, root, "stacks/node/stack.yaml", "description: x\n")
		writeFile(t, root, "stacks/README.md", "about these stacks")

		b, err := loadDir(t, root)
		require.NoError(t, err)
		assert.Empty(t, b.Warnings)
		_, ok := b.Component(ComponentStack, "node")
		assert.True(t, ok)
	})
}

func TestManifestError_Unwrap(t *testing.T) {
	inner := errors.New("boom")
	me := &ManifestError{Dir: "/x", Err: inner}
	require.ErrorIs(t, me, inner)
	assert.Contains(t, me.Error(), "/x")
}

package toolchain_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/toolchain"
)

func mapFile(data string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(data)} //nolint:exhaustruct // in-memory test file; mode/time irrelevant
}

func TestLoad_RootAndUserFragments(t *testing.T) {
	fsys := fstest.MapFS{
		toolchain.ManifestFile:     mapFile("description: Node.js toolchain\n"),
		toolchain.RootFragmentFile: mapFile("RUN echo install-node\n"),
		toolchain.UserFragmentFile: mapFile("RUN echo install-nvm\n"),
	}

	def, err := toolchain.Load("node", fsys)
	require.NoError(t, err)
	assert.Equal(t, "node", def.Name)
	assert.Equal(t, "Node.js toolchain", def.Description)
	assert.Equal(t, "RUN echo install-node\n", def.RootFragment)
	assert.Equal(t, "RUN echo install-nvm\n", def.UserFragment)
}

func TestLoad_RootOnly(t *testing.T) {
	fsys := fstest.MapFS{
		toolchain.ManifestFile:     mapFile("description: Go toolchain\n"),
		toolchain.RootFragmentFile: mapFile("RUN echo install-go\n"),
	}

	def, err := toolchain.Load("go", fsys)
	require.NoError(t, err)
	assert.NotEmpty(t, def.RootFragment)
	assert.Empty(t, def.UserFragment)
}

func TestLoad_UserOnly(t *testing.T) {
	fsys := fstest.MapFS{
		toolchain.ManifestFile:     mapFile("description: Rust toolchain\n"),
		toolchain.UserFragmentFile: mapFile("RUN echo install-rust\n"),
	}

	def, err := toolchain.Load("rust", fsys)
	require.NoError(t, err)
	assert.Empty(t, def.RootFragment)
	assert.NotEmpty(t, def.UserFragment)
}

func TestLoad_MissingManifest(t *testing.T) {
	fsys := fstest.MapFS{
		toolchain.RootFragmentFile: mapFile("RUN echo x\n"),
	}

	_, err := toolchain.Load("node", fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), toolchain.ManifestFile)
}

func TestLoad_NoFragments(t *testing.T) {
	fsys := fstest.MapFS{
		toolchain.ManifestFile: mapFile("description: empty\n"),
	}

	_, err := toolchain.Load("node", fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), toolchain.RootFragmentFile)
	assert.Contains(t, err.Error(), toolchain.UserFragmentFile)
}

func TestLoad_FragmentTemplateSyntaxError(t *testing.T) {
	fsys := fstest.MapFS{
		toolchain.ManifestFile:     mapFile("description: broken\n"),
		toolchain.RootFragmentFile: mapFile("RUN {{.Unclosed\n"),
	}

	_, err := toolchain.Load("node", fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), toolchain.RootFragmentFile)
}

func TestLoad_EmptyFragmentRejected(t *testing.T) {
	fsys := fstest.MapFS{
		toolchain.ManifestFile:     mapFile("description: blank\n"),
		toolchain.RootFragmentFile: mapFile("  \n\t\n"),
		toolchain.UserFragmentFile: mapFile("RUN echo ok\n"),
	}

	_, err := toolchain.Load("node", fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is empty")
}

func TestLoad_InvalidName(t *testing.T) {
	fsys := fstest.MapFS{
		toolchain.ManifestFile:     mapFile("description: x\n"),
		toolchain.RootFragmentFile: mapFile("RUN echo x\n"),
	}

	for _, bad := range []string{"", "-node", "no/slash", "sp ace"} {
		_, err := toolchain.Load(bad, fsys)
		assert.Error(t, err, "name %q must be rejected", bad)
	}
}

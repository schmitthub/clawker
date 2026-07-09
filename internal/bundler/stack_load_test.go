package bundler_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundler"
)

func TestLoadStackDefinition_RootAndUserFragments(t *testing.T) {
	fsys := fstest.MapFS{
		bundler.StackManifestFile:     mapFile("description: Node.js stack\n"),
		bundler.StackRootFragmentFile: mapFile("RUN echo install-node\n"),
		bundler.StackUserFragmentFile: mapFile("RUN echo install-nvm\n"),
	}

	def, err := bundler.LoadStackDefinition("node", fsys)
	require.NoError(t, err)
	assert.Equal(t, "node", def.Name)
	assert.Equal(t, "Node.js stack", def.Description)
	assert.Equal(t, "RUN echo install-node\n", def.RootFragment)
	assert.Equal(t, "RUN echo install-nvm\n", def.UserFragment)
}

func TestLoadStackDefinition_RootOnly(t *testing.T) {
	fsys := fstest.MapFS{
		bundler.StackManifestFile:     mapFile("description: Go toolchain\n"),
		bundler.StackRootFragmentFile: mapFile("RUN echo install-go\n"),
	}

	def, err := bundler.LoadStackDefinition("go", fsys)
	require.NoError(t, err)
	assert.NotEmpty(t, def.RootFragment)
	assert.Empty(t, def.UserFragment)
}

func TestLoadStackDefinition_UserOnly(t *testing.T) {
	fsys := fstest.MapFS{
		bundler.StackManifestFile:     mapFile("description: Rust toolchain\n"),
		bundler.StackUserFragmentFile: mapFile("RUN echo install-rust\n"),
	}

	def, err := bundler.LoadStackDefinition("rust", fsys)
	require.NoError(t, err)
	assert.Empty(t, def.RootFragment)
	assert.NotEmpty(t, def.UserFragment)
}

func TestLoadStackDefinition_MissingManifest(t *testing.T) {
	fsys := fstest.MapFS{
		bundler.StackRootFragmentFile: mapFile("RUN echo x\n"),
	}

	_, err := bundler.LoadStackDefinition("node", fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), bundler.StackManifestFile)
}

func TestLoadStackDefinition_NoFragments(t *testing.T) {
	fsys := fstest.MapFS{
		bundler.StackManifestFile: mapFile("description: empty\n"),
	}

	_, err := bundler.LoadStackDefinition("node", fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), bundler.StackRootFragmentFile)
	assert.Contains(t, err.Error(), bundler.StackUserFragmentFile)
}

func TestLoadStackDefinition_FragmentTemplateSyntaxError(t *testing.T) {
	fsys := fstest.MapFS{
		bundler.StackManifestFile:     mapFile("description: broken\n"),
		bundler.StackRootFragmentFile: mapFile("RUN {{.Unclosed\n"),
	}

	_, err := bundler.LoadStackDefinition("node", fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), bundler.StackRootFragmentFile)
}

func TestLoadStackDefinition_EmptyFragmentRejected(t *testing.T) {
	fsys := fstest.MapFS{
		bundler.StackManifestFile:     mapFile("description: blank\n"),
		bundler.StackRootFragmentFile: mapFile("  \n\t\n"),
		bundler.StackUserFragmentFile: mapFile("RUN echo ok\n"),
	}

	_, err := bundler.LoadStackDefinition("node", fsys)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is empty")
}

func TestLoadStackDefinition_InvalidName(t *testing.T) {
	fsys := fstest.MapFS{
		bundler.StackManifestFile:     mapFile("description: x\n"),
		bundler.StackRootFragmentFile: mapFile("RUN echo x\n"),
	}

	for _, bad := range []string{"", "-node", "no/slash", "sp ace"} {
		_, err := bundler.LoadStackDefinition(bad, fsys)
		assert.Error(t, err, "name %q must be rejected", bad)
	}
}

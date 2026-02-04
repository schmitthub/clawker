package buildkit

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewImageBuilder_NilAPIClient(t *testing.T) {
	builder := NewImageBuilder(nil)
	err := builder(context.Background(), whail.ImageBuildKitOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil API client")
}

func TestToSolveOpt_EmptyContextDir(t *testing.T) {
	opts := whail.ImageBuildKitOptions{}

	_, err := toSolveOpt(opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context directory is required")
}

func TestToSolveOpt_DefaultDockerfile(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	assert.Equal(t, "Dockerfile", solveOpt.FrontendAttrs["filename"])
	assert.Equal(t, "dockerfile.v0", solveOpt.Frontend)
}

func TestToSolveOpt_CustomDockerfile(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		Dockerfile: "build/Dockerfile.dev",
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	assert.Equal(t, "build/Dockerfile.dev", solveOpt.FrontendAttrs["filename"])
}

func TestToSolveOpt_BuildArgs(t *testing.T) {
	dir := t.TempDir()
	v := "bar"
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		BuildArgs:  map[string]*string{"FOO": &v},
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	assert.Equal(t, "bar", solveOpt.FrontendAttrs["build-arg:FOO"])
}

func TestToSolveOpt_Labels(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		Labels:     map[string]string{"com.test.managed": "true", "app": "myapp"},
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	assert.Equal(t, "true", solveOpt.FrontendAttrs["label:com.test.managed"])
	assert.Equal(t, "myapp", solveOpt.FrontendAttrs["label:app"])
}

func TestToSolveOpt_NoCache(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		NoCache:    true,
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	_, ok := solveOpt.FrontendAttrs["no-cache"]
	assert.True(t, ok, "expected no-cache attribute to be set")

	// Verify CacheImports is explicitly set to empty to prevent cache import
	// (addresses moby/buildkit#2409 where no-cache only "verifies cache")
	assert.NotNil(t, solveOpt.CacheImports, "expected CacheImports to be non-nil")
	assert.Empty(t, solveOpt.CacheImports, "expected CacheImports to be empty when NoCache is true")
}

func TestToSolveOpt_NoCacheOff(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		NoCache:    false,
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	_, ok := solveOpt.FrontendAttrs["no-cache"]
	assert.False(t, ok, "expected no-cache attribute to not be set when NoCache is false")

	// CacheImports should not be explicitly set when NoCache is false
	assert.Nil(t, solveOpt.CacheImports, "expected CacheImports to be nil when NoCache is false")
}

func TestToSolveOpt_Target(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		Target:     "builder",
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	assert.Equal(t, "builder", solveOpt.FrontendAttrs["target"])
}

func TestToSolveOpt_Pull(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		Pull:       true,
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	assert.Equal(t, "pull", solveOpt.FrontendAttrs["image-resolve-mode"])
}

func TestToSolveOpt_NetworkMode(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir:  dir,
		NetworkMode: "host",
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	assert.Equal(t, "host", solveOpt.FrontendAttrs["force-network-mode"])
}

func TestToSolveOpt_Tags(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		Tags:       []string{"myimage:latest", "myimage:v1"},
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	require.Len(t, solveOpt.Exports, 1)
	export := solveOpt.Exports[0]
	assert.Equal(t, "moby", export.Type)
	assert.Equal(t, "myimage:latest,myimage:v1", export.Attrs["name"])
	assert.Equal(t, "false", export.Attrs["push"])
}

func TestToSolveOpt_LocalMounts(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	assert.NotNil(t, solveOpt.LocalMounts["context"], "expected context local mount to be set")
	assert.NotNil(t, solveOpt.LocalMounts["dockerfile"], "expected dockerfile local mount to be set")
}

func TestToSolveOpt_NilBuildArgs(t *testing.T) {
	dir := t.TempDir()
	v := "val"
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		BuildArgs:  map[string]*string{"SET": &v, "NIL": nil},
	}

	solveOpt, err := toSolveOpt(opts)
	require.NoError(t, err)

	assert.Equal(t, "val", solveOpt.FrontendAttrs["build-arg:SET"])
	_, ok := solveOpt.FrontendAttrs["build-arg:NIL"]
	assert.False(t, ok, "expected nil build arg to be omitted")
}

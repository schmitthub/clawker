package fetch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle/bundletest"
	"github.com/schmitthub/clawker/internal/bundle/fetch"
)

// seedRepo authors a repo with an initial commit tagged v0.1.0 and a later
// drift commit, returning the tagged SHA and the branch-tip SHA.
func seedRepo(t *testing.T, srv *bundletest.Server, name string) (string, string) {
	t.Helper()
	repo := srv.InitRepo(t, name)
	taggedSHA := repo.Commit(t, "initial", map[string]string{
		".clawker-bundle/bundle.yaml": "namespace: acme\nname: tools\nversion: 0.1.0\n",
		"README.md":                   "hello\n",
	})
	repo.Tag(t, "v0.1.0")
	tipSHA := repo.Commit(t, "drift", map[string]string{"stacks/node/stack.yaml": "description: node\n"})
	return taggedSHA, tipSHA
}

func TestFetcher_HTTP(t *testing.T) {
	srv := bundletest.New(t)
	taggedSHA, tipSHA := seedRepo(t, srv, "tools")
	f := fetch.NewFetcher()
	ctx := context.Background()
	url := srv.HTTPURL("tools")

	t.Run("ResolveRef dereferences an annotated tag to its commit", func(t *testing.T) {
		got, err := f.ResolveRef(ctx, url, "v0.1.0")
		require.NoError(t, err)
		assert.Equal(t, taggedSHA, got)
	})

	t.Run("ResolveRef resolves a branch head", func(t *testing.T) {
		got, err := f.ResolveRef(ctx, url, "master")
		require.NoError(t, err)
		assert.Equal(t, tipSHA, got)
	})

	t.Run("clone by ref checks out the branch tip", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "clone")
		got, err := f.Clone(ctx, fetch.CloneOptions{URL: url, Ref: "master", SHA: "", Dir: dir})
		require.NoError(t, err)
		assert.Equal(t, tipSHA, got)
		assert.FileExists(t, filepath.Join(dir, "stacks", "node", "stack.yaml"))
	})

	t.Run("clone by tag checks out the tagged commit", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "clone")
		got, err := f.Clone(ctx, fetch.CloneOptions{URL: url, Ref: "v0.1.0", SHA: "", Dir: dir})
		require.NoError(t, err)
		assert.Equal(t, taggedSHA, got)
		assert.NoFileExists(t, filepath.Join(dir, "stacks", "node", "stack.yaml"))
	})

	t.Run("clone by sha checks out that commit", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "clone")
		got, err := f.Clone(ctx, fetch.CloneOptions{URL: url, Ref: "", SHA: taggedSHA, Dir: dir})
		require.NoError(t, err)
		assert.Equal(t, taggedSHA, got)
		assert.FileExists(t, filepath.Join(dir, "README.md"))
		assert.NoFileExists(t, filepath.Join(dir, "stacks", "node", "stack.yaml"))
	})

	t.Run("ResolveRef fails on an unknown ref", func(t *testing.T) {
		_, err := f.ResolveRef(ctx, url, "no-such-ref")
		require.Error(t, err)
	})

	t.Run("clone fails on an unreachable repo", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "clone")
		_, err := f.Clone(ctx, fetch.CloneOptions{URL: srv.HTTPURL("missing"), Ref: "master", SHA: "", Dir: dir})
		require.Error(t, err)
		assert.NoDirExists(t, filepath.Join(dir, ".git"))
	})
}

func TestFetcher_SSH(t *testing.T) {
	srv := bundletest.New(t)
	taggedSHA, tipSHA := seedRepo(t, srv, "tools")
	f := fetch.NewFetcher()
	ctx := context.Background()
	url := srv.SSHURL("tools")

	t.Run("ResolveRef over ssh", func(t *testing.T) {
		got, err := f.ResolveRef(ctx, url, "v0.1.0")
		require.NoError(t, err)
		assert.Equal(t, taggedSHA, got)
	})

	t.Run("clone by ref over ssh", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "clone")
		got, err := f.Clone(ctx, fetch.CloneOptions{URL: url, Ref: "master", SHA: "", Dir: dir})
		require.NoError(t, err)
		assert.Equal(t, tipSHA, got)
		assert.FileExists(t, filepath.Join(dir, "stacks", "node", "stack.yaml"))
	})

	t.Run("clone by sha over ssh", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "clone")
		got, err := f.Clone(ctx, fetch.CloneOptions{URL: url, Ref: "", SHA: taggedSHA, Dir: dir})
		require.NoError(t, err)
		assert.Equal(t, taggedSHA, got)
		require.FileExists(t, filepath.Join(dir, "README.md"))
	})
}

// TestFetcher_CloneRejectsExistingDir guards the empty-dir precondition.
func TestFetcher_CloneRejectsExistingContent(t *testing.T) {
	srv := bundletest.New(t)
	seedRepo(t, srv, "tools")
	f := fetch.NewFetcher()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "occupied"), []byte("x"), 0o600))
	_, err := f.Clone(context.Background(), fetch.CloneOptions{
		URL: srv.HTTPURL("tools"), Ref: "master", SHA: "", Dir: dir,
	})
	require.Error(t, err)
}

package context_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dockercontext "github.com/schmitthub/clawker/internal/docker/context"
)

// Fixtures are written as literal JSON rather than marshalled from a struct:
// this package parses someone else's file format, so the tests should hold
// bytes in that format, byte-for-byte as the docker CLI writes them.

// dockerConfigDir points the reader at an isolated config dir for the test.
func dockerConfigDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("DOCKER_CONTEXT", "")
	return dir
}

// writeCurrentContext writes a config.json naming the active context, the way
// `docker context use` does.
func writeCurrentContext(t *testing.T, dir, name string) {
	t.Helper()

	raw := fmt.Sprintf(`{"auths":{},"currentContext":%q}`, name)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0o600))
}

// writeStoredContext writes a context into the store, in the shape a rootless
// install produces.
func writeStoredContext(t *testing.T, dir, name, host string) {
	t.Helper()

	raw := fmt.Sprintf(
		`{"Name":%q,"Metadata":{"Description":"Rootless mode"},"Endpoints":{"docker":{"Host":%q,"SkipTLSVerify":false}}}`,
		name,
		host,
	)
	writeStoredContextBytes(t, dir, name, []byte(raw))
}

func writeStoredContextBytes(t *testing.T, dir, name string, raw []byte) {
	t.Helper()

	// The store keys each entry by the sha256 of the context's name, which
	// is how the docker CLI addresses it.
	digest := sha256.Sum256([]byte(name))
	metaDir := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(digest[:]))
	require.NoError(t, os.MkdirAll(metaDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(metaDir, "meta.json"), raw, 0o600))
}

func TestCurrent(t *testing.T) {
	t.Run("reads the active context", func(t *testing.T) {
		dir := dockerConfigDir(t)
		writeCurrentContext(t, dir, "rootless")
		writeStoredContext(t, dir, "rootless", "unix:///run/user/1003/docker.sock")

		got, err := dockercontext.Current()

		require.NoError(t, err)
		assert.Equal(t, "rootless", got.Name)
		assert.Equal(t, "unix:///run/user/1003/docker.sock", got.Host)
		assert.Equal(t, "Rootless mode", got.Description)
		assert.False(t, got.SkipTLSVerify)
	})

	t.Run("DOCKER_CONTEXT outranks config.json", func(t *testing.T) {
		dir := dockerConfigDir(t)
		writeCurrentContext(t, dir, "rootless")
		writeStoredContext(t, dir, "rootless", "unix:///run/user/1003/docker.sock")
		writeStoredContext(t, dir, "remote", "tcp://10.0.0.5:2376")
		t.Setenv("DOCKER_CONTEXT", "remote")

		got, err := dockercontext.Current()

		require.NoError(t, err)
		assert.Equal(t, "tcp://10.0.0.5:2376", got.Host)
	})

	t.Run("no config file at all", func(t *testing.T) {
		dockerConfigDir(t)

		_, err := dockercontext.Current()

		require.ErrorIs(t, err, dockercontext.ErrConfigNotFound)
	})

	t.Run("config file selects no context", func(t *testing.T) {
		dir := dockerConfigDir(t)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"auths":{}}`), 0o600))

		_, err := dockercontext.Current()

		require.ErrorIs(t, err, dockercontext.ErrNoCurrentContext)
	})

	t.Run("config file selects the built-in default context", func(t *testing.T) {
		dir := dockerConfigDir(t)
		writeCurrentContext(t, dir, dockercontext.DefaultName)

		_, err := dockercontext.Current()

		require.ErrorIs(t, err, dockercontext.ErrNoCurrentContext)
	})

	t.Run("a context that is not in the store is reported, not swallowed", func(t *testing.T) {
		dir := dockerConfigDir(t)
		writeCurrentContext(t, dir, "rootless")

		_, err := dockercontext.Current()

		// The docker CLI fails outright here; falling back silently would
		// send clawker to a daemon the user's own docker cannot reach.
		require.ErrorIs(t, err, dockercontext.ErrContextNotFound)
		assert.Contains(t, err.Error(), "rootless")
	})

	t.Run("an unparseable config file is an error", func(t *testing.T) {
		dir := dockerConfigDir(t)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600))

		_, err := dockercontext.Current()

		require.Error(t, err)
		require.NotErrorIs(t, err, dockercontext.ErrConfigNotFound)
		require.NotErrorIs(t, err, dockercontext.ErrNoCurrentContext)
		require.NotErrorIs(t, err, dockercontext.ErrContextNotFound)
	})
}

func TestRead(t *testing.T) {
	t.Run("a context carrying no docker endpoint has no address", func(t *testing.T) {
		dir := dockerConfigDir(t)
		writeStoredContextBytes(t, dir, "k8s-only",
			[]byte(`{"Name":"k8s-only","Endpoints":{"kubernetes":{"Host":"https://10.0.0.9:6443"}}}`))

		_, err := dockercontext.Read("k8s-only")

		require.ErrorIs(t, err, dockercontext.ErrNoDockerEndpoint)
	})

	t.Run("a docker endpoint with an empty host has no address", func(t *testing.T) {
		dir := dockerConfigDir(t)
		writeStoredContext(t, dir, "empty", "")

		_, err := dockercontext.Read("empty")

		require.ErrorIs(t, err, dockercontext.ErrNoDockerEndpoint)
	})

	t.Run("the default context is never read from the store", func(t *testing.T) {
		dockerConfigDir(t)

		_, err := dockercontext.Read(dockercontext.DefaultName)

		require.ErrorIs(t, err, dockercontext.ErrNoCurrentContext)
	})

	t.Run("an unparseable context file is an error", func(t *testing.T) {
		dir := dockerConfigDir(t)
		writeStoredContextBytes(t, dir, "broken", []byte("{not json"))

		_, err := dockercontext.Read("broken")

		require.Error(t, err)
		require.NotErrorIs(t, err, dockercontext.ErrContextNotFound)
	})

	t.Run("TLS verification is carried through", func(t *testing.T) {
		dir := dockerConfigDir(t)
		writeStoredContextBytes(t, dir, "insecure",
			[]byte(`{"Name":"insecure","Endpoints":{"docker":{"Host":"tcp://10.0.0.5:2376","SkipTLSVerify":true}}}`))

		got, err := dockercontext.Read("insecure")

		require.NoError(t, err)
		assert.True(t, got.SkipTLSVerify)
	})
}

func TestConfigDirFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DOCKER_CONFIG", "")
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".docker")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	writeCurrentContext(t, dir, "rootless")
	writeStoredContext(t, dir, "rootless", "unix:///run/user/1003/docker.sock")

	got, err := dockercontext.Current()

	require.NoError(t, err)
	assert.Equal(t, "unix:///run/user/1003/docker.sock", got.Host)
}

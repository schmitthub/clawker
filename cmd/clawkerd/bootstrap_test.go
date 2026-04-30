package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
)

func writeBootstrapDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o400))
	}
	return dir
}

func validBootstrapFiles() map[string]string {
	return map[string]string{
		consts.BootstrapCertFile:      "cert-pem",
		consts.BootstrapKeyFile:       "key-pem",
		consts.BootstrapCAFile:        "ca-pem",
		consts.BootstrapAssertionFile: "assertion-jwt\n",
		consts.BootstrapVerifierFile:  "verifier-bytes\n",
	}
}

func TestReadBootstrap_HappyPath(t *testing.T) {
	dir := writeBootstrapDir(t, validBootstrapFiles())

	b, err := readBootstrap(dir)
	require.NoError(t, err)

	assert.Equal(t, []byte("cert-pem"), b.CertPEM)
	assert.Equal(t, []byte("key-pem"), b.KeyPEM)
	assert.Equal(t, []byte("ca-pem"), b.CACertPEM)
	// Assertion + verifier are TrimSpace'd because they're read as
	// strings — leading/trailing whitespace from text-mode tools must
	// not break the JWT parse or the PKCE compare.
	assert.Equal(t, "assertion-jwt", b.Assertion)
	assert.Equal(t, "verifier-bytes", b.Verifier)
}

func TestReadBootstrap_MissingFile(t *testing.T) {
	for missing := range validBootstrapFiles() {
		t.Run("missing "+missing, func(t *testing.T) {
			files := validBootstrapFiles()
			delete(files, missing)
			dir := writeBootstrapDir(t, files)
			_, err := readBootstrap(dir)
			require.Error(t, err)
			assert.Contains(t, err.Error(), missing)
		})
	}
}

func TestReadBootstrap_EmptyFile(t *testing.T) {
	// A zero-byte cert/key/CA/assertion/verifier is structurally
	// useless and would only fail later with confusing parser errors.
	// Reject up front.
	files := validBootstrapFiles()
	files[consts.BootstrapCertFile] = ""
	dir := writeBootstrapDir(t, files)
	_, err := readBootstrap(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

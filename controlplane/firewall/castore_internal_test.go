package firewall

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/logger"
)

func newTestCAStore(t *testing.T, certDirFn func() (string, error)) *CAStore {
	t.Helper()
	store, err := NewCAStore(certDirFn)
	require.NoError(t, err)
	return store
}

func TestSDSServer_DoesNotGenerateCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certs")
	ca := newTestCAStore(t, func() (string, error) { return dir, nil })
	rules, err := NewRulesStoreFromString(sdsCacheTestRules)
	require.NoError(t, err)
	server, err := NewSDSServer(SDSServerDeps{Store: rules, CA: ca, Log: logger.Nop()})
	require.NoError(t, err)
	_, err = server.secretFor("probe.suno.com")
	require.ErrorIs(t, err, ErrNoCA)
	assert.NoDirExists(t, dir)
}

func TestCAStore_EnsureRepairsMismatchedPair(t *testing.T) {
	dir := t.TempDir()
	store := newTestCAStore(t, func() (string, error) { return dir, nil })
	initial, _, err := store.Ensure()
	require.NoError(t, err)
	_, otherKey, err := EnsureCA(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, writeKeyPEM(filepath.Join(dir, caKeyFile), otherKey))

	_, _, err = store.Load()
	require.Error(t, err, "Load must reject a certificate and key that do not match")
	repaired, key, err := store.Ensure()
	require.NoError(t, err)
	assert.NotEqual(t, initial.SerialNumber, repaired.SerialNumber)
	_, _, err = GenerateSNICert(repaired, key, "probe.example.com")
	require.NoError(t, err)
}

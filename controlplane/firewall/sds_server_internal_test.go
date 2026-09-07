package firewall

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/logger"
)

const sdsCacheTestRules = `
rules:
  - dst: .suno.com
    proto: https
`

// newSDSCacheTestServer builds an SDSServer against an in-memory rules store
// and a throwaway cert dir, for direct secretFor exercise.
func newSDSCacheTestServer(t *testing.T) *SDSServer {
	t.Helper()
	store, err := NewRulesStoreFromString(sdsCacheTestRules)
	require.NoError(t, err)
	certDir := t.TempDir()
	srv, err := NewSDSServer(SDSServerDeps{
		Store:     store,
		CertDirFn: func() (string, error) { return certDir, nil },
		Log:       logger.Nop(),
	})
	require.NoError(t, err)
	return srv
}

// The SNI is peer-controlled: any name under an admitted wildcard zone mints
// a cache entry, so the cache must stay bounded and an eviction must never
// turn into a denial — admitted() is the only gate.
func TestSDSServer_MintCacheBoundedAndEvictionRemints(t *testing.T) {
	srv := newSDSCacheTestServer(t)

	total := sdsCacheMaxEntries + 8
	for i := range total {
		_, err := srv.secretFor(fmt.Sprintf("h%d.suno.com", i))
		require.NoError(t, err)
	}
	assert.Equal(t, sdsCacheMaxEntries, srv.cache.Len())

	// h0 was evicted (least recently used). Its next request re-mints
	// successfully instead of being denied.
	entry, err := srv.secretFor("h0.suno.com")
	require.NoError(t, err)
	assert.NotNil(t, entry.resource)

	// A resident SNI still answers from cache: identical version and bytes.
	last := fmt.Sprintf("h%d.suno.com", total-1)
	first, err := srv.secretFor(last)
	require.NoError(t, err)
	second, err := srv.secretFor(last)
	require.NoError(t, err)
	assert.Equal(t, first.version, second.version)
	assert.Equal(t, first.resource.GetValue(), second.resource.GetValue())
}

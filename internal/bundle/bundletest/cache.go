package bundletest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/consts"
)

// handPlacedKey is the entry directory used for a hand-placed (unmanaged)
// cache entry — an arbitrary key no declared source value digests to.
const handPlacedKey = "handplaced00"

// PlantCachedBundle writes a value-keyed bundle cache entry under
// [consts.BundlesSubdir] exactly as an install would leave it: the entry
// directory <ns>/<name>/<sourceKey> carrying the identity manifest, the given
// component files, and — unless url is empty (a hand-placed, unmanaged entry) —
// the fetch receipt for the source url at ref v1. A test declaring
// {URL: url, Ref: "v1"} therefore addresses the planted entry by exact value.
// components maps entry-root-relative file paths to contents, e.g.
// "stacks/x/stack.yaml" → "description: x\n".
func PlantCachedBundle(t *testing.T, ns, name, version, url string, components map[string]string) {
	t.Helper()
	PlantCachedBundleSource(t, ns, name, version,
		bundle.Source{URL: url, Ref: "v1", SHA: "", Path: ""}, components)
}

// PlantCachedBundleSource is PlantCachedBundle for an arbitrary source value:
// the entry lands at the exact key the given source digests to, with a receipt
// for its canonical form. An empty-URL source plants a hand-placed (unmanaged)
// entry with no receipt.
func PlantCachedBundleSource(t *testing.T, ns, name, version string, src bundle.Source, components map[string]string) {
	t.Helper()
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	key := handPlacedKey
	if src.URL != "" {
		key = src.Key()
	}
	entryRoot := filepath.Join(cacheRoot, ns, name, key)
	require.NoError(t, os.MkdirAll(filepath.Join(entryRoot, ".clawker-bundle"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(entryRoot, ".clawker-bundle", "bundle.yaml"),
		[]byte("namespace: "+ns+"\nname: "+name+"\nversion: "+version+"\n"), 0o600))
	for rel, content := range components {
		path := filepath.Join(entryRoot, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	if src.URL == "" {
		return
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(entryRoot, bundle.ReceiptFile),
		[]byte(
			"canonical: \""+src.Canonical()+"\"\nsha: \"\"\nfetched_at: 2026-01-01T00:00:00Z\nversion: "+version+"\n",
		),
		0o600,
	))
}

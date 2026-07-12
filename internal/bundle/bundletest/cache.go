package bundletest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
)

// PlantCachedBundle writes a bundle cache entry under [consts.BundlesSubdir]
// exactly as an install would leave it: a version content root carrying the
// identity manifest plus the given component files, and — unless url is empty
// (a hand-placed, unmanaged entry) — the source.yaml linking the entry to url
// at ref v1 with the version recorded under that pin. components maps
// version-root-relative file paths to contents, e.g.
// "stacks/x/stack.yaml" → "description: x\n".
func PlantCachedBundle(t *testing.T, ns, name, version, url string, components map[string]string) {
	t.Helper()
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	verRoot := filepath.Join(cacheRoot, ns, name, version)
	require.NoError(t, os.MkdirAll(filepath.Join(verRoot, ".clawker-bundle"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(verRoot, ".clawker-bundle", "bundle.yaml"),
		[]byte("namespace: "+ns+"\nname: "+name+"\nversion: "+version+"\n"), 0o600))
	for rel, content := range components {
		path := filepath.Join(verRoot, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	if url == "" {
		return
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(cacheRoot, ns, name, "source.yaml"),
		[]byte(
			"url: "+url+"\nref: v1\nversions:\n  \""+version+"\":\n    sha: \"\"\n    fetched_at: 2026-01-01T00:00:00Z\n",
		),
		0o600,
	))
}

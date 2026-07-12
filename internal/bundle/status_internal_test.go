package bundle

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
)

// Statuses links every declaration↔cache state into one honest per-identity
// view: resolving (in-place and installed), declared-but-uncached,
// cached-but-undeclared, and hand-placed (unmanaged).
func TestManager_Statuses(t *testing.T) {
	f := newResolverFixture(t)

	// Declared + cached → resolving (installed).
	f.installBundleStack(t)
	// Cached with metadata, declaration removed → undeclared.
	f.cacheBundleStack(t, "other", "extra", "2.0.0", "go")
	f.cacheSourceMeta(t, "other", "extra", "https://example.com/other/extra.git", "v2",
		map[string]time.Time{"2.0.0": time.Now()})
	// Cached without metadata → unmanaged.
	f.cacheBundleStack(t, "hand", "placed", "0.1.0", "rust")
	// Declared, never fetched → not installed.
	f.declareRemote(t, "https://example.com/acme/missing.git", "v9")
	// In-place declaration → resolving (in-place).
	inPlace := filepath.Join(f.projectDir, "vendor", "dev")
	writeManifest(t, inPlace, "namespace: dev\nname: loop\nversion: 0.0.1\n")
	writeFile(t, inPlace, "stacks/node/stack.yaml", "description: dev\n")
	f.decls = append(f.decls, config.BundleDeclaration{
		Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/dev"},
		File:   filepath.Join(f.projectDir, "clawker.yaml"),
	})

	m := &Manager{cfg: f.cfg, resolver: f.r, fetcher: nil}
	rows, err := m.Statuses()
	require.NoError(t, err)

	byKey := map[string]Status{}
	for _, s := range rows {
		byKey[s.ID.String()+"|"+s.Source] = s
	}
	require.Len(t, rows, 5)

	installed := byKey["acme.tools|git:https://example.com/acme/tools.git//@ref:v1"]
	assert.Equal(t, StatusResolving, installed.State)
	assert.Equal(t, TierInstalled, installed.Tier)
	assert.Equal(t, "1.0.0", installed.Version)
	assert.Contains(t, installed.File, "clawker.yaml")

	undeclared := byKey["other.extra|git:https://example.com/other/extra.git//@ref:v2"]
	assert.Equal(t, StatusUndeclared, undeclared.State)
	assert.Empty(t, undeclared.File)

	unmanaged := byKey["hand.placed|"]
	assert.Equal(t, StatusUnmanaged, unmanaged.State)

	missing := byKey[".|git:https://example.com/acme/missing.git//@ref:v9"]
	assert.Equal(t, StatusNotInstalled, missing.State)
	assert.Equal(t, BundleID{Namespace: "", Name: ""}, missing.ID, "identity is unknown until fetched")

	inPlaceRow := byKey["dev.loop|path:"+inPlace]
	assert.Equal(t, StatusResolving, inPlaceRow.State)
	assert.Equal(t, TierInPlace, inPlaceRow.Tier)
	assert.Equal(t, "0.0.1", inPlaceRow.Version, "an in-place bundle reports its manifest version")
}

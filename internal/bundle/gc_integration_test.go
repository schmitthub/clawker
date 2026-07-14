package bundle_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundle/bundletest"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
)

// declRoot materializes a "registered project root" fixture: a directory whose
// flat dotted clawker.yaml declares the given bundles: node.
func declRoot(t *testing.T, bundlesYAML string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "."+consts.ProjectConfigFile), []byte(bundlesYAML), 0o644))
	return dir
}

// managerWithRoots wires a Manager over the CURRENT testenv with the given
// current-config declarations and a registered-roots provider listing the
// fixture root dirs.
func managerWithRoots(decls []config.BundleSource, rootDirs ...string) *bundle.Manager {
	cfg := configmocks.NewBlankConfig()
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		out := make([]config.BundleDeclaration, 0, len(decls))
		for _, s := range decls {
			out = append(out, config.BundleDeclaration{Source: s, File: "clawker.yaml"})
		}
		return out
	}
	cfg.ProjectRootFunc = func() string { return "" }
	return bundle.NewManager(cfg, bundle.WithRegisteredRoots(
		func(context.Context) ([]string, error) { return rootDirs, nil }))
}

// plantedEntryRoot resolves the cache entry dir a planted acme-namespace
// source occupies.
func plantedEntryRoot(t *testing.T, name string, src bundle.Source) string {
	t.Helper()
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	return filepath.Join(cacheRoot, "acme", name, src.Key())
}

func TestManager_Prune_CollectsUndeclaredValues(t *testing.T) {
	testenv.New(t)
	live := bundle.Source{URL: "https://x/tools.git", Ref: "v1", SHA: "", Path: ""}
	stale := bundle.Source{URL: "https://x/tools.git", Ref: "v0", SHA: "", Path: ""}
	bundletest.PlantCachedBundleSource(t, "acme", "tools", "1.0.0", live,
		map[string]string{"stacks/node/stack.yaml": "description: node\n"})
	bundletest.PlantCachedBundleSource(t, "acme", "tools", "0.9.0", stale,
		map[string]string{"stacks/node/stack.yaml": "description: node\n"})
	// A hand-placed (receipt-less) entry is not refetchable, so GC must never
	// collect it — it is a user artifact, not cache.
	bundletest.PlantCachedBundle(t, "acme", "tools", "0.0.1", "",
		map[string]string{"stacks/hand/stack.yaml": "description: hand\n"})

	mgr := managerWithRoots([]config.BundleSource{
		{URL: live.URL, Ref: live.Ref, SHA: "", Path: "", AutoUpdate: false},
	})
	report, err := mgr.Prune(context.Background())
	require.NoError(t, err)

	assert.DirExists(t, plantedEntryRoot(t, "tools", live))
	assert.NoDirExists(t, plantedEntryRoot(t, "tools", stale))
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	assert.DirExists(t, filepath.Join(cacheRoot, "acme", "tools", "handplaced00"))

	require.Len(t, report.Drops, 1)
	assert.Equal(t, bundle.BundleID{Namespace: "acme", Name: "tools"}, report.Drops[0].ID)
	assert.Equal(t, stale.Key(), report.Drops[0].Key)
	assert.Equal(t, stale.Canonical(), report.Drops[0].Source)
	assert.Empty(t, report.MultiSource)
}

func TestManager_Prune_CrossProjectRootsKeepEntries(t *testing.T) {
	testenv.New(t)
	mine := bundle.Source{URL: "https://x/tools.git", Ref: "v1", SHA: "", Path: ""}
	theirs := bundle.Source{URL: "git@github.com:fork/tools.git", Ref: "v1", SHA: "", Path: ""}
	bundletest.PlantCachedBundleSource(t, "acme", "tools", "1.0.0", mine,
		map[string]string{"stacks/node/stack.yaml": "description: node\n"})
	bundletest.PlantCachedBundleSource(t, "acme", "tools", "1.0.0", theirs,
		map[string]string{"stacks/node/stack.yaml": "description: node\n"})

	otherRoot := declRoot(t, "bundles:\n  - url: git@github.com:fork/tools.git\n    ref: v1\n")
	mgr := managerWithRoots([]config.BundleSource{
		{URL: mine.URL, Ref: mine.Ref, SHA: "", Path: "", AutoUpdate: false},
	}, otherRoot)

	report, err := mgr.Prune(context.Background())
	require.NoError(t, err)

	// Both values are rooted — one by the current config, one by the other
	// registered project — so nothing is collected.
	assert.Empty(t, report.Drops)
	assert.DirExists(t, plantedEntryRoot(t, "tools", mine))
	assert.DirExists(t, plantedEntryRoot(t, "tools", theirs))

	// One identity cached from two distinct repositories across projects is
	// the mirror-attack anomaly surface prune must surface.
	require.Len(t, report.MultiSource, 1)
	ms := report.MultiSource[0]
	assert.Equal(t, bundle.BundleID{Namespace: "acme", Name: "tools"}, ms.ID)
	require.Len(t, ms.Repos, 2)
	repos := []string{ms.Repos[0].Repository, ms.Repos[1].Repository}
	assert.Contains(t, repos, "https://x/tools.git")
	assert.Contains(t, repos, "git@github.com:fork/tools.git")
	for _, r := range ms.Repos {
		require.Len(t, r.Files, 1)
		if r.Repository == "https://x/tools.git" {
			assert.Equal(t, "clawker.yaml", r.Files[0])
		} else {
			assert.Equal(t, filepath.Join(otherRoot, "."+consts.ProjectConfigFile), r.Files[0])
		}
	}
}

func TestManager_Prune_SameRepoTwoPinsIsNotMultiSource(t *testing.T) {
	testenv.New(t)
	v1 := bundle.Source{URL: "https://x/tools.git", Ref: "v1", SHA: "", Path: ""}
	v2 := bundle.Source{URL: "https://x/tools.git", Ref: "v2", SHA: "", Path: ""}
	bundletest.PlantCachedBundleSource(t, "acme", "tools", "1.0.0", v1,
		map[string]string{"stacks/node/stack.yaml": "description: node\n"})
	bundletest.PlantCachedBundleSource(t, "acme", "tools", "2.0.0", v2,
		map[string]string{"stacks/node/stack.yaml": "description: node\n"})

	otherRoot := declRoot(t, "bundles:\n  - url: https://x/tools.git\n    ref: v2\n")
	mgr := managerWithRoots([]config.BundleSource{
		{URL: v1.URL, Ref: v1.Ref, SHA: "", Path: "", AutoUpdate: false},
	}, otherRoot)

	report, err := mgr.Prune(context.Background())
	require.NoError(t, err)
	assert.Empty(t, report.Drops)
	// Two pins of ONE repository are ordinary multi-project coexistence, not
	// the two-repositories anomaly.
	assert.Empty(t, report.MultiSource)
}

func TestManager_Prune_DropsWholeIdentityAndCleansDirs(t *testing.T) {
	testenv.New(t)
	gone := bundle.Source{URL: "https://x/gone.git", Ref: "v1", SHA: "", Path: ""}
	bundletest.PlantCachedBundleSource(t, "beta", "gone", "1.0.0", gone,
		map[string]string{"stacks/node/stack.yaml": "description: node\n"})

	mgr := managerWithRoots(nil)
	report, err := mgr.Prune(context.Background())
	require.NoError(t, err)

	require.Len(t, report.Drops, 1)
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	// The emptied identity and namespace directories are cleaned up with the
	// last entry, so the cache tree never accumulates empty husks.
	assert.NoDirExists(t, filepath.Join(cacheRoot, "beta", "gone"))
	assert.NoDirExists(t, filepath.Join(cacheRoot, "beta"))
}

func TestManager_Prune_RootsUnavailable(t *testing.T) {
	t.Run("no roots provider", func(t *testing.T) {
		testenv.New(t)
		stale := bundle.Source{URL: "https://x/tools.git", Ref: "v0", SHA: "", Path: ""}
		bundletest.PlantCachedBundleSource(t, "acme", "tools", "0.9.0", stale,
			map[string]string{"stacks/node/stack.yaml": "description: node\n"})

		mgr := managerForDecls()
		_, err := mgr.Prune(context.Background())
		require.Error(t, err)
		assert.DirExists(t, plantedEntryRoot(t, "tools", stale))
	})

	t.Run("provider error collects nothing", func(t *testing.T) {
		testenv.New(t)
		stale := bundle.Source{URL: "https://x/tools.git", Ref: "v0", SHA: "", Path: ""}
		bundletest.PlantCachedBundleSource(t, "acme", "tools", "0.9.0", stale,
			map[string]string{"stacks/node/stack.yaml": "description: node\n"})

		cfg := configmocks.NewBlankConfig()
		cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration { return nil }
		cfg.ProjectRootFunc = func() string { return "" }
		mgr := bundle.NewManager(cfg, bundle.WithRegisteredRoots(
			func(context.Context) ([]string, error) { return nil, errors.New("registry unreadable") }))

		_, err := mgr.Prune(context.Background())
		require.ErrorContains(t, err, "registry unreadable")
		assert.DirExists(t, plantedEntryRoot(t, "tools", stale))
	})

	t.Run("malformed registered root collects nothing", func(t *testing.T) {
		testenv.New(t)
		stale := bundle.Source{URL: "https://x/tools.git", Ref: "v0", SHA: "", Path: ""}
		bundletest.PlantCachedBundleSource(t, "acme", "tools", "0.9.0", stale,
			map[string]string{"stacks/node/stack.yaml": "description: node\n"})

		badRoot := declRoot(t, "bundles: notalist\n")
		mgr := managerWithRoots(nil, badRoot)
		_, err := mgr.Prune(context.Background())
		require.Error(t, err)
		assert.DirExists(t, plantedEntryRoot(t, "tools", stale))
	})
}

func TestManager_AutoGC_IdentityScoped(t *testing.T) {
	testenv.New(t)
	toolsStale := bundle.Source{URL: "https://x/tools.git", Ref: "v0", SHA: "", Path: ""}
	extraStale := bundle.Source{URL: "https://x/extra.git", Ref: "v0", SHA: "", Path: ""}
	bundletest.PlantCachedBundleSource(t, "acme", "tools", "0.9.0", toolsStale,
		map[string]string{"stacks/node/stack.yaml": "description: node\n"})
	bundletest.PlantCachedBundleSource(t, "acme", "extra", "0.9.0", extraStale,
		map[string]string{"stacks/node/stack.yaml": "description: node\n"})

	mgr := managerWithRoots(nil)
	warnings := mgr.AutoGC(context.Background(), bundle.BundleID{Namespace: "acme", Name: "tools"})

	// Only the touched identity is reconciled — the other identity's stale
	// entry waits for its own touch or an explicit prune.
	assert.NoDirExists(t, plantedEntryRoot(t, "tools", toolsStale))
	assert.DirExists(t, plantedEntryRoot(t, "extra", extraStale))
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0].Message, "acme.tools")

	t.Run("without a roots provider AutoGC is off", func(t *testing.T) {
		plain := managerForDecls()
		got := plain.AutoGC(context.Background(), bundle.BundleID{Namespace: "acme", Name: "extra"})
		assert.Empty(t, got)
		assert.DirExists(t, plantedEntryRoot(t, "extra", extraStale))
	})

	t.Run("unknown identity is a no-op", func(t *testing.T) {
		got := mgr.AutoGC(context.Background(), bundle.BundleID{Namespace: "no", Name: "such"})
		assert.Empty(t, got)
	})
}

func TestManager_AutoUpdateCheck_PrunesRefetchedIdentity(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))

	testenv.New(t)
	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "master", SHA: "", Path: "", AutoUpdate: true}
	mgr := managerWithRoots([]config.BundleSource{src})
	ctx := context.Background()
	_, err := mgr.Install(ctx, src)
	require.NoError(t, err)

	// A stranded sibling of the SAME identity, no longer declared anywhere.
	stale := bundle.Source{URL: srv.HTTPURL("tools"), Ref: "v0", SHA: "", Path: ""}
	bundletest.PlantCachedBundleSource(t, "acme", "tools", "0.9.0", stale,
		map[string]string{"stacks/node/stack.yaml": "description: node\n"})

	repo.Commit(t, "v2", bundleFiles("2.0.0"))
	warnings := mgr.AutoUpdateCheck(ctx)

	assert.NoDirExists(t, plantedEntryRoot(t, "tools", stale),
		"the refetched identity's stranded sibling must be reconciled")
	msgs := make([]string, 0, len(warnings))
	for _, w := range warnings {
		msgs = append(msgs, w.Message)
	}
	joined := strings.Join(msgs, "\n")
	assert.Contains(t, joined, "auto-updated")
	assert.Contains(t, joined, stale.Key(), "the collected entry is reported")
}

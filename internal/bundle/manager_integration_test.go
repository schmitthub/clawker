package bundle_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundle/bundletest"
	"github.com/schmitthub/clawker/internal/bundle/componentcheck"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
)

// bundleFiles is a minimal single-stack bundle: one convention dir plus the
// marker dir, no bare-manifest-at-root form.
func bundleFiles(version string) map[string]string {
	manifest := "namespace: acme\nname: tools\n"
	if version != "" {
		manifest += "version: " + version + "\n"
	}
	return map[string]string{
		".clawker-bundle/bundle.yaml":            manifest,
		"stacks/node/stack.yaml":                 "description: node stack\n",
		"stacks/node/Dockerfile.stack-root.tmpl": "RUN true\n",
	}
}

// newManager wires a Manager over an isolated data-dir cache and the given
// declared sources.
func newManager(t *testing.T, decls []config.BundleSource) *bundle.Manager {
	t.Helper()
	testenv.New(t)
	return managerForDecls(decls...)
}

// entryRoot resolves the value-keyed cache entry directory a declared source
// addresses (all fixtures ship the acme.tools identity).
func entryRoot(t *testing.T, src config.BundleSource) string {
	t.Helper()
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	return filepath.Join(cacheRoot, "acme", "tools", bundle.SourceFromConfig(src).Key())
}

// entryManifest reads the identity manifest of a cache entry, for asserting
// which content a refetch left behind.
func entryManifest(t *testing.T, entry string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(entry, bundle.MarkerDir, bundle.ManifestFile))
	require.NoError(t, err)
	return string(raw)
}

func TestManager_Install_HTTPJourney(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))
	repo.Tag(t, "v1.0.0")

	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{src})
	mustInstall(t, mgr, src)

	entry := entryRoot(t, src)
	assert.FileExists(t, filepath.Join(entry, "stacks", "node", "stack.yaml"))
	assert.FileExists(t, filepath.Join(entry, bundle.ReceiptFile))
	assert.NoDirExists(t, filepath.Join(entry, ".git"))

	comp, err := mgr.Resolver().Resolve(bundle.ComponentStack, "acme.tools.node")
	require.NoError(t, err)
	assert.Equal(t, "acme.tools.node", comp.Address.String())

	t.Run("reinstall of same source is idempotent", func(t *testing.T) {
		mustInstall(t, mgr, src)
		assert.FileExists(t, filepath.Join(entry, "stacks", "node", "stack.yaml"))
	})
}

func TestManager_Install_RejectsInvalidComponent(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	// A stack with no Dockerfile fragment loads structurally but breaks at
	// consumption — install must reject it before anything hits the cache.
	repo.Commit(t, "v1", map[string]string{
		".clawker-bundle/bundle.yaml": "namespace: acme\nname: tools\n",
		"stacks/node/stack.yaml":      "description: node stack\n",
	})
	repo.Tag(t, "v1.0.0")

	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{src})

	_, err := mgr.Install(context.Background(), src)
	require.ErrorContains(t, err, "no fragment found")
	assert.NoDirExists(t, entryRoot(t, src), "a rejected bundle must not be committed to the cache")
}

func TestManager_Install_Subdir(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "mono")
	repo.Commit(t, "monorepo", map[string]string{
		"bundles/tools/.clawker-bundle/bundle.yaml":            "namespace: acme\nname: tools\nversion: 1.0.0\n",
		"bundles/tools/stacks/node/stack.yaml":                 "description: node\n",
		"bundles/tools/stacks/node/Dockerfile.stack-root.tmpl": "RUN true\n",
		"unrelated/readme.md":                                  "ignore me\n",
	})
	repo.Tag(t, "v1.0.0")

	src := config.BundleSource{
		URL: srv.HTTPURL("mono"), Ref: "v1.0.0", SHA: "", Path: "bundles/tools", AutoUpdate: false,
	}
	mgr := newManager(t, []config.BundleSource{src})
	mustInstall(t, mgr, src)

	comp, err := mgr.Resolver().Resolve(bundle.ComponentStack, "acme.tools.node")
	require.NoError(t, err)
	assert.Equal(t, "acme.tools.node", comp.Address.String())
}

func TestManager_Install_SHAPin(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	sha := repo.Commit(t, "v1", bundleFiles(""))

	testenv.New(t)
	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "", SHA: sha, Path: "", AutoUpdate: false}
	mgr := managerForDecls(src)
	mustInstall(t, mgr, src)

	entry := entryRoot(t, src)
	assert.FileExists(t, filepath.Join(entry, "stacks", "node", "stack.yaml"))

	// No manifest version → the receipt's display version is the resolved sha,
	// and the resolved bundle reports it.
	bundles, _, err := mgr.Resolver().Bundles()
	require.NoError(t, err)
	rb, ok := bundles[bundle.BundleID{Namespace: "acme", Name: "tools"}]
	require.True(t, ok)
	assert.Equal(t, sha, rb.Version)
}

// Two different repositories shipping the same identity install side by side —
// the cache is value-keyed, so install never blocks on identity. The collision
// surfaces at RESOLVE, where two declared sources yielding one identity in one
// scope are a C1 hard error.
func TestManager_Install_SameIdentityTwoSources(t *testing.T) {
	srv := bundletest.New(t)
	a := srv.InitRepo(t, "a")
	a.Commit(t, "v1", bundleFiles("1.0.0"))
	a.Tag(t, "v1.0.0")
	b := srv.InitRepo(t, "b")
	b.Commit(t, "v1", bundleFiles("1.0.0"))
	b.Tag(t, "v1.0.0")

	srcA := config.BundleSource{URL: srv.HTTPURL("a"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
	srcB := config.BundleSource{URL: srv.HTTPURL("b"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{srcA, srcB})
	mustInstall(t, mgr, srcA)
	mustInstall(t, mgr, srcB, "a second source of the same identity installs its own entry")
	assert.DirExists(t, entryRoot(t, srcA))
	assert.DirExists(t, entryRoot(t, srcB))

	// Both declared in one scope → C1 at resolve.
	_, _, err := mgr.Resolver().Bundles()
	var collision *bundle.CollisionError
	require.ErrorAs(t, err, &collision)
	assert.Equal(t, "acme.tools", collision.Identity.String())
}

// TestManager_Install_RepinAddressesNewEntry pins the value-keyed re-pin flow:
// editing a declaration's ref (v1→v2) and installing again addresses a NEW
// cache entry; the v1 entry stays untouched for anything still declaring it.
func TestManager_Install_RepinAddressesNewEntry(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))
	repo.Tag(t, "v1.0.0")
	repo.Commit(t, "v2", bundleFiles("2.0.0"))
	repo.Tag(t, "v2.0.0")

	url := srv.HTTPURL("tools")
	v1 := config.BundleSource{URL: url, Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
	v2 := config.BundleSource{URL: url, Ref: "v2.0.0", SHA: "", Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{v2})
	mustInstall(t, mgr, v1)
	mustInstall(t, mgr, v2)

	// Sibling entries; the live v2 declaration resolves its own entry's version.
	assert.FileExists(t, filepath.Join(entryRoot(t, v1), "stacks", "node", "stack.yaml"))
	assert.FileExists(t, filepath.Join(entryRoot(t, v2), "stacks", "node", "stack.yaml"))

	bundles, _, err := mgr.Resolver().Bundles()
	require.NoError(t, err)
	rb, ok := bundles[bundle.BundleID{Namespace: "acme", Name: "tools"}]
	require.True(t, ok, "the re-pinned declaration must resolve its own cache entry")
	assert.Equal(t, "2.0.0", rb.Version)
}

// TestManager_MultiPinCoexistence pins the locked-spec promise "versions
// coexist; project A pins v1, project B v2": two projects sharing one host
// cache, each declaring the same repository at a different pin, BOTH resolve —
// each to the version fetched under its own pin. One project's install never
// unresolves the other's declaration.
func TestManager_MultiPinCoexistence(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))
	repo.Tag(t, "v1.0.0")
	repo.Commit(t, "v2", bundleFiles("2.0.0"))
	repo.Tag(t, "v2.0.0")

	testenv.New(t) // ONE shared host cache for both "projects"
	url := srv.HTTPURL("tools")
	v1 := config.BundleSource{URL: url, Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
	v2 := config.BundleSource{URL: url, Ref: "v2.0.0", SHA: "", Path: "", AutoUpdate: false}
	projectA := managerForDecls(v1)
	projectB := managerForDecls(v2)
	mustInstall(t, projectA, v1)
	mustInstall(t, projectB, v2, "B's re-pin of the shared entry must not collide")

	id := bundle.BundleID{Namespace: "acme", Name: "tools"}
	bundlesA, _, err := projectA.Resolver().Bundles()
	require.NoError(t, err)
	rbA, ok := bundlesA[id]
	require.True(t, ok, "A's v1 declaration must keep resolving after B re-pinned the shared cache entry")
	assert.Equal(t, "1.0.0", rbA.Version, "A resolves the version fetched under ITS pin")

	bundlesB, _, err := projectB.Resolver().Bundles()
	require.NoError(t, err)
	rbB, ok := bundlesB[id]
	require.True(t, ok)
	assert.Equal(t, "2.0.0", rbB.Version, "B resolves the version fetched under ITS pin")
}

// TestManager_Install_SubdirsAreDistinctSources: two subdirs of one repository
// are DIFFERENT source values — each installs its own value-keyed entry, and
// declaring both in one scope is a C1 collision when they ship one identity.
func TestManager_Install_SubdirsAreDistinctSources(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "mono")
	files := map[string]string{}
	for path, content := range bundleFiles("1.0.0") {
		files["bundles/one/"+path] = content
		files["bundles/two/"+path] = content
	}
	repo.Commit(t, "v1", files)
	repo.Tag(t, "v1.0.0")

	url := srv.HTTPURL("mono")
	one := config.BundleSource{URL: url, Ref: "v1.0.0", SHA: "", Path: "bundles/one", AutoUpdate: false}
	two := config.BundleSource{URL: url, Ref: "v1.0.0", SHA: "", Path: "bundles/two", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{one, two})
	mustInstall(t, mgr, one)
	mustInstall(t, mgr, two)
	assert.NotEqual(t, entryRoot(t, one), entryRoot(t, two))

	_, _, err := mgr.Resolver().Bundles()
	var collision *bundle.CollisionError
	require.ErrorAs(t, err, &collision, "two declared subdirs shipping one identity must collide at resolve")
}

// mustInstall installs src and fails the test on error, discarding the
// returned identity (the fixtures all ship acme.tools).
func mustInstall(t *testing.T, mgr *bundle.Manager, src config.BundleSource, msgAndArgs ...any) {
	t.Helper()
	_, err := mgr.Install(context.Background(), src)
	require.NoError(t, err, msgAndArgs...)
}

// managerForDecls wires a Manager over the CURRENT testenv (callers own the
// isolation), so several managers — several "projects" — can share one host
// cache.
func managerForDecls(decls ...config.BundleSource) *bundle.Manager {
	cfg := configmocks.NewBlankConfig()
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		out := make([]config.BundleDeclaration, 0, len(decls))
		for _, s := range decls {
			out = append(out, config.BundleDeclaration{Source: s, File: "clawker.yaml"})
		}
		return out
	}
	cfg.ProjectRootFunc = func() string { return "" }
	return bundle.NewManager(cfg, componentcheck.Validate)
}

func TestManager_Update_RefetchesOnDrift(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))

	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "master", SHA: "", Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{src})
	ctx := context.Background()
	mustInstall(t, mgr, src)

	id := bundle.BundleID{Namespace: "acme", Name: "tools"}
	entry := entryRoot(t, src)

	t.Run("no drift is a no-op", func(t *testing.T) {
		results, err := mgr.Update(ctx, id)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, bundle.UpdateUnchanged, results[0].Outcome)
	})

	// Move the branch tip: a new commit bumps the manifest version.
	repo.Commit(t, "v2", bundleFiles("2.0.0"))

	t.Run("drift refetches the entry in place", func(t *testing.T) {
		results, err := mgr.Update(ctx, id)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, bundle.UpdateRefetched, results[0].Outcome)
		assert.Equal(t, "2.0.0", results[0].NewVersion)
		assert.Contains(t, entryManifest(t, entry), "version: 2.0.0")
	})
}

func TestManager_Update_FailureKeepsCache(t *testing.T) {
	srv := bundletest.New(t)
	// The declared repository does not exist on the server, so the update's
	// tip resolve fails; the entry was planted at the declaration's exact key,
	// exactly as a prior successful install would have left it.
	src := config.BundleSource{URL: srv.HTTPURL("gone"), Ref: "v1", SHA: "", Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{src})
	bundletest.PlantCachedBundle(
		t,
		"acme",
		"tools",
		"1.0.0",
		srv.HTTPURL("gone"),
		map[string]string{
			"stacks/node/stack.yaml":                 "description: node stack\n",
			"stacks/node/Dockerfile.stack-root.tmpl": "RUN true\n",
		},
	)

	id := bundle.BundleID{Namespace: "acme", Name: "tools"}
	results, err := mgr.Update(context.Background(), id)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, bundle.UpdateFailed, results[0].Outcome)
	require.Error(t, results[0].Err)

	// Cache still serves the originally installed content.
	entry := entryRoot(t, src)
	assert.FileExists(t, filepath.Join(entry, "stacks", "node", "stack.yaml"))
}

func TestManager_AutoUpdateCheck_OptInOnly(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))

	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "master", SHA: "", Path: "", AutoUpdate: true}
	mgr := newManager(t, []config.BundleSource{src})
	ctx := context.Background()
	mustInstall(t, mgr, src)

	t.Run("no drift yields no warnings", func(t *testing.T) {
		assert.Empty(t, mgr.AutoUpdateCheck(ctx))
	})

	repo.Commit(t, "v2", bundleFiles("2.0.0"))

	t.Run("drift on an opted-in bundle refetches with a warning", func(t *testing.T) {
		warnings := mgr.AutoUpdateCheck(ctx)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0].Message, "auto-updated")
		assert.Contains(t, entryManifest(t, entryRoot(t, src)), "version: 2.0.0")
	})
}

func TestManager_AutoUpdateCheck_IgnoresUndeclaredOptIn(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))

	// Installed, but declared WITHOUT auto_update → never auto-checked.
	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "master", SHA: "", Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{src})
	ctx := context.Background()
	mustInstall(t, mgr, src)

	repo.Commit(t, "v2", bundleFiles("2.0.0"))

	assert.Empty(t, mgr.AutoUpdateCheck(ctx))
}

func TestManager_InstallDeclared_FetchesMissing(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))
	repo.Tag(t, "v1.0.0")

	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{src})
	ctx := context.Background()

	installed, err := mgr.InstallDeclared(ctx)
	require.NoError(t, err)
	require.Len(t, installed, 1)
	assert.Equal(t, "acme.tools", installed[0].String())

	// Second pass: already cached, nothing to do.
	installed, err = mgr.InstallDeclared(ctx)
	require.NoError(t, err)
	assert.Empty(t, installed)
}

// TestManager_Install_InvalidManifestNoCommit drives the distinct hard-fail
// manifest branches through a real fetch and pins the atomic-commit contract:
// validation happens BEFORE the rename, so a rejected bundle leaves no trace in
// the cache.
func TestManager_Install_InvalidManifestNoCommit(t *testing.T) {
	cases := map[string]struct {
		manifest  string
		absentDir string // cache namespace dir that must not exist afterwards
	}{
		"reserved namespace": {
			manifest:  "namespace: clawker\nname: tools\n",
			absentDir: "clawker",
		},
		"missing namespace": {
			manifest:  "name: tools\n",
			absentDir: "acme",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := bundletest.New(t)
			repo := srv.InitRepo(t, "bad")
			repo.Commit(t, "v1", map[string]string{
				filepath.Join(bundle.MarkerDir, bundle.ManifestFile): tc.manifest,
				"stacks/node/stack.yaml":                             "description: node\n",
				"stacks/node/Dockerfile.stack-root.tmpl":             "RUN true\n",
			})
			repo.Tag(t, "v1.0.0")

			mgr := newManager(t, nil)
			_, err := mgr.Install(context.Background(), config.BundleSource{
				URL: srv.HTTPURL("bad"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false,
			})
			var manifestErr *bundle.ManifestError
			require.ErrorAs(t, err, &manifestErr)

			cacheRoot, rootErr := consts.BundlesSubdir()
			require.NoError(t, rootErr)
			assert.NoDirExists(t, filepath.Join(cacheRoot, tc.absentDir))
		})
	}
}

func TestManager_Update_SHAPinStays(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	sha := repo.Commit(t, "v1", bundleFiles(""))

	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "", SHA: sha, Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{src})
	ctx := context.Background()
	mustInstall(t, mgr, src)

	// The upstream moves on, but a sha-pinned source is reproducible: update
	// leaves the pin untouched and fetches nothing new.
	repo.Commit(t, "v2", bundleFiles("2.0.0"))

	results, err := mgr.Update(ctx, bundle.BundleID{Namespace: "acme", Name: "tools"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, bundle.UpdateSkippedPinned, results[0].Outcome)

	// The pinned entry is the only cache entry — no drift was pulled in.
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	entries, err := os.ReadDir(filepath.Join(cacheRoot, "acme", "tools"))
	require.NoError(t, err)
	var keys []string
	for _, e := range entries {
		if e.IsDir() {
			keys = append(keys, e.Name())
		}
	}
	assert.Equal(t, []string{bundle.SourceFromConfig(src).Key()}, keys)
}

func TestManager_Install_UnreachableSource(t *testing.T) {
	srv := bundletest.New(t)
	// A repository that was never initialized on the server: the clone fails.
	mgr := newManager(t, nil)
	_, err := mgr.Install(context.Background(), config.BundleSource{
		URL: srv.HTTPURL("nonexistent"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false,
	})
	var srcErr *bundle.SourceError
	require.ErrorAs(t, err, &srcErr)

	// A failed fetch commits nothing: the cache has no bundle namespace.
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(cacheRoot, "acme"))
}

// An unpinned source (url with no ref/sha) tracks the repository's default
// branch: install clones its tip, resolution passes the declaration gate, and
// update/auto-update refetch when the branch moves — the CC-literal
// unpinned-plugin behavior.
func TestManager_UnpinnedTracksDefaultBranch(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))

	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "", SHA: "", Path: "", AutoUpdate: true}
	mgr := newManager(t, []config.BundleSource{src})
	ctx := context.Background()
	id := bundle.BundleID{Namespace: "acme", Name: "tools"}

	mustInstall(t, mgr, src)

	entry := entryRoot(t, src)
	assert.FileExists(t, filepath.Join(entry, "stacks", "node", "stack.yaml"))

	comp, err := mgr.Resolver().Resolve(bundle.ComponentStack, "acme.tools.node")
	require.NoError(t, err, "an unpinned declaration gates its cache entry like any other")
	assert.Equal(t, "acme.tools.node", comp.Address.String())

	t.Run("update is a no-op while the branch has not moved", func(t *testing.T) {
		results, uErr := mgr.Update(ctx, id)
		require.NoError(t, uErr)
		require.Len(t, results, 1)
		assert.Equal(t, bundle.UpdateUnchanged, results[0].Outcome)
	})

	// The default branch moves on.
	repo.Commit(t, "v2", bundleFiles("2.0.0"))

	t.Run("update refetches the moved default branch", func(t *testing.T) {
		results, uErr := mgr.Update(ctx, id)
		require.NoError(t, uErr)
		require.Len(t, results, 1)
		assert.Equal(t, bundle.UpdateRefetched, results[0].Outcome)
		assert.Contains(t, entryManifest(t, entry), "version: 2.0.0")
	})

	repo.Commit(t, "v3", bundleFiles("3.0.0"))

	t.Run("auto-update covers an opted-in unpinned source", func(t *testing.T) {
		warnings := mgr.AutoUpdateCheck(ctx)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0].Message, "auto-updated")
		assert.Contains(t, entryManifest(t, entry), "version: 3.0.0")
	})
}

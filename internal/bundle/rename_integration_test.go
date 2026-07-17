package bundle_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundle/bundletest"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
)

// The cache is keyed <namespace>/<name>/<sourceKey>, but the identity levels
// come from the FETCHED manifest while the key comes from the DECLARED value.
// An upstream rename of the manifest namespace/name therefore leaves the same
// key under two identities. These tests pin the contract that restores "one
// declared value addresses one entry": the fresher fetch is the entry the key
// addresses, the superseded twin never shadows it, and the update/GC passes
// remove the twin rather than stranding it forever.

// renamedBundleFiles is bundleFiles with a caller-chosen manifest namespace,
// for driving an upstream identity rename through a real fetch.
func renamedBundleFiles(namespace, version string) map[string]string {
	return map[string]string{
		".clawker-bundle/bundle.yaml":            "namespace: " + namespace + "\nname: tools\nversion: " + version + "\n",
		"stacks/node/stack.yaml":                 "description: node stack\n",
		"stacks/node/Dockerfile.stack-root.tmpl": "RUN true\n",
	}
}

// cachedEntryRoot resolves the value-keyed cache entry dir of the fixture
// bundle (always named tools) under an arbitrary namespace — plantedEntryRoot
// hardcodes the acme namespace, and the rename scenarios need both sides.
func cachedEntryRoot(t *testing.T, ns string, src bundle.Source) string {
	t.Helper()
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	return filepath.Join(cacheRoot, ns, "tools", src.Key())
}

// writeReceiptAt overwrites a planted entry's fetch receipt so a test controls
// the fetched_at ordering between two same-key twins.
func writeReceiptAt(t *testing.T, entryRoot string, src bundle.Source, version, fetchedAt string) {
	t.Helper()
	require.NoError(t, os.WriteFile(
		filepath.Join(entryRoot, bundle.ReceiptFile),
		[]byte("canonical: \""+src.Canonical()+"\"\nsha: \"\"\nfetched_at: "+fetchedAt+"\nversion: "+version+"\n"),
		0o600))
}

// joinWarnings flattens warning messages for substring assertions.
func joinWarnings(warnings []bundle.Warning) string {
	msgs := make([]string, 0, len(warnings))
	for _, w := range warnings {
		msgs = append(msgs, w.Message)
	}
	return strings.Join(msgs, "\n")
}

// stackComponents is the minimal valid component payload for planted entries.
func stackComponents() map[string]string {
	return map[string]string{
		"stacks/node/stack.yaml":                 "description: node\n",
		"stacks/node/Dockerfile.stack-root.tmpl": "RUN true\n",
	}
}

// plantRenamedTwins plants the state an upstream identity rename leaves
// behind: one declared value's key under two identities, zeta fetched first
// and acme fetched later.
func plantRenamedTwins(t *testing.T, src bundle.Source) {
	t.Helper()
	bundletest.PlantCachedBundleSource(t, "zeta", "tools", "1.0.0", src, stackComponents())
	bundletest.PlantCachedBundleSource(t, "acme", "tools", "2.0.0", src, stackComponents())
	writeReceiptAt(t, cachedEntryRoot(t, "zeta", src), src, "1.0.0", "2026-01-01T00:00:00Z")
	writeReceiptAt(t, cachedEntryRoot(t, "acme", src), src, "2.0.0", "2026-02-01T00:00:00Z")
}

func TestManager_AutoUpdateCheck_UpstreamIdentityRename(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", renamedBundleFiles("zeta", "1.0.0"))

	testenv.New(t)
	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "master", SHA: "", Path: "", AutoUpdate: true}
	s := bundle.SourceFromConfig(src)
	mgr := managerWithRoots([]config.BundleSource{src})
	ctx := context.Background()
	_, _, err := mgr.Install(ctx, src)
	require.NoError(t, err)
	require.DirExists(t, cachedEntryRoot(t, "zeta", s))

	// Upstream renames the manifest namespace on the tracked branch.
	repo.Commit(t, "v2", renamedBundleFiles("acme", "2.0.0"))
	warnings := mgr.AutoUpdateCheck(ctx)

	assert.DirExists(t, cachedEntryRoot(t, "acme", s))
	assert.NoDirExists(t, cachedEntryRoot(t, "zeta", s),
		"the renamed-away identity's same-key twin is superseded and must not survive")
	assert.Contains(t, joinWarnings(warnings), "renamed upstream",
		"the operator must be told the identity moved")

	// The fresh identity resolves; the stale one no longer shadows it. A new
	// manager avoids the memoized bundle set.
	res := managerWithRoots([]config.BundleSource{src}).Resolver()
	_, err = res.Resolve(bundle.ComponentStack, "acme.tools.node")
	require.NoError(t, err)
	_, err = res.Resolve(bundle.ComponentStack, "zeta.tools.node")
	require.ErrorIs(t, err, bundle.ErrNotCached)

	// The rename is settled — no perpetual refetch: the next check compares
	// the fresh entry's receipt against the unchanged tip and stays quiet.
	assert.Empty(t, mgr.AutoUpdateCheck(ctx))
}

func TestResolver_DuplicateKeyLatestFetchWins(t *testing.T) {
	testenv.New(t)
	src := bundle.Source{URL: "https://x/tools.git", Ref: "v1", SHA: "", Path: ""}
	plantRenamedTwins(t, src)

	decl := config.BundleSource{URL: src.URL, Ref: src.Ref, SHA: "", Path: "", AutoUpdate: false}
	res := managerWithRoots([]config.BundleSource{decl}).Resolver()

	comp, err := res.Resolve(bundle.ComponentStack, "acme.tools.node")
	require.NoError(t, err, "the fresher fetch is the entry the declared value addresses")
	assert.Equal(t, "acme.tools.node", comp.Address.String())

	_, err = res.Resolve(bundle.ComponentStack, "zeta.tools.node")
	require.ErrorIs(t, err, bundle.ErrNotCached,
		"the superseded twin must not shadow the declared value's entry")
}

func TestManager_Prune_SupersededDuplicateKeyCollected(t *testing.T) {
	testenv.New(t)
	src := bundle.Source{URL: "https://x/tools.git", Ref: "v1", SHA: "", Path: ""}
	plantRenamedTwins(t, src)

	decl := config.BundleSource{URL: src.URL, Ref: src.Ref, SHA: "", Path: "", AutoUpdate: false}
	mgr := managerWithRoots([]config.BundleSource{decl})
	report, err := mgr.Prune(context.Background())
	require.NoError(t, err)

	assert.DirExists(t, cachedEntryRoot(t, "acme", src))
	assert.NoDirExists(t, cachedEntryRoot(t, "zeta", src),
		"a rooted key keeps only the entry it addresses; the superseded twin is collected")
	require.Len(t, report.Drops, 1)
	assert.Equal(t, bundle.BundleID{Namespace: "zeta", Name: "tools"}, report.Drops[0].ID)
	assert.Equal(t, src.Key(), report.Drops[0].Key)
	assert.Empty(t, report.MultiSource)
}

func TestManager_AutoGC_CollectsSupersededTwinOfTouchedIdentity(t *testing.T) {
	testenv.New(t)
	src := bundle.Source{URL: "https://x/tools.git", Ref: "v1", SHA: "", Path: ""}
	plantRenamedTwins(t, src)

	decl := config.BundleSource{URL: src.URL, Ref: src.Ref, SHA: "", Path: "", AutoUpdate: false}
	mgr := managerWithRoots([]config.BundleSource{decl})

	// The install/update verbs report the FRESH identity — the twin sits under
	// the old one, and the identity-scoped sweep must still reach it via the
	// shared key.
	warnings := mgr.AutoGC(context.Background(), bundle.BundleID{Namespace: "acme", Name: "tools"})

	assert.DirExists(t, cachedEntryRoot(t, "acme", src))
	assert.NoDirExists(t, cachedEntryRoot(t, "zeta", src))
	assert.Contains(t, joinWarnings(warnings), src.Key())
}

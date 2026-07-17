package bundle_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
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

	_, _, err := mgr.Install(context.Background(), src)
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

// A bundle whose content is reachable only through a symlink climbing out of
// the bundle root must be REJECTED, not "successfully installed" as a broken
// entry. The commit drops such a link (it cannot be carried into a
// self-contained cache entry), so validating anything else than the tree the
// commit produces would bless content the cache will never hold: the manifest
// resolves in the clone, the entry lands without it, and every later
// Bundles() call — bundle list, build, run, monitor up — hard-fails on the
// unreadable manifest, with the entry rooted by its declaration so no GC ever
// reclaims it.
func TestManager_Install_RejectsContentSymlinkedOutOfBundleRoot(t *testing.T) {
	cases := map[string]struct {
		link   string // path inside the bundle root that is really a symlink out
		target string // its link target, relative to the link's own directory
	}{
		"manifest": {
			link:   "bundles/tools/.clawker-bundle/bundle.yaml",
			target: "../../../shared/bundle.yaml",
		},
		"component": {
			link:   "bundles/tools/stacks/node/Dockerfile.stack-root.tmpl",
			target: "../../../../shared/Dockerfile.stack-root.tmpl",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := bundletest.New(t)
			repo := srv.InitRepo(t, "mono")
			// The bundle lives at bundles/tools; shared/ sits OUTSIDE it, so a
			// link into shared/ escapes the bundle root while staying inside
			// the repository — the shape a monorepo author reaches for, and the
			// shape an attacker uses to pass validation with content the entry
			// can never carry.
			files := map[string]string{
				"shared/bundle.yaml":                                   "namespace: acme\nname: tools\nversion: 1.0.0\n",
				"shared/Dockerfile.stack-root.tmpl":                    "RUN true\n",
				"bundles/tools/.clawker-bundle/bundle.yaml":            "namespace: acme\nname: tools\nversion: 1.0.0\n",
				"bundles/tools/stacks/node/stack.yaml":                 "description: node\n",
				"bundles/tools/stacks/node/Dockerfile.stack-root.tmpl": "RUN true\n",
			}
			delete(files, tc.link)
			repo.Commit(t, "v1", files)
			repo.Symlink(t, "escape", map[string]string{tc.link: tc.target})
			repo.Tag(t, "v1.0.0")

			src := config.BundleSource{
				URL: srv.HTTPURL("mono"), Ref: "v1.0.0", SHA: "", Path: "bundles/tools", AutoUpdate: false,
			}
			mgr := newManager(t, []config.BundleSource{src})

			_, _, err := mgr.Install(context.Background(), src)
			require.Error(t, err, "content reachable only outside the bundle root must fail validation")
			assert.Contains(t, err.Error(), "self-contained",
				"the error must name the dropped symlink as the cause, not just a missing file")
			assert.Contains(t, err.Error(), filepath.Base(tc.link))

			cacheRoot, rootErr := consts.BundlesSubdir()
			require.NoError(t, rootErr)
			assert.NoDirExists(t, filepath.Join(cacheRoot, "acme"),
				"a bundle rejected at validation must leave nothing in the cache")
		})
	}
}

// The receipt name is clawker-owned. A bundle shipping ".fetch.yaml" itself —
// and above all shipping it as an in-tree relative SYMLINK, the shape the copy
// is designed to carry — must not let the post-validation receipt write follow
// that link and overwrite validated content (the manifest, a component file).
// The staged tree the swap publishes must be the tree validation read, plus
// exactly one fresh receipt file.
func TestManager_Install_ReceiptNameIsReservedNeverFollowed(t *testing.T) {
	t.Run("symlink at the receipt name cannot redirect the receipt write", func(t *testing.T) {
		srv := bundletest.New(t)
		repo := srv.InitRepo(t, "tools")
		repo.Commit(t, "v1", bundleFiles("1.0.0"))
		// .fetch.yaml -> .clawker-bundle/bundle.yaml: relative, in-tree — the
		// copy would carry it, and a follow-the-link receipt write would
		// replace the manifest AFTER validation blessed it.
		repo.Symlink(t, "squat the receipt name", map[string]string{
			".fetch.yaml": ".clawker-bundle/bundle.yaml",
		})
		repo.Tag(t, "v1.0.0")

		src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
		mgr := newManager(t, []config.BundleSource{src})
		mustInstall(t, mgr, src)

		entry := entryRoot(t, src)
		assert.Contains(t, entryManifest(t, entry), "namespace: acme",
			"the committed manifest must be the one validation read, not receipt YAML")

		info, err := os.Lstat(filepath.Join(entry, bundle.ReceiptFile))
		require.NoError(t, err)
		assert.True(t, info.Mode().IsRegular(),
			"the entry's receipt must be a fresh regular file, never a bundle-authored link")

		// The whole point of bug 1: the installed entry keeps resolving.
		bundles, _, err := mgr.Resolver().Bundles()
		require.NoError(t, err)
		_, ok := bundles[bundle.BundleID{Namespace: "acme", Name: "tools"}]
		assert.True(t, ok)
	})

	t.Run("regular file at the receipt name is replaced by the real receipt", func(t *testing.T) {
		srv := bundletest.New(t)
		repo := srv.InitRepo(t, "tools")
		files := bundleFiles("1.0.0")
		files[".fetch.yaml"] = "canonical: bundle-authored-lie\n"
		repo.Commit(t, "v1", files)
		repo.Tag(t, "v1.0.0")

		src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
		mgr := newManager(t, []config.BundleSource{src})
		mustInstall(t, mgr, src)

		raw, err := os.ReadFile(filepath.Join(entryRoot(t, src), bundle.ReceiptFile))
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "bundle-authored-lie",
			"the entry's receipt is written by the install, never shipped by the bundle")
	})
}

// The REVERSE alias of the receipt-name squat: nothing sits at the reserved
// root path, but a carried in-tree link at a COMPONENT path targets it. The
// receipt does not exist while validation runs, so an optional read (a stack's
// root fragment) sees "absent — fine"; the receipt write then makes the link
// resolve, and the committed entry would serve receipt YAML as the fragment.
// The link must be dropped like any other non-portable shape: receipt bytes
// are clawker-written metadata and must never serve as component content.
func TestManager_Install_DropsLinkAliasingTheReceipt(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", map[string]string{
		".clawker-bundle/bundle.yaml":            "namespace: acme\nname: tools\nversion: 1.0.0\n",
		"stacks/node/stack.yaml":                 "description: node\n",
		"stacks/node/Dockerfile.stack-user.tmpl": "RUN user\n",
	})
	// Relative, in-tree, and dangling at validate time — the one carried-link
	// shape whose TARGET the pipeline itself creates after validation.
	repo.Symlink(t, "alias the receipt", map[string]string{
		"stacks/node/Dockerfile.stack-root.tmpl": "../../" + bundle.ReceiptFile,
	})
	repo.Tag(t, "v1.0.0")

	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{src})

	_, warnings, err := mgr.Install(context.Background(), src)
	require.NoError(t, err, "the optional fragment stays absent; the install succeeds without it")
	require.Len(t, warnings, 1, "the dropped alias must be reported")
	assert.Contains(t, warnings[0].Message, "stacks/node/Dockerfile.stack-root.tmpl")

	// The committed entry must not serve receipt bytes as component content.
	entry := entryRoot(t, src)
	assert.NoFileExists(t, filepath.Join(entry, "stacks", "node", "Dockerfile.stack-root.tmpl"))

	comp, err := mgr.Resolver().Resolve(bundle.ComponentStack, "acme.tools.node")
	require.NoError(t, err)
	assert.Equal(t, "acme.tools.node", comp.Address.String())
}

// Symlink safety must not rest on reading link targets lexically: an in-tree
// DIRECTORY link (one resolving to the bundle root — in-tree, so nothing drops
// it) lets a second link reach through it to a path its own spelling never
// names. This receipt alias is invisible to any single-level textual compare
// (`stacks/node/Dockerfile.stack-root.tmpl -> dir/.fetch.yaml`, `dir -> ../..`)
// and must be refused by resolving the link for real. It lands on
// sanitize's UNRESOLVABLE branch — the receipt does not exist when sanitize
// runs — so the entry cannot serve receipt bytes as a fragment. (The
// escapes-stage branch, where a link resolves to a real EXISTING file outside
// the stage, is pinned directly in TestSanitizeStagedLinks_* — it needs a real
// out-of-stage file, which the subdir copy cannot stage without brittle cache-
// depth coupling.)
func TestManager_Install_DropsLinkAliasingReceiptThroughADirectoryLink(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", map[string]string{
		".clawker-bundle/bundle.yaml":            "namespace: acme\nname: tools\nversion: 1.0.0\n",
		"stacks/node/stack.yaml":                 "description: node\n",
		"stacks/node/Dockerfile.stack-user.tmpl": "RUN user\n",
	})
	repo.Symlink(t, "alias the receipt through a dir link", map[string]string{
		"stacks/node/dir":                        "../..",
		"stacks/node/Dockerfile.stack-root.tmpl": "dir/" + bundle.ReceiptFile,
	})
	repo.Tag(t, "v1.0.0")

	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{src})

	_, warnings, err := mgr.Install(context.Background(), src)
	require.NoError(t, err, "the optional fragment stays absent; the install succeeds without it")
	require.Len(t, warnings, 1, "the dropped alias must be reported")
	assert.Contains(t, warnings[0].Message, "stacks/node/Dockerfile.stack-root.tmpl")

	entry := entryRoot(t, src)
	assert.NoFileExists(t, filepath.Join(entry, "stacks", "node", "Dockerfile.stack-root.tmpl"),
		"the entry must never serve receipt bytes as a Dockerfile fragment")
}

// A dropped symlink that validation does not happen to need (an asset a
// Dockerfile fragment references, say) must still be reported: otherwise the
// install is silent, and the missing content surfaces later as an opaque build
// failure. On the success path the drop is a Warning.
func TestManager_Install_WarnsOnDroppedSymlinks(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "mono")
	repo.Commit(t, "v1", map[string]string{
		"shared/entrypoint.sh":                                 "#!/bin/sh\n",
		"bundles/tools/.clawker-bundle/bundle.yaml":            "namespace: acme\nname: tools\nversion: 1.0.0\n",
		"bundles/tools/stacks/node/stack.yaml":                 "description: node\n",
		"bundles/tools/stacks/node/Dockerfile.stack-root.tmpl": "RUN true\n",
	})
	// An asset shared from outside the bundle root: validation never reads it
	// (the stack loader wants only the manifest and fragments), so the install
	// would succeed silently while the entry ships without it.
	repo.Symlink(t, "escaping asset", map[string]string{
		"bundles/tools/stacks/node/entrypoint.sh": "../../../../shared/entrypoint.sh",
	})
	repo.Tag(t, "v1.0.0")

	src := config.BundleSource{
		URL: srv.HTTPURL("mono"), Ref: "v1.0.0", SHA: "", Path: "bundles/tools", AutoUpdate: false,
	}
	mgr := newManager(t, []config.BundleSource{src})

	_, warnings, err := mgr.Install(context.Background(), src)
	require.NoError(t, err, "a dropped link validation does not need is a warning, not a failure")
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0].Message, "stacks/node/entrypoint.sh",
		"the warning must name the dropped link")
	assert.NoFileExists(t, filepath.Join(entryRoot(t, src), "stacks", "node", "entrypoint.sh"))
}

// The flip side of rejecting escaping links: a bundle sharing one file between
// two of its own components via a relative symlink is a legitimate, expected
// authoring shape. The link stays inside the bundle root, so the cache entry
// carries it and it must install and resolve like any other content.
func TestManager_Install_IntraBundleSymlinkSurvives(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", map[string]string{
		".clawker-bundle/bundle.yaml":            "namespace: acme\nname: tools\nversion: 1.0.0\n",
		"stacks/node/stack.yaml":                 "description: node\n",
		"stacks/node/Dockerfile.stack-root.tmpl": "RUN node\n",
		"stacks/deno/stack.yaml":                 "description: deno\n",
	})
	// deno reuses node's fragment — an in-tree relative link.
	repo.Symlink(t, "share fragment", map[string]string{
		"stacks/deno/Dockerfile.stack-root.tmpl": "../node/Dockerfile.stack-root.tmpl",
	})
	repo.Tag(t, "v1.0.0")

	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}
	mgr := newManager(t, []config.BundleSource{src})
	mustInstall(t, mgr, src, "an intra-bundle symlink is legitimate content and must install")

	// The committed entry carries the link and it still resolves to the shared
	// fragment — the cache entry is self-contained.
	entry := entryRoot(t, src)
	shared, err := os.ReadFile(filepath.Join(entry, "stacks", "deno", "Dockerfile.stack-root.tmpl"))
	require.NoError(t, err)
	assert.Equal(t, "RUN node\n", string(shared))

	comp, err := mgr.Resolver().Resolve(bundle.ComponentStack, "acme.tools.deno")
	require.NoError(t, err)
	assert.Equal(t, "acme.tools.deno", comp.Address.String())
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
// returned identity (the fixtures all ship acme.tools). The plain fixtures
// carry no droppable content, so a warning here is itself a failure.
func mustInstall(t *testing.T, mgr *bundle.Manager, src config.BundleSource, msgAndArgs ...any) {
	t.Helper()
	_, warnings, err := mgr.Install(context.Background(), src)
	require.NoError(t, err, msgAndArgs...)
	require.Empty(t, warnings, msgAndArgs...)
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

// Remove purges cache entries other processes may be committing into, and the
// per-entry advisory lock files live INSIDE the tree it purges. Removing the
// tree wholesale would unlink a lock while a writer holds it, so the next
// writer's flock.New creates a fresh inode at the same path and acquires
// instantly — two writers on one entry directory. Remove must take each
// entry's lock, and — unlike the GC removal path, whose entries are condemned
// with no legitimate writers — it must LEAVE the lock file behind: a
// still-declared identity has legitimate concurrent installers, and they must
// keep locking the same inode.
func TestManager_Remove_SerializesOnTheEntryLock(t *testing.T) {
	testenv.New(t)
	src := config.BundleSource{
		URL: "https://example.com/acme/tools.git", Ref: "v1", SHA: "", Path: "", AutoUpdate: false,
	}
	bundletest.PlantCachedBundle(t, "acme", "tools", "1.0.0", src.URL, map[string]string{
		"stacks/node/stack.yaml":                 "description: node stack\n",
		"stacks/node/Dockerfile.stack-root.tmpl": "RUN true\n",
	})
	mgr := managerForDecls(src)
	id := bundle.BundleID{Namespace: "acme", Name: "tools"}
	entry := entryRoot(t, src)

	// Stand in for an in-flight writer holding the entry's lock.
	held := flock.New(entry + ".lock")
	locked, err := held.TryLock()
	require.NoError(t, err)
	require.True(t, locked)
	t.Cleanup(func() {
		if unlockErr := held.Unlock(); unlockErr != nil {
			t.Logf("unlock: %v", unlockErr)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	removed, err := mgr.Remove(ctx, id)
	require.Error(t, err, "Remove must block on the entry lock, not purge through a live writer")
	assert.False(t, removed)
	assert.FileExists(t, filepath.Join(entry, "stacks", "node", "stack.yaml"),
		"the locked entry must survive a Remove that could not take its lock")

	// With the writer gone, the same Remove purges the identity.
	require.NoError(t, held.Unlock())
	removed, err = mgr.Remove(context.Background(), id)
	require.NoError(t, err)
	assert.True(t, removed)
	assert.NoDirExists(t, entry)

	// The lock INODE must survive the purge. Remove targets identities that may
	// still be declared — a concurrent install of the same value is legitimate,
	// and every such writer must keep serializing on one inode. Unlinking the
	// lock would hand the next writer a fresh inode at the same path, granting
	// instantly alongside anyone still holding the old one.
	assert.FileExists(t, entry+".lock",
		"Remove must leave the per-entry lock file so future writers lock the same inode")
}

func TestManager_Remove_NotCachedIsNoOp(t *testing.T) {
	testenv.New(t)
	mgr := managerForDecls()
	removed, err := mgr.Remove(context.Background(), bundle.BundleID{Namespace: "acme", Name: "tools"})
	require.NoError(t, err)
	assert.False(t, removed)
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

	installed, warnings, err := mgr.InstallDeclared(ctx)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	require.Len(t, installed, 1)
	assert.Equal(t, "acme.tools", installed[0].String())

	// Second pass: already cached, nothing to do.
	installed, _, err = mgr.InstallDeclared(ctx)
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
			_, _, err := mgr.Install(context.Background(), config.BundleSource{
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
	_, _, err := mgr.Install(context.Background(), config.BundleSource{
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

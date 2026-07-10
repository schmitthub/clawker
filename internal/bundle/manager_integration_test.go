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
		".clawker-bundle/bundle.yaml": manifest,
		"stacks/node/stack.yaml":      "description: node stack\n",
	}
}

// newManager wires a Manager over an isolated data-dir cache and the given
// declared sources.
func newManager(t *testing.T, decls []config.BundleSource) *bundle.Manager {
	t.Helper()
	testenv.New(t)
	cfg := configmocks.NewBlankConfig()
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		out := make([]config.BundleDeclaration, 0, len(decls))
		for _, s := range decls {
			out = append(out, config.BundleDeclaration{Source: s, File: "clawker.yaml"})
		}
		return out
	}
	cfg.ProjectRootFunc = func() string { return "" }
	return bundle.NewManager(cfg)
}

func TestManager_Install_HTTPJourney(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))
	repo.Tag(t, "v1.0.0")

	mgr := newManager(t, nil)
	ctx := context.Background()
	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false}

	require.NoError(t, mgr.Install(ctx, src))

	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	versionDir := filepath.Join(cacheRoot, "acme", "tools", "1.0.0")
	assert.FileExists(t, filepath.Join(versionDir, "stacks", "node", "stack.yaml"))
	assert.FileExists(t, filepath.Join(cacheRoot, "acme", "tools", "source.yaml"))
	assert.NoDirExists(t, filepath.Join(versionDir, ".git"))

	comp, err := mgr.Resolver().Resolve(bundle.ComponentStack, "acme.tools.node")
	require.NoError(t, err)
	assert.Equal(t, "acme.tools.node", comp.Address.String())

	t.Run("reinstall of same source is idempotent", func(t *testing.T) {
		require.NoError(t, mgr.Install(ctx, src))
		assert.FileExists(t, filepath.Join(versionDir, "stacks", "node", "stack.yaml"))
	})
}

func TestManager_Install_Subdir(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "mono")
	repo.Commit(t, "monorepo", map[string]string{
		"bundles/tools/.clawker-bundle/bundle.yaml": "namespace: acme\nname: tools\nversion: 1.0.0\n",
		"bundles/tools/stacks/node/stack.yaml":      "description: node\n",
		"unrelated/readme.md":                       "ignore me\n",
	})
	repo.Tag(t, "v1.0.0")

	mgr := newManager(t, nil)
	src := config.BundleSource{
		URL: srv.HTTPURL("mono"), Ref: "v1.0.0", SHA: "", Path: "bundles/tools", AutoUpdate: false,
	}
	require.NoError(t, mgr.Install(context.Background(), src))

	comp, err := mgr.Resolver().Resolve(bundle.ComponentStack, "acme.tools.node")
	require.NoError(t, err)
	assert.Equal(t, "acme.tools.node", comp.Address.String())
}

func TestManager_Install_SHAPin(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	sha := repo.Commit(t, "v1", bundleFiles(""))

	mgr := newManager(t, nil)
	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "", SHA: sha, Path: "", AutoUpdate: false}
	require.NoError(t, mgr.Install(context.Background(), src))

	// No manifest version → the version dir is the full 40-char sha.
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(cacheRoot, "acme", "tools", sha, "stacks", "node", "stack.yaml"))
}

func TestManager_Install_C1Collision(t *testing.T) {
	srv := bundletest.New(t)
	a := srv.InitRepo(t, "a")
	a.Commit(t, "v1", bundleFiles("1.0.0"))
	a.Tag(t, "v1.0.0")
	b := srv.InitRepo(t, "b")
	b.Commit(t, "v1", bundleFiles("1.0.0"))
	b.Tag(t, "v1.0.0")

	mgr := newManager(t, nil)
	ctx := context.Background()
	require.NoError(t, mgr.Install(ctx, config.BundleSource{
		URL: srv.HTTPURL("a"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false,
	}))

	err := mgr.Install(ctx, config.BundleSource{
		URL: srv.HTTPURL("b"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false,
	})
	var collision *bundle.CollisionError
	require.ErrorAs(t, err, &collision)
	assert.Equal(t, "acme.tools", collision.Identity.String())
}

func TestManager_Update_RefetchesOnDrift(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))

	mgr := newManager(t, nil)
	ctx := context.Background()
	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "master", SHA: "", Path: "", AutoUpdate: false}
	require.NoError(t, mgr.Install(ctx, src))

	id := bundle.BundleID{Namespace: "acme", Name: "tools"}

	t.Run("no drift is a no-op", func(t *testing.T) {
		results, err := mgr.Update(ctx, id)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, bundle.UpdateUnchanged, results[0].Outcome)
	})

	// Move the branch tip: a new commit bumps the manifest version.
	repo.Commit(t, "v2", bundleFiles("2.0.0"))

	t.Run("drift refetches a new version", func(t *testing.T) {
		results, err := mgr.Update(ctx, id)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, bundle.UpdateRefetched, results[0].Outcome)

		cacheRoot, err := consts.BundlesSubdir()
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(cacheRoot, "acme", "tools", "2.0.0", "stacks", "node", "stack.yaml"))
	})
}

func TestManager_Update_FailureKeepsCache(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))

	mgr := newManager(t, nil)
	ctx := context.Background()
	require.NoError(t, mgr.Install(ctx, config.BundleSource{
		URL: srv.HTTPURL("tools"), Ref: "master", SHA: "", Path: "", AutoUpdate: false,
	}))

	// Rewrite source.yaml to point the cached bundle at an unreachable URL so
	// the update resolve fails without disturbing the fetched content.
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	repointSource(t,
		filepath.Join(cacheRoot, "acme", "tools", "source.yaml"), srv.HTTPURL("tools"), srv.HTTPURL("gone"))

	id := bundle.BundleID{Namespace: "acme", Name: "tools"}
	results, err := mgr.Update(ctx, id)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, bundle.UpdateFailed, results[0].Outcome)
	require.Error(t, results[0].Err)

	// Cache still serves the originally installed content.
	assert.FileExists(t, filepath.Join(cacheRoot, "acme", "tools", "1.0.0", "stacks", "node", "stack.yaml"))
}

func TestManager_AutoUpdateCheck_OptInOnly(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "tools")
	repo.Commit(t, "v1", bundleFiles("1.0.0"))

	src := config.BundleSource{URL: srv.HTTPURL("tools"), Ref: "master", SHA: "", Path: "", AutoUpdate: true}
	mgr := newManager(t, []config.BundleSource{src})
	ctx := context.Background()
	require.NoError(t, mgr.Install(ctx, src))

	t.Run("no drift yields no warnings", func(t *testing.T) {
		assert.Empty(t, mgr.AutoUpdateCheck(ctx))
	})

	repo.Commit(t, "v2", bundleFiles("2.0.0"))

	t.Run("drift on an opted-in bundle refetches with a warning", func(t *testing.T) {
		warnings := mgr.AutoUpdateCheck(ctx)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0].Message, "auto-updated")

		cacheRoot, err := consts.BundlesSubdir()
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(cacheRoot, "acme", "tools", "2.0.0", "stacks", "node", "stack.yaml"))
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
	require.NoError(t, mgr.Install(ctx, src))

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

func TestManager_Install_MalformedManifestNoCommit(t *testing.T) {
	srv := bundletest.New(t)
	repo := srv.InitRepo(t, "bad")
	repo.Commit(t, "v1", map[string]string{
		".clawker-bundle/bundle.yaml": "namespace: clawker\nname: tools\n", // reserved namespace
		"stacks/node/stack.yaml":      "description: node\n",
	})
	repo.Tag(t, "v1.0.0")

	mgr := newManager(t, nil)
	err := mgr.Install(context.Background(), config.BundleSource{
		URL: srv.HTTPURL("bad"), Ref: "v1.0.0", SHA: "", Path: "", AutoUpdate: false,
	})
	var manifestErr *bundle.ManifestError
	require.ErrorAs(t, err, &manifestErr)

	cacheRoot, err2 := consts.BundlesSubdir()
	require.NoError(t, err2)
	assert.NoDirExists(t, filepath.Join(cacheRoot, "clawker"))
}

// repointSource rewrites the cached source.yaml URL so an update targets an
// unreachable repository without touching the fetched content.
func repointSource(t *testing.T, path, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	updated := strings.Replace(string(raw), from, to, 1)
	require.NotEqual(t, string(raw), updated, "source url not found in %s", path)
	require.NoError(t, os.WriteFile(path, []byte(updated), 0o600))
}

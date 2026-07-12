package bundle

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
)

// resolverFixture wires a resolver over real isolated filesystem tiers: a temp
// project root (loose project + relative in-place sources), the isolated
// config dir (loose user), and the isolated data dir (bundle cache). Bundle
// declarations are injected through the config mock — config parsing has its
// own tests; the resolver's real surface is the filesystem resolution.
type resolverFixture struct {
	r          *Resolver
	cfg        *configmocks.ConfigMock
	env        *testenv.Env
	projectDir string
	decls      []config.BundleDeclaration
}

func newResolverFixture(t *testing.T) *resolverFixture {
	t.Helper()
	env := testenv.New(t)
	projectDir := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	f := &resolverFixture{cfg: configmocks.NewBlankConfig(), env: env, projectDir: projectDir, r: nil, decls: nil}
	f.cfg.ProjectRootFunc = func() string { return f.projectDir }
	f.cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration { return f.decls }
	f.r = NewResolver(f.cfg)
	return f
}

// looseUserStack writes a loose user-tier stack component.
func (f *resolverFixture) looseUserStack(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(consts.ConfigDir(), ComponentStack.Dir(), name)
	writeFile(t, dir, "stack.yaml", "description: user "+name+"\n")
	return dir
}

// looseProjectStack writes a loose project-tier stack component.
func (f *resolverFixture) looseProjectStack(t *testing.T, name string) {
	t.Helper()
	dir := filepath.Join(f.projectDir, consts.DotClawkerDir, ComponentStack.Dir(), name)
	writeFile(t, dir, "stack.yaml", "description: project "+name+"\n")
}

// looseUserComponent writes a loose user-tier component of any type; the
// resolver keys off the convention directory's existence, so the manifest
// filename is immaterial here (a bare placeholder keeps the dir non-empty).
func (f *resolverFixture) looseUserComponent(t *testing.T, ct ComponentType, name string) string {
	t.Helper()
	dir := filepath.Join(consts.ConfigDir(), ct.Dir(), name)
	writeFile(t, dir, "manifest.yaml", "description: user "+name+"\n")
	return dir
}

// looseProjectComponent writes a loose project-tier component of any type.
func (f *resolverFixture) looseProjectComponent(t *testing.T, ct ComponentType, name string) string {
	t.Helper()
	dir := filepath.Join(f.projectDir, consts.DotClawkerDir, ct.Dir(), name)
	writeFile(t, dir, "manifest.yaml", "description: project "+name+"\n")
	return dir
}

// cacheBundleStack writes a cached bundle shipping one stack component —
// content root only, no source metadata (a hand-placed entry unless
// cacheSourceMeta is also called).
func (f *resolverFixture) cacheBundleStack(t *testing.T, ns, name, version, stack string) {
	t.Helper()
	root, err := consts.BundlesSubdir()
	require.NoError(t, err)
	versionRoot := filepath.Join(root, ns, name, version)
	writeManifest(t, versionRoot, "namespace: "+ns+"\nname: "+name+"\nversion: "+version+"\n")
	writeFile(t, versionRoot, "stacks/"+stack+"/stack.yaml", "description: "+stack+"\n")
}

// cacheSourceMeta writes the cache-internal source.yaml linking a cached bundle
// to a remote ref source, with one recorded fetch per version.
func (f *resolverFixture) cacheSourceMeta(t *testing.T, ns, name, url, ref string, fetched map[string]time.Time) {
	t.Helper()
	root, err := consts.BundlesSubdir()
	require.NoError(t, err)
	versions := map[string]versionMeta{}
	for v, at := range fetched {
		versions[v] = versionMeta{SHA: "", FetchedAt: at, Pin: "ref:" + ref}
	}
	require.NoError(t, writeSourceMeta(filepath.Join(root, ns, name), sourceMeta{
		URL: url, Ref: ref, SHA: "", Subdir: "", Versions: versions,
	}))
}

// declareRemote appends a live remote declaration in the project clawker.yaml.
func (f *resolverFixture) declareRemote(t *testing.T, url, ref string) {
	t.Helper()
	f.decls = append(f.decls, config.BundleDeclaration{
		Source: config.BundleSource{URL: url, Ref: ref, SHA: "", AutoUpdate: false, Path: ""},
		File:   filepath.Join(f.projectDir, "clawker.yaml"),
	})
}

// installBundleStack writes the canonical fully-declared cached bundle
// (acme.tools@1.0.0 shipping the node stack): content root, source.yaml, and a
// live declaration matching it — the state `bundle install` leaves behind.
func (f *resolverFixture) installBundleStack(t *testing.T) {
	t.Helper()
	const url = "https://example.com/acme/tools.git"
	f.cacheBundleStack(t, "acme", "tools", "1.0.0", "node")
	f.cacheSourceMeta(t, "acme", "tools", url, "v1", map[string]time.Time{"1.0.0": time.Now()})
	f.declareRemote(t, url, "v1")
}

func TestResolve_BareFloor(t *testing.T) {
	f := newResolverFixture(t)
	c, err := f.r.Resolve(ComponentStack, "node")
	require.NoError(t, err)
	assert.Equal(t, TierFloor, c.Provenance.Tier)
	assert.Equal(t, "node", c.Address.String())
}

// A bare resolution must never scan the bundle set, so a broken bundle
// declaration cannot block a floor-only resolve.
func TestResolve_BareIgnoresBrokenBundleDecl(t *testing.T) {
	f := newResolverFixture(t)
	f.decls = []config.BundleDeclaration{
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/broken"},
			File:   filepath.Join(f.projectDir, "clawker.yaml"),
		},
	}
	// The declared path does not exist; Bundles() would fail.
	_, _, err := f.r.Bundles()
	require.Error(t, err)

	// Bare resolution still succeeds against the floor.
	c, err := f.r.Resolve(ComponentStack, "node")
	require.NoError(t, err)
	assert.Equal(t, TierFloor, c.Provenance.Tier)
}

// The loose-shadow precedence (C3: loose over floor; C4: user over project over
// floor) is the ONE algorithm for every component type, not a stack quirk. Each
// floor name below ships in the embedded floor, so a same-named loose dir is a
// genuine shadow of a real floor component.
func TestResolve_ShadowingAcrossComponentTypes_C3C4(t *testing.T) {
	cases := []struct {
		ct        ComponentType
		floorName string
	}{
		{ComponentHarness, "claude"},
		{ComponentStack, "node"},
		{ComponentMonitoring, "claude-code"},
	}
	for _, tc := range cases {
		t.Run(tc.ct.String(), func(t *testing.T) {
			t.Run("C3 loose project shadows floor", func(t *testing.T) {
				f := newResolverFixture(t)
				projectDir := f.looseProjectComponent(t, tc.ct, tc.floorName)
				c, err := f.r.Resolve(tc.ct, tc.floorName)
				require.NoError(t, err)
				assert.Equal(t, TierLooseProject, c.Provenance.Tier)
				assert.Equal(t, projectDir, c.Provenance.Dir)
			})

			t.Run("C4 user shadows project shadows floor", func(t *testing.T) {
				f := newResolverFixture(t)
				f.looseProjectComponent(t, tc.ct, tc.floorName)
				userDir := f.looseUserComponent(t, tc.ct, tc.floorName)
				c, err := f.r.Resolve(tc.ct, tc.floorName)
				require.NoError(t, err)
				assert.Equal(t, TierLooseUser, c.Provenance.Tier, "user loose beats project loose and floor")
				assert.Equal(t, userDir, c.Provenance.Dir)
			})
		})
	}
}

func TestResolve_QualifiedInstalled(t *testing.T) {
	f := newResolverFixture(t)
	f.installBundleStack(t)

	c, err := f.r.Resolve(ComponentStack, "acme.tools.node")
	require.NoError(t, err)
	assert.Equal(t, TierInstalled, c.Provenance.Tier)
	assert.Equal(t, BundleID{Namespace: "acme", Name: "tools"}, c.Provenance.Bundle)
	assert.Equal(t, "acme.tools.node", c.Address.String())

	// A component the bundle does not ship is a hard error, not ErrNotCached.
	_, err = f.r.Resolve(ComponentStack, "acme.tools.rust")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotCached)
}

// An in-place declaration and a DECLARED cached bundle claiming the same
// identity is a C1 collision — the resolver has no way to know they are the
// same bundle, and silently preferring the local dir would let any directory
// hijack a trusted installed identity. Hard error naming both declaring files,
// never a silent winner.
func TestResolve_QualifiedInPlaceVsCacheCollides(t *testing.T) {
	f := newResolverFixture(t)
	// A declared cached bundle and an in-place declaration both claim acme/tools.
	f.installBundleStack(t)
	inPlace := filepath.Join(f.projectDir, "vendor", "tools")
	writeManifest(t, inPlace, "namespace: acme\nname: tools\n")
	writeFile(t, inPlace, "stacks/node/stack.yaml", "description: dev loop\n")
	f.decls = append(f.decls, config.BundleDeclaration{
		Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/tools"},
		File:   filepath.Join(f.projectDir, "local.clawker.yaml"),
	})

	_, err := f.r.Resolve(ComponentStack, "acme.tools.node")
	var ce *CollisionError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, BundleID{Namespace: "acme", Name: "tools"}, ce.Identity)
	assert.Equal(t, "path:"+inPlace, ce.ACanonical, "the in-place side is named")
	assert.Equal(t, filepath.Join(f.projectDir, "local.clawker.yaml"), ce.AFile,
		"the in-place declaring file is named")
	assert.Equal(t, filepath.Join(f.projectDir, "clawker.yaml"), ce.BFile,
		"the remote declaring file is named")
	assert.Contains(t, err.Error(), "bundle remove acme.tools", "the purge remedy is named")
}

// The in-place-vs-cache collision requires BOTH sides declared: an undeclared
// cache entry is inert, so the bundle author's dev flow is to swap the url
// declaration for a path declaration — no purge needed, no collision.
func TestResolve_InPlaceOverUndeclaredCache(t *testing.T) {
	f := newResolverFixture(t)
	// Cached with source metadata, but its declaration was removed.
	f.cacheBundleStack(t, "acme", "tools", "1.0.0", "node")
	f.cacheSourceMeta(t, "acme", "tools", "https://example.com/acme/tools.git", "v1",
		map[string]time.Time{"1.0.0": time.Now()})
	inPlace := filepath.Join(f.projectDir, "vendor", "tools")
	writeManifest(t, inPlace, "namespace: acme\nname: tools\n")
	writeFile(t, inPlace, "stacks/node/stack.yaml", "description: dev loop\n")
	f.decls = []config.BundleDeclaration{
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/tools"},
			File:   filepath.Join(f.projectDir, "clawker.yaml"),
		},
	}

	c, err := f.r.Resolve(ComponentStack, "acme.tools.node")
	require.NoError(t, err)
	assert.Equal(t, TierInPlace, c.Provenance.Tier)
}

// Declaration-gating: a cached bundle resolves ONLY while a live declaration
// matches the source recorded in its source.yaml. Deleting the `bundles:`
// entry makes the cached copy unavailable; a hand-placed entry (no
// source.yaml) traces to nothing and never resolves.
func TestBundles_CacheDeclarationGating(t *testing.T) {
	const url = "https://example.com/acme/tools.git"

	t.Run("undeclared cache entry is inert", func(t *testing.T) {
		f := newResolverFixture(t)
		f.cacheBundleStack(t, "acme", "tools", "1.0.0", "node")
		f.cacheSourceMeta(t, "acme", "tools", url, "v1", map[string]time.Time{"1.0.0": time.Now()})

		_, err := f.r.Resolve(ComponentStack, "acme.tools.node")
		assert.ErrorIs(t, err, ErrNotCached)
	})

	t.Run("re-declaring reactivates without a refetch", func(t *testing.T) {
		f := newResolverFixture(t)
		f.cacheBundleStack(t, "acme", "tools", "1.0.0", "node")
		f.cacheSourceMeta(t, "acme", "tools", url, "v1", map[string]time.Time{"1.0.0": time.Now()})
		f.declareRemote(t, url, "v1")

		c, err := f.r.Resolve(ComponentStack, "acme.tools.node")
		require.NoError(t, err)
		assert.Equal(t, TierInstalled, c.Provenance.Tier)
	})

	t.Run("a declaration with a different pin does not match", func(t *testing.T) {
		f := newResolverFixture(t)
		f.cacheBundleStack(t, "acme", "tools", "1.0.0", "node")
		f.cacheSourceMeta(t, "acme", "tools", url, "v1", map[string]time.Time{"1.0.0": time.Now()})
		f.declareRemote(t, url, "v2")

		_, err := f.r.Resolve(ComponentStack, "acme.tools.node")
		assert.ErrorIs(t, err, ErrNotCached)
	})

	t.Run("hand-placed cache entry never resolves", func(t *testing.T) {
		f := newResolverFixture(t)
		f.cacheBundleStack(t, "acme", "tools", "1.0.0", "node") // no source.yaml
		f.declareRemote(t, url, "v1")

		_, err := f.r.Resolve(ComponentStack, "acme.tools.node")
		assert.ErrorIs(t, err, ErrNotCached)
	})
}

// Version selection follows the matched source's fetch history, not directory
// sort order: the most recently fetched version wins even when an older fetch
// sorts later lexically.
func TestBundles_VersionPickLatestFetched(t *testing.T) {
	f := newResolverFixture(t)
	const url = "https://example.com/acme/tools.git"
	// "1.10.0" sorts BEFORE "1.9.0" lexically, so last-sorted would pick 1.9.0.
	f.cacheBundleStack(t, "acme", "tools", "1.9.0", "node")
	f.cacheBundleStack(t, "acme", "tools", "1.10.0", "node")
	base := time.Now()
	f.cacheSourceMeta(t, "acme", "tools", url, "master", map[string]time.Time{
		"1.9.0":  base,
		"1.10.0": base.Add(time.Hour),
	})
	f.declareRemote(t, url, "master")

	bundles, _, err := f.r.Bundles()
	require.NoError(t, err)
	rb, ok := bundles[BundleID{Namespace: "acme", Name: "tools"}]
	require.True(t, ok)
	assert.Equal(t, "1.10.0", rb.Version, "the most recently fetched version wins")
}

func TestResolve_QualifiedNotCached(t *testing.T) {
	f := newResolverFixture(t)
	_, err := f.r.Resolve(ComponentStack, "acme.tools.node")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotCached)
}

func TestBundles_C1Collision(t *testing.T) {
	f := newResolverFixture(t)
	a := filepath.Join(f.projectDir, "vendor", "a")
	b := filepath.Join(f.projectDir, "vendor", "b")
	writeManifest(t, a, "namespace: acme\nname: tools\n")
	writeFile(t, a, "stacks/node/stack.yaml", "description: a\n")
	writeManifest(t, b, "namespace: acme\nname: tools\n")
	writeFile(t, b, "stacks/node/stack.yaml", "description: b\n")

	f.decls = []config.BundleDeclaration{
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/a"},
			File:   filepath.Join(f.projectDir, "project.clawker.yaml"),
		},
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/b"},
			File:   filepath.Join(f.projectDir, "user.clawker.yaml"),
		},
	}

	_, _, err := f.r.Bundles()
	require.Error(t, err)
	var ce *CollisionError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, BundleID{Namespace: "acme", Name: "tools"}, ce.Identity)
	assert.Contains(t, err.Error(), "project.clawker.yaml")
	assert.Contains(t, err.Error(), "user.clawker.yaml")
}

func TestBundles_C1IdempotentSameSource(t *testing.T) {
	f := newResolverFixture(t)
	dir := filepath.Join(f.projectDir, "vendor", "tools")
	writeManifest(t, dir, "namespace: acme\nname: tools\n")
	writeFile(t, dir, "stacks/node/stack.yaml", "description: x\n")

	// The same source declared twice (e.g. project + .local) is idempotent —
	// including under cosmetically different spellings of the same directory
	// (relative vs absolute vs unclean, anchored at each declaring file's own
	// directory): the claim key is the resolved absolute path, so no false C1.
	f.decls = []config.BundleDeclaration{
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/tools"},
			File:   filepath.Join(f.projectDir, "a.yaml"),
		},
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "vendor/tools"},
			File:   filepath.Join(f.projectDir, "b.yaml"),
		},
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: dir},
			File:   filepath.Join(f.projectDir, "sub", "c.yaml"), // absolute path — anchor immaterial
		},
		{
			Source: config.BundleSource{
				URL:        "",
				Ref:        "",
				SHA:        "",
				AutoUpdate: false,
				Path:       "../project/vendor/../vendor/tools/",
			},
			File: filepath.Join(f.projectDir, "d.yaml"),
		},
	}
	bundles, _, err := f.r.Bundles()
	require.NoError(t, err)
	require.Len(t, bundles, 1)
}

// A declaration in the user config-dir clawker.yaml anchors its relative path
// at the config dir — the same one rule as every other layer, no project root
// involved.
func TestBundles_ConfigDirLayerRelativePath(t *testing.T) {
	f := newResolverFixture(t)
	f.cfg.ProjectRootFunc = func() string { return "" } // no project in sight
	dir := filepath.Join(consts.ConfigDir(), "vendor", "tools")
	writeManifest(t, dir, "namespace: acme\nname: tools\n")
	writeFile(t, dir, "stacks/node/stack.yaml", "description: x\n")
	f.decls = []config.BundleDeclaration{
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/tools"},
			File:   filepath.Join(consts.ConfigDir(), "clawker.yaml"),
		},
	}

	bundles, _, err := f.r.Bundles()
	require.NoError(t, err)
	require.Len(t, bundles, 1)
	b, ok := bundles[BundleID{Namespace: "acme", Name: "tools"}]
	require.True(t, ok)
	assert.Equal(t, TierInPlace, b.Tier)
}

func TestList_BareShadowRows(t *testing.T) {
	f := newResolverFixture(t)
	f.looseUserStack(t, "node")
	f.looseProjectStack(t, "node")

	components, _, err := f.r.List(ComponentStack)
	require.NoError(t, err)

	byName := map[string]Component{}
	for _, c := range components {
		byName[c.Address.String()] = c
	}
	node, ok := byName["node"]
	require.True(t, ok)
	assert.Equal(t, TierLooseUser, node.Provenance.Tier)
	require.True(t, node.Provenance.Shadowed())
	// It shadows both the project loose dir and the floor.
	require.Len(t, node.Provenance.Shadows, 2)
	assert.Equal(t, TierLooseProject, node.Provenance.Shadows[0].Tier)
	assert.Equal(t, TierFloor, node.Provenance.Shadows[1].Tier)

	// Floor-only stacks still list once, unshadowed.
	goStack, ok := byName["go"]
	require.True(t, ok)
	assert.Equal(t, TierFloor, goStack.Provenance.Tier)
	assert.False(t, goStack.Provenance.Shadowed())
}

func TestList_QualifiedInstalledRows(t *testing.T) {
	f := newResolverFixture(t)
	f.installBundleStack(t)

	components, _, err := f.r.List(ComponentStack)
	require.NoError(t, err)
	var found bool
	for _, c := range components {
		if c.Address.String() == "acme.tools.node" {
			found = true
			assert.Equal(t, TierInstalled, c.Provenance.Tier)
		}
	}
	assert.True(t, found, "installed bundle stacks are listed with qualified addresses")
}

func TestBundles_SurfacesWarnings(t *testing.T) {
	f := newResolverFixture(t)
	dir := filepath.Join(f.projectDir, "vendor", "tools")
	writeManifest(t, dir, "namespace: acme\nname: tools\n")
	writeFile(t, dir, "stacks/node/stack.yaml", "description: x\n")
	writeFile(t, dir, "stack/typo/keep.txt", "x") // unknown dir warning
	f.decls = []config.BundleDeclaration{
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/tools"},
			File:   filepath.Join(f.projectDir, "clawker.yaml"),
		},
	}

	_, warnings, err := f.r.Bundles()
	require.NoError(t, err)
	require.NotEmpty(t, warnings)
	assert.Contains(t, warnings[0].Message, "unknown top-level directory")
}

func TestResolveLocalPath(t *testing.T) {
	t.Run("relative anchors to the declaring file's directory", func(t *testing.T) {
		got, err := resolveLocalPath(
			Source{URL: "", Ref: "", SHA: "", Path: "./vendor/b"},
			filepath.Join("/cfg", "home", "clawker.yaml"),
		)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("/cfg", "home", "vendor", "b"), got)
	})

	t.Run("absolute path ignores the declaring file", func(t *testing.T) {
		got, err := resolveLocalPath(Source{URL: "", Ref: "", SHA: "", Path: "/opt/b/"}, "")
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean("/opt/b"), got)
	})

	t.Run("relative with no declaring file is a SourceError", func(t *testing.T) {
		_, err := resolveLocalPath(Source{URL: "", Ref: "", SHA: "", Path: "./rel"}, "")
		var se *SourceError
		require.ErrorAs(t, err, &se)
	})
}

func TestSelectVersion(t *testing.T) {
	base := time.Now()
	ib := InstalledBundle{
		ID:       BundleID{Namespace: "acme", Name: "tools"},
		Root:     "",
		Versions: []string{"1.10.0", "1.9.0"}, // sorted; last-sorted is 1.9.0
	}
	meta := func(fetched map[string]time.Time) sourceMeta {
		versions := map[string]versionMeta{}
		for v, at := range fetched {
			// Legacy shape: no per-version pin recorded (selectVersion is the
			// fallback path for exactly these entries).
			versions[v] = versionMeta{SHA: "", FetchedAt: at, Pin: ""}
		}
		return sourceMeta{URL: "https://example.com/x.git", Ref: "master", SHA: "", Subdir: "", Versions: versions}
	}

	t.Run("most recently fetched wins over sort order", func(t *testing.T) {
		m := meta(map[string]time.Time{"1.9.0": base, "1.10.0": base.Add(time.Hour)})
		assert.Equal(t, "1.10.0", selectVersion(ib, m))
	})

	t.Run("FetchedAt tie settles on the later directory name", func(t *testing.T) {
		m := meta(map[string]time.Time{"1.9.0": base, "1.10.0": base})
		assert.Equal(t, "1.9.0", selectVersion(ib, m))
	})

	t.Run("no recorded fetches falls back to last-sorted", func(t *testing.T) {
		m := meta(nil)
		assert.Equal(t, "1.9.0", selectVersion(ib, m))
	})
}

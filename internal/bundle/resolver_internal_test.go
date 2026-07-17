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

// cacheBundleEntry writes a value-keyed cache entry — the content root at
// <ns>/<name>/<key> shipping one stack component — and returns the entry root.
// Without a receipt it models a hand-placed entry.
func (f *resolverFixture) cacheBundleEntry(t *testing.T, ns, name, key, version, stack string) string {
	t.Helper()
	root, err := consts.BundlesSubdir()
	require.NoError(t, err)
	entryRoot := filepath.Join(root, ns, name, key)
	writeManifest(t, entryRoot, "namespace: "+ns+"\nname: "+name+"\nversion: "+version+"\n")
	writeFile(t, entryRoot, "stacks/"+stack+"/stack.yaml", "description: "+stack+"\n")
	return entryRoot
}

// cacheReceipt writes the fetch receipt an install leaves inside an entry.
func (f *resolverFixture) cacheReceipt(t *testing.T, entryRoot string, src Source, version string) {
	t.Helper()
	require.NoError(t, writeReceipt(entryRoot, fetchReceipt{
		Canonical: src.Canonical(), SHA: "", FetchedAt: time.Now(), Version: version,
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
// (acme.tools@1.0.0 shipping the node stack): the value-keyed entry with its
// receipt, and a live declaration addressing it — the state `bundle install`
// leaves behind.
func (f *resolverFixture) installBundleStack(t *testing.T) {
	t.Helper()
	const url = "https://example.com/acme/tools.git"
	src := Source{URL: url, Ref: "v1", SHA: "", Path: ""}
	entry := f.cacheBundleEntry(t, "acme", "tools", src.Key(), "1.0.0", "node")
	f.cacheReceipt(t, entry, src, "1.0.0")
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
	// Cached with a receipt, but its declaration was removed.
	src := Source{URL: "https://example.com/acme/tools.git", Ref: "v1", SHA: "", Path: ""}
	entry := f.cacheBundleEntry(t, "acme", "tools", src.Key(), "1.0.0", "node")
	f.cacheReceipt(t, entry, src, "1.0.0")
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

// Declaration-gating: the cache is value-keyed, so a cache entry resolves ONLY
// while a live declaration's exact value addresses its key. Deleting the
// `bundles:` entry makes the cached copy inert; editing any part of the value
// (the ref, the url form) addresses a different key.
func TestBundles_CacheDeclarationGating(t *testing.T) {
	const url = "https://example.com/acme/tools.git"
	srcV1 := Source{URL: url, Ref: "v1", SHA: "", Path: ""}

	t.Run("undeclared cache entry is inert", func(t *testing.T) {
		f := newResolverFixture(t)
		entry := f.cacheBundleEntry(t, "acme", "tools", srcV1.Key(), "1.0.0", "node")
		f.cacheReceipt(t, entry, srcV1, "1.0.0")

		_, err := f.r.Resolve(ComponentStack, "acme.tools.node")
		assert.ErrorIs(t, err, ErrNotCached)
	})

	t.Run("re-declaring reactivates without a refetch", func(t *testing.T) {
		f := newResolverFixture(t)
		entry := f.cacheBundleEntry(t, "acme", "tools", srcV1.Key(), "1.0.0", "node")
		f.cacheReceipt(t, entry, srcV1, "1.0.0")
		f.declareRemote(t, url, "v1")

		c, err := f.r.Resolve(ComponentStack, "acme.tools.node")
		require.NoError(t, err)
		assert.Equal(t, TierInstalled, c.Provenance.Tier)
	})

	t.Run("a declaration with a different pin addresses a different key", func(t *testing.T) {
		f := newResolverFixture(t)
		entry := f.cacheBundleEntry(t, "acme", "tools", srcV1.Key(), "1.0.0", "node")
		f.cacheReceipt(t, entry, srcV1, "1.0.0")
		f.declareRemote(t, url, "v2")

		_, err := f.r.Resolve(ComponentStack, "acme.tools.node")
		assert.ErrorIs(t, err, ErrNotCached)
	})

	t.Run("a different url form addresses a different key", func(t *testing.T) {
		f := newResolverFixture(t)
		entry := f.cacheBundleEntry(t, "acme", "tools", srcV1.Key(), "1.0.0", "node")
		f.cacheReceipt(t, entry, srcV1, "1.0.0")
		f.declareRemote(t, "git@example.com:acme/tools.git", "v1")

		_, err := f.r.Resolve(ComponentStack, "acme.tools.node")
		assert.ErrorIs(t, err, ErrNotCached)
	})

	t.Run("an entry at a key no value digests to never resolves", func(t *testing.T) {
		f := newResolverFixture(t)
		f.cacheBundleEntry(t, "acme", "tools", "handplaced00", "1.0.0", "node")
		f.declareRemote(t, url, "v1")

		_, err := f.r.Resolve(ComponentStack, "acme.tools.node")
		assert.ErrorIs(t, err, ErrNotCached)
	})
}

// The receipt is display-only: a corrupt .fetch.yaml degrades the version
// column with a warning, never resolution.
func TestBundles_CorruptReceiptStillResolves(t *testing.T) {
	f := newResolverFixture(t)
	const url = "https://example.com/acme/tools.git"
	src := Source{URL: url, Ref: "v1", SHA: "", Path: ""}
	entry := f.cacheBundleEntry(t, "acme", "tools", src.Key(), "1.0.0", "node")
	writeFile(t, entry, ReceiptFile, "canonical: [unclosed\n")
	f.declareRemote(t, url, "v1")

	bundles, warnings, err := f.r.Bundles()
	require.NoError(t, err)
	rb, ok := bundles[BundleID{Namespace: "acme", Name: "tools"}]
	require.True(t, ok, "a corrupt receipt must not block resolution")
	assert.Empty(t, rb.Version)
	require.NotEmpty(t, warnings)
	assert.Contains(t, warnings[len(warnings)-1].Message, "unreadable fetch receipt")
}

// Two remote declarations resolving the same identity from different values in
// one scope are a C1 collision — the resolver never silently picks a winner.
func TestBundles_TwoRemoteDeclsSameIdentityCollide(t *testing.T) {
	f := newResolverFixture(t)
	const url = "https://example.com/acme/tools.git"
	srcV1 := Source{URL: url, Ref: "v1", SHA: "", Path: ""}
	srcV2 := Source{URL: url, Ref: "v2", SHA: "", Path: ""}
	e1 := f.cacheBundleEntry(t, "acme", "tools", srcV1.Key(), "1.0.0", "node")
	f.cacheReceipt(t, e1, srcV1, "1.0.0")
	e2 := f.cacheBundleEntry(t, "acme", "tools", srcV2.Key(), "2.0.0", "node")
	f.cacheReceipt(t, e2, srcV2, "2.0.0")
	f.declareRemote(t, url, "v1")
	f.declareRemote(t, url, "v2")

	_, _, err := f.r.Bundles()
	var ce *CollisionError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, BundleID{Namespace: "acme", Name: "tools"}, ce.Identity)
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

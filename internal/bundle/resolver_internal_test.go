package bundle

import (
	"os"
	"path/filepath"
	"testing"

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

// cacheBundleStack writes a cached bundle shipping one stack component.
func (f *resolverFixture) cacheBundleStack(t *testing.T, ns, name, version, stack string) {
	t.Helper()
	root, err := consts.BundlesSubdir()
	require.NoError(t, err)
	versionRoot := filepath.Join(root, ns, name, version)
	writeManifest(t, versionRoot, "namespace: "+ns+"\nname: "+name+"\nversion: "+version+"\n")
	writeFile(t, versionRoot, "stacks/"+stack+"/stack.yaml", "description: "+stack+"\n")
}

func TestResolve_BareFloor(t *testing.T) {
	f := newResolverFixture(t)
	c, err := f.r.Resolve(ComponentStack, "node")
	require.NoError(t, err)
	assert.Equal(t, TierFloor, c.Provenance.Tier)
	assert.Equal(t, "node", c.Address.String())
}

func TestResolve_LooseShadowsFloor_C3(t *testing.T) {
	f := newResolverFixture(t)
	f.looseProjectStack(t, "node")
	c, err := f.r.Resolve(ComponentStack, "node")
	require.NoError(t, err)
	assert.Equal(t, TierLooseProject, c.Provenance.Tier, "loose project shadows the floor")
}

func TestResolve_UserShadowsProject_C4(t *testing.T) {
	f := newResolverFixture(t)
	f.looseProjectStack(t, "node")
	userDir := f.looseUserStack(t, "node")
	c, err := f.r.Resolve(ComponentStack, "node")
	require.NoError(t, err)
	assert.Equal(t, TierLooseUser, c.Provenance.Tier, "user loose beats project loose")
	assert.Equal(t, userDir, c.Provenance.Dir)
}

// A bare resolution must never scan the bundle set, so a broken bundle
// declaration cannot block a floor-only resolve.
func TestResolve_BareIgnoresBrokenBundleDecl(t *testing.T) {
	f := newResolverFixture(t)
	f.decls = []config.BundleDeclaration{
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/broken"},
			File:   "clawker.yaml",
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

func TestResolve_QualifiedInstalled(t *testing.T) {
	f := newResolverFixture(t)
	f.cacheBundleStack(t, "acme", "tools", "1.0.0", "node")

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

func TestResolve_QualifiedInPlaceOverridesCache(t *testing.T) {
	f := newResolverFixture(t)
	// A cached bundle and an in-place declaration both claim acme/tools.
	f.cacheBundleStack(t, "acme", "tools", "1.0.0", "node")
	inPlace := filepath.Join(f.projectDir, "vendor", "tools")
	writeManifest(t, inPlace, "namespace: acme\nname: tools\n")
	writeFile(t, inPlace, "stacks/node/stack.yaml", "description: dev loop\n")
	f.decls = []config.BundleDeclaration{
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/tools"},
			File:   "clawker.yaml",
		},
	}

	c, err := f.r.Resolve(ComponentStack, "acme.tools.node")
	require.NoError(t, err)
	assert.Equal(t, TierInPlace, c.Provenance.Tier, "the in-place dev loop overrides the cache")
	assert.Equal(t, filepath.Join(inPlace, "stacks", "node"), c.Provenance.Dir)
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
			File:   "project.clawker.yaml",
		},
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/b"},
			File:   "user.clawker.yaml",
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
	// (relative vs absolute vs unclean): the claim key is the resolved
	// absolute path, so no false C1.
	f.decls = []config.BundleDeclaration{
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "./vendor/tools"},
			File:   "a.yaml",
		},
		{
			Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: "vendor/tools"},
			File:   "b.yaml",
		},
		{Source: config.BundleSource{URL: "", Ref: "", SHA: "", AutoUpdate: false, Path: dir}, File: "c.yaml"},
		{
			Source: config.BundleSource{
				URL:        "",
				Ref:        "",
				SHA:        "",
				AutoUpdate: false,
				Path:       "./vendor/../vendor/tools/",
			},
			File: "d.yaml",
		},
	}
	bundles, _, err := f.r.Bundles()
	require.NoError(t, err)
	require.Len(t, bundles, 1)
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
	f.cacheBundleStack(t, "acme", "tools", "1.0.0", "node")

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
			File:   "clawker.yaml",
		},
	}

	_, warnings, err := f.r.Bundles()
	require.NoError(t, err)
	require.NotEmpty(t, warnings)
	assert.Contains(t, warnings[0].Message, "unknown top-level directory")
}

func TestResolveLocalPath_RelativeNeedsProjectRoot(t *testing.T) {
	f := newResolverFixture(t)
	f.cfg.ProjectRootFunc = func() string { return "" }
	_, err := f.r.resolveLocalPath(Source{URL: "", Ref: "", SHA: "", Path: "./rel"})
	var se *SourceError
	require.ErrorAs(t, err, &se)
}

func TestSelectVersion(t *testing.T) {
	assert.Empty(t, selectVersion(nil))
	assert.Equal(t, "1.0.0", selectVersion([]string{"1.0.0"}))
	// Deterministic pick among multiple until source-driven pinning lands.
	assert.Equal(t, "2.0.0", selectVersion([]string{"1.0.0", "2.0.0"}))
}

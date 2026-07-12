package bundler //nolint:testpackage // shares in-package helpers (testConfig, newTestProjectGenerator, provenanceLine)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/testenv"
)

// loadFloorStack loads a shipped stack straight from the embedded floor.
func loadFloorStack(t *testing.T, name string) *StackDefinition {
	t.Helper()
	fsys, err := bundle.FloorFS(bundle.ComponentStack, name)
	require.NoError(t, err)
	def, err := LoadStackDefinition(name, fsys)
	require.NoError(t, err)
	return def
}

func TestShippedStacks_LoadAndFragments(t *testing.T) {
	assert.Equal(t, []string{"go", "node", "python", "rust"}, ShippedStackNames())

	// Which scopes each language stack provisions, with the guard
	// marker proving each fragment self-guards against an existing install.
	wantRootGuard := map[string]string{
		"go":     "command -v go",
		"node":   "command -v node",
		"python": "command -v python3",
	}
	wantUserGuard := map[string]string{
		"node": ".nvm/nvm.sh",
		"rust": ".cargo/bin/cargo",
	}
	for _, name := range ShippedStackNames() {
		def := loadFloorStack(t, name)
		if marker, ok := wantRootGuard[name]; ok {
			assert.Contains(t, def.RootFragment, marker,
				"%s root fragment must self-guard", name)
		} else {
			assert.Empty(t, def.RootFragment, "%s ships no root fragment", name)
		}
		if marker, ok := wantUserGuard[name]; ok {
			assert.Contains(t, def.UserFragment, marker,
				"%s user fragment must self-guard", name)
		} else {
			assert.Empty(t, def.UserFragment, "%s ships no user fragment", name)
		}
	}
}

func TestShippedStacks_RenderBothBuildKitModes(t *testing.T) {
	for _, name := range ShippedStackNames() {
		def := loadFloorStack(t, name)
		root, user := splitFragments([]*StackDefinition{def})
		for _, buildKit := range []bool{false, true} {
			// Fragment renders touch only BuildKitEnabled.
			var tctx DockerfileContext
			tctx.BuildKitEnabled = buildKit
			for _, fragments := range [][]namedFragment{root, user} {
				steps, renderErr := renderStackSteps(fragments, &tctx)
				require.NoError(t, renderErr, "%s buildkit=%v", name, buildKit)
				for _, step := range steps {
					assert.Contains(t, step, "RUN", "%s must emit at least one RUN", name)
				}
			}
		}
	}
}

// looseResolver isolates the XDG dirs, anchors a temp project root, and returns
// a resolver over the real filesystem tiers plus that project root. Loose and
// installed fixtures are written under it with the write* helpers.
func looseResolver(t *testing.T) (*bundle.Resolver, string) {
	t.Helper()
	env := testenv.New(t)
	root := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(root, 0o755))
	cfg := configmocks.NewFromString("", "")
	cfg.ProjectRootFunc = func() string { return root }
	return bundle.NewResolver(cfg), root
}

// writeLooseStack writes a loose project-tier stack component (one root
// fragment) under root/.clawker/stacks/<name>/.
func writeLooseStack(t *testing.T, root, name, rootFragment string) {
	t.Helper()
	dir := filepath.Join(root, consts.DotClawkerDir, bundle.ComponentStack.Dir(), name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, StackManifestFile), []byte("description: "+name+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, StackRootFragmentFile), []byte(rootFragment), 0o644))
}

// installedBundleURL is the remote source coordinate writeInstalledStack
// records in the planted cache entry's source.yaml; a declaration of the same
// url+ref (declareInstalledBundle, or a matching bundles: yaml entry) is what
// makes the cached bundle resolvable.
func installedBundleURL(ns, name string) string {
	return "https://example.com/" + ns + "/" + name + ".git"
}

// declareInstalledBundle wires a live declaration matching a planted cache
// entry onto the config mock — the resolver gates cached bundles on it.
func declareInstalledBundle(cfg *configmocks.ConfigMock, ns, name string) {
	cfg.BundleDeclarationsFunc = func() []config.BundleDeclaration {
		return []config.BundleDeclaration{
			{
				Source: config.BundleSource{
					URL: installedBundleURL(ns, name), Ref: "v1", SHA: "", Path: "", AutoUpdate: false,
				},
				File: "clawker.yaml",
			},
		}
	}
}

// writeInstalledStack writes a cached bundle shipping one stack component into
// the isolated bundle cache, with the source.yaml linking it to
// installedBundleURL — a matching declaration makes the qualified address
// resolve to it.
func writeInstalledStack(t *testing.T, ns, name, version, stack, rootFragment string) {
	t.Helper()
	cacheRoot, err := consts.BundlesSubdir()
	require.NoError(t, err)
	verRoot := filepath.Join(cacheRoot, ns, name, version)
	marker := filepath.Join(verRoot, bundle.MarkerDir)
	require.NoError(t, os.MkdirAll(marker, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(marker, bundle.ManifestFile),
		[]byte("namespace: "+ns+"\nname: "+name+"\nversion: "+version+"\n"), 0o644))
	sdir := filepath.Join(verRoot, bundle.ComponentStack.Dir(), stack)
	require.NoError(t, os.MkdirAll(sdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sdir, StackManifestFile), []byte("description: "+stack+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sdir, StackRootFragmentFile), []byte(rootFragment), 0o644))
	srcYAML := "url: " + installedBundleURL(ns, name) + "\nref: v1\nversions:\n" +
		"  \"" + version + "\":\n    sha: \"\"\n    fetched_at: 2026-01-01T00:00:00Z\n"
	require.NoError(t, os.WriteFile(filepath.Join(cacheRoot, ns, name, "source.yaml"), []byte(srcYAML), 0o644))
}

// A bare stack with no loose override resolves straight from the embedded
// floor (the virtual base), unshadowed.
func TestResolveStack_Floor(t *testing.T) {
	r, _ := looseResolver(t)
	def, comp, err := resolveStack(r, "node")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "nodejs.org/dist")
	assert.Contains(t, def.UserFragment, "nvm-sh/nvm", "the node stack provisions nvm in user scope")
	assert.Equal(t, bundle.TierFloor, comp.Provenance.Tier)
}

// A loose project stack wins over the floor stack of the same name, wholesale;
// the build records where it resolved from.
func TestResolveStack_LooseShadowsFloor(t *testing.T) {
	r, root := looseResolver(t)
	writeLooseStack(t, root, "node", "RUN echo loose-node\n")
	def, comp, err := resolveStack(r, "node")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "loose-node", "the loose project stack wins wholesale")
	assert.Empty(t, def.UserFragment, "a wholesale win never merges the floor's user fragment")
	assert.Equal(t, bundle.TierLooseProject, comp.Provenance.Tier)
	assert.Contains(t, provenanceLine(comp), "stack node ← project (")
}

// A qualified address resolves from the installed bundle set only — and only
// while its source is declared.
func TestResolveStack_QualifiedInstalled(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewFromString("", "")
	declareInstalledBundle(cfg, "acme", "tools")
	r := bundle.NewResolver(cfg)
	writeInstalledStack(t, "acme", "tools", "1.0.0", "special", "RUN echo installed-special\n")
	def, comp, err := resolveStack(r, "acme.tools.special")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "installed-special")
	assert.Equal(t, bundle.TierInstalled, comp.Provenance.Tier)
	assert.Equal(t, "acme.tools.special", comp.Address.String())
	assert.Contains(t, provenanceLine(comp), "stack acme.tools.special ← bundle acme.tools")
}

// A name that resolves on no tier is a hard, loud error — never a silent skip.
func TestResolveStack_Unknown(t *testing.T) {
	r, _ := looseResolver(t)
	_, _, err := resolveStack(r, "no-such-stack")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// tcSettingsYAML returns settings with full telemetry defaults (monitoring is
// the only settings surface these generator tests need — component selection
// lives in the project config, resolution in the filesystem tiers).
func tcSettingsYAML() string {
	return `
monitoring:
  otel_collector_port: 4318
  otel_collector_host: "localhost"
  telemetry:
    metric_export_interval_ms: 10000
    logs_export_interval_ms: 5000
    log_tool_details: true
    log_user_prompts: true
    include_account_uuid: true
    include_session_id: true
`
}

// writeLooseHarness writes a loose project harness named "other" under
// root/.clawker/harnesses/other/, with the given manifest verbatim and a
// template carrying position markers for block_1 and block_3.
func writeLooseHarness(t *testing.T, root, manifest string) {
	t.Helper()
	dir := filepath.Join(root, consts.DotClawkerDir, bundle.ComponentHarness.Dir(), "other")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, HarnessManifestFile), []byte(manifest), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, HarnessTemplateFile),
		[]byte(`{{define "block_1"}}RUN echo B1MARK{{end}}
{{define "block_3"}}RUN echo B3MARK{{end}}`),
		0o644,
	))
}

// tcGenerator builds a ProjectGenerator selecting a loose project harness
// "other" whose manifest is bundleManifest, over an isolated filesystem.
func tcGenerator(t *testing.T, projectBody, bundleManifest string) *ProjectGenerator {
	t.Helper()
	env := testenv.New(t)
	root := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(root, 0o755))
	writeLooseHarness(t, root, bundleManifest)
	cfg := configmocks.NewFromString(projectBody, tcSettingsYAML())
	cfg.ProjectRootFunc = func() string { return root }
	gen := NewProjectGenerator(cfg, t.TempDir())
	gen.Harness = "other"
	gen.HarnessVersion = testHarnessVersion
	gen.BaseImageRef = testBaseImageRef
	return gen
}

const (
	nodeMarker   = "nodejs.org/dist"
	nvmMarker    = "nvm-sh/nvm"
	pythonMarker = "astral.sh" // python (uv installer) root fragment
)

// build.stacks places project-declared stacks in the BASE image; the base is
// harness-agnostic, so the harness image carries none of them.
func TestGenerateBase_ProjectDeclaredStacks(t *testing.T) {
	gen := tcGenerator(t, `
build:
  stacks: [node]
  instructions:
    root_run: ["echo ROOTMARK"]
    user_run: ["echo USERMARK"]
`, "version: { resolver: none }\n")

	base, err := gen.GenerateBase()
	require.NoError(t, err)
	content := string(base)

	nodeIdx := strings.Index(content, nodeMarker)
	rootRunIdx := strings.Index(content, "echo ROOTMARK")
	require.GreaterOrEqual(t, nodeIdx, 0, "node root fragment must render in the base")
	require.GreaterOrEqual(t, rootRunIdx, 0)
	assert.Less(t, nodeIdx, rootRunIdx, "root fragment must precede root_run")

	nvmIdx := strings.Index(content, nvmMarker)
	userRunIdx := strings.Index(content, "echo USERMARK")
	require.GreaterOrEqual(t, nvmIdx, 0,
		"node user fragment (nvm) must render in the base — one declaration provisions both scopes")
	require.GreaterOrEqual(t, userRunIdx, 0)
	assert.Less(t, nvmIdx, userRunIdx, "user fragment must precede user_run")

	// The harness declares no stacks, so its image carries none — the base's
	// project stacks don't leak into the harness image.
	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	assert.NotContains(t, string(harnessImg), nodeMarker)
	assert.NotContains(t, string(harnessImg), nvmMarker)
}

// A harness's stacks: dependency places stacks in that harness image only.
func TestGenerateHarness_HarnessDeclaredStacks(t *testing.T) {
	gen := tcGenerator(t, "version: \"1\"\n", `
version: { resolver: none }
stacks: [node]
`)

	// Nothing project-declared → base carries no stack bytes.
	base, err := gen.GenerateBase()
	require.NoError(t, err)
	assert.NotContains(t, string(base), nodeMarker)
	assert.NotContains(t, string(base), nvmMarker)

	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	content := string(harnessImg)

	// Root-scope fragment renders in root scope, before block_1.
	nodeIdx := strings.Index(content, nodeMarker)
	b1Idx := strings.Index(content, "B1MARK")
	userRootIdx := strings.Index(content, "USER root")
	require.GreaterOrEqual(t, nodeIdx, 0)
	require.GreaterOrEqual(t, b1Idx, 0)
	assert.Less(t, userRootIdx, nodeIdx, "root stack anchors after USER root")
	assert.Less(t, nodeIdx, b1Idx, "root stack must precede block_1")

	// User-scope fragment renders after the USER switch + ARG ZSH_ENV
	// (fragments may reference ${ZSH_ENV}), before block_3.
	nvmIdx := strings.Index(content, nvmMarker)
	b3Idx := strings.Index(content, "B3MARK")
	zshEnvIdx := strings.Index(content, "ARG ZSH_ENV")
	require.GreaterOrEqual(t, nvmIdx, 0)
	require.GreaterOrEqual(t, b3Idx, 0)
	require.GreaterOrEqual(t, zshEnvIdx, 0)
	assert.Less(t, zshEnvIdx, nvmIdx, "ARG ZSH_ENV must be in scope for user fragments")
	assert.Less(t, nvmIdx, b3Idx, "user stack must precede block_3")
}

// A name declared in both build.stacks and the harness manifest renders in BOTH
// the base and the harness image — no cross-stratum dedup; fragment self-guards
// own any interaction (design §2).
func TestGenerate_BothDeclared_BothRender(t *testing.T) {
	gen := tcGenerator(t, `
build:
  stacks: [node]
`, `
version: { resolver: none }
stacks: [node]
`)

	base, err := gen.GenerateBase()
	require.NoError(t, err)
	assert.Contains(t, string(base), nodeMarker, "project-declared node renders in the base")

	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	assert.Contains(t, string(harnessImg), nodeMarker,
		"harness-declared node ALSO renders in the harness image — no cross-stratum dedup")
}

// A harness declaring a bare stack whose name a loose project dir also defines
// renders the loose definition (it wins wholesale over the floor) and the
// generator surfaces where each component resolved from.
func TestGenerateHarness_LooseStackShadowsFloor(t *testing.T) {
	env := testenv.New(t)
	root := filepath.Join(env.Dirs.Base, "project")
	require.NoError(t, os.MkdirAll(root, 0o755))
	writeLooseHarness(t, root, "version: { resolver: none }\nstacks: [node]\n")
	writeLooseStack(t, root, "node", "RUN echo loose-node-shadow\n")

	cfg := configmocks.NewFromString("version: \"1\"\n", tcSettingsYAML())
	cfg.ProjectRootFunc = func() string { return root }
	gen := NewProjectGenerator(cfg, t.TempDir())
	gen.Harness = "other"
	gen.HarnessVersion = testHarnessVersion
	gen.BaseImageRef = testBaseImageRef

	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	assert.Contains(t, string(harnessImg), "loose-node-shadow", "the loose node definition wins over the floor")
	assert.NotContains(t, string(harnessImg), nodeMarker, "floor node is shadowed, not rendered")

	prov := gen.Provenance()
	require.NotEmpty(t, prov)
	joined := strings.Join(prov, "\n")
	assert.Contains(t, joined, "stack node ← project (", "the loose stack resolution is surfaced")
	assert.Contains(t, joined, "harness other ← project (", "the loose harness names its source")
}

// The harness bundle always names its resolved source, floor included.
func TestLoadHarnessResolved_Provenance(t *testing.T) {
	t.Run("floor names its source", func(t *testing.T) {
		r, _ := looseResolver(t)
		_, comp, err := loadHarnessResolved(r, DefaultHarnessName)
		require.NoError(t, err)
		assert.Equal(t, bundle.TierFloor, comp.Provenance.Tier)
		assert.Equal(t, "harness "+DefaultHarnessName+" ← built-in", provenanceLine(comp))
	})

	t.Run("loose harness names its dir", func(t *testing.T) {
		r, root := looseResolver(t)
		writeLooseHarness(t, root, "version: { resolver: none }\n")
		_, comp, err := loadHarnessResolved(r, "other")
		require.NoError(t, err)
		assert.Equal(t, bundle.TierLooseProject, comp.Provenance.Tier)
		assert.Contains(t, provenanceLine(comp), "harness other ← project (")
	})
}

// A build.stacks entry resolving nowhere is a hard, loud error.
func TestGenerateBase_UnknownStack(t *testing.T) {
	gen := tcGenerator(t, `
build:
  stacks: [definitely-not-a-stack]
`, "version: { resolver: none }\n")

	_, err := gen.GenerateBase()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// build.stacks rejects a repeated name (a harness installer+overlay list
// renders a repeat once).
func TestGenerateBase_DuplicateDeclaration(t *testing.T) {
	gen := tcGenerator(t, `
build:
  stacks: [node, node]
`, "version: { resolver: none }\n")

	_, err := gen.GenerateBase()
	require.ErrorContains(t, err, "duplicate stack declaration")
}

// Stacks render top-to-bottom in declaration order; the engine never reorders.
// A ≥3-stack base list whose fragments each carry a unique marker must land in
// the rendered Dockerfile in exact declaration order — red if resolveStackDecls
// or splitFragments ever sorts.
func TestGenerateBase_StacksRenderInDeclarationOrder(t *testing.T) {
	// python, go, node all ship root fragments carrying a distinct upstream
	// marker; declaring them out of alphabetical order proves no sort sneaks in.
	gen := tcGenerator(t, `
build:
  stacks: [python, go, node]
`, "version: { resolver: none }\n")

	base, err := gen.GenerateBase()
	require.NoError(t, err)
	content := string(base)

	pythonIdx := strings.Index(content, pythonMarker)
	goIdx := strings.Index(content, goStackMarker)
	nodeIdx := strings.Index(content, nodeMarker)
	require.GreaterOrEqual(t, pythonIdx, 0, "python root fragment must render")
	require.GreaterOrEqual(t, goIdx, 0, "go root fragment must render")
	require.GreaterOrEqual(t, nodeIdx, 0, "node root fragment must render")

	assert.Less(t, pythonIdx, goIdx, "python (declared 1st) must precede go (declared 2nd)")
	assert.Less(t, goIdx, nodeIdx, "go (declared 2nd) must precede node (declared 3rd)")
}

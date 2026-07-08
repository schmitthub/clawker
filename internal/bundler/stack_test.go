package bundler //nolint:testpackage // shares in-package test helpers (testConfig, newTestProjectGenerator)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/harness"
	"github.com/schmitthub/clawker/internal/stack"
)

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
		def, err := loadEmbeddedStack(name)
		require.NoError(t, err, "shipped definition %s must load", name)
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
		def, err := loadEmbeddedStack(name)
		require.NoError(t, err)
		root, user := splitFragments([]*stack.Definition{def})
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

// writeStackDef writes a minimal definition dir (one fragment at the
// given scope file) and returns its path.
func writeStackDef(t *testing.T, fragmentFile, fragment string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, stack.ManifestFile), []byte("description: test\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, fragmentFile), []byte(fragment), 0o644))
	return dir
}

// bundleWithStack builds a loaded harness bundle embedding one
// stack definition.
func bundleWithStack(t *testing.T, tcName string) *harness.Bundle {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, harness.ManifestFile), []byte("{}\n"), 0o644))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, harness.TemplateFile), []byte(`{{define "block_4"}}RUN echo hi{{end}}`), 0o644),
	)
	tcDir := filepath.Join(dir, stack.StacksSubdir, tcName)
	require.NoError(t, os.MkdirAll(tcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, stack.ManifestFile), []byte("description: test\n"), 0o644))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(tcDir, stack.RootFragmentFile),
			[]byte("RUN echo bundle-stack-"+tcName+"\n"),
			0o644,
		),
	)
	b, err := harness.Load("bundletest", os.DirFS(dir))
	require.NoError(t, err)
	return b
}

func TestResolveStack_ShippedVirtualBase(t *testing.T) {
	// No project registry entry → shipped definitions resolve straight from
	// the embedded FS (the virtual base layer), with no shadow provenance.
	cfg := configmocks.NewFromString("", "")

	def, prov, err := resolveStack(cfg, nil, "node")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "nodejs.org/dist")
	assert.Contains(t, def.UserFragment, "nvm-sh/nvm",
		"the node stack provisions nvm in user scope")
	assert.Equal(t, "shipped", prov.source)
	assert.Empty(t, prov.shadows, "an unshadowed shipped resolution has no provenance line")
}

func TestResolveStack_ProjectRegistryWins(t *testing.T) {
	dir := writeStackDef(t, stack.UserFragmentFile, "RUN echo custom-def\n")
	cfg := configmocks.NewFromString(`
stacks:
  mytool:
    path: `+dir+`
`, "")

	def, prov, err := resolveStack(cfg, nil, "mytool")
	require.NoError(t, err)
	assert.Empty(t, def.RootFragment)
	assert.Contains(t, def.UserFragment, "custom-def")
	assert.Contains(t, prov.source, "project (")
	assert.Empty(t, prov.shadows, "no shipped/bundle definition named mytool → nothing shadowed")
}

func TestResolveStack_ProjectRegistryMissing(t *testing.T) {
	cfg := configmocks.NewFromString(`
stacks:
  mytool:
    path: /nonexistent/definitely/missing
`, "")

	_, _, err := resolveStack(cfg, nil, "mytool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stacks.mytool.path")
}

func TestResolveStack_RelativeRegistryPathResolvesAgainstProjectRoot(t *testing.T) {
	root := t.TempDir()
	stackDir := filepath.Join(root, "stacks", "mytool")
	require.NoError(t, os.MkdirAll(stackDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, stack.ManifestFile), []byte("description: rel\n"), 0o644))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(stackDir, stack.RootFragmentFile), []byte("RUN echo rel-def\n"), 0o644),
	)

	cfg := configmocks.NewFromString(`
stacks:
  mytool:
    path: ./stacks/mytool
`, "")
	cfg.ProjectRootFunc = func() string { return root }

	def, _, err := resolveStack(cfg, nil, "mytool")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "rel-def")
}

func TestResolveStack_RelativeRegistryPathWithoutRootErrors(t *testing.T) {
	// A relative registry path with no resolved project root must hard-error —
	// resolving it against the process CWD could silently load whatever
	// happens to live there.
	cfg := configmocks.NewFromString(`
stacks:
  mytool:
    path: ./stacks/mytool
`, "")

	_, _, err := resolveStack(cfg, nil, "mytool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no project root is resolved")
}

func TestResolveStack_BundleEmbedded(t *testing.T) {
	cfg := configmocks.NewFromString("", "")
	b := bundleWithStack(t, "codex-special")

	def, prov, err := resolveStack(cfg, b, "codex-special")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "bundle-stack-codex-special")
	assert.Equal(t, "bundletest bundle", prov.source)
	assert.Empty(t, prov.shadows)
}

func TestResolveStack_BundleShadowsShipped(t *testing.T) {
	// A bundle embedding a definition named like a shipped one WINS wholesale
	// (matching key at a closer layer), and the provenance records the shadow —
	// no error, no silent skip.
	cfg := configmocks.NewFromString("", "")
	b := bundleWithStack(t, "node")

	def, prov, err := resolveStack(cfg, b, "node")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "bundle-stack-node")
	assert.Equal(t, "bundletest bundle", prov.source)
	assert.Equal(t, []string{"shipped"}, prov.shadows)
	assert.Contains(t, prov.line(), "shadows shipped")
}

func TestResolveStack_ProjectShadowsBundle(t *testing.T) {
	dir := writeStackDef(t, stack.RootFragmentFile, "RUN echo registered\n")
	cfg := configmocks.NewFromString(`
stacks:
  mytool:
    path: `+dir+`
`, "")
	b := bundleWithStack(t, "mytool")

	def, prov, err := resolveStack(cfg, b, "mytool")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "registered", "the project registry wins wholesale")
	assert.Contains(t, prov.source, "project (")
	assert.Equal(t, []string{"bundletest bundle"}, prov.shadows)
}

func TestResolveStack_Unknown(t *testing.T) {
	cfg := configmocks.NewFromString("", "")

	_, _, err := resolveStack(cfg, nil, "no-such-stack")
	require.ErrorIs(t, err, ErrUnknownStack)
	assert.Contains(t, err.Error(), "clawker stack register")
}

// tcSettingsYAML returns settings with full telemetry defaults (monitoring is
// the only settings surface these generator tests need — harness/stack
// registration lives in the project config).
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

// tcBundleDir writes a harness bundle whose manifest is given verbatim and
// whose template carries position markers for block_1 and block_3.
func tcBundleDir(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, harness.ManifestFile), []byte(manifest), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, harness.TemplateFile),
		[]byte(`{{define "block_1"}}RUN echo B1MARK{{end}}
{{define "block_3"}}RUN echo B3MARK{{end}}`),
		0o644,
	))
	return dir
}

// projectYAMLWithHarness appends a project-side harness registry entry naming
// the "other" bundle at dir to a project config body.
func projectYAMLWithHarness(body, dir string) string {
	return body + "\nharnesses:\n  other:\n    path: " + dir + "\n"
}

// tcGenerator builds a ProjectGenerator selecting the project-registered
// "other" bundle whose manifest is bundleManifest.
func tcGenerator(t *testing.T, projectBody, bundleManifest string) *ProjectGenerator {
	t.Helper()
	dir := tcBundleDir(t, bundleManifest)
	cfg := configmocks.NewFromString(projectYAMLWithHarness(projectBody, dir), tcSettingsYAML())
	gen := NewProjectGenerator(cfg, t.TempDir())
	gen.Harness = "other"
	gen.HarnessVersion = testHarnessVersion
	gen.BaseImageRef = testBaseImageRef
	return gen
}

const (
	nodeMarker = "nodejs.org/dist"
	nvmMarker  = "nvm-sh/nvm"
)

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

	// The bundle declares no stacks, so the harness image carries none — this
	// asserts the base's project stacks don't leak into the harness image, not
	// cross-stratum dedup (which is dead).
	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	assert.NotContains(t, string(harnessImg), nodeMarker)
	assert.NotContains(t, string(harnessImg), nvmMarker)
}

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

// TestGenerate_BothDeclared_BothRender proves cross-stratum dedup is dead: a
// name declared in both build.stacks and the harness manifest renders in BOTH
// the base and the harness image (design §2 — fragment self-guards own any
// interaction, the engine never judges satisfaction).
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

// TestGenerateHarness_BundleShadowsShipped: a harness manifest declaring a name
// its bundle also embeds renders the bundle's definition (closer layer wins)
// and records shadow provenance surfaced via the generator.
func TestGenerateHarness_BundleShadowsShipped(t *testing.T) {
	bundleDir := tcBundleDir(t, "version: { resolver: none }\nstacks: [node]\n")
	tcDir := filepath.Join(bundleDir, stack.StacksSubdir, "node")
	require.NoError(t, os.MkdirAll(tcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, stack.ManifestFile), []byte("description: shadow\n"), 0o644))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(tcDir, stack.RootFragmentFile), []byte("RUN echo bundle-node-shadow\n"), 0o644),
	)

	cfg := configmocks.NewFromString(projectYAMLWithHarness("version: \"1\"\n", bundleDir), tcSettingsYAML())
	gen := NewProjectGenerator(cfg, t.TempDir())
	gen.Harness = "other"
	gen.HarnessVersion = testHarnessVersion
	gen.BaseImageRef = testBaseImageRef

	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	assert.Contains(t, string(harnessImg), "bundle-node-shadow", "the bundle's node definition wins over shipped")
	assert.NotContains(t, string(harnessImg), nodeMarker, "shipped node is shadowed, not rendered")

	prov := gen.Provenance()
	require.NotEmpty(t, prov)
	joined := strings.Join(prov, "\n")
	assert.Contains(t, joined, "stack node ← other bundle shadows shipped")
	assert.Contains(t, joined, "harness other ← ")
}

// writeBundleDir writes a minimal loadable harness bundle into dir.
func writeBundleDir(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, harness.ManifestFile), []byte("version:\n  resolver: none\n"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, harness.TemplateFile), []byte(`{{define "block_6"}}CMD ["x"]{{end}}`), 0o644))
}

// TestLoadHarnessResolved_Provenance exercises the harness-bundle provenance
// line (in-package so the unexported provenance type is reachable).
func TestLoadHarnessResolved_Provenance(t *testing.T) {
	t.Run("shipped names its source, no shadow", func(t *testing.T) {
		cfg := configmocks.NewFromString("", "")
		_, prov, err := loadHarnessResolved(cfg, DefaultHarnessName)
		require.NoError(t, err)
		assert.Equal(t, "shipped", prov.source)
		assert.False(t, prov.shadows)
		assert.Equal(t, "harness "+DefaultHarnessName+" ← shipped", prov.line())
	})

	t.Run("project entry shadowing a shipped bundle", func(t *testing.T) {
		dir := t.TempDir()
		writeBundleDir(t, dir)
		cfg := configmocks.NewFromString("harnesses:\n  "+DefaultHarnessName+":\n    path: "+dir+"\n", "")
		_, prov, err := loadHarnessResolved(cfg, DefaultHarnessName)
		require.NoError(t, err)
		assert.True(t, prov.shadows, "a project entry named like a shipped bundle shadows it")
		assert.Contains(t, prov.line(), "shadows shipped")
	})

	t.Run("custom project entry, no shadow", func(t *testing.T) {
		dir := t.TempDir()
		writeBundleDir(t, dir)
		cfg := configmocks.NewFromString("harnesses:\n  custom:\n    path: "+dir+"\n", "")
		_, prov, err := loadHarnessResolved(cfg, "custom")
		require.NoError(t, err)
		assert.False(t, prov.shadows, "custom is not shipped → nothing shadowed")
		assert.Contains(t, prov.line(), "(project registry)")
	})
}

func TestGenerateBase_UnknownStack(t *testing.T) {
	gen := tcGenerator(t, `
build:
  stacks: [definitely-not-a-stack]
`, "version: { resolver: none }\n")

	_, err := gen.GenerateBase()
	require.ErrorIs(t, err, ErrUnknownStack)
}

func TestGenerateBase_DuplicateDeclaration(t *testing.T) {
	gen := tcGenerator(t, `
build:
  stacks: [node, node]
`, "version: { resolver: none }\n")

	_, err := gen.GenerateBase()
	require.ErrorContains(t, err, "duplicate stack declaration")
}

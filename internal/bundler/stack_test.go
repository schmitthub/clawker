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

func TestResolveStack_ShippedEmbeddedBootstrap(t *testing.T) {
	// No registry at all → shipped definitions load from the embedded copy.
	cfg := configmocks.NewFromString("", "")

	def, err := resolveStack(cfg, nil, "node")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "nodejs.org/dist")
	assert.Contains(t, def.UserFragment, "nvm-sh/nvm",
		"the node stack provisions nvm in user scope")
}

func TestResolveStack_RegistryPathWins(t *testing.T) {
	dir := writeStackDef(t, stack.UserFragmentFile, "RUN echo custom-def\n")
	cfg := configmocks.NewFromString("", `
stacks:
  mytool:
    path: `+dir+`
`)

	def, err := resolveStack(cfg, nil, "mytool")
	require.NoError(t, err)
	assert.Empty(t, def.RootFragment)
	assert.Contains(t, def.UserFragment, "custom-def")
}

func TestResolveStack_RegistryPathMissing(t *testing.T) {
	cfg := configmocks.NewFromString("", `
stacks:
  mytool:
    path: /nonexistent/definitely/missing
`)

	_, err := resolveStack(cfg, nil, "mytool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stacks.mytool.path")
}

func TestResolveStack_BundleEmbedded(t *testing.T) {
	cfg := configmocks.NewFromString("", "")
	b := bundleWithStack(t, "codex-special")

	def, err := resolveStack(cfg, b, "codex-special")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "bundle-stack-codex-special")
}

func TestResolveStack_BundleShippedCollision(t *testing.T) {
	// A bundle embedding a definition named like a shipped one is a
	// flat-namespace collision — loud error, not silent precedence.
	cfg := configmocks.NewFromString("", "")
	b := bundleWithStack(t, "node")

	_, err := resolveStack(cfg, b, "node")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one namespace")
}

func TestResolveStack_BundleRegistryCollision(t *testing.T) {
	dir := writeStackDef(t, stack.RootFragmentFile, "RUN echo registered\n")
	cfg := configmocks.NewFromString("", `
stacks:
  mytool:
    path: `+dir+`
`)
	b := bundleWithStack(t, "mytool")

	_, err := resolveStack(cfg, b, "mytool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), dir)
}

func TestResolveStack_Unknown(t *testing.T) {
	cfg := configmocks.NewFromString("", "")

	_, err := resolveStack(cfg, nil, "no-such-stack")
	require.ErrorIs(t, err, ErrUnknownStack)
}

// tcSettingsYAML returns settings with full telemetry defaults plus a
// registered harness bundle "other" at dir.
func tcSettingsYAML(dir string) string {
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
harnesses:
  other:
    default: true
    path: ` + dir + "\n"
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

// tcGenerator builds a ProjectGenerator selecting the "other" bundle.
func tcGenerator(t *testing.T, projectYAML, bundleManifest string) *ProjectGenerator {
	t.Helper()
	cfg := configmocks.NewFromString(projectYAML, tcSettingsYAML(tcBundleDir(t, bundleManifest)))
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

	// Project-declared stacks live in the base ONLY — the harness image
	// builds FROM it and must not re-render them.
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

func TestGenerate_BothDeclared_BaseWins(t *testing.T) {
	gen := tcGenerator(t, `
build:
  stacks: [node]
`, `
version: { resolver: none }
stacks: [node]
`)

	base, err := gen.GenerateBase()
	require.NoError(t, err)
	assert.Contains(t, string(base), nodeMarker)

	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	assert.NotContains(t, string(harnessImg), nodeMarker,
		"a project-declared stack renders once, in the base — never again in the harness image")
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

func TestGenerateHarness_ProjectDeclCollidesWithBundleEmbedded(t *testing.T) {
	bundleDir := tcBundleDir(t, "version: { resolver: none }\n")
	tcDir := filepath.Join(bundleDir, stack.StacksSubdir, "node")
	require.NoError(t, os.MkdirAll(tcDir, 0o755))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(tcDir, stack.ManifestFile), []byte("description: shadow\n"), 0o644),
	)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(tcDir, stack.RootFragmentFile), []byte("RUN echo shadow\n"), 0o644),
	)

	cfg := configmocks.NewFromString(`
build:
  stacks: [node]
`, tcSettingsYAML(bundleDir))
	gen := NewProjectGenerator(cfg, t.TempDir())
	gen.Harness = "other"
	gen.HarnessVersion = testHarnessVersion
	gen.BaseImageRef = testBaseImageRef

	_, err := gen.GenerateHarness()
	require.ErrorContains(t, err, "one namespace",
		"a bundle must never silently shadow the definition the base used")
}

func TestEnsureStacks_SeedsRegistryAndDefinitions(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)

	warnings, err := EnsureStacks(cfg)
	require.NoError(t, err)
	assert.Empty(t, warnings, "fresh materialize must not report staleness")

	// Definition files materialized to the seeded default location, and the
	// registry entry records that path explicitly.
	for _, name := range ShippedStackNames() {
		dir := ShippedStackDefaultDir(name)
		assert.FileExists(t, filepath.Join(dir, stack.ManifestFile))
		entry, ok := cfg.Settings().Stacks[name]
		require.True(t, ok, "registry entry seeded for %s", name)
		assert.Equal(t, dir, entry.Path)
		_, loadErr := resolveStack(cfg, nil, name)
		require.NoError(t, loadErr, "materialized %s must load through the registry", name)
	}

	// Resolution now reads through the registry (materialized copy wins).
	def, err := resolveStack(cfg, nil, "node")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "nodejs.org/dist")

	// User edit to the materialized copy is never clobbered — and an edit is
	// NOT staleness (the stamp tracks the shipped tree, not the user copy).
	fragPath := filepath.Join(ShippedStackDefaultDir("rust"), stack.UserFragmentFile)
	require.NoError(t, os.WriteFile(fragPath, []byte("RUN echo user-edited\n"), 0o644))
	warnings, err = EnsureStacks(cfg)
	require.NoError(t, err)
	assert.Empty(t, warnings, "a user edit must not trip the shipped-stamp check")
	edited, err := os.ReadFile(fragPath)
	require.NoError(t, err)
	assert.Equal(t, "RUN echo user-edited\n", string(edited))
}

// TestEnsureStacks_ShippedStampStaleness mirrors the harness contract: a
// mismatched (or missing) stamp on a materialized shipped definition warns
// and never overwrites the user-owned copy.
func TestEnsureStacks_ShippedStampStaleness(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)

	warnings, err := EnsureStacks(cfg)
	require.NoError(t, err)
	require.Empty(t, warnings)

	dir := ShippedStackDefaultDir("go")
	stampPath := filepath.Join(dir, harness.ShippedStampFile)
	require.FileExists(t, stampPath, "fresh materialize must stamp the copy")

	require.NoError(t, os.WriteFile(stampPath, []byte("stale-hash\n"), 0o644))
	warnings, err = EnsureStacks(cfg)
	require.NoError(t, err)
	require.Len(t, warnings, 1, "exactly the stale definition warns")
	assert.Contains(t, warnings[0], `"go"`)
	assert.Contains(t, warnings[0], dir)

	// The stale copy still loads — the stamp is invisible to definition
	// loading — and is never auto-refreshed.
	_, err = resolveStack(cfg, nil, "go")
	require.NoError(t, err)
	stamp, err := os.ReadFile(stampPath)
	require.NoError(t, err)
	assert.Equal(t, "stale-hash\n", string(stamp))
}

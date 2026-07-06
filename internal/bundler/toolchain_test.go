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
	"github.com/schmitthub/clawker/internal/toolchain"
)

func TestShippedToolchains_LoadAndFragments(t *testing.T) {
	assert.Equal(t, []string{"go", "node", "python", "rust"}, ShippedToolchainNames())

	// Which scopes each language toolchain provisions, with the guard
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
	for _, name := range ShippedToolchainNames() {
		def, err := loadEmbeddedToolchain(name)
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

func TestShippedToolchains_RenderBothBuildKitModes(t *testing.T) {
	for _, name := range ShippedToolchainNames() {
		def, err := loadEmbeddedToolchain(name)
		require.NoError(t, err)
		root, user := splitFragments([]*toolchain.Definition{def})
		for _, buildKit := range []bool{false, true} {
			// Fragment renders touch only BuildKitEnabled.
			var tctx DockerfileContext
			tctx.BuildKitEnabled = buildKit
			for _, fragments := range [][]namedFragment{root, user} {
				steps, renderErr := renderToolchainSteps(fragments, &tctx)
				require.NoError(t, renderErr, "%s buildkit=%v", name, buildKit)
				for _, step := range steps {
					assert.Contains(t, step, "RUN", "%s must emit at least one RUN", name)
				}
			}
		}
	}
}

// writeToolchainDef writes a minimal definition dir (one fragment at the
// given scope file) and returns its path.
func writeToolchainDef(t *testing.T, fragmentFile, fragment string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, toolchain.ManifestFile), []byte("description: test\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, fragmentFile), []byte(fragment), 0o644))
	return dir
}

// bundleWithToolchain builds a loaded harness bundle embedding one
// toolchain definition.
func bundleWithToolchain(t *testing.T, tcName string) *harness.Bundle {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, harness.ManifestFile), []byte("{}\n"), 0o644))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, harness.TemplateFile), []byte(`{{define "block_4"}}RUN echo hi{{end}}`), 0o644),
	)
	tcDir := filepath.Join(dir, toolchain.ToolchainsSubdir, tcName)
	require.NoError(t, os.MkdirAll(tcDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, toolchain.ManifestFile), []byte("description: test\n"), 0o644))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(tcDir, toolchain.RootFragmentFile),
			[]byte("RUN echo bundle-toolchain-"+tcName+"\n"),
			0o644,
		),
	)
	b, err := harness.Load("bundletest", os.DirFS(dir))
	require.NoError(t, err)
	return b
}

func TestResolveToolchain_ShippedEmbeddedBootstrap(t *testing.T) {
	// No registry at all → shipped definitions load from the embedded copy.
	cfg := configmocks.NewFromString("", "")

	def, err := resolveToolchain(cfg, nil, "node")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "nodejs.org/dist")
	assert.Contains(t, def.UserFragment, "nvm-sh/nvm",
		"the node toolchain provisions nvm in user scope")
}

func TestResolveToolchain_RegistryPathWins(t *testing.T) {
	dir := writeToolchainDef(t, toolchain.UserFragmentFile, "RUN echo custom-def\n")
	cfg := configmocks.NewFromString("", `
toolchains:
  mytool:
    path: `+dir+`
`)

	def, err := resolveToolchain(cfg, nil, "mytool")
	require.NoError(t, err)
	assert.Empty(t, def.RootFragment)
	assert.Contains(t, def.UserFragment, "custom-def")
}

func TestResolveToolchain_RegistryPathMissing(t *testing.T) {
	cfg := configmocks.NewFromString("", `
toolchains:
  mytool:
    path: /nonexistent/definitely/missing
`)

	_, err := resolveToolchain(cfg, nil, "mytool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "toolchains.mytool.path")
}

func TestResolveToolchain_BundleEmbedded(t *testing.T) {
	cfg := configmocks.NewFromString("", "")
	b := bundleWithToolchain(t, "codex-special")

	def, err := resolveToolchain(cfg, b, "codex-special")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "bundle-toolchain-codex-special")
}

func TestResolveToolchain_BundleShippedCollision(t *testing.T) {
	// A bundle embedding a definition named like a shipped one is a
	// flat-namespace collision — loud error, not silent precedence.
	cfg := configmocks.NewFromString("", "")
	b := bundleWithToolchain(t, "node")

	_, err := resolveToolchain(cfg, b, "node")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "one namespace")
}

func TestResolveToolchain_BundleRegistryCollision(t *testing.T) {
	dir := writeToolchainDef(t, toolchain.RootFragmentFile, "RUN echo registered\n")
	cfg := configmocks.NewFromString("", `
toolchains:
  mytool:
    path: `+dir+`
`)
	b := bundleWithToolchain(t, "mytool")

	_, err := resolveToolchain(cfg, b, "mytool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), dir)
}

func TestResolveToolchain_Unknown(t *testing.T) {
	cfg := configmocks.NewFromString("", "")

	_, err := resolveToolchain(cfg, nil, "no-such-toolchain")
	require.ErrorIs(t, err, ErrUnknownToolchain)
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

func TestGenerateBase_ProjectDeclaredToolchains(t *testing.T) {
	gen := tcGenerator(t, `
build:
  toolchains: [node]
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

	// Project-declared toolchains live in the base ONLY — the harness image
	// builds FROM it and must not re-render them.
	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	assert.NotContains(t, string(harnessImg), nodeMarker)
	assert.NotContains(t, string(harnessImg), nvmMarker)
}

func TestGenerateHarness_HarnessDeclaredToolchains(t *testing.T) {
	gen := tcGenerator(t, "version: \"1\"\n", `
version: { resolver: none }
toolchains: [node]
`)

	// Nothing project-declared → base carries no toolchain bytes.
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
	assert.Less(t, userRootIdx, nodeIdx, "root toolchain anchors after USER root")
	assert.Less(t, nodeIdx, b1Idx, "root toolchain must precede block_1")

	// User-scope fragment renders after the USER switch + ARG ZSH_ENV
	// (fragments may reference ${ZSH_ENV}), before block_3.
	nvmIdx := strings.Index(content, nvmMarker)
	b3Idx := strings.Index(content, "B3MARK")
	zshEnvIdx := strings.Index(content, "ARG ZSH_ENV")
	require.GreaterOrEqual(t, nvmIdx, 0)
	require.GreaterOrEqual(t, b3Idx, 0)
	require.GreaterOrEqual(t, zshEnvIdx, 0)
	assert.Less(t, zshEnvIdx, nvmIdx, "ARG ZSH_ENV must be in scope for user fragments")
	assert.Less(t, nvmIdx, b3Idx, "user toolchain must precede block_3")
}

func TestGenerate_BothDeclared_BaseWins(t *testing.T) {
	gen := tcGenerator(t, `
build:
  toolchains: [node]
`, `
version: { resolver: none }
toolchains: [node]
`)

	base, err := gen.GenerateBase()
	require.NoError(t, err)
	assert.Contains(t, string(base), nodeMarker)

	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	assert.NotContains(t, string(harnessImg), nodeMarker,
		"a project-declared toolchain renders once, in the base — never again in the harness image")
}

func TestGenerateBase_UnknownToolchain(t *testing.T) {
	gen := tcGenerator(t, `
build:
  toolchains: [definitely-not-a-toolchain]
`, "version: { resolver: none }\n")

	_, err := gen.GenerateBase()
	require.ErrorIs(t, err, ErrUnknownToolchain)
}

func TestGenerateBase_DuplicateDeclaration(t *testing.T) {
	gen := tcGenerator(t, `
build:
  toolchains: [node, node]
`, "version: { resolver: none }\n")

	_, err := gen.GenerateBase()
	require.ErrorContains(t, err, "duplicate toolchain declaration")
}

func TestGenerateHarness_ProjectDeclCollidesWithBundleEmbedded(t *testing.T) {
	bundleDir := tcBundleDir(t, "version: { resolver: none }\n")
	tcDir := filepath.Join(bundleDir, toolchain.ToolchainsSubdir, "node")
	require.NoError(t, os.MkdirAll(tcDir, 0o755))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(tcDir, toolchain.ManifestFile), []byte("description: shadow\n"), 0o644),
	)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(tcDir, toolchain.RootFragmentFile), []byte("RUN echo shadow\n"), 0o644),
	)

	cfg := configmocks.NewFromString(`
build:
  toolchains: [node]
`, tcSettingsYAML(bundleDir))
	gen := NewProjectGenerator(cfg, t.TempDir())
	gen.Harness = "other"
	gen.HarnessVersion = testHarnessVersion
	gen.BaseImageRef = testBaseImageRef

	_, err := gen.GenerateHarness()
	require.ErrorContains(t, err, "one namespace",
		"a bundle must never silently shadow the definition the base used")
}

func TestEnsureToolchains_SeedsRegistryAndDefinitions(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)

	require.NoError(t, EnsureToolchains(cfg))

	// Definition files materialized to the seeded default location, and the
	// registry entry records that path explicitly.
	for _, name := range ShippedToolchainNames() {
		dir := ShippedToolchainDefaultDir(name)
		assert.FileExists(t, filepath.Join(dir, toolchain.ManifestFile))
		entry, ok := cfg.Settings().Toolchains[name]
		require.True(t, ok, "registry entry seeded for %s", name)
		assert.Equal(t, dir, entry.Path)
		_, loadErr := resolveToolchain(cfg, nil, name)
		require.NoError(t, loadErr, "materialized %s must load through the registry", name)
	}

	// Resolution now reads through the registry (materialized copy wins).
	def, err := resolveToolchain(cfg, nil, "node")
	require.NoError(t, err)
	assert.Contains(t, def.RootFragment, "nodejs.org/dist")

	// User edit to the materialized copy is never clobbered.
	fragPath := filepath.Join(ShippedToolchainDefaultDir("rust"), toolchain.UserFragmentFile)
	require.NoError(t, os.WriteFile(fragPath, []byte("RUN echo user-edited\n"), 0o644))
	require.NoError(t, EnsureToolchains(cfg))
	edited, err := os.ReadFile(fragPath)
	require.NoError(t, err)
	assert.Equal(t, "RUN echo user-edited\n", string(edited))
}

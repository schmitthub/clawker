package bundler //nolint:testpackage // shares in-package test helpers (testConfig, newTestProjectGenerator) with dockerfile_test.go

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Markers keyed off stable content in the shipped stack fragments and the
// overlay-declared apt package, used to locate overlay output in a render.
const (
	goStackMarker  = "go.dev/dl" // go stack root fragment (overlay-declared here)
	overlayAptPkg  = "libnss3"   // overlay package, absent from every other list
	aptCacheTarget = "target=/var/cache/apt"
	// nodeArgLine appears exactly once per render of the node root fragment,
	// making it a render-count discriminator (unlike nodeMarker, which the
	// fragment repeats).
	nodeArgLine = "ARG NODE_VERSION=24"
)

// Conformance: E1 — declaration order preserved (installer→overlay). E18 — a name across installer+overlay renders once.
// TestGenerateHarness_OverlayStackRepeatedAcrossSources: a stack name declared
// by BOTH the bundle's installer list and the project overlay renders exactly
// once, at its first (installer) position; a distinct overlay-only stack still
// renders after it (installer → overlay order). The harness-overlay golden
// cannot pin this — it declares no name in both sources.
func TestGenerateHarness_OverlayStackRepeatedAcrossSources(t *testing.T) {
	// claude's bundle declares node; the overlay repeats node and adds go.
	cfg := testConfig(t, `
version: "1"
build:
  harnesses:
    claude:
      stacks: [node, go]
`)
	gen := newTestProjectGenerator(cfg, t.TempDir())

	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	content := string(harnessImg)

	assert.Equal(t, 1, strings.Count(content, nodeArgLine),
		"node — declared by installer AND overlay — must render exactly once")
	nodeIdx := strings.Index(content, nodeArgLine)
	goIdx := strings.Index(content, goStackMarker)
	require.GreaterOrEqual(t, goIdx, 0, "overlay-only stack (go) must render")
	assert.Less(t, nodeIdx, goIdx, "the repeated name keeps its installer position, before overlay stacks")
}

// Conformance: E19 — overlay is scoped to one harness image. E22 — overlay packages render as-declared, no dedup.
// TestGenerateHarness_OverlayPackagesLegacyBuilder: with BuildKit off, the
// overlay apt RUN renders without cache-mount directives (parity with the base
// template's legacy branch). The BuildKit-on branch is byte-locked by the
// harness-overlay golden; no golden covers the legacy overlay render.
func TestGenerateHarness_OverlayPackagesLegacyBuilder(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
build:
  harnesses:
    claude:
      packages: [`+overlayAptPkg+`]
`)
	gen := newTestProjectGenerator(cfg, t.TempDir())
	gen.BuildKitEnabled = false

	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	content := string(harnessImg)
	require.Contains(t, content, overlayAptPkg, "overlay package must render")
	// The overlay apt RUN is the only apt-cache consumer in the harness
	// image, so with BuildKit off no /var/cache/apt mount may appear.
	assert.NotContains(t, content, aptCacheTarget, "legacy builder must not emit apt cache mounts")
}

// Conformance: E19 — the per-harness build overlay is scoped to exactly one harness image.
// TestGenerateHarness_OverlayInjectAfterGlobal: overlay inject points render in
// the harness image after the global project inject at the same anchors
// (declaration order: global first, overlay appended).
func TestGenerateHarness_OverlayInjectAfterGlobal(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
build:
  inject:
    after_harness_install:
      - "RUN echo GLOBAL_INSTALL"
    before_entrypoint:
      - "RUN echo GLOBAL_ENTRY"
  harnesses:
    claude:
      inject:
        after_harness_install:
          - "RUN echo OVERLAY_INSTALL"
        before_entrypoint:
          - "RUN echo OVERLAY_ENTRY"
`)
	gen := newTestProjectGenerator(cfg, t.TempDir())

	harnessImg, err := gen.GenerateHarness()
	require.NoError(t, err)
	content := string(harnessImg)

	globalInstall := strings.Index(content, "GLOBAL_INSTALL")
	overlayInstall := strings.Index(content, "OVERLAY_INSTALL")
	globalEntry := strings.Index(content, "GLOBAL_ENTRY")
	overlayEntry := strings.Index(content, "OVERLAY_ENTRY")
	require.GreaterOrEqual(t, globalInstall, 0)
	require.GreaterOrEqual(t, overlayInstall, 0)
	require.GreaterOrEqual(t, globalEntry, 0)
	require.GreaterOrEqual(t, overlayEntry, 0)
	assert.Less(t, globalInstall, overlayInstall, "overlay after_harness_install renders after the global one")
	assert.Less(t, globalEntry, overlayEntry, "overlay before_entrypoint renders after the global one")
}

// Conformance: E19 — the per-harness build overlay is scoped to exactly one harness image.
// TestGenerateHarness_OverlayScopedToNamedHarness: an overlay keyed to one
// harness renders only in that harness's image — a sibling harness built from
// the same project config stays clean.
func TestGenerateHarness_OverlayScopedToNamedHarness(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
build:
  harnesses:
    claude:
      stacks: [go]
      packages: [`+overlayAptPkg+`]
      inject:
        after_harness_install:
          - "RUN echo CLAUDE_ONLY_OVERLAY"
`)

	claudeGen := newTestProjectGenerator(cfg, t.TempDir())
	claudeImg, err := claudeGen.GenerateHarness()
	require.NoError(t, err)
	claude := string(claudeImg)
	assert.Contains(t, claude, goStackMarker, "the named harness image carries its overlay stack")
	assert.Contains(t, claude, overlayAptPkg, "the named harness image carries its overlay package")
	assert.Contains(t, claude, "CLAUDE_ONLY_OVERLAY", "the named harness image carries its overlay inject")

	codexGen := newTestProjectGenerator(cfg, t.TempDir())
	codexGen.Harness = "codex"
	codexImg, err := codexGen.GenerateHarness()
	require.NoError(t, err)
	codex := string(codexImg)
	assert.NotContains(t, codex, goStackMarker, "sibling harness must not carry another harness's overlay stack")
	assert.NotContains(t, codex, overlayAptPkg, "sibling harness must not carry another harness's overlay package")
	assert.NotContains(t, codex, "CLAUDE_ONLY_OVERLAY",
		"sibling harness must not carry another harness's overlay inject")
}

// Conformance: E19 — a dead overlay key (naming no known harness) is a loud GenerateHarness error.
// TestGenerateHarness_OverlayUnknownHarnessKey: an overlay keyed to a harness
// that resolves nowhere (typo or unregistered bundle) is dead config that
// would silently drop its content from every image — GenerateHarness must
// error, naming the key and the register remedy.
func TestGenerateHarness_OverlayUnknownHarnessKey(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
build:
  harnesses:
    claud:
      packages: [`+overlayAptPkg+`]
`)
	gen := newTestProjectGenerator(cfg, t.TempDir())

	_, err := gen.GenerateHarness()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build.harnesses.claud")
	assert.Contains(t, err.Error(), "clawker harness register")
}

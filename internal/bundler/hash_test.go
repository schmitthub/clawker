package bundler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentHash_Stability(t *testing.T) {
	dockerfile := []byte("FROM alpine:latest\nRUN echo hello\n")

	h1, err := ContentHash(dockerfile, nil, "", nil)
	require.NoError(t, err)

	h2, err := ContentHash(dockerfile, nil, "", nil)
	require.NoError(t, err)

	assert.Equal(t, h1, h2, "same input should produce same hash")
	assert.Len(t, h1, 12, "hash should be 12 hex characters")
}

func TestContentHash_Sensitivity(t *testing.T) {
	df1 := []byte("FROM alpine:latest\nRUN echo hello\n")
	df2 := []byte("FROM alpine:latest\nRUN echo world\n")

	h1, err := ContentHash(df1, nil, "", nil)
	require.NoError(t, err)

	h2, err := ContentHash(df2, nil, "", nil)
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2, "different Dockerfiles should produce different hashes")
}

func TestContentHash_IncludeFiles(t *testing.T) {
	dir := t.TempDir()

	// Create two include files
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bbb"), 0644))

	dockerfile := []byte("FROM alpine:latest\n")

	// Hash with includes
	h1, err := ContentHash(dockerfile, []string{"a.txt", "b.txt"}, dir, nil)
	require.NoError(t, err)

	// Hash without includes should differ
	h2, err := ContentHash(dockerfile, nil, dir, nil)
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2, "adding includes should change the hash")

	// Hash with same includes in different order should be the same (sorted)
	h3, err := ContentHash(dockerfile, []string{"b.txt", "a.txt"}, dir, nil)
	require.NoError(t, err)

	assert.Equal(t, h1, h3, "include order should not affect hash")
}

func TestContentHash_IncludeContentChange(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("v1"), 0644))
	dockerfile := []byte("FROM alpine:latest\n")

	h1, err := ContentHash(dockerfile, []string{"file.txt"}, dir, nil)
	require.NoError(t, err)

	// Change file content
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("v2"), 0644))

	h2, err := ContentHash(dockerfile, []string{"file.txt"}, dir, nil)
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2, "changing include file content should change hash")
}

func TestContentHash_MissingInclude(t *testing.T) {
	dockerfile := []byte("FROM alpine:latest\n")

	// Missing include files must return an error
	_, err := ContentHash(dockerfile, []string{"nonexistent.txt"}, "/tmp", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent.txt")
}

func TestContentHash_PermissionError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	require.NoError(t, os.WriteFile(path, []byte("secret"), 0000))

	dockerfile := []byte("FROM alpine:latest\n")
	_, err := ContentHash(dockerfile, []string{"secret.txt"}, dir, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "secret.txt")
}

// TestContentHash_MetadataStability verifies that config-only changes (env vars,
// labels, firewall domains, EXPOSE, VOLUME, etc.) do NOT change the content hash.
// These values are now injected at container creation time or via build API labels,
// keeping the Dockerfile (and hash) purely structural.
func TestContentHash_MetadataStability(t *testing.T) {
	baseYAML := `
version: "1"
name: "testproj"
build:
  image: "buildpack-deps:bookworm-scm"
  packages:
    - "ripgrep"
workspace:
  remote_path: "/workspace"
security:
  firewall:
    enable: true
`
	// Generate baseline Dockerfile and hash
	cfg1 := testConfig(t, baseYAML)
	gen1 := NewProjectGenerator(cfg1, "/tmp/test")
	df1, err := gen1.Generate()
	require.NoError(t, err)
	hash1, err := ContentHash(df1, nil, "", nil)
	require.NoError(t, err)

	// Change env vars — hash should NOT change
	cfg2 := testConfig(t, baseYAML+`
agent:
  env:
    FOO: "bar"
    BAZ: "qux"
`)
	gen2 := NewProjectGenerator(cfg2, "/tmp/test")
	df2, err := gen2.Generate()
	require.NoError(t, err)
	hash2, err := ContentHash(df2, nil, "", nil)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2, "changing agent env should not change hash")

	// Change editor — hash should NOT change
	cfg3 := testConfig(t, baseYAML+`
agent:
  editor: "vim"
  visual: "code"
`)
	gen3 := NewProjectGenerator(cfg3, "/tmp/test")
	df3, err := gen3.Generate()
	require.NoError(t, err)
	hash3, err := ContentHash(df3, nil, "", nil)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash3, "changing editor should not change hash")

	// Change firewall domains — hash should NOT change
	cfg4 := testConfig(t, `
version: "1"
name: "testproj"
build:
  image: "buildpack-deps:bookworm-scm"
  packages:
    - "ripgrep"
workspace:
  remote_path: "/workspace"
security:
  firewall:
    enable: true
    add_domains:
      - "custom.com"
`)
	gen4 := NewProjectGenerator(cfg4, "/tmp/test")
	df4, err := gen4.Generate()
	require.NoError(t, err)
	hash4, err := ContentHash(df4, nil, "", nil)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash4, "changing firewall domains should not change hash")

	// Change labels — hash should NOT change
	cfg5 := testConfig(t, `
version: "1"
name: "testproj"
build:
  image: "buildpack-deps:bookworm-scm"
  packages:
    - "ripgrep"
  instructions:
    labels:
      app: "myapp"
    env:
      NODE_ENV: "production"
workspace:
  remote_path: "/workspace"
security:
  firewall:
    enable: true
`)
	gen5 := NewProjectGenerator(cfg5, "/tmp/test")
	df5, err := gen5.Generate()
	require.NoError(t, err)
	hash5, err := ContentHash(df5, nil, "", nil)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash5, "changing labels and instruction env should not change hash")

	// Change project name — hash should NOT change (labels are via API)
	cfg6 := testConfig(t, `
version: "1"
name: "different-project"
build:
  image: "buildpack-deps:bookworm-scm"
  packages:
    - "ripgrep"
workspace:
  remote_path: "/workspace"
security:
  firewall:
    enable: true
`)
	gen6 := NewProjectGenerator(cfg6, "/tmp/test")
	df6, err := gen6.Generate()
	require.NoError(t, err)
	hash6, err := ContentHash(df6, nil, "", nil)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash6, "changing project name should not change hash")

	// Change packages — hash SHOULD change (structural)
	cfg7 := testConfig(t, `
version: "1"
name: "testproj"
build:
  image: "buildpack-deps:bookworm-scm"
  packages:
    - "ripgrep"
    - "jq-extra-tools"
workspace:
  remote_path: "/workspace"
security:
  firewall:
    enable: true
`)
	gen7 := NewProjectGenerator(cfg7, "/tmp/test")
	df7, err := gen7.Generate()
	require.NoError(t, err)
	hash7, err := ContentHash(df7, nil, "", nil)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hash7, "changing packages should change hash")
}

func hashTestConfig(t *testing.T) string {
	return `
version: "1"
name: "testproj"
build:
  image: "buildpack-deps:bookworm-scm"
  packages:
    - "ripgrep"
workspace:
  remote_path: "/workspace"
security:
  firewall:
    enable: true
`
}

// TestContentHash_BuildKitVsLegacy verifies that BuildKit and legacy Dockerfiles
// produce different content hashes because the Dockerfile structure differs
// (--mount=type=cache directives vs plain RUN commands).
func TestContentHash_BuildKitVsLegacy(t *testing.T) {
	cfg := testConfig(t, hashTestConfig(t))

	// BuildKit mode
	genBK := NewProjectGenerator(cfg, "/tmp/test")
	genBK.BuildKitEnabled = true
	dfBK, err := genBK.Generate()
	require.NoError(t, err)
	hashBK, err := ContentHash(dfBK, nil, "", nil)
	require.NoError(t, err)

	// Legacy mode
	genLegacy := NewProjectGenerator(cfg, "/tmp/test")
	genLegacy.BuildKitEnabled = false
	dfLegacy, err := genLegacy.Generate()
	require.NoError(t, err)
	hashLegacy, err := ContentHash(dfLegacy, nil, "", nil)
	require.NoError(t, err)

	assert.NotEqual(t, hashBK, hashLegacy, "BuildKit and legacy Dockerfiles should produce different hashes")

	// Verify BuildKit Dockerfile contains cache mounts
	assert.Contains(t, string(dfBK), "--mount=type=cache", "BuildKit Dockerfile should contain cache mount directives")

	// Verify legacy Dockerfile does NOT contain cache mounts
	assert.NotContains(t, string(dfLegacy), "--mount=type=cache", "Legacy Dockerfile should not contain cache mount directives")
}

// TestContentHash_StableBuildKit verifies that BuildKit Dockerfiles produce
// stable hashes across multiple generations with the same config.
func TestContentHash_StableBuildKit(t *testing.T) {
	cfg := testConfig(t, hashTestConfig(t))

	gen1 := NewProjectGenerator(cfg, "/tmp/test")
	gen1.BuildKitEnabled = true
	df1, err := gen1.Generate()
	require.NoError(t, err)
	hash1, err := ContentHash(df1, nil, "", nil)
	require.NoError(t, err)

	gen2 := NewProjectGenerator(cfg, "/tmp/test")
	gen2.BuildKitEnabled = true
	df2, err := gen2.Generate()
	require.NoError(t, err)
	hash2, err := ContentHash(df2, nil, "", nil)
	require.NoError(t, err)

	assert.Equal(t, hash1, hash2, "same config with BuildKit should produce stable hashes")
}

// TestContentHash_StableLegacy verifies that legacy Dockerfiles produce
// stable hashes across multiple generations with the same config.
func TestContentHash_StableLegacy(t *testing.T) {
	cfg := testConfig(t, hashTestConfig(t))

	gen1 := NewProjectGenerator(cfg, "/tmp/test")
	gen1.BuildKitEnabled = false
	df1, err := gen1.Generate()
	require.NoError(t, err)
	hash1, err := ContentHash(df1, nil, "", nil)
	require.NoError(t, err)

	gen2 := NewProjectGenerator(cfg, "/tmp/test")
	gen2.BuildKitEnabled = false
	df2, err := gen2.Generate()
	require.NoError(t, err)
	hash2, err := ContentHash(df2, nil, "", nil)
	require.NoError(t, err)

	assert.Equal(t, hash1, hash2, "same config with legacy builder should produce stable hashes")
}

// TestContentHash_BuildKitAlpineVsLegacy verifies BuildKit vs legacy divergence
// on Alpine base images (different cache mount paths than Debian).
func TestContentHash_BuildKitAlpineVsLegacy(t *testing.T) {
	cfg := testConfig(t, `
version: "1"
name: "testproj"
build:
  image: "alpine:3.22"
workspace:
  remote_path: "/workspace"
security:
  firewall:
    enable: true
`)
	genBK := NewProjectGenerator(cfg, "/tmp/test")
	genBK.BuildKitEnabled = true
	dfBK, err := genBK.Generate()
	require.NoError(t, err)

	genLegacy := NewProjectGenerator(cfg, "/tmp/test")
	genLegacy.BuildKitEnabled = false
	dfLegacy, err := genLegacy.Generate()
	require.NoError(t, err)

	assert.Contains(t, string(dfBK), "--mount=type=cache", "Alpine BuildKit Dockerfile should contain cache mount directives")
	assert.NotContains(t, string(dfLegacy), "--mount=type=cache", "Alpine legacy Dockerfile should not contain cache mount directives")

	hashBK, err := ContentHash(dfBK, nil, "", nil)
	require.NoError(t, err)
	hashLegacy, err := ContentHash(dfLegacy, nil, "", nil)
	require.NoError(t, err)
	assert.NotEqual(t, hashBK, hashLegacy, "Alpine BuildKit and legacy should produce different hashes")
}

// TestContentHash_EmbeddedScripts verifies that embedded scripts affect the hash.
func TestContentHash_EmbeddedScripts(t *testing.T) {
	dockerfile := []byte("FROM alpine:latest\n")

	// Hash without embedded scripts
	h1, err := ContentHash(dockerfile, nil, "", nil)
	require.NoError(t, err)

	// Hash with embedded scripts should differ
	scripts := []string{"#!/bin/bash\necho 'script1'", "#!/bin/bash\necho 'script2'"}
	h2, err := ContentHash(dockerfile, nil, "", scripts)
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2, "adding embedded scripts should change the hash")

	// Hash with same scripts should be stable
	h3, err := ContentHash(dockerfile, nil, "", scripts)
	require.NoError(t, err)

	assert.Equal(t, h2, h3, "same embedded scripts should produce same hash")

	// Hash with different script content should differ
	scripts2 := []string{"#!/bin/bash\necho 'script1'", "#!/bin/bash\necho 'modified'"}
	h4, err := ContentHash(dockerfile, nil, "", scripts2)
	require.NoError(t, err)

	assert.NotEqual(t, h2, h4, "different embedded script content should change hash")
}

// TestContentHash_EmbeddedScriptsHelper verifies that EmbeddedScripts() returns
// a non-empty slice and that using it produces stable hashes.
func TestContentHash_EmbeddedScriptsHelper(t *testing.T) {
	scripts := EmbeddedScripts()
	assert.NotEmpty(t, scripts, "EmbeddedScripts() should return non-empty slice")

	// Verify all scripts are non-empty
	for i, script := range scripts {
		assert.NotEmpty(t, script, "embedded script %d should not be empty", i)
	}

	// Using EmbeddedScripts() should produce stable hashes
	dockerfile := []byte("FROM alpine:latest\n")
	h1, err := ContentHash(dockerfile, nil, "", EmbeddedScripts())
	require.NoError(t, err)
	h2, err := ContentHash(dockerfile, nil, "", EmbeddedScripts())
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "EmbeddedScripts() should produce stable hashes")
}

// TestEmbeddedScripts_ContainsExpectedContent verifies that EmbeddedScripts()
// dynamically discovers scripts and includes expected content from both bundler
// assets and hostproxy internals.
func TestEmbeddedScripts_ContainsExpectedContent(t *testing.T) {
	scripts := EmbeddedScripts()
	combined := ""
	for _, s := range scripts {
		combined += s
	}

	// Bundler assets should be present
	assert.Contains(t, combined, "#!/bin/bash", "Should contain shell scripts from bundler assets")
	assert.Contains(t, combined, "ENTRYPOINT", "Should contain entrypoint markers")

	// Hostproxy scripts should be present (from internals.AllScripts())
	assert.Contains(t, combined, "host-open", "Should contain host-open script")
	assert.Contains(t, combined, "callback", "Should contain callback forwarder")
	assert.Contains(t, combined, "MsgReady", "Should contain socket server source")
}

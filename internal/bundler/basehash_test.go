package bundler //nolint:testpackage // shares in-package test helpers (testConfig, newTestProjectGenerator)

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// strptr returns a pointer to s, mirroring the non-nil *string a
// `--build-arg KEY=VALUE` yields (a bare `--build-arg KEY` yields nil).
func strptr(s string) *string { return &s }

func copyInstructionsYAML() string {
	return `
version: "1"
build:
  instructions:
    copy:
      - src: "scripts"
        dst: "/opt/scripts"
      - src: "config-*.yaml"
        dst: "/opt/config/"
`
}

func writeCopyFixtures(t *testing.T, workDir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "scripts", "run.sh"), []byte("echo run"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "config-a.yaml"), []byte("a: 1"), 0o644))
}

func TestBaseContentHash_Deterministic(t *testing.T) {
	workDir := t.TempDir()
	writeCopyFixtures(t, workDir)
	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)

	df := []byte("FROM x\n")
	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.Equal(t, h1, h2)
	assert.Len(t, h1, 64, "hex-encoded sha256")
}

func TestBaseContentHash_ChangesOnDockerfileChange(t *testing.T) {
	workDir := t.TempDir()
	gen := newTestProjectGenerator(testConfig(t, minimalProjectYAML()), workDir)

	h1, err := gen.BaseContentHash([]byte("FROM x\n"), nil)
	require.NoError(t, err)
	h2, err := gen.BaseContentHash([]byte("FROM y\n"), nil)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2)
}

func TestBaseContentHash_ChangesOnCopySourceChange(t *testing.T) {
	workDir := t.TempDir()
	writeCopyFixtures(t, workDir)
	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)

	df := []byte("FROM x\n")
	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(workDir, "scripts", "run.sh"), []byte("echo changed"), 0o755))
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "copy-src content change must flip the hash")

	// Glob-matched file too.
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "config-b.yaml"), []byte("b: 2"), 0o644))
	h3, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h2, h3, "new glob match must flip the hash")
}

func TestBaseContentHash_IgnoresUnreferencedFiles(t *testing.T) {
	workDir := t.TempDir()
	writeCopyFixtures(t, workDir)
	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)

	df := []byte("FROM x\n")
	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(workDir, "main.go"), []byte("package main"), 0o644))
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.Equal(t, h1, h2,
		"source edits outside copy srcs must not rebuild the base")
}

func TestBaseContentHash_MissingSrcStableMarker(t *testing.T) {
	workDir := t.TempDir() // fixtures NOT written — srcs missing
	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)

	df := []byte("FROM x\n")
	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "missing srcs hash a stable marker, no error")

	// The src appearing later must flip the hash.
	writeCopyFixtures(t, workDir)
	h3, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h3)
}

// TestBaseContentHash_NoBuildArgsIsDockerfileOnly pins the byte-format
// invariant: with no build-args (or only irrelevant ones) the hash is exactly
// sha256 of the rendered bytes — the arg-free path appends no arg bytes, so a
// base's identity depends only on its rendered Dockerfile and is independent of
// the arg-folding path. The literal sha256 catches any stray byte written.
func TestBaseContentHash_NoBuildArgsIsDockerfileOnly(t *testing.T) {
	// minimalProjectYAML declares no copy instructions, so the only hashed
	// input is the Dockerfile bytes themselves.
	gen := newTestProjectGenerator(testConfig(t, minimalProjectYAML()), t.TempDir())
	df := []byte("FROM x\nARG NODE_VERSION=24\n")

	sum := sha256.Sum256(df)
	want := hex.EncodeToString(sum[:])

	hNil, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.Equal(t, want, hNil, "nil build-args must equal the plain dockerfile-only hash")

	hEmpty, err := gen.BaseContentHash(df, map[string]*string{})
	require.NoError(t, err)
	assert.Equal(t, want, hEmpty, "an empty build-arg map must equal the arg-free hash")

	// A harness-only arg the base never declares must also write nothing.
	hIrrelevant, err := gen.BaseContentHash(df, map[string]*string{"CLAUDE_CODE_VERSION": strptr("2.1.4")})
	require.NoError(t, err)
	assert.Equal(t, want, hIrrelevant, "an undeclared build-arg must not touch the hash")
}

// TestBaseContentHash_RelevantBuildArgChangesHash: a --build-arg targeting an
// ARG the base declares folds into the hash, and different values differ —
// this is what forces the base rebuild BuildKit would otherwise do itself.
func TestBaseContentHash_RelevantBuildArgChangesHash(t *testing.T) {
	gen := newTestProjectGenerator(testConfig(t, minimalProjectYAML()), t.TempDir())
	df := []byte("FROM x\nARG NODE_VERSION=24\n")

	h0, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	h20, err := gen.BaseContentHash(df, map[string]*string{"NODE_VERSION": strptr("20")})
	require.NoError(t, err)
	h22, err := gen.BaseContentHash(df, map[string]*string{"NODE_VERSION": strptr("22")})
	require.NoError(t, err)

	assert.NotEqual(t, h0, h20, "a build-arg the base declares must change the hash")
	assert.NotEqual(t, h20, h22, "differing values of a base-relevant arg must differ")
}

// TestBaseContentHash_IrrelevantBuildArgKeepsHash: harness-only and unknown
// build-args must not perturb the base hash even when other, relevant args are
// also present.
func TestBaseContentHash_IrrelevantBuildArgKeepsHash(t *testing.T) {
	gen := newTestProjectGenerator(testConfig(t, minimalProjectYAML()), t.TempDir())
	df := []byte("FROM x\nARG NODE_VERSION=24\n")

	base, err := gen.BaseContentHash(df, map[string]*string{"NODE_VERSION": strptr("20")})
	require.NoError(t, err)
	// Same relevant arg + an extra undeclared one must not change the hash.
	withExtra, err := gen.BaseContentHash(df, map[string]*string{
		"NODE_VERSION":        strptr("20"),
		"CLAUDE_CODE_VERSION": strptr("2.1.4"),
		"TOTALLY_UNKNOWN":     strptr("x"),
	})
	require.NoError(t, err)
	assert.Equal(t, base, withExtra, "undeclared args must not affect the hash")
}

// TestBaseContentHash_NilBuildArgUsesEnv: `--build-arg NAME` with no value is
// a pass-through — Docker reads the value from the client environment, so the
// hash must reflect the current env value.
func TestBaseContentHash_NilBuildArgUsesEnv(t *testing.T) {
	gen := newTestProjectGenerator(testConfig(t, minimalProjectYAML()), t.TempDir())
	df := []byte("FROM x\nARG TZ=UTC\n")

	t.Setenv("TZ", "America/New_York")
	h1, err := gen.BaseContentHash(df, map[string]*string{"TZ": nil})
	require.NoError(t, err)

	t.Setenv("TZ", "Europe/London")
	h2, err := gen.BaseContentHash(df, map[string]*string{"TZ": nil})
	require.NoError(t, err)

	assert.NotEqual(t, h1, h2, "a pass-through build-arg must reflect the env value")
}

// TestBaseDeclaredArgNames pins the ARG parser's edge cases: multi-stage
// scoping (every stage's ARGs count), quoted defaults with spaces, indentation
// and lowercase keyword, no-default form, backslash line continuation, and
// case-sensitive names. Non-ARG lines (a longer keyword, a comment, a mid-line
// ARG token) are rejected.
func TestBaseDeclaredArgNames(t *testing.T) {
	df := []byte(strings.Join([]string{
		"FROM base AS builder",
		"ARG GO_VERSION=1.25",
		`ARG QUOTED="a b"`,    // quoted default containing a space
		"   arg   INDENTED=1", // lowercase keyword, leading + inner whitespace
		"ARG \\",              // backslash line continuation...
		"    WRAPPED=7",       // ...the real name is on the next physical line
		"FROM base",           // second stage
		"ARG NODE_VERSION",    // no default
		"ARGUMENT NOTANARG=1", // longer keyword, not ARG
		"# ARG COMMENTED=1",   // comment line
		"RUN echo ARG NOPE",   // ARG appears mid-instruction
	}, "\n"))

	got := baseDeclaredArgNames(df)

	for _, want := range []string{"GO_VERSION", "QUOTED", "INDENTED", "WRAPPED", "NODE_VERSION"} {
		_, ok := got[want]
		assert.Truef(t, ok, "expected ARG %q to be declared", want)
	}
	for _, absent := range []string{"NOTANARG", "COMMENTED", "NOPE", "node_version"} {
		_, ok := got[absent]
		assert.Falsef(t, ok, "did not expect %q among declared ARGs", absent)
	}
	assert.Len(t, got, 5, "exactly the five real ARG declarations")
}

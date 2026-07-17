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

// httpsProxyArg is the canonical Docker predefined build arg exercised by the
// predefined-arg tests.
const httpsProxyArg = "HTTPS_PROXY"

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

// Conformance: E11 — with no base-relevant build-args the hash is byte-identical to the arg-free hash.
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

// Conformance: E11 — a --build-arg targeting a base-declared ARG folds into the freshness hash.
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

// Conformance: E11 — harness-only args never force a base rebuild (excluded from the base hash).
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

// Conformance: E11 — a nil (pass-through) base-declared build-arg resolves its effective value via [os.Getenv].
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

// Conformance: E11 — the freshness hash folds only the base Dockerfile's actually-declared ARG names.
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

// TestBaseContentHash_PredefinedProxyArgChangesHash: Docker honors a fixed set
// of predefined build args (the proxy variables) without any ARG declaration in
// the Dockerfile, and they change what every network-bound RUN step does — so a
// --build-arg targeting one must fold into the freshness hash exactly like a
// declared ARG, or the base skip silently eats the proxy setting.
func TestBaseContentHash_PredefinedProxyArgChangesHash(t *testing.T) {
	gen := newTestProjectGenerator(testConfig(t, minimalProjectYAML()), t.TempDir())
	// No ARG lines at all — the proxy args are honored regardless.
	df := []byte("FROM x\nRUN apt-get update\n")

	h0, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)

	hProxy, err := gen.BaseContentHash(df, map[string]*string{httpsProxyArg: strptr("http://proxy:3128")})
	require.NoError(t, err)
	assert.NotEqual(t, h0, hProxy, "a predefined proxy build-arg must change the hash despite no ARG declaration")

	hProxy2, err := gen.BaseContentHash(df, map[string]*string{httpsProxyArg: strptr("http://proxy2:3128")})
	require.NoError(t, err)
	assert.NotEqual(t, hProxy, hProxy2, "differing proxy values must differ")

	hLower, err := gen.BaseContentHash(df, map[string]*string{"no_proxy": strptr("internal.example")})
	require.NoError(t, err)
	assert.NotEqual(t, h0, hLower, "lowercase predefined variants are honored by Docker and must count")

	// An arg that is neither declared nor predefined still writes nothing.
	hIrrelevant, err := gen.BaseContentHash(df, map[string]*string{"NOT_A_PROXY": strptr("x")})
	require.NoError(t, err)
	assert.Equal(t, h0, hIrrelevant, "undeclared non-predefined args must not touch the hash")
}

// TestBaseContentHash_ChangesOnCopySrcModeChange: with no chmod declared on the
// copy instruction, Docker COPY preserves the source file's permission bits, so
// a mode-only change (same bytes) produces a different image and must flip the
// hash — BuildKit's own COPY cache key includes mode.
func TestBaseContentHash_ChangesOnCopySrcModeChange(t *testing.T) {
	workDir := t.TempDir()
	writeCopyFixtures(t, workDir)
	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)
	df := []byte("FROM x\n")

	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)

	// Same bytes, different permission bits (fixture writes run.sh 0755).
	require.NoError(t, os.Chmod(filepath.Join(workDir, "scripts", "run.sh"), 0o644))
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "a copy-src mode change must flip the hash")
}

// TestBaseContentHash_SymlinkCopySrc: a copy src that IS a symlink bakes its
// dereferenced content into the image (BuildKit context transfer), so both
// editing the target's content and repointing the link must flip the hash.
func TestBaseContentHash_SymlinkCopySrc(t *testing.T) {
	workDir := t.TempDir()
	target := filepath.Join(workDir, "shared", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte(`{"a":1}`), 0o644))
	link := filepath.Join(workDir, "settings.json")
	require.NoError(t, os.Symlink(target, link))

	yaml := `
version: "1"
build:
  instructions:
    copy:
      - src: "settings.json"
        dst: "/opt/settings.json"
`
	gen := newTestProjectGenerator(testConfig(t, yaml), workDir)
	df := []byte("FROM x\n")

	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	h1b, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.Equal(t, h1, h1b, "symlink src hashing must stay deterministic")

	// Editing the symlink's target must flip the hash.
	require.NoError(t, os.WriteFile(target, []byte(`{"a":2}`), 0o644))
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "editing a symlinked copy src's target must flip the hash")

	// Repointing the link (even to a byte-identical target) must flip it.
	other := filepath.Join(workDir, "shared", "other.json")
	require.NoError(t, os.WriteFile(other, []byte(`{"a":2}`), 0o644))
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(other, link))
	h3, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h2, h3, "repointing a symlinked copy src must flip the hash")
}

// TestBaseContentHash_DanglingSymlinkCopySrc: a dangling symlink src hashes
// deterministically without error (the build itself surfaces the failure), and
// the target later appearing must flip the hash.
func TestBaseContentHash_DanglingSymlinkCopySrc(t *testing.T) {
	workDir := t.TempDir()
	target := filepath.Join(workDir, "nowhere.json")
	require.NoError(t, os.Symlink(target, filepath.Join(workDir, "settings.json")))

	yaml := `
version: "1"
build:
  instructions:
    copy:
      - src: "settings.json"
        dst: "/opt/settings.json"
`
	gen := newTestProjectGenerator(testConfig(t, yaml), workDir)
	df := []byte("FROM x\n")

	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "a dangling symlink src must hash deterministically, no error")

	require.NoError(t, os.WriteFile(target, []byte(`{"a":1}`), 0o644))
	h3, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h3, "the target appearing must flip the hash")
}

// TestBaseContentHash_NestedSymlinkRepoint: a symlink INSIDE a copied directory
// transfers as a symlink, so its target string is image content — repointing it
// must flip the hash even when old and new targets hold identical bytes.
func TestBaseContentHash_NestedSymlinkRepoint(t *testing.T) {
	workDir := t.TempDir()
	writeCopyFixtures(t, workDir)
	scripts := filepath.Join(workDir, "scripts")
	require.NoError(t, os.WriteFile(filepath.Join(scripts, "a.txt"), []byte("same"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(scripts, "b.txt"), []byte("same"), 0o644))
	link := filepath.Join(scripts, "current")
	require.NoError(t, os.Symlink("a.txt", link))

	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)
	df := []byte("FROM x\n")

	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)

	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink("b.txt", link))
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "repointing a nested symlink must flip the hash")
}

// TestBaseContentHash_ChangesOnDockerignoreChange: the BuildKit base build uses
// the project dir as its local context and loads .dockerignore from it, so an
// ignore edit changes what COPY can see (and bake) without touching a single
// copy-src byte on disk — it is a base input and must flip the hash.
func TestBaseContentHash_ChangesOnDockerignoreChange(t *testing.T) {
	workDir := t.TempDir()
	writeCopyFixtures(t, workDir)
	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)
	df := []byte("FROM x\n")

	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".dockerignore"), []byte("scripts/run.sh\n"), 0o644))
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "adding a .dockerignore must flip the hash")

	require.NoError(
		t,
		os.WriteFile(filepath.Join(workDir, ".dockerignore"), []byte("scripts/run.sh\nconfig-a.yaml\n"), 0o644),
	)
	h3, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h2, h3, "editing .dockerignore must flip the hash")

	h3b, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.Equal(t, h3, h3b, "unchanged .dockerignore must hash deterministically")
}

// TestBaseContentHash_DockerignoreIrrelevantWithoutCopySrcs: with no copy
// instructions nothing reaches the image from the build context, so
// .dockerignore is not a base input — the hash stays byte-identical to the
// plain dockerfile-only hash.
func TestBaseContentHash_DockerignoreIrrelevantWithoutCopySrcs(t *testing.T) {
	workDir := t.TempDir()
	gen := newTestProjectGenerator(testConfig(t, minimalProjectYAML()), workDir)
	df := []byte("FROM x\n")

	sum := sha256.Sum256(df)
	want := hex.EncodeToString(sum[:])

	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".dockerignore"), []byte("*\n"), 0o644))
	h, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.Equal(t, want, h, "no copy srcs → .dockerignore must not touch the hash")
}

// TestBaseContentHash_UnresolvableSymlinkCopySrcDoesNotAbort: a copy-src link
// that cannot be resolved (here a symlink loop) must never abort the freshness
// gate — the gate's contract is "spurious rebuild at worst, never a blocked
// build"; the Docker build itself surfaces the real, actionable error. The
// hash must stay deterministic, and the link becoming resolvable must flip it.
func TestBaseContentHash_UnresolvableSymlinkCopySrcDoesNotAbort(t *testing.T) {
	workDir := t.TempDir()
	link := filepath.Join(workDir, "settings.json")
	other := filepath.Join(workDir, "b")
	// settings.json -> b -> settings.json: EvalSymlinks fails with ELOOP.
	require.NoError(t, os.Symlink(other, link))
	require.NoError(t, os.Symlink(link, other))

	yaml := `
version: "1"
build:
  instructions:
    copy:
      - src: "settings.json"
        dst: "/opt/settings.json"
`
	gen := newTestProjectGenerator(testConfig(t, yaml), workDir)
	df := []byte("FROM x\n")

	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err, "an unresolvable copy-src link must not abort the freshness gate")
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "an unresolvable link must hash deterministically")

	// Breaking the loop by giving the link real content must flip the hash.
	require.NoError(t, os.Remove(other))
	require.NoError(t, os.WriteFile(other, []byte(`{"a":1}`), 0o644))
	h3, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h3, "the link becoming resolvable must flip the hash")
}

// TestBaseContentHash_DereferencedSymlinkPrunesGit: when a copy src is a
// symlink into a sibling checkout, the sibling's .git tree must stay outside
// the hash — git state is never a freshness input — or every commit/fetch in
// the sibling defeats the base cache (and the walk crawls its object store).
func TestBaseContentHash_DereferencedSymlinkPrunesGit(t *testing.T) {
	parent := t.TempDir()
	workDir := filepath.Join(parent, "project")
	sibling := filepath.Join(parent, "sibling")
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(sibling, ".git"), 0o755))
	gitHead := filepath.Join(sibling, ".git", "HEAD")
	require.NoError(t, os.WriteFile(gitHead, []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sibling, "file.txt"), []byte("v1"), 0o644))
	require.NoError(t, os.Symlink(sibling, filepath.Join(workDir, "shared")))

	yaml := `
version: "1"
build:
  instructions:
    copy:
      - src: "shared"
        dst: "/opt/shared"
`
	gen := newTestProjectGenerator(testConfig(t, yaml), workDir)
	df := []byte("FROM x\n")

	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)

	// Mutating ONLY the sibling's git state must not move the hash.
	require.NoError(t, os.WriteFile(gitHead, []byte("ref: refs/heads/other\n"), 0o644))
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.Equal(t, h1, h2, "sibling .git churn must not flip the hash")

	// Real content under the dereferenced root still counts.
	require.NoError(t, os.WriteFile(filepath.Join(sibling, "file.txt"), []byte("v2"), 0o644))
	h3, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h2, h3, "content edits under the dereferenced root must flip the hash")
}

// TestBaseContentHash_ChangesOnCopySrcDirModeChange: Docker COPY preserves a
// directory's own permission bits exactly as it preserves file modes, so a
// mode-only change to the DIRECTORY (no file bytes touched) is a different
// image and must flip the hash.
func TestBaseContentHash_ChangesOnCopySrcDirModeChange(t *testing.T) {
	workDir := t.TempDir()
	writeCopyFixtures(t, workDir)
	gen := newTestProjectGenerator(testConfig(t, copyInstructionsYAML()), workDir)
	df := []byte("FROM x\n")

	h1, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)

	// Fixture creates scripts/ 0755; flip only the directory's bits.
	require.NoError(t, os.Chmod(filepath.Join(workDir, "scripts"), 0o700))
	h2, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "a copy-src directory mode change must flip the hash")

	h2b, err := gen.BaseContentHash(df, nil)
	require.NoError(t, err)
	assert.Equal(t, h2, h2b, "directory records must hash deterministically")
}

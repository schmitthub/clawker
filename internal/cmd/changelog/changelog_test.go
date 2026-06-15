package changelog

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	changelogpkg "github.com/schmitthub/clawker/internal/changelog"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// fixtureChangelog is a self-contained CHANGELOG.md (Keep a Changelog + the
// clawker HTML-comment metadata convention) served to the loader over httptest.
// It mirrors the real shape but uses stable versions so the command tests don't
// depend on the curated content of the repo's CHANGELOG.md.
const fixtureChangelog = `# Changelog

## [Unreleased]

### Added

- Work in progress that must never become an entry.

## [0.12.0] - 2026-06-11
<!-- clawker: tag=feature docs=https://docs.clawker.dev/aliases -->

### Added

- **User-configurable command aliases.** Define your own shortcuts.

## [0.11.0] - 2026-06-10
<!-- clawker: tag=fix -->

### Fixed

- **Worktree containers protect the host repository.** Read-only masks.

## [0.5.0] - 2026-03-20

### Added

- **Global egress firewall stack.** Shared Envoy + CoreDNS + eBPF.
`

// newTestCmd builds a Factory wired to a test IOStreams and a Changelog loader
// backed by an httptest server serving fixtureChangelog. The loader has a nil
// state store (so every Load fetches) and a temp-dir cache path. Returns the
// Factory plus the stdout and stderr buffers backing it.
func newTestCmd(t *testing.T, version string) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fixtureChangelog))
	}))
	t.Cleanup(srv.Close)

	cachePath := filepath.Join(t.TempDir(), "changelog-cache.md")
	loader := changelogpkg.NewLoader(srv.Client(), srv.URL, cachePath, nil, changelogpkg.DefaultTTL)

	ios, _, stdout, stderr := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: ios,
		Version:   version,
		Changelog: func() (*changelogpkg.Loader, error) { return loader, nil },
	}
	return f, stdout, stderr
}

func TestNewCmdChangelog_NoArgs_CurrentVersionEntry(t *testing.T) {
	f, stdout, _ := newTestCmd(t, "0.12.0")

	cmd := NewCmdChangelog(f, nil)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "v0.12.0") {
		t.Errorf("expected current version header in output, got:\n%s", out)
	}
	if strings.Contains(out, "v0.11.0") {
		t.Errorf("no-arg output should only show the current version, got:\n%s", out)
	}
}

func TestNewCmdChangelog_All_FullHistory(t *testing.T) {
	f, stdout, _ := newTestCmd(t, "0.12.0")

	cmd := NewCmdChangelog(f, nil)
	cmd.SetArgs([]string{"--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"v0.12.0", "v0.11.0", "v0.5.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("--all output missing %s, got:\n%s", want, out)
		}
	}
}

func TestNewCmdChangelog_Since_RangeQuery(t *testing.T) {
	f, stdout, _ := newTestCmd(t, "0.12.0")

	cmd := NewCmdChangelog(f, nil)
	cmd.SetArgs([]string{"--since", "v0.11.0"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "v0.12.0") {
		t.Errorf("--since v0.11.0 should include v0.12.0, got:\n%s", out)
	}
	// 0.11.0 is the lower bound (exclusive) and must NOT appear; 0.5.0 is below
	// the bound and must NOT appear either.
	if strings.Contains(out, "v0.11.0") {
		t.Errorf("--since is lo-exclusive; v0.11.0 should not appear, got:\n%s", out)
	}
	if strings.Contains(out, "v0.5.0") {
		t.Errorf("--since v0.11.0 should not include v0.5.0, got:\n%s", out)
	}
}

func TestNewCmdChangelog_Version_SelectsSpecificEntry(t *testing.T) {
	// The running binary is 0.12.0, but --version selects a different release.
	f, stdout, _ := newTestCmd(t, "0.12.0")

	cmd := NewCmdChangelog(f, nil)
	cmd.SetArgs([]string{"--version", "v0.5.0"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "v0.5.0") {
		t.Errorf("--version v0.5.0 should show that entry, got:\n%s", out)
	}
	if strings.Contains(out, "v0.12.0") {
		t.Errorf("--version v0.5.0 should not show the running version, got:\n%s", out)
	}
}

func TestNewCmdChangelog_AllAndSince_MutuallyExclusive(t *testing.T) {
	f, _, _ := newTestCmd(t, "0.12.0")
	cmd := NewCmdChangelog(f, nil)
	cmd.SetArgs([]string{"--all", "--since", "v0.11.0"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for --all + --since")
	}
	// validateFlags returns cmdutil.FlagErrorf; assert the type, not the prose.
	var flagErr *cmdutil.FlagError
	if !errors.As(err, &flagErr) {
		t.Errorf("expected a *cmdutil.FlagError, got %T: %v", err, err)
	}
}

func TestNewCmdChangelog_UnknownVersion_InfoMessage(t *testing.T) {
	f, stdout, stderr := newTestCmd(t, "9.9.9")

	cmd := NewCmdChangelog(f, nil)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if stdout.String() != "" {
		t.Errorf("expected no entry body for an unknown version, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "No curated changelog entries") {
		t.Errorf("expected info message on stderr, got:\n%s", stderr.String())
	}
}

// TestNewCmdChangelog_LoadError_DegradesToStderrNote asserts that a load failure
// (unreachable URL, no cache) degrades to a brief stderr note and a zero exit —
// a network blip must not fail `clawker changelog`.
func TestNewCmdChangelog_LoadError_DegradesToStderrNote(t *testing.T) {
	ios, _, stdout, stderr := iostreams.Test()
	// Point the loader at an unreachable URL with an empty temp-dir cache so both
	// the fetch and the cache fallback fail.
	cachePath := filepath.Join(t.TempDir(), "changelog-cache.md")
	loader := changelogpkg.NewLoader(http.DefaultClient, "http://127.0.0.1:0/nope", cachePath, nil, changelogpkg.DefaultTTL)
	f := &cmdutil.Factory{
		IOStreams: ios,
		Version:   "0.12.0",
		Changelog: func() (*changelogpkg.Loader, error) { return loader, nil },
	}

	cmd := NewCmdChangelog(f, nil)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute should degrade, not error: %v", err)
	}
	if stdout.String() != "" {
		t.Errorf("expected no stdout on load failure, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "could not load changelog") {
		t.Errorf("expected degraded stderr note, got:\n%s", stderr.String())
	}
}

func TestNewCmdChangelog_RejectsPositionalArgs(t *testing.T) {
	f, _, _ := newTestCmd(t, "0.12.0")
	cmd := NewCmdChangelog(f, nil)
	cmd.SetArgs([]string{"extra"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for unexpected positional arg")
	}
}

func TestNewCmdChangelog_RunFOverride(t *testing.T) {
	f, _, _ := newTestCmd(t, "0.12.0")
	var got *ChangelogOptions
	cmd := NewCmdChangelog(f, func(_ context.Context, o *ChangelogOptions) error {
		got = o
		return nil
	})
	cmd.SetArgs([]string{"--since", "v0.5.0"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got == nil {
		t.Fatal("runF was not invoked")
	}
	if got.Since != "v0.5.0" {
		t.Errorf("Since = %q, want v0.5.0", got.Since)
	}
	if got.Version != "0.12.0" {
		t.Errorf("Version = %q, want 0.12.0 (from Factory)", got.Version)
	}
}

func TestTagBadge_AllTagsRendered(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	cs := ios.ColorScheme()

	cases := map[string]string{
		changelogpkg.TagFeature:  tagEmojiFeature,
		changelogpkg.TagFix:      tagEmojiFix,
		changelogpkg.TagBreaking: tagEmojiBreaking,
		changelogpkg.TagPerf:     tagEmojiPerf,
		changelogpkg.TagChanged:  tagEmojiChanged,
		"unknown":                tagEmojiDefault,
	}
	for tag, emoji := range cases {
		badge := tagBadge(cs, tag)
		if !strings.Contains(badge, emoji) {
			t.Errorf("tagBadge(%q) = %q, missing emoji %q", tag, badge, emoji)
		}
	}
}

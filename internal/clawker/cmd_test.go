package clawker

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/changelog"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/state"
	"github.com/schmitthub/clawker/internal/update"
	"github.com/schmitthub/clawker/pkg/whail"
)

// teaserEntries is the fixture changelog the show-once teaser tests pass into
// maybeShowChangelog (entries are now loaded in Main() and threaded in, not
// fetched here). Newest-first, mirroring changelog.Parse output. The versions
// cover the bounds the catch-up tests assert against.
func teaserEntries() []changelog.Entry {
	return []changelog.Entry{
		{Version: "0.12.0", Date: "2026-06-11", Tag: changelog.TagFeature, Title: "Aliases"},
		{Version: "0.11.0", Date: "2026-06-10", Tag: changelog.TagFix, Title: "Worktree masks"},
		{Version: "0.10.3", Date: "2026-06-01", Tag: changelog.TagFix, Title: "A patch"},
		{Version: "0.10.0", Date: "2026-05-20", Tag: changelog.TagFeature, Title: "A minor"},
		{Version: "0.8.0", Date: "2026-04-20", Tag: changelog.TagFeature, Title: "Older feature"},
		{Version: "0.6.0", Date: "2026-03-25", Tag: changelog.TagChanged, Title: "A change"},
		{Version: "0.5.0", Date: "2026-03-20", Tag: changelog.TagFeature, Title: "Firewall"},
	}
}

// newChangelogTestFactory builds a Factory + file-backed state facade for the
// show-once teaser tests. ttyStderr forces the stderr-TTY gate so the suppression
// branches can be exercised directly. The state file lives in a fresh temp dir.
func newChangelogTestFactory(t *testing.T, ttyStderr bool) (*cmdutil.Factory, *state.State, *bytes.Buffer) {
	t.Helper()
	// Neutralize ambient teaser-suppression env so these tests exercise the
	// real TTY/opt-out logic rather than the host environment. GitHub Actions
	// sets CI=true, which changelogSuppressed honors and which would otherwise
	// silence every teaser-expecting case. "CI" is the canonical cross-tool
	// CI-detection var (kept literal, matching changelogSuppressed). Tests that
	// want env-driven suppression re-set these after calling this helper.
	t.Setenv("CI", "")
	t.Setenv(consts.EnvNoUpdateNotifier, "")
	tio, _, _, errOut := iostreams.Test()
	tio.SetStderrTTY(ttyStderr)

	st, err := state.New(state.WithStateDirOverride(t.TempDir()))
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}

	nop := logger.Nop()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return nop, nil },
	}
	return f, st, errOut
}

// TestMaybeShowChangelog_BootstrapFromCurrentVersion: first changelog-aware run
// (empty cursor) with a prior current_version below the running version surfaces
// the gained entries, then advances the cursor so a second run is silent. The
// prior is the snapshot Main() takes before the update goroutine runs.
func TestMaybeShowChangelog_BootstrapFromCurrentVersion(t *testing.T) {
	f, st, errOut := newChangelogTestFactory(t, true)

	// Main() snapshotted prior current_version 0.10.0 before the update
	// goroutine overwrote it; it is threaded in as the priorCurrentVersion arg.
	maybeShowChangelog(f, st, teaserEntries(), "0.12.0", "0.10.0")

	out := errOut.String()
	if !strings.Contains(out, "What's new in clawker") {
		t.Errorf("expected teaser header, got:\n%s", out)
	}
	// 0.10.0 < v <= 0.12.0 → 0.10.3, 0.11.0, 0.12.0 must appear; 0.10.0 must not.
	for _, want := range []string{"v0.12.0", "v0.11.0", "v0.10.3"} {
		if !strings.Contains(out, want) {
			t.Errorf("teaser missing %s, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "v0.10.0 ") {
		t.Errorf("0.10.0 is the exclusive lower bound and must not appear, got:\n%s", out)
	}
	if got := st.LastSeenChangelog(); got != "0.12.0" {
		t.Errorf("cursor = %q, want 0.12.0 after showing", got)
	}

	// Second run: cursor already at current, nothing new → silent, no teaser.
	errOut.Reset()
	maybeShowChangelog(f, st, teaserEntries(), "0.12.0", "0.10.0")
	if errOut.String() != "" {
		t.Errorf("expected silent second run, got:\n%s", errOut.String())
	}
}

// TestMaybeShowChangelog_BootstrapUsesSnapshotNotLiveCurrentVersion guards the
// ordering race the snapshot exists to defeat: in production the update
// goroutine runs RecordUpdateCheck and overwrites current_version to the
// RUNNING binary before maybeShowChangelog reads it. If the bootstrap read
// current_version live, prior would equal cur and the catch-up teaser would
// never fire on the exact upgrade path it exists for. Here the store's live
// current_version is already the running version (0.12.0) — simulating that
// overwrite — yet the snapshotted prior (0.10.0) still drives a catch-up.
func TestMaybeShowChangelog_BootstrapUsesSnapshotNotLiveCurrentVersion(t *testing.T) {
	f, st, errOut := newChangelogTestFactory(t, true)

	// The goroutine already overwrote current_version to the running binary.
	if err := st.RecordUpdateCheck(time.Now(), "0.12.0", "0.12.0"); err != nil {
		t.Fatalf("RecordUpdateCheck: %v", err)
	}
	if live := st.CurrentVersion(); live != "0.12.0" {
		t.Fatalf("precondition: live current_version = %q, want 0.12.0", live)
	}

	// Pass the version Main() snapshotted BEFORE that overwrite (0.10.0). A
	// live read would see 0.12.0 == cur and take the welcome branch; the
	// snapshot must win and surface the gained entries instead.
	maybeShowChangelog(f, st, teaserEntries(), "0.12.0", "0.10.0")

	out := errOut.String()
	if !strings.Contains(out, "What's new in clawker") {
		t.Errorf("snapshot prior must drive the catch-up teaser, got:\n%s", out)
	}
	if strings.Contains(out, "curated changelog") {
		t.Errorf("must not fall through to the welcome branch, got:\n%s", out)
	}
	if !strings.Contains(out, "v0.11.0") {
		t.Errorf("teaser missing entries gained since the snapshot, got:\n%s", out)
	}
}

// TestMaybeShowChangelog_FirstRunNoCatchupSeedsSilently: empty cursor and no
// prior (or prior >= current) seeds the cursor at the current version and
// prints nothing — there is no catch-up history to surface.
func TestMaybeShowChangelog_FirstRunNoCatchupSeedsSilently(t *testing.T) {
	f, st, errOut := newChangelogTestFactory(t, true)

	// No prior recorded → empty snapshot → no catch-up, silent seed.
	maybeShowChangelog(f, st, teaserEntries(), "0.12.0", "")

	if out := errOut.String(); out != "" {
		t.Errorf("first-run no-catchup must print nothing, got:\n%s", out)
	}
	if got := st.LastSeenChangelog(); got != "0.12.0" {
		t.Errorf("cursor = %q, want 0.12.0 seeded", got)
	}
}

// TestMaybeShowChangelog_ShowsOnceThenSilent: with an explicit cursor, the teaser
// fires once and the cursor advances so the next run is silent.
func TestMaybeShowChangelog_ShowsOnceThenSilent(t *testing.T) {
	f, st, errOut := newChangelogTestFactory(t, true)
	if err := st.SetLastSeenChangelog("0.11.0"); err != nil {
		t.Fatalf("SetLastSeenChangelog: %v", err)
	}

	maybeShowChangelog(f, st, teaserEntries(), "0.12.0", "")
	if !strings.Contains(errOut.String(), "v0.12.0") {
		t.Errorf("expected v0.12.0 in first teaser, got:\n%s", errOut.String())
	}

	errOut.Reset()
	maybeShowChangelog(f, st, teaserEntries(), "0.12.0", "")
	if errOut.String() != "" {
		t.Errorf("expected silent second run after cursor advanced, got:\n%s", errOut.String())
	}
}

// TestMaybeShowChangelog_MultiVersionCatchup: a v0.5→v0.12 jump surfaces the whole
// gained series.
func TestMaybeShowChangelog_MultiVersionCatchup(t *testing.T) {
	f, st, errOut := newChangelogTestFactory(t, true)
	if err := st.SetLastSeenChangelog("0.5.0"); err != nil {
		t.Fatalf("SetLastSeenChangelog: %v", err)
	}

	maybeShowChangelog(f, st, teaserEntries(), "0.12.0", "")
	out := errOut.String()
	for _, want := range []string{"v0.12.0", "v0.10.0", "v0.8.0", "v0.6.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("multi-version catch-up missing %s, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "v0.5.0 ") {
		t.Errorf("0.5.0 is the exclusive lower bound and must not appear, got:\n%s", out)
	}
}

// TestMaybeShowChangelog_SuppressedLeavesCursor: when suppressed (non-TTY here),
// the teaser stays silent AND the cursor is left untouched so it retries later.
func TestMaybeShowChangelog_SuppressedLeavesCursor(t *testing.T) {
	f, st, errOut := newChangelogTestFactory(t, false) // non-TTY → suppressed
	if err := st.SetLastSeenChangelog("0.11.0"); err != nil {
		t.Fatalf("SetLastSeenChangelog: %v", err)
	}

	maybeShowChangelog(f, st, teaserEntries(), "0.12.0", "")
	if errOut.String() != "" {
		t.Errorf("expected silence when suppressed, got:\n%s", errOut.String())
	}
	if got := st.LastSeenChangelog(); got != "0.11.0" {
		t.Errorf("suppressed run must leave cursor at 0.11.0, got %q", got)
	}
}

// TestMaybeShowChangelog_SuppressedByEnv: the update-notifier opt-out env also
// suppresses the changelog teaser (and leaves the cursor).
func TestMaybeShowChangelog_SuppressedByEnv(t *testing.T) {
	f, st, errOut := newChangelogTestFactory(t, true) // TTY, but env opts out
	t.Setenv(consts.EnvNoUpdateNotifier, "1")         // set after the helper's neutralize
	if err := st.SetLastSeenChangelog("0.11.0"); err != nil {
		t.Fatalf("SetLastSeenChangelog: %v", err)
	}

	maybeShowChangelog(f, st, teaserEntries(), "0.12.0", "")
	if errOut.String() != "" {
		t.Errorf("expected silence with opt-out env, got:\n%s", errOut.String())
	}
	if got := st.LastSeenChangelog(); got != "0.11.0" {
		t.Errorf("opt-out run must leave cursor at 0.11.0, got %q", got)
	}
}

// TestMaybeShowChangelog_DevVersionNoop: a DEV build never shows the teaser.
func TestMaybeShowChangelog_DevVersionNoop(t *testing.T) {
	f, st, errOut := newChangelogTestFactory(t, true)
	maybeShowChangelog(f, st, teaserEntries(), "DEV", "")
	if errOut.String() != "" {
		t.Errorf("DEV build must not show a teaser, got:\n%s", errOut.String())
	}
	if got := st.LastSeenChangelog(); got != "" {
		t.Errorf("DEV build must not touch the cursor, got %q", got)
	}
}

// TestMaybeShowChangelog_NilStateNoop: a nil state facade (store unavailable) is
// a silent no-op.
func TestMaybeShowChangelog_NilStateNoop(t *testing.T) {
	f, _, errOut := newChangelogTestFactory(t, true)
	maybeShowChangelog(f, nil, teaserEntries(), "0.12.0", "0.10.0")
	if errOut.String() != "" {
		t.Errorf("nil state must be a no-op, got:\n%s", errOut.String())
	}
}

// TestMaybeShowChangelog_NilEntriesLeavesCursor: when the background load failed
// (nil entries) but a cursor is already established, the teaser stays silent and
// the cursor is left untouched so the next interactive run retries once entries
// load successfully.
func TestMaybeShowChangelog_NilEntriesLeavesCursor(t *testing.T) {
	f, st, errOut := newChangelogTestFactory(t, true)
	if err := st.SetLastSeenChangelog("0.11.0"); err != nil {
		t.Fatalf("SetLastSeenChangelog: %v", err)
	}

	maybeShowChangelog(f, st, nil, "0.12.0", "")
	if errOut.String() != "" {
		t.Errorf("nil entries must print nothing, got:\n%s", errOut.String())
	}
	if got := st.LastSeenChangelog(); got != "0.11.0" {
		t.Errorf("nil-entries run must leave cursor at 0.11.0, got %q", got)
	}
}

func TestPrintDockerInstallHelper(t *testing.T) {
	var buf bytes.Buffer
	cs := iostreams.NewColorScheme(false, "") // no color for test assertions
	pingErr := errors.New("dial unix /var/run/docker.sock: connect: connection refused")
	dockerErr := whail.ErrDockerHealthCheckFailed(pingErr)
	wrapped := fmt.Errorf("connecting to Docker: %w", dockerErr)

	printDockerInstallHelper(&buf, cs, wrapped)

	output := buf.String()
	wantParts := []string{
		"Failed to connect to Docker",
		"dial unix /var/run/docker.sock: connect: connection refused",
		"https://docs.docker.com/get-docker/",
		"docker info",
		"Re-run your command",
	}
	for _, part := range wantParts {
		if !strings.Contains(output, part) {
			t.Errorf("output missing %q, got:\n%s", part, output)
		}
	}
}

func TestPrintDockerInstallHelper_SentinelDetection(t *testing.T) {
	// Simulate the full error chain: whail → docker → factory → command
	underlying := errors.New("dial unix /var/run/docker.sock: connect: no such file or directory")
	dockerErr := whail.ErrDockerHealthCheckFailed(underlying)
	commandWrapped := fmt.Errorf("connecting to Docker: %w", dockerErr)

	// Verify the sentinel is detectable at the top level
	if !errors.Is(commandWrapped, whail.ErrDockerNotAvailable) {
		t.Fatal("sentinel not detectable through command wrapping")
	}
}

func TestPrintUpdateNotification_NilResult(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()

	printUpdateNotification(tio, nil)

	if errOut.String() != "" {
		t.Errorf("expected no output for nil result, got %q", errOut.String())
	}
}

func TestPrintUpdateNotification_NonTTY(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()
	// Default: non-TTY — should suppress output

	result := &update.CheckResult{
		CurrentVersion: "1.0.0",
		LatestVersion:  "2.0.0",
		ReleaseURL:     "https://github.com/schmitthub/clawker/releases/tag/v2.0.0",
		IsNewer:        true,
	}

	printUpdateNotification(tio, result)

	if errOut.String() != "" {
		t.Errorf("expected no output for non-TTY stderr, got %q", errOut.String())
	}
}

func TestPrintUpdateNotification_NotNewerSuppressed(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()
	tio.SetStderrTTY(true)

	// A check result that is NOT newer (e.g. user is up to date) must not
	// notify, even on a TTY. The result still carries fetched data so the
	// caller can persist it; only IsNewer gates the notification.
	result := &update.CheckResult{
		CurrentVersion: "2.0.0",
		LatestVersion:  "2.0.0",
		ReleaseURL:     "https://github.com/schmitthub/clawker/releases/tag/v2.0.0",
		IsNewer:        false,
	}

	printUpdateNotification(tio, result)

	if errOut.String() != "" {
		t.Errorf("expected no output when not newer, got %q", errOut.String())
	}
}

func TestPrintUpdateNotification_TTYWithResult(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()
	tio.SetStderrTTY(true)

	result := &update.CheckResult{
		CurrentVersion: "1.0.0",
		LatestVersion:  "2.0.0",
		ReleaseURL:     "https://github.com/schmitthub/clawker/releases/tag/v2.0.0",
		IsNewer:        true,
	}

	printUpdateNotification(tio, result)

	output := errOut.String()
	if output == "" {
		t.Fatal("expected notification output on TTY stderr, got empty")
	}
	if !strings.Contains(output, "1.0.0") {
		t.Errorf("output should contain current version '1.0.0', got %q", output)
	}
	if !strings.Contains(output, "2.0.0") {
		t.Errorf("output should contain latest version '2.0.0', got %q", output)
	}
	if !strings.Contains(output, "A new release of clawker is available:") {
		t.Errorf("output should contain announcement text, got %q", output)
	}
	if !strings.Contains(output, "To upgrade:") {
		t.Errorf("output should contain upgrade header, got %q", output)
	}
	if !strings.Contains(output, "brew upgrade clawker") {
		t.Errorf("output should contain brew upgrade instructions, got %q", output)
	}
	if !strings.Contains(output, "install.sh") {
		t.Errorf("output should contain install script reference, got %q", output)
	}
	if !strings.Contains(output, "clawker build") {
		t.Errorf("output should contain build command, got %q", output)
	}
	if !strings.Contains(output, "in each project") {
		t.Errorf("output should contain per-project rebuild reminder, got %q", output)
	}
	if !strings.Contains(output, "https://github.com/schmitthub/clawker/releases/tag/v2.0.0") {
		t.Errorf("output should contain release URL, got %q", output)
	}
}

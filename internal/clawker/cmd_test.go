package clawker

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/changelog"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/update"
	"github.com/schmitthub/clawker/pkg/whail"
)

// sampleEntries is the gained-entry slice the render tests feed into
// maybeShowChangelog. Rendering is the unit under test here; the
// fetch→parse→diff that produces these is covered in internal/changelog
// (changes_test.go), so a hand-built slice is the right input at this layer.
func sampleEntries() []changelog.Entry {
	return []changelog.Entry{
		{Version: "0.12.0", Date: "2026-06-11", Body: "### Added\n\n- **Command aliases.** Define your own shortcuts."},
		{Version: "0.11.0", Date: "2026-06-10", Body: "### Fixed\n\n- **Worktree masks.** Protect the host repository."},
	}
}

// newChangelogTestFactory builds a Factory for the teaser render/suppress
// tests. ttyStderr forces the stderr-TTY gate so the suppression branches can be
// exercised. Ambient teaser-suppression env (CI / opt-out) is neutralized so the
// tests drive the real TTY/opt-out logic, not the host env. The cursor lifecycle
// itself lives in internal/changelog and is tested there (changes_test.go), so
// these tests need no state facade.
func newChangelogTestFactory(t *testing.T, ttyStderr bool) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	t.Setenv("CI", "")
	t.Setenv(consts.EnvNoUpdateNotifier, "")
	tio, _, _, errOut := iostreams.Test()
	tio.SetStderrTTY(ttyStderr)

	nop := logger.Nop()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return nop, nil },
	}
	return f, errOut
}

// TestMaybeShowChangelog_RendersWhenInteractive: gained entries render the
// teaser header and version headers on an interactive (TTY) stderr.
func TestMaybeShowChangelog_RendersWhenInteractive(t *testing.T) {
	f, errOut := newChangelogTestFactory(t, true)
	maybeShowChangelog(f, sampleEntries())
	out := errOut.String()
	if !strings.Contains(out, "What's new in clawker") {
		t.Errorf("expected teaser header, got:\n%s", out)
	}
	for _, want := range []string{"v0.12.0", "v0.11.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("teaser missing %s, got:\n%s", want, out)
		}
	}
}

// TestMaybeShowChangelog_RendersBody: the teaser renders each entry's
// Keep-a-Changelog body, not just the version header.
func TestMaybeShowChangelog_RendersBody(t *testing.T) {
	f, errOut := newChangelogTestFactory(t, true)
	f.IOStreams.SetColorEnabled(false) // ASCII so body substrings assert cleanly
	maybeShowChangelog(f, sampleEntries())
	out := errOut.String()
	for _, want := range []string{"Command aliases", "Worktree masks"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered teaser missing body %q, got:\n%s", want, out)
		}
	}
}

// TestMaybeShowChangelog_EmptyGainedSilent: no gained entries → nothing printed.
func TestMaybeShowChangelog_EmptyGainedSilent(t *testing.T) {
	f, errOut := newChangelogTestFactory(t, true)
	maybeShowChangelog(f, nil)
	if out := errOut.String(); out != "" {
		t.Errorf("no gained entries must print nothing, got:\n%s", out)
	}
}

// TestMaybeShowChangelog_SuppressedNonTTY: a non-TTY stderr suppresses the
// teaser even with gained entries.
func TestMaybeShowChangelog_SuppressedNonTTY(t *testing.T) {
	f, errOut := newChangelogTestFactory(t, false)
	maybeShowChangelog(f, sampleEntries())
	if out := errOut.String(); out != "" {
		t.Errorf("non-TTY must suppress the teaser, got:\n%s", out)
	}
}

// TestMaybeShowChangelog_SuppressedByEnv: the update-notifier opt-out env
// suppresses the teaser on a TTY too.
func TestMaybeShowChangelog_SuppressedByEnv(t *testing.T) {
	f, errOut := newChangelogTestFactory(t, true)
	t.Setenv(consts.EnvNoUpdateNotifier, "1")
	maybeShowChangelog(f, sampleEntries())
	if out := errOut.String(); out != "" {
		t.Errorf("opt-out env must suppress the teaser, got:\n%s", out)
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

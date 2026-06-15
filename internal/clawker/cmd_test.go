package clawker

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/changelog"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/update"
	"github.com/schmitthub/clawker/pkg/whail"
)

// sampleEntries is the gained-entry slice the render tests feed into
// printChangelogTeaser. Rendering is the unit under test here; the
// fetch→parse→diff that produces these is covered in internal/changelog
// (changes_test.go), so a hand-built slice is the right input at this layer.
func sampleEntries() []changelog.Entry {
	return []changelog.Entry{
		{Version: "0.12.0", Date: "2026-06-11", Body: "### Added\n\n- **Command aliases.** Define your own shortcuts."},
		{Version: "0.11.0", Date: "2026-06-10", Body: "### Fixed\n\n- **Worktree masks.** Protect the host repository."},
	}
}

// teaserIOStreams builds a test IOStreams for the changelog teaser render tests.
// The teaser no longer gates on the TTY itself — suppression is decided once in
// Main via notificationsSuppressed — so these tests exercise the pure rendering
// path with whatever IOStreams they are given.
func teaserIOStreams() (*iostreams.IOStreams, *bytes.Buffer) {
	tio, _, _, errOut := iostreams.Test()
	return tio, errOut
}

// TestNotificationsSuppressed is the single suppression gate for BOTH the update
// notifier and the changelog teaser. The ambient CI / opt-out env is neutralized
// so the table drives the real TTY/opt-out logic, not the host environment.
func TestNotificationsSuppressed(t *testing.T) {
	tests := []struct {
		name      string
		stderrTTY bool
		noNotify  string
		ci        string
		want      bool
	}{
		{name: "clean interactive", stderrTTY: true, want: false},
		{name: "non-TTY stderr", stderrTTY: false, want: true},
		{name: "opt-out env set", stderrTTY: true, noNotify: "1", want: true},
		{name: "CI set", stderrTTY: true, ci: "1", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CI", tt.ci)
			t.Setenv(consts.EnvNoNotifier, tt.noNotify)
			tio, _, _, _ := iostreams.Test()
			tio.SetStderrTTY(tt.stderrTTY)
			if got := notificationsSuppressed(tio); got != tt.want {
				t.Errorf("notificationsSuppressed = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPrintChangelogTeaser_RendersHeaders: gained entries render the teaser
// header and per-release version headers.
func TestPrintChangelogTeaser_RendersHeaders(t *testing.T) {
	tio, errOut := teaserIOStreams()
	printChangelogTeaser(tio, sampleEntries())
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

// TestPrintChangelogTeaser_RendersBody: the teaser renders each entry's
// Keep-a-Changelog body, not just the version header.
func TestPrintChangelogTeaser_RendersBody(t *testing.T) {
	tio, errOut := teaserIOStreams()
	tio.SetColorEnabled(false) // ASCII so body substrings assert cleanly
	printChangelogTeaser(tio, sampleEntries())
	out := errOut.String()
	for _, want := range []string{"Command aliases", "Worktree masks"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered teaser missing body %q, got:\n%s", want, out)
		}
	}
}

// TestPrintChangelogTeaser_EmptySelfGuard: no gained entries → nothing printed.
// This is the teaser's own empty self-guard (mirrors printUpdateNotification's
// nil guard), so suppression no longer has to be the caller's concern.
func TestPrintChangelogTeaser_EmptySelfGuard(t *testing.T) {
	tio, errOut := teaserIOStreams()
	printChangelogTeaser(tio, nil)
	if out := errOut.String(); out != "" {
		t.Errorf("no gained entries must print nothing, got:\n%s", out)
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

func TestPrintUpdateNotification_NilInfo(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()

	printUpdateNotification(tio, nil)

	if errOut.String() != "" {
		t.Errorf("expected no output for nil info, got %q", errOut.String())
	}
}

func TestPrintUpdateNotification_RendersBody(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()

	// A non-nil ReleaseInfo always renders — the not-newer / TTL-fresh cases are
	// represented as a nil info from update.CheckForUpdate (covered above), and
	// TTY/CI/opt-out suppression is gated once in Main (notificationsSuppressed),
	// not in this renderer.
	info := &update.ReleaseInfo{
		CurrentVersion: "1.0.0",
		LatestVersion:  "2.0.0",
		ReleaseURL:     "https://github.com/schmitthub/clawker/releases/tag/v2.0.0",
	}

	printUpdateNotification(tio, info)

	output := errOut.String()
	if output == "" {
		t.Fatal("expected notification output for non-nil info, got empty")
	}
	wantParts := []string{
		"1.0.0",
		"2.0.0",
		"A new release of clawker is available:",
		"To upgrade:",
		"brew upgrade clawker",
		"install.sh",
		"clawker build",
		"in each project",
		"https://github.com/schmitthub/clawker/releases/tag/v2.0.0",
	}
	for _, want := range wantParts {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q, got %q", want, output)
		}
	}
}

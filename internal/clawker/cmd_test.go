package clawker

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/update"
)

func TestPrintUpdateNotification_NilResult(t *testing.T) {
	tio := iostreamstest.New()
	tio.SetInteractive(true)

	printUpdateNotification(tio.IOStreams, nil)

	if tio.ErrBuf.String() != "" {
		t.Errorf("expected no output for nil result, got %q", tio.ErrBuf.String())
	}
}

func TestPrintUpdateNotification_NonTTY(t *testing.T) {
	tio := iostreamstest.New()
	// Default: non-TTY — should suppress output

	result := &update.CheckResult{
		CurrentVersion: "1.0.0",
		LatestVersion:  "2.0.0",
		ReleaseURL:     "https://github.com/schmitthub/clawker/releases/tag/v2.0.0",
	}

	printUpdateNotification(tio.IOStreams, result)

	if tio.ErrBuf.String() != "" {
		t.Errorf("expected no output for non-TTY stderr, got %q", tio.ErrBuf.String())
	}
}

func TestPrintUpdateNotification_TTYWithResult(t *testing.T) {
	tio := iostreamstest.New()
	tio.SetInteractive(true)

	result := &update.CheckResult{
		CurrentVersion: "1.0.0",
		LatestVersion:  "2.0.0",
		ReleaseURL:     "https://github.com/schmitthub/clawker/releases/tag/v2.0.0",
	}

	printUpdateNotification(tio.IOStreams, result)

	output := tio.ErrBuf.String()
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
	if !strings.Contains(output, "https://github.com/schmitthub/clawker/releases/tag/v2.0.0") {
		t.Errorf("output should contain release URL, got %q", output)
	}
}

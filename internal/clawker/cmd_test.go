package clawker

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/update"
	"github.com/schmitthub/clawker/pkg/whail"
)

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
	}

	printUpdateNotification(tio, result)

	if errOut.String() != "" {
		t.Errorf("expected no output for non-TTY stderr, got %q", errOut.String())
	}
}

func TestPrintUpdateNotification_TTYWithResult(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()
	tio.SetStderrTTY(true)

	result := &update.CheckResult{
		CurrentVersion: "1.0.0",
		LatestVersion:  "2.0.0",
		ReleaseURL:     "https://github.com/schmitthub/clawker/releases/tag/v2.0.0",
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
	if !strings.Contains(output, "https://github.com/schmitthub/clawker/releases/tag/v2.0.0") {
		t.Errorf("output should contain release URL, got %q", output)
	}
}

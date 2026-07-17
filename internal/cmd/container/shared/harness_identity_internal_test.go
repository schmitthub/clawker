package shared

import (
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// errDaemon simulates a daemon-level inspect failure — permission, transport,
// engine wedge — anything that is NOT a benign not-found. Identity resolution
// must surface it, never silently resolve to the configured default harness.
var errDaemon = errors.New("daemon connection reset")

// The configured default in a blank config resolves to the built-in default
// harness; identity tests pin against it by const so a default change shows
// up here, not as a silent fixture drift.
const defaultHarness = consts.DefaultHarnessName

func TestHarnessForImage_LabelIsSourceOfTruth(t *testing.T) {
	cfg := configmocks.NewBlankConfig()
	fake := mocks.NewFakeClient(cfg)
	fake.SetupImageExistsWithLabels("clawker-proj:codex", map[string]string{
		consts.LabelHarness: "codex",
	})

	got, err := harnessForImage(t.Context(), fake.Client, cfg, "clawker-proj:codex", logger.Nop())
	if err != nil {
		t.Fatalf("harnessForImage() error = %v", err)
	}
	// The image label wins over the configured default — this is what keeps
	// recreation stable regardless of what build.harness says today.
	if got != "codex" {
		t.Errorf("harnessForImage() = %q, want %q (image label, not configured default)", got, "codex")
	}
}

func TestHarnessForImage_UnlabeledFallsBackToDefault(t *testing.T) {
	cfg := configmocks.NewBlankConfig()
	fake := mocks.NewFakeClient(cfg)
	fake.SetupImageExistsWithLabels("clawker-proj:latest", nil) // legacy pre-harness-label image

	got, err := harnessForImage(t.Context(), fake.Client, cfg, "clawker-proj:latest", logger.Nop())
	if err != nil {
		t.Fatalf("harnessForImage() error = %v — an unlabeled legacy image must fall back, not fail", err)
	}
	if got != defaultHarness {
		t.Errorf("harnessForImage() = %q, want configured default %q", got, defaultHarness)
	}
}

func TestHarnessForImage_NotFoundFallsBackToDefault(t *testing.T) {
	cfg := configmocks.NewBlankConfig()
	fake := mocks.NewFakeClient(cfg)
	fake.SetupImageExists("some-other-image", true) // requested ref: not found

	got, err := harnessForImage(t.Context(), fake.Client, cfg, "external:image", logger.Nop())
	if err != nil {
		t.Fatalf("harnessForImage() error = %v — not-found is the one benign sentinel and must collapse", err)
	}
	if got != defaultHarness {
		t.Errorf("harnessForImage() = %q, want configured default %q", got, defaultHarness)
	}
}

func TestHarnessForImage_InspectFailureSurfaces(t *testing.T) {
	cfg := configmocks.NewBlankConfig()
	fake := mocks.NewFakeClient(cfg)
	fake.SetupImageInspectError(errDaemon)

	got, err := harnessForImage(t.Context(), fake.Client, cfg, "clawker-proj:codex", logger.Nop())
	if err == nil {
		t.Fatalf("harnessForImage() = (%q, nil), want error — daemon failure silently resolved to the default",
			got)
	}
	if !strings.Contains(err.Error(), "clawker-proj:codex") {
		t.Errorf("error = %q, must name the image ref", err.Error())
	}
}

func TestContainerHarnessName_LabelIsSourceOfTruth(t *testing.T) {
	cfg := configmocks.NewBlankConfig()
	fake := mocks.NewFakeClient(cfg)
	fake.SetupContainerInspect("ctr1", container.Summary{ //nolint:exhaustruct // sparse fixture
		ID: "ctr1",
		Labels: map[string]string{
			cfg.LabelManaged():  cfg.ManagedLabelValue(),
			consts.LabelHarness: "acme.tools.myharness",
		},
	})

	got, err := containerHarnessName(t.Context(), fake.Client, cfg, "ctr1", logger.Nop())
	if err != nil {
		t.Fatalf("containerHarnessName() error = %v", err)
	}
	if got != "acme.tools.myharness" {
		t.Errorf("containerHarnessName() = %q, want %q (container label, not configured default)",
			got, "acme.tools.myharness")
	}
}

func TestContainerHarnessName_UnlabeledFallsBackToDefault(t *testing.T) {
	cfg := configmocks.NewBlankConfig()
	fake := mocks.NewFakeClient(cfg)
	fake.SetupContainerInspect("ctr1", container.Summary{ //nolint:exhaustruct // sparse fixture
		ID: "ctr1",
		Labels: map[string]string{
			cfg.LabelManaged(): cfg.ManagedLabelValue(), // pre-harness-label container
		},
	})

	got, err := containerHarnessName(t.Context(), fake.Client, cfg, "ctr1", logger.Nop())
	if err != nil {
		t.Fatalf("containerHarnessName() error = %v — an unlabeled legacy container must fall back, not fail", err)
	}
	if got != defaultHarness {
		t.Errorf("containerHarnessName() = %q, want configured default %q", got, defaultHarness)
	}
}

func TestContainerHarnessName_NotFoundFallsBackToDefault(t *testing.T) {
	cfg := configmocks.NewBlankConfig()
	fake := mocks.NewFakeClient(cfg)
	other := container.Summary{ID: "some-other-container"} //nolint:exhaustruct // sparse fixture
	fake.SetupContainerInspect("some-other-container", other)

	got, err := containerHarnessName(t.Context(), fake.Client, cfg, "ctr-missing", logger.Nop())
	if err != nil {
		t.Fatalf("containerHarnessName() error = %v — not-found is the one benign sentinel and must collapse", err)
	}
	if got != defaultHarness {
		t.Errorf("containerHarnessName() = %q, want configured default %q", got, defaultHarness)
	}
}

func TestContainerHarnessName_InspectFailureSurfaces(t *testing.T) {
	cfg := configmocks.NewBlankConfig()
	fake := mocks.NewFakeClient(cfg)
	fake.SetupContainerInspectError(errDaemon)

	got, err := containerHarnessName(t.Context(), fake.Client, cfg, "ctr1", logger.Nop())
	if err == nil {
		t.Fatalf("containerHarnessName() = (%q, nil), want error — daemon failure silently resolved to the default",
			got)
	}
	if !strings.Contains(err.Error(), "ctr1") {
		t.Errorf("error = %q, must name the container ref", err.Error())
	}
}

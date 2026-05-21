package workspace

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/volume"
	mobyclient "github.com/moby/moby/client"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// TestSnapshotStrategy_Prepare_UsesAgentVolumeLabels pins the contract that
// SnapshotStrategy.Prepare creates its workspace volume with the standard
// agent-volume labels. Without this, the cleanup query on
// LabelProject + LabelAgent would miss the volume and only the name
// fallback would catch it.
func TestSnapshotStrategy_Prepare_UsesAgentVolumeLabels(t *testing.T) {
	mockCfg := configmocks.NewBlankConfig()
	fake := mocks.NewFakeClient(mockCfg)
	fake.SetupVolumeExists("", false)

	var captured map[string]string
	fake.FakeAPI.VolumeCreateFn = func(_ context.Context, opts mobyclient.VolumeCreateOptions) (mobyclient.VolumeCreateResult, error) {
		captured = opts.Labels
		return mobyclient.VolumeCreateResult{
			Volume: volume.Volume{Name: opts.Name, Labels: opts.Labels},
		}, nil
	}
	// CopyToVolume errors during Prepare trigger a cleanup VolumeRemove.
	fake.FakeAPI.VolumeRemoveFn = func(_ context.Context, _ string, _ mobyclient.VolumeRemoveOptions) (mobyclient.VolumeRemoveResult, error) {
		return mobyclient.VolumeRemoveResult{}, nil
	}

	cfg := Config{
		ProjectName: "myproj",
		AgentName:   "myagent",
		HostPath:    "/nonexistent-path-for-test",
		RemotePath:  "/workspace",
	}
	s, err := NewSnapshotStrategy(cfg, logger.Nop())
	if err != nil {
		t.Fatalf("NewSnapshotStrategy: %v", err)
	}

	// Prepare fails inside CopyToVolume because HostPath doesn't exist, but
	// labels are captured by EnsureVolume which runs first.
	_ = s.Prepare(t.Context(), fake.Client)

	if captured == nil {
		t.Fatal("VolumeCreate was never called — labels not captured")
	}
	if got := captured[mockCfg.LabelPurpose()]; got != mockCfg.PurposeAgent() {
		t.Errorf("LabelPurpose = %q, want %q", got, mockCfg.PurposeAgent())
	}
	if got := captured[mockCfg.LabelProject()]; got != "myproj" {
		t.Errorf("LabelProject = %q, want %q", got, "myproj")
	}
	if got := captured[mockCfg.LabelAgent()]; got != "myagent" {
		t.Errorf("LabelAgent = %q, want %q", got, "myagent")
	}
	if _, ok := captured[mockCfg.LabelManaged()]; !ok {
		t.Error("LabelManaged missing")
	}
	// Guard against regression to the pre-fix hand-rolled label map.
	for k := range captured {
		switch k {
		case "clawker.project", "clawker.type", "clawker.mode":
			t.Errorf("legacy label %q must not be present", k)
		}
	}
}

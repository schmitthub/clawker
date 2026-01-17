package docker

import (
	"strings"
	"testing"
	"time"
)

func TestLabelConstants(t *testing.T) {
	// Verify label prefix consistency
	tests := []struct {
		name  string
		label string
	}{
		{"LabelManaged", LabelManaged},
		{"LabelProject", LabelProject},
		{"LabelAgent", LabelAgent},
		{"LabelVersion", LabelVersion},
		{"LabelImage", LabelImage},
		{"LabelCreated", LabelCreated},
		{"LabelWorkdir", LabelWorkdir},
		{"LabelPurpose", LabelPurpose},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.HasPrefix(tt.label, LabelPrefix) {
				t.Errorf("%s = %q, expected prefix %q", tt.name, tt.label, LabelPrefix)
			}
		})
	}
}

func TestContainerLabels(t *testing.T) {
	labels := ContainerLabels("myproject", "myagent", "1.0.0", "myimage:latest", "/workspace")

	expected := map[string]string{
		LabelManaged: ManagedLabelValue,
		LabelProject: "myproject",
		LabelAgent:   "myagent",
		LabelVersion: "1.0.0",
		LabelImage:   "myimage:latest",
		LabelWorkdir: "/workspace",
	}

	for key, want := range expected {
		if got := labels[key]; got != want {
			t.Errorf("labels[%q] = %q, want %q", key, got, want)
		}
	}

	// Verify created timestamp is present and valid
	created := labels[LabelCreated]
	if created == "" {
		t.Error("LabelCreated should not be empty")
	}
	if _, err := time.Parse(time.RFC3339, created); err != nil {
		t.Errorf("LabelCreated %q is not valid RFC3339: %v", created, err)
	}
}

func TestVolumeLabels(t *testing.T) {
	labels := VolumeLabels("myproject", "myagent", "workspace")

	expected := map[string]string{
		LabelManaged: ManagedLabelValue,
		LabelProject: "myproject",
		LabelAgent:   "myagent",
		LabelPurpose: "workspace",
	}

	for key, want := range expected {
		if got := labels[key]; got != want {
			t.Errorf("labels[%q] = %q, want %q", key, got, want)
		}
	}

	// VolumeLabels should NOT include created timestamp
	if _, ok := labels[LabelCreated]; ok {
		t.Error("VolumeLabels should not include LabelCreated")
	}
}

func TestImageLabels(t *testing.T) {
	labels := ImageLabels("myproject", "1.0.0")

	expected := map[string]string{
		LabelManaged: ManagedLabelValue,
		LabelProject: "myproject",
		LabelVersion: "1.0.0",
	}

	for key, want := range expected {
		if got := labels[key]; got != want {
			t.Errorf("labels[%q] = %q, want %q", key, got, want)
		}
	}

	// Verify created timestamp
	created := labels[LabelCreated]
	if created == "" {
		t.Error("LabelCreated should not be empty")
	}
}

func TestNetworkLabels(t *testing.T) {
	labels := NetworkLabels()

	if got := labels[LabelManaged]; got != ManagedLabelValue {
		t.Errorf("labels[LabelManaged] = %q, want %q", got, ManagedLabelValue)
	}

	// NetworkLabels should only have managed label
	if len(labels) != 1 {
		t.Errorf("NetworkLabels should have exactly 1 label, got %d", len(labels))
	}
}

func TestClawkerFilter(t *testing.T) {
	f := ClawkerFilter()

	// Should contain the managed label filter
	labelFilters := f["label"]
	if len(labelFilters) != 1 {
		t.Errorf("expected 1 label filter, got %d", len(labelFilters))
	}

	expected := LabelManaged + "=" + ManagedLabelValue
	if _, ok := labelFilters[expected]; !ok {
		t.Errorf("filter missing expected label %q", expected)
	}
}

func TestProjectFilter(t *testing.T) {
	f := ProjectFilter("myproject")

	labelFilters := f["label"]
	if len(labelFilters) != 2 {
		t.Errorf("expected 2 label filters, got %d", len(labelFilters))
	}

	// Check for both filters
	_, hasManaged := labelFilters[LabelManaged+"="+ManagedLabelValue]
	_, hasProject := labelFilters[LabelProject+"=myproject"]

	if !hasManaged {
		t.Error("ProjectFilter should include managed label")
	}
	if !hasProject {
		t.Error("ProjectFilter should include project label")
	}
}

func TestAgentFilter(t *testing.T) {
	f := AgentFilter("myproject", "myagent")

	labelFilters := f["label"]
	if len(labelFilters) != 3 {
		t.Errorf("expected 3 label filters, got %d", len(labelFilters))
	}

	// Check for all three filters
	_, hasManaged := labelFilters[LabelManaged+"="+ManagedLabelValue]
	_, hasProject := labelFilters[LabelProject+"=myproject"]
	_, hasAgent := labelFilters[LabelAgent+"=myagent"]

	if !hasManaged {
		t.Error("AgentFilter should include managed label")
	}
	if !hasProject {
		t.Error("AgentFilter should include project label")
	}
	if !hasAgent {
		t.Error("AgentFilter should include agent label")
	}
}

package docker

import (
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/config"
)

// testClient returns a *Client with only cfg set (no engine).
// Sufficient for label/filter methods that only read c.cfg.
func testClient(t *testing.T) (*Client, config.Config) {
	t.Helper()
	cfg := testConfig(t, `
version: "1"
project: "testproject"
`)
	return &Client{cfg: cfg}, cfg
}

func TestLabelConstants(t *testing.T) {
	_, cfg := testClient(t)

	// Verify all label methods return strings prefixed with LabelPrefix
	tests := []struct {
		name  string
		label string
	}{
		{"LabelManaged", cfg.LabelManaged()},
		{"LabelProject", cfg.LabelProject()},
		{"LabelAgent", cfg.LabelAgent()},
		{"LabelVersion", cfg.LabelVersion()},
		{"LabelImage", cfg.LabelImage()},
		{"LabelCreated", cfg.LabelCreated()},
		{"LabelWorkdir", cfg.LabelWorkdir()},
		{"LabelPurpose", cfg.LabelPurpose()},
	}

	prefix := cfg.LabelPrefix()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.HasPrefix(tt.label, prefix) {
				t.Errorf("%s = %q, expected prefix %q", tt.name, tt.label, prefix)
			}
		})
	}
}

func TestContainerLabels(t *testing.T) {
	c, cfg := testClient(t)

	t.Run("with project", func(t *testing.T) {
		labels := c.ContainerLabels("myproject", "myagent", "1.0.0", "myimage:latest", "/workspace")

		expected := map[string]string{
			cfg.LabelManaged(): cfg.ManagedLabelValue(),
			cfg.LabelProject(): "myproject",
			cfg.LabelAgent():   "myagent",
			cfg.LabelVersion(): "1.0.0",
			cfg.LabelImage():   "myimage:latest",
			cfg.LabelWorkdir(): "/workspace",
		}

		for key, want := range expected {
			if got := labels[key]; got != want {
				t.Errorf("labels[%q] = %q, want %q", key, got, want)
			}
		}

		// Verify created timestamp is present and valid
		created := labels[cfg.LabelCreated()]
		if created == "" {
			t.Error("LabelCreated should not be empty")
		}
		if _, err := time.Parse(time.RFC3339, created); err != nil {
			t.Errorf("LabelCreated %q is not valid RFC3339: %v", created, err)
		}
	})

	t.Run("empty project omits LabelProject", func(t *testing.T) {
		labels := c.ContainerLabels("", "dev", "1.0.0", "myimage:latest", "/workspace")

		if _, ok := labels[cfg.LabelProject()]; ok {
			t.Error("labels should not contain LabelProject when project is empty")
		}
		if got := labels[cfg.LabelAgent()]; got != "dev" {
			t.Errorf("labels[LabelAgent] = %q, want %q", got, "dev")
		}
		if got := labels[cfg.LabelManaged()]; got != cfg.ManagedLabelValue() {
			t.Errorf("labels[LabelManaged] = %q, want %q", got, cfg.ManagedLabelValue())
		}
	})
}

func TestVolumeLabels(t *testing.T) {
	c, cfg := testClient(t)

	t.Run("with project", func(t *testing.T) {
		labels := c.VolumeLabels("myproject", "myagent", "workspace")

		expected := map[string]string{
			cfg.LabelManaged(): cfg.ManagedLabelValue(),
			cfg.LabelProject(): "myproject",
			cfg.LabelAgent():   "myagent",
			cfg.LabelPurpose(): "workspace",
		}

		for key, want := range expected {
			if got := labels[key]; got != want {
				t.Errorf("labels[%q] = %q, want %q", key, got, want)
			}
		}

		// VolumeLabels should NOT include created timestamp
		if _, ok := labels[cfg.LabelCreated()]; ok {
			t.Error("VolumeLabels should not include LabelCreated")
		}
	})

	t.Run("empty project omits LabelProject", func(t *testing.T) {
		labels := c.VolumeLabels("", "dev", "workspace")

		if _, ok := labels[cfg.LabelProject()]; ok {
			t.Error("labels should not contain LabelProject when project is empty")
		}
		if got := labels[cfg.LabelAgent()]; got != "dev" {
			t.Errorf("labels[LabelAgent] = %q, want %q", got, "dev")
		}
	})
}

func TestGlobalVolumeLabels(t *testing.T) {
	c, cfg := testClient(t)

	labels := c.GlobalVolumeLabels("globals")

	expected := map[string]string{
		cfg.LabelManaged(): cfg.ManagedLabelValue(),
		cfg.LabelPurpose(): "globals",
	}

	for key, want := range expected {
		if got := labels[key]; got != want {
			t.Errorf("labels[%q] = %q, want %q", key, got, want)
		}
	}

	// Should NOT include project or agent labels
	if _, ok := labels[cfg.LabelProject()]; ok {
		t.Error("GlobalVolumeLabels should not include LabelProject")
	}
	if _, ok := labels[cfg.LabelAgent()]; ok {
		t.Error("GlobalVolumeLabels should not include LabelAgent")
	}

	// Should have exactly 2 labels
	if got := len(labels); got != 2 {
		t.Errorf("len(labels) = %d, want 2", got)
	}
}

func TestImageLabels(t *testing.T) {
	c, cfg := testClient(t)

	t.Run("with project", func(t *testing.T) {
		labels := c.ImageLabels("myproject", "1.0.0")

		expected := map[string]string{
			cfg.LabelManaged(): cfg.ManagedLabelValue(),
			cfg.LabelProject(): "myproject",
			cfg.LabelVersion(): "1.0.0",
		}

		for key, want := range expected {
			if got := labels[key]; got != want {
				t.Errorf("labels[%q] = %q, want %q", key, got, want)
			}
		}

		// Verify created timestamp
		created := labels[cfg.LabelCreated()]
		if created == "" {
			t.Error("LabelCreated should not be empty")
		}
	})

	t.Run("empty project omits LabelProject", func(t *testing.T) {
		labels := c.ImageLabels("", "1.0.0")

		if _, ok := labels[cfg.LabelProject()]; ok {
			t.Error("labels should not contain LabelProject when project is empty")
		}
		if got := labels[cfg.LabelVersion()]; got != "1.0.0" {
			t.Errorf("labels[LabelVersion] = %q, want %q", got, "1.0.0")
		}
	})
}

func TestNetworkLabels(t *testing.T) {
	c, cfg := testClient(t)

	labels := c.NetworkLabels()

	if got := labels[cfg.LabelManaged()]; got != cfg.ManagedLabelValue() {
		t.Errorf("labels[LabelManaged] = %q, want %q", got, cfg.ManagedLabelValue())
	}

	// NetworkLabels should only have managed label
	if len(labels) != 1 {
		t.Errorf("NetworkLabels should have exactly 1 label, got %d", len(labels))
	}
}

func TestClawkerFilter(t *testing.T) {
	c, cfg := testClient(t)

	f := c.ClawkerFilter()

	// Should contain the managed label filter
	labelFilters := f["label"]
	if len(labelFilters) != 1 {
		t.Errorf("expected 1 label filter, got %d", len(labelFilters))
	}

	expected := cfg.LabelManaged() + "=" + cfg.ManagedLabelValue()
	if _, ok := labelFilters[expected]; !ok {
		t.Errorf("filter missing expected label %q", expected)
	}
}

func TestProjectFilter(t *testing.T) {
	c, cfg := testClient(t)

	f := c.ProjectFilter("myproject")

	labelFilters := f["label"]
	if len(labelFilters) != 2 {
		t.Errorf("expected 2 label filters, got %d", len(labelFilters))
	}

	// Check for both filters
	_, hasManaged := labelFilters[cfg.LabelManaged()+"="+cfg.ManagedLabelValue()]
	_, hasProject := labelFilters[cfg.LabelProject()+"=myproject"]

	if !hasManaged {
		t.Error("ProjectFilter should include managed label")
	}
	if !hasProject {
		t.Error("ProjectFilter should include project label")
	}
}

func TestAgentFilter(t *testing.T) {
	c, cfg := testClient(t)

	f := c.AgentFilter("myproject", "myagent")

	labelFilters := f["label"]
	if len(labelFilters) != 3 {
		t.Errorf("expected 3 label filters, got %d", len(labelFilters))
	}

	// Check for all three filters
	_, hasManaged := labelFilters[cfg.LabelManaged()+"="+cfg.ManagedLabelValue()]
	_, hasProject := labelFilters[cfg.LabelProject()+"=myproject"]
	_, hasAgent := labelFilters[cfg.LabelAgent()+"=myagent"]

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

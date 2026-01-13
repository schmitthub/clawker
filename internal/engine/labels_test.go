package engine

import (
	"testing"
	"time"
)

func TestContainerLabels(t *testing.T) {
	project := "my-project"
	agent := "ralph"
	version := "1.2.3"
	image := "clawker/my-project:latest"
	workdir := "/Users/test/project"

	labels := ContainerLabels(project, agent, version, image, workdir)

	// Check required labels
	expectedLabels := map[string]string{
		LabelManaged: "true",
		LabelProject: project,
		LabelAgent:   agent,
		LabelVersion: version,
		LabelImage:   image,
		LabelWorkdir: workdir,
	}

	for key, expectedValue := range expectedLabels {
		if val, ok := labels[key]; !ok {
			t.Errorf("ContainerLabels() missing label %q", key)
		} else if val != expectedValue {
			t.Errorf("ContainerLabels() label %q = %q, want %q", key, val, expectedValue)
		}
	}

	// Check created timestamp exists and is valid
	if created, ok := labels[LabelCreated]; !ok {
		t.Error("ContainerLabels() missing created timestamp")
	} else {
		_, err := time.Parse(time.RFC3339, created)
		if err != nil {
			t.Errorf("ContainerLabels() created timestamp %q is not valid RFC3339: %v", created, err)
		}
	}
}

func TestVolumeLabels(t *testing.T) {
	project := "my-project"
	agent := "ralph"
	purpose := "workspace"

	labels := VolumeLabels(project, agent, purpose)

	expectedLabels := map[string]string{
		LabelManaged: "true",
		LabelProject: project,
		LabelAgent:   agent,
		LabelPurpose: purpose,
	}

	for key, expectedValue := range expectedLabels {
		if val, ok := labels[key]; !ok {
			t.Errorf("VolumeLabels() missing label %q", key)
		} else if val != expectedValue {
			t.Errorf("VolumeLabels() label %q = %q, want %q", key, val, expectedValue)
		}
	}
}

func TestVolumeLabels_Purposes(t *testing.T) {
	// Test different volume purposes
	purposes := []string{"workspace", "config", "history"}
	for _, purpose := range purposes {
		t.Run(purpose, func(t *testing.T) {
			labels := VolumeLabels("project", "agent", purpose)
			if labels[LabelPurpose] != purpose {
				t.Errorf("VolumeLabels() purpose = %q, want %q", labels[LabelPurpose], purpose)
			}
		})
	}
}

func TestClawkerFilter(t *testing.T) {
	filter := ClawkerFilter()

	// Verify the filter has the managed label
	filterMap := filter.Get("label")
	found := false
	for _, val := range filterMap {
		if val == LabelManaged+"=true" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("ClawkerFilter() should filter by %s=true", LabelManaged)
	}
}

func TestProjectFilter(t *testing.T) {
	project := "my-project"
	filter := ProjectFilter(project)

	filterMap := filter.Get("label")

	// Should have both managed and project labels
	hasManaged := false
	hasProject := false

	for _, val := range filterMap {
		if val == LabelManaged+"=true" {
			hasManaged = true
		}
		if val == LabelProject+"="+project {
			hasProject = true
		}
	}

	if !hasManaged {
		t.Errorf("ProjectFilter() should filter by %s=true", LabelManaged)
	}
	if !hasProject {
		t.Errorf("ProjectFilter() should filter by %s=%s", LabelProject, project)
	}
}

func TestAgentFilter(t *testing.T) {
	project := "my-project"
	agent := "ralph"
	filter := AgentFilter(project, agent)

	filterMap := filter.Get("label")

	// Should have managed, project, and agent labels
	hasManaged := false
	hasProject := false
	hasAgent := false

	for _, val := range filterMap {
		if val == LabelManaged+"=true" {
			hasManaged = true
		}
		if val == LabelProject+"="+project {
			hasProject = true
		}
		if val == LabelAgent+"="+agent {
			hasAgent = true
		}
	}

	if !hasManaged {
		t.Errorf("AgentFilter() should filter by %s=true", LabelManaged)
	}
	if !hasProject {
		t.Errorf("AgentFilter() should filter by %s=%s", LabelProject, project)
	}
	if !hasAgent {
		t.Errorf("AgentFilter() should filter by %s=%s", LabelAgent, agent)
	}
}

func TestLabelConstants(t *testing.T) {
	// Verify label constants have the correct prefix
	labels := []string{
		LabelManaged,
		LabelProject,
		LabelAgent,
		LabelVersion,
		LabelImage,
		LabelCreated,
		LabelWorkdir,
		LabelPurpose,
	}

	prefix := "com.clawker."
	for _, label := range labels {
		if len(label) < len(prefix) || label[:len(prefix)] != prefix {
			t.Errorf("Label %q should have prefix %q", label, prefix)
		}
	}
}


func TestFilterComposition(t *testing.T) {
	// Verify that more specific filters are supersets of less specific ones
	clawkerFilter := ClawkerFilter()
	projectFilter := ProjectFilter("myproject")
	agentFilter := AgentFilter("myproject", "myagent")

	clawkerLabels := clawkerFilter.Get("label")
	projectLabels := projectFilter.Get("label")
	agentLabels := agentFilter.Get("label")

	// Project filter should have at least as many labels as clawker filter
	if len(projectLabels) < len(clawkerLabels) {
		t.Error("ProjectFilter should have at least as many labels as ClawkerFilter")
	}

	// Agent filter should have at least as many labels as project filter
	if len(agentLabels) < len(projectLabels) {
		t.Error("AgentFilter should have at least as many labels as ProjectFilter")
	}
}

package docker

import (
	"strings"
	"testing"
)

func TestGenerateRandomName(t *testing.T) {
	// Generate multiple names and verify format
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		name := GenerateRandomName()
		parts := strings.Split(name, "-")
		if len(parts) != 2 {
			t.Errorf("GenerateRandomName() = %q, expected adjective-noun format", name)
		}
		seen[name] = true
	}

	// Should have generated multiple unique names (very unlikely to get all same)
	if len(seen) < 10 {
		t.Errorf("GenerateRandomName() generated too few unique names: %d", len(seen))
	}
}

func TestContainerName(t *testing.T) {
	tests := []struct {
		project string
		agent   string
		want    string
	}{
		{"myproject", "myagent", "clawker.myproject.myagent"},
		{"test", "agent1", "clawker.test.agent1"},
		{"backend", "worker", "clawker.backend.worker"},
		{"", "ralph", "clawker.ralph"},
	}

	for _, tt := range tests {
		t.Run(tt.project+"_"+tt.agent, func(t *testing.T) {
			got := ContainerName(tt.project, tt.agent)
			if got != tt.want {
				t.Errorf("ContainerName(%q, %q) = %q, want %q", tt.project, tt.agent, got, tt.want)
			}
		})
	}
}

func TestContainerNamesFromAgents(t *testing.T) {
	tests := []struct {
		name    string
		project string
		agents  []string
		want    []string
	}{
		{
			name:    "multiple agents with project",
			project: "myproject",
			agents:  []string{"ralph", "worker"},
			want:    []string{"clawker.myproject.ralph", "clawker.myproject.worker"},
		},
		{
			name:    "empty agents slice",
			project: "myproject",
			agents:  []string{},
			want:    []string{},
		},
		{
			name:    "nil agents slice",
			project: "myproject",
			agents:  nil,
			want:    nil,
		},
		{
			name:    "empty project gives 2-segment names",
			project: "",
			agents:  []string{"ralph", "worker"},
			want:    []string{"clawker.ralph", "clawker.worker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContainerNamesFromAgents(tt.project, tt.agents)
			if len(got) != len(tt.want) {
				t.Fatalf("ContainerNamesFromAgents(%q, %v) returned %d items, want %d", tt.project, tt.agents, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ContainerNamesFromAgents(%q, %v)[%d] = %q, want %q", tt.project, tt.agents, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestContainerNamePrefix(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{"myproject", "clawker.myproject."},
		{"test", "clawker.test."},
		{"", "clawker."},
	}

	for _, tt := range tests {
		t.Run(tt.project, func(t *testing.T) {
			got := ContainerNamePrefix(tt.project)
			if got != tt.want {
				t.Errorf("ContainerNamePrefix(%q) = %q, want %q", tt.project, got, tt.want)
			}
		})
	}
}

func TestVolumeName(t *testing.T) {
	tests := []struct {
		project string
		agent   string
		purpose string
		want    string
	}{
		{"myproject", "myagent", "workspace", "clawker.myproject.myagent-workspace"},
		{"test", "agent1", "config", "clawker.test.agent1-config"},
		{"backend", "worker", "history", "clawker.backend.worker-history"},
		{"", "ralph", "workspace", "clawker.ralph-workspace"},
	}

	for _, tt := range tests {
		name := tt.project + "_" + tt.agent + "_" + tt.purpose
		t.Run(name, func(t *testing.T) {
			got := VolumeName(tt.project, tt.agent, tt.purpose)
			if got != tt.want {
				t.Errorf("VolumeName(%q, %q, %q) = %q, want %q", tt.project, tt.agent, tt.purpose, got, tt.want)
			}
		})
	}
}

func TestImageTag(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{"myproject", "clawker-myproject:latest"},
		{"test", "clawker-test:latest"},
		{"", "clawker:latest"},
	}

	for _, tt := range tests {
		t.Run(tt.project, func(t *testing.T) {
			got := ImageTag(tt.project)
			if got != tt.want {
				t.Errorf("ImageTag(%q) = %q, want %q", tt.project, got, tt.want)
			}
		})
	}
}

func TestParseContainerName(t *testing.T) {
	tests := []struct {
		name        string
		wantProject string
		wantAgent   string
		wantOK      bool
	}{
		// Valid 3-segment names
		{"clawker.myproject.myagent", "myproject", "myagent", true},
		{"clawker.test.agent1", "test", "agent1", true},
		{"/clawker.backend.worker", "backend", "worker", true}, // Docker adds leading slash

		// Valid 2-segment orphan names
		{"clawker.ralph", "", "ralph", true},
		{"/clawker.ralph", "", "ralph", true}, // Docker adds leading slash

		// Invalid names
		{"invalid", "", "", false},
		{"notclawker.project.agent", "", "", false},
		{"clawker.a.b.c", "", "", false}, // Too many parts
		{"", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProject, gotAgent, gotOK := ParseContainerName(tt.name)
			if gotOK != tt.wantOK {
				t.Errorf("ParseContainerName(%q) ok = %v, want %v", tt.name, gotOK, tt.wantOK)
			}
			if gotOK {
				if gotProject != tt.wantProject {
					t.Errorf("ParseContainerName(%q) project = %q, want %q", tt.name, gotProject, tt.wantProject)
				}
				if gotAgent != tt.wantAgent {
					t.Errorf("ParseContainerName(%q) agent = %q, want %q", tt.name, gotAgent, tt.wantAgent)
				}
			}
		})
	}
}

func TestNetworkName(t *testing.T) {
	if NetworkName != "clawker-net" {
		t.Errorf("NetworkName = %q, want %q", NetworkName, "clawker-net")
	}
}

func TestNamePrefix(t *testing.T) {
	if NamePrefix != "clawker" {
		t.Errorf("NamePrefix = %q, want %q", NamePrefix, "clawker")
	}
}

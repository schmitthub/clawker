package docker

import (
	"strings"
	"testing"
)

func TestValidateResourceName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
		errMsg  string // substring expected in error message
	}{
		// Valid names
		{"dev", false, ""},
		{"my-agent", false, ""},
		{"agent_1", false, ""},
		{"Agent.2", false, ""},
		{"a", false, ""},
		{"A1-b2_c3.d4", false, ""},
		{"test123", false, ""},
		{strings.Repeat("a", 128), false, ""}, // max length

		// Invalid: empty
		{"", true, "cannot be empty"},

		// Invalid: starts with hyphen (flag-like values)
		{"--rm", true, "cannot start with a hyphen"},
		{"-it", true, "cannot start with a hyphen"},
		{"-v", true, "cannot start with a hyphen"},

		// Invalid: starts with non-alphanumeric
		{".hidden", true, "only [a-zA-Z0-9]"},
		{"_private", true, "only [a-zA-Z0-9]"},

		// Invalid: contains illegal characters
		{"my agent", true, "only [a-zA-Z0-9]"},
		{"my@agent", true, "only [a-zA-Z0-9]"},
		{"my/agent", true, "only [a-zA-Z0-9]"},
		{"my:agent", true, "only [a-zA-Z0-9]"},

		// Invalid: too long
		{strings.Repeat("a", 129), true, "too long"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResourceName(tt.name)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateResourceName(%q) = nil, want error containing %q", tt.name, tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateResourceName(%q) error = %q, want error containing %q", tt.name, err.Error(), tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("ValidateResourceName(%q) = %v, want nil", tt.name, err)
			}
		})
	}
}

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
		wantErr bool
	}{
		{"myproject", "myagent", "clawker.myproject.myagent", false},
		{"test", "agent1", "clawker.test.agent1", false},
		{"backend", "worker", "clawker.backend.worker", false},
		{"", "dev", "clawker.dev", false},

		// Error cases
		{"myproject", "--rm", "", true},
		{"myproject", "", "", true},
		{"--bad", "agent", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.project+"_"+tt.agent, func(t *testing.T) {
			got, err := ContainerName(tt.project, tt.agent)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ContainerName(%q, %q) = %q, nil; want error", tt.project, tt.agent, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ContainerName(%q, %q) unexpected error: %v", tt.project, tt.agent, err)
				return
			}
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
		wantErr bool
	}{
		{
			name:    "multiple agents with project",
			project: "myproject",
			agents:  []string{"dev", "worker"},
			want:    []string{"clawker.myproject.dev", "clawker.myproject.worker"},
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
			agents:  []string{"dev", "worker"},
			want:    []string{"clawker.dev", "clawker.worker"},
		},
		{
			name:    "invalid agent name returns error",
			project: "myproject",
			agents:  []string{"dev", "--rm"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ContainerNamesFromAgents(tt.project, tt.agents)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ContainerNamesFromAgents(%q, %v) = %v, nil; want error", tt.project, tt.agents, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ContainerNamesFromAgents(%q, %v) unexpected error: %v", tt.project, tt.agents, err)
				return
			}
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
		wantErr bool
	}{
		{"myproject", "myagent", "workspace", "clawker.myproject.myagent-workspace", false},
		{"test", "agent1", "config", "clawker.test.agent1-config", false},
		{"backend", "worker", "history", "clawker.backend.worker-history", false},
		{"", "dev", "workspace", "clawker.dev-workspace", false},

		// Error cases
		{"myproject", "--rm", "config", "", true},
		{"myproject", "", "config", "", true},
		{"--bad", "agent", "config", "", true},
	}

	for _, tt := range tests {
		name := tt.project + "_" + tt.agent + "_" + tt.purpose
		t.Run(name, func(t *testing.T) {
			got, err := VolumeName(tt.project, tt.agent, tt.purpose)
			if tt.wantErr {
				if err == nil {
					t.Errorf("VolumeName(%q, %q, %q) = %q, nil; want error", tt.project, tt.agent, tt.purpose, got)
				}
				return
			}
			if err != nil {
				t.Errorf("VolumeName(%q, %q, %q) unexpected error: %v", tt.project, tt.agent, tt.purpose, err)
				return
			}
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

func TestImageTagWithHash(t *testing.T) {
	tests := []struct {
		project string
		hash    string
		want    string
	}{
		{"myproject", "abc123def456", "clawker-myproject:sha-abc123def456"},
		{"test", "deadbeef0000", "clawker-test:sha-deadbeef0000"},
		{"", "abc123def456", "clawker:sha-abc123def456"},
	}

	for _, tt := range tests {
		t.Run(tt.project+"_"+tt.hash, func(t *testing.T) {
			got := ImageTagWithHash(tt.project, tt.hash)
			if got != tt.want {
				t.Errorf("ImageTagWithHash(%q, %q) = %q, want %q", tt.project, tt.hash, got, tt.want)
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
		{"clawker.dev", "", "dev", true},
		{"/clawker.dev", "", "dev", true}, // Docker adds leading slash

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

func TestGlobalVolumeName(t *testing.T) {
	tests := []struct {
		purpose string
		want    string
	}{
		{"globals", "clawker-globals"},
		{"cache", "clawker-cache"},
	}

	for _, tt := range tests {
		t.Run(tt.purpose, func(t *testing.T) {
			got := GlobalVolumeName(tt.purpose)
			if got != tt.want {
				t.Errorf("GlobalVolumeName(%q) = %q, want %q", tt.purpose, got, tt.want)
			}
		})
	}
}

func TestNamePrefix(t *testing.T) {
	if NamePrefix != "clawker" {
		t.Errorf("NamePrefix = %q, want %q", NamePrefix, "clawker")
	}
}

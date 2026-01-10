package engine

import (
	"testing"
)

func TestContainerName(t *testing.T) {
	tests := []struct {
		project string
		agent   string
		want    string
	}{
		{"my-project", "ralph", "claucker/my-project/ralph"},
		{"test", "worker", "claucker/test/worker"},
		{"project123", "clever-fox", "claucker/project123/clever-fox"},
	}

	for _, tt := range tests {
		t.Run(tt.project+"-"+tt.agent, func(t *testing.T) {
			got := ContainerName(tt.project, tt.agent)
			if got != tt.want {
				t.Errorf("ContainerName(%q, %q) = %q, want %q", tt.project, tt.agent, got, tt.want)
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
		{"my-project", "ralph", "workspace", "claucker/my-project/ralph-workspace"},
		{"my-project", "ralph", "config", "claucker/my-project/ralph-config"},
		{"test", "worker", "history", "claucker/test/worker-history"},
	}

	for _, tt := range tests {
		t.Run(tt.project+"-"+tt.agent+"-"+tt.purpose, func(t *testing.T) {
			got := VolumeName(tt.project, tt.agent, tt.purpose)
			if got != tt.want {
				t.Errorf("VolumeName(%q, %q, %q) = %q, want %q", tt.project, tt.agent, tt.purpose, got, tt.want)
			}
		})
	}
}

func TestParseContainerName(t *testing.T) {
	tests := []struct {
		name    string
		project string
		agent   string
		ok      bool
	}{
		{"claucker/myapp/ralph", "myapp", "ralph", true},
		{"claucker/test/worker", "test", "worker", true},
		{"/claucker/myapp/ralph", "myapp", "ralph", true}, // Docker adds leading /
		{"invalid-name", "", "", false},
		{"claucker-old-format", "", "", false},
		{"claucker/only-project", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, agent, ok := ParseContainerName(tt.name)
			if ok != tt.ok {
				t.Errorf("ParseContainerName(%q) ok = %v, want %v", tt.name, ok, tt.ok)
			}
			if ok && (project != tt.project || agent != tt.agent) {
				t.Errorf("ParseContainerName(%q) = (%q, %q), want (%q, %q)", tt.name, project, agent, tt.project, tt.agent)
			}
		})
	}
}

func TestContainerNamePrefix(t *testing.T) {
	got := ContainerNamePrefix("myapp")
	want := "claucker/myapp/"
	if got != want {
		t.Errorf("ContainerNamePrefix(%q) = %q, want %q", "myapp", got, want)
	}
}

func TestContainerConfigDefaults(t *testing.T) {
	cfg := ContainerConfig{
		Name:  "test-container",
		Image: "node:20-slim",
	}

	// Test that defaults are zero values
	if cfg.Tty {
		t.Error("Tty should default to false")
	}
	if cfg.OpenStdin {
		t.Error("OpenStdin should default to false")
	}
	if len(cfg.Mounts) != 0 {
		t.Error("Mounts should default to empty")
	}
	if len(cfg.Env) != 0 {
		t.Error("Env should default to empty")
	}
}

func TestContainerConfigWithTTY(t *testing.T) {
	cfg := ContainerConfig{
		Name:         "interactive-container",
		Image:        "node:20-slim",
		Tty:          true,
		OpenStdin:    true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	if !cfg.Tty {
		t.Error("Tty should be true for interactive containers")
	}
	if !cfg.OpenStdin {
		t.Error("OpenStdin should be true for interactive containers")
	}
	if !cfg.AttachStdin {
		t.Error("AttachStdin should be true for interactive containers")
	}
}

func TestContainerConfigWithSecurity(t *testing.T) {
	cfg := ContainerConfig{
		Name:   "secure-container",
		Image:  "node:20-slim",
		CapAdd: []string{"NET_ADMIN", "NET_RAW"},
		User:   "1001:1001",
	}

	// Verify security settings
	if len(cfg.CapAdd) != 2 {
		t.Errorf("CapAdd should have 2 entries, got %d", len(cfg.CapAdd))
	}

	hasNetAdmin := false
	hasNetRaw := false
	for _, cap := range cfg.CapAdd {
		if cap == "NET_ADMIN" {
			hasNetAdmin = true
		}
		if cap == "NET_RAW" {
			hasNetRaw = true
		}
	}

	if !hasNetAdmin {
		t.Error("CapAdd should include NET_ADMIN for firewall support")
	}
	if !hasNetRaw {
		t.Error("CapAdd should include NET_RAW for firewall support")
	}

	if cfg.User != "1001:1001" {
		t.Errorf("User = %q, want %q", cfg.User, "1001:1001")
	}
}

func TestVolumeNamingConsistency(t *testing.T) {
	projectName := "my-app"
	agentName := "ralph"

	containerName := ContainerName(projectName, agentName)
	workspaceVolume := VolumeName(projectName, agentName, "workspace")
	configVolume := VolumeName(projectName, agentName, "config")

	// All names should start with "claucker/"
	if containerName[:9] != "claucker/" {
		t.Errorf("Container name should start with 'claucker/': %s", containerName)
	}
	if workspaceVolume[:9] != "claucker/" {
		t.Errorf("Workspace volume should start with 'claucker/': %s", workspaceVolume)
	}
	if configVolume[:9] != "claucker/" {
		t.Errorf("Config volume should start with 'claucker/': %s", configVolume)
	}

	// Verify exact formats
	if containerName != "claucker/my-app/ralph" {
		t.Errorf("Container name format unexpected: %s", containerName)
	}
	if workspaceVolume != "claucker/my-app/ralph-workspace" {
		t.Errorf("Workspace volume format unexpected: %s", workspaceVolume)
	}
	if configVolume != "claucker/my-app/ralph-config" {
		t.Errorf("Config volume format unexpected: %s", configVolume)
	}
}

func TestGenerateRandomName(t *testing.T) {
	// Test that it generates non-empty names
	name := GenerateRandomName()
	if name == "" {
		t.Error("GenerateRandomName() should not return empty string")
	}

	// Test that it contains a hyphen (adjective-noun format)
	hasHyphen := false
	for _, c := range name {
		if c == '-' {
			hasHyphen = true
			break
		}
	}
	if !hasHyphen {
		t.Errorf("GenerateRandomName() = %q, expected adjective-noun format with hyphen", name)
	}

	// Test that multiple calls generate different names (with high probability)
	names := make(map[string]bool)
	for i := 0; i < 10; i++ {
		names[GenerateRandomName()] = true
	}
	if len(names) < 5 {
		t.Error("GenerateRandomName() should generate diverse names")
	}
}

package engine

import (
	"testing"
)

func TestContainerName(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{"my-project", "claucker-my-project"},
		{"test", "claucker-test"},
		{"project123", "claucker-project123"},
	}

	for _, tt := range tests {
		t.Run(tt.project, func(t *testing.T) {
			got := ContainerName(tt.project)
			if got != tt.want {
				t.Errorf("ContainerName(%q) = %q, want %q", tt.project, got, tt.want)
			}
		})
	}
}

func TestVolumeName(t *testing.T) {
	tests := []struct {
		project string
		purpose string
		want    string
	}{
		{"my-project", "workspace", "claucker-my-project-workspace"},
		{"my-project", "config", "claucker-my-project-config"},
		{"test", "data", "claucker-test-data"},
	}

	for _, tt := range tests {
		t.Run(tt.project+"-"+tt.purpose, func(t *testing.T) {
			got := VolumeName(tt.project, tt.purpose)
			if got != tt.want {
				t.Errorf("VolumeName(%q, %q) = %q, want %q", tt.project, tt.purpose, got, tt.want)
			}
		})
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

	containerName := ContainerName(projectName)
	workspaceVolume := VolumeName(projectName, "workspace")
	configVolume := VolumeName(projectName, "config")

	// All names should start with "claucker-"
	if containerName[:9] != "claucker-" {
		t.Errorf("Container name should start with 'claucker-': %s", containerName)
	}
	if workspaceVolume[:9] != "claucker-" {
		t.Errorf("Workspace volume should start with 'claucker-': %s", workspaceVolume)
	}
	if configVolume[:9] != "claucker-" {
		t.Errorf("Config volume should start with 'claucker-': %s", configVolume)
	}

	// All names should contain the project name
	if containerName != "claucker-my-app" {
		t.Errorf("Container name format unexpected: %s", containerName)
	}
	if workspaceVolume != "claucker-my-app-workspace" {
		t.Errorf("Workspace volume format unexpected: %s", workspaceVolume)
	}
	if configVolume != "claucker-my-app-config" {
		t.Errorf("Config volume format unexpected: %s", configVolume)
	}
}

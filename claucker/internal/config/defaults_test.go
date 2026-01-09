package config

import (
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Test version
	if cfg.Version != "1" {
		t.Errorf("DefaultConfig().Version = %q, want %q", cfg.Version, "1")
	}

	// Test build defaults
	if cfg.Build.Image != "node:20-slim" {
		t.Errorf("DefaultConfig().Build.Image = %q, want %q", cfg.Build.Image, "node:20-slim")
	}

	expectedPackages := []string{"git", "curl", "ripgrep"}
	if len(cfg.Build.Packages) != len(expectedPackages) {
		t.Errorf("DefaultConfig().Build.Packages length = %d, want %d", len(cfg.Build.Packages), len(expectedPackages))
	}
	for i, pkg := range expectedPackages {
		if i < len(cfg.Build.Packages) && cfg.Build.Packages[i] != pkg {
			t.Errorf("DefaultConfig().Build.Packages[%d] = %q, want %q", i, cfg.Build.Packages[i], pkg)
		}
	}

	// Test workspace defaults
	if cfg.Workspace.RemotePath != "/workspace" {
		t.Errorf("DefaultConfig().Workspace.RemotePath = %q, want %q", cfg.Workspace.RemotePath, "/workspace")
	}
	if cfg.Workspace.DefaultMode != "bind" {
		t.Errorf("DefaultConfig().Workspace.DefaultMode = %q, want %q", cfg.Workspace.DefaultMode, "bind")
	}

	// Test security defaults - firewall should be enabled by default
	if !cfg.Security.EnableFirewall {
		t.Error("DefaultConfig().Security.EnableFirewall should be true (security-first)")
	}

	// Docker socket should be disabled by default
	if cfg.Security.DockerSocket {
		t.Error("DefaultConfig().Security.DockerSocket should be false (security-first)")
	}

	// Check CapAdd for firewall support
	hasNetAdmin := false
	hasNetRaw := false
	for _, cap := range cfg.Security.CapAdd {
		if cap == "NET_ADMIN" {
			hasNetAdmin = true
		}
		if cap == "NET_RAW" {
			hasNetRaw = true
		}
	}
	if !hasNetAdmin {
		t.Error("DefaultConfig().Security.CapAdd should include NET_ADMIN")
	}
	if !hasNetRaw {
		t.Error("DefaultConfig().Security.CapAdd should include NET_RAW")
	}
}

func TestDefaultConfigYAML(t *testing.T) {
	// Test that the YAML template is valid
	if DefaultConfigYAML == "" {
		t.Error("DefaultConfigYAML should not be empty")
	}

	// Check for required sections
	requiredSections := []string{
		"version:",
		"project:",
		"build:",
		"agent:",
		"workspace:",
		"security:",
	}

	for _, section := range requiredSections {
		if !strings.Contains(DefaultConfigYAML, section) {
			t.Errorf("DefaultConfigYAML should contain %q", section)
		}
	}

	// Check for placeholder
	if !strings.Contains(DefaultConfigYAML, "%s") {
		t.Error("DefaultConfigYAML should contain placeholder for project name")
	}

	// Verify security defaults are documented correctly
	if !strings.Contains(DefaultConfigYAML, "enable_firewall: true") {
		t.Error("DefaultConfigYAML should document enable_firewall: true as default")
	}
	if !strings.Contains(DefaultConfigYAML, "docker_socket: false") {
		t.Error("DefaultConfigYAML should document docker_socket: false as default")
	}
}

func TestDefaultIgnoreFile(t *testing.T) {
	if DefaultIgnoreFile == "" {
		t.Error("DefaultIgnoreFile should not be empty")
	}

	// Check for critical patterns that should always be ignored
	criticalPatterns := []string{
		"node_modules/",
		".git/",
		".env",
		"*.pem",
		"*.key",
	}

	for _, pattern := range criticalPatterns {
		if !strings.Contains(DefaultIgnoreFile, pattern) {
			t.Errorf("DefaultIgnoreFile should contain critical pattern %q", pattern)
		}
	}
}

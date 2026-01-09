package config

import (
	"testing"
)

func TestParseMode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Mode
		wantErr bool
	}{
		{
			name:    "bind mode",
			input:   "bind",
			want:    ModeBind,
			wantErr: false,
		},
		{
			name:    "snapshot mode",
			input:   "snapshot",
			want:    ModeSnapshot,
			wantErr: false,
		},
		{
			name:    "empty defaults to bind",
			input:   "",
			want:    ModeBind,
			wantErr: false,
		},
		{
			name:    "invalid mode returns error",
			input:   "invalid",
			want:    "",
			wantErr: true,
		},
		{
			name:    "uppercase bind is invalid",
			input:   "BIND",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMode() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidationError(t *testing.T) {
	err := &ValidationError{
		Field:   "project",
		Message: "is required",
		Value:   nil,
	}

	expected := "invalid project: is required"
	if err.Error() != expected {
		t.Errorf("ValidationError.Error() = %q, want %q", err.Error(), expected)
	}
}

func TestConfigStructure(t *testing.T) {
	// Test that Config struct can be instantiated with all fields
	cfg := Config{
		Version: "1",
		Project: "test-project",
		Build: BuildConfig{
			Image:      "node:20-slim",
			Dockerfile: "./Dockerfile",
			Packages:   []string{"git", "curl"},
			Context:    ".",
			BuildArgs:  map[string]string{"ARG1": "value1"},
		},
		Agent: AgentConfig{
			Includes: []string{"./README.md"},
			Env:      map[string]string{"NODE_ENV": "development"},
			Memory:   "4G",
		},
		Workspace: WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "snapshot",
		},
		Security: SecurityConfig{
			EnableFirewall: true,
			DockerSocket:   false,
			AllowedDomains: []string{"github.com"},
			CapAdd:         []string{"NET_ADMIN"},
		},
	}

	// Verify fields are set correctly
	if cfg.Version != "1" {
		t.Errorf("Config.Version = %q, want %q", cfg.Version, "1")
	}
	if cfg.Project != "test-project" {
		t.Errorf("Config.Project = %q, want %q", cfg.Project, "test-project")
	}
	if cfg.Build.Image != "node:20-slim" {
		t.Errorf("Config.Build.Image = %q, want %q", cfg.Build.Image, "node:20-slim")
	}
	if !cfg.Security.EnableFirewall {
		t.Error("Config.Security.EnableFirewall should be true")
	}
	if cfg.Security.DockerSocket {
		t.Error("Config.Security.DockerSocket should be false")
	}
}

func TestModeConstants(t *testing.T) {
	if ModeBind != "bind" {
		t.Errorf("ModeBind = %q, want %q", ModeBind, "bind")
	}
	if ModeSnapshot != "snapshot" {
		t.Errorf("ModeSnapshot = %q, want %q", ModeSnapshot, "snapshot")
	}
}

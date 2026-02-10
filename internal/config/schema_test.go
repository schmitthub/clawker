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
	cfg := Project{
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
			Firewall:     &FirewallConfig{Enable: true, AddDomains: []string{"github.com"}},
			DockerSocket: false,
			CapAdd:       []string{"NET_ADMIN"},
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
	if !cfg.Security.FirewallEnabled() {
		t.Error("Config.Security.FirewallEnabled() should be true")
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

func boolPtr(b bool) *bool {
	return &b
}

func TestGitCredentialsConfig_GitHTTPSEnabled(t *testing.T) {
	tests := []struct {
		name             string
		config           *GitCredentialsConfig
		hostProxyEnabled bool
		want             bool
	}{
		{
			name:             "nil config, host proxy enabled",
			config:           nil,
			hostProxyEnabled: true,
			want:             true,
		},
		{
			name:             "nil config, host proxy disabled",
			config:           nil,
			hostProxyEnabled: false,
			want:             false,
		},
		{
			name:             "nil ForwardHTTPS, host proxy enabled",
			config:           &GitCredentialsConfig{ForwardHTTPS: nil},
			hostProxyEnabled: true,
			want:             true,
		},
		{
			name:             "nil ForwardHTTPS, host proxy disabled",
			config:           &GitCredentialsConfig{ForwardHTTPS: nil},
			hostProxyEnabled: false,
			want:             false,
		},
		{
			name:             "ForwardHTTPS true, host proxy enabled",
			config:           &GitCredentialsConfig{ForwardHTTPS: boolPtr(true)},
			hostProxyEnabled: true,
			want:             true,
		},
		{
			name:             "ForwardHTTPS true, host proxy disabled",
			config:           &GitCredentialsConfig{ForwardHTTPS: boolPtr(true)},
			hostProxyEnabled: false,
			want:             false,
		},
		{
			name:             "ForwardHTTPS false, host proxy enabled",
			config:           &GitCredentialsConfig{ForwardHTTPS: boolPtr(false)},
			hostProxyEnabled: true,
			want:             false,
		},
		{
			name:             "ForwardHTTPS false, host proxy disabled",
			config:           &GitCredentialsConfig{ForwardHTTPS: boolPtr(false)},
			hostProxyEnabled: false,
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GitHTTPSEnabled(tt.hostProxyEnabled)
			if got != tt.want {
				t.Errorf("GitHTTPSEnabled(%v) = %v, want %v", tt.hostProxyEnabled, got, tt.want)
			}
		})
	}
}

func TestGitCredentialsConfig_GitSSHEnabled(t *testing.T) {
	tests := []struct {
		name   string
		config *GitCredentialsConfig
		want   bool
	}{
		{
			name:   "nil config",
			config: nil,
			want:   true,
		},
		{
			name:   "nil ForwardSSH",
			config: &GitCredentialsConfig{ForwardSSH: nil},
			want:   true,
		},
		{
			name:   "ForwardSSH true",
			config: &GitCredentialsConfig{ForwardSSH: boolPtr(true)},
			want:   true,
		},
		{
			name:   "ForwardSSH false",
			config: &GitCredentialsConfig{ForwardSSH: boolPtr(false)},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GitSSHEnabled()
			if got != tt.want {
				t.Errorf("GitSSHEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGitCredentialsConfig_CopyGitConfigEnabled(t *testing.T) {
	tests := []struct {
		name   string
		config *GitCredentialsConfig
		want   bool
	}{
		{
			name:   "nil config",
			config: nil,
			want:   true,
		},
		{
			name:   "nil CopyGitConfig",
			config: &GitCredentialsConfig{CopyGitConfig: nil},
			want:   true,
		},
		{
			name:   "CopyGitConfig true",
			config: &GitCredentialsConfig{CopyGitConfig: boolPtr(true)},
			want:   true,
		},
		{
			name:   "CopyGitConfig false",
			config: &GitCredentialsConfig{CopyGitConfig: boolPtr(false)},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.CopyGitConfigEnabled()
			if got != tt.want {
				t.Errorf("CopyGitConfigEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClaudeCodeConfig_UseHostAuthEnabled(t *testing.T) {
	tests := []struct {
		name   string
		config *ClaudeCodeConfig
		want   bool
	}{
		{
			name:   "nil config",
			config: nil,
			want:   true,
		},
		{
			name:   "nil UseHostAuth",
			config: &ClaudeCodeConfig{UseHostAuth: nil},
			want:   true,
		},
		{
			name:   "UseHostAuth true",
			config: &ClaudeCodeConfig{UseHostAuth: boolPtr(true)},
			want:   true,
		},
		{
			name:   "UseHostAuth false",
			config: &ClaudeCodeConfig{UseHostAuth: boolPtr(false)},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.UseHostAuthEnabled()
			if got != tt.want {
				t.Errorf("UseHostAuthEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClaudeCodeConfig_ConfigStrategy(t *testing.T) {
	tests := []struct {
		name   string
		config *ClaudeCodeConfig
		want   string
	}{
		{
			name:   "nil config",
			config: nil,
			want:   "fresh",
		},
		{
			name:   "empty strategy",
			config: &ClaudeCodeConfig{Config: ClaudeCodeConfigOptions{Strategy: ""}},
			want:   "fresh",
		},
		{
			name:   "explicit fresh",
			config: &ClaudeCodeConfig{Config: ClaudeCodeConfigOptions{Strategy: "fresh"}},
			want:   "fresh",
		},
		{
			name:   "explicit copy",
			config: &ClaudeCodeConfig{Config: ClaudeCodeConfigOptions{Strategy: "copy"}},
			want:   "copy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.ConfigStrategy()
			if got != tt.want {
				t.Errorf("ConfigStrategy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgentConfig_SharedDirEnabled(t *testing.T) {
	tests := []struct {
		name   string
		config *AgentConfig
		want   bool
	}{
		{
			name:   "nil config",
			config: nil,
			want:   false,
		},
		{
			name:   "nil EnableSharedDir",
			config: &AgentConfig{EnableSharedDir: nil},
			want:   false,
		},
		{
			name:   "EnableSharedDir true",
			config: &AgentConfig{EnableSharedDir: boolPtr(true)},
			want:   true,
		},
		{
			name:   "EnableSharedDir false",
			config: &AgentConfig{EnableSharedDir: boolPtr(false)},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.SharedDirEnabled()
			if got != tt.want {
				t.Errorf("SharedDirEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

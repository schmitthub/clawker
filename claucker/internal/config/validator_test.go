package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatorValidVersion(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	validator := NewValidator(tmpDir)

	tests := []struct {
		name    string
		version string
		wantErr bool
	}{
		{"valid version 1", "1", false},
		{"empty version", "", true},
		{"invalid version 2", "2", true},
		{"invalid version string", "one", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Version = tt.version
			cfg.Project = "test-project"

			err := validator.Validate(cfg)
			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatorValidProject(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	validator := NewValidator(tmpDir)

	tests := []struct {
		name    string
		project string
		wantErr bool
	}{
		{"valid project name", "my-project", false},
		{"valid project with numbers", "project123", false},
		{"valid project with underscore", "my_project", false},
		{"empty project", "", true},
		{"project with spaces", "my project", true},
		{"project with slash", "my/project", true},
		{"project with colon", "my:project", true},
		{"project with asterisk", "my*project", true},
		{"project too long", string(make([]byte, 65)), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Version = "1"
			cfg.Project = tt.project

			err := validator.Validate(cfg)
			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatorValidBuild(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test Dockerfile
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte("FROM alpine"), 0644); err != nil {
		t.Fatalf("failed to create Dockerfile: %v", err)
	}

	// Create a test context directory
	contextDir := filepath.Join(tmpDir, "context")
	if err := os.Mkdir(contextDir, 0755); err != nil {
		t.Fatalf("failed to create context dir: %v", err)
	}

	validator := NewValidator(tmpDir)

	tests := []struct {
		name       string
		image      string
		dockerfile string
		context    string
		wantErr    bool
	}{
		{"valid image only", "node:20-slim", "", "", false},
		{"valid dockerfile only", "", "./Dockerfile", "", false},
		{"valid image and dockerfile", "node:20-slim", "./Dockerfile", "", false},
		{"missing image and dockerfile", "", "", "", true},
		{"nonexistent dockerfile", "", "./nonexistent", "", true},
		{"valid context directory", "node:20-slim", "", "./context", false},
		{"nonexistent context", "node:20-slim", "", "./nonexistent", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Version = "1"
			cfg.Project = "test-project"
			cfg.Build.Image = tt.image
			cfg.Build.Dockerfile = tt.dockerfile
			cfg.Build.Context = tt.context

			err := validator.Validate(cfg)
			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatorValidWorkspace(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	validator := NewValidator(tmpDir)

	tests := []struct {
		name       string
		remotePath string
		mode       string
		wantErr    bool
	}{
		{"valid absolute path", "/workspace", "bind", false},
		{"valid snapshot mode", "/workspace", "snapshot", false},
		{"empty remote path", "", "bind", true},
		{"relative remote path", "workspace", "bind", true},
		{"invalid mode", "/workspace", "invalid", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Version = "1"
			cfg.Project = "test-project"
			cfg.Workspace.RemotePath = tt.remotePath
			cfg.Workspace.DefaultMode = tt.mode

			err := validator.Validate(cfg)
			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatorValidAgent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test include file
	includePath := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(includePath, []byte("# Test"), 0644); err != nil {
		t.Fatalf("failed to create include file: %v", err)
	}

	validator := NewValidator(tmpDir)

	tests := []struct {
		name     string
		includes []string
		env      map[string]string
		wantErr  bool
	}{
		{"valid include file", []string{"./README.md"}, nil, false},
		{"nonexistent include file", []string{"./nonexistent.md"}, nil, true},
		{"valid env vars", nil, map[string]string{"NODE_ENV": "dev"}, false},
		{"invalid env var name with space", nil, map[string]string{"NODE ENV": "dev"}, true},
		{"invalid env var name with equals", nil, map[string]string{"NODE=ENV": "dev"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Version = "1"
			cfg.Project = "test-project"
			if tt.includes != nil {
				cfg.Agent.Includes = tt.includes
			}
			if tt.env != nil {
				cfg.Agent.Env = tt.env
			}

			err := validator.Validate(cfg)
			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatorValidSecurity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	validator := NewValidator(tmpDir)

	tests := []struct {
		name           string
		allowedDomains []string
		wantErr        bool
	}{
		{"valid domains", []string{"github.com", "registry.npmjs.org"}, false},
		{"domain with whitespace", []string{"github .com"}, true},
		{"empty allowed domains", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Version = "1"
			cfg.Project = "test-project"
			cfg.Security.AllowedDomains = tt.allowedDomains

			err := validator.Validate(cfg)
			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMultiValidationError(t *testing.T) {
	errors := []error{
		&ValidationError{Field: "version", Message: "is required"},
		&ValidationError{Field: "project", Message: "is required"},
	}

	multiErr := &MultiValidationError{Errors: errors}

	// Test single error case
	singleErr := &MultiValidationError{Errors: []error{errors[0]}}
	if singleErr.Error() != "invalid version: is required" {
		t.Errorf("single error message wrong: %s", singleErr.Error())
	}

	// Test multiple errors case
	errStr := multiErr.Error()
	if errStr == "" {
		t.Error("MultiValidationError.Error() should not be empty")
	}

	// Verify we can get the individual errors
	validationErrors := multiErr.ValidationErrors()
	if len(validationErrors) != 2 {
		t.Errorf("ValidationErrors() returned %d errors, want 2", len(validationErrors))
	}
}

func TestValidatorCompleteConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create necessary files
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Test"), 0644); err != nil {
		t.Fatalf("failed to create README.md: %v", err)
	}

	validator := NewValidator(tmpDir)

	// Test a completely valid configuration
	cfg := &Config{
		Version: "1",
		Project: "complete-project",
		Build: BuildConfig{
			Image:    "node:20-slim",
			Packages: []string{"git", "curl"},
		},
		Agent: AgentConfig{
			Includes: []string{"./README.md"},
			Env:      map[string]string{"NODE_ENV": "development"},
		},
		Workspace: WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
		Security: SecurityConfig{
			EnableFirewall: true,
			DockerSocket:   false,
			AllowedDomains: []string{"github.com"},
			CapAdd:         []string{"NET_ADMIN", "NET_RAW"},
		},
	}

	if err := validator.Validate(cfg); err != nil {
		t.Errorf("Validate() returned error for valid config: %v", err)
	}
}

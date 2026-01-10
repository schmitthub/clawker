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

func TestValidatorValidInstructions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	validator := NewValidator(tmpDir)

	tests := []struct {
		name         string
		instructions *DockerInstructions
		wantErr      bool
	}{
		{
			name:         "nil instructions is valid",
			instructions: nil,
			wantErr:      false,
		},
		{
			name: "valid copy instruction",
			instructions: &DockerInstructions{
				Copy: []CopyInstruction{
					{Src: "config.json", Dest: "/app/config.json"},
				},
			},
			wantErr: false,
		},
		{
			name: "copy with chown",
			instructions: &DockerInstructions{
				Copy: []CopyInstruction{
					{Src: "config.json", Dest: "/app/config.json", Chown: "claude:claude"},
				},
			},
			wantErr: false,
		},
		{
			name: "copy with chmod",
			instructions: &DockerInstructions{
				Copy: []CopyInstruction{
					{Src: "script.sh", Dest: "/app/script.sh", Chmod: "755"},
				},
			},
			wantErr: false,
		},
		{
			name: "copy missing src",
			instructions: &DockerInstructions{
				Copy: []CopyInstruction{
					{Src: "", Dest: "/app/config.json"},
				},
			},
			wantErr: true,
		},
		{
			name: "copy missing dest",
			instructions: &DockerInstructions{
				Copy: []CopyInstruction{
					{Src: "config.json", Dest: ""},
				},
			},
			wantErr: true,
		},
		{
			name: "copy with path traversal",
			instructions: &DockerInstructions{
				Copy: []CopyInstruction{
					{Src: "../../../etc/passwd", Dest: "/app/passwd"},
				},
			},
			wantErr: true,
		},
		{
			name: "copy with invalid chown",
			instructions: &DockerInstructions{
				Copy: []CopyInstruction{
					{Src: "config.json", Dest: "/app/config.json", Chown: "invalid format here"},
				},
			},
			wantErr: true,
		},
		{
			name: "copy with invalid chmod",
			instructions: &DockerInstructions{
				Copy: []CopyInstruction{
					{Src: "script.sh", Dest: "/app/script.sh", Chmod: "999"},
				},
			},
			wantErr: true,
		},
		{
			name: "valid expose port",
			instructions: &DockerInstructions{
				Expose: []ExposePort{
					{Port: 8080},
					{Port: 3000, Protocol: "tcp"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid expose port too low",
			instructions: &DockerInstructions{
				Expose: []ExposePort{
					{Port: 0},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid expose port too high",
			instructions: &DockerInstructions{
				Expose: []ExposePort{
					{Port: 70000},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid expose protocol",
			instructions: &DockerInstructions{
				Expose: []ExposePort{
					{Port: 8080, Protocol: "sctp"},
				},
			},
			wantErr: true,
		},
		{
			name: "valid args",
			instructions: &DockerInstructions{
				Args: []ArgDefinition{
					{Name: "NODE_VERSION", Default: "20"},
					{Name: "BUILD_DATE"},
				},
			},
			wantErr: false,
		},
		{
			name: "arg missing name",
			instructions: &DockerInstructions{
				Args: []ArgDefinition{
					{Name: "", Default: "value"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid arg name",
			instructions: &DockerInstructions{
				Args: []ArgDefinition{
					{Name: "invalid-name-with-dash"},
				},
			},
			wantErr: true,
		},
		{
			name: "valid workdir",
			instructions: &DockerInstructions{
				Workdir: "/app",
			},
			wantErr: false,
		},
		{
			name: "invalid workdir not absolute",
			instructions: &DockerInstructions{
				Workdir: "app",
			},
			wantErr: true,
		},
		{
			name: "valid healthcheck",
			instructions: &DockerInstructions{
				Healthcheck: &HealthcheckConfig{
					Cmd:      []string{"curl", "-f", "http://localhost:8080/health"},
					Interval: "30s",
					Timeout:  "10s",
					Retries:  3,
				},
			},
			wantErr: false,
		},
		{
			name: "healthcheck missing cmd",
			instructions: &DockerInstructions{
				Healthcheck: &HealthcheckConfig{
					Interval: "30s",
				},
			},
			wantErr: true,
		},
		{
			name: "healthcheck invalid interval",
			instructions: &DockerInstructions{
				Healthcheck: &HealthcheckConfig{
					Cmd:      []string{"curl", "-f", "http://localhost:8080/health"},
					Interval: "invalid",
				},
			},
			wantErr: true,
		},
		{
			name: "valid user_run with cmd",
			instructions: &DockerInstructions{
				UserRun: []RunInstruction{
					{Cmd: "npm install"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid user_run with os-specific",
			instructions: &DockerInstructions{
				UserRun: []RunInstruction{
					{Alpine: "apk add --no-cache python3", Debian: "apt-get install -y python3"},
				},
			},
			wantErr: false,
		},
		{
			name: "user_run missing all commands",
			instructions: &DockerInstructions{
				UserRun: []RunInstruction{
					{},
				},
			},
			wantErr: true,
		},
		{
			name: "user_run both cmd and os-specific",
			instructions: &DockerInstructions{
				UserRun: []RunInstruction{
					{Cmd: "echo hello", Alpine: "echo alpine"},
				},
			},
			wantErr: true,
		},
		{
			name: "valid env",
			instructions: &DockerInstructions{
				Env: map[string]string{
					"NODE_ENV": "production",
					"DEBUG":    "false",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid env name",
			instructions: &DockerInstructions{
				Env: map[string]string{
					"INVALID NAME": "value",
				},
			},
			wantErr: true,
		},
		{
			name: "valid labels",
			instructions: &DockerInstructions{
				Labels: map[string]string{
					"org.opencontainers.image.source": "https://github.com/example/repo",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid label key",
			instructions: &DockerInstructions{
				Labels: map[string]string{
					"invalid key": "value",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Version = "1"
			cfg.Project = "test-project"
			cfg.Build.Instructions = tt.instructions

			err := validator.Validate(cfg)
			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatorValidInject(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claucker-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	validator := NewValidator(tmpDir)

	tests := []struct {
		name    string
		inject  *InjectConfig
		wantErr bool
	}{
		{
			name:    "nil inject is valid",
			inject:  nil,
			wantErr: false,
		},
		{
			name: "valid RUN instruction",
			inject: &InjectConfig{
				AfterPackages: []string{"RUN echo hello"},
			},
			wantErr: false,
		},
		{
			name: "valid COPY instruction",
			inject: &InjectConfig{
				AfterUserSetup: []string{"COPY myfile /app/myfile"},
			},
			wantErr: false,
		},
		{
			name: "valid ENV instruction",
			inject: &InjectConfig{
				AfterFrom: []string{"ENV MY_VAR=value"},
			},
			wantErr: false,
		},
		{
			name: "valid ARG instruction",
			inject: &InjectConfig{
				AfterFrom: []string{"ARG BUILD_VERSION"},
			},
			wantErr: false,
		},
		{
			name: "valid comment",
			inject: &InjectConfig{
				AfterPackages: []string{"# This is a comment"},
			},
			wantErr: false,
		},
		{
			name: "multiple valid instructions",
			inject: &InjectConfig{
				AfterPackages: []string{
					"RUN apt-get update",
					"RUN apt-get install -y python3",
					"ENV PYTHON_VERSION=3.11",
				},
			},
			wantErr: false,
		},
		{
			name: "empty instruction",
			inject: &InjectConfig{
				AfterPackages: []string{""},
			},
			wantErr: true,
		},
		{
			name: "whitespace only instruction",
			inject: &InjectConfig{
				AfterPackages: []string{"   "},
			},
			wantErr: true,
		},
		{
			name: "invalid instruction prefix",
			inject: &InjectConfig{
				AfterPackages: []string{"INVALID hello world"},
			},
			wantErr: true,
		},
		{
			name: "plain text is invalid",
			inject: &InjectConfig{
				AfterPackages: []string{"just some plain text"},
			},
			wantErr: true,
		},
		{
			name: "all injection points valid",
			inject: &InjectConfig{
				AfterFrom:          []string{"ARG CUSTOM_ARG"},
				AfterPackages:      []string{"RUN echo packages done"},
				AfterUserSetup:     []string{"RUN mkdir -p /custom"},
				AfterUserSwitch:    []string{"RUN echo user switch done"},
				AfterClaudeInstall: []string{"RUN echo claude installed"},
				BeforeEntrypoint:   []string{"ENV READY=true"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Version = "1"
			cfg.Project = "test-project"
			cfg.Build.Inject = tt.inject

			err := validator.Validate(cfg)
			hasErr := err != nil
			if hasErr != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatorHelperFunctions(t *testing.T) {
	t.Run("isValidChown", func(t *testing.T) {
		tests := []struct {
			input string
			valid bool
		}{
			{"claude", true},
			{"claude:claude", true},
			{"1001", true},
			{"1001:1001", true},
			{"user_name", true},
			{"user-name", true},
			{"user name", false},
			{"user:group:extra", false},
			{"", false},
		}
		for _, tt := range tests {
			if got := isValidChown(tt.input); got != tt.valid {
				t.Errorf("isValidChown(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		}
	})

	t.Run("isValidChmod", func(t *testing.T) {
		tests := []struct {
			input string
			valid bool
		}{
			{"755", true},
			{"644", true},
			{"0755", true},
			{"0644", true},
			{"777", true},
			{"999", false},
			{"abc", false},
			{"", false},
		}
		for _, tt := range tests {
			if got := isValidChmod(tt.input); got != tt.valid {
				t.Errorf("isValidChmod(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		}
	})

	t.Run("isValidIdentifier", func(t *testing.T) {
		tests := []struct {
			input string
			valid bool
		}{
			{"NODE_VERSION", true},
			{"_private", true},
			{"build123", true},
			{"BUILD_DATE", true},
			{"invalid-name", false},
			{"123start", false},
			{"has space", false},
			{"", false},
		}
		for _, tt := range tests {
			if got := isValidIdentifier(tt.input); got != tt.valid {
				t.Errorf("isValidIdentifier(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		}
	})

	t.Run("isValidDuration", func(t *testing.T) {
		tests := []struct {
			input string
			valid bool
		}{
			{"30s", true},
			{"1m", true},
			{"2h", true},
			{"500ms", true},
			{"1.5s", true},
			{"100ns", true},
			{"30", false},
			{"s30", false},
			{"", false},
		}
		for _, tt := range tests {
			if got := isValidDuration(tt.input); got != tt.valid {
				t.Errorf("isValidDuration(%q) = %v, want %v", tt.input, got, tt.valid)
			}
		}
	})
}

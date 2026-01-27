package cmdutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/spf13/cobra"
)

func TestIsProjectDir(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "clawker-project-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	projectDir := filepath.Join(tmpDir, "myproject")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	nonProjectDir := filepath.Join(tmpDir, "nonproject")
	if err := os.MkdirAll(nonProjectDir, 0755); err != nil {
		t.Fatalf("failed to create non-project dir: %v", err)
	}

	tests := []struct {
		name     string
		dir      string
		settings *config.Settings
		setup    func()
		want     bool
	}{
		{
			name:     "returns true when clawker.yaml exists",
			dir:      projectDir,
			settings: nil,
			setup: func() {
				configPath := filepath.Join(projectDir, config.ConfigFileName)
				os.WriteFile(configPath, []byte("version: '1'"), 0644)
			},
			want: true,
		},
		{
			name:     "returns false without clawker.yaml and nil settings",
			dir:      nonProjectDir,
			settings: nil,
			setup:    func() {},
			want:     false,
		},
		{
			name: "returns true when registered in settings",
			dir:  nonProjectDir,
			settings: &config.Settings{
				Projects: []string{nonProjectDir},
			},
			setup: func() {},
			want:  true,
		},
		{
			name: "returns false when not registered in settings",
			dir:  nonProjectDir,
			settings: &config.Settings{
				Projects: []string{"/some/other/path"},
			},
			setup: func() {},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up before each test
			os.Remove(filepath.Join(projectDir, config.ConfigFileName))
			tt.setup()

			got := IsProjectDir(tt.dir, tt.settings)
			if got != tt.want {
				t.Errorf("IsProjectDir() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindProjectRoot(t *testing.T) {
	// Create temp directory structure
	tmpDir, err := os.MkdirTemp("", "clawker-project-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create nested structure: tmpDir/project/sub/deep
	projectDir := filepath.Join(tmpDir, "project")
	subDir := filepath.Join(projectDir, "sub")
	deepDir := filepath.Join(subDir, "deep")
	if err := os.MkdirAll(deepDir, 0755); err != nil {
		t.Fatalf("failed to create directory structure: %v", err)
	}

	// Create clawker.yaml in project directory
	configPath := filepath.Join(projectDir, config.ConfigFileName)
	if err := os.WriteFile(configPath, []byte("version: '1'"), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	tests := []struct {
		name     string
		dir      string
		settings *config.Settings
		want     string
	}{
		{
			name:     "finds root from exact directory",
			dir:      projectDir,
			settings: nil,
			want:     projectDir,
		},
		{
			name:     "finds root from subdirectory",
			dir:      subDir,
			settings: nil,
			want:     projectDir,
		},
		{
			name:     "finds root from deeply nested directory",
			dir:      deepDir,
			settings: nil,
			want:     projectDir,
		},
		{
			name:     "returns empty when no root found",
			dir:      tmpDir,
			settings: nil,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindProjectRoot(tt.dir, tt.settings)
			if got != tt.want {
				t.Errorf("FindProjectRoot() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindProjectRoot_RegisteredProject(t *testing.T) {
	// Test finding root via registered project (no clawker.yaml)
	tmpDir, err := os.MkdirTemp("", "clawker-project-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	registeredDir := filepath.Join(tmpDir, "registered")
	subDir := filepath.Join(registeredDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create directory structure: %v", err)
	}

	settings := &config.Settings{
		Projects: []string{registeredDir},
	}

	// From subdirectory, should find registered parent
	got := FindProjectRoot(subDir, settings)
	if got != registeredDir {
		t.Errorf("FindProjectRoot() = %q, want %q", got, registeredDir)
	}
}

func TestIsChildOfProject(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		settings *config.Settings
		want     string
	}{
		{
			name:     "returns empty with nil settings",
			dir:      "/some/path",
			settings: nil,
			want:     "",
		},
		{
			name: "returns project root for exact match",
			dir:  "/projects/myapp",
			settings: &config.Settings{
				Projects: []string{"/projects/myapp"},
			},
			want: "/projects/myapp",
		},
		{
			name: "returns project root for child path",
			dir:  "/projects/myapp/src/components",
			settings: &config.Settings{
				Projects: []string{"/projects/myapp"},
			},
			want: "/projects/myapp",
		},
		{
			name: "returns empty when not child of any project",
			dir:  "/other/path",
			settings: &config.Settings{
				Projects: []string{"/projects/myapp", "/projects/otherapp"},
			},
			want: "",
		},
		{
			name: "returns empty for partial name match (not child)",
			dir:  "/projects/myapp-v2",
			settings: &config.Settings{
				Projects: []string{"/projects/myapp"},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsChildOfProject(tt.dir, tt.settings)
			if got != tt.want {
				t.Errorf("IsChildOfProject() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsChildOfProject_RealPaths(t *testing.T) {
	// Test with actual filesystem paths for path normalization
	tmpDir, err := os.MkdirTemp("", "clawker-project-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	projectDir := filepath.Join(tmpDir, "project")
	childDir := filepath.Join(projectDir, "child")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatalf("failed to create directory structure: %v", err)
	}

	settings := &config.Settings{
		Projects: []string{projectDir},
	}

	// Test with actual paths
	got := IsChildOfProject(childDir, settings)
	if got != projectDir {
		t.Errorf("IsChildOfProject() = %q, want %q", got, projectDir)
	}
}

func TestCommandRequiresProject(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		want        bool
	}{
		{
			name:        "annotation set to true",
			annotations: map[string]string{AnnotationRequiresProject: "true"},
			want:        true,
		},
		{
			name:        "annotation set to false",
			annotations: map[string]string{AnnotationRequiresProject: "false"},
			want:        false,
		},
		{
			name:        "annotation set to empty string",
			annotations: map[string]string{AnnotationRequiresProject: ""},
			want:        false,
		},
		{
			name:        "annotation key present with wrong value",
			annotations: map[string]string{AnnotationRequiresProject: "yes"},
			want:        false,
		},
		{
			name:        "no annotation present",
			annotations: map[string]string{"other.key": "value"},
			want:        false,
		},
		{
			name:        "nil annotations map",
			annotations: nil,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{
				Use:         "test",
				Annotations: tt.annotations,
			}
			got := CommandRequiresProject(cmd)
			if got != tt.want {
				t.Errorf("CommandRequiresProject() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckProjectContext(t *testing.T) {
	// Create temp directories for testing
	tmpDir, err := os.MkdirTemp("", "clawker-project-context-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a directory with clawker.yaml (project dir)
	projectDir := filepath.Join(tmpDir, "myproject")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}
	configPath := filepath.Join(projectDir, config.ConfigFileName)
	if err := os.WriteFile(configPath, []byte("version: '1'"), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Create a directory without clawker.yaml (non-project dir)
	nonProjectDir := filepath.Join(tmpDir, "nonproject")
	if err := os.MkdirAll(nonProjectDir, 0755); err != nil {
		t.Fatalf("failed to create non-project dir: %v", err)
	}

	tests := []struct {
		name      string
		workDir   string
		input     string // stdin input for confirmation prompt
		wantErr   error
		wantAbort bool
	}{
		{
			name:      "inside project dir - no prompt needed",
			workDir:   projectDir,
			input:     "", // no input needed
			wantErr:   nil,
			wantAbort: false,
		},
		{
			name:      "outside project - user confirms",
			workDir:   nonProjectDir,
			input:     "y\n",
			wantErr:   nil,
			wantAbort: false,
		},
		{
			name:      "outside project - user declines",
			workDir:   nonProjectDir,
			input:     "n\n",
			wantErr:   ErrAborted,
			wantAbort: true,
		},
		{
			name:      "outside project - EOF (no input)",
			workDir:   nonProjectDir,
			input:     "",
			wantErr:   ErrAborted,
			wantAbort: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a factory with the test workdir and IOStreams
			ios := iostreams.NewTestIOStreams()
			f := &Factory{
				WorkDir:   tt.workDir,
				IOStreams: ios.IOStreams,
			}

			// Create a command with stdin set to our test input
			cmd := &cobra.Command{Use: "testcmd"}
			cmd.SetIn(strings.NewReader(tt.input))

			err := CheckProjectContext(cmd, f)

			if tt.wantAbort {
				if err != ErrAborted {
					t.Errorf("CheckProjectContext() error = %v, want ErrAborted", err)
				}
			} else {
				if err != nil {
					t.Errorf("CheckProjectContext() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestConfirmExternalProjectOperation(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		projectPath string
		operation   string
		want        bool
	}{
		{
			name:        "user confirms with y",
			input:       "y\n",
			projectPath: "/some/path",
			operation:   "stop",
			want:        true,
		},
		{
			name:        "user confirms with Y",
			input:       "Y\n",
			projectPath: "/some/path",
			operation:   "stop",
			want:        true,
		},
		{
			name:        "user declines with n",
			input:       "n\n",
			projectPath: "/some/path",
			operation:   "stop",
			want:        false,
		},
		{
			name:        "user declines with N",
			input:       "N\n",
			projectPath: "/some/path",
			operation:   "stop",
			want:        false,
		},
		{
			name:        "EOF treated as decline",
			input:       "",
			projectPath: "/some/path",
			operation:   "stop",
			want:        false,
		},
		{
			name:        "empty input treated as decline",
			input:       "\n",
			projectPath: "/some/path",
			operation:   "stop",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios := iostreams.NewTestIOStreams()
			reader := strings.NewReader(tt.input)
			got := ConfirmExternalProjectOperation(ios.IOStreams, reader, tt.projectPath, tt.operation)
			if got != tt.want {
				t.Errorf("ConfirmExternalProjectOperation() = %v, want %v", got, tt.want)
			}
		})
	}
}

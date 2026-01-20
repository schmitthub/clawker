package init

import (
	"bytes"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
)

func TestNewCmdProjectInit(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdProjectInit(f)

	// Check command use
	if cmd.Use != "init [project-name]" {
		t.Errorf("expected Use 'init [project-name]', got '%s'", cmd.Use)
	}

	// Check force flag exists
	forceFlag := cmd.Flags().Lookup("force")
	if forceFlag == nil {
		t.Error("expected --force flag to exist")
	}
	if forceFlag.Shorthand != "f" {
		t.Errorf("expected --force shorthand 'f', got '%s'", forceFlag.Shorthand)
	}
	if forceFlag.DefValue != "false" {
		t.Errorf("expected --force default 'false', got '%s'", forceFlag.DefValue)
	}

	// Check yes flag exists
	yesFlag := cmd.Flags().Lookup("yes")
	if yesFlag == nil {
		t.Error("expected --yes flag to exist")
	}
	if yesFlag.Shorthand != "y" {
		t.Errorf("expected --yes shorthand 'y', got '%s'", yesFlag.Shorthand)
	}
	if yesFlag.DefValue != "false" {
		t.Errorf("expected --yes default 'false', got '%s'", yesFlag.DefValue)
	}

	// Check that at most 1 arg is accepted
	if cmd.Args == nil {
		t.Error("expected Args to be set")
	}
}

func TestNewCmdProjectInit_FlagParsing(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantForce bool
		wantYes   bool
		wantErr   bool
	}{
		{
			name:      "no flags",
			args:      []string{},
			wantForce: false,
			wantYes:   false,
			wantErr:   false,
		},
		{
			name:      "force flag",
			args:      []string{"--force"},
			wantForce: true,
			wantYes:   false,
			wantErr:   false,
		},
		{
			name:      "force shorthand",
			args:      []string{"-f"},
			wantForce: true,
			wantYes:   false,
			wantErr:   false,
		},
		{
			name:      "yes flag",
			args:      []string{"--yes"},
			wantForce: false,
			wantYes:   true,
			wantErr:   false,
		},
		{
			name:      "yes shorthand",
			args:      []string{"-y"},
			wantForce: false,
			wantYes:   true,
			wantErr:   false,
		},
		{
			name:      "both flags",
			args:      []string{"--force", "--yes"},
			wantForce: true,
			wantYes:   true,
			wantErr:   false,
		},
		{
			name:      "with project name",
			args:      []string{"my-project"},
			wantForce: false,
			wantYes:   false,
			wantErr:   false,
		},
		{
			name:      "with project name and flags",
			args:      []string{"my-project", "-f", "-y"},
			wantForce: true,
			wantYes:   true,
			wantErr:   false,
		},
		{
			name:      "too many args",
			args:      []string{"project1", "project2"},
			wantForce: false,
			wantYes:   false,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmdutil.New("1.0.0", "abc123")

			var capturedOpts *ProjectInitOptions
			cmd := NewCmdProjectInit(f)
			// Replace RunE to capture options
			originalRunE := cmd.RunE
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				// Parse flags manually to capture options
				forceVal, _ := cmd.Flags().GetBool("force")
				yesVal, _ := cmd.Flags().GetBool("yes")
				capturedOpts = &ProjectInitOptions{
					Force: forceVal,
					Yes:   yesVal,
				}
				// Don't actually run the command
				return nil
			}
			_ = originalRunE // silence unused warning

			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			err := cmd.Execute()

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if capturedOpts == nil {
				t.Fatal("options not captured")
			}

			if capturedOpts.Force != tt.wantForce {
				t.Errorf("Force = %v, want %v", capturedOpts.Force, tt.wantForce)
			}
			if capturedOpts.Yes != tt.wantYes {
				t.Errorf("Yes = %v, want %v", capturedOpts.Yes, tt.wantYes)
			}
		})
	}
}

func TestGenerateConfigYAML(t *testing.T) {
	tests := []struct {
		name          string
		projectName   string
		buildImage    string
		defaultImage  string
		workspaceMode string
		wantContains  []string
		wantNotContain []string
	}{
		{
			name:          "basic config",
			projectName:   "my-app",
			buildImage:    "buildpack-deps:bookworm-scm",
			defaultImage:  "",
			workspaceMode: "bind",
			wantContains: []string{
				`project: "my-app"`,
				`image: "buildpack-deps:bookworm-scm"`,
				`default_mode: "bind"`,
				`version: "1"`,
				`enable_firewall: true`,
				`docker_socket: false`,
			},
			wantNotContain: []string{
				"default_image:",
			},
		},
		{
			name:          "with default image",
			projectName:   "test-project",
			buildImage:    "alpine:latest",
			defaultImage:  "clawker-default:latest",
			workspaceMode: "snapshot",
			wantContains: []string{
				`project: "test-project"`,
				`image: "alpine:latest"`,
				`default_image: "clawker-default:latest"`,
				`default_mode: "snapshot"`,
			},
			wantNotContain: []string{},
		},
		{
			name:          "includes standard packages",
			projectName:   "pkg-test",
			buildImage:    "debian:latest",
			defaultImage:  "",
			workspaceMode: "bind",
			wantContains: []string{
				"- git",
				"- curl",
				"- ripgrep",
			},
			wantNotContain: []string{},
		},
		{
			name:          "includes commented sections",
			projectName:   "comments-test",
			buildImage:    "debian:latest",
			defaultImage:  "",
			workspaceMode: "bind",
			wantContains: []string{
				"# copy:",
				"# root_run:",
				"# user_run:",
				"# shell:",
				"# editor:",
			},
			wantNotContain: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateConfigYAML(tt.projectName, tt.buildImage, tt.defaultImage, tt.workspaceMode)

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("generateConfigYAML() missing expected content %q\nGot:\n%s", want, result)
				}
			}

			for _, notWant := range tt.wantNotContain {
				if strings.Contains(result, notWant) {
					t.Errorf("generateConfigYAML() contains unexpected content %q\nGot:\n%s", notWant, result)
				}
			}
		})
	}
}

func TestGenerateConfigYAML_ValidYAML(t *testing.T) {
	// Test that generated YAML is valid by checking basic structure
	result := generateConfigYAML("test", "debian:latest", "default:latest", "bind")

	// Check it starts with version
	if !strings.HasPrefix(result, `version: "1"`) {
		t.Errorf("generateConfigYAML() should start with version, got:\n%s", result[:50])
	}

	// Check it has proper sections
	sections := []string{"build:", "agent:", "workspace:", "security:"}
	for _, section := range sections {
		if !strings.Contains(result, section) {
			t.Errorf("generateConfigYAML() missing section %q", section)
		}
	}
}

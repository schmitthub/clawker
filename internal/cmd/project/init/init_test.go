package init

import (
	"context"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
)

func TestNewCmdProjectInit(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *ProjectInitOptions
	cmd := NewCmdProjectInit(f, func(_ context.Context, opts *ProjectInitOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotOpts == nil {
		t.Fatal("expected runF to be called")
	}

	if gotOpts.IOStreams != tio.IOStreams {
		t.Error("expected IOStreams to be set from factory")
	}

	if gotOpts.Force {
		t.Error("expected Force to be false by default")
	}
	if gotOpts.Yes {
		t.Error("expected Yes to be false by default")
	}
	if gotOpts.Name != "" {
		t.Errorf("expected Name to be empty, got %q", gotOpts.Name)
	}
}

func TestNewCmdProjectInit_FlagParsing(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantName  string
		wantForce bool
		wantYes   bool
		wantErr   bool
	}{
		{
			name:      "no flags",
			args:      []string{},
			wantForce: false,
			wantYes:   false,
		},
		{
			name:      "force flag",
			args:      []string{"--force"},
			wantForce: true,
		},
		{
			name:      "force shorthand",
			args:      []string{"-f"},
			wantForce: true,
		},
		{
			name:    "yes flag",
			args:    []string{"--yes"},
			wantYes: true,
		},
		{
			name:    "yes shorthand",
			args:    []string{"-y"},
			wantYes: true,
		},
		{
			name:      "both flags",
			args:      []string{"--force", "--yes"},
			wantForce: true,
			wantYes:   true,
		},
		{
			name:     "with project name",
			args:     []string{"my-project"},
			wantName: "my-project",
		},
		{
			name:      "with project name and flags",
			args:      []string{"my-project", "-f", "-y"},
			wantName:  "my-project",
			wantForce: true,
			wantYes:   true,
		},
		{
			name:    "too many args",
			args:    []string{"project1", "project2"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreamstest.New()
			f := &cmdutil.Factory{IOStreams: tio.IOStreams}

			var gotOpts *ProjectInitOptions
			cmd := NewCmdProjectInit(f, func(_ context.Context, opts *ProjectInitOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if gotOpts == nil {
				t.Fatal("expected runF to be called")
			}

			if gotOpts.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", gotOpts.Name, tt.wantName)
			}
			if gotOpts.Force != tt.wantForce {
				t.Errorf("Force = %v, want %v", gotOpts.Force, tt.wantForce)
			}
			if gotOpts.Yes != tt.wantYes {
				t.Errorf("Yes = %v, want %v", gotOpts.Yes, tt.wantYes)
			}
		})
	}
}

func TestGenerateConfigYAML(t *testing.T) {
	tests := []struct {
		name           string
		buildImage     string
		defaultImage   string
		workspaceMode  string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:          "basic config",
			buildImage:    "buildpack-deps:bookworm-scm",
			defaultImage:  "",
			workspaceMode: "bind",
			wantContains: []string{
				`image: "buildpack-deps:bookworm-scm"`,
				`default_mode: "bind"`,
				`version: "1"`,
				`enable_firewall: true`,
				`docker_socket: false`,
			},
			wantNotContain: []string{
				"default_image:",
				"project:",
			},
		},
		{
			name:          "with default image",
			buildImage:    "alpine:latest",
			defaultImage:  "clawker-default:latest",
			workspaceMode: "snapshot",
			wantContains: []string{
				`image: "alpine:latest"`,
				`default_image: "clawker-default:latest"`,
				`default_mode: "snapshot"`,
			},
			wantNotContain: []string{
				"project:",
			},
		},
		{
			name:          "includes standard packages",
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
			result := generateConfigYAML(tt.buildImage, tt.defaultImage, tt.workspaceMode)

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
	result := generateConfigYAML("debian:latest", "default:latest", "bind")

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

package init

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	intbuild "github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	prompterpkg "github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
)

func TestNewCmdProjectInit(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
	}

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

	if gotOpts.TUI == nil {
		t.Error("expected TUI to be wired from factory")
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
			f := &cmdutil.Factory{
				IOStreams: tio.IOStreams,
				TUI:       tui.NewTUI(tio.IOStreams),
			}

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

func TestBuildProjectWizardFields(t *testing.T) {
	wctx := wizardContext{
		configExists:   true,
		force:          false,
		nameDefault:    "my-dir",
		configFileName: ".clawker.yaml",
	}
	fields := buildProjectWizardFields(wctx)

	if len(fields) != 5 {
		t.Fatalf("expected 5 wizard fields, got %d", len(fields))
	}

	// Field 0: overwrite
	if fields[0].ID != "overwrite" {
		t.Errorf("field[0].ID = %q, want %q", fields[0].ID, "overwrite")
	}
	if fields[0].Kind != tui.FieldConfirm {
		t.Errorf("field[0].Kind = %v, want FieldConfirm", fields[0].Kind)
	}
	if fields[0].DefaultYes {
		t.Error("overwrite field should default to No")
	}

	// Field 1: project_name
	if fields[1].ID != "project_name" {
		t.Errorf("field[1].ID = %q, want %q", fields[1].ID, "project_name")
	}
	if fields[1].Kind != tui.FieldText {
		t.Errorf("field[1].Kind = %v, want FieldText", fields[1].Kind)
	}
	if fields[1].Default != "my-dir" {
		t.Errorf("field[1].Default = %q, want %q", fields[1].Default, "my-dir")
	}
	if !fields[1].Required {
		t.Error("project_name should be required")
	}

	// Field 2: flavor
	if fields[2].ID != "flavor" {
		t.Errorf("field[2].ID = %q, want %q", fields[2].ID, "flavor")
	}
	if fields[2].Kind != tui.FieldSelect {
		t.Errorf("field[2].Kind = %v, want FieldSelect", fields[2].Kind)
	}
	flavors := intbuild.DefaultFlavorOptions()
	if len(fields[2].Options) != len(flavors)+1 {
		t.Errorf("flavor options = %d, want %d", len(fields[2].Options), len(flavors)+1)
	}
	lastOpt := fields[2].Options[len(fields[2].Options)-1]
	if lastOpt.Label != "Custom" {
		t.Errorf("last flavor option = %q, want %q", lastOpt.Label, "Custom")
	}

	// Field 3: custom_image
	if fields[3].ID != "custom_image" {
		t.Errorf("field[3].ID = %q, want %q", fields[3].ID, "custom_image")
	}
	if fields[3].Kind != tui.FieldText {
		t.Errorf("field[3].Kind = %v, want FieldText", fields[3].Kind)
	}
	if !fields[3].Required {
		t.Error("custom_image should be required")
	}

	// Field 4: workspace_mode
	if fields[4].ID != "workspace_mode" {
		t.Errorf("field[4].ID = %q, want %q", fields[4].ID, "workspace_mode")
	}
	if fields[4].Kind != tui.FieldSelect {
		t.Errorf("field[4].Kind = %v, want FieldSelect", fields[4].Kind)
	}
	if len(fields[4].Options) != 2 {
		t.Fatalf("workspace_mode options = %d, want 2", len(fields[4].Options))
	}
	if fields[4].Options[0].Label != "bind" {
		t.Errorf("workspace_mode option[0] = %q, want %q", fields[4].Options[0].Label, "bind")
	}
	if fields[4].Options[1].Label != "snapshot" {
		t.Errorf("workspace_mode option[1] = %q, want %q", fields[4].Options[1].Label, "snapshot")
	}
}

func TestBuildProjectWizardFields_NoExistingConfig(t *testing.T) {
	wctx := wizardContext{
		configExists:   false,
		force:          false,
		nameDefault:    "my-dir",
		configFileName: ".clawker.yaml",
	}
	fields := buildProjectWizardFields(wctx)

	// Overwrite field should be skipped when config doesn't exist
	if fields[0].SkipIf == nil {
		t.Fatal("overwrite field must have SkipIf")
	}
	if !fields[0].SkipIf(tui.WizardValues{}) {
		t.Error("overwrite field should be skipped when configExists=false")
	}

	// Other fields should NOT be skipped (no "overwrite" key in values means it was skipped)
	if fields[1].SkipIf(tui.WizardValues{}) {
		t.Error("project_name should not be skipped when overwrite was skipped")
	}
	if fields[2].SkipIf(tui.WizardValues{}) {
		t.Error("flavor should not be skipped when overwrite was skipped")
	}
	if fields[4].SkipIf(tui.WizardValues{}) {
		t.Error("workspace_mode should not be skipped when overwrite was skipped")
	}
}

func TestBuildProjectWizardFields_OverwriteDeclined(t *testing.T) {
	wctx := wizardContext{
		configExists:   true,
		force:          false,
		nameDefault:    "my-dir",
		configFileName: ".clawker.yaml",
	}
	fields := buildProjectWizardFields(wctx)

	// When overwrite is "no", all setup fields should be skipped
	vals := tui.WizardValues{"overwrite": "no"}
	if fields[1].SkipIf(vals) != true {
		t.Error("project_name should be skipped when overwrite=no")
	}
	if fields[2].SkipIf(vals) != true {
		t.Error("flavor should be skipped when overwrite=no")
	}
	if fields[3].SkipIf(vals) != true {
		t.Error("custom_image should be skipped when overwrite=no")
	}
	if fields[4].SkipIf(vals) != true {
		t.Error("workspace_mode should be skipped when overwrite=no")
	}
}

func TestBuildProjectWizardFields_ForceSkipsOverwrite(t *testing.T) {
	wctx := wizardContext{
		configExists:   true,
		force:          true,
		nameDefault:    "my-dir",
		configFileName: ".clawker.yaml",
	}
	fields := buildProjectWizardFields(wctx)

	if !fields[0].SkipIf(tui.WizardValues{}) {
		t.Error("overwrite field should be skipped when force=true")
	}
}

func TestBuildProjectWizardFields_CustomImageSkipIf(t *testing.T) {
	wctx := wizardContext{
		configExists:   false,
		nameDefault:    "my-dir",
		configFileName: ".clawker.yaml",
	}
	fields := buildProjectWizardFields(wctx)

	// custom_image skipped when flavor is not "Custom"
	if !fields[3].SkipIf(tui.WizardValues{"flavor": "bookworm"}) {
		t.Error("custom_image should be skipped when flavor != Custom")
	}
	if fields[3].SkipIf(tui.WizardValues{"flavor": "Custom"}) {
		t.Error("custom_image should NOT be skipped when flavor == Custom")
	}
}

func TestFlavorFieldOptionsWithCustom(t *testing.T) {
	options := flavorFieldOptionsWithCustom()
	flavors := intbuild.DefaultFlavorOptions()

	if len(options) != len(flavors)+1 {
		t.Fatalf("expected %d options, got %d", len(flavors)+1, len(options))
	}

	for i, opt := range options[:len(flavors)] {
		if opt.Label != flavors[i].Name {
			t.Errorf("option[%d].Label = %q, want %q", i, opt.Label, flavors[i].Name)
		}
		if opt.Description != flavors[i].Description {
			t.Errorf("option[%d].Description = %q, want %q", i, opt.Description, flavors[i].Description)
		}
	}

	last := options[len(options)-1]
	if last.Label != "Custom" {
		t.Errorf("last option label = %q, want %q", last.Label, "Custom")
	}
}

func TestResolveImageFromWizard(t *testing.T) {
	tests := []struct {
		name   string
		values tui.WizardValues
		want   string
	}{
		{
			name:   "known flavor",
			values: tui.WizardValues{"flavor": "bookworm"},
			want:   intbuild.FlavorToImage("bookworm"),
		},
		{
			name:   "custom image",
			values: tui.WizardValues{"flavor": "Custom", "custom_image": "node:20"},
			want:   "node:20",
		},
		{
			name:   "alpine flavor",
			values: tui.WizardValues{"flavor": "alpine3.22"},
			want:   intbuild.FlavorToImage("alpine3.22"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveImageFromWizard(tt.values)
			if got != tt.want {
				t.Errorf("resolveImageFromWizard() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPerformProjectSetup(t *testing.T) {
	// Chdir into temp dir, then resolve via Getwd so the path matches
	// what performProjectSetup sees (macOS: /var → /private/var).
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	require.NoError(t, os.Chdir(t.TempDir()))
	tmpDir, err := os.Getwd()
	require.NoError(t, err)

	tio := iostreamstest.New()
	cfg := configmocks.NewIsolatedTestConfig(t)
	mockPM := projectmocks.NewMockProjectManager()

	var registeredName, registeredPath string
	mockPM.RegisterFunc = func(_ context.Context, name string, repoPath string) (project.Project, error) {
		registeredName = name
		registeredPath = repoPath
		return projectmocks.NewMockProject(name, repoPath), nil
	}

	opts := &ProjectInitOptions{
		IOStreams:      tio.IOStreams,
		Config:         func() (config.Config, error) { return cfg, nil },
		ProjectManager: func() (project.ProjectManager, error) { return mockPM, nil },
		Prompter:       func() *prompterpkg.Prompter { return nil }, // not called in non-interactive setup
		Yes:            true,                                        // prevent maybeOfferUserDefault from calling prompter
	}

	err = performProjectSetup(context.Background(), opts, "test-project", "debian:latest", "snapshot")
	if err != nil {
		t.Fatalf("performProjectSetup() error: %v", err)
	}

	// Verify config file was created
	configPath := filepath.Join(tmpDir, "."+cfg.ProjectConfigFileName())
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	if !strings.Contains(string(content), `image: "debian:latest"`) {
		t.Error("config file missing build image")
	}
	if !strings.Contains(string(content), `default_mode: "snapshot"`) {
		t.Error("config file missing workspace mode")
	}

	// Verify ignore file was created
	ignorePath := filepath.Join(tmpDir, cfg.ClawkerIgnoreName())
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
		t.Error("ignore file not created")
	}

	// Verify project was registered
	if registeredName != "test-project" {
		t.Errorf("registered name = %q, want %q", registeredName, "test-project")
	}
	if registeredPath != tmpDir {
		t.Errorf("registered path = %q, want %q", registeredPath, tmpDir)
	}

	// Verify output
	out := tio.OutBuf.String()
	if !strings.Contains(out, "Created:") {
		t.Error("expected success output with 'Created:'")
	}
	if !strings.Contains(out, "test-project") {
		t.Error("expected project name in output")
	}
	if !strings.Contains(out, "Next Steps:") {
		t.Error("expected next steps in output")
	}
}

func TestScaffoldProjectConfig(t *testing.T) {
	tests := []struct {
		name           string
		buildImage     string
		workspaceMode  string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:          "basic config",
			buildImage:    "buildpack-deps:bookworm-scm",
			workspaceMode: "bind",
			wantContains: []string{
				`image: "buildpack-deps:bookworm-scm"`,
				`default_mode: "bind"`,
				"enable: true", // nested firewall.enable
				"docker_socket: false",
			},
			wantNotContain: []string{
				"enable_firewall:", // old flat form should NOT appear
				"default_image:",
				"version:",
				"project:",
			},
		},
		{
			name:          "snapshot mode",
			buildImage:    "alpine:latest",
			workspaceMode: "snapshot",
			wantContains: []string{
				`image: "alpine:latest"`,
				`default_mode: "snapshot"`,
			},
			wantNotContain: []string{
				"default_image:",
				"version:",
			},
		},
		{
			name:          "includes standard packages",
			buildImage:    "debian:latest",
			workspaceMode: "bind",
			wantContains: []string{
				"- git",
				"- curl",
				"- ripgrep",
			},
		},
		{
			name:          "uses nested firewall schema",
			buildImage:    "debian:latest",
			workspaceMode: "bind",
			wantContains: []string{
				"firewall:",
				"enable: true",
			},
			wantNotContain: []string{
				"enable_firewall:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scaffoldProjectConfig(tt.buildImage, tt.workspaceMode)

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("scaffoldProjectConfig() missing expected content %q\nGot:\n%s", want, result)
				}
			}

			for _, notWant := range tt.wantNotContain {
				if strings.Contains(result, notWant) {
					t.Errorf("scaffoldProjectConfig() contains unexpected content %q\nGot:\n%s", notWant, result)
				}
			}
		})
	}
}

func TestScaffoldProjectConfig_ValidStructure(t *testing.T) {
	result := scaffoldProjectConfig("debian:latest", "bind")

	// Check it starts with the comment header from DefaultConfigYAML
	if !strings.HasPrefix(result, "# Clawker") {
		t.Errorf("scaffoldProjectConfig() should start with '# Clawker', got:\n%s", result[:50])
	}

	// Check it has proper sections
	sections := []string{"build:", "agent:", "workspace:", "security:"}
	for _, section := range sections {
		if !strings.Contains(result, section) {
			t.Errorf("scaffoldProjectConfig() missing section %q", section)
		}
	}
}

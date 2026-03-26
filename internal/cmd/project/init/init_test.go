package init

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/schmitthub/clawker/internal/storage"
	"github.com/schmitthub/clawker/internal/tui"
)

// sanitizeTestName produces a valid project name from an arbitrary preset name.
func sanitizeTestName(name string) string {
	s := strings.ToLower(name)
	s = strings.NewReplacer("/", "-", "+", "", "#", "", " ", "-", ".", "").Replace(s)
	return "test-" + s
}

// chdirTemp changes to a temp directory and restores the original on cleanup.
func chdirTemp(t *testing.T) string {
	t.Helper()
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	require.NoError(t, os.Chdir(t.TempDir()))
	wd, err := os.Getwd()
	require.NoError(t, err)
	return wd
}

func TestNewCmdProjectInit(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
	}

	var gotOpts *ProjectInitOptions
	cmd := NewCmdProjectInit(f, func(_ context.Context, opts *ProjectInitOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts, "expected runF to be called")
	assert.Equal(t, tio, gotOpts.IOStreams)
	assert.NotNil(t, gotOpts.TUI)
	assert.False(t, gotOpts.Force)
	assert.False(t, gotOpts.Yes)
	assert.Empty(t, gotOpts.Name)
}

func TestNewCmdProjectInit_FlagParsing(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantName   string
		wantPreset string
		wantForce  bool
		wantYes    bool
		wantErr    bool
	}{
		{name: "no flags", args: []string{}},
		{name: "force flag", args: []string{"--force"}, wantForce: true},
		{name: "force shorthand", args: []string{"-f"}, wantForce: true},
		{name: "yes flag", args: []string{"--yes"}, wantYes: true},
		{name: "yes shorthand", args: []string{"-y"}, wantYes: true},
		{name: "both flags", args: []string{"--force", "--yes"}, wantForce: true, wantYes: true},
		{name: "with project name", args: []string{"my-project"}, wantName: "my-project"},
		{name: "name and flags", args: []string{"my-project", "-f", "-y"}, wantName: "my-project", wantForce: true, wantYes: true},
		{name: "too many args", args: []string{"project1", "project2"}, wantErr: true},
		{name: "preset with yes", args: []string{"--yes", "--preset", "Go"}, wantYes: true, wantPreset: "Go"},
		{name: "preset name and yes", args: []string{"my-project", "--yes", "--preset", "Python"}, wantName: "my-project", wantYes: true, wantPreset: "Python"},
		{name: "preset without yes", args: []string{"--preset", "Go"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio, _, _, _ := iostreams.Test()
			f := &cmdutil.Factory{
				IOStreams: tio,
				TUI:       tui.NewTUI(tio),
			}

			var gotOpts *ProjectInitOptions
			cmd := NewCmdProjectInit(f, func(_ context.Context, opts *ProjectInitOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			assert.Equal(t, tt.wantName, gotOpts.Name)
			assert.Equal(t, tt.wantPreset, gotOpts.Preset)
			assert.Equal(t, tt.wantForce, gotOpts.Force)
			assert.Equal(t, tt.wantYes, gotOpts.Yes)
		})
	}
}

func TestPresetCompletions(t *testing.T) {
	completions := PresetCompletions()

	// Should have one entry per non-AutoCustomize preset.
	var presetCount int
	for _, p := range config.Presets() {
		if !p.AutoCustomize {
			presetCount++
		}
	}
	assert.Len(t, completions, presetCount)

	// Each completion should be a non-empty string (cobra.Completion is a string type).
	for _, c := range completions {
		assert.NotEmpty(t, string(c))
	}

	// "Build from scratch" (AutoCustomize) should not appear.
	for _, c := range completions {
		assert.NotContains(t, string(c), "Build from scratch")
	}
}

func TestPresetWithoutYes_ReturnsError(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
	}

	cmd := NewCmdProjectInit(f, func(_ context.Context, _ *ProjectInitOptions) error {
		t.Fatal("runF should not be called when --preset is used without --yes")
		return nil
	})

	cmd.SetArgs([]string{"--preset", "Go"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--preset requires --yes")
}

// --- Wizard step definition tests ---

func TestBuildInitWizardSteps(t *testing.T) {
	presets := config.Presets()
	wctx := wizardContext{
		configExists:   true,
		force:          false,
		nameDefault:    "my-dir",
		configFileName: ".clawker.yaml",
		presets:        presets,
	}
	steps := buildInitWizardSteps(wctx)

	require.Len(t, steps, 4, "expected 4 wizard steps: overwrite, project_name, preset, action")

	assert.Equal(t, "overwrite", steps[0].ID)
	assert.Equal(t, "project_name", steps[1].ID)
	assert.Equal(t, "preset", steps[2].ID)
	assert.Equal(t, "action", steps[3].ID)

	// All pages should be non-nil.
	for _, s := range steps {
		assert.NotNil(t, s.Page, "step %q should have a non-nil Page", s.ID)
	}
}

func TestBuildInitWizardSteps_NoExistingConfig(t *testing.T) {
	wctx := wizardContext{
		configExists:   false,
		force:          false,
		nameDefault:    "my-dir",
		configFileName: ".clawker.yaml",
		presets:        config.Presets(),
	}
	steps := buildInitWizardSteps(wctx)

	// Overwrite should be skipped.
	assert.True(t, steps[0].SkipIf(tui.WizardValues{}))

	// Other steps should not be skipped.
	assert.False(t, steps[1].SkipIf(tui.WizardValues{}))
	assert.False(t, steps[2].SkipIf(tui.WizardValues{}))
}

func TestBuildInitWizardSteps_OverwriteDeclined(t *testing.T) {
	wctx := wizardContext{
		configExists:   true,
		force:          false,
		nameDefault:    "my-dir",
		configFileName: ".clawker.yaml",
		presets:        config.Presets(),
	}
	steps := buildInitWizardSteps(wctx)

	vals := tui.WizardValues{"overwrite": "no"}
	assert.True(t, steps[1].SkipIf(vals), "project_name skipped on overwrite=no")
	assert.True(t, steps[2].SkipIf(vals), "preset skipped on overwrite=no")
	assert.True(t, steps[3].SkipIf(vals), "action skipped on overwrite=no")
}

func TestBuildInitWizardSteps_ForceSkipsOverwrite(t *testing.T) {
	wctx := wizardContext{
		configExists:   true,
		force:          true,
		nameDefault:    "my-dir",
		configFileName: ".clawker.yaml",
		presets:        config.Presets(),
	}
	steps := buildInitWizardSteps(wctx)

	assert.True(t, steps[0].SkipIf(tui.WizardValues{}), "overwrite skipped when force=true")
}

func TestBuildInitWizardSteps_ActionSkipForAutoCustomize(t *testing.T) {
	wctx := wizardContext{
		configExists:   false,
		nameDefault:    "my-dir",
		configFileName: ".clawker.yaml",
		presets:        config.Presets(),
	}
	steps := buildInitWizardSteps(wctx)

	// Action should be skipped for "Build from scratch" (AutoCustomize preset).
	vals := tui.WizardValues{"preset": "Build from scratch"}
	assert.True(t, steps[3].SkipIf(vals), "action skipped for AutoCustomize preset")

	// Action should NOT be skipped for normal presets.
	vals = tui.WizardValues{"preset": "Go"}
	assert.False(t, steps[3].SkipIf(vals), "action shown for normal presets")
}

// --- Validation tests ---

func TestValidateProjectName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "valid lowercase", input: "my-project"},
		{name: "valid with dots", input: "my.project"},
		{name: "valid with underscores", input: "my_project"},
		{name: "valid with numbers", input: "project123"},
		{name: "single char", input: "a"},
		{name: "starts with number", input: "1project"},
		{name: "empty", input: "", wantErr: "required"},
		{name: "uppercase", input: "MyProject", wantErr: "lowercase"},
		{name: "mixed case", input: "my-Project", wantErr: "lowercase"},
		{name: "contains space", input: "my project", wantErr: "spaces"},
		{name: "starts with dot", input: ".project", wantErr: "start with"},
		{name: "starts with hyphen", input: "-project", wantErr: "start with"},
		{name: "special chars", input: "my@project", wantErr: "start with"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProjectName(tt.input)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

// --- Preset lookup tests ---

func TestPresetByName(t *testing.T) {
	presets := config.Presets()

	t.Run("found", func(t *testing.T) {
		p, ok := presetByName(presets, "Go")
		assert.True(t, ok)
		assert.Equal(t, "Go", p.Name)
		assert.NotEmpty(t, p.YAML)
	})

	t.Run("not found", func(t *testing.T) {
		_, ok := presetByName(presets, "NonExistent")
		assert.False(t, ok)
	})

	t.Run("Build from scratch is AutoCustomize", func(t *testing.T) {
		p, ok := presetByName(presets, "Build from scratch")
		assert.True(t, ok)
		assert.True(t, p.AutoCustomize)
	})
}

// --- Preset→store→write round-trip test ---

func TestPerformProjectSetup_PresetRoundTrip(t *testing.T) {
	presets := config.Presets()
	for _, preset := range presets {
		if preset.AutoCustomize {
			continue // same YAML as Bare
		}
		t.Run(preset.Name, func(t *testing.T) {
			wd := chdirTemp(t)

			tio, _, out, _ := iostreams.Test()
			cfg := configmocks.NewIsolatedTestConfig(t)
			mockPM := projectmocks.NewMockProjectManager()

			var registeredName string
			mockPM.RegisterFunc = func(_ context.Context, name string, _ string) (project.Project, error) {
				registeredName = name
				return projectmocks.NewMockProject(name, wd), nil
			}

			configPath := filepath.Join(wd, "."+cfg.ProjectConfigFileName())
			err := performProjectSetup(context.Background(), performSetupInput{
				ios:         tio,
				tui:         tui.NewTUI(tio),
				log:         logger.Nop(),
				cfg:         cfg,
				pm:          mockPM,
				projectName: sanitizeTestName(preset.Name),
				preset:      preset,
				configPath:  configPath,
				wd:          wd,
			})
			require.NoError(t, err)

			// Verify config file was created and is valid YAML.
			content, err := os.ReadFile(configPath)
			require.NoError(t, err, "config file not created")

			// Reload the written file into a store to verify it's valid.
			reloaded, err := storage.NewFromString[config.Project](
				string(content),
				storage.WithDefaultsFromStruct[config.Project](),
			)
			require.NoError(t, err, "written config is invalid YAML")

			snap := reloaded.Read()
			assert.NotEmpty(t, snap.Build.Image, "preset %s should set build.image", preset.Name)
			assert.NotEmpty(t, snap.Build.Packages, "preset %s should set build.packages", preset.Name)

			// Verify ignore file was created.
			ignorePath := filepath.Join(wd, cfg.ClawkerIgnoreName())
			assert.FileExists(t, ignorePath)

			// Verify project was registered.
			assert.NotEmpty(t, registeredName)

			// Verify success output.
			assert.Contains(t, out.String(), "Created:")
			assert.Contains(t, out.String(), preset.Name)
		})
	}
}

func TestPerformProjectSetup_OverwriteCreatesIgnore(t *testing.T) {
	wd := chdirTemp(t)

	tio, _, _, _ := iostreams.Test()
	cfg := configmocks.NewIsolatedTestConfig(t)
	mockPM := projectmocks.NewMockProjectManager()
	mockPM.RegisterFunc = func(_ context.Context, name string, repoPath string) (project.Project, error) {
		return projectmocks.NewMockProject(name, repoPath), nil
	}

	preset, _ := presetByName(config.Presets(), "Bare")
	configPath := filepath.Join(wd, "."+cfg.ProjectConfigFileName())

	// Pre-create ignore file with custom content.
	ignorePath := filepath.Join(wd, cfg.ClawkerIgnoreName())
	require.NoError(t, os.WriteFile(ignorePath, []byte("custom\n"), 0644))

	// Without --force, ignore file should NOT be overwritten.
	err := performProjectSetup(context.Background(), performSetupInput{
		ios:         tio,
		tui:         tui.NewTUI(tio),
		log:         logger.Nop(),
		cfg:         cfg,
		pm:          mockPM,
		projectName: "test",
		preset:      preset,
		configPath:  configPath,
		wd:          wd,
		force:       false,
	})
	require.NoError(t, err)

	content, err := os.ReadFile(ignorePath)
	require.NoError(t, err)
	assert.Equal(t, "custom\n", string(content), "ignore file should not be overwritten without --force")

	// With --force, ignore file should be overwritten.
	err = performProjectSetup(context.Background(), performSetupInput{
		ios:         tio,
		tui:         tui.NewTUI(tio),
		log:         logger.Nop(),
		cfg:         cfg,
		pm:          mockPM,
		projectName: "test",
		preset:      preset,
		configPath:  configPath,
		wd:          wd,
		force:       true,
	})
	require.NoError(t, err)

	content, err = os.ReadFile(ignorePath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Clawker Ignore File")
}

func TestRunNonInteractive_PresetFlag(t *testing.T) {
	wd := chdirTemp(t)

	tio, _, out, _ := iostreams.Test()
	cfg := configmocks.NewIsolatedTestConfig(t)
	mockPM := projectmocks.NewMockProjectManager()
	mockPM.RegisterFunc = func(_ context.Context, name string, repoPath string) (project.Project, error) {
		return projectmocks.NewMockProject(name, repoPath), nil
	}

	opts := &ProjectInitOptions{
		IOStreams:      tio,
		Config:         func() (config.Config, error) { return cfg, nil },
		Logger:         func() (*logger.Logger, error) { return logger.Nop(), nil },
		ProjectManager: func() (project.ProjectManager, error) { return mockPM, nil },
		Yes:            true,
		Preset:         "Go",
	}

	err := Run(context.Background(), opts)
	require.NoError(t, err)

	// Verify config file was created with Go preset values.
	configPath := filepath.Join(wd, "."+cfg.ProjectConfigFileName())
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)

	reloaded, err := storage.NewFromString[config.Project](
		string(content),
		storage.WithDefaultsFromStruct[config.Project](),
	)
	require.NoError(t, err)

	snap := reloaded.Read()
	assert.Equal(t, "golang:1.23-bookworm", snap.Build.Image)
	assert.Contains(t, out.String(), "Go")
}

func TestRunNonInteractive_UnknownPreset(t *testing.T) {
	chdirTemp(t)

	tio, _, _, _ := iostreams.Test()
	cfg := configmocks.NewIsolatedTestConfig(t)

	opts := &ProjectInitOptions{
		IOStreams:      tio,
		Config:         func() (config.Config, error) { return cfg, nil },
		Logger:         func() (*logger.Logger, error) { return logger.Nop(), nil },
		ProjectManager: func() (project.ProjectManager, error) { return projectmocks.NewMockProjectManager(), nil },
		Yes:            true,
		Preset:         "NonExistent",
	}

	err := Run(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown preset")
}

func TestRunNonInteractive_ExistingConfigNoForce(t *testing.T) {
	wd := chdirTemp(t)

	// Create an existing config file.
	require.NoError(t, os.WriteFile(filepath.Join(wd, ".clawker.yaml"), []byte("build:\n  image: test\n"), 0644))

	tio, _, _, errBuf := iostreams.Test()
	cfg := configmocks.NewIsolatedTestConfig(t)

	opts := &ProjectInitOptions{
		IOStreams:      tio,
		Config:         func() (config.Config, error) { return cfg, nil },
		Logger:         func() (*logger.Logger, error) { return logger.Nop(), nil },
		ProjectManager: func() (project.ProjectManager, error) { return projectmocks.NewMockProjectManager(), nil },
		Yes:            true,
	}

	err := Run(context.Background(), opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "configuration already exists")
	assert.Contains(t, errBuf.String(), "already exists")
}

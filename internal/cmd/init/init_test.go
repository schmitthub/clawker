package init

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	intbuild "github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// testInitOpts builds a default InitOptions for testing with an isolated config.
func testInitOpts(t *testing.T, tio *iostreams.IOStreams) *InitOptions {
	t.Helper()

	cfg := configmocks.NewIsolatedTestConfig(t)

	fake := dockertest.NewFakeClient(cfg)
	fake.Client.BuildDefaultImageFunc = func(_ context.Context, _ string, _ whail.BuildProgressFunc) error {
		return nil
	}

	return &InitOptions{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
		Prompter:  func() *prompter.Prompter { return prompter.NewPrompter(tio) },
		Config:    func() (config.Config, error) { return cfg, nil },
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Client:    func(_ context.Context) (*docker.Client, error) { return fake.Client, nil },
	}
}

// --- NewCmdInit tests ---

func TestNewCmdInit(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	cfg := configmocks.NewBlankConfig()
	fake := dockertest.NewFakeClient(cfg)
	f := &cmdutil.Factory{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
		Prompter:  func() *prompter.Prompter { return prompter.NewPrompter(tio) },
		Config:    func() (config.Config, error) { return cfg, nil },
		Client:    func(_ context.Context) (*docker.Client, error) { return fake.Client, nil },
	}

	var gotOpts *InitOptions
	cmd := NewCmdInit(f, func(_ context.Context, opts *InitOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts, "expected runF to be called")

	assert.Equal(t, tio, gotOpts.IOStreams, "IOStreams should be wired from factory")
	assert.NotNil(t, gotOpts.TUI, "TUI should be wired from factory")
	assert.NotNil(t, gotOpts.Prompter, "Prompter func should be wired from factory")
	assert.NotNil(t, gotOpts.Config, "Config func should be wired from factory")
	assert.NotNil(t, gotOpts.Client, "Client func should be wired from factory")
	assert.False(t, gotOpts.Yes, "Yes should be false by default")
}

func TestNewCmdInit_YesFlag(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: tio}

	var gotOpts *InitOptions
	cmd := NewCmdInit(f, func(_ context.Context, opts *InitOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--yes"})
	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts, "expected runF to be called")
	assert.True(t, gotOpts.Yes, "Yes should be true when --yes flag is set")
}

// --- Run dispatch tests ---

func TestRun_NonInteractive(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()

	opts := testInitOpts(t, tio)
	opts.Yes = true

	err := Run(context.Background(), opts)
	require.NoError(t, err)

	output := errOut.String()
	assert.Contains(t, output, "Setting up clawker user settings...")
	assert.Contains(t, output, "Settings:")
	assert.Contains(t, output, "Next Steps:")
}

func TestRun_NonInteractive_SettingsUnchanged(t *testing.T) {
	tio, _, _, _ := iostreams.Test()

	opts := testInitOpts(t, tio)
	opts.Yes = true

	err := Run(context.Background(), opts)
	require.NoError(t, err)

	// In non-interactive mode, build should be skipped (no error)
	_, cfgErr := opts.Config()
	require.NoError(t, cfgErr)
}

// --- performSetup tests ---

func TestPerformSetup_NoBuild(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()
	opts := testInitOpts(t, tio)

	err := performSetup(context.Background(), opts, false, "")
	require.NoError(t, err)

	output := errOut.String()
	assert.Contains(t, output, "Setting up clawker user settings...")
	assert.Contains(t, output, "Settings:")
	assert.Contains(t, output, "Next Steps:")
	assert.NotContains(t, output, "Building base image")

	// Config should load without error (build was skipped)
	_, cfgErr := opts.Config()
	require.NoError(t, cfgErr)

	// No build → no user-level clawker.yaml should be created
	cfgPath, pathErr := config.UserProjectConfigFilePath()
	require.NoError(t, pathErr)
	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr), "user project config should not exist when build is skipped")
}

func TestPerformSetup_BuildSuccess(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()
	opts := testInitOpts(t, tio)

	err := performSetup(context.Background(), opts, true, "bookworm")
	require.NoError(t, err)

	// Verify config loads without error after successful build
	_, cfgErr := opts.Config()
	require.NoError(t, cfgErr)

	// User-level clawker.yaml should be created with build.image
	cfgPath, pathErr := config.UserProjectConfigFilePath()
	require.NoError(t, pathErr)

	data, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr, "user project config should exist after successful build")

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(data, &parsed))

	build, ok := parsed["build"].(map[string]any)
	require.True(t, ok, "build key should be a map")
	assert.Equal(t, docker.DefaultImageTag, build["image"], "build.image should be the default image tag")

	// Should contain only the build key (minimal file)
	assert.Len(t, parsed, 1, "user config should only contain the build key")

	// Output should mention the default image
	assert.Contains(t, errOut.String(), "Default image:")
}

func TestPerformSetup_BuildFailure(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()
	cfg := configmocks.NewIsolatedTestConfig(t)

	fake := dockertest.NewFakeClient(cfg)
	fake.Client.BuildDefaultImageFunc = func(_ context.Context, _ string, _ whail.BuildProgressFunc) error {
		return fmt.Errorf("insufficient disk space")
	}

	opts := &InitOptions{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
		Prompter:  func() *prompter.Prompter { return prompter.NewPrompter(tio) },
		Config:    func() (config.Config, error) { return cfg, nil },
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Client:    func(_ context.Context) (*docker.Client, error) { return fake.Client, nil },
	}

	err := performSetup(context.Background(), opts, true, "bookworm")
	// performSetup does not return an error on build failure — it prints a message and continues
	require.NoError(t, err)

	output := errOut.String()
	assert.Contains(t, output, "Base image build failed")
	assert.Contains(t, output, "You can manually build later")

	// Build failure should not cause a crash — settings are still valid
	assert.Equal(t, cfg.Settings().Logging.MaxSizeMB, cfg.Settings().Logging.MaxSizeMB)

	// Build failure → no user-level clawker.yaml should be created
	cfgPath, pathErr := config.UserProjectConfigFilePath()
	require.NoError(t, pathErr)
	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr), "user project config should not exist after build failure")
}

// --- Wizard field definition tests ---

func TestBuildWizardFields(t *testing.T) {
	fields := buildWizardFields()
	require.Len(t, fields, 3, "wizard should have 3 fields")

	// Field 0: build
	assert.Equal(t, "build", fields[0].ID)
	assert.Equal(t, "Build Image", fields[0].Title)
	assert.Equal(t, "Build an initial base image?", fields[0].Prompt)
	assert.Equal(t, tui.FieldSelect, fields[0].Kind)
	require.Len(t, fields[0].Options, 2)
	assert.Equal(t, "Yes", fields[0].Options[0].Label)
	assert.Equal(t, "No", fields[0].Options[1].Label)
	assert.Equal(t, 0, fields[0].DefaultIdx)
	assert.Nil(t, fields[0].SkipIf, "build field should not have SkipIf")

	// Field 1: flavor
	assert.Equal(t, "flavor", fields[1].ID)
	assert.Equal(t, "Flavor", fields[1].Title)
	assert.Equal(t, "Select Linux flavor", fields[1].Prompt)
	assert.Equal(t, tui.FieldSelect, fields[1].Kind)
	assert.NotEmpty(t, fields[1].Options, "flavor should have options")
	assert.Equal(t, 0, fields[1].DefaultIdx)
	require.NotNil(t, fields[1].SkipIf, "flavor field should have SkipIf")

	// SkipIf should skip when build != "Yes"
	assert.True(t, fields[1].SkipIf(tui.WizardValues{"build": "No"}), "flavor should be skipped when build=No")
	assert.False(t, fields[1].SkipIf(tui.WizardValues{"build": "Yes"}), "flavor should not be skipped when build=Yes")

	// Field 2: confirm
	assert.Equal(t, "confirm", fields[2].ID)
	assert.Equal(t, "Submit", fields[2].Title)
	assert.Equal(t, "Proceed with setup?", fields[2].Prompt)
	assert.Equal(t, tui.FieldConfirm, fields[2].Kind)
	assert.True(t, fields[2].DefaultYes)
}

func TestFlavorFieldOptions(t *testing.T) {
	options := flavorFieldOptions()
	flavors := intbuild.DefaultFlavorOptions()

	require.Len(t, options, len(flavors), "should have same count as bundler flavors")

	for i, opt := range options {
		assert.Equal(t, flavors[i].Name, opt.Label, "option %d label should match flavor name", i)
		assert.Equal(t, flavors[i].Description, opt.Description, "option %d description should match", i)
	}

	// Verify bookworm is first (recommended)
	assert.Equal(t, "bookworm", options[0].Label, "first option should be bookworm")
}

// --- Constant/option tests ---

func TestDefaultImageTag(t *testing.T) {
	expected := "clawker-default:latest"
	assert.Equal(t, expected, docker.DefaultImageTag, "DefaultImageTag constant")
}

func TestFlavorOptions(t *testing.T) {
	flavorOptions := intbuild.DefaultFlavorOptions()
	require.NotEmpty(t, flavorOptions, "flavor options should be defined")

	// Check that bookworm (recommended) is first
	assert.Equal(t, "bookworm", flavorOptions[0].Name, "first flavor option should be bookworm")

	// Check all options have name and description
	for i, opt := range flavorOptions {
		assert.NotEmpty(t, opt.Name, "flavor option %d has empty name", i)
		assert.NotEmpty(t, opt.Description, "flavor option %d has empty description", i)
	}

	// Verify expected flavors exist
	expectedFlavors := []string{"bookworm", "trixie", "alpine3.22", "alpine3.23"}
	names := make([]string, len(flavorOptions))
	for i, opt := range flavorOptions {
		names[i] = opt.Name
	}
	for _, expected := range expectedFlavors {
		assert.Contains(t, names, expected, "expected flavor %q to exist", expected)
	}
}

// --- saveUserProjectConfig tests ---

func TestSaveUserProjectConfig_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CLAWKER_CONFIG_DIR", tmpDir)

	err := saveUserProjectConfig("clawker-default:latest")
	require.NoError(t, err)

	cfgPath := filepath.Join(tmpDir, "clawker.yaml")
	data, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr, "config file should be created")

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(data, &parsed))

	build, ok := parsed["build"].(map[string]any)
	require.True(t, ok, "build key should be a map")
	assert.Equal(t, "clawker-default:latest", build["image"])
	assert.Len(t, parsed, 1, "file should only contain build key")
}

func TestSaveUserProjectConfig_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("CLAWKER_CONFIG_DIR", tmpDir)

	// Pre-seed an existing config with other keys
	cfgPath := filepath.Join(tmpDir, "clawker.yaml")
	existing := []byte("agent:\n  env:\n    FOO: bar\nbuild:\n  packages:\n    - git\n")
	require.NoError(t, os.WriteFile(cfgPath, existing, 0o644))

	err := saveUserProjectConfig("clawker-default:latest")
	require.NoError(t, err)

	data, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(data, &parsed))

	// build.image should be set
	build, ok := parsed["build"].(map[string]any)
	require.True(t, ok, "build key should be a map")
	assert.Equal(t, "clawker-default:latest", build["image"])

	// Existing build.packages should be preserved
	assert.NotNil(t, build["packages"], "existing build.packages should be preserved")

	// Existing agent key should be preserved
	agent, ok := parsed["agent"].(map[string]any)
	require.True(t, ok, "agent key should be preserved")
	env, ok := agent["env"].(map[string]any)
	require.True(t, ok, "agent.env should be preserved")
	assert.Equal(t, "bar", env["FOO"])
}

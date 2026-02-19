package init

import (
	"context"
	"fmt"
	"testing"

	intbuild "github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/config/configtest"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testInitOpts builds a default InitOptions for testing with an in-memory settings loader.
// Returns the options and the settings loader for verification.
func testInitOpts(t *testing.T, tio *iostreamstest.TestIOStreams) (*InitOptions, *configtest.InMemorySettingsLoader) {
	t.Helper()

	// Use temp dir for clawker home so performSetup can create share dir
	t.Setenv(config.clawkerHomeEnv, t.TempDir())

	sl := configtest.NewInMemorySettingsLoader()
	cfg := config.NewConfigForTest(nil, config.DefaultSettings())
	cfg.SetSettingsLoader(sl)

	fake := dockertest.NewFakeClient()
	fake.SetupLegacyBuild()

	return &InitOptions{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		Prompter:  func() *prompter.Prompter { return prompter.NewPrompter(tio.IOStreams) },
		Config:    func() config.Provider { return cfg },
		Client:    func(_ context.Context) (*docker.Client, error) { return fake.Client, nil },
	}, sl
}

// --- NewCmdInit tests ---

func TestNewCmdInit(t *testing.T) {
	tio := iostreamstest.New()
	cfg := config.NewConfigForTest(nil, config.DefaultSettings())
	fake := dockertest.NewFakeClient()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		Prompter:  func() *prompter.Prompter { return prompter.NewPrompter(tio.IOStreams) },
		Config:    func() config.Provider { return cfg },
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

	assert.Equal(t, tio.IOStreams, gotOpts.IOStreams, "IOStreams should be wired from factory")
	assert.NotNil(t, gotOpts.TUI, "TUI should be wired from factory")
	assert.NotNil(t, gotOpts.Prompter, "Prompter func should be wired from factory")
	assert.NotNil(t, gotOpts.Config, "Config func should be wired from factory")
	assert.NotNil(t, gotOpts.Client, "Client func should be wired from factory")
	assert.False(t, gotOpts.Yes, "Yes should be false by default")
}

func TestNewCmdInit_YesFlag(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

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
	tio := iostreamstest.New()
	// Non-interactive by default (isInputTTY = 0)

	opts, _ := testInitOpts(t, tio)
	opts.Yes = true

	err := Run(context.Background(), opts)
	require.NoError(t, err)

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "Setting up clawker user settings...")
	assert.Contains(t, output, "Created: (in-memory)")
	assert.Contains(t, output, "Next Steps:")
}

func TestRun_NonInteractive_SavesSettings(t *testing.T) {
	tio := iostreamstest.New()

	opts, sl := testInitOpts(t, tio)
	opts.Yes = true

	err := Run(context.Background(), opts)
	require.NoError(t, err)

	// Load and verify settings were saved
	settings, loadErr := sl.Load()
	require.NoError(t, loadErr)

	// In non-interactive mode, DefaultImage should remain empty (no build)
	assert.Empty(t, settings.DefaultImage, "DefaultImage should be empty when build is skipped")
}

// --- performSetup tests ---

func TestPerformSetup_NoBuild(t *testing.T) {
	tio := iostreamstest.New()
	opts, sl := testInitOpts(t, tio)

	err := performSetup(context.Background(), opts, false, "")
	require.NoError(t, err)

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "Setting up clawker user settings...")
	assert.Contains(t, output, "Created: (in-memory)")
	assert.Contains(t, output, "Next Steps:")
	assert.NotContains(t, output, "Building base image")

	// Settings should have empty DefaultImage
	settings, loadErr := sl.Load()
	require.NoError(t, loadErr)
	assert.Empty(t, settings.DefaultImage, "DefaultImage should be empty when build is skipped")

	// Share directory should have been created
	shareDir, err := config.ShareDir()
	require.NoError(t, err)
	assert.DirExists(t, shareDir, "share directory should be created during init")
	assert.Contains(t, output, shareDir, "output should mention share directory creation")
}

func TestPerformSetup_BuildSuccess(t *testing.T) {
	tio := iostreamstest.New()
	opts, sl := testInitOpts(t, tio)

	err := performSetup(context.Background(), opts, true, "bookworm")
	require.NoError(t, err)

	// Verify settings were updated with DefaultImageTag after build
	settings, loadErr := sl.Load()
	require.NoError(t, loadErr)
	assert.Equal(t, docker.DefaultImageTag, settings.DefaultImage,
		"DefaultImage should be set to DefaultImageTag after successful build")
}

func TestPerformSetup_BuildFailure(t *testing.T) {
	tio := iostreamstest.New()

	sl := configtest.NewInMemorySettingsLoader()
	cfg := config.NewConfigForTest(nil, config.DefaultSettings())
	cfg.SetSettingsLoader(sl)

	// Set up fake client with a build error
	fake := dockertest.NewFakeClient()
	fake.SetupLegacyBuildError(fmt.Errorf("insufficient disk space"))

	opts := &InitOptions{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		Prompter:  func() *prompter.Prompter { return prompter.NewPrompter(tio.IOStreams) },
		Config:    func() config.Provider { return cfg },
		Client:    func(_ context.Context) (*docker.Client, error) { return fake.Client, nil },
	}

	err := performSetup(context.Background(), opts, true, "bookworm")
	// performSetup does not return an error on build failure — it prints a message and continues
	require.NoError(t, err)

	output := tio.ErrBuf.String()
	assert.Contains(t, output, "Base image build failed")
	assert.Contains(t, output, "You can manually build later")

	// Settings should NOT have DefaultImage set after failed build
	settings, loadErr := sl.Load()
	require.NoError(t, loadErr)
	assert.Empty(t, settings.DefaultImage, "DefaultImage should remain empty after build failure")
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

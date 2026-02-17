package shared

import (
	"context"
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/config/configtest"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- RebuildMissingDefaultImage tests ---

func TestRebuildMissingImage_NonInteractive(t *testing.T) {
	tio := iostreamstest.New()
	// Non-interactive by default

	err := RebuildMissingDefaultImage(context.Background(), RebuildMissingImageOpts{
		ImageRef:    "test-image:latest",
		IOStreams:   tio.IOStreams,
		CommandVerb: "run",
	})

	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Contains(t, tio.ErrBuf.String(), "test-image:latest")
	assert.Contains(t, tio.ErrBuf.String(), "not found")
	assert.Contains(t, tio.ErrBuf.String(), "clawker init")
}

func TestRebuildMissingImage_UserDeclines(t *testing.T) {
	tio := iostreamstest.New()
	tio.SetInteractive(true)
	tio.InBuf.SetInput("2\n") // "No"

	err := RebuildMissingDefaultImage(context.Background(), RebuildMissingImageOpts{
		ImageRef:    "test-image:latest",
		IOStreams:   tio.IOStreams,
		Prompter:    func() *prompter.Prompter { return prompter.NewPrompter(tio.IOStreams) },
		CommandVerb: "run",
	})

	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Contains(t, tio.ErrBuf.String(), "clawker init")
}

func TestRebuildMissingImage_BuildSuccess(t *testing.T) {
	tio := iostreamstest.New()
	tio.SetInteractive(true)
	tio.InBuf.SetInput("1\n1\n") // "Yes" then "bookworm"

	var capturedFlavor string
	buildFn := func(_ context.Context, flavor string, _ whail.BuildProgressFunc) error {
		capturedFlavor = flavor
		return nil
	}

	sl := configtest.NewInMemorySettingsLoader(config.DefaultSettings())

	err := RebuildMissingDefaultImage(context.Background(), RebuildMissingImageOpts{
		ImageRef:       "test-image:latest",
		IOStreams:      tio.IOStreams,
		TUI:            nil, // spinner fallback
		Prompter:       func() *prompter.Prompter { return prompter.NewPrompter(tio.IOStreams) },
		SettingsLoader: func() config.SettingsLoader { return sl },
		BuildImage:     buildFn,
		CommandVerb:    "run",
	})

	require.NoError(t, err)
	assert.Equal(t, "bookworm", capturedFlavor)
	assert.Contains(t, tio.ErrBuf.String(), docker.DefaultImageTag)

	// Verify settings were persisted
	saved, loadErr := sl.Load()
	require.NoError(t, loadErr)
	assert.Equal(t, docker.DefaultImageTag, saved.DefaultImage)
}

func TestRebuildMissingImage_BuildFailure(t *testing.T) {
	tio := iostreamstest.New()
	tio.SetInteractive(true)
	tio.InBuf.SetInput("1\n1\n") // "Yes" then "bookworm"

	buildFn := func(_ context.Context, _ string, _ whail.BuildProgressFunc) error {
		return fmt.Errorf("build exploded")
	}

	err := RebuildMissingDefaultImage(context.Background(), RebuildMissingImageOpts{
		ImageRef:    "test-image:latest",
		IOStreams:   tio.IOStreams,
		TUI:         nil,
		Prompter:    func() *prompter.Prompter { return prompter.NewPrompter(tio.IOStreams) },
		BuildImage:  buildFn,
		CommandVerb: "run",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rebuild default image")
	assert.Contains(t, err.Error(), "build exploded")
}

func TestRebuildMissingImage_PersistSettingsWarning(t *testing.T) {
	tio := iostreamstest.New()
	tio.SetInteractive(true)
	tio.InBuf.SetInput("1\n1\n") // "Yes" then "bookworm"

	buildFn := func(_ context.Context, _ string, _ whail.BuildProgressFunc) error {
		return nil
	}

	err := RebuildMissingDefaultImage(context.Background(), RebuildMissingImageOpts{
		ImageRef:       "test-image:latest",
		IOStreams:      tio.IOStreams,
		TUI:            nil,
		Prompter:       func() *prompter.Prompter { return prompter.NewPrompter(tio.IOStreams) },
		SettingsLoader: func() config.SettingsLoader { return &failSaveSettingsLoader{} },
		BuildImage:     buildFn,
		CommandVerb:    "run",
	})

	require.NoError(t, err)
	assert.Contains(t, tio.ErrBuf.String(), "Could not save")
}

// --- persistDefaultImageSetting tests ---

func TestPersistDefaultImageSetting_NilLoaderFn(t *testing.T) {
	warning := persistDefaultImageSetting(nil)
	assert.Empty(t, warning)
}

func TestPersistDefaultImageSetting_NilLoader(t *testing.T) {
	warning := persistDefaultImageSetting(func() config.SettingsLoader { return nil })
	assert.Empty(t, warning)
}

func TestPersistDefaultImageSetting_LoadError(t *testing.T) {
	warning := persistDefaultImageSetting(func() config.SettingsLoader {
		return &failLoadSettingsLoader{}
	})
	assert.Contains(t, warning, "Could not save")
}

func TestPersistDefaultImageSetting_SaveError(t *testing.T) {
	warning := persistDefaultImageSetting(func() config.SettingsLoader {
		return &failSaveSettingsLoader{}
	})
	assert.Contains(t, warning, "Could not save")
}

func TestPersistDefaultImageSetting_Success(t *testing.T) {
	sl := configtest.NewInMemorySettingsLoader(config.DefaultSettings())
	warning := persistDefaultImageSetting(func() config.SettingsLoader { return sl })
	assert.Empty(t, warning)

	saved, err := sl.Load()
	require.NoError(t, err)
	assert.Equal(t, docker.DefaultImageTag, saved.DefaultImage)
}

// --- progressStatus tests ---

func TestProgressStatus(t *testing.T) {
	tests := []struct {
		input whail.BuildStepStatus
		want  tui.ProgressStepStatus
	}{
		{whail.BuildStepRunning, tui.StepRunning},
		{whail.BuildStepComplete, tui.StepComplete},
		{whail.BuildStepCached, tui.StepCached},
		{whail.BuildStepError, tui.StepError},
		{whail.BuildStepPending, tui.StepPending},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, progressStatus(tt.input))
	}
}

// --- Test doubles ---

// failLoadSettingsLoader is a SettingsLoader that fails on Load.
type failLoadSettingsLoader struct{}

func (f *failLoadSettingsLoader) Path() string                { return "" }
func (f *failLoadSettingsLoader) ProjectSettingsPath() string { return "" }
func (f *failLoadSettingsLoader) Exists() bool                { return false }
func (f *failLoadSettingsLoader) Load() (*config.Settings, error) {
	return nil, fmt.Errorf("load failed")
}
func (f *failLoadSettingsLoader) Save(*config.Settings) error { return nil }
func (f *failLoadSettingsLoader) EnsureExists() (bool, error) { return false, nil }

// failSaveSettingsLoader is a SettingsLoader that loads OK but fails on Save.
type failSaveSettingsLoader struct{}

func (f *failSaveSettingsLoader) Path() string                { return "" }
func (f *failSaveSettingsLoader) ProjectSettingsPath() string { return "" }
func (f *failSaveSettingsLoader) Exists() bool                { return false }
func (f *failSaveSettingsLoader) Load() (*config.Settings, error) {
	return config.DefaultSettings(), nil
}
func (f *failSaveSettingsLoader) Save(*config.Settings) error { return fmt.Errorf("save failed") }
func (f *failSaveSettingsLoader) EnsureExists() (bool, error) { return false, nil }

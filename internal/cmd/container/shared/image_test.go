package shared

import (
	"context"
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
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

	cfg, _ := configmocks.NewIsolatedTestConfig(t)

	err := RebuildMissingDefaultImage(context.Background(), RebuildMissingImageOpts{
		ImageRef:    "test-image:latest",
		IOStreams:   tio.IOStreams,
		TUI:         nil, // spinner fallback
		Prompter:    func() *prompter.Prompter { return prompter.NewPrompter(tio.IOStreams) },
		Cfg:         cfg,
		BuildImage:  buildFn,
		CommandVerb: "run",
	})

	require.NoError(t, err)
	assert.Equal(t, "bookworm", capturedFlavor)
	assert.Contains(t, tio.ErrBuf.String(), docker.DefaultImageTag)

	// Verify settings were persisted
	settings := cfg.Settings()
	assert.Equal(t, docker.DefaultImageTag, settings.DefaultImage)
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

	mock := configmocks.NewBlankConfig()
	mock.SetFunc = func(key string, value any) error { return nil }
	mock.WriteFunc = func(opts config.WriteOptions) error { return fmt.Errorf("save failed") }

	err := RebuildMissingDefaultImage(context.Background(), RebuildMissingImageOpts{
		ImageRef:    "test-image:latest",
		IOStreams:   tio.IOStreams,
		TUI:         nil,
		Prompter:    func() *prompter.Prompter { return prompter.NewPrompter(tio.IOStreams) },
		Cfg:         mock,
		BuildImage:  buildFn,
		CommandVerb: "run",
	})

	require.NoError(t, err)
	assert.Contains(t, tio.ErrBuf.String(), "Could not save")
}

// --- persistDefaultImageSetting tests ---

func TestPersistDefaultImageSetting_NilConfig(t *testing.T) {
	warning := persistDefaultImageSetting(nil)
	assert.Empty(t, warning)
}

func TestPersistDefaultImageSetting_WriteError(t *testing.T) {
	mock := configmocks.NewBlankConfig()
	mock.SetFunc = func(key string, value any) error { return nil }
	mock.WriteFunc = func(opts config.WriteOptions) error { return fmt.Errorf("save failed") }

	warning := persistDefaultImageSetting(mock)
	assert.Contains(t, warning, "Could not save")
}

func TestPersistDefaultImageSetting_Success(t *testing.T) {
	cfg, _ := configmocks.NewIsolatedTestConfig(t)
	warning := persistDefaultImageSetting(cfg)
	assert.Empty(t, warning)

	settings := cfg.Settings()
	assert.Equal(t, docker.DefaultImageTag, settings.DefaultImage)
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

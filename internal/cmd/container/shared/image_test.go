package shared

import (
	"context"
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- RebuildMissingDefaultImage tests ---

func TestRebuildMissingImage_NonInteractive(t *testing.T) {
	tio, _, _, errOut := iostreams.Test()
	// Non-interactive by default

	err := RebuildMissingDefaultImage(context.Background(), RebuildMissingImageOpts{
		ImageRef:    "test-image:latest",
		IOStreams:   tio,
		CommandVerb: "run",
	})

	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Contains(t, errOut.String(), "test-image:latest")
	assert.Contains(t, errOut.String(), "not found")
	assert.Contains(t, errOut.String(), "clawker init")
}

func TestRebuildMissingImage_UserDeclines(t *testing.T) {
	tio, in, _, errOut := iostreams.Test()
	tio.SetStdinTTY(true)
	tio.SetStdoutTTY(true)
	in.WriteString("2\n") // "No"

	err := RebuildMissingDefaultImage(context.Background(), RebuildMissingImageOpts{
		ImageRef:    "test-image:latest",
		IOStreams:   tio,
		Prompter:    func() *prompter.Prompter { return prompter.NewPrompter(tio) },
		CommandVerb: "run",
	})

	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Contains(t, errOut.String(), "clawker init")
}

func TestRebuildMissingImage_BuildSuccess(t *testing.T) {
	tio, in, _, errOut := iostreams.Test()
	tio.SetStdinTTY(true)
	tio.SetStdoutTTY(true)
	in.WriteString("1\n1\n") // "Yes" then "bookworm"

	var capturedFlavor string
	buildFn := func(_ context.Context, flavor string, _ whail.BuildProgressFunc) error {
		capturedFlavor = flavor
		return nil
	}

	err := RebuildMissingDefaultImage(context.Background(), RebuildMissingImageOpts{
		ImageRef:    "test-image:latest",
		IOStreams:   tio,
		TUI:         nil, // spinner fallback
		Prompter:    func() *prompter.Prompter { return prompter.NewPrompter(tio) },
		BuildImage:  buildFn,
		CommandVerb: "run",
	})

	require.NoError(t, err)
	assert.Equal(t, "bookworm", capturedFlavor)
	assert.Contains(t, errOut.String(), docker.DefaultImageTag)
}

func TestRebuildMissingImage_BuildFailure(t *testing.T) {
	tio, in, _, _ := iostreams.Test()
	tio.SetStdinTTY(true)
	tio.SetStdoutTTY(true)
	in.WriteString("1\n1\n") // "Yes" then "bookworm"

	buildFn := func(_ context.Context, _ string, _ whail.BuildProgressFunc) error {
		return fmt.Errorf("build exploded")
	}

	err := RebuildMissingDefaultImage(context.Background(), RebuildMissingImageOpts{
		ImageRef:    "test-image:latest",
		IOStreams:   tio,
		TUI:         nil,
		Prompter:    func() *prompter.Prompter { return prompter.NewPrompter(tio) },
		BuildImage:  buildFn,
		CommandVerb: "run",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to rebuild default image")
	assert.Contains(t, err.Error(), "build exploded")
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

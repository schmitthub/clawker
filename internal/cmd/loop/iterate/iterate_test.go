package iterate

import (
	"context"
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFactory(t *testing.T) (*cmdutil.Factory, *iostreams.TestIOStreams) {
	t.Helper()
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:      tui.NewTUI(tio.IOStreams),
	}
	return f, tio
}

func testFactoryWithConfig(t *testing.T) (*cmdutil.Factory, *iostreams.TestIOStreams) {
	t.Helper()
	tio := iostreams.NewTestIOStreams()
	project := config.DefaultConfig()
	project.Project = "testproject"
	cfg := config.NewConfigForTest(project, nil)
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:      tui.NewTUI(tio.IOStreams),
		Config:   func() *config.Config { return cfg },
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("docker not available in tests")
		},
	}
	return f, tio
}

func TestNewCmdIterate(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *IterateOptions
	cmd := NewCmdIterate(f, func(_ context.Context, opts *IterateOptions) error {
		gotOpts = opts
		return nil
	})

	assert.Equal(t, "iterate", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	cmd.SetArgs([]string{"--agent", "dev", "--prompt", "Fix tests"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.NotNil(t, gotOpts.IOStreams)
	assert.Equal(t, "Fix tests", gotOpts.Prompt)
}

func TestNewCmdIterate_PromptShorthand(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *IterateOptions
	cmd := NewCmdIterate(f, func(_ context.Context, opts *IterateOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "-p", "Short prompt"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Equal(t, "Short prompt", gotOpts.Prompt)
}

func TestNewCmdIterate_PromptFile(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *IterateOptions
	cmd := NewCmdIterate(f, func(_ context.Context, opts *IterateOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "--prompt-file", "task.md"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Equal(t, "task.md", gotOpts.PromptFile)
	assert.Empty(t, gotOpts.Prompt)
}

func TestNewCmdIterate_PromptAndPromptFileMutuallyExclusive(t *testing.T) {
	f, tio := testFactory(t)

	cmd := NewCmdIterate(f, func(_ context.Context, _ *IterateOptions) error {
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "--prompt", "test", "--prompt-file", "file.md"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "if any flags in the group [prompt prompt-file] are set none of the others can be")
}

func TestNewCmdIterate_RequiresPromptOrPromptFile(t *testing.T) {
	f, tio := testFactory(t)

	cmd := NewCmdIterate(f, func(_ context.Context, _ *IterateOptions) error {
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one of the flags in the group [prompt prompt-file] is required")
}

func TestNewCmdIterate_SharedFlagDefaults(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *IterateOptions
	cmd := NewCmdIterate(f, func(_ context.Context, opts *IterateOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "--prompt", "test"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)

	// Loop control defaults
	assert.Equal(t, loop.DefaultMaxLoops, gotOpts.MaxLoops)
	assert.Equal(t, loop.DefaultStagnationThreshold, gotOpts.StagnationThreshold)
	assert.Equal(t, loop.DefaultTimeoutMinutes, gotOpts.TimeoutMinutes)
	assert.Equal(t, loop.DefaultLoopDelaySeconds, gotOpts.LoopDelay)

	// Circuit breaker defaults
	assert.Equal(t, loop.DefaultSameErrorThreshold, gotOpts.SameErrorThreshold)
	assert.Equal(t, loop.DefaultOutputDeclineThreshold, gotOpts.OutputDeclineThreshold)
	assert.Equal(t, loop.DefaultMaxConsecutiveTestLoops, gotOpts.MaxConsecutiveTestLoops)
	assert.Equal(t, loop.DefaultSafetyCompletionThreshold, gotOpts.SafetyCompletionThreshold)
	assert.Equal(t, loop.DefaultCompletionThreshold, gotOpts.CompletionThreshold)
	assert.False(t, gotOpts.StrictCompletion)

	// Execution defaults
	assert.False(t, gotOpts.SkipPermissions)
	assert.Equal(t, loop.DefaultCallsPerHour, gotOpts.CallsPerHour)
	assert.False(t, gotOpts.ResetCircuit)

	// Hooks, system prompt, container defaults
	assert.Empty(t, gotOpts.HooksFile)
	assert.Empty(t, gotOpts.AppendSystemPrompt)
	assert.Empty(t, gotOpts.Worktree)
	assert.Empty(t, gotOpts.Image)

	// Output defaults
	assert.False(t, gotOpts.Verbose)
	assert.True(t, gotOpts.Format.IsDefault())
}

func TestNewCmdIterate_AllFlags(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *IterateOptions
	cmd := NewCmdIterate(f, func(_ context.Context, opts *IterateOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{
		"--agent", "myagent",
		"--prompt", "Do everything",
		"--max-loops", "100",
		"--stagnation-threshold", "5",
		"--timeout", "30",
		"--loop-delay", "10",
		"--same-error-threshold", "8",
		"--output-decline-threshold", "50",
		"--max-test-loops", "5",
		"--safety-completion-threshold", "10",
		"--completion-threshold", "3",
		"--strict-completion",
		"--skip-permissions",
		"--calls-per-hour", "200",
		"--reset-circuit",
		"--hooks-file", "/path/to/hooks.json",
		"--append-system-prompt", "Be thorough",
		"--worktree", "feature/test",
		"--image", "node:20-slim",
		"--verbose",
	})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)

	assert.Equal(t, "myagent", gotOpts.Agent)
	assert.Equal(t, "Do everything", gotOpts.Prompt)
	assert.Equal(t, 100, gotOpts.MaxLoops)
	assert.Equal(t, 5, gotOpts.StagnationThreshold)
	assert.Equal(t, 30, gotOpts.TimeoutMinutes)
	assert.Equal(t, 10, gotOpts.LoopDelay)
	assert.Equal(t, 8, gotOpts.SameErrorThreshold)
	assert.Equal(t, 50, gotOpts.OutputDeclineThreshold)
	assert.Equal(t, 5, gotOpts.MaxConsecutiveTestLoops)
	assert.Equal(t, 10, gotOpts.SafetyCompletionThreshold)
	assert.Equal(t, 3, gotOpts.CompletionThreshold)
	assert.True(t, gotOpts.StrictCompletion)
	assert.True(t, gotOpts.SkipPermissions)
	assert.Equal(t, 200, gotOpts.CallsPerHour)
	assert.True(t, gotOpts.ResetCircuit)
	assert.Equal(t, "/path/to/hooks.json", gotOpts.HooksFile)
	assert.Equal(t, "Be thorough", gotOpts.AppendSystemPrompt)
	assert.Equal(t, "feature/test", gotOpts.Worktree)
	assert.Equal(t, "node:20-slim", gotOpts.Image)
	assert.True(t, gotOpts.Verbose)
}

func TestNewCmdIterate_JSONOutput(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *IterateOptions
	cmd := NewCmdIterate(f, func(_ context.Context, opts *IterateOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "--prompt", "test", "--json"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.True(t, gotOpts.Format.IsJSON())
}

func TestNewCmdIterate_QuietOutput(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *IterateOptions
	cmd := NewCmdIterate(f, func(_ context.Context, opts *IterateOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "--prompt", "test", "--quiet"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.True(t, gotOpts.Format.Quiet)
}

func TestNewCmdIterate_VerboseExclusivity(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "verbose and json",
			args: []string{"--agent", "dev", "--prompt", "test", "--verbose", "--json"},
		},
		{
			name: "verbose and quiet",
			args: []string{"--agent", "dev", "--prompt", "test", "--verbose", "--quiet"},
		},
		{
			name: "verbose and format",
			args: []string{"--agent", "dev", "--prompt", "test", "--verbose", "--format", "json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, tio := testFactory(t)

			cmd := NewCmdIterate(f, func(_ context.Context, _ *IterateOptions) error {
				return nil
			})

			cmd.SetArgs(tt.args)
			cmd.SetIn(tio.In)
			cmd.SetOut(tio.Out)
			cmd.SetErr(tio.ErrOut)

			err := cmd.Execute()
			require.Error(t, err)
		})
	}
}

func TestNewCmdIterate_AgentFlag(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *IterateOptions
	cmd := NewCmdIterate(f, func(_ context.Context, opts *IterateOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "myagent", "--prompt", "test"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Equal(t, "myagent", gotOpts.Agent)
}

func TestNewCmdIterate_AgentRequired(t *testing.T) {
	f, tio := testFactory(t)

	cmd := NewCmdIterate(f, func(_ context.Context, _ *IterateOptions) error {
		return nil
	})

	cmd.SetArgs([]string{"--prompt", "test"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `required flag(s) "agent" not set`)
}

func TestNewCmdIterate_RealRunNeedsDocker(t *testing.T) {
	// With nil runF, the real iterateRun is called.
	// It should fail gracefully at the Docker client step (not panic).
	f, tio := testFactoryWithConfig(t)

	cmd := NewCmdIterate(f, nil)
	cmd.SetArgs([]string{"--agent", "dev", "--prompt", "test"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker not available")
}

func TestNewCmdIterate_FlagsCaptured(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *IterateOptions
	cmd := NewCmdIterate(f, func(_ context.Context, opts *IterateOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "--prompt", "test", "--max-loops", "75"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	require.NotNil(t, gotOpts.flags)
	assert.True(t, gotOpts.flags.Changed("max-loops"))
	assert.False(t, gotOpts.flags.Changed("stagnation-threshold"))
}

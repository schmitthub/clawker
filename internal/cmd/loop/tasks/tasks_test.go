package tasks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/cmd/loop/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFactory(t *testing.T) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
	}
	return f, tio
}

func testFactoryWithConfig(t *testing.T) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()
	project := config.DefaultProject()
	project.Project = "testproject"
	cfg := config.NewConfigForTest(project, nil)
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		Config:    func() *config.Config { return cfg },
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("docker not available in tests")
		},
	}
	return f, tio
}

func TestNewCmdTasks(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *TasksOptions
	cmd := NewCmdTasks(f, func(_ context.Context, opts *TasksOptions) error {
		gotOpts = opts
		return nil
	})

	assert.Equal(t, "tasks", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	cmd.SetArgs([]string{"--tasks", "todo.md"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.NotNil(t, gotOpts.IOStreams)
	assert.Equal(t, "todo.md", gotOpts.TasksFile)
}

func TestNewCmdTasks_RequiresTasksFlag(t *testing.T) {
	f, tio := testFactory(t)

	cmd := NewCmdTasks(f, func(_ context.Context, _ *TasksOptions) error {
		return nil
	})

	cmd.SetArgs([]string{})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `required flag(s) "tasks" not set`)
}

func TestNewCmdTasks_TaskPrompt(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *TasksOptions
	cmd := NewCmdTasks(f, func(_ context.Context, opts *TasksOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--tasks", "todo.md", "--task-prompt", "Pick highest priority"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Equal(t, "Pick highest priority", gotOpts.TaskPrompt)
	assert.Empty(t, gotOpts.TaskPromptFile)
}

func TestNewCmdTasks_TaskPromptFile(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *TasksOptions
	cmd := NewCmdTasks(f, func(_ context.Context, opts *TasksOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--tasks", "todo.md", "--task-prompt-file", "instructions.md"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Equal(t, "instructions.md", gotOpts.TaskPromptFile)
	assert.Empty(t, gotOpts.TaskPrompt)
}

func TestNewCmdTasks_TaskPromptMutuallyExclusive(t *testing.T) {
	f, tio := testFactory(t)

	cmd := NewCmdTasks(f, func(_ context.Context, _ *TasksOptions) error {
		return nil
	})

	cmd.SetArgs([]string{"--tasks", "todo.md", "--task-prompt", "inline", "--task-prompt-file", "file.md"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "if any flags in the group [task-prompt task-prompt-file] are set none of the others can be")
}

func TestNewCmdTasks_SharedFlagDefaults(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *TasksOptions
	cmd := NewCmdTasks(f, func(_ context.Context, opts *TasksOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--tasks", "todo.md"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)

	// Loop control defaults
	assert.Equal(t, shared.DefaultMaxLoops, gotOpts.MaxLoops)
	assert.Equal(t, shared.DefaultStagnationThreshold, gotOpts.StagnationThreshold)
	assert.Equal(t, shared.DefaultTimeoutMinutes, gotOpts.TimeoutMinutes)
	assert.Equal(t, shared.DefaultLoopDelaySeconds, gotOpts.LoopDelay)

	// Circuit breaker defaults
	assert.Equal(t, shared.DefaultSameErrorThreshold, gotOpts.SameErrorThreshold)
	assert.Equal(t, shared.DefaultOutputDeclineThreshold, gotOpts.OutputDeclineThreshold)
	assert.Equal(t, shared.DefaultMaxConsecutiveTestLoops, gotOpts.MaxConsecutiveTestLoops)
	assert.Equal(t, shared.DefaultSafetyCompletionThreshold, gotOpts.SafetyCompletionThreshold)
	assert.Equal(t, shared.DefaultCompletionThreshold, gotOpts.CompletionThreshold)
	assert.False(t, gotOpts.StrictCompletion)

	// Execution defaults
	assert.False(t, gotOpts.SkipPermissions)
	assert.Equal(t, shared.DefaultCallsPerHour, gotOpts.CallsPerHour)
	assert.False(t, gotOpts.ResetCircuit)

	// Optional fields
	assert.Empty(t, gotOpts.HooksFile)
	assert.Empty(t, gotOpts.AppendSystemPrompt)
	assert.Empty(t, gotOpts.Agent, "Agent should be empty at flag-parse time (set in run function)")
	assert.Empty(t, gotOpts.Worktree)
	assert.Empty(t, gotOpts.Image)
	assert.Empty(t, gotOpts.TaskPrompt)
	assert.Empty(t, gotOpts.TaskPromptFile)

	// Output defaults
	assert.False(t, gotOpts.Verbose)
	assert.True(t, gotOpts.Format.IsDefault())
}

func TestNewCmdTasks_AllFlags(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *TasksOptions
	cmd := NewCmdTasks(f, func(_ context.Context, opts *TasksOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{
		"--tasks", "backlog.md",
		"--task-prompt", "Do the highest priority task",
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

	assert.Equal(t, "backlog.md", gotOpts.TasksFile)
	assert.Equal(t, "Do the highest priority task", gotOpts.TaskPrompt)
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

func TestNewCmdTasks_JSONOutput(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *TasksOptions
	cmd := NewCmdTasks(f, func(_ context.Context, opts *TasksOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--tasks", "todo.md", "--json"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.True(t, gotOpts.Format.IsJSON())
}

func TestNewCmdTasks_VerboseExclusivity(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "verbose and json",
			args: []string{"--tasks", "todo.md", "--verbose", "--json"},
		},
		{
			name: "verbose and quiet",
			args: []string{"--tasks", "todo.md", "--verbose", "--quiet"},
		},
		{
			name: "verbose and format",
			args: []string{"--tasks", "todo.md", "--verbose", "--format", "json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, tio := testFactory(t)

			cmd := NewCmdTasks(f, func(_ context.Context, _ *TasksOptions) error {
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

func TestNewCmdTasks_NoAgentFlag(t *testing.T) {
	// The --agent flag is not accepted on tasks (agent names are auto-generated).
	f, tio := testFactory(t)

	cmd := NewCmdTasks(f, func(_ context.Context, _ *TasksOptions) error {
		return nil
	})

	cmd.SetArgs([]string{"--agent", "dev", "--tasks", "todo.md"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown flag: --agent")
}

func TestNewCmdTasks_AgentEmptyAtFlagParse(t *testing.T) {
	// Agent is not set by flags — it's populated programmatically in the run function.
	f, tio := testFactory(t)

	var gotOpts *TasksOptions
	cmd := NewCmdTasks(f, func(_ context.Context, opts *TasksOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--tasks", "todo.md"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	assert.Empty(t, gotOpts.Agent, "Agent should be empty at flag-parse time")
}

func TestNewCmdTasks_RealRunNeedsDocker(t *testing.T) {
	// With nil runF, the real tasksRun is called.
	// Create a real tasks file so we get past prompt resolution.
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	require.NoError(t, os.WriteFile(tasksPath, []byte("- [ ] Task 1"), 0o644))

	f, tio := testFactoryWithConfig(t)

	cmd := NewCmdTasks(f, nil)
	cmd.SetArgs([]string{"--tasks", tasksPath})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker not available")
}

func TestNewCmdTasks_FactoryDIWiring(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *TasksOptions
	cmd := NewCmdTasks(f, func(_ context.Context, opts *TasksOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--tasks", "todo.md"})
	cmd.SetIn(tio.In)
	cmd.SetOut(tio.Out)
	cmd.SetErr(tio.ErrOut)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)

	// Verify all Factory DI fields are wired
	assert.NotNil(t, gotOpts.IOStreams, "IOStreams should be wired")
	assert.NotNil(t, gotOpts.TUI, "TUI should be wired")
	assert.Nil(t, gotOpts.HostProxy, "HostProxy should be nil for test factory")
	assert.Nil(t, gotOpts.SocketBridge, "SocketBridge should be nil for test factory")
	assert.Empty(t, gotOpts.Version, "Version should be empty for test factory")
}

func TestNewCmdTasks_FlagsCaptured(t *testing.T) {
	f, tio := testFactory(t)

	var gotOpts *TasksOptions
	cmd := NewCmdTasks(f, func(_ context.Context, opts *TasksOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--tasks", "todo.md", "--max-loops", "75"})
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

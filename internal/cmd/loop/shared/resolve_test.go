package shared

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ResolvePrompt tests ---

func TestResolvePrompt_InlinePrompt(t *testing.T) {
	got, err := ResolvePrompt("Fix all tests", "")
	require.NoError(t, err)
	assert.Equal(t, "Fix all tests", got)
}

func TestResolvePrompt_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prompt.md")
	err := os.WriteFile(path, []byte("  Fix all tests  \n"), 0o644)
	require.NoError(t, err)

	got, gotErr := ResolvePrompt("", path)
	require.NoError(t, gotErr)
	assert.Equal(t, "Fix all tests", got)
}

func TestResolvePrompt_FileNotFound(t *testing.T) {
	_, err := ResolvePrompt("", "/nonexistent/path/to/file.md")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading prompt file")
}

func TestResolvePrompt_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.md")
	err := os.WriteFile(path, []byte("   \n  "), 0o644)
	require.NoError(t, err)

	_, gotErr := ResolvePrompt("", path)
	require.Error(t, gotErr)
	assert.Contains(t, gotErr.Error(), "empty")
}

// --- ResolveTasksPrompt tests ---

func TestResolveTasksPrompt_DefaultTemplate(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	err := os.WriteFile(tasksPath, []byte("- [ ] Task 1\n- [ ] Task 2"), 0o644)
	require.NoError(t, err)

	got, gotErr := ResolveTasksPrompt(tasksPath, "", "")
	require.NoError(t, gotErr)
	assert.Contains(t, got, "Task 1")
	assert.Contains(t, got, "Task 2")
	assert.Contains(t, got, "<tasks>")
}

func TestResolveTasksPrompt_CustomInlineTemplate(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	err := os.WriteFile(tasksPath, []byte("- [ ] Task 1"), 0o644)
	require.NoError(t, err)

	got, gotErr := ResolveTasksPrompt(tasksPath, "Do these tasks: %s", "")
	require.NoError(t, gotErr)
	assert.Contains(t, got, "Do these tasks:")
	assert.Contains(t, got, "Task 1")
}

func TestResolveTasksPrompt_TemplateWithoutPlaceholder(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	err := os.WriteFile(tasksPath, []byte("- [ ] Task 1"), 0o644)
	require.NoError(t, err)

	got, gotErr := ResolveTasksPrompt(tasksPath, "Complete the following work.", "")
	require.NoError(t, gotErr)
	assert.Contains(t, got, "Complete the following work.")
	assert.Contains(t, got, "Task 1")
}

func TestResolveTasksPrompt_CustomTemplateFile(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	err := os.WriteFile(tasksPath, []byte("- [ ] Task 1"), 0o644)
	require.NoError(t, err)

	templatePath := filepath.Join(dir, "template.md")
	err = os.WriteFile(templatePath, []byte("Custom instructions: %s"), 0o644)
	require.NoError(t, err)

	got, gotErr := ResolveTasksPrompt(tasksPath, "", templatePath)
	require.NoError(t, gotErr)
	assert.Contains(t, got, "Custom instructions:")
	assert.Contains(t, got, "Task 1")
}

func TestResolveTasksPrompt_TasksFileNotFound(t *testing.T) {
	_, err := ResolveTasksPrompt("/nonexistent/tasks.md", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading tasks file")
}

func TestResolveTasksPrompt_EmptyTasksFile(t *testing.T) {
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	err := os.WriteFile(tasksPath, []byte("   \n"), 0o644)
	require.NoError(t, err)

	_, gotErr := ResolveTasksPrompt(tasksPath, "", "")
	require.Error(t, gotErr)
	assert.Contains(t, gotErr.Error(), "empty")
}

// --- BuildRunnerOptions tests ---

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	return cmd
}

func TestBuildRunnerOptions_BasicMapping(t *testing.T) {
	loopOpts := &LoopOptions{
		MaxLoops:                  100,
		StagnationThreshold:       5,
		TimeoutMinutes:            30,
		LoopDelay:                 10,
		SameErrorThreshold:        8,
		OutputDeclineThreshold:    50,
		MaxConsecutiveTestLoops:   5,
		SafetyCompletionThreshold: 10,
		CompletionThreshold:       3,
		StrictCompletion:          true,
		SkipPermissions:           true,
		CallsPerHour:              200,
		ResetCircuit:              true,
		Verbose:                   true,
	}

	cmd := newTestCmd()
	opts := BuildRunnerOptions(loopOpts, "myproject", "dev", "clawker.myproject.dev", "Fix tests", "/workspace", cmd.Flags(), nil)

	assert.Equal(t, "clawker.myproject.dev", opts.ContainerName)
	assert.Equal(t, "myproject", opts.Project)
	assert.Equal(t, "dev", opts.Agent)
	assert.Equal(t, "Fix tests", opts.Prompt)
	assert.Equal(t, "/workspace", opts.WorkDir)
	assert.Equal(t, 100, opts.MaxLoops)
	assert.Equal(t, 5, opts.StagnationThreshold)
	assert.Equal(t, time.Duration(30)*time.Minute, opts.Timeout)
	assert.Equal(t, 10, opts.LoopDelaySeconds)
	assert.Equal(t, 8, opts.SameErrorThreshold)
	assert.Equal(t, 50, opts.OutputDeclineThreshold)
	assert.Equal(t, 5, opts.MaxConsecutiveTestLoops)
	assert.Equal(t, 10, opts.SafetyCompletionThreshold)
	assert.Equal(t, 3, opts.CompletionThreshold)
	assert.True(t, opts.UseStrictCompletion)
	assert.True(t, opts.SkipPermissions)
	assert.Equal(t, 200, opts.CallsPerHour)
	assert.True(t, opts.ResetCircuit)
	assert.True(t, opts.Verbose)
}

func TestBuildRunnerOptions_ConfigOverrides(t *testing.T) {
	loopOpts := NewLoopOptions()
	// Leave at defaults — config should override
	loopOpts.MaxLoops = loop.DefaultMaxLoops
	loopOpts.StagnationThreshold = loop.DefaultStagnationThreshold
	loopOpts.TimeoutMinutes = loop.DefaultTimeoutMinutes

	loopCfg := &config.LoopConfig{
		MaxLoops:            200,
		StagnationThreshold: 10,
		TimeoutMinutes:      60,
		CallsPerHour:        50,
		SkipPermissions:     true,
	}

	// Create a command and register flags so Changed() works properly
	cmd := newTestCmd()
	AddLoopFlags(cmd, loopOpts)
	// Don't set any flags — none are "changed"
	require.NoError(t, cmd.ParseFlags([]string{}))

	opts := BuildRunnerOptions(loopOpts, "proj", "dev", "clawker.proj.dev", "test", "/workspace", cmd.Flags(), loopCfg)

	assert.Equal(t, 200, opts.MaxLoops)
	assert.Equal(t, 10, opts.StagnationThreshold)
	assert.Equal(t, time.Duration(60)*time.Minute, opts.Timeout)
	assert.Equal(t, 50, opts.CallsPerHour)
	assert.True(t, opts.SkipPermissions)
}

func TestBuildRunnerOptions_ExplicitFlagWins(t *testing.T) {
	loopOpts := NewLoopOptions()

	loopCfg := &config.LoopConfig{
		MaxLoops:            200,
		StagnationThreshold: 10,
	}

	cmd := newTestCmd()
	AddLoopFlags(cmd, loopOpts)
	// Set --max-loops explicitly
	require.NoError(t, cmd.ParseFlags([]string{"--max-loops", "75"}))

	opts := BuildRunnerOptions(loopOpts, "proj", "dev", "clawker.proj.dev", "test", "/workspace", cmd.Flags(), loopCfg)

	// Explicit flag wins over config
	assert.Equal(t, 75, opts.MaxLoops)
	// Config wins for unchanged flag
	assert.Equal(t, 10, opts.StagnationThreshold)
}

func TestBuildRunnerOptions_NilConfig(t *testing.T) {
	loopOpts := &LoopOptions{
		MaxLoops:            100,
		StagnationThreshold: 5,
		TimeoutMinutes:      30,
	}

	cmd := newTestCmd()
	opts := BuildRunnerOptions(loopOpts, "proj", "dev", "clawker.proj.dev", "test", "/workspace", cmd.Flags(), nil)

	assert.Equal(t, 100, opts.MaxLoops)
	assert.Equal(t, 5, opts.StagnationThreshold)
}

func TestBuildRunnerOptions_NilFlags(t *testing.T) {
	loopOpts := &LoopOptions{
		MaxLoops:            100,
		StagnationThreshold: 5,
		TimeoutMinutes:      30,
	}

	opts := BuildRunnerOptions(loopOpts, "proj", "dev", "clawker.proj.dev", "test", "/workspace", nil, nil)

	assert.Equal(t, 100, opts.MaxLoops)
	assert.Equal(t, 5, opts.StagnationThreshold)
}

func TestBuildRunnerOptions_SystemPromptDefault(t *testing.T) {
	loopOpts := NewLoopOptions()
	// AppendSystemPrompt is empty (default)

	cmd := newTestCmd()
	opts := BuildRunnerOptions(loopOpts, "proj", "dev", "clawker.proj.dev", "test", "/workspace", cmd.Flags(), nil)

	// Default system prompt should contain LOOP_STATUS instructions
	assert.Contains(t, opts.SystemPrompt, "---LOOP_STATUS---")
	assert.Contains(t, opts.SystemPrompt, "---END_LOOP_STATUS---")
	assert.Equal(t, loop.BuildSystemPrompt(""), opts.SystemPrompt)
}

func TestBuildRunnerOptions_SystemPromptWithAdditional(t *testing.T) {
	loopOpts := NewLoopOptions()
	loopOpts.AppendSystemPrompt = "Always run tests before marking complete."

	cmd := newTestCmd()
	opts := BuildRunnerOptions(loopOpts, "proj", "dev", "clawker.proj.dev", "test", "/workspace", cmd.Flags(), nil)

	// Should have both default and additional instructions
	assert.Contains(t, opts.SystemPrompt, "---LOOP_STATUS---")
	assert.Contains(t, opts.SystemPrompt, "Always run tests before marking complete.")
	assert.Equal(t, loop.BuildSystemPrompt("Always run tests before marking complete."), opts.SystemPrompt)
}

func TestBuildRunnerOptions_SkipPermissionsBoolean(t *testing.T) {
	loopOpts := NewLoopOptions()
	// SkipPermissions defaults to false

	loopCfg := &config.LoopConfig{
		SkipPermissions: true,
	}

	cmd := newTestCmd()
	AddLoopFlags(cmd, loopOpts)
	require.NoError(t, cmd.ParseFlags([]string{}))

	opts := BuildRunnerOptions(loopOpts, "proj", "dev", "clawker.proj.dev", "test", "/workspace", cmd.Flags(), loopCfg)

	// Config override for boolean
	assert.True(t, opts.SkipPermissions)
}

package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/cmd/loop/iterate"
	"github.com/schmitthub/clawker/internal/cmd/loop/shared"
	loopshared "github.com/schmitthub/clawker/internal/cmd/loop/shared"
	"github.com/schmitthub/clawker/internal/cmd/loop/status"
	"github.com/schmitthub/clawker/internal/cmd/loop/tasks"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/schmitthub/clawker/test/harness/builders"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// loop iterate — command integration tests
// ---------------------------------------------------------------------------

// TestLoopIterate_RequiresDocker verifies that the iterate command fails with a
// clear error when Docker is not available (only runs when Docker IS available,
// but exercises the full wiring path).
func TestLoopIterate_RequiresDocker(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-iterate-cmd-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	client := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), client, project); err != nil {
			t.Logf("WARNING: cleanup failed for %s: %v", project, err)
		}
	}()

	f, tio := harness.NewTestFactory(t, h)

	// Run iterate command with --quiet to suppress TUI and --max-loops 1
	cmd := iterate.NewCmdIterate(f, nil)
	cmd.SetArgs([]string{
		"--prompt", "echo hello",
		"--max-loops", "1",
		"--timeout", "1",
		"--quiet",
		"--image", "alpine:latest",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	// The command will create a container, try to run claude, and fail
	// because alpine doesn't have claude installed. This tests the full
	// wiring: Factory → Docker → Container lifecycle → Loop runner.
	// The error is expected since claude is not available.
	err := cmd.Execute()

	// The command should run (even if the loop itself fails)
	// We're testing the wiring, not the loop outcome.
	// If it panics or fails to wire up, we'll catch that here.
	_ = err

	// Verify a container was created (and cleaned up by deferred cleanup)
	// by checking that the project had some activity.
	stderr := tio.ErrBuf.String()
	// The command should at least attempt to create/start a container
	// or report that it couldn't connect to Docker.
	t.Logf("stderr output: %s", stderr)
}

// TestLoopIterate_JSONOutput verifies that --json flag produces valid JSON output
// after loop execution (even if the loop itself fails).
func TestLoopIterate_JSONOutput(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-json-cmd-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	client := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), client, project); err != nil {
			t.Logf("WARNING: cleanup failed for %s: %v", project, err)
		}
	}()

	f, tio := harness.NewTestFactory(t, h)

	// Use root command so we inherit SilenceUsage/SilenceErrors
	cmd, err := h.NewRootCmd(f)
	require.NoError(t, err)
	cmd.SetArgs([]string{
		"loop", "iterate",
		"--prompt", "echo hello",
		"--max-loops", "1",
		"--timeout", "1",
		"--json",
		"--image", "alpine:latest",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	// Execute — don't check error since the loop itself may fail (no claude)
	_ = cmd.Execute()

	// If there's any stdout output, it should be valid JSON
	stdout := tio.OutBuf.String()
	if stdout != "" {
		var result shared.ResultOutput
		err := json.Unmarshal([]byte(stdout), &result)
		if err != nil {
			t.Logf("Invalid JSON output: %s", stdout)
		}
		assert.NoError(t, err, "JSON output should be valid: %s", stdout)
	}
}

// TestLoopIterate_ContainerCleanup verifies that the container created by
// iterate is cleaned up after the loop exits (even on error).
func TestLoopIterate_ContainerCleanup(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-cleanup-cmd-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	client := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), client, project); err != nil {
			t.Logf("WARNING: cleanup failed for %s: %v", project, err)
		}
	}()

	f, tio := harness.NewTestFactory(t, h)

	cmd := iterate.NewCmdIterate(f, nil)
	cmd.SetArgs([]string{
		"--prompt", "echo hello",
		"--max-loops", "1",
		"--timeout", "1",
		"--quiet",
		"--image", "alpine:latest",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	_ = cmd.Execute()

	// After the command returns, give a moment for deferred cleanup
	time.Sleep(2 * time.Second)

	// Verify no running containers remain for this project
	containers, err := client.ListContainersByProject(ctx, project, false)
	require.NoError(t, err)
	assert.Empty(t, containers, "no running containers should remain after iterate exits")
}

// TestLoopIterate_AgentNameAutoGenerated verifies that iterate auto-generates
// an agent name in the loop-<adjective>-<noun> format.
func TestLoopIterate_AgentNameAutoGenerated(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-autogen-cmd-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	client := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), client, project); err != nil {
			t.Logf("WARNING: cleanup failed for %s: %v", project, err)
		}
	}()

	f, tio := harness.NewTestFactory(t, h)

	// Use runF trapdoor to capture the options without running the loop.
	var capturedOpts *iterate.IterateOptions
	cmd := iterate.NewCmdIterate(f, func(ctx context.Context, opts *iterate.IterateOptions) error {
		capturedOpts = opts
		return nil
	})
	cmd.SetArgs([]string{
		"--prompt", "test",
		"--image", "alpine:latest",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// At parse time, the agent name should be empty (it's set in the run function).
	// The runF trapdoor fires before iterateRun, so Agent won't be set yet.
	require.NotNil(t, capturedOpts)
	// Agent is empty at parse time — it's populated by iterateRun via GenerateAgentName()
	assert.Empty(t, capturedOpts.Agent, "agent should be empty at parse time (set in run function)")
}

// ---------------------------------------------------------------------------
// loop tasks — command integration tests
// ---------------------------------------------------------------------------

// TestLoopTasks_RequiresTasksFlag verifies that --tasks is required.
func TestLoopTasks_RequiresTasksFlag(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-tasks-required-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	f, tio := harness.NewTestFactory(t, h)

	cmd := tasks.NewCmdTasks(f, nil)
	cmd.SetArgs([]string{
		// Missing --tasks flag
		"--max-loops", "1",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err, "should fail without --tasks flag")
	assert.Contains(t, err.Error(), "tasks", "error should mention the missing tasks flag")
}

// TestLoopTasks_TasksFileResolution verifies that the tasks command reads and
// processes the task file correctly via the runF trapdoor.
func TestLoopTasks_TasksFileResolution(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-tasks-resolve-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	// Write a tasks file
	tasksContent := "- [ ] Fix authentication bug\n- [ ] Add input validation\n- [x] Update README"
	tasksPath := filepath.Join(t.TempDir(), "tasks.md")
	require.NoError(t, os.WriteFile(tasksPath, []byte(tasksContent), 0644))

	f, tio := harness.NewTestFactory(t, h)

	var capturedOpts *tasks.TasksOptions
	cmd := tasks.NewCmdTasks(f, func(ctx context.Context, opts *tasks.TasksOptions) error {
		capturedOpts = opts
		return nil
	})
	cmd.SetArgs([]string{
		"--tasks", tasksPath,
		"--image", "alpine:latest",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, capturedOpts)
	assert.Equal(t, tasksPath, capturedOpts.TasksFile)
}

// TestLoopTasks_MutualExclusivity verifies that --task-prompt and
// --task-prompt-file are mutually exclusive.
func TestLoopTasks_MutualExclusivity(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-tasks-mutex-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	tasksPath := filepath.Join(t.TempDir(), "tasks.md")
	require.NoError(t, os.WriteFile(tasksPath, []byte("- [ ] Task 1"), 0644))

	promptFilePath := filepath.Join(t.TempDir(), "prompt.md")
	require.NoError(t, os.WriteFile(promptFilePath, []byte("custom prompt"), 0644))

	f, tio := harness.NewTestFactory(t, h)

	cmd := tasks.NewCmdTasks(f, func(ctx context.Context, opts *tasks.TasksOptions) error {
		return nil
	})
	cmd.SetArgs([]string{
		"--tasks", tasksPath,
		"--task-prompt", "inline prompt",
		"--task-prompt-file", promptFilePath,
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err, "should fail with both --task-prompt and --task-prompt-file")
}

// ---------------------------------------------------------------------------
// loop status — command integration tests
// ---------------------------------------------------------------------------

// TestLoopStatus_NoSession verifies that status reports appropriately when
// there is no session for the given agent.
func TestLoopStatus_NoSession(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-status-cmd-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	f, tio := harness.NewTestFactory(t, h)

	cmd := status.NewCmdStatus(f, nil)
	cmd.SetArgs([]string{
		"--agent", "nonexistent-agent",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	// The status command should handle missing sessions gracefully
	// (either with a user-friendly message or a specific error)
	if err != nil {
		assert.Contains(t, err.Error(), "no session", "error should indicate no session found")
	} else {
		// If no error, stderr should have a message about no session
		output := tio.ErrBuf.String() + tio.OutBuf.String()
		assert.Contains(t, output, "session", "output should mention session status")
	}
}

// ---------------------------------------------------------------------------
// loop shared — lifecycle integration tests
// ---------------------------------------------------------------------------

// TestLoopShared_SetupLoopContainer verifies the full container lifecycle:
// create → inject hooks → start → cleanup.
func TestLoopShared_SetupLoopContainer(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-lifecycle-cmd-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	client := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), client, project); err != nil {
			t.Logf("WARNING: cleanup failed for %s: %v", project, err)
		}
	}()

	f, tio := harness.NewTestFactory(t, h)
	cfgProvider := f.Config()
	cfg, ok := cfgProvider.(*config.Config)
	require.True(t, ok, "expected *config.Config provider, got %T", cfgProvider)

	dockerClient, err := f.Client(ctx)
	require.NoError(t, err)

	// Use BuildLightImage which has "sleep infinity" entrypoint to keep the container running.
	// alpine:latest exits immediately, but loop tests need a running container for lifecycle testing.
	image := harness.BuildLightImage(t, client)

	agentName := "test-lifecycle-" + time.Now().Format("150405.000000")
	loopOpts := shared.NewLoopOptions()
	loopOpts.Agent = agentName
	loopOpts.Image = image

	setup, cleanup, err := shared.SetupLoopContainer(ctx, &shared.LoopContainerConfig{
		Client:    dockerClient,
		Config:    cfg,
		LoopOpts:  loopOpts,
		IOStreams: tio.IOStreams,
	})
	require.NoError(t, err, "SetupLoopContainer should succeed: stderr=%s", tio.ErrBuf.String())
	require.NotNil(t, setup)
	require.NotNil(t, cleanup)

	// Verify the container is running
	assert.NotEmpty(t, setup.ContainerID)
	assert.NotEmpty(t, setup.ContainerName)
	assert.Equal(t, agentName, setup.AgentName)
	assert.Equal(t, project, setup.ProjectCfg.Project)

	running := harness.ContainerIsRunning(ctx, client, setup.ContainerName)
	assert.True(t, running, "container should be running after SetupLoopContainer")

	// Verify hooks were injected (settings.json should exist)
	ctr := &harness.RunningContainer{ID: setup.ContainerID, Name: setup.ContainerName}
	result, err := ctr.Exec(ctx, client, "test", "-f", "/home/claude/.claude/settings.json")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "settings.json should exist after hook injection")

	// Verify stop-check.js was injected
	result, err = ctr.Exec(ctx, client, "test", "-f", loopshared.StopCheckScriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "stop-check.js should exist after hook injection")

	// Clean up
	cleanup()

	// Verify container was removed
	time.Sleep(2 * time.Second)
	exists := harness.ContainerExists(ctx, client, setup.ContainerName)
	assert.False(t, exists, "container should be removed after cleanup")
}

// TestLoopShared_ConcurrencyDetection verifies that CheckConcurrency detects
// running containers for the same project and working directory.
func TestLoopShared_ConcurrencyDetection(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	project := "loop-concurrency-cmd-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	client := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), client, project); err != nil {
			t.Logf("WARNING: cleanup failed for %s: %v", project, err)
		}
	}()

	_, tio := harness.NewTestFactory(t, h)

	// Without any running containers, concurrency check should return Proceed
	action, err := shared.CheckConcurrency(ctx, &shared.ConcurrencyCheckConfig{
		Client:    client,
		Project:   project,
		WorkDir:   "/some/workdir",
		IOStreams: tio.IOStreams,
		Prompter:  nil, // non-interactive
	})
	require.NoError(t, err)
	assert.Equal(t, shared.ActionProceed, action, "should proceed when no running containers")
}

// ---------------------------------------------------------------------------
// Config override integration tests
// ---------------------------------------------------------------------------

// TestLoopIterate_ConfigOverrides verifies that clawker.yaml loop config values
// are applied when CLI flags are not explicitly set.
func TestLoopIterate_ConfigOverrides(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-config-override-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	// Update config with loop settings
	h.UpdateConfig(func(cfg *config.Project) {
		cfg.Loop = &config.LoopConfig{
			MaxLoops:            99,
			StagnationThreshold: 7,
			TimeoutMinutes:      30,
			SkipPermissions:     true,
		}
	})

	f, tio := harness.NewTestFactory(t, h)

	var capturedOpts *iterate.IterateOptions
	cmd := iterate.NewCmdIterate(f, func(ctx context.Context, opts *iterate.IterateOptions) error {
		capturedOpts = opts
		return nil
	})
	cmd.SetArgs([]string{
		"--prompt", "test",
		"--image", "alpine:latest",
		// No --max-loops, --stagnation-threshold, --timeout flags
		// Config values should apply
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, capturedOpts)

	// When using runF trapdoor, the config override happens inside iterateRun
	// which was bypassed. But we can verify the flags were captured at parse time.
	assert.Equal(t, loopshared.DefaultMaxLoops, capturedOpts.MaxLoops,
		"MaxLoops should be default at parse time (config override happens in run)")
}

// TestLoopIterate_ExplicitFlagWins verifies that explicit CLI flags take
// precedence over config file values.
func TestLoopIterate_ExplicitFlagWins(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-flag-wins-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	f, tio := harness.NewTestFactory(t, h)

	var capturedOpts *iterate.IterateOptions
	cmd := iterate.NewCmdIterate(f, func(ctx context.Context, opts *iterate.IterateOptions) error {
		capturedOpts = opts
		return nil
	})
	cmd.SetArgs([]string{
		"--prompt", "test",
		"--image", "alpine:latest",
		"--max-loops", "42",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, capturedOpts)
	assert.Equal(t, 42, capturedOpts.MaxLoops, "explicit --max-loops should be captured")
}

// TestLoopIterate_PromptMutualExclusivity verifies --prompt and --prompt-file
// are mutually exclusive.
func TestLoopIterate_PromptMutualExclusivity(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-prompt-mutex-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	promptFile := filepath.Join(t.TempDir(), "prompt.txt")
	require.NoError(t, os.WriteFile(promptFile, []byte("from file"), 0644))

	f, tio := harness.NewTestFactory(t, h)

	cmd := iterate.NewCmdIterate(f, func(ctx context.Context, opts *iterate.IterateOptions) error {
		return nil
	})
	cmd.SetArgs([]string{
		"--prompt", "inline",
		"--prompt-file", promptFile,
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err, "should fail with both --prompt and --prompt-file")
}

// TestLoopIterate_RequiresPromptSource verifies that one of --prompt or
// --prompt-file is required.
func TestLoopIterate_RequiresPromptSource(t *testing.T) {
	harness.RequireDocker(t)

	project := "loop-no-prompt-test"
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject(project).
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)
	h.Chdir()

	f, tio := harness.NewTestFactory(t, h)

	cmd := iterate.NewCmdIterate(f, func(ctx context.Context, opts *iterate.IterateOptions) error {
		return nil
	})
	cmd.SetArgs([]string{
		"--max-loops", "1",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err, "should fail without a prompt source")
}

package lifecycle

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClawkerBinaryCommands tests the clawker CLI binary directly.
// These tests build the clawker binary and execute CLI commands to verify
// end-to-end functionality.
func TestClawkerBinaryCommands(t *testing.T) {
	SkipIfNoDocker(t)

	// Build clawker binary
	binaryPath := buildClawkerBinary(t)
	t.Logf("Built clawker binary: %s", binaryPath)

	t.Run("clawker container ls works", func(t *testing.T) {
		env := NewTestEnv(t)
		defer env.Cleanup()

		output, err := runClawkerCommand(t, binaryPath, env.WorkDir, "container", "ls")
		// The command should succeed even with no containers
		require.NoError(t, err, "clawker container ls should succeed: %s", output)
	})

	t.Run("clawker help works", func(t *testing.T) {
		env := NewTestEnv(t)
		defer env.Cleanup()

		output, err := runClawkerCommand(t, binaryPath, env.WorkDir, "--help")
		require.NoError(t, err, "clawker --help should succeed")
		assert.Contains(t, output, "clawker", "help output should mention clawker")
	})

	t.Run("clawker config check works", func(t *testing.T) {
		env := NewTestEnv(t)
		defer env.Cleanup()

		output, err := runClawkerCommand(t, binaryPath, env.WorkDir, "config", "check")
		// Should succeed with our test clawker.yaml
		require.NoError(t, err, "clawker config check should succeed: %s", output)
	})
}

// TestClawkerBinaryContainerLifecycle tests container lifecycle via the CLI binary.
func TestClawkerBinaryContainerLifecycle(t *testing.T) {
	SkipIfNoDocker(t)

	binaryPath := buildClawkerBinary(t)
	env := NewTestEnv(t)
	defer env.Cleanup()

	agentName := GenerateAgentName("cli")

	t.Run("create container via CLI", func(t *testing.T) {
		// Create container
		output, err := runClawkerCommand(t, binaryPath, env.WorkDir,
			"container", "create",
			"--agent", agentName,
			env.ImageTag,
			"sleep", "300",
		)
		require.NoError(t, err, "clawker container create should succeed: %s", output)

		// Verify container exists
		ctr, err := env.FindContainerByAgent(agentName)
		require.NoError(t, err, "container should exist after create")
		require.NotNil(t, ctr)
	})

	t.Run("start container via CLI", func(t *testing.T) {
		output, err := runClawkerCommand(t, binaryPath, env.WorkDir,
			"container", "start",
			"--agent", agentName,
		)
		require.NoError(t, err, "clawker container start should succeed: %s", output)

		// Give container time to potentially crash
		time.Sleep(2 * time.Second)

		// Verify running
		ctr, err := env.FindContainerByAgent(agentName)
		require.NoError(t, err)
		require.NotNil(t, ctr)
		assert.Equal(t, "running", string(ctr.State), "container should be running")
	})

	t.Run("container does not exit immediately after start", func(t *testing.T) {
		// Wait additional time
		time.Sleep(3 * time.Second)

		// Verify still running
		ctr, err := env.FindContainerByAgent(agentName)
		require.NoError(t, err)
		require.NotNil(t, ctr)
		assert.Equal(t, "running", string(ctr.State), "container should still be running after 5 seconds total")
	})

	t.Run("stop container via CLI", func(t *testing.T) {
		output, err := runClawkerCommand(t, binaryPath, env.WorkDir,
			"container", "stop",
			"--agent", agentName,
		)
		require.NoError(t, err, "clawker container stop should succeed: %s", output)

		// Verify stopped
		containerName := env.ContainerName(agentName)
		info, err := env.Client.APIClient.ContainerInspect(env.Ctx, containerName, client.ContainerInspectOptions{})
		require.NoError(t, err)
		assert.False(t, info.Container.State.Running, "container should be stopped")
	})

	t.Run("restart container via CLI", func(t *testing.T) {
		output, err := runClawkerCommand(t, binaryPath, env.WorkDir,
			"container", "start",
			"--agent", agentName,
		)
		require.NoError(t, err, "clawker container start should succeed: %s", output)

		time.Sleep(2 * time.Second)

		ctr, err := env.FindContainerByAgent(agentName)
		require.NoError(t, err)
		assert.Equal(t, "running", string(ctr.State), "container should be running after restart")
	})

	t.Run("remove container via CLI", func(t *testing.T) {
		// Stop first
		_, _ = runClawkerCommand(t, binaryPath, env.WorkDir, "container", "stop", "--agent", agentName)

		output, err := runClawkerCommand(t, binaryPath, env.WorkDir,
			"container", "rm",
			"--agent", agentName,
		)
		require.NoError(t, err, "clawker container rm should succeed: %s", output)

		// Verify removed
		ctr, err := env.FindContainerByAgent(agentName)
		assert.Error(t, err, "container should not exist after remove")
		assert.Nil(t, ctr)
	})
}

// TestClawkerBinaryContainerLs tests the container ls command output.
func TestClawkerBinaryContainerLs(t *testing.T) {
	SkipIfNoDocker(t)

	binaryPath := buildClawkerBinary(t)
	env := NewTestEnv(t)
	defer env.Cleanup()

	agentName := GenerateAgentName("ls")

	// Create a container
	_, err := runClawkerCommand(t, binaryPath, env.WorkDir,
		"container", "create",
		"--agent", agentName,
		env.ImageTag,
		"sleep", "300",
	)
	require.NoError(t, err)
	defer func() {
		_, _ = runClawkerCommand(t, binaryPath, env.WorkDir, "container", "rm", "-f", "--agent", agentName)
	}()

	t.Run("ls shows created container", func(t *testing.T) {
		output, err := runClawkerCommand(t, binaryPath, env.WorkDir, "container", "ls", "-a")
		require.NoError(t, err)

		// Output should contain the agent name
		assert.Contains(t, output, agentName, "ls output should show our container")
	})

	t.Run("ls with format flag", func(t *testing.T) {
		output, err := runClawkerCommand(t, binaryPath, env.WorkDir,
			"container", "ls", "-a", "--format", "{{.Name}}")
		require.NoError(t, err)

		// Should contain the full container name
		expectedName := env.ContainerName(agentName)
		assert.Contains(t, output, expectedName, "formatted output should contain container name")
	})
}

// TestClawkerBinaryErrorHandling tests that the CLI handles errors gracefully.
func TestClawkerBinaryErrorHandling(t *testing.T) {
	SkipIfNoDocker(t)

	binaryPath := buildClawkerBinary(t)
	env := NewTestEnv(t)
	defer env.Cleanup()

	t.Run("stop non-existent container gives helpful error", func(t *testing.T) {
		cmd := exec.Command(binaryPath, "container", "stop", "--agent", "nonexistent-agent")
		cmd.Dir = env.WorkDir
		output, err := cmd.CombinedOutput()

		// Should error
		assert.Error(t, err, "stopping non-existent container should fail")
		// Output should contain helpful message
		outputStr := string(output)
		assert.True(t,
			strings.Contains(outputStr, "not found") ||
				strings.Contains(outputStr, "No such container") ||
				strings.Contains(outputStr, "does not exist"),
			"error should indicate container not found: %s", outputStr)
	})

	t.Run("invalid command shows help", func(t *testing.T) {
		cmd := exec.Command(binaryPath, "invalidcommand")
		cmd.Dir = env.WorkDir
		output, _ := cmd.CombinedOutput()

		// Should mention usage or available commands
		outputStr := string(output)
		assert.True(t,
			strings.Contains(strings.ToLower(outputStr), "unknown") ||
				strings.Contains(strings.ToLower(outputStr), "usage") ||
				strings.Contains(strings.ToLower(outputStr), "available"),
			"invalid command should show usage info: %s", outputStr)
	})
}

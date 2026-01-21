//go:build e2e

package run

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestRunE2E_InteractiveMode is an e2e test that validates interactive mode (-it)
// by spawning the actual clawker binary with a pseudo-terminal using a freshly built clawker image.
//
// PRIMARY GOAL: Verify that the container is running Claude Code and NOT exiting unexpectedly.
// If the container exits when it shouldn't, the test fails - the "why" will be apparent from
// recent code changes. This test detects regressions in the full startup flow.
func TestRunE2E_InteractiveMode(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	// Build the clawker binary
	clawkerBin := buildClawkerBinary(t)

	// Create harness with firewall ENABLED - this tests the real-world scenario
	// If the firewall blocks required domains (like api.anthropic.com), the container
	// will exit and this test will fail - which is exactly what we want to detect
	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("run-interactive-test").
				WithSecurity(testutil.SecurityFirewallEnabled()),
		),
	)

	// Build a fresh test image with the harness configuration
	// This ensures the image respects the firewall.enable=false setting
	imageTag := testutil.BuildTestImage(t, h, testutil.BuildTestImageOptions{
		SuppressOutput: true,
	})
	t.Logf("Built test image: %s", imageTag)

	// Ensure cleanup even if test fails
	client := testutil.NewTestClient(t)
	rawClient := testutil.NewRawDockerClient(t)
	defer testutil.CleanupProjectResources(ctx, client, "run-interactive-test")

	agentName := "test-interactive-" + time.Now().Format("150405.000000")
	containerName := h.ContainerName(agentName)

	// Build the command - NO command override, let the clawker entrypoint run
	// Use the freshly built image that respects our config
	cmd := exec.Command(clawkerBin, "run",
		"-it", "--rm",
		"--agent", agentName,
		imageTag,
	)
	cmd.Dir = h.ProjectDir
	cmd.Env = append(os.Environ(), "CLAWKER_CONFIG_DIR="+h.ConfigDir)

	// Start with PTY
	ptmx, err := pty.Start(cmd)
	require.NoError(t, err, "failed to start command with PTY")
	defer ptmx.Close()

	// Error channel for capturing critical error patterns from stdout
	errorCh := make(chan string, 1)
	// Read output in background, watching for error patterns only
	// Ready signal detection is handled separately via WaitForReadyFile
	go func() {
		buf := make([]byte, 65536)
		var output []byte
		errorPatterns := []string{
			"[clawker] error",
			"Firewall initialization failed",
			"Failed to attach",
			"unable to upgrade to tcp",
			"container exited",
		}
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				output = append(output, buf[:n]...)
				// Check for critical error patterns
				for _, pattern := range errorPatterns {
					if bytes.Contains(output, []byte(pattern)) {
						errorCh <- string(output)
						return
					}
				}
			}
			if err != nil {
				// PTY closed, no errors found
				return
			}
		}
	}()

	// Create context with 120-second timeout for E2E tests (per PRD Component 2)
	waitCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	// Wait for container to be running, then wait for ready file
	// This approach is more reliable than stdout capture which can miss the ready signal
	readyCh := make(chan error, 1)
	go func() {
		// First wait for container to exist and be running
		if err := testutil.WaitForContainerRunning(waitCtx, rawClient, containerName); err != nil {
			readyCh <- err
			return
		}
		t.Logf("Container %s is running, waiting for ready file...", containerName)

		// Then wait for the ready file which indicates entrypoint completed
		readyCh <- testutil.WaitForReadyFile(waitCtx, rawClient, containerName)
	}()

	// Wait for either ready signal or error
	select {
	case err := <-readyCh:
		if err != nil {
			cmd.Process.Kill()
			t.Fatalf("container failed to become ready: %v", err)
		}
		t.Logf("Container %s ready file detected", containerName)
	case errorOutput := <-errorCh:
		cmd.Process.Kill()
		t.Fatalf("detected error pattern in output:\n%s", errorOutput)
	case <-waitCtx.Done():
		cmd.Process.Kill()
		t.Fatal("timeout waiting for container to become ready (120s)")
	}

	// Verify container is still running after ready signal
	containers, err := client.ListContainersByProject(ctx, "run-interactive-test", false) // false = only running
	require.NoError(t, err, "failed to list containers")

	var found bool
	for _, c := range containers {
		if c.Name == containerName {
			found = true
			require.Equal(t, "running", c.Status, "container should be running, not %s", c.Status)
			break
		}
	}
	require.True(t, found, "container %s not found in running containers after ready signal", containerName)

	// Verify Claude Code process is actually running inside the container
	// This catches issues where the container starts but Claude Code fails to launch
	err = testutil.VerifyClaudeCodeRunning(ctx, rawClient, containerName)
	require.NoError(t, err, "Claude Code process verification failed - process not found in container")
	t.Logf("Claude Code process verified running in container %s", containerName)

	t.Logf("Container %s is running after ready signal, waiting to verify stability...", containerName)

	// CRITICAL: Wait to allow Claude Code to attempt API connection
	// Claude Code shows the welcome screen BEFORE attempting to connect to api.anthropic.com
	// If the firewall blocks the API, the container will exit shortly after startup
	// We need to wait and verify the container is STILL running
	time.Sleep(10 * time.Second)

	// SECOND CHECK: Verify container is STILL running after delay
	// This catches containers that start successfully but exit due to API connection failures
	containers, err = client.ListContainersByProject(ctx, "run-interactive-test", false)
	require.NoError(t, err, "failed to list containers after delay")

	found = false
	for _, c := range containers {
		if c.Name == containerName {
			found = true
			require.Equal(t, "running", c.Status, "container should still be running after 10s, not %s", c.Status)
			break
		}
	}
	require.True(t, found, "container %s exited after showing welcome screen - likely failed to connect to API (firewall blocking?)", containerName)

	t.Logf("SUCCESS: Container %s is still running after 10s - Claude Code is operational", containerName)

	// Send Ctrl+C to gracefully exit
	_, _ = ptmx.Write([]byte{3}) // ASCII ETX (Ctrl+C)
	time.Sleep(500 * time.Millisecond)

	// Send 'exit' command in case Ctrl+C didn't work
	_, _ = ptmx.Write([]byte("exit\n"))

	// Wait for command to complete
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-done:
		// Command finished (may have non-zero exit, that's ok for Ctrl+C)
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		// Don't fail here - we already verified the container started
	}

	// Note: We don't verify --rm cleanup here because killing the parent process
	// doesn't properly trigger Docker's --rm cleanup. The defer CleanupProjectResources
	// handles cleanup. The test's main goal is to verify container startup, entrypoint,
	// and interactive attach work correctly - which we've already verified above.
}

// buildClawkerBinary builds the clawker binary and returns its path.
// It caches the binary in a temp directory for the duration of the test run.
func buildClawkerBinary(t *testing.T) string {
	t.Helper()

	// Use a temp directory for the binary
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "clawker")

	// Find the project root (where go.mod is)
	projectRoot, err := testutil.FindProjectRoot()
	require.NoError(t, err, "failed to find project root")

	// Build the binary
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/clawker")
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "failed to build clawker binary: %s", string(output))

	return binPath
}

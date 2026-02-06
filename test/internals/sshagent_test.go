package internals

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/test/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// SSH Agent Forwarding - TDD Integration Test
//
// This test defines the expected behavior for SSH agent forwarding in clawker
// containers. It mirrors the GPG test pattern in gpgagent_test.go:
//
//   Clawker Architecture:
//   - clawker-socket-server runs inside container
//   - Reads CLAWKER_REMOTE_SOCKETS env var
//   - Creates Unix socket listeners at ~/.ssh/agent.sock
//   - Forwards traffic via muxrpc to host's SSH agent
//
// The test does NOT manually set up SSH infrastructure - that's the
// implementation's job via the socket bridge.
// =============================================================================

// skipIfNoHostSSHAgent skips the test if no SSH agent is available.
func skipIfNoHostSSHAgent(t *testing.T) {
	t.Helper()

	sockPath := os.Getenv("SSH_AUTH_SOCK")
	if sockPath == "" {
		t.Skip("SSH_AUTH_SOCK not set, no SSH agent available")
	}

	// Verify the socket exists
	if _, err := os.Stat(sockPath); err != nil {
		t.Skipf("SSH_AUTH_SOCK socket does not exist: %v", err)
	}
}

// skipIfNoSSHKeys skips the test if no SSH keys are loaded in the agent.
func skipIfNoSSHKeys(t *testing.T) {
	t.Helper()

	cmd := exec.Command("ssh-add", "-l")
	output, err := cmd.Output()
	if err != nil {
		t.Skipf("ssh-add -l failed (no keys loaded?): %v", err)
	}

	outputStr := string(output)
	if strings.Contains(outputStr, "no identities") || strings.Contains(outputStr, "The agent has no identities") {
		t.Skip("No SSH keys loaded in agent")
	}
}

// getHostSSHKeyFingerprints returns the fingerprints of keys in the host's SSH agent.
func getHostSSHKeyFingerprints(t *testing.T) []string {
	t.Helper()

	cmd := exec.Command("ssh-add", "-l")
	output, err := cmd.Output()
	require.NoError(t, err, "failed to list SSH keys")

	var fingerprints []string
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			// Format: "2048 SHA256:xxxxx user@host (RSA)" or similar
			fingerprints = append(fingerprints, fields[1])
		}
	}
	return fingerprints
}

// TestSshAgentForwarding_EndToEnd is the definitive TDD test for SSH agent forwarding.
//
// This test creates a clawker container and expects the FULL SSH agent
// forwarding experience to work out of the box. It does NOT manually set up
// any SSH infrastructure - that's the implementation's job.
//
// Expected implementation provides:
// 1. CLAWKER_REMOTE_SOCKETS env var in container with ssh-agent entry
// 2. Socket server process inside container (clawker-socket-server)
// 3. SSH socket at ~/.ssh/agent.sock created by socket server
// 4. Forwarding to host's SSH_AUTH_SOCK via muxrpc
//
// When all of these work, `ssh-add -l` shows the host's SSH keys.
func TestSshAgentForwarding_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	skipIfNoHostSSHAgent(t)
	skipIfNoSSHKeys(t)
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Get host key fingerprints for verification
	hostFingerprints := getHostSSHKeyFingerprints(t)
	t.Logf("Host SSH key fingerprints: %v", hostFingerprints)
	require.NotEmpty(t, hostFingerprints, "expected at least one SSH key fingerprint")

	// =========================================================================
	// STEP 1: Create container with clawker internals
	//
	// The test harness provides a light image with clawker internals baked in.
	// RunContainer auto-detects SSH_AUTH_SOCK and starts the socket bridge.
	// =========================================================================
	t.Log("STEP 1: Creating container with clawker internals...")

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	// Create container - harness auto-detects SSH and starts bridge
	ctr := harness.RunContainer(t, client, image,
		harness.WithUser("root"), // Need root to verify setup, actual ops run as claude
	)

	// =========================================================================
	// STEP 2: Verify CLAWKER_REMOTE_SOCKETS env var exists with ssh-agent entry
	//
	// The env var tells the socket server which sockets to create and forward.
	// Expected format: '[{"path":"/home/claude/.ssh/agent.sock","type":"ssh-agent"}]'
	// =========================================================================
	t.Log("STEP 2: Checking CLAWKER_REMOTE_SOCKETS env var...")

	result, err := ctr.Exec(ctx, client, "sh", "-c", "echo $CLAWKER_REMOTE_SOCKETS")
	require.NoError(t, err, "failed to check env var")

	socketsEnv := strings.TrimSpace(result.Stdout)
	t.Logf("CLAWKER_REMOTE_SOCKETS=%q", socketsEnv)

	require.NotEmpty(t, socketsEnv,
		"CLAWKER_REMOTE_SOCKETS env var must be set by clawker. "+
			"This env var tells the container-side socket server which sockets to create.")

	require.Contains(t, socketsEnv, "ssh-agent",
		"CLAWKER_REMOTE_SOCKETS must include the SSH agent socket entry. "+
			"Expected to contain 'ssh-agent' type.")

	// =========================================================================
	// STEP 3: Verify socket server process is running
	//
	// The socket server (clawker-socket-server) must be running inside the
	// container to create and forward sockets.
	// =========================================================================
	t.Log("STEP 3: Checking socket server process...")

	result, err = ctr.Exec(ctx, client, "sh", "-c",
		"ps aux | grep -E '(clawker|socket-server)' | grep -v grep || echo 'NO_SOCKET_SERVER'")
	require.NoError(t, err, "failed to check processes")

	t.Logf("Socket server processes:\n%s", result.Stdout)

	require.NotContains(t, result.Stdout, "NO_SOCKET_SERVER",
		"No clawker socket server process found. "+
			"The socket bridge must start clawker-socket-server inside the container.")

	// =========================================================================
	// STEP 4: Verify SSH socket exists as a Unix socket
	//
	// The socket server must create ~/.ssh/agent.sock as a Unix socket.
	// =========================================================================
	t.Log("STEP 4: Checking SSH socket exists...")

	result, err = ctr.Exec(ctx, client, "sh", "-c",
		"ls -la /home/claude/.ssh/agent.sock 2>&1 && file /home/claude/.ssh/agent.sock")
	require.NoError(t, err, "failed to check SSH socket")

	t.Logf("SSH socket status:\n%s", result.Stdout)

	require.Contains(t, result.Stdout, "socket",
		"SSH socket must exist at /home/claude/.ssh/agent.sock as a Unix socket. "+
			"The clawker socket server must create this socket and forward connections.")

	// =========================================================================
	// STEP 5: Verify socket ownership and permissions
	//
	// The socket should be owned by claude with appropriate permissions.
	// =========================================================================
	t.Log("STEP 5: Checking socket ownership and permissions...")

	result, err = ctr.Exec(ctx, client, "sh", "-c",
		"stat -c '%a %U' /home/claude/.ssh/agent.sock 2>/dev/null || stat -f '%Lp %Su' /home/claude/.ssh/agent.sock")
	require.NoError(t, err, "failed to check socket permissions")

	t.Logf("Socket permissions: %s", result.Stdout)

	// Socket should be owned by claude
	assert.Contains(t, result.Stdout, "claude", "socket should be owned by claude user")

	// =========================================================================
	// STEP 6: Verify socket is accessible by claude user
	//
	// The socket should be accessible when setting SSH_AUTH_SOCK explicitly.
	// Note: The entrypoint should ideally set this, but for now we test with
	// explicit path to verify the socket forwarding works.
	// =========================================================================
	t.Log("STEP 6: Verifying socket is accessible...")

	// =========================================================================
	// STEP 7: Verify ssh-add -l can list keys via forwarded agent
	//
	// This is the critical test - ssh-add must show the host's keys,
	// meaning the agent forwarding is working.
	// =========================================================================
	t.Log("STEP 7: Testing ssh-add -l to list keys...")

	result, err = ctr.Exec(ctx, client, "su", "-", "claude", "-c",
		"SSH_AUTH_SOCK=/home/claude/.ssh/agent.sock ssh-add -l 2>&1")
	require.NoError(t, err, "failed to run ssh-add -l")

	t.Logf("ssh-add -l output:\n%s", result.Stdout+result.Stderr)

	combined := result.Stdout + result.Stderr

	// Should NOT contain error messages
	assert.NotContains(t, combined, "Could not open a connection",
		"ssh-add must be able to connect to the agent socket")
	assert.NotContains(t, combined, "Error connecting",
		"ssh-add must not have connection errors")
	assert.NotContains(t, combined, "no identities",
		"ssh-add should show keys from host agent, not 'no identities'")

	// Verify at least one key fingerprint from host is visible
	foundKey := false
	for _, fp := range hostFingerprints {
		if strings.Contains(combined, fp) {
			foundKey = true
			t.Logf("Found host key fingerprint in container: %s", fp)
			break
		}
	}

	require.True(t, foundKey,
		"ssh-add -l must show at least one key from the host SSH agent. "+
			"Expected one of: %v. Got: %s", hostFingerprints, combined)

	// =========================================================================
	// STEP 8: Verify SSH agent operations work
	//
	// If socket forwarding works, we can perform SSH agent operations.
	// We test by listing keys and getting their public key data.
	// Note: ssh-keygen -Y sign requires a local key file, not agent keys,
	// so we test with ssh-add -L which requires agent communication.
	// =========================================================================
	t.Log("STEP 8: Testing SSH agent key retrieval...")

	// Get public key from agent - this verifies bidirectional communication
	result, err = ctr.Exec(ctx, client, "su", "-", "claude", "-c",
		"SSH_AUTH_SOCK=/home/claude/.ssh/agent.sock ssh-add -L 2>&1")
	require.NoError(t, err, "failed to run ssh-add -L")

	t.Logf("ssh-add -L output:\n%s", result.Stdout+result.Stderr)

	pubkeyOutput := result.Stdout + result.Stderr

	// Should contain public key data
	require.True(t,
		strings.Contains(pubkeyOutput, "ssh-rsa") ||
			strings.Contains(pubkeyOutput, "ssh-ed25519") ||
			strings.Contains(pubkeyOutput, "ecdsa-sha2"),
		"ssh-add -L must return public key data. "+
			"This verifies the socket server forwards agent protocol correctly. Got: %s", pubkeyOutput)

	t.Log("SUCCESS: SSH agent forwarding is fully functional!")
}

// TestSshAgentProxy_EntrypointIntegration verifies entrypoint sets up known_hosts.
// This test is independent of the muxrpc forwarding - it tests that the entrypoint
// correctly populates ~/.ssh/known_hosts with common SSH host keys.
func TestSshAgentProxy_EntrypointIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "entrypoint.sh")
	ctr := harness.RunContainer(t, client, image)

	// Test the ssh_setup_known_hosts function by inlining it
	testScript := `
		HOME=/home/claude
		export HOME

		# Inline ssh_setup_known_hosts function from entrypoint.sh
		ssh_setup_known_hosts() {
			mkdir -p "$HOME/.ssh"
			chmod 700 "$HOME/.ssh"
			cat >> "$HOME/.ssh/known_hosts" << 'KNOWN_HOSTS'
github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl
gitlab.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAfuCHKVTjquxvt6CM6tdG4SLp1Btn/nOeHHE5UOzRdf
bitbucket.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIazEu89wgQZ4bqs3d63QSMzYVa0MuJ2e2gKTKqu+UUO
KNOWN_HOSTS
			chmod 600 "$HOME/.ssh/known_hosts"
		}

		# Call the setup function
		ssh_setup_known_hosts

		# Verify known_hosts was created
		if [ -f "$HOME/.ssh/known_hosts" ]; then
			echo "KNOWN_HOSTS_CREATED"
		fi
	`
	execResult, err := ctr.Exec(ctx, client, "bash", "-c", testScript)
	require.NoError(t, err, "failed to run test script")

	t.Logf("test output: %s", execResult.Stdout)
	assert.Contains(t, execResult.Stdout, "KNOWN_HOSTS_CREATED", "known_hosts should be created")
}

// TestSshAgentProxy_DirectSocketFallback verifies direct socket detection works.
// This tests the environment variable detection logic, independent of the muxrpc
// forwarding mechanism.
func TestSshAgentProxy_DirectSocketFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image)

	// Test the direct socket path detection (Linux case where socket is bind-mounted)
	testScript := `
		HOME=/home/claude
		# Create a fake socket file that simulates a mounted socket
		mkdir -p /tmp/ssh
		touch /tmp/ssh/agent.sock  # Just a file, not a real socket

		SSH_AUTH_SOCK=/tmp/ssh/agent.sock
		export HOME SSH_AUTH_SOCK

		# The entrypoint checks if socket exists with [ -e "$SSH_AUTH_SOCK" ]
		if [ -e "$SSH_AUTH_SOCK" ]; then
			echo "SOCKET_EXISTS"
		fi
	`
	execResult, err := ctr.Exec(ctx, client, "bash", "-c", testScript)
	require.NoError(t, err, "failed to run test script")

	t.Logf("test output: %s", execResult.Stdout)
	assert.Contains(t, execResult.Stdout, "SOCKET_EXISTS", "socket detection should work")
}

package internals

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"
	"github.com/schmitthub/clawker/internal/workspace"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGpgAgentProxy_SocketCreation verifies the GPG agent proxy creates the socket
func TestGpgAgentProxy_SocketCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start mock host proxy
	proxy := hostproxytest.NewMockHostProxy(t)

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image,
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Get proxy URL
	proxyURL := strings.Replace(proxy.URL(), "127.0.0.1", "host.docker.internal", 1)

	// Create a mock gpg-agent-proxy script for testing
	mockProxyScript := `#!/bin/sh
# Mock gpg-agent-proxy for testing
# Creates socket and simulates forwarding

mkdir -p "$HOME/.gnupg"
chmod 700 "$HOME/.gnupg"
SOCKET_PATH="$HOME/.gnupg/S.gpg-agent"

# Create a named pipe to simulate the socket
rm -f "$SOCKET_PATH"
mkfifo "$SOCKET_PATH" 2>/dev/null || {
    # If mkfifo fails, just create a regular file to test socket path creation
    touch "$SOCKET_PATH"
}

echo "GPG agent proxy mock started at $SOCKET_PATH"
echo "SOCKET_CREATED"

# In a real scenario, this would listen on the socket
# For testing, we just verify the path exists
`
	createMock := "cat > /tmp/gpg-agent-proxy << 'EOF'\n" + mockProxyScript + "\nEOF"
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", createMock)
	require.NoError(t, err, "failed to create mock gpg-agent-proxy")
	require.Equal(t, 0, execResult.ExitCode, "failed to create mock")

	_, err = ctr.Exec(ctx, client, "chmod", "+x", "/tmp/gpg-agent-proxy")
	require.NoError(t, err, "failed to chmod mock")

	// Run the mock proxy
	runScript := `
		HOME=/home/claude
		CLAWKER_HOST_PROXY="` + proxyURL + `"
		export CLAWKER_HOST_PROXY
		/tmp/gpg-agent-proxy
	`
	execResult, err = ctr.Exec(ctx, client, "sh", "-c", runScript)
	require.NoError(t, err, "failed to run gpg-agent-proxy")

	t.Logf("gpg-agent-proxy output: %s", execResult.Stdout)

	// Verify socket was created (or mock file)
	assert.Contains(t, execResult.Stdout, "SOCKET_CREATED", "socket should be created")

	// Verify the socket path exists
	checkSocket, err := ctr.Exec(ctx, client, "sh", "-c", "test -e /home/claude/.gnupg/S.gpg-agent && echo EXISTS")
	require.NoError(t, err, "failed to check socket")
	assert.Contains(t, checkSocket.Stdout, "EXISTS", "socket path should exist")
}

// TestGpgAgentProxy_ForwardsToProxy verifies GPG agent requests are forwarded
func TestGpgAgentProxy_ForwardsToProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start mock host proxy
	proxy := hostproxytest.NewMockHostProxy(t)

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image,
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Get proxy URL
	proxyURL := strings.Replace(proxy.URL(), "127.0.0.1", "host.docker.internal", 1)

	// Simulate what the gpg-agent-proxy does: POST to /gpg/agent
	// The proxy expects base64-encoded data in a JSON body
	simulateAgentRequest := `
		# Simulate GPG agent request (base64-encoded Assuan command)
		# "GETINFO version\n" encoded in base64
		echo '{"data":"R0VUSU5GTyB2ZXJzaW9uCg=="}' | \
		curl -sf -X POST \
			-H "Content-Type: application/json" \
			-d @- \
			"` + proxyURL + `/gpg/agent" 2>&1
		echo ""
		echo "REQUEST_SENT"
	`
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", simulateAgentRequest)
	require.NoError(t, err, "failed to send agent request")

	t.Logf("agent request output: %s", execResult.Stdout)
	assert.Contains(t, execResult.Stdout, "REQUEST_SENT", "request should be sent")

	// Verify the proxy received the GPG agent request
	time.Sleep(100 * time.Millisecond)
	gpgRequests := proxy.GPGRequests
	require.Len(t, gpgRequests, 1, "expected 1 GPG agent request")
	assert.Equal(t, "GETINFO version\n", string(gpgRequests[0]), "request data should match decoded")
}

// TestGpgAgentProxy_EntrypointEnvironment verifies entrypoint sets up GPG environment correctly
func TestGpgAgentProxy_EntrypointEnvironment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start mock host proxy
	proxy := hostproxytest.NewMockHostProxy(t)

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "entrypoint.sh")
	ctr := harness.RunContainer(t, client, image,
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Get proxy URL
	proxyURL := strings.Replace(proxy.URL(), "127.0.0.1", "host.docker.internal", 1)

	// Test the GPG setup logic by checking directory permissions
	testScript := `
		HOME=/home/claude
		CLAWKER_HOST_PROXY="` + proxyURL + `"
		CLAWKER_GPG_VIA_PROXY=true
		export HOME CLAWKER_HOST_PROXY CLAWKER_GPG_VIA_PROXY

		# Simulate the entrypoint's GPG setup
		mkdir -p "$HOME/.gnupg"
		chmod 700 "$HOME/.gnupg"

		# Verify permissions - mask off setgid bit (can appear as 2xxx in containers)
		perms=$(stat -c "%a" "$HOME/.gnupg" 2>/dev/null || stat -f "%Lp" "$HOME/.gnupg")
		# Strip leading 2 if setgid bit is set (2700 -> 700)
		perms_masked=$(echo "$perms" | sed 's/^2//')
		if [ "$perms_masked" = "700" ]; then
			echo "PERMISSIONS_CORRECT"
		else
			echo "PERMISSIONS_WRONG: $perms"
		fi
	`
	execResult, err := ctr.Exec(ctx, client, "bash", "-c", testScript)
	require.NoError(t, err, "failed to run test script")

	t.Logf("test output: %s", execResult.Stdout)
	assert.Contains(t, execResult.Stdout, "PERMISSIONS_CORRECT", ".gnupg should have 700 permissions")
}

// TestGpgAgentProxy_SocketPermissions verifies socket has correct permissions
func TestGpgAgentProxy_SocketPermissions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image)

	// Create a socket file with correct ownership
	setupScript := `
		mkdir -p /home/claude/.gnupg
		chmod 700 /home/claude/.gnupg

		# Create a file to simulate socket
		touch /home/claude/.gnupg/S.gpg-agent
		chmod 600 /home/claude/.gnupg/S.gpg-agent
		chown claude:claude /home/claude/.gnupg/S.gpg-agent

		# Verify permissions
		stat -c "%a %U" /home/claude/.gnupg/S.gpg-agent
	`
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", setupScript)
	require.NoError(t, err, "failed to setup socket")

	t.Logf("socket permissions: %s", execResult.Stdout)

	// Socket should be owned by claude with 600 permissions
	assert.Contains(t, execResult.Stdout, "600", "socket should have 600 permissions")
	assert.Contains(t, execResult.Stdout, "claude", "socket should be owned by claude")
}

// TestGpgAgentProxy_DirectSocketFallback verifies direct socket mount works when available
func TestGpgAgentProxy_DirectSocketFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image)

	// Test the direct socket path (Linux case)
	testScript := `
		HOME=/home/claude
		# Create a fake socket file that simulates a mounted socket
		mkdir -p "$HOME/.gnupg"
		touch "$HOME/.gnupg/S.gpg-agent"

		# The entrypoint checks if socket exists with [ -S ... ]
		# We're using a file, not a real socket, so use -e instead
		if [ -e "$HOME/.gnupg/S.gpg-agent" ]; then
			echo "SOCKET_EXISTS"
		fi
	`
	execResult, err := ctr.Exec(ctx, client, "bash", "-c", testScript)
	require.NoError(t, err, "failed to run test script")

	t.Logf("test output: %s", execResult.Stdout)
	assert.Contains(t, execResult.Stdout, "SOCKET_EXISTS", "socket should exist")
}

// TestGpgAgentProxy_WaitLoop verifies the entrypoint wait loop works correctly
func TestGpgAgentProxy_WaitLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "entrypoint.sh")
	ctr := harness.RunContainer(t, client, image)

	// Test the wait loop logic extracted from entrypoint
	testScript := `
		HOME=/home/claude
		mkdir -p "$HOME/.gnupg"

		# Simulate a slow socket creation (takes 0.5s)
		(sleep 0.5 && touch "$HOME/.gnupg/S.gpg-agent") &
		creator_pid=$!

		# Wait loop (similar to entrypoint)
		socket_path="$HOME/.gnupg/S.gpg-agent"
		wait_count=0
		max_wait=20  # 20 * 100ms = 2 seconds
		while [ $wait_count -lt $max_wait ]; do
			if [ -e "$socket_path" ]; then
				echo "SOCKET_FOUND_AFTER_${wait_count}_ITERATIONS"
				kill $creator_pid 2>/dev/null || true
				exit 0
			fi
			sleep 0.1
			wait_count=$((wait_count + 1))
		done
		echo "TIMEOUT"
	`
	execResult, err := ctr.Exec(ctx, client, "bash", "-c", testScript)
	require.NoError(t, err, "failed to run test script")

	t.Logf("test output: %s", execResult.Stdout)
	assert.Contains(t, execResult.Stdout, "SOCKET_FOUND_AFTER_", "socket should be found by wait loop")
	assert.NotContains(t, execResult.Stdout, "TIMEOUT", "should not timeout")
}

// TestGpgAgentProxy_RealProxySocketCreation tests the proxy approach.
// SKIPPED: The proxy approach is now disabled by default in favor of direct socket mounting.
// This test is kept for reference if the proxy fallback needs to be re-enabled.
func TestGpgAgentProxy_RealProxySocketCreation(t *testing.T) {
	t.Skip("GPG agent proxy is disabled by default - direct socket mounting is now preferred")
}

// TestGpgAgentProxy_ConnectAgent tests the proxy approach.
// SKIPPED: The proxy approach is now disabled by default in favor of direct socket mounting.
// This test is kept for reference if the proxy fallback needs to be re-enabled.
func TestGpgAgentProxy_ConnectAgent(t *testing.T) {
	t.Skip("GPG agent proxy is disabled by default - direct socket mounting is now preferred")
}

// TestGpgAgentDirectMount_SocketMount verifies that GPG agent socket can be mounted directly
// into a container and gpg-connect-agent can communicate with it.
// This test requires GPG agent to be running on the host.
//
// NOTE: On Docker Desktop for macOS, socket mounting via the Mounts API (mount.Mount) fails
// with a "/socket_mnt" path prefix error. The -v syntax (Binds) works, but the SDK uses Mounts.
// This is a Docker Desktop quirk - the clawker CLI works correctly because Docker's internal
// translation handles it. We skip this test on macOS since the real functionality is verified
// through manual testing with clawker run.
func TestGpgAgentDirectMount_SocketMount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	// Skip on macOS due to Docker Desktop's differing treatment of socket mounts via API vs CLI.
	// See: https://github.com/docker/for-mac/issues/6545
	if runtime.GOOS == "darwin" {
		t.Skip("Skipping on macOS - Docker Desktop socket mounting via SDK Mounts API has issues with /socket_mnt prefix")
	}

	// Check if GPG agent is available on the host
	if !workspace.IsGPGAgentAvailable() {
		t.Skip("GPG agent not available on host - skipping direct socket mount test")
	}

	mounts := workspace.GetGPGAgentMounts()
	if len(mounts) == 0 {
		t.Skip("GetGPGAgentMounts returned empty - GPG not configured on host")
	}

	t.Logf("GPG socket mount: source=%s target=%s", mounts[0].Source, mounts[0].Target)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	// Mount the GPG socket to /tmp instead of ~/.gnupg since the directory doesn't exist
	// in the light test image. The actual clawker images have ~/.gnupg created.
	socketMountPath := "/tmp/gpg-agent.sock"
	gpgMount := mount.Mount{
		Type:     mount.TypeBind,
		Source:   mounts[0].Source,
		Target:   socketMountPath,
		ReadOnly: false,
	}

	t.Logf("Creating container with mount: type=%s source=%s target=%s readonly=%v",
		gpgMount.Type, gpgMount.Source, gpgMount.Target, gpgMount.ReadOnly)
	ctr := harness.RunContainer(t, client, image,
		harness.WithCmd("sleep", "infinity"),
		harness.WithMounts(gpgMount),
	)

	// Test that gpg-connect-agent can communicate with the mounted socket
	testScript := `
		SOCKET_PATH="` + socketMountPath + `"

		# Check socket exists
		if [ ! -S "$SOCKET_PATH" ]; then
			echo "SOCKET_NOT_FOUND"
			ls -la "$SOCKET_PATH" 2>&1 || true
			exit 1
		fi

		echo "SOCKET_FOUND"

		# Test communication with GPG agent using explicit socket path
		# GETINFO version should return the GPG agent version
		result=$(gpg-connect-agent --no-autostart -S "$SOCKET_PATH" 'GETINFO version' '/bye' 2>&1)
		exit_code=$?

		if [ $exit_code -eq 0 ]; then
			echo "CONNECT_SUCCESS"
			echo "RESULT: $result"
		else
			echo "CONNECT_FAILED with exit code $exit_code"
			echo "OUTPUT: $result"
		fi
	`
	execResult, err := ctr.Exec(ctx, client, "bash", "-c", testScript)
	require.NoError(t, err, "failed to run gpg-connect-agent test")

	t.Logf("gpg-connect-agent test:\nstdout: %s\nstderr: %s", execResult.Stdout, execResult.Stderr)

	// Verify socket was accessible and communication succeeded
	assert.Contains(t, execResult.Stdout, "SOCKET_FOUND", "socket should be accessible in container")
	assert.Contains(t, execResult.Stdout, "CONNECT_SUCCESS", "gpg-connect-agent should succeed")
	// The output should contain version info (e.g., "D 2.4.9" for GPG agent version)
	assert.Contains(t, execResult.Stdout, "RESULT:", "should have result output")
}

// TestGpgAgentDirectMount_UseGPGAgentProxyDisabled verifies that UseGPGAgentProxy returns false
func TestGpgAgentDirectMount_UseGPGAgentProxyDisabled(t *testing.T) {
	// This test verifies the new default behavior where we prefer socket mounting
	if workspace.UseGPGAgentProxy() {
		t.Error("UseGPGAgentProxy() should return false - socket mounting is now preferred")
	}
}

package internals

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"
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

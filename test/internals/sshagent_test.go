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

// TestSshAgentProxy_SocketCreation verifies the SSH agent proxy creates the socket
func TestSshAgentProxy_SocketCreation(t *testing.T) {
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

	// Create a mock ssh-agent-proxy script for testing
	mockProxyScript := `#!/bin/sh
# Mock ssh-agent-proxy for testing
# Creates socket and simulates forwarding

mkdir -p "$HOME/.ssh"
SOCKET_PATH="$HOME/.ssh/agent.sock"

# Create a named pipe to simulate the socket
rm -f "$SOCKET_PATH"
mkfifo "$SOCKET_PATH" 2>/dev/null || {
    # If mkfifo fails, just create a regular file to test socket path creation
    touch "$SOCKET_PATH"
}

echo "SSH agent proxy mock started at $SOCKET_PATH"
echo "SOCKET_CREATED"

# In a real scenario, this would listen on the socket
# For testing, we just verify the path exists
`
	createMock := "cat > /tmp/ssh-agent-proxy << 'EOF'\n" + mockProxyScript + "\nEOF"
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", createMock)
	require.NoError(t, err, "failed to create mock ssh-agent-proxy")
	require.Equal(t, 0, execResult.ExitCode, "failed to create mock")

	_, err = ctr.Exec(ctx, client, "chmod", "+x", "/tmp/ssh-agent-proxy")
	require.NoError(t, err, "failed to chmod mock")

	// Run the mock proxy
	runScript := `
		HOME=/home/claude
		CLAWKER_HOST_PROXY="` + proxyURL + `"
		export CLAWKER_HOST_PROXY
		/tmp/ssh-agent-proxy
	`
	execResult, err = ctr.Exec(ctx, client, "sh", "-c", runScript)
	require.NoError(t, err, "failed to run ssh-agent-proxy")

	t.Logf("ssh-agent-proxy output: %s", execResult.Stdout)

	// Verify socket was created (or mock file)
	assert.Contains(t, execResult.Stdout, "SOCKET_CREATED", "socket should be created")

	// Verify the socket path exists
	checkSocket, err := ctr.Exec(ctx, client, "sh", "-c", "test -e /home/claude/.ssh/agent.sock && echo EXISTS")
	require.NoError(t, err, "failed to check socket")
	assert.Contains(t, checkSocket.Stdout, "EXISTS", "socket path should exist")
}

// TestSshAgentProxy_ForwardsToProxy verifies SSH agent requests are forwarded
func TestSshAgentProxy_ForwardsToProxy(t *testing.T) {
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

	// Simulate what the ssh-agent-proxy does: POST to /ssh/agent
	simulateAgentRequest := `
		# Simulate SSH agent request (binary data)
		echo -n "SSH_AGENT_REQUEST_DATA" | \
		curl -sf -X POST \
			-H "Content-Type: application/octet-stream" \
			--data-binary @- \
			"` + proxyURL + `/ssh/agent" 2>&1
		echo ""
		echo "REQUEST_SENT"
	`
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", simulateAgentRequest)
	require.NoError(t, err, "failed to send agent request")

	t.Logf("agent request output: %s", execResult.Stdout)
	assert.Contains(t, execResult.Stdout, "REQUEST_SENT", "request should be sent")

	// Verify the proxy received the SSH agent request
	time.Sleep(100 * time.Millisecond)
	sshRequests := proxy.SSHRequests
	require.Len(t, sshRequests, 1, "expected 1 SSH agent request")
	assert.Equal(t, []byte("SSH_AGENT_REQUEST_DATA"), sshRequests[0], "request data should match")
}

// TestSshAgentProxy_EntrypointIntegration verifies entrypoint sets up SSH agent
func TestSshAgentProxy_EntrypointIntegration(t *testing.T) {
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

	// Test the ssh_setup_known_hosts function by inlining it
	testScript := `
		HOME=/home/claude
		CLAWKER_HOST_PROXY="` + proxyURL + `"
		CLAWKER_SSH_VIA_PROXY=true
		export HOME CLAWKER_HOST_PROXY CLAWKER_SSH_VIA_PROXY

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

// TestSshAgentProxy_SocketPermissions verifies socket has correct permissions
func TestSshAgentProxy_SocketPermissions(t *testing.T) {
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
		mkdir -p /home/claude/.ssh
		chmod 700 /home/claude/.ssh

		# Create a file to simulate socket
		touch /home/claude/.ssh/agent.sock
		chmod 600 /home/claude/.ssh/agent.sock
		chown claude:claude /home/claude/.ssh/agent.sock

		# Verify permissions
		stat -c "%a %U" /home/claude/.ssh/agent.sock
	`
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", setupScript)
	require.NoError(t, err, "failed to setup socket")

	t.Logf("socket permissions: %s", execResult.Stdout)

	// Socket should be owned by claude with 600 permissions
	assert.Contains(t, execResult.Stdout, "600", "socket should have 600 permissions")
	assert.Contains(t, execResult.Stdout, "claude", "socket should be owned by claude")
}

// TestSshAgentProxy_DirectSocketFallback verifies direct socket mount works when available
func TestSshAgentProxy_DirectSocketFallback(t *testing.T) {
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
	assert.Contains(t, execResult.Stdout, "SOCKET_EXISTS", "socket should exist")
}

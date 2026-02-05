package internals

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/test/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// GPG Agent Forwarding - TDD Integration Test
//
// This test defines the expected behavior for GPG agent forwarding in clawker
// containers. It mirrors VS Code's devcontainer implementation:
//
//   VS Code Architecture:
//   - vscode-remote-containers-server.js runs inside container
//   - Reads REMOTE_CONTAINERS_SOCKETS env var
//   - Creates Unix socket listeners at each path
//   - Forwards traffic via muxrpc to VS Code host
//   - Host connects to real GPG agent
//
//   Clawker Expected Architecture:
//   - clawker-socket-server (or similar) runs inside container
//   - Reads CLAWKER_REMOTE_SOCKETS env var
//   - Creates Unix socket listeners at each path
//   - Forwards traffic via HTTP/muxrpc to hostproxy
//   - Hostproxy connects to host's GPG agent
//
// The test does NOT implement any of this - it DEMANDS the implementation
// provides it. Each assertion failure tells implementers what to build.
// =============================================================================

// skipIfNoHostGPGSigningKey skips the test if the host has no GPG signing key.
func skipIfNoHostGPGSigningKey(t *testing.T) {
	t.Helper()

	cmd := exec.Command("gpg", "--list-secret-keys", "--keyid-format", "long")
	output, err := cmd.Output()
	if err != nil {
		t.Skipf("gpg command failed (GPG not installed or no agent): %v", err)
	}

	if !strings.Contains(string(output), "sec") {
		t.Skip("No GPG signing key found on host")
	}
}

// getHostSigningKeyFingerprint returns the 40-char fingerprint of the host's GPG key.
func getHostSigningKeyFingerprint(t *testing.T) string {
	t.Helper()

	cmd := exec.Command("gpg", "--list-secret-keys", "--keyid-format", "long")
	output, err := cmd.Output()
	require.NoError(t, err, "failed to list GPG secret keys")

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) == 40 && isHexString(trimmed) {
			return trimmed
		}
	}

	t.Fatal("Could not parse GPG key fingerprint from output")
	return ""
}

func isHexString(s string) bool {
	match, _ := regexp.MatchString("^[0-9A-Fa-f]+$", s)
	return match
}

// TestGpgAgentForwarding_EndToEnd is the definitive TDD test for GPG agent forwarding.
//
// This test creates a clawker container and expects the FULL VS Code-like GPG
// forwarding experience to work out of the box. It does NOT manually set up
// any GPG infrastructure - that's the implementation's job.
//
// Expected implementation provides:
// 1. CLAWKER_REMOTE_SOCKETS env var in container (like REMOTE_CONTAINERS_SOCKETS)
// 2. Socket server process inside container (like vscode-remote-containers-server.js)
// 3. GPG socket at ~/.gnupg/S.gpg-agent created by socket server
// 4. Public key exported to ~/.gnupg/pubring.kbx
// 5. Hostproxy forwarding GPG agent protocol to host
//
// When all of these work, `git commit -S` succeeds.
func TestGpgAgentForwarding_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	skipIfNoHostGPGSigningKey(t)
	harness.RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Get host key fingerprint for verification
	hostKeyFP := getHostSigningKeyFingerprint(t)
	t.Logf("Host GPG key fingerprint: %s", hostKeyFP)

	// =========================================================================
	// STEP 1: Create container with clawker internals
	//
	// The test harness provides a light image with GPG and clawker internals.
	// The implementation must ensure hostproxy is running and the container
	// is configured to use it.
	// =========================================================================
	t.Log("STEP 1: Creating container with clawker internals...")

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)

	// Create container - implementation must inject CLAWKER_REMOTE_* env vars
	// and start the socket server process
	ctr := harness.RunContainer(t, client, image,
		harness.WithUser("root"), // Need root to verify setup, actual ops run as claude
	)

	// =========================================================================
	// STEP 2: Verify CLAWKER_REMOTE_SOCKETS env var exists
	//
	// Like VS Code's REMOTE_CONTAINERS_SOCKETS, clawker must set this env var
	// to tell the socket server which sockets to create and forward.
	//
	// Expected format: CLAWKER_REMOTE_SOCKETS='["/path/to/socket1", "/path/to/socket2"]'
	// Must include: "/home/claude/.gnupg/S.gpg-agent"
	// =========================================================================
	t.Log("STEP 2: Checking CLAWKER_REMOTE_SOCKETS env var...")

	result, err := ctr.Exec(ctx, client, "sh", "-c", "echo $CLAWKER_REMOTE_SOCKETS")
	require.NoError(t, err, "failed to check env var")

	socketsEnv := strings.TrimSpace(result.Stdout)
	t.Logf("CLAWKER_REMOTE_SOCKETS=%q", socketsEnv)

	require.NotEmpty(t, socketsEnv,
		"CLAWKER_REMOTE_SOCKETS env var must be set by clawker. "+
			"This env var tells the container-side socket server which sockets to create. "+
			"Expected format: '[\"path1\", \"path2\"]' including GPG socket path.")

	require.Contains(t, socketsEnv, "gpg-agent",
		"CLAWKER_REMOTE_SOCKETS must include the GPG agent socket path. "+
			"Expected to contain '.gnupg/S.gpg-agent' or similar.")

	// =========================================================================
	// STEP 3: Verify socket server process is running
	//
	// Like VS Code's vscode-remote-containers-server.js, clawker must run a
	// socket server process inside the container that:
	// - Parses CLAWKER_REMOTE_SOCKETS
	// - Creates Unix socket listeners at each path
	// - Forwards connections to hostproxy
	// =========================================================================
	t.Log("STEP 3: Checking socket server process...")

	result, err = ctr.Exec(ctx, client, "sh", "-c",
		"ps aux | grep -E '(clawker|socket-server)' | grep -v grep || echo 'NO_SOCKET_SERVER'")
	require.NoError(t, err, "failed to check processes")

	t.Logf("Socket server processes:\n%s", result.Stdout)

	require.NotContains(t, result.Stdout, "NO_SOCKET_SERVER",
		"No clawker socket server process found. "+
			"Clawker must run a socket server inside the container (like VS Code's server.js) "+
			"that creates and forwards Unix sockets listed in CLAWKER_REMOTE_SOCKETS.")

	// =========================================================================
	// STEP 4: Verify GPG socket exists and is owned by socket server
	//
	// The socket server must create ~/.gnupg/S.gpg-agent as a Unix socket.
	// =========================================================================
	t.Log("STEP 4: Checking GPG socket exists...")

	result, err = ctr.Exec(ctx, client, "sh", "-c",
		"ls -la /home/claude/.gnupg/S.gpg-agent 2>&1 && file /home/claude/.gnupg/S.gpg-agent")
	require.NoError(t, err, "failed to check GPG socket")

	t.Logf("GPG socket status:\n%s", result.Stdout)

	require.Contains(t, result.Stdout, "socket",
		"GPG socket must exist at /home/claude/.gnupg/S.gpg-agent. "+
			"The clawker socket server must create this socket and forward connections to hostproxy.")

	// =========================================================================
	// STEP 5: Verify public key was exported to container
	//
	// Like VS Code, clawker must export the host's public GPG key to the
	// container's pubring.kbx so GPG knows which key to use.
	// =========================================================================
	t.Log("STEP 5: Checking public key was exported...")

	result, err = ctr.Exec(ctx, client, "sh", "-c",
		"ls -la /home/claude/.gnupg/pubring.kbx 2>&1 && wc -c /home/claude/.gnupg/pubring.kbx")
	require.NoError(t, err, "failed to check pubring.kbx")

	t.Logf("pubring.kbx status:\n%s", result.Stdout)

	// VS Code's pubring.kbx is 674 bytes. Empty/stub is ~32 bytes.
	require.Contains(t, result.Stdout, "pubring.kbx",
		"pubring.kbx must exist at /home/claude/.gnupg/")

	// Check it's not just an empty stub
	result, err = ctr.Exec(ctx, client, "sh", "-c",
		"stat -c%s /home/claude/.gnupg/pubring.kbx 2>/dev/null || stat -f%z /home/claude/.gnupg/pubring.kbx")
	require.NoError(t, err, "failed to get pubring.kbx size")

	sizeStr := strings.TrimSpace(result.Stdout)
	t.Logf("pubring.kbx size: %s bytes", sizeStr)

	require.NotEqual(t, "32", sizeStr,
		"pubring.kbx is only 32 bytes (empty stub). "+
			"Clawker must export the host's public GPG key to /home/claude/.gnupg/pubring.kbx. "+
			"Expected size ~600+ bytes for a real key.")

	// =========================================================================
	// STEP 6: Verify GPG can see the secret key via agent forwarding
	//
	// This is the critical test - GPG must show "sec" (secret key available)
	// when listing keys, meaning the agent forwarding is working.
	// =========================================================================
	t.Log("STEP 6: Checking GPG sees secret key via agent...")

	result, err = ctr.Exec(ctx, client, "su", "-", "claude", "-c",
		"gpg --homedir /home/claude/.gnupg --list-secret-keys 2>&1")
	require.NoError(t, err, "failed to run gpg --list-secret-keys")

	t.Logf("gpg --list-secret-keys output:\n%s", result.Stdout+result.Stderr)

	combined := result.Stdout + result.Stderr

	assert.Contains(t, combined, hostKeyFP,
		"GPG output must contain host key fingerprint %s. "+
			"This means the public key was correctly exported.", hostKeyFP)

	require.Contains(t, combined, "sec",
		"GPG must show 'sec' marker indicating secret key is accessible via agent. "+
			"This means the socket forwarding to host GPG agent is working. "+
			"Without 'sec', GPG can see the public key but cannot sign.")

	// =========================================================================
	// STEP 7: Verify GPG can sign data
	//
	// If socket forwarding works, GPG can perform signing operations.
	// =========================================================================
	t.Log("STEP 7: Testing GPG signing...")

	result, err = ctr.Exec(ctx, client, "su", "-", "claude", "-c",
		`echo "test data" | gpg --homedir /home/claude/.gnupg --armor --detach-sign 2>&1`)
	require.NoError(t, err, "failed to run gpg sign")

	t.Logf("GPG sign output:\n%s", result.Stdout+result.Stderr)

	signOutput := result.Stdout + result.Stderr

	require.Contains(t, signOutput, "-----BEGIN PGP SIGNATURE-----",
		"GPG signing must produce a valid signature. "+
			"This requires the socket server to forward signing requests to host GPG agent.")

	require.Contains(t, signOutput, "-----END PGP SIGNATURE-----",
		"GPG signature must be complete.")

	// =========================================================================
	// STEP 8: Create git repo and make signed commit
	//
	// This is the ultimate end-to-end test. If this works, GPG forwarding
	// is fully functional for the primary use case: signed git commits.
	// =========================================================================
	t.Log("STEP 8: Testing git signed commit...")

	// Setup git repo as root, then chown to claude
	setupScript := `
		mkdir -p /home/claude/test-repo
		cd /home/claude/test-repo
		git init
		git config user.email "test@example.com"
		git config user.name "Test User"
		git config user.signingkey ` + hostKeyFP + `
		git config gpg.program gpg
		git config commit.gpgsign true
		echo "test content" > testfile.txt
		git add testfile.txt
		chown -R claude:claude /home/claude/test-repo
	`
	result, err = ctr.Exec(ctx, client, "bash", "-c", setupScript)
	require.NoError(t, err, "failed to setup git repo")
	require.Equal(t, 0, result.ExitCode, "git repo setup failed: %s", result.Stderr)

	// Make signed commit as claude user
	commitCmd := `cd /home/claude/test-repo && HOME=/home/claude git commit -S -m "test signed commit" 2>&1`
	result, err = ctr.Exec(ctx, client, "su", "-", "claude", "-c", commitCmd)
	require.NoError(t, err, "failed to run git commit")

	t.Logf("git commit output:\n%s", result.Stdout+result.Stderr)

	require.Equal(t, 0, result.ExitCode,
		"git commit -S must succeed. Exit code was %d. Output: %s",
		result.ExitCode, result.Stdout+result.Stderr)

	// =========================================================================
	// STEP 9: Verify commit signature
	//
	// Confirm the commit was actually signed.
	// =========================================================================
	t.Log("STEP 9: Verifying commit signature...")

	verifyCmd := `cd /home/claude/test-repo && git log --show-signature -1 2>&1`
	result, err = ctr.Exec(ctx, client, "su", "-", "claude", "-c", verifyCmd)
	require.NoError(t, err, "failed to run git log")

	t.Logf("git log --show-signature output:\n%s", result.Stdout+result.Stderr)

	verifyOutput := result.Stdout + result.Stderr

	signatureVerified := strings.Contains(verifyOutput, "Good signature") ||
		strings.Contains(verifyOutput, "gpg: Signature made")

	require.True(t, signatureVerified,
		"git log --show-signature must show the commit was signed. "+
			"Expected 'Good signature' or 'gpg: Signature made'. Got: %s", verifyOutput)

	t.Log("SUCCESS: GPG agent forwarding is fully functional!")
}

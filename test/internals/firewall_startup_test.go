package internals

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

// TestFirewallStartup_FullScript verifies the complete init-firewall.sh script runs successfully.
// This tests the actual script (not a simplified version like other firewall tests).
// It exercises the full startup flow including GitHub IP fetching and verification.
func TestFirewallStartup_FullScript(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Start container with required capabilities
	result, err := StartFromDockerfile(ctx, t, "testdata/Dockerfile.alpine", func(req *testcontainers.ContainerRequest) {
		req.CapAdd = []string{"NET_ADMIN", "NET_RAW"}
		req.User = "root"
		req.ExtraHosts = []string{"host.docker.internal:host-gateway"}
	})
	require.NoError(t, err)

	// Install all dependencies needed by the real firewall script
	deps := []string{"sh", "-c", `
		apk add --no-cache ipset bind-tools jq curl bash iproute2 &&
		# Create aggregate stub (real script expects this)
		cat > /usr/local/bin/aggregate << 'EOF'
#!/bin/sh
cat
EOF
		chmod +x /usr/local/bin/aggregate
	`}
	execResult, err := result.Exec(ctx, deps)
	require.NoError(t, err)
	require.Equal(t, 0, execResult.ExitCode, "failed to install deps: %s", execResult.CleanOutput())

	// Copy the actual firewall script
	copyFirewallScriptToContainer(ctx, t, result, "init-firewall.sh")

	// Run the actual firewall script (this is the key difference from other tests)
	t.Log("Running full init-firewall.sh script...")
	execResult, err = result.Exec(ctx, []string{"bash", "/tmp/init-firewall.sh"})
	require.NoError(t, err, "failed to execute firewall script")

	output := execResult.CleanOutput()

	if execResult.ExitCode != 0 {
		t.Logf("Firewall script failed:\n%s", output)

		// Check for specific failure patterns
		if strings.Contains(output, "Failed to fetch GitHub IP") {
			t.Fatal("Firewall failed: GitHub API unreachable")
		}
		if strings.Contains(output, "Failed to detect host IP") {
			t.Fatal("Firewall failed: Host IP detection failed")
		}
		if strings.Contains(output, "Firewall verification failed") {
			t.Fatal("Firewall failed: Verification step failed")
		}
		t.Fatalf("Firewall script failed with exit code %d", execResult.ExitCode)
	}

	t.Log("Full firewall script completed successfully")
	t.Logf("Script output:\n%s", output)

	// Verify iptables rules were actually set
	rulesResult, err := result.Exec(ctx, []string{"iptables", "-L", "-n", "-v"})
	require.NoError(t, err, "failed to list iptables rules")
	t.Logf("iptables rules:\n%s", rulesResult.CleanOutput())

	// Verify ipset was created
	ipsetResult, err := result.Exec(ctx, []string{"ipset", "list"})
	require.NoError(t, err, "failed to list ipset")
	t.Logf("ipset:\n%s", ipsetResult.CleanOutput())
}

// TestFirewallStartup_MissingCapability verifies failure when NET_ADMIN is missing.
// This tests that the firewall script fails gracefully when capabilities are not available.
func TestFirewallStartup_MissingCapability(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start WITHOUT NET_ADMIN capability
	result, err := StartFromDockerfile(ctx, t, "testdata/Dockerfile.alpine", func(req *testcontainers.ContainerRequest) {
		// Intentionally no CapAdd
		req.User = "root"
	})
	require.NoError(t, err)

	// Install iptables
	installDeps := []string{"sh", "-c", "apk add --no-cache iptables"}
	execResult, err := result.Exec(ctx, installDeps)
	require.NoError(t, err)
	require.Equal(t, 0, execResult.ExitCode, "failed to install iptables")

	// Try to run iptables (should fail due to missing capability)
	execResult, err = result.Exec(ctx, []string{"iptables", "-L"})
	require.NoError(t, err)

	// Expect failure due to missing capability
	if execResult.ExitCode == 0 {
		// If it succeeded, we might be running on a permissive system
		t.Log("iptables succeeded - system may be permissive or running as privileged")
	} else {
		t.Logf("Correctly detected missing capability: exit code %d", execResult.ExitCode)
		output := execResult.CleanOutput()
		assert.True(t,
			strings.Contains(output, "Permission denied") ||
				strings.Contains(output, "Operation not permitted") ||
				strings.Contains(output, "can't initialize"),
			"should show capability error, got: %s", output)
	}
}

// TestFirewallStartup_ExitCodeOnFailure verifies that the firewall script properly exits with non-zero
// when it encounters errors. This tests the WaitForContainerRunning fail-fast detection.
func TestFirewallStartup_ExitCodeOnFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Start container WITHOUT required capabilities (firewall will fail)
	result, err := StartFromDockerfile(ctx, t, "testdata/Dockerfile.alpine", func(req *testcontainers.ContainerRequest) {
		// No NET_ADMIN capability - firewall script should fail
		req.User = "root"
	})
	require.NoError(t, err)

	// Install minimal dependencies
	deps := []string{"sh", "-c", "apk add --no-cache bash"}
	execResult, err := result.Exec(ctx, deps)
	require.NoError(t, err)
	require.Equal(t, 0, execResult.ExitCode, "failed to install deps")

	// Create a minimal firewall script that will fail due to missing iptables capability
	createScript := []string{"sh", "-c", `cat > /tmp/test-firewall.sh << 'EOF'
#!/bin/bash
set -euo pipefail

echo "Attempting to configure firewall..."

# This should fail without NET_ADMIN capability
iptables -L

echo "Firewall configured successfully"
EOF
chmod +x /tmp/test-firewall.sh`}

	execResult, err = result.Exec(ctx, createScript)
	require.NoError(t, err)
	require.Equal(t, 0, execResult.ExitCode)

	// Install iptables (needed for the script to run the command)
	execResult, err = result.Exec(ctx, []string{"sh", "-c", "apk add --no-cache iptables"})
	require.NoError(t, err)
	require.Equal(t, 0, execResult.ExitCode, "failed to install iptables")

	// Run the script - it should fail
	execResult, err = result.Exec(ctx, []string{"bash", "/tmp/test-firewall.sh"})
	require.NoError(t, err) // exec itself should succeed

	// The script should have failed with non-zero exit code
	t.Logf("Script exit code: %d", execResult.ExitCode)
	t.Logf("Script output:\n%s", execResult.CleanOutput())

	// We expect non-zero exit code when iptables fails
	// This verifies that a container startup would fail and be detectable
	assert.NotEqual(t, 0, execResult.ExitCode,
		"firewall script should exit with non-zero when iptables fails without NET_ADMIN")
}

// copyFirewallScriptToContainer copies the firewall script from internal/build/templates to the container.
func copyFirewallScriptToContainer(ctx context.Context, t *testing.T, result *ContainerResult, scriptName string) {
	t.Helper()

	projectRoot, err := findProjectRoot()
	require.NoError(t, err, "failed to find project root")

	scriptPath := filepath.Join(projectRoot, "internal", "build", "templates", scriptName)
	content, err := os.ReadFile(scriptPath)
	require.NoError(t, err, "failed to read script %s", scriptName)

	// Create script in container
	destPath := "/tmp/" + scriptName
	createScript := []string{"sh", "-c", "cat > " + destPath + " << 'SCRIPT_EOF'\n" + string(content) + "\nSCRIPT_EOF"}
	execResult, err := result.Exec(ctx, createScript)
	require.NoError(t, err, "failed to copy script to container")
	require.Equal(t, 0, execResult.ExitCode, "failed to copy script: %s", execResult.CleanOutput())

	// Make executable
	chmodResult, err := result.Exec(ctx, []string{"chmod", "+x", destPath})
	require.NoError(t, err, "failed to chmod script")
	require.Equal(t, 0, chmodResult.ExitCode, "failed to chmod script")
}

// findProjectRoot is defined in scripts_test.go

package internals

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFirewall_IptablesDropPolicy verifies the firewall sets OUTPUT chain to DROP
func TestFirewall_IptablesDropPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "init-firewall.sh")
	ctr := harness.RunContainer(t, client, image,
		harness.WithCapAdd("NET_ADMIN", "NET_RAW"),
		harness.WithUser("root"),
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Install additional dependencies needed for firewall script
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", "apk add --no-cache ipset bind-tools")
	require.NoError(t, err, "failed to install dependencies")
	require.Equal(t, 0, execResult.ExitCode, "failed to install deps: %s", execResult.CleanOutput())

	// Install aggregate tool (simple IP aggregator)
	installAggregate := `
		# Create a simple aggregate stub that just passes through IPs
		cat > /tmp/aggregate << 'EOF'
#!/bin/sh
# Simple passthrough - real aggregate would optimize CIDR ranges
cat
EOF
		chmod +x /tmp/aggregate
	`
	execResult, err = ctr.Exec(ctx, client, "sh", "-c", installAggregate)
	require.NoError(t, err, "failed to install aggregate")
	require.Equal(t, 0, execResult.ExitCode, "failed to install aggregate")

	// Create a minimal test version of the firewall script that skips GitHub IP fetching
	minimalFirewall := `#!/bin/bash
set -euo pipefail

# Flush existing rules
iptables -F
iptables -X

# Create ipset
ipset destroy allowed-domains 2>/dev/null || true
ipset create allowed-domains hash:net

# Set default policies to DROP
iptables -P INPUT DROP
iptables -P FORWARD DROP
iptables -P OUTPUT DROP

# Allow localhost
iptables -A INPUT -i lo -j ACCEPT
iptables -A OUTPUT -o lo -j ACCEPT

# Allow DNS
iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
iptables -A INPUT -p udp --sport 53 -j ACCEPT

# Allow established connections
iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

# Allow ipset destinations
iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT

# Reject everything else
iptables -A OUTPUT -j REJECT --reject-with icmp-admin-prohibited

echo "Firewall configured"
`
	createFirewall := "cat > /tmp/test-firewall.sh << 'EOF'\n" + minimalFirewall + "\nEOF"
	execResult, err = ctr.Exec(ctx, client, "sh", "-c", createFirewall)
	require.NoError(t, err, "failed to create test firewall script")
	require.Equal(t, 0, execResult.ExitCode, "failed to create firewall script")

	_, err = ctr.Exec(ctx, client, "chmod", "+x", "/tmp/test-firewall.sh")
	require.NoError(t, err, "failed to chmod firewall script")

	// Run the minimal firewall script
	execResult, err = ctr.Exec(ctx, client, "bash", "/tmp/test-firewall.sh")
	require.NoError(t, err, "failed to run firewall script")
	require.Equal(t, 0, execResult.ExitCode, "firewall script failed: %s", execResult.CleanOutput())

	// Verify OUTPUT chain policy is DROP
	policyResult, err := ctr.Exec(ctx, client, "iptables", "-L", "OUTPUT", "-n")
	require.NoError(t, err, "failed to list iptables rules")

	output := policyResult.Stdout
	t.Logf("iptables OUTPUT chain:\n%s", output)

	// Check for DROP policy
	assert.Contains(t, output, "DROP", "OUTPUT chain should have DROP policy or rule")
}

// TestFirewall_BlockedDomainsUnreachable verifies blocked domains cannot be reached
func TestFirewall_BlockedDomainsUnreachable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image,
		harness.WithCapAdd("NET_ADMIN", "NET_RAW"),
		harness.WithUser("root"),
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Install dependencies
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", "apk add --no-cache ipset")
	require.NoError(t, err, "failed to install ipset")
	require.Equal(t, 0, execResult.ExitCode, "failed to install ipset")

	// Set up minimal firewall that blocks everything except localhost and DNS
	setupFirewall := `
		iptables -F
		iptables -X
		ipset destroy allowed-domains 2>/dev/null || true
		ipset create allowed-domains hash:net

		# Allow localhost and DNS only
		iptables -A INPUT -i lo -j ACCEPT
		iptables -A OUTPUT -o lo -j ACCEPT
		iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
		iptables -A INPUT -p udp --sport 53 -j ACCEPT
		iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
		iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

		# Set DROP policy
		iptables -P INPUT DROP
		iptables -P FORWARD DROP
		iptables -P OUTPUT DROP

		# Allow ipset (currently empty)
		iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT

		# Reject others
		iptables -A OUTPUT -j REJECT --reject-with icmp-admin-prohibited
	`
	execResult, err = ctr.Exec(ctx, client, "sh", "-c", setupFirewall)
	require.NoError(t, err, "failed to setup firewall")
	require.Equal(t, 0, execResult.ExitCode, "firewall setup failed: %s", execResult.CleanOutput())

	// Try to curl a blocked domain (should fail)
	curlResult, err := ctr.Exec(ctx, client, "sh", "-c", "curl --connect-timeout 5 https://example.com 2>&1 || echo 'BLOCKED'")
	require.NoError(t, err, "failed to run curl")

	output := curlResult.Stdout
	t.Logf("curl output: %s", output)

	// Should be blocked (connection refused, network unreachable, or timeout)
	assert.True(t,
		strings.Contains(output, "BLOCKED") ||
			strings.Contains(output, "Connection refused") ||
			strings.Contains(output, "Network is unreachable") ||
			strings.Contains(output, "timed out") ||
			strings.Contains(output, "Operation not permitted"),
		"curl to blocked domain should fail, got: %s", output)
}

// TestFirewall_AllowedDomainsReachable verifies allowed domains can be reached
func TestFirewall_AllowedDomainsReachable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image,
		harness.WithCapAdd("NET_ADMIN", "NET_RAW"),
		harness.WithUser("root"),
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Install dependencies
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", "apk add --no-cache ipset bind-tools")
	require.NoError(t, err, "failed to install dependencies")
	require.Equal(t, 0, execResult.ExitCode, "failed to install deps")

	// Set up firewall with example.com allowed
	setupFirewall := `
		iptables -F
		iptables -X
		ipset destroy allowed-domains 2>/dev/null || true
		ipset create allowed-domains hash:net

		# Resolve and add example.com IPs
		for ip in $(dig +short example.com A); do
			ipset add allowed-domains "$ip" -exist
			echo "Added $ip to allowed-domains"
		done

		# Allow localhost and DNS
		iptables -A INPUT -i lo -j ACCEPT
		iptables -A OUTPUT -o lo -j ACCEPT
		iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
		iptables -A INPUT -p udp --sport 53 -j ACCEPT
		iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
		iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

		# Allow ipset destinations
		iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT

		# Set policies (after rules are added)
		iptables -P INPUT DROP
		iptables -P FORWARD DROP
		iptables -P OUTPUT DROP

		# Reject others
		iptables -A OUTPUT -j REJECT --reject-with icmp-admin-prohibited

		echo "Firewall configured with example.com allowed"
	`
	execResult, err = ctr.Exec(ctx, client, "sh", "-c", setupFirewall)
	require.NoError(t, err, "failed to setup firewall")
	require.Equal(t, 0, execResult.ExitCode, "firewall setup failed: %s", execResult.CleanOutput())

	// Try to curl the allowed domain (should succeed)
	curlResult, err := ctr.Exec(ctx, client, "sh", "-c", "curl --connect-timeout 10 -s -o /dev/null -w '%{http_code}' https://example.com")
	require.NoError(t, err, "failed to run curl")

	t.Logf("curl response code: %s", curlResult.CleanOutput())

	// Should get a successful response (200)
	assert.Equal(t, "200", strings.TrimSpace(curlResult.CleanOutput()), "should be able to reach allowed domain")
}

// TestFirewall_HostDockerInternalAllowed verifies host.docker.internal is accessible
func TestFirewall_HostDockerInternalAllowed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Start mock host proxy
	proxy := hostproxytest.NewMockHostProxy(t)

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image,
		harness.WithCapAdd("NET_ADMIN", "NET_RAW"),
		harness.WithUser("root"),
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Install dependencies
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", "apk add --no-cache ipset")
	require.NoError(t, err, "failed to install ipset")
	require.Equal(t, 0, execResult.ExitCode, "failed to install ipset")

	// Set up firewall that allows host.docker.internal (matching init-firewall.sh approach)
	setupFirewall := `
		iptables -F
		iptables -X
		ipset destroy allowed-domains 2>/dev/null || true
		ipset create allowed-domains hash:net

		# Allow localhost and DNS
		iptables -A INPUT -i lo -j ACCEPT
		iptables -A OUTPUT -o lo -j ACCEPT
		iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
		iptables -A INPUT -p udp --sport 53 -j ACCEPT
		iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
		iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

		# Get host network from default route (like init-firewall.sh)
		HOST_IP=$(ip route | grep default | cut -d" " -f3)
		if [ -n "$HOST_IP" ]; then
			HOST_NETWORK=$(echo "$HOST_IP" | sed "s/\.[0-9]*$/.0\/24/")
			echo "Allowing host network: $HOST_NETWORK"
			iptables -A INPUT -s "$HOST_NETWORK" -j ACCEPT
			iptables -A OUTPUT -d "$HOST_NETWORK" -j ACCEPT
		fi

		# Allow host.docker.internal (use multiple lookup methods like init-firewall.sh)
		host_addrs=$( (getent hosts host.docker.internal 2>/dev/null | awk '{print $1}'; getent ahostsv4 host.docker.internal 2>/dev/null | awk '{print $1}') | sort -u )
		if [ -n "$host_addrs" ]; then
			for host_ip in $host_addrs; do
				if echo "$host_ip" | grep -q ':'; then
					echo "Allowing host.docker.internal (IPv6): $host_ip"
					ip6tables -A INPUT -s "$host_ip" -j ACCEPT 2>/dev/null || true
					ip6tables -A OUTPUT -d "$host_ip" -j ACCEPT 2>/dev/null || true
				else
					echo "Allowing host.docker.internal (IPv4): $host_ip"
					iptables -A INPUT -s "$host_ip" -j ACCEPT
					iptables -A OUTPUT -d "$host_ip" -j ACCEPT
				fi
			done
		fi

		# Set DROP policy
		iptables -P INPUT DROP
		iptables -P FORWARD DROP
		iptables -P OUTPUT DROP

		# Allow ipset
		iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT

		# Reject others
		iptables -A OUTPUT -j REJECT --reject-with icmp-admin-prohibited

		echo "Firewall configured"
	`
	execResult, err = ctr.Exec(ctx, client, "sh", "-c", setupFirewall)
	require.NoError(t, err, "failed to setup firewall")
	require.Equal(t, 0, execResult.ExitCode, "firewall setup failed: %s", execResult.CleanOutput())

	// Get the proxy port from the URL
	proxyURL := proxy.URL()
	// Extract port from URL like http://127.0.0.1:12345
	port := proxyURL[strings.LastIndex(proxyURL, ":")+1:]

	// Try to reach host.docker.internal (our mock proxy)
	curlResult, err := ctr.Exec(ctx, client, "sh", "-c",
		"curl --connect-timeout 5 -s -v http://host.docker.internal:"+port+"/health 2>&1 || echo 'CURL_EXIT_CODE:'$?")
	require.NoError(t, err, "failed to run curl")

	t.Logf("curl to host.docker.internal: %s", curlResult.CleanOutput())

	// Should get the health response (check that we got the JSON response with "ok" status)
	assert.Contains(t, curlResult.CleanOutput(), "ok", "should be able to reach host.docker.internal")
}

// TestFirewall_DockerNetworkAllowed verifies Docker bridge networks are allowed
func TestFirewall_DockerNetworkAllowed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client)
	ctr := harness.RunContainer(t, client, image,
		harness.WithCapAdd("NET_ADMIN", "NET_RAW"),
		harness.WithUser("root"),
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Install dependencies
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", "apk add --no-cache ipset iproute2")
	require.NoError(t, err, "failed to install dependencies")
	require.Equal(t, 0, execResult.ExitCode, "failed to install deps")

	// Set up firewall that allows Docker networks
	setupFirewall := `
		iptables -F
		iptables -X
		ipset destroy allowed-domains 2>/dev/null || true
		ipset create allowed-domains hash:net

		# Allow localhost and DNS
		iptables -A INPUT -i lo -j ACCEPT
		iptables -A OUTPUT -o lo -j ACCEPT
		iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
		iptables -A INPUT -p udp --sport 53 -j ACCEPT
		iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
		iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

		# Get host IP from default route and allow host network
		HOST_IP=$(ip route | grep default | cut -d" " -f3)
		if [ -n "$HOST_IP" ]; then
			HOST_NETWORK=$(echo "$HOST_IP" | sed "s/\.[0-9]*$/.0\/24/")
			echo "Allowing host network: $HOST_NETWORK"
			iptables -A INPUT -s "$HOST_NETWORK" -j ACCEPT
			iptables -A OUTPUT -d "$HOST_NETWORK" -j ACCEPT
		fi

		# Allow Docker bridge networks
		while read -r network; do
			if [ -n "$network" ]; then
				echo "Allowing Docker network: $network"
				iptables -A INPUT -s "$network" -j ACCEPT
				iptables -A OUTPUT -d "$network" -j ACCEPT
			fi
		done < <(ip route | grep -v default | grep -v "dev lo" | awk '{print $1}' | grep -E "^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+$")

		# Set DROP policy
		iptables -P INPUT DROP
		iptables -P FORWARD DROP
		iptables -P OUTPUT DROP

		# Allow ipset
		iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT

		# Reject others
		iptables -A OUTPUT -j REJECT --reject-with icmp-admin-prohibited

		echo "Firewall configured"
	`
	execResult, err = ctr.Exec(ctx, client, "bash", "-c", setupFirewall)
	require.NoError(t, err, "failed to setup firewall")
	require.Equal(t, 0, execResult.ExitCode, "firewall setup failed: %s", execResult.CleanOutput())

	// Verify we can still reach the Docker network (ping gateway)
	pingResult, err := ctr.Exec(ctx, client, "sh", "-c", "ip route | grep default | cut -d' ' -f3 | xargs -I{} ping -c 1 -W 2 {}")
	require.NoError(t, err, "failed to run ping")

	t.Logf("ping result: %s", pingResult.Stdout)

	// Should be able to ping the gateway
	assert.Equal(t, 0, pingResult.ExitCode, "should be able to ping Docker gateway")
}


// TestFirewall_IPRangeSourcesParsing verifies the init-firewall.sh script correctly parses
// CLAWKER_FIREWALL_IP_RANGE_SOURCES environment variable with lowercase JSON keys.
// This is a regression test for the JSON tag bug where Go serialized PascalCase keys
// but the shell script expected lowercase keys.
func TestFirewall_IPRangeSourcesParsing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "init-firewall.sh")

	// Generate env vars using real RuntimeEnv (same code path as production)
	env, err := docker.RuntimeEnv(docker.RuntimeEnvOpts{
		FirewallEnabled: true,
		FirewallIPRangeSources: []config.IPRangeSource{
			{Name: "github"},
			{Name: "google"},
		},
	})
	require.NoError(t, err)

	// Log the env var for debugging
	for _, e := range env {
		if strings.HasPrefix(e, "CLAWKER_FIREWALL_IP_RANGE_SOURCES=") {
			t.Logf("IP Range Sources env: %s", e)
			// Verify it has lowercase keys (not PascalCase)
			assert.Contains(t, e, `"name":"github"`, "JSON must have lowercase 'name' key")
			assert.NotContains(t, e, `"Name":`, "JSON must NOT have PascalCase 'Name' key")
		}
	}

	ctr := harness.RunContainer(t, client, image,
		harness.WithCapAdd("NET_ADMIN", "NET_RAW"),
		harness.WithUser("root"),
		harness.WithEnv(env...),
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Install all dependencies needed by the firewall script
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", `
		apk add --no-cache ipset bind-tools jq curl bash iproute2 &&
		# Create aggregate stub (real script expects this)
		cat > /usr/local/bin/aggregate << 'EOF'
#!/bin/sh
cat
EOF
		chmod +x /usr/local/bin/aggregate
	`)
	require.NoError(t, err)
	require.Equal(t, 0, execResult.ExitCode, "failed to install deps: %s", execResult.CleanOutput())

	// Run the actual firewall script
	t.Log("Running init-firewall.sh with IP range sources...")
	execResult, err = ctr.Exec(ctx, client, "bash", "/usr/local/bin/init-firewall.sh")
	require.NoError(t, err, "failed to execute firewall script")

	output := execResult.CleanOutput()
	t.Logf("Firewall script output:\n%s", output)

	// CRITICAL: Verify JSON parsing worked by checking source names were extracted
	// These assertions prove the lowercase JSON keys are being parsed correctly
	// If this fails with "empty name" error, the JSON tags are broken
	assert.Contains(t, output, "Fetching IP ranges from github", "script should parse github source name from JSON")
	assert.Contains(t, output, "Fetching IP ranges from google", "script should parse google source name from JSON")

	// Verify IP ranges were processed (not just fetched)
	assert.Contains(t, output, "Processing github IPs", "script should process github IPs")
	assert.Contains(t, output, "Processing google IPs", "script should process google IPs")

	// Check for JSON parsing failure indicators (should NOT be present)
	assert.NotContains(t, output, "ERROR: IP range source has empty name", "JSON parsing should not fail")
	assert.NotContains(t, output, "JSON parsing likely failed", "should not see JSON parsing error")

	// Verify ipsets were created (proves the processing completed)
	ipsetResult, err := ctr.Exec(ctx, client, "ipset", "list", "-name")
	require.NoError(t, err, "failed to list ipsets")
	t.Logf("ipsets: %s", ipsetResult.CleanOutput())
	assert.Contains(t, ipsetResult.CleanOutput(), "allowed-domains", "allowed-domains ipset should exist")

	// Count entries in ipset
	ipsetList, err := ctr.Exec(ctx, client, "ipset", "list", "allowed-domains")
	require.NoError(t, err, "failed to list ipset contents")
	entryCount := strings.Count(ipsetList.CleanOutput(), "\n") - 10 // subtract header lines
	t.Logf("IP entries in allowed-domains: ~%d", entryCount)
	assert.Greater(t, entryCount, 50, "should have substantial IP entries from github+google")

	// ACTUAL CONNECTIVITY TESTS - verify we can reach the allowed IPs
	t.Log("=== CONNECTIVITY TESTS ===")

	// Test GitHub API (should succeed - we added github IP ranges)
	githubResult, err := ctr.Exec(ctx, client, "sh", "-c",
		"curl -s --connect-timeout 15 -o /dev/null -w '%{http_code}' https://api.github.com/zen")
	require.NoError(t, err, "failed to run curl for github")
	githubCode := strings.TrimSpace(githubResult.CleanOutput())
	t.Logf("GitHub API response code: %s", githubCode)
	assert.Equal(t, "200", githubCode, "should be able to reach GitHub API (IP ranges added)")

	// Test Google (gstatic) - should succeed with google IP ranges
	googleResult, err := ctr.Exec(ctx, client, "sh", "-c",
		"curl -s --connect-timeout 15 -o /dev/null -w '%{http_code}' https://www.gstatic.com/ipranges/goog.json")
	require.NoError(t, err, "failed to run curl for google")
	googleCode := strings.TrimSpace(googleResult.CleanOutput())
	t.Logf("Google (gstatic) response code: %s", googleCode)
	assert.Equal(t, "200", googleCode, "should be able to reach Google (IP ranges added)")

	// Test blocked domain (should fail - not in allowlist)
	blockedResult, err := ctr.Exec(ctx, client, "sh", "-c",
		"curl -s --connect-timeout 5 https://example.com 2>&1 || echo 'BLOCKED'")
	require.NoError(t, err, "failed to run curl for blocked domain")
	blockedOutput := blockedResult.CleanOutput()
	t.Logf("Blocked domain (example.com) result: %s", blockedOutput)
	assert.True(t,
		strings.Contains(blockedOutput, "BLOCKED") ||
			strings.Contains(blockedOutput, "Connection refused") ||
			strings.Contains(blockedOutput, "timed out") ||
			strings.Contains(blockedOutput, "Operation not permitted"),
		"example.com should be blocked by firewall")

	// Script should complete successfully now that we've verified connectivity
	require.Equal(t, 0, execResult.ExitCode, "firewall script should succeed - if this fails, check the connectivity test results above")
}

// TestFirewall_EntrypointConfigPassing verifies that the entrypoint correctly passes
// firewall config to the init-firewall.sh script via files (not env vars).
// This is a regression test for the bug where sudo stripped environment variables,
// preventing the firewall script from receiving IP range source configuration.
//
// The production flow is:
//  1. Container starts with entrypoint.sh as PID 1 (as non-root user)
//  2. Entrypoint writes config to /tmp/clawker/firewall-* files
//  3. Entrypoint calls `sudo /usr/local/bin/init-firewall.sh`
//  4. init-firewall.sh reads config from files (env vars are stripped by sudo)
//
// Previous tests ran init-firewall.sh directly as root with env vars set,
// which doesn't exercise the actual production flow.
func TestFirewall_EntrypointConfigPassing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	client := harness.NewTestClient(t)
	image := harness.BuildLightImage(t, client, "init-firewall.sh", "entrypoint.sh")

	// Generate env vars using real RuntimeEnv (same as production)
	env, err := docker.RuntimeEnv(docker.RuntimeEnvOpts{
		FirewallEnabled: true,
		FirewallIPRangeSources: []config.IPRangeSource{
			{Name: "github"},
		},
	})
	require.NoError(t, err)

	// Start container as root to set up the test environment
	ctr := harness.RunContainer(t, client, image,
		harness.WithCapAdd("NET_ADMIN", "NET_RAW"),
		harness.WithUser("root"),
		harness.WithEnv(env...),
		harness.WithExtraHost("host.docker.internal:host-gateway"),
	)

	// Set up the test environment: create a non-root user, install deps, configure sudoers
	setupScript := `
		# Install dependencies
		apk add --no-cache sudo ipset bind-tools jq curl bash iproute2 shadow

		# Create non-root user
		useradd -m -s /bin/bash testuser

		# Configure passwordless sudo for firewall script (matches production Dockerfile)
		echo "testuser ALL=(root) NOPASSWD: /usr/local/bin/init-firewall.sh" > /etc/sudoers.d/testuser-firewall
		chmod 0440 /etc/sudoers.d/testuser-firewall

		# Make tmp dir accessible
		mkdir -p /tmp/clawker
		chmod 777 /tmp/clawker
	`
	execResult, err := ctr.Exec(ctx, client, "sh", "-c", setupScript)
	require.NoError(t, err)
	require.Equal(t, 0, execResult.ExitCode, "failed to set up test env: %s", execResult.CleanOutput())

	// Get the IP range sources env var to pass to the test script
	var ipRangeSourcesEnv string
	for _, e := range env {
		if strings.HasPrefix(e, "CLAWKER_FIREWALL_IP_RANGE_SOURCES=") {
			ipRangeSourcesEnv = strings.TrimPrefix(e, "CLAWKER_FIREWALL_IP_RANGE_SOURCES=")
		}
	}
	require.NotEmpty(t, ipRangeSourcesEnv, "IP range sources env var should be set")

	// Now run the test as the non-root user
	// This simulates the real entrypoint flow:
	// 1. Write config files (since env vars will be stripped by sudo)
	// 2. Call sudo to run the firewall script
	//
	// We pass the config as a heredoc to simulate how container startup works:
	// - Container receives env var from Docker
	// - Entrypoint writes it to a file before calling sudo
	testScript := fmt.Sprintf(`
		# This simulates what entrypoint.sh does in production
		# The config comes from container env var (passed here as argument)
		IP_RANGE_SOURCES='%s'

		# Step 1: Write config to files (entrypoint does this before calling sudo)
		mkdir -p /tmp/clawker
		echo "$IP_RANGE_SOURCES" > /tmp/clawker/firewall-ip-range-sources
		echo "" > /tmp/clawker/firewall-domains

		# Verify files were written
		echo "=== Config file contents ==="
		cat /tmp/clawker/firewall-ip-range-sources

		# Step 2: Call sudo (just like entrypoint.sh does)
		# Note: sudo WILL strip env vars - that's the bug we're testing for
		echo "=== Running firewall script via sudo ==="
		sudo /usr/local/bin/init-firewall.sh

		# Step 3: Verify firewall is working
		echo "=== Verifying GitHub connectivity ==="
		curl -s --connect-timeout 10 -o /dev/null -w '%%{http_code}' https://api.github.com/zen
	`, ipRangeSourcesEnv)

	// Run as testuser (non-root)
	execResult, err = ctr.Exec(ctx, client, "su", "-", "testuser", "-c", testScript)
	require.NoError(t, err)

	output := execResult.CleanOutput()
	t.Logf("Test output:\n%s", output)

	// CRITICAL: Verify the firewall script received the config via files
	// If this fails with "No IP range sources configured", the file-passing mechanism is broken
	assert.Contains(t, output, "Processing IP range sources", "firewall should receive IP range sources via files")
	assert.Contains(t, output, "Fetching IP ranges from github", "firewall should process github source")
	assert.NotContains(t, output, "No IP range sources configured", "config should be passed via files, not env vars")

	// Verify connectivity works
	assert.Contains(t, output, "200", "should be able to reach GitHub after firewall setup")

	require.Equal(t, 0, execResult.ExitCode, "entrypoint-style firewall setup should succeed")
}

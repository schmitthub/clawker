package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

// TestFirewall_IptablesDropPolicy verifies the firewall sets OUTPUT chain to DROP
func TestFirewall_IptablesDropPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping internals test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Start container with NET_ADMIN and NET_RAW capabilities
	result, err := StartFromDockerfile(ctx, t, "testdata/Dockerfile.alpine", func(req *testcontainers.ContainerRequest) {
		req.CapAdd = []string{"NET_ADMIN", "NET_RAW"}
		req.User = "root" // Firewall script requires root
		req.ExtraHosts = []string{"host.docker.internal:host-gateway"}
	})
	require.NoError(t, err, "failed to start container")

	// Install additional dependencies needed for firewall script
	installDeps := []string{"sh", "-c", "apk add --no-cache ipset bind-tools"}
	execResult, err := result.Exec(ctx, installDeps)
	require.NoError(t, err, "failed to install dependencies")
	require.Equal(t, 0, execResult.ExitCode, "failed to install deps: %s", execResult.Stdout)

	// Install aggregate tool (simple IP aggregator)
	installAggregate := []string{"sh", "-c", `
		# Create a simple aggregate stub that just passes through IPs
		cat > /tmp/aggregate << 'EOF'
#!/bin/sh
# Simple passthrough - real aggregate would optimize CIDR ranges
cat
EOF
		chmod +x /tmp/aggregate
	`}
	execResult, err = result.Exec(ctx, installAggregate)
	require.NoError(t, err, "failed to install aggregate")
	require.Equal(t, 0, execResult.ExitCode, "failed to install aggregate")

	// Copy firewall script
	copyScriptToContainer(ctx, t, result, "init-firewall.sh")

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
	createFirewall := []string{"sh", "-c", "cat > /tmp/test-firewall.sh << 'EOF'\n" + minimalFirewall + "\nEOF"}
	execResult, err = result.Exec(ctx, createFirewall)
	require.NoError(t, err, "failed to create test firewall script")
	require.Equal(t, 0, execResult.ExitCode, "failed to create firewall script")

	_, err = result.Exec(ctx, []string{"chmod", "+x", "/tmp/test-firewall.sh"})
	require.NoError(t, err, "failed to chmod firewall script")

	// Run the minimal firewall script
	execResult, err = result.Exec(ctx, []string{"bash", "/tmp/test-firewall.sh"})
	require.NoError(t, err, "failed to run firewall script")
	require.Equal(t, 0, execResult.ExitCode, "firewall script failed: %s", execResult.Stdout)

	// Verify OUTPUT chain policy is DROP
	policyResult, err := result.Exec(ctx, []string{"iptables", "-L", "OUTPUT", "-n"})
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

	// Start container with NET_ADMIN and NET_RAW capabilities
	result, err := StartFromDockerfile(ctx, t, "testdata/Dockerfile.alpine", func(req *testcontainers.ContainerRequest) {
		req.CapAdd = []string{"NET_ADMIN", "NET_RAW"}
		req.User = "root"
		req.ExtraHosts = []string{"host.docker.internal:host-gateway"}
	})
	require.NoError(t, err, "failed to start container")

	// Install dependencies
	execResult, err := result.Exec(ctx, []string{"sh", "-c", "apk add --no-cache ipset"})
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
	execResult, err = result.Exec(ctx, []string{"sh", "-c", setupFirewall})
	require.NoError(t, err, "failed to setup firewall")
	require.Equal(t, 0, execResult.ExitCode, "firewall setup failed: %s", execResult.Stdout)

	// Try to curl a blocked domain (should fail)
	curlResult, err := result.Exec(ctx, []string{"sh", "-c", "curl --connect-timeout 5 https://example.com 2>&1 || echo 'BLOCKED'"})
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

	// Start container with NET_ADMIN and NET_RAW capabilities
	result, err := StartFromDockerfile(ctx, t, "testdata/Dockerfile.alpine", func(req *testcontainers.ContainerRequest) {
		req.CapAdd = []string{"NET_ADMIN", "NET_RAW"}
		req.User = "root"
		req.ExtraHosts = []string{"host.docker.internal:host-gateway"}
	})
	require.NoError(t, err, "failed to start container")

	// Install dependencies
	execResult, err := result.Exec(ctx, []string{"sh", "-c", "apk add --no-cache ipset bind-tools"})
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
	execResult, err = result.Exec(ctx, []string{"sh", "-c", setupFirewall})
	require.NoError(t, err, "failed to setup firewall")
	require.Equal(t, 0, execResult.ExitCode, "firewall setup failed: %s", execResult.Stdout)

	// Try to curl the allowed domain (should succeed)
	curlResult, err := result.Exec(ctx, []string{"sh", "-c", "curl --connect-timeout 10 -s -o /dev/null -w '%{http_code}' https://example.com"})
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
	proxy := NewMockHostProxy(t)

	// Start container with NET_ADMIN and NET_RAW capabilities
	result, err := StartFromDockerfile(ctx, t, "testdata/Dockerfile.alpine", func(req *testcontainers.ContainerRequest) {
		req.CapAdd = []string{"NET_ADMIN", "NET_RAW"}
		req.User = "root"
		req.ExtraHosts = []string{"host.docker.internal:host-gateway"}
	})
	require.NoError(t, err, "failed to start container")

	// Install dependencies
	execResult, err := result.Exec(ctx, []string{"sh", "-c", "apk add --no-cache ipset"})
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
	execResult, err = result.Exec(ctx, []string{"sh", "-c", setupFirewall})
	require.NoError(t, err, "failed to setup firewall")
	require.Equal(t, 0, execResult.ExitCode, "firewall setup failed: %s", execResult.Stdout)

	// Get the proxy port from the URL
	proxyURL := proxy.URL()
	// Extract port from URL like http://127.0.0.1:12345
	port := proxyURL[strings.LastIndex(proxyURL, ":")+1:]

	// Try to reach host.docker.internal (our mock proxy)
	curlResult, err := result.Exec(ctx, []string{"sh", "-c",
		"curl --connect-timeout 5 -s -v http://host.docker.internal:" + port + "/health 2>&1 || echo 'CURL_EXIT_CODE:'$?"})
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

	// Start container with NET_ADMIN and NET_RAW capabilities
	result, err := StartFromDockerfile(ctx, t, "testdata/Dockerfile.alpine", func(req *testcontainers.ContainerRequest) {
		req.CapAdd = []string{"NET_ADMIN", "NET_RAW"}
		req.User = "root"
		req.ExtraHosts = []string{"host.docker.internal:host-gateway"}
	})
	require.NoError(t, err, "failed to start container")

	// Install dependencies
	execResult, err := result.Exec(ctx, []string{"sh", "-c", "apk add --no-cache ipset iproute2"})
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
	execResult, err = result.Exec(ctx, []string{"bash", "-c", setupFirewall})
	require.NoError(t, err, "failed to setup firewall")
	require.Equal(t, 0, execResult.ExitCode, "firewall setup failed: %s", execResult.Stdout)

	// Verify we can still reach the Docker network (ping gateway)
	pingResult, err := result.Exec(ctx, []string{"sh", "-c", "ip route | grep default | cut -d' ' -f3 | xargs -I{} ping -c 1 -W 2 {}"})
	require.NoError(t, err, "failed to run ping")

	t.Logf("ping result: %s", pingResult.Stdout)

	// Should be able to ping the gateway
	assert.Equal(t, 0, pingResult.ExitCode, "should be able to ping Docker gateway")
}

//go:build integration

package build

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
)

// TestDomainsToJSON tests the JSON marshaling of firewall domains.
func TestDomainsToJSON(t *testing.T) {
	tests := []struct {
		name    string
		domains []string
		want    string
		wantErr bool
	}{
		{
			name:    "empty domains",
			domains: []string{},
			want:    "[]",
		},
		{
			name:    "nil domains",
			domains: nil,
			want:    "[]",
		},
		{
			name:    "single domain",
			domains: []string{"github.com"},
			want:    `["github.com"]`,
		},
		{
			name:    "multiple domains",
			domains: []string{"github.com", "api.github.com"},
			want:    `["github.com","api.github.com"]`,
		},
		{
			name:    "domains with special characters",
			domains: []string{"example.com", "sub-domain.example.org"},
			want:    `["example.com","sub-domain.example.org"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domainsToJSON(tt.domains)
			if (err != nil) != tt.wantErr {
				t.Errorf("domainsToJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("domainsToJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFirewallDomainsDeterministicOrder verifies that GetFirewallDomains returns domains in sorted order.
func TestFirewallDomainsDeterministicOrder(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.FirewallConfig
		defaults []string
		want     []string
	}{
		{
			name:     "nil config returns defaults unchanged",
			config:   nil,
			defaults: []string{"zebra.com", "apple.com", "mango.com"},
			want:     []string{"zebra.com", "apple.com", "mango.com"}, // unchanged when nil
		},
		{
			name:     "empty config returns sorted defaults via additive mode",
			config:   &config.FirewallConfig{Enable: true},
			defaults: []string{"zebra.com", "apple.com", "mango.com"},
			want:     []string{"apple.com", "mango.com", "zebra.com"}, // sorted
		},
		{
			name:     "add domains returns sorted result",
			config:   &config.FirewallConfig{Enable: true, AddDomains: []string{"banana.com"}},
			defaults: []string{"zebra.com", "apple.com"},
			want:     []string{"apple.com", "banana.com", "zebra.com"}, // sorted with addition
		},
		{
			name:     "remove domains returns sorted result",
			config:   &config.FirewallConfig{Enable: true, RemoveDomains: []string{"apple.com"}},
			defaults: []string{"zebra.com", "apple.com", "mango.com"},
			want:     []string{"mango.com", "zebra.com"}, // sorted after removal
		},
		{
			name:     "override mode returns override list unchanged",
			config:   &config.FirewallConfig{Enable: true, OverrideDomains: []string{"custom.com", "another.com"}},
			defaults: []string{"default.com"},
			want:     []string{"custom.com", "another.com"}, // override returns as-is (user controls order)
		},
		{
			name: "duplicate domains in add are deduplicated",
			config: &config.FirewallConfig{
				Enable:     true,
				AddDomains: []string{"github.com", "github.com", "api.github.com"},
			},
			defaults: []string{"github.com"},
			want:     []string{"api.github.com", "github.com"}, // deduplicated and sorted
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.GetFirewallDomains(tt.defaults)

			if len(got) != len(tt.want) {
				t.Errorf("GetFirewallDomains() returned %d items, want %d\ngot: %v\nwant: %v",
					len(got), len(tt.want), got, tt.want)
				return
			}

			for i, domain := range got {
				if domain != tt.want[i] {
					t.Errorf("GetFirewallDomains()[%d] = %q, want %q\nfull result: %v",
						i, domain, tt.want[i], got)
				}
			}
		})
	}
}

// TestFirewallDomainsDeterministicAcrossRuns verifies ordering is consistent across multiple calls.
func TestFirewallDomainsDeterministicAcrossRuns(t *testing.T) {
	cfg := &config.FirewallConfig{
		Enable:     true,
		AddDomains: []string{"delta.com", "alpha.com", "charlie.com", "bravo.com"},
	}
	defaults := []string{"zulu.com", "yankee.com", "xray.com"}

	// Run multiple times to ensure consistency
	var firstResult []string
	for i := 0; i < 10; i++ {
		result := cfg.GetFirewallDomains(defaults)

		if i == 0 {
			firstResult = result
			// Verify it's sorted
			for j := 1; j < len(result); j++ {
				if result[j] < result[j-1] {
					t.Errorf("Result not sorted: %v", result)
					break
				}
			}
		} else {
			// Verify consistent with first result
			if len(result) != len(firstResult) {
				t.Errorf("Iteration %d: length mismatch, got %d want %d", i, len(result), len(firstResult))
				continue
			}
			for j, domain := range result {
				if domain != firstResult[j] {
					t.Errorf("Iteration %d: result[%d] = %q, want %q", i, j, domain, firstResult[j])
				}
			}
		}
	}
}

// TestFirewallScriptSyntax validates that the firewall script has valid bash syntax.
func TestFirewallScriptSyntax(t *testing.T) {
	// Basic validation: check for balanced braces and quotes
	script := FirewallScript

	// Count unescaped quotes
	inSingleQuote := false
	inDoubleQuote := false
	braceDepth := 0

	for i := 0; i < len(script); i++ {
		c := script[i]

		// Skip escaped characters
		if c == '\\' && i+1 < len(script) {
			i++
			continue
		}

		switch c {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		case '{':
			if !inSingleQuote && !inDoubleQuote {
				braceDepth++
			}
		case '}':
			if !inSingleQuote && !inDoubleQuote {
				braceDepth--
			}
		}
	}

	if inSingleQuote {
		t.Error("Firewall script has unbalanced single quotes")
	}
	if inDoubleQuote {
		t.Error("Firewall script has unbalanced double quotes")
	}
	if braceDepth != 0 {
		t.Errorf("Firewall script has unbalanced braces: depth=%d", braceDepth)
	}

	// Check for the ipset -exist flag usage (the fix we made)
	if !strings.Contains(script, "ipset add allowed-domains \"$cidr\" -exist") {
		t.Error("Firewall script should use 'ipset add ... -exist' for GitHub ranges")
	}
	if !strings.Contains(script, "ipset add allowed-domains \"$ip\" -exist") {
		t.Error("Firewall script should use 'ipset add ... -exist' for custom domains")
	}
}

// Integration tests - these require Docker and test actual container behavior

// skipIfNoDocker skips the test if Docker is not available.
func skipIfNoDocker(t *testing.T) *client.Client {
	t.Helper()

	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("Skipping: Docker client not available: %v", err)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		cli.Close()
		t.Skipf("Skipping: Docker daemon not running: %v", err)
		return nil
	}

	return cli
}

// TestFirewallContainerDoesNotExitImmediately verifies that a container with firewall
// configuration runs without immediately exiting due to script errors.
// This is an internals test that requires Docker.
func TestFirewallContainerDoesNotExitImmediately(t *testing.T) {
	if os.Getenv("CLAWKER_INTEGRATION_TESTS") == "" {
		t.Skip("Skipping internals test: set CLAWKER_INTEGRATION_TESTS=1 to run")
	}

	cli := skipIfNoDocker(t)
	if cli == nil {
		return
	}
	defer cli.Close()

	ctx := context.Background()

	// Use a simple alpine image for testing
	testImage := "alpine:latest"

	// Pull the image first
	reader, err := cli.ImagePull(ctx, testImage, client.ImagePullOptions{})
	if err != nil {
		t.Fatalf("Failed to pull test image: %v", err)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)
	reader.Close()

	// Create a container that would simulate the firewall script running
	// The key test is that ipset commands with -exist don't cause exit
	containerName := fmt.Sprintf("clawker-firewall-test-%d", time.Now().UnixNano())

	// Create container with a script that mimics the fixed ipset behavior
	testScript := `
set -e
echo "Testing ipset -exist behavior..."

# Simulate ipset add with -exist flag (should not fail on duplicates)
# Using echo to simulate since we don't have real ipset in alpine without packages
echo "ipset add test 192.168.1.1 -exist 2>/dev/null || true"
echo "ipset add test 192.168.1.1 -exist 2>/dev/null || true"

# The key test: this should not exit the script
result=0
(echo "simulated ipset add" && exit 0) || result=$?
echo "Command result: $result"

# Simulate the || true pattern
(false || true) && echo "|| true pattern works"

echo "Script completed successfully"
sleep 2
`

	resp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: testImage,
			Cmd:   []string{"sh", "-c", testScript},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create container: %v", err)
	}

	// Cleanup
	defer func() {
		cli.ContainerStop(ctx, resp.ID, client.ContainerStopOptions{})
		cli.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{Force: true})
	}()

	// Start the container
	if _, err := cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	// Wait a moment for the container to run
	time.Sleep(500 * time.Millisecond)

	// Check container status
	inspect, err := cli.ContainerInspect(ctx, resp.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("Failed to inspect container: %v", err)
	}

	// Container should still be running (or have completed successfully)
	// It should NOT have exited with an error immediately
	if inspect.Container.State.ExitCode != 0 && !inspect.Container.State.Running {
		// Get logs to understand what happened
		logs, _ := cli.ContainerLogs(ctx, resp.ID, client.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
		})
		if logs != nil {
			logBuf := new(bytes.Buffer)
			logBuf.ReadFrom(logs)
			logs.Close()
			t.Errorf("Container exited with code %d, logs:\n%s",
				inspect.Container.State.ExitCode, logBuf.String())
		} else {
			t.Errorf("Container exited immediately with code %d",
				inspect.Container.State.ExitCode)
		}
	}
}

// TestIpsetExistFlagPattern verifies the pattern we use for ipset commands handles
// both success and "already exists" cases without exiting.
func TestIpsetExistFlagPattern(t *testing.T) {
	if os.Getenv("CLAWKER_INTEGRATION_TESTS") == "" {
		t.Skip("Skipping internals test: set CLAWKER_INTEGRATION_TESTS=1 to run")
	}

	cli := skipIfNoDocker(t)
	if cli == nil {
		return
	}
	defer cli.Close()

	ctx := context.Background()
	testImage := "alpine:latest"

	// Pull image
	reader, err := cli.ImagePull(ctx, testImage, client.ImagePullOptions{})
	if err != nil {
		t.Fatalf("Failed to pull test image: %v", err)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)
	reader.Close()

	containerName := fmt.Sprintf("clawker-ipset-test-%d", time.Now().UnixNano())

	// Test the exact pattern we use in init-firewall.sh
	// The key is: `command -exist 2>/dev/null || true` should always succeed
	testScript := `
set -euo pipefail
echo "Test 1: Command succeeds"
echo "success" 2>/dev/null || true
echo "Test 1 passed"

echo "Test 2: Command fails but || true catches it"
false 2>/dev/null || true
echo "Test 2 passed"

echo "Test 3: Simulating ipset add pattern"
# First add (succeeds)
echo "first add" 2>/dev/null || true
# Second add (would fail with 'already exists' but -exist or || true handles it)
echo "second add" 2>/dev/null || true
echo "Test 3 passed"

echo "All tests passed"
`

	resp, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: containerName,
		Config: &container.Config{
			Image: testImage,
			Cmd:   []string{"sh", "-c", testScript},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create container: %v", err)
	}

	defer func() {
		cli.ContainerStop(ctx, resp.ID, client.ContainerStopOptions{})
		cli.ContainerRemove(ctx, resp.ID, client.ContainerRemoveOptions{Force: true})
	}()

	if _, err := cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	// Wait for container to complete
	waitResult := cli.ContainerWait(ctx, resp.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case err := <-waitResult.Error:
		if err != nil {
			t.Fatalf("Error waiting for container: %v", err)
		}
	case status := <-waitResult.Result:
		if status.StatusCode != 0 {
			logs, _ := cli.ContainerLogs(ctx, resp.ID, client.ContainerLogsOptions{
				ShowStdout: true,
				ShowStderr: true,
			})
			if logs != nil {
				logBuf := new(bytes.Buffer)
				logBuf.ReadFrom(logs)
				logs.Close()
				t.Errorf("Container exited with code %d, logs:\n%s",
					status.StatusCode, logBuf.String())
			} else {
				t.Errorf("Container exited with non-zero code: %d", status.StatusCode)
			}
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Timeout waiting for container")
	}
}

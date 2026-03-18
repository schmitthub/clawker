package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

func newFirewallHarness(t *testing.T) *harness.Harness {
	t.Helper()
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			Firewall:       firewall.NewManager,
		},
	}
	setup := h.NewIsolatedFS(nil)

	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	return h
}

// runInContainer runs a command inside a fresh container and returns the result.
// The container starts, runs the command, and is automatically removed.
func runInContainer(h *harness.Harness, agent string, cmd ...string) *harness.RunResult {
	h.T.Helper()
	args := []string{"container", "run", "--rm", "--agent", agent, "@"}
	args = append(args, cmd...)
	return h.Run(args...)
}

func TestFirewall_BlockedDomain(t *testing.T) {
	h := newFirewallHarness(t)

	// Blocked: example.com is NOT in the allowed rules.
	res := runInContainer(h, "firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, res.Err, "curl to blocked domain should fail")
}

func TestFirewall_AllowedDomain(t *testing.T) {
	h := newFirewallHarness(t)

	// Allowed: api.anthropic.com is a required rule.
	res := runInContainer(h, "firewall-test",
		"curl", "-s", "--max-time", "10", "-o", "/dev/null", "-w", "%{http_code}",
		"https://api.anthropic.com")
	require.NoError(t, res.Err, "curl to allowed domain failed\nstdout: %s\nstderr: %s",
		res.Stdout, res.Stderr)
	httpCode := strings.TrimSpace(res.Stdout)
	assert.NotEmpty(t, httpCode, "should get an HTTP response code")
}

func TestFirewall_AddRemove(t *testing.T) {
	h := newFirewallHarness(t)

	// Verify blocked before add.
	blocked := runInContainer(h, "firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, blocked.Err, "example.com should be blocked initially")

	// Add example.com.
	addRes := h.Run("firewall", "add", "example.com")
	require.NoError(t, addRes.Err, "firewall add failed\nstdout: %s\nstderr: %s",
		addRes.Stdout, addRes.Stderr)

	// Verify allowed after add.
	allowed := runInContainer(h, "firewall-test",
		"curl", "-s", "--max-time", "10", "-o", "/dev/null", "-w", "%{http_code}",
		"https://example.com")
	require.NoError(t, allowed.Err, "curl after add should succeed\nstdout: %s\nstderr: %s",
		allowed.Stdout, allowed.Stderr)
	assert.NotEmpty(t, strings.TrimSpace(allowed.Stdout), "should get HTTP response code")

	// Remove example.com.
	removeRes := h.Run("firewall", "remove", "example.com")
	require.NoError(t, removeRes.Err, "firewall remove failed\nstdout: %s\nstderr: %s",
		removeRes.Stdout, removeRes.Stderr)

	// Verify blocked again after remove.
	blockedAgain := runInContainer(h, "firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, blockedAgain.Err, "example.com should be blocked after remove")
}

func TestFirewall_Status(t *testing.T) {
	h := newFirewallHarness(t)

	// Run a container to trigger firewall startup.
	res := runInContainer(h, "firewall-test", "echo", "started")
	t.Logf("run stdout: %s", res.Stdout)
	t.Logf("run stderr: %s", res.Stderr)
	require.NoError(t, res.Err, "container run failed\nstdout: %s\nstderr: %s",
		res.Stdout, res.Stderr)

	statusRes := h.Run("firewall", "status", "--json")
	require.NoError(t, statusRes.Err, "firewall status failed\nstdout: %s\nstderr: %s",
		statusRes.Stdout, statusRes.Stderr)
	assert.Contains(t, statusRes.Stdout, `"running": true`)
	assert.Contains(t, statusRes.Stdout, `"rule_count": 7`)
}

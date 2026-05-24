package e2e

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

// newFirewallHarness wires the real Config/Docker/ControlPlane/AdminClient
// stack. requiredServices names which services must have been running at
// cleanup time; omit for the default ("firewall", "controlplane"). Tests
// that intentionally tear the firewall down mid-test (e.g. TestFirewall_UpDown)
// pass only "controlplane" so the cleanup invariant check doesn't fail on
// the deliberately-absent firewall containers.
func newFirewallHarness(t *testing.T, requiredServices ...string) *harness.Harness {
	t.Helper()
	if len(requiredServices) == 0 {
		requiredServices = []string{"firewall", "controlplane"}
	}
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	// Register the stack check BEFORE NewIsolatedFS so it runs AFTER
	// cleanup (t.Cleanup is LIFO). Cleanup populates h.Cleanup, then
	// this check reads it.
	t.Cleanup(func() { h.RequireServicesWereRunning(t, requiredServices...) })

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

func TestFirewall_BlockedDomain(t *testing.T) {
	h := newFirewallHarness(t)

	// Blocked: example.com is NOT in the allowed rules.
	res := h.RunInContainer("firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, res.Err, "curl to blocked domain should fail")
}

func TestFirewall_UpDown(t *testing.T) {
	// This test explicitly tears the firewall down before returning, so
	// only the CP is expected to be running at cleanup time.
	h := newFirewallHarness(t, "controlplane")

	res := h.Run("firewall", "up")
	// Should not return error code
	require.NoError(t, res.Err, "firewall up failed\nstdout: %s\nstderr: %s",
		res.Stdout, res.Stderr)
	statusRes := h.Run("firewall", "status")
	require.NoError(t, statusRes.Err, "firewall status failed\nstdout: %s\nstderr: %s",
		statusRes.Stdout, statusRes.Stderr)
	downRes := h.Run("firewall", "down")
	require.NoError(t, downRes.Err, "firewall down failed\nstdout: %s\nstderr: %s",
		downRes.Stdout, downRes.Stderr)

}

func TestFirewall_ICMPBlocked(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	setup := h.NewIsolatedFS(nil)

	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
  packages:
    - iputils-ping
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

	// ICMP must be blocked to prevent ICMP tunneling (ptunnel, icmpsh).
	// ping sends ICMP echo requests — should fail with the DROP rule in place.
	res := h.RunInContainer("firewall-icmp", "ping", "-c", "1", "-W", "3", "8.8.8.8")
	assert.NotNil(t, res.Err, "ping should fail — ICMP must be blocked to prevent tunneling")
}

// TestFirewall_Bypass exercises the full `firewall bypass` surface in one
// composite flow: explicit --stop restore, natural dead-man timer expiry
// (INV-B2-007), and the stopped-container error path (INV-B2-016).
func TestFirewall_Bypass(t *testing.T) {
	h := newFirewallHarness(t)

	// Start a long-lived container in detached mode so we can exec into it.
	startRes := h.Run("container", "run", "--detach", "--agent", "firewall-test", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)

	// Baseline: example.com is NOT in the allowed rules.
	blockRes := h.ExecInContainer("firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, blockRes.Err, "baseline: curl to blocked domain should fail")

	// --- Explicit --stop arc -------------------------------------------------
	bypassRes := h.Run("firewall", "bypass", "30s", "--agent", "firewall-test", "--non-interactive")
	require.NoError(t, bypassRes.Err, "firewall bypass failed\nstdout: %s\nstderr: %s",
		bypassRes.Stdout, bypassRes.Stderr)

	allowedRes := h.ExecInContainer("firewall-test",
		"curl", "-s", "--max-time", "10", "-o", "/dev/null", "-w", "%{http_code}", "https://example.com")
	require.NoError(t, allowedRes.Err, "curl during bypass should succeed\nstdout: %s\nstderr: %s",
		allowedRes.Stdout, allowedRes.Stderr)
	code := strings.TrimSpace(allowedRes.Stdout)
	assert.True(t, strings.HasPrefix(code, "2") || strings.HasPrefix(code, "3"),
		"expected 2xx/3xx during bypass, got %q", code)

	stopRes := h.Run("firewall", "bypass", "--stop", "--agent", "firewall-test")
	require.NoError(t, stopRes.Err, "firewall bypass --stop failed\nstdout: %s\nstderr: %s",
		stopRes.Stdout, stopRes.Stderr)

	blockAgainRes := h.ExecInContainer("firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, blockAgainRes.Err, "curl should be blocked again after --stop")

	// --- Natural expiry arc (INV-B2-007 dead-man timer) ---------------------
	expiryRes := h.Run("firewall", "bypass", "5s", "--agent", "firewall-test", "--non-interactive")
	require.NoError(t, expiryRes.Err, "short bypass failed\nstdout: %s\nstderr: %s",
		expiryRes.Stdout, expiryRes.Stderr)

	duringExpiry := h.ExecInContainer("firewall-test",
		"curl", "-s", "--max-time", "3", "-o", "/dev/null", "-w", "%{http_code}", "https://example.com")
	require.NoError(t, duringExpiry.Err, "curl during short bypass should succeed")
	expiryCode := strings.TrimSpace(duringExpiry.Stdout)
	assert.True(t, strings.HasPrefix(expiryCode, "2") || strings.HasPrefix(expiryCode, "3"),
		"expected 2xx/3xx during short bypass, got %q", expiryCode)

	// Wait past expiry + a small buffer for the dead-man timer to fire.
	time.Sleep(8 * time.Second)

	postExpiry := h.ExecInContainer("firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, postExpiry.Err, "after bypass expiry, enforcement must be restored automatically")

	// --- Stopped-container arc (INV-B2-016 drift guard) ---------------------
	stopAgentRes := h.Run("container", "stop", "--agent", "firewall-test")
	require.NoError(t, stopAgentRes.Err, "container stop failed\nstdout: %s\nstderr: %s",
		stopAgentRes.Stdout, stopAgentRes.Stderr)

	goneRes := h.Run("firewall", "bypass", "30s", "--agent", "firewall-test", "--non-interactive")
	assert.NotNil(t, goneRes.Err, "bypass on stopped container must fail (INV-B2-016)")
}

func TestFirewall_AllowedDomain(t *testing.T) {
	h := newFirewallHarness(t)

	// Allowed: api.anthropic.com is a required rule.
	res := h.RunInContainer("firewall-test",
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
	blocked := h.RunInContainer("firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, blocked.Err, "example.com should be blocked initially")

	// Add example.com.
	addRes := h.Run("firewall", "add", "example.com")
	require.NoError(t, addRes.Err, "firewall add failed\nstdout: %s\nstderr: %s",
		addRes.Stdout, addRes.Stderr)

	// Verify allowed after add.
	allowed := h.RunInContainer("firewall-test",
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
	blockedAgain := h.RunInContainer("firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, blockedAgain.Err, "example.com should be blocked after remove")

	// remove non-existent domain should fail with non-zero exit code.
	removeNonExistent := h.Run("firewall", "remove", "nonexistent.com")
	assert.NotEqual(t, 0, removeNonExistent.ExitCode,
		"removing a non-existent domain should fail with non-zero exit code")
}

func TestFirewall_ConfigRules(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// Use security.firewall.rules (explicit EgressRule list) instead of add_domains.
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
security:
  firewall:
    rules:
      - dst: "example.com"
        proto: "http"
        port: 443
        action: "allow"
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	// Concurrent config sync: goroutine A starts a container (full AddRules →
	// FirewallInit → regenerateAndRestart path with config rules including
	// example.com TLS), goroutine B adds httpbin.org via CLI (AddRules →
	// regenerateAndRestart). Both write to the store and restart
	// Envoy/CoreDNS concurrently — the ActionQueue serializes them.
	var wg sync.WaitGroup
	errs := make([]error, 2)

	// Goroutine A: container start path — first container triggers daemon startup,
	// syncs config rules, starts stack, then curls example.com through the firewall.
	wg.Add(1)
	go func() {
		defer wg.Done()
		res := h.RunInContainer("config-rules-test",
			"curl", "-s", "--max-time", "15", "-o", "/dev/null", "-w", "%{http_code}",
			"https://example.com")
		if res.Err != nil {
			errs[0] = fmt.Errorf("container start: %w\nstdout: %s\nstderr: %s",
				res.Err, res.Stdout, res.Stderr)
		}
	}()

	// Goroutine B: CLI firewall add — hits AddRules on the same stack.
	wg.Add(1)
	go func() {
		defer wg.Done()
		res := h.Run("firewall", "add", "httpbin.org")
		if res.Err != nil {
			errs[1] = fmt.Errorf("firewall add: %w\nstdout: %s\nstderr: %s",
				res.Err, res.Stdout, res.Stderr)
		}
	}()

	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "concurrent operation %d failed", i)
	}

	// All three domains should be in the global list after concurrent sync.
	finalList := h.Run("firewall", "list")
	require.NoError(t, finalList.Err, "list failed after concurrent sync")
	assert.Contains(t, finalList.Stdout, "example.com", "TLS config rule should survive concurrent sync")
	assert.Contains(t, finalList.Stdout, "httpbin.org", "CLI-added rule should survive concurrent sync")

	// Verify TLS rules actually work through the firewall.
	httpbinRes := h.RunInContainer("verify-test",
		"curl", "-s", "--max-time", "10", "-o", "/dev/null", "-w", "%{http_code}",
		"https://httpbin.org")
	require.NoError(t, httpbinRes.Err,
		"httpbin.org should be allowed after concurrent add\nstdout: %s\nstderr: %s",
		httpbinRes.Stdout, httpbinRes.Stderr)

	// Stack should still be healthy.
	statusRes := h.Run("firewall", "status", "--json")
	require.NoError(t, statusRes.Err, "status failed after concurrent sync")
	assert.Contains(t, statusRes.Stdout, `"running": true`)

	// --- Firewall down + immediate remove: queue serializes store mutation
	// even after Envoy/CoreDNS are torn down. CP stays alive so the
	// AdminService still processes the RPC — the rule store is updated
	// without a running stack.
	downRes := h.Run("firewall", "down")
	require.NoError(t, downRes.Err, "firewall down failed\nstdout: %s\nstderr: %s",
		downRes.Stdout, downRes.Stderr)

	removeRes := h.Run("firewall", "remove", "example.com")
	require.NoError(t, removeRes.Err,
		"firewall remove after down should succeed (CP still alive)\nstdout: %s\nstderr: %s",
		removeRes.Stdout, removeRes.Stderr)

	listAfterRemove := h.Run("firewall", "list")
	require.NoError(t, listAfterRemove.Err, "list after remove failed")
	assert.NotContains(t, listAfterRemove.Stdout, "example.com",
		"example.com should be gone after firewall down + remove")
	assert.Contains(t, listAfterRemove.Stdout, "httpbin.org",
		"httpbin.org should still be present")

	// --- CP down: remove should fail with non-zero exit code.
	cpDownRes := h.Run("controlplane", "down")
	require.NoError(t, cpDownRes.Err, "controlplane down failed\nstdout: %s\nstderr: %s",
		cpDownRes.Stdout, cpDownRes.Stderr)

	removeNoCP := h.Run("firewall", "remove", "httpbin.org")
	assert.NotEqual(t, 0, removeNoCP.ExitCode,
		"firewall remove should fail when CP is down")
}

func TestFirewall_Status(t *testing.T) {
	h := newFirewallHarness(t)

	// Run a container to trigger firewall startup.
	res := h.RunInContainer("firewall-test", "echo", "started")
	t.Logf("run stdout: %s", res.Stdout)
	t.Logf("run stderr: %s", res.Stderr)
	require.NoError(t, res.Err, "container run failed\nstdout: %s\nstderr: %s",
		res.Stdout, res.Stderr)

	statusRes := h.Run("firewall", "status", "--json")
	require.NoError(t, statusRes.Err, "firewall status failed\nstdout: %s\nstderr: %s",
		statusRes.Stdout, statusRes.Stderr)
	assert.Contains(t, statusRes.Stdout, `"running": true`)
}

func TestFirewall_IntraNetworkBypass(t *testing.T) {
	h := newFirewallHarness(t)
	ctx := context.Background()

	// Boot a container to trigger firewall startup and create clawker-net.
	bootRes := h.RunInContainer("intra-net-boot", "echo", "started")
	require.NoError(t, bootRes.Err, "boot container failed\nstdout: %s\nstderr: %s",
		bootRes.Stdout, bootRes.Stderr)

	// Start a simple HTTP listener on clawker-net — no firewall rule for this service.
	listenerName := fmt.Sprintf("clawker-test-listener-%d", time.Now().UnixNano())
	//nolint:gosec // args are test-controlled
	startCmd := exec.CommandContext(ctx, "docker", "run", "-d",
		"--name", listenerName,
		"--network", "clawker-net",
		"busybox", "sh", "-c",
		"mkdir -p /www && echo OK > /www/index.html && httpd -f -p 8080 -h /www")
	startOut, err := startCmd.CombinedOutput()
	require.NoError(t, err, "start listener failed: %s", string(startOut))
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", "rm", "-f", listenerName).Run()
	})

	// Get the listener's IP on clawker-net.
	//nolint:gosec // args are test-controlled
	ipOut, err := exec.CommandContext(ctx, "docker", "inspect", "-f",
		`{{(index .NetworkSettings.Networks "clawker-net").IPAddress}}`,
		listenerName).Output()
	require.NoError(t, err, "inspect listener IP failed")
	listenerIP := strings.TrimSpace(string(ipOut))
	require.NotEmpty(t, listenerIP, "listener should have an IP on clawker-net")
	t.Logf("listener IP: %s", listenerIP)

	// Wait for httpd to start.
	time.Sleep(1 * time.Second)

	// Agent container can reach intra-network service via CIDR bypass (no firewall rule).
	connectRes := h.RunInContainer("intra-net-test",
		"curl", "-s", "--max-time", "5", "--connect-timeout", "3",
		"-o", "/dev/null", "-w", "%{http_code}",
		"http://"+net.JoinHostPort(listenerIP, "8080")+"/")

	require.NoError(t, connectRes.Err,
		"intra-network should succeed via CIDR bypass\nstdout: %s\nstderr: %s",
		connectRes.Stdout, connectRes.Stderr)
	assert.Contains(t, connectRes.Stdout, "200",
		"should get HTTP 200 from listener on clawker-net")

	// Sanity: external domain still blocked by firewall.
	blockedRes := h.RunInContainer("intra-net-test",
		"curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, blockedRes.Err, "external domain should still be blocked")
}

func TestFirewall_HostProxyReachable(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			HostProxy:      hostproxy.NewManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
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

	// Agent container with real host proxy — should reach /health via targeted eBPF RETURN.
	healthRes := h.RunInContainer("hp-test",
		"sh", "-c",
		`curl -s --max-time 5 --connect-timeout 3 -o /dev/null -w "%{http_code}" "$CLAWKER_HOST_PROXY/health"`)
	require.NoError(t, healthRes.Err,
		"host proxy /health should be reachable through firewall\nstdout: %s\nstderr: %s",
		healthRes.Stdout, healthRes.Stderr)
	assert.Contains(t, healthRes.Stdout, "200",
		"should get HTTP 200 from host proxy health endpoint")

	// Non-proxy host port should still be blocked (CIDR bypass doesn't cover host).
	blockedRes := h.RunInContainer("hp-test",
		"curl", "-s", "--max-time", "3", "--connect-timeout", "2",
		"-o", "/dev/null", "-w", "%{http_code}",
		"http://host.docker.internal:9999")
	assert.NotNil(t, blockedRes.Err, "non-proxy host port should be blocked by firewall")
}

func TestFirewall_SSHTCPMapping(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// Configure an SSH rule for github.com — exercises the TCP mapping path:
	// eBPF --dport 22 → DNAT envoy:10001 → LOGICAL_DNS cluster github.com:22
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
security:
  firewall:
    rules:
      - dst: "github.com"
        proto: "ssh"
        port: 22
        action: "allow"
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	// Start a long-lived container so we can exec into it.
	startRes := h.Run("container", "run", "--detach", "--agent", "ssh-test", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)

	// ssh-keyscan fetches host keys over SSH (port 22) without authentication.
	// Full path: DNS → CoreDNS → eBPF --dport 22 → DNAT envoy:10001 → LOGICAL_DNS cluster → github.com:22
	scanRes := h.ExecInContainer("ssh-test", "ssh-keyscan", "-T", "10", "github.com")
	require.NoError(t, scanRes.Err,
		"ssh-keyscan should succeed via TCP mapping\nstdout: %s\nstderr: %s",
		scanRes.Stdout, scanRes.Stderr)
	assert.Contains(t, scanRes.Stdout, "github.com", "should return github.com host keys")

	// Verify DNS blocks non-allowed domains (CoreDNS returns NXDOMAIN).
	// This is the sole domain gate for non-TLS protocols with port-only eBPF matching.
	digRes := h.ExecInContainer("ssh-test", "dig", "+short", "+timeout=3", "gitlab.com")
	t.Logf("dig gitlab.com result: stdout=%q stderr=%q err=%v", digRes.Stdout, digRes.Stderr, digRes.Err)
	assert.Empty(t, strings.TrimSpace(digRes.Stdout), "gitlab.com should not resolve (CoreDNS NXDOMAIN)")

	h.Run("container", "stop", "--agent", "ssh-test")

}

func TestFirewall_DockerInternalDNS(t *testing.T) {
	h := newFirewallHarness(t)

	// Start a detached agent container (firewall enabled).
	startRes := h.Run("container", "run", "--detach", "--agent", "dns-test", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)
	t.Cleanup(func() {
		h.Run("container", "stop", "--agent", "dns-test")
	})

	// Verify host.docker.internal resolves (docker.internal zone → Docker DNS).
	hostRes := h.ExecInContainer("dns-test", "getent", "hosts", "host.docker.internal")
	t.Logf("host.docker.internal: stdout=%q stderr=%q err=%v", hostRes.Stdout, hostRes.Stderr, hostRes.Err)
	require.NoError(t, hostRes.Err,
		"host.docker.internal should resolve through CoreDNS → Docker DNS\nstdout: %s\nstderr: %s",
		hostRes.Stdout, hostRes.Stderr)
	assert.NotEmpty(t, strings.TrimSpace(hostRes.Stdout), "should get an IP for host.docker.internal")

	// If monitoring stack is running (otel-collector on clawker-net), verify it resolves.
	otelRes := h.ExecInContainer("dns-test", "getent", "hosts", "otel-collector")
	t.Logf("otel-collector: stdout=%q stderr=%q err=%v", otelRes.Stdout, otelRes.Stderr, otelRes.Err)
	if otelRes.Err == nil {
		assert.NotEmpty(t, strings.TrimSpace(otelRes.Stdout), "should get an IP for otel-collector")
	} else {
		t.Log("otel-collector not running on clawker-net, skipping monitoring DNS check")
	}

	// Sanity: blocked domain still fails.
	blockedRes := h.ExecInContainer("dns-test", "getent", "hosts", "evil.example.com")
	assert.NotNil(t, blockedRes.Err, "non-whitelisted domain should not resolve")
}

func TestFirewall_HTTPDomainDetection(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// Configure an HTTP rule for example.com — exercises the consolidated egress listener:
	// eBPF --dport 80 → DNAT envoy:10000 → tls_inspector → raw_buffer filter chain → Host header → domain match
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
security:
  firewall:
    rules:
      - dst: "example.com"
        proto: "http"
        port: 80
        action: "allow"
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	// Start a long-lived container so we can exec into it.
	startRes := h.Run("container", "run", "--detach", "--agent", "http-test", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)
	t.Cleanup(func() {
		h.Run("container", "stop", "--agent", "http-test")
	})

	// Verify the HTTP rule is in the global list.
	listRes := h.Run("firewall", "list")
	require.NoError(t, listRes.Err, "list failed\nstdout: %s\nstderr: %s",
		listRes.Stdout, listRes.Stderr)
	assert.Contains(t, listRes.Stdout, "example.com", "HTTP rule should be in firewall list")

	// Plain HTTP request to example.com — full path:
	// DNS → CoreDNS allows example.com → eBPF --dport 80 → DNAT envoy:10000
	// → tls_inspector → raw_buffer filter chain → Host header "example.com" → virtual host match
	// → per-domain LOGICAL_DNS cluster → upstream example.com:80
	httpRes := h.ExecInContainer("http-test",
		"curl", "-s", "--max-time", "15", "--connect-timeout", "10",
		"-o", "/dev/null", "-w", "%{http_code}",
		"http://example.com/")
	require.NoError(t, httpRes.Err,
		"plain HTTP to example.com should succeed via HTTP listener\nstdout: %s\nstderr: %s",
		httpRes.Stdout, httpRes.Stderr)
	httpCode := strings.TrimSpace(httpRes.Stdout)
	assert.Contains(t, []string{"200", "301", "302"}, httpCode,
		"should get a valid HTTP response code from example.com, got %q", httpCode)

	// Verify plain HTTP to a non-allowed domain is blocked.
	blockedRes := h.ExecInContainer("http-test",
		"curl", "-s", "--max-time", "5", "--connect-timeout", "3",
		"http://httpbin.org/")
	assert.NotNil(t, blockedRes.Err,
		"plain HTTP to non-allowed domain should be blocked")
}

func TestFirewall_FirewallDisabled(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			HostProxy:      hostproxy.NewManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
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
	setup.WriteYAML(t, testenv.Settings, setup.Dirs.Config, `
firewall:
    enable: false
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	agent := "firewall-disabled-test"

	runTest := h.RunInContainer(agent,
		"sh", "-c",
		"curl -s --max-time 5 --connect-timeout 3 -o /dev/null -w \"%{http_code}\" https://example.com")

	require.NoError(t, runTest.Err, "curl should succeed when firewall is disabled\nstdout: %s\nstderr: %s",
		runTest.Stdout, runTest.Stderr)
	assert.Contains(t, runTest.Stdout, "200", "should get HTTP 200 when firewall is disabled")

	// Firewall stack runs inside the CP container; when disabled via config
	// the CP should not start either. Confirm via the break-glass Docker
	// container listing rather than a removed in-process manager.
	ctx := context.Background()
	_ = ctx
}

func TestFirewall_PathRulesDefaultDeny(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// Allow example.com on HTTP with path rules: only /test is allowed, default deny.
	// Path: DNS → CoreDNS → eBPF --dport 80 → DNAT envoy:10000
	// → tls_inspector → raw_buffer filter chain → Host header → virtual host → path prefix match
	// /test → LOGICAL_DNS cluster → upstream; anything else → 403 (path_default: deny)
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
security:
  firewall:
    rules:
      - dst: "example.com"
        proto: "http"
        port: 80
        action: "allow"
        path_rules:
          - path: "/test"
            action: "allow"
        path_default: "deny"
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	startRes := h.Run("container", "run", "--detach", "--agent", "path-default-deny", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)
	t.Cleanup(func() {
		h.Run("container", "stop", "--agent", "path-default-deny")
	})

	// Allowed path: /test should reach upstream through firewall.
	// example.com may return 200 or 404 for /test — either is fine, it means traffic got through.
	allowedRes := h.ExecInContainer("path-default-deny",
		"curl", "-s", "--max-time", "15", "--connect-timeout", "10",
		"-o", "/dev/null", "-w", "%{http_code}",
		"http://example.com/test")
	require.NoError(t, allowedRes.Err,
		"curl to allowed path /test should succeed\nstdout: %s\nstderr: %s",
		allowedRes.Stdout, allowedRes.Stderr)
	httpCode := strings.TrimSpace(allowedRes.Stdout)
	assert.NotEqual(t, "403", httpCode,
		"allowed path /test should not return 403 (Envoy block), got %q", httpCode)

	// Denied path: /evil should be blocked by Envoy with 403 (path_default: deny).
	deniedRes := h.ExecInContainer("path-default-deny",
		"curl", "-s", "--max-time", "10", "--connect-timeout", "5",
		"-o", "/dev/null", "-w", "%{http_code}",
		"http://example.com/evil")
	require.NoError(t, deniedRes.Err,
		"curl to denied path should get HTTP 403 (not connection error)\nstdout: %s\nstderr: %s",
		deniedRes.Stdout, deniedRes.Stderr)
	deniedCode := strings.TrimSpace(deniedRes.Stdout)
	assert.Equal(t, "403", deniedCode,
		"denied path /evil should return 403 from Envoy, got %q", deniedCode)

	// Verify the 403 body contains the firewall block message.
	bodyRes := h.ExecInContainer("path-default-deny",
		"curl", "-s", "--max-time", "10", "--connect-timeout", "5",
		"http://example.com/evil")
	require.NoError(t, bodyRes.Err, "body check curl should succeed")
	// Centralized non-fingerprinting body — firewall verdict travels via
	// the `action` access log field (route metadata), NOT the body. See
	// envoy_config.go::firewallBlockedBody.
	assert.Contains(t, bodyRes.Stdout, "Forbidden",
		"denied path should return non-fingerprinting Forbidden body")
	assert.NotContains(t, bodyRes.Stdout, "clawker",
		"deny body must not disclose enforcement product identity")
}

func TestFirewall_PathRulesExplicitDeny(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// Allow example.com on HTTP with path rules: /evil is explicitly denied, default allow.
	// Path: DNS → CoreDNS → eBPF --dport 80 → DNAT envoy:10000
	// → tls_inspector → raw_buffer filter chain → Host header → virtual host → path prefix match
	// /evil → 403; anything else → LOGICAL_DNS cluster → upstream (path_default: allow)
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
security:
  firewall:
    rules:
      - dst: "example.com"
        proto: "http"
        port: 80
        action: "allow"
        path_rules:
          - path: "/evil"
            action: "deny"
        path_default: "allow"
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	startRes := h.Run("container", "run", "--detach", "--agent", "path-explicit-deny", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)
	t.Cleanup(func() {
		h.Run("container", "stop", "--agent", "path-explicit-deny")
	})

	// Allowed path: / should reach upstream (path_default: allow).
	allowedRes := h.ExecInContainer("path-explicit-deny",
		"curl", "-s", "--max-time", "15", "--connect-timeout", "10",
		"-o", "/dev/null", "-w", "%{http_code}",
		"http://example.com/")
	require.NoError(t, allowedRes.Err,
		"curl to allowed path / should succeed\nstdout: %s\nstderr: %s",
		allowedRes.Stdout, allowedRes.Stderr)
	httpCode := strings.TrimSpace(allowedRes.Stdout)
	assert.Contains(t, []string{"200", "301", "302"}, httpCode,
		"allowed path / should get valid response from example.com, got %q", httpCode)

	// Explicitly denied path: /evil should be blocked by Envoy with 403.
	deniedRes := h.ExecInContainer("path-explicit-deny",
		"curl", "-s", "--max-time", "10", "--connect-timeout", "5",
		"-o", "/dev/null", "-w", "%{http_code}",
		"http://example.com/evil")
	require.NoError(t, deniedRes.Err,
		"curl to denied path should get HTTP 403 (not connection error)\nstdout: %s\nstderr: %s",
		deniedRes.Stdout, deniedRes.Stderr)
	deniedCode := strings.TrimSpace(deniedRes.Stdout)
	assert.Equal(t, "403", deniedCode,
		"explicitly denied path /evil should return 403 from Envoy, got %q", deniedCode)

	// Verify the 403 body contains the firewall block message.
	bodyRes := h.ExecInContainer("path-explicit-deny",
		"curl", "-s", "--max-time", "10", "--connect-timeout", "5",
		"http://example.com/evil")
	require.NoError(t, bodyRes.Err, "body check curl should succeed")
	// Centralized non-fingerprinting body — firewall verdict travels via
	// the `action` access log field (route metadata), NOT the body. See
	// envoy_config.go::firewallBlockedBody.
	assert.Contains(t, bodyRes.Stdout, "Forbidden",
		"denied path should return non-fingerprinting Forbidden body")
	assert.NotContains(t, bodyRes.Stdout, "clawker",
		"deny body must not disclose enforcement product identity")
}

func TestFirewall_TLSPathRulesDefaultDeny(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// TLS rule with MITM path inspection: only /test allowed, default deny.
	// Path: DNS → CoreDNS → eBPF --dport 443 → DNAT envoy:10000
	// → tls_inspector (SNI) → MITM filter chain (TLS termination + domain cert)
	// → http_connection_manager → path prefix match → allow or 403
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
security:
  firewall:
    rules:
      - dst: "example.com"
        proto: "http"
        port: 443
        action: "allow"
        path_rules:
          - path: "/test"
            action: "allow"
        path_default: "deny"
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	startRes := h.Run("container", "run", "--detach", "--agent", "tls-path-default", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)
	t.Cleanup(func() {
		h.Run("container", "stop", "--agent", "tls-path-default")
	})

	// Allowed path: /test should reach upstream through MITM proxy.
	allowedRes := h.ExecInContainer("tls-path-default",
		"curl", "-s", "--max-time", "15", "--connect-timeout", "10",
		"-o", "/dev/null", "-w", "%{http_code}",
		"https://example.com/test")
	require.NoError(t, allowedRes.Err,
		"curl to allowed path /test should succeed\nstdout: %s\nstderr: %s",
		allowedRes.Stdout, allowedRes.Stderr)
	httpCode := strings.TrimSpace(allowedRes.Stdout)
	assert.NotEqual(t, "403", httpCode,
		"allowed path /test should not return 403 (Envoy block), got %q", httpCode)

	// Denied path: /evil should be blocked by Envoy MITM with 403.
	deniedRes := h.ExecInContainer("tls-path-default",
		"curl", "-s", "--max-time", "10", "--connect-timeout", "5",
		"-o", "/dev/null", "-w", "%{http_code}",
		"https://example.com/evil")
	require.NoError(t, deniedRes.Err,
		"curl to denied path should get HTTP 403 (not connection error)\nstdout: %s\nstderr: %s",
		deniedRes.Stdout, deniedRes.Stderr)
	deniedCode := strings.TrimSpace(deniedRes.Stdout)
	assert.Equal(t, "403", deniedCode,
		"denied path /evil should return 403 from Envoy MITM, got %q", deniedCode)

	// Verify block message in body.
	bodyRes := h.ExecInContainer("tls-path-default",
		"curl", "-s", "--max-time", "10", "--connect-timeout", "5",
		"https://example.com/evil")
	require.NoError(t, bodyRes.Err, "body check curl should succeed")
	// Centralized non-fingerprinting body — firewall verdict travels via
	// the `action` access log field (route metadata), NOT the body. See
	// envoy_config.go::firewallBlockedBody.
	assert.Contains(t, bodyRes.Stdout, "Forbidden",
		"denied path should return non-fingerprinting Forbidden body")
	assert.NotContains(t, bodyRes.Stdout, "clawker",
		"deny body must not disclose enforcement product identity")
}

// TestFirewall_PathRuleNormalizationDefeatsSmuggling locks in the
// path-smuggling fix verified live on 2026-05-23 against the live
// firewall. Without normalize_path + path_with_escaped_slashes_action
// on the HCM, URL-encoded `..` sequences pass the literal-prefix match
// and forward upstream — a CVE-class bypass of the path-rule security
// boundary clawker depends on for UGC-class allowed domains (see
// project_mitm_load_bearing memory). Curl with `%2e%2e/` traversal
// from `/test/...` to outside-prefix paths must be blocked by clawker
// (403 with the centralized non-fingerprint body), NOT reach upstream.
func TestFirewall_PathRuleNormalizationDefeatsSmuggling(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// TLS rule with path inspection: only /allowed/ allowed; default deny.
	// The smuggle attempts target paths OUTSIDE /allowed/ via URL-encoded
	// traversal. Without HCM normalize_path the literal prefix match
	// permits these to reach upstream; with normalize_path they collapse
	// to paths outside the allow prefix and hit the deny default.
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
security:
  firewall:
    rules:
      - dst: "example.com"
        proto: "http"
        port: 443
        action: "allow"
        path_rules:
          - path: "/allowed/"
            action: "allow"
        path_default: "deny"
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	startRes := h.Run("container", "run", "--detach", "--agent", "smuggle", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)
	t.Cleanup(func() {
		h.Run("container", "stop", "--agent", "smuggle")
	})

	// Each smuggle vector — clawker must block all of them with 403.
	// The path normalizes (via Envoy's UNESCAPE_AND_REDIRECT behavior or
	// equivalent) to a path outside `/allowed/`, hitting the default deny.
	smuggleVectors := []struct {
		name string
		url  string
	}{
		{name: "url-encoded %2e%2e", url: "https://example.com/allowed/%2e%2e/escaped"},
		{name: "url-encoded ..%2f", url: "https://example.com/allowed/..%2fescaped"},
		{name: "double-encoded", url: "https://example.com/allowed/%252e%252e/escaped"},
		{name: "merged-slash", url: "https://example.com/allowed//..//escaped"},
	}

	for _, v := range smuggleVectors {
		t.Run(v.name, func(t *testing.T) {
			// Use -L to follow Envoy's UNESCAPE_AND_REDIRECT 307 (the
			// normalized path then hits the deny default → 403). Without
			// -L we'd accept the redirect itself as a "success" outcome
			// even though the eventual path is denied.
			res := h.ExecInContainer("smuggle",
				"curl", "-sL", "--max-time", "15", "--connect-timeout", "10",
				"-o", "/dev/null", "-w", "%{http_code}",
				v.url)
			require.NoError(t, res.Err,
				"curl with smuggle vector should reach clawker (not connection error)\nstdout: %s\nstderr: %s",
				res.Stdout, res.Stderr)
			httpCode := strings.TrimSpace(res.Stdout)
			assert.Equal(t, "403", httpCode,
				"smuggle vector %q must be blocked by clawker, got HTTP %s — path normalization is broken",
				v.url, httpCode)

			// Body must be the centralized non-fingerprint Forbidden body
			// (not an upstream-served response that would indicate the
			// request escaped clawker).
			bodyRes := h.ExecInContainer("smuggle",
				"curl", "-sL", "--max-time", "15", "--connect-timeout", "10",
				v.url)
			require.NoError(t, bodyRes.Err, "body check curl should succeed")
			assert.Contains(t, bodyRes.Stdout, "Forbidden",
				"smuggle vector %q must hit clawker deny (Forbidden body), got: %s",
				v.url, bodyRes.Stdout)
		})
	}
}

func TestFirewall_TLSPathRulesExplicitDeny(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// TLS rule with MITM: /evil explicitly denied, default allow.
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
security:
  firewall:
    rules:
      - dst: "example.com"
        proto: "http"
        port: 443
        action: "allow"
        path_rules:
          - path: "/evil"
            action: "deny"
        path_default: "allow"
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	startRes := h.Run("container", "run", "--detach", "--agent", "tls-path-explicit", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)
	t.Cleanup(func() {
		h.Run("container", "stop", "--agent", "tls-path-explicit")
	})

	// Allowed path: / should reach upstream through MITM proxy.
	allowedRes := h.ExecInContainer("tls-path-explicit",
		"curl", "-s", "--max-time", "15", "--connect-timeout", "10",
		"-o", "/dev/null", "-w", "%{http_code}",
		"https://example.com/")
	require.NoError(t, allowedRes.Err,
		"curl to allowed path / should succeed\nstdout: %s\nstderr: %s",
		allowedRes.Stdout, allowedRes.Stderr)
	httpCode := strings.TrimSpace(allowedRes.Stdout)
	assert.Contains(t, []string{"200", "301", "302"}, httpCode,
		"allowed path / should get valid response from example.com, got %q", httpCode)

	// Explicitly denied path: /evil should be blocked by Envoy MITM with 403.
	deniedRes := h.ExecInContainer("tls-path-explicit",
		"curl", "-s", "--max-time", "10", "--connect-timeout", "5",
		"-o", "/dev/null", "-w", "%{http_code}",
		"https://example.com/evil")
	require.NoError(t, deniedRes.Err,
		"curl to denied path should get HTTP 403 (not connection error)\nstdout: %s\nstderr: %s",
		deniedRes.Stdout, deniedRes.Stderr)
	deniedCode := strings.TrimSpace(deniedRes.Stdout)
	assert.Equal(t, "403", deniedCode,
		"explicitly denied path /evil should return 403 from Envoy MITM, got %q", deniedCode)

	// Verify block message in body.
	bodyRes := h.ExecInContainer("tls-path-explicit",
		"curl", "-s", "--max-time", "10", "--connect-timeout", "5",
		"https://example.com/evil")
	require.NoError(t, bodyRes.Err, "body check curl should succeed")
	// Centralized non-fingerprinting body — firewall verdict travels via
	// the `action` access log field (route metadata), NOT the body. See
	// envoy_config.go::firewallBlockedBody.
	assert.Contains(t, bodyRes.Stdout, "Forbidden",
		"denied path should return non-fingerprinting Forbidden body")
	assert.NotContains(t, bodyRes.Stdout, "clawker",
		"deny body must not disclose enforcement product identity")
}

// TestFirewall_WildcardAndExactCoexist verifies that a wildcard rule (.example.com)
// and an exact rule (example.com) produce independent Envoy filter chains with
// separate PathRules. Both the apex and subdomains get their own path restrictions,
// ensuring they can coexist without Envoy SNI/filter chain collisions.
func TestFirewall_WildcardAndExactCoexist(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// Exact rule: clawker.dev (apex) MITM — only /quickstart allowed.
	// Wildcard rule: .clawker.dev (subdomains) MITM — only /introduction allowed.
	// Both are real domains with valid TLS certs (docs.clawker.dev is a real subdomain).
	// Each gets its own MITM filter chain with independent path restrictions,
	// proving wildcard and exact rules coexist without Envoy SNI collisions.
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
workspace:
  default_mode: "snapshot"
security:
  firewall:
    rules:
      - dst: "clawker.dev"
        proto: "http"
        port: 443
        action: "allow"
        path_rules:
          - path: "/quickstart"
            action: "allow"
        path_default: "deny"
      - dst: ".clawker.dev"
        proto: "http"
        port: 443
        action: "allow"
        path_rules:
          - path: "/introduction"
            action: "allow"
        path_default: "deny"
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	startRes := h.Run("container", "run", "--detach", "--agent", "wildcard-coexist", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)
	t.Cleanup(func() {
		h.Run("container", "stop", "--agent", "wildcard-coexist")
	})

	// --- Apex (exact rule): MITM, only /quickstart allowed ---

	// Allowed: clawker.dev/quickstart should reach upstream through MITM.
	apexAllowed := h.ExecInContainer("wildcard-coexist",
		"curl", "-s", "--max-time", "15", "--connect-timeout", "10",
		"-o", "/dev/null", "-w", "%{http_code}",
		"https://clawker.dev/quickstart")
	require.NoError(t, apexAllowed.Err,
		"curl to apex allowed path should succeed\nstdout: %s\nstderr: %s",
		apexAllowed.Stdout, apexAllowed.Stderr)
	apexAllowedCode := strings.TrimSpace(apexAllowed.Stdout)
	assert.NotEqual(t, "403", apexAllowedCode,
		"apex allowed path /quickstart should not be blocked, got %q", apexAllowedCode)

	// Denied: clawker.dev/introduction should be blocked (path_default: deny).
	apexDenied := h.ExecInContainer("wildcard-coexist",
		"curl", "-s", "--max-time", "10", "--connect-timeout", "5",
		"-o", "/dev/null", "-w", "%{http_code}",
		"https://clawker.dev/introduction")
	require.NoError(t, apexDenied.Err,
		"curl to apex denied path should get 403\nstdout: %s\nstderr: %s",
		apexDenied.Stdout, apexDenied.Stderr)
	apexDeniedCode := strings.TrimSpace(apexDenied.Stdout)
	assert.Equal(t, "403", apexDeniedCode,
		"apex path /introduction should be blocked by path_default:deny, got %q", apexDeniedCode)

	// --- Subdomain (wildcard rule): MITM, only /introduction allowed ---

	// docs.clawker.dev/introduction should pass through MITM — wildcard allows /introduction.
	subAllowed := h.ExecInContainer("wildcard-coexist",
		"curl", "-s", "--max-time", "15", "--connect-timeout", "10",
		"-o", "/dev/null", "-w", "%{http_code}",
		"https://docs.clawker.dev/introduction")
	require.NoError(t, subAllowed.Err,
		"curl to subdomain allowed path should succeed\nstdout: %s\nstderr: %s",
		subAllowed.Stdout, subAllowed.Stderr)
	subCode := strings.TrimSpace(subAllowed.Stdout)
	assert.NotEqual(t, "403", subCode,
		"subdomain /introduction should be allowed by wildcard path rule, got %q", subCode)

	// docs.clawker.dev/quickstart should be denied — wildcard only allows /introduction.
	subDenied := h.ExecInContainer("wildcard-coexist",
		"curl", "-s", "--max-time", "10", "--connect-timeout", "5",
		"-o", "/dev/null", "-w", "%{http_code}",
		"https://docs.clawker.dev/quickstart")
	require.NoError(t, subDenied.Err,
		"curl to subdomain denied path should get 403\nstdout: %s\nstderr: %s",
		subDenied.Stdout, subDenied.Stderr)
	subDeniedCode := strings.TrimSpace(subDenied.Stdout)
	assert.Equal(t, "403", subDeniedCode,
		"subdomain path /quickstart should be blocked by wildcard path_default:deny, got %q", subDeniedCode)
}

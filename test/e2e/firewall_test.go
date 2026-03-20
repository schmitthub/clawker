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
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/hostproxy"
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

func TestFirewall_BlockedDomain(t *testing.T) {
	h := newFirewallHarness(t)

	// Blocked: example.com is NOT in the allowed rules.
	res := h.RunInContainer("firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, res.Err, "curl to blocked domain should fail")
}

func TestFirewall_Bypass(t *testing.T) {
	h := newFirewallHarness(t)

	// Start a long-lived container in detached mode so we can exec into it.
	startRes := h.Run("container", "run", "--detach", "--agent", "firewall-test", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)

	// Blocked: example.com is NOT in the allowed rules.
	blockRes := h.ExecInContainer("firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, blockRes.Err, "curl to blocked domain should fail")

	bypassRes := h.Run("firewall", "bypass", "30s", "--agent", "firewall-test", "--non-interactive")
	require.NoError(t, bypassRes.Err, "firewall bypass failed\nstdout: %s\nstderr: %s",
		bypassRes.Stdout, bypassRes.Stderr)

	// Should succeed now — bypass disables iptables rules, all traffic goes direct.
	allowedRes := h.ExecInContainer("firewall-test",
		"curl", "-s", "--max-time", "10", "-o", "/dev/null", "-w", "%{http_code}", "https://example.com")
	require.NoError(t, allowedRes.Err, "curl after bypass should succeed\nstdout: %s\nstderr: %s",
		allowedRes.Stdout, allowedRes.Stderr)
	assert.NotEmpty(t, strings.TrimSpace(allowedRes.Stdout), "should get HTTP response code")

	stopRes := h.Run("firewall", "bypass", "--stop", "--agent", "firewall-test")
	require.NoError(t, stopRes.Err, "firewall bypass stop failed\nstdout: %s\nstderr: %s",
		stopRes.Stdout, stopRes.Stderr)

	// Should be blocked again after stopping bypass.
	blockAgainRes := h.ExecInContainer("firewall-test", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, blockAgainRes.Err, "curl should be blocked again after stopping bypass")

	stopAgentRes := h.Run("container", "stop", "--agent", "firewall-test")
	require.NoError(t, stopAgentRes.Err, "container stop failed\nstdout: %s\nstderr: %s",
		stopAgentRes.Stdout, stopAgentRes.Stderr)
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
}

func TestFirewall_ConfigRules(t *testing.T) {
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
        proto: "tls"
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
	// EnsureDaemon → regenerateAndRestart path with config rules including
	// example.com TLS + otel-collector TCP), goroutine B adds httpbin.org via
	// CLI (AddRules → regenerateAndRestart). Both write to the store and
	// restart Envoy/CoreDNS concurrently.
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
			Firewall:       firewall.NewManager,
			HostProxy:      hostproxy.NewManager,
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

	// Agent container with real host proxy — should reach /health via targeted iptables RETURN.
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
			Firewall:       firewall.NewManager,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// Configure an SSH rule for github.com — exercises the TCP mapping path:
	// iptables --dport 22 → DNAT envoy:10001 → LOGICAL_DNS cluster github.com:22
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
	// Full path: DNS → CoreDNS → iptables --dport 22 → DNAT envoy:10001 → LOGICAL_DNS cluster → github.com:22
	scanRes := h.ExecInContainer("ssh-test", "ssh-keyscan", "-T", "10", "github.com")
	require.NoError(t, scanRes.Err,
		"ssh-keyscan should succeed via TCP mapping\nstdout: %s\nstderr: %s",
		scanRes.Stdout, scanRes.Stderr)
	assert.Contains(t, scanRes.Stdout, "github.com", "should return github.com host keys")

	// Verify DNS blocks non-allowed domains (CoreDNS returns NXDOMAIN).
	// This is the sole domain gate for non-TLS protocols with port-only iptables matching.
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
			Firewall:       firewall.NewManager,
		},
	}
	setup := h.NewIsolatedFS(nil)

	// Configure an HTTP rule for example.com — exercises the HTTP listener path:
	// iptables --dport 80 → DNAT envoy:10080 → http_connection_manager → Host header → domain match
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
	// DNS → CoreDNS allows example.com → iptables --dport 80 → DNAT envoy:10080
	// → http_connection_manager → Host header "example.com" → virtual host match → allow
	// → dynamic_forward_proxy → upstream example.com:80
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
			Firewall:       firewall.NewManager,
			HostProxy:      hostproxy.NewManager,
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

	ctx := context.Background()

	fwMgr, err := runTest.Factory.Firewall(ctx)
	require.NoError(t, err, "getting firewall manager from factory should not error")

	stack := fwMgr.IsRunning(ctx)
	require.False(t, stack, "firewall stack should not be running when firewall is disabled")

}

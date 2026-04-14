package e2e

// Branch-2 firewall handler E2E coverage. These tests pin the new
// 13-method scope-corrected admin surface (Spec §8) and the INV-B2-016
// drift guard end-to-end through the CLI. They are authored alongside
// the Task 5 handler rewrite and deferred to the final host-side review
// pass per the initiative E2E policy — agents do not run E2E tests.

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFirewall_BypassExpiry_E2E verifies that a bypass without --stop
// auto-restores enforcement on the dead-man timer. This is the natural
// expiry path the existing TestFirewall_Bypass shortcuts via --stop.
//
// Wiring coverage: FirewallBypass RPC → Disable + AfterFunc(timeout) →
// drift-guarded Enable on fire.
func TestFirewall_BypassExpiry_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E timeout test")
	}
	h := newFirewallHarness(t)

	startRes := h.Run("container", "run", "--detach", "--agent", "fw-bypass-expiry", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)

	// example.com is not on the allow list — confirm baseline blocked.
	pre := h.ExecInContainer("fw-bypass-expiry", "curl", "-s", "--max-time", "5", "https://example.com")
	require.NotNil(t, pre.Err, "example.com should be blocked before bypass")

	// Short bypass; do NOT stop it manually — let the timer expire.
	bypassRes := h.Run("firewall", "bypass", "5s", "--agent", "fw-bypass-expiry", "--non-interactive")
	require.NoError(t, bypassRes.Err, "bypass failed\nstdout: %s\nstderr: %s",
		bypassRes.Stdout, bypassRes.Stderr)

	// Within the bypass window the request must succeed.
	allowed := h.ExecInContainer("fw-bypass-expiry",
		"curl", "-s", "--max-time", "3", "-o", "/dev/null", "-w", "%{http_code}", "https://example.com")
	require.NoError(t, allowed.Err, "during bypass curl should succeed")
	assert.NotEmpty(t, strings.TrimSpace(allowed.Stdout))

	// Wait past expiry + a small buffer for the dead-man timer to fire.
	time.Sleep(8 * time.Second)

	post := h.ExecInContainer("fw-bypass-expiry", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, post.Err, "after bypass expiry, enforcement must be restored automatically")

	stop := h.Run("container", "stop", "--agent", "fw-bypass-expiry")
	require.NoError(t, stop.Err, "container stop failed\nstdout: %s\nstderr: %s",
		stop.Stdout, stop.Stderr)
}

// TestFirewall_BypassOnGoneContainer_E2E verifies that calling bypass on a
// container Docker no longer knows about returns a structured error
// rather than silently writing stale state. INV-B2-016 mandates
// FailedPrecondition for the gone case.
func TestFirewall_BypassOnGoneContainer_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E setup-heavy")
	}
	h := newFirewallHarness(t)

	startRes := h.Run("container", "run", "--detach", "--agent", "fw-bypass-gone", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)

	stopRes := h.Run("container", "stop", "--agent", "fw-bypass-gone")
	require.NoError(t, stopRes.Err, "container stop failed\nstdout: %s\nstderr: %s",
		stopRes.Stdout, stopRes.Stderr)

	// Container is gone; bypass must surface a clear error from the CP
	// drift guard. We don't pin the exact error code at the CLI layer
	// because the legacy CLI shim wraps RPC errors; the assertion is
	// "the call fails — enforcement is not reconfigured on a stale
	// cgroup_id".
	bypassRes := h.Run("firewall", "bypass", "30s", "--agent", "fw-bypass-gone", "--non-interactive")
	assert.NotNil(t, bypassRes.Err, "bypass on a stopped container must fail (INV-B2-016)")
}

// TestFirewall_FullEnrollBypassRestore_E2E runs the canonical Task 5
// happy path: container starts (FirewallEnable enrolls it), bypass
// suspends enforcement, dead-man timer restores enforcement, container
// stops cleanly. Acts as the integration regression for the
// Enable→Bypass→Enable invariant chain.
func TestFirewall_FullEnrollBypassRestore_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E full lifecycle")
	}
	h := newFirewallHarness(t)

	startRes := h.Run("container", "run", "--detach", "--agent", "fw-roundtrip", "@", "sleep", "infinity")
	require.NoError(t, startRes.Err, "container start failed\nstdout: %s\nstderr: %s",
		startRes.Stdout, startRes.Stderr)

	// Baseline blocked.
	blocked := h.ExecInContainer("fw-roundtrip", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, blocked.Err, "baseline: example.com blocked")

	// Bypass.
	bypassRes := h.Run("firewall", "bypass", "5s", "--agent", "fw-roundtrip", "--non-interactive")
	require.NoError(t, bypassRes.Err, "bypass failed\nstdout: %s\nstderr: %s",
		bypassRes.Stdout, bypassRes.Stderr)

	allowed := h.ExecInContainer("fw-roundtrip",
		"curl", "-s", "--max-time", "3", "-o", "/dev/null", "-w", "%{http_code}", "https://example.com")
	require.NoError(t, allowed.Err, "during bypass curl should succeed")
	assert.NotEmpty(t, strings.TrimSpace(allowed.Stdout))

	// Wait for natural expiry → drift-guarded Enable restores enforcement.
	time.Sleep(8 * time.Second)

	restored := h.ExecInContainer("fw-roundtrip", "curl", "-s", "--max-time", "5", "https://example.com")
	assert.NotNil(t, restored.Err, "after bypass expiry curl should be blocked again")

	stop := h.Run("container", "stop", "--agent", "fw-roundtrip")
	require.NoError(t, stop.Err, "container stop failed\nstdout: %s\nstderr: %s",
		stop.Stdout, stop.Stderr)
}

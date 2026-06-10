package e2e

// Break-glass `clawker controlplane up/down/status` CLI E2E coverage.
// Pins the Task 7 verbs through the full Cobra → Factory → *docker.Client
// → CP container path. Authored alongside the command implementation and
// deferred to the final host-side review pass per initiative E2E policy
// — agents do not run E2E tests.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

// newControlPlaneHarness matches newFirewallHarness: real Config, real
// docker.Client, real ProjectManager. The CLI binary itself exercises
// the break-glass verbs end-to-end.
func newControlPlaneHarness(t *testing.T) *harness.Harness {
	t.Helper()
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
		},
	}
	h.NewIsolatedFS(nil)
	return h
}

// TestControlPlaneCLI_UpStatusDown walks the break-glass verbs in
// their intended order on a fresh env:
//  1. `controlplane status` before up — container absent, exit 0.
//  2. `controlplane up` — CP boots; `/healthz` green; firewall stack
//     comes up too (firewall.enable defaults true); idempotent on
//     repeat call.
//  3. `controlplane status --json` — container running + healthz ok.
//  4. `controlplane down` — CP removed; CP's SIGTERM handler runs the
//     full firewall + eBPF teardown, so no orphan-firewall warning fires.
//  5. `controlplane status` after down — back to the absent baseline.
func TestControlPlaneCLI_UpStatusDown(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E CP lifecycle test")
	}
	h := newControlPlaneHarness(t)

	statusBefore := h.Run("controlplane", "status", "--json")
	require.NoError(t, statusBefore.Err, "status before up failed: %s", statusBefore.Stderr)

	var pre cpStatusRow
	require.NoError(t, json.Unmarshal([]byte(statusBefore.Stdout), &pre),
		"status output must parse as JSON: %q", statusBefore.Stdout)
	assert.False(t, pre.ContainerRunning, "CP must not be running on a fresh env")

	up := h.Run("controlplane", "up")
	require.NoError(t, up.Err, "controlplane up failed: %s", up.Stderr)
	assert.Contains(t, up.Stdout, "Control plane is up")
	// firewall.enable defaults true — the verb must also bring the
	// Envoy + CoreDNS stack up and block until it's healthy.
	assert.Contains(t, up.Stdout, "Firewall stack up",
		"controlplane up must bring the firewall stack up when firewall.enable is set")

	// Idempotent re-invocation — must succeed and not duplicate the
	// container. A second `up` on an already-running CP is the canonical
	// break-glass scenario operators run.
	upAgain := h.Run("controlplane", "up")
	require.NoError(t, upAgain.Err, "second controlplane up failed: %s", upAgain.Stderr)

	statusUp := h.Run("controlplane", "status", "--json")
	require.NoError(t, statusUp.Err, "status while up failed: %s", statusUp.Stderr)

	var running cpStatusRow
	require.NoError(t, json.Unmarshal([]byte(statusUp.Stdout), &running),
		"status output must parse as JSON: %q", statusUp.Stdout)
	assert.True(t, running.ContainerRunning, "CP container should be running")
	assert.True(t, running.HealthzOK, "healthz should be reporting 200")
	assert.True(t, running.FirewallRunning, "firewall stack should be up after controlplane up (firewall.enable defaults true)")
	assert.True(t, running.FirewallReady, "firewall stack should be healthy after controlplane up")

	down := h.Run("controlplane", "down")
	require.NoError(t, down.Err, "controlplane down failed: %s", down.Stderr)
	assert.Contains(t, down.Stdout, "Control plane stopped")
	// The CP's SIGTERM handler runs the full firewall + eBPF teardown
	// before exiting (INV-B2-008, reworked). No orphan warning: if one
	// fires, the CP shutdown path is incomplete.
	assert.NotContains(t, down.Stderr, "Envoy and CoreDNS",
		"orphan-firewall warning indicates CP SIGTERM didn't run full teardown")

	statusAfter := h.Run("controlplane", "status", "--json")
	require.NoError(t, statusAfter.Err, "status after down failed: %s", statusAfter.Stderr)

	var post cpStatusRow
	require.NoError(t, json.Unmarshal([]byte(statusAfter.Stdout), &post))
	assert.False(t, post.ContainerRunning, "CP must be absent after down")
}

// TestControlPlaneCLI_DownOnAbsentCP verifies that `controlplane down`
// is a no-op when the CP is not running, preserving the short-circuit
// contract: no ensureRunning side effect, no error, no warning spam.
func TestControlPlaneCLI_DownOnAbsentCP(t *testing.T) {
	h := newControlPlaneHarness(t)

	// Best-effort pre-clean so the test is robust against stray state
	// from a previous crashed run.
	cleanupCPIfPresent(t, 30*time.Second)

	res := h.Run("controlplane", "down")
	require.NoError(t, res.Err, "down on absent CP should succeed: %s", res.Stderr)
	assert.Contains(t, res.Stdout, "not running")
	// The orphan-firewall warning is only emitted when we actually tore
	// the CP down — a no-op path must stay quiet on stderr.
	assert.False(t, strings.Contains(res.Stderr, "Envoy and CoreDNS"),
		"warning must not fire on the no-op path")
}

// cpStatusRow mirrors the JSON schema emitted by `controlplane status
// --json`. Duplicated here rather than imported to keep this E2E file
// self-contained and guard against accidental drift if the command
// output shape changes.
type cpStatusRow struct {
	ContainerRunning bool   `json:"container_running"`
	HealthzOK        bool   `json:"healthz_ok"`
	HealthzStatus    int    `json:"healthz_status,omitempty"`
	HealthzError     string `json:"healthz_error,omitempty"`
	FirewallRunning  bool   `json:"firewall_running"`
	FirewallReady    bool   `json:"firewall_ready"`
	FirewallRuleCnt  int    `json:"firewall_rule_count"`
	FirewallError    string `json:"firewall_error,omitempty"`
}

// cleanupCPIfPresent stops and removes the CP container when it exists.
// Used by the no-op path test to guarantee a clean precondition — the
// harness cleanup runs at end-of-test, not at start.
func cleanupCPIfPresent(t *testing.T, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Build a minimal docker.Client for the precondition sweep.
	dc, err := docker.NewClient(ctx, nil, logger.Nop())
	if err != nil {
		t.Logf("cleanup: docker client: %v (skipping pre-clean)", err)
		return
	}
	defer func() { _ = dc.Close() }()
	if err := cpboot.Stop(ctx, dc); err != nil {
		t.Logf("cleanup: cpboot.Stop: %v (ignoring — test robust)", err)
	}
}

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	fwcp "github.com/schmitthub/clawker/internal/controlplane/firewall"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/pkg/whail"
)

// TestFirewallStack_E2E exercises firewall.Stack lifecycle against real
// Docker: bring clawker-net + Envoy + CoreDNS up, assert Status reflects
// that, then tear down. The test constructs a Stack directly — tests
// that exercise the full CLI → AdminService → Stack path live in
// firewall_test.go.
func TestFirewallStack_E2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg := configmocks.NewIsolatedTestConfig(t)
	log := logger.Nop()

	dc, err := docker.NewClient(ctx, cfg, log)
	require.NoError(t, err, "constructing docker client")
	t.Cleanup(func() { _ = dc.Close() })

	// CLI is the authoritative owner of clawker-net; the Stack's EnsureRunning
	// defensive guard also creates it, but we bring it up explicitly here so
	// the assertion surface is unambiguous.
	_, err = dc.EnsureNetwork(ctx, whail.EnsureNetworkOptions{Name: cfg.ClawkerNetwork()})
	require.NoError(t, err, "ensure clawker-net")

	store, err := fwcp.NewRulesStore(cfg)
	require.NoError(t, err, "constructing rules store")

	// Seed the store with the project's required rules so the generated
	// Envoy + CoreDNS configs include the critical allow-list for CI
	// domains (api.anthropic.com, etc.).
	require.NoError(t, store.Set(func(f *fwcp.EgressRulesFile) {
		f.Rules = append(f.Rules, cfg.RequiredFirewallRules()...)
	}), "seeding required rules")
	require.NoError(t, store.Write(), "writing seeded rules")

	stack := fwcp.NewStack(dc, cfg, log, store)

	// Teardown runs regardless of which assertion fails — leaving Envoy
	// or CoreDNS behind would poison the shared clawker-net for the next
	// test in the suite.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := stack.Stop(cleanupCtx); err != nil {
			t.Logf("stack cleanup error: %v", err)
		}
	})

	require.NoError(t, stack.EnsureRunning(ctx), "EnsureRunning")

	status, err := stack.Status(ctx)
	require.NoError(t, err, "Status after EnsureRunning")
	assert.True(t, status.Running, "stack should report running")
	assert.True(t, status.EnvoyHealth, "envoy should report healthy")
	assert.True(t, status.CoreDNSHealth, "coredns should report healthy")
	assert.NotEmpty(t, status.EnvoyIP, "EnvoyIP should be populated")
	assert.NotEmpty(t, status.CoreDNSIP, "CoreDNSIP should be populated")
	assert.NotEmpty(t, status.NetworkID, "NetworkID should be populated")
	assert.GreaterOrEqual(t, status.RuleCount, 1, "at least one required rule seeded")

	// Accessors consistent with status.
	assert.Equal(t, status.EnvoyIP, stack.EnvoyIP())
	assert.Equal(t, status.CoreDNSIP, stack.CoreDNSIP())
	assert.Equal(t, status.NetworkID, stack.NetworkID())
	assert.NotEmpty(t, stack.CIDR(), "CIDR should be populated")

	// Idempotent re-entry: a second EnsureRunning must succeed without
	// recreating containers.
	require.NoError(t, stack.EnsureRunning(ctx), "second EnsureRunning should be idempotent")

	// Reload regenerates configs and restarts the containers.
	require.NoError(t, stack.Reload(ctx), "Reload")

	// Stop removes both containers.
	require.NoError(t, stack.Stop(ctx), "Stop")

	// After Stop, Status reports not running; the network stays in place
	// so agent containers attached to it aren't orphaned.
	postStopStatus, err := stack.Status(ctx)
	require.NoError(t, err, "Status after Stop")
	assert.False(t, postStopStatus.Running, "stack should not be running after Stop")
	assert.False(t, postStopStatus.EnvoyHealth, "envoy should not be healthy after Stop")
	assert.False(t, postStopStatus.CoreDNSHealth, "coredns should not be healthy after Stop")

}

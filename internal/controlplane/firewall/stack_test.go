package firewall_test

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	fwcp "github.com/schmitthub/clawker/internal/controlplane/firewall"
	dockermocks "github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
)

// stackFixture wires up a Stack backed by FakeClient with default
// NetworkInspect + empty ContainerList stubs. Individual tests override
// only the Fn fields they care about.
type stackFixture struct {
	cfg   config.Config
	fake  *dockermocks.FakeClient
	stack *fwcp.Stack
}

func newStackFixture(t *testing.T) *stackFixture {
	t.Helper()
	cfg := configmocks.NewIsolatedTestConfig(t)
	store, err := fwcp.NewRulesStore(cfg)
	require.NoError(t, err)

	fake := dockermocks.NewFakeClient(cfg)
	// Default: clawker-net exists with managed labels + a valid IPAM
	// config matching config defaults. Tests wanting a missing-network
	// scenario override NetworkInspectFn to return ErrNetworkNotFound.
	fake.FakeAPI.NetworkInspectFn = func(_ context.Context, name string, _ mobyclient.NetworkInspectOptions) (mobyclient.NetworkInspectResult, error) {
		return mobyclient.NetworkInspectResult{
			Network: network.Inspect{
				Network: network.Network{
					Name:   name,
					ID:     "net-" + name,
					Labels: map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()},
					IPAM: network.IPAM{
						Config: []network.IPAMConfig{
							{
								Subnet:  netip.MustParsePrefix("172.20.0.0/16"),
								Gateway: netip.MustParseAddr("172.20.0.1"),
							},
						},
					},
				},
			},
		}, nil
	}
	fake.FakeAPI.ContainerListFn = func(context.Context, mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		return mobyclient.ContainerListResult{}, nil
	}
	stack := fwcp.NewStack(fake.Client, cfg, logger.Nop(), store, nil)
	return &stackFixture{cfg: cfg, fake: fake, stack: stack}
}

func TestStack_Accessors_EmptyWhenNetworkMissing(t *testing.T) {
	// Pre-bring-up state: the clawker-net network does not exist yet.
	// Accessors must return empty strings rather than panicking or
	// returning stale values.
	cfg := configmocks.NewIsolatedTestConfig(t)
	store, err := fwcp.NewRulesStore(cfg)
	require.NoError(t, err)
	fake := dockermocks.NewFakeClient(cfg)
	fake.FakeAPI.NetworkInspectFn = func(context.Context, string, mobyclient.NetworkInspectOptions) (mobyclient.NetworkInspectResult, error) {
		return mobyclient.NetworkInspectResult{}, errors.New("network not found")
	}

	stack := fwcp.NewStack(fake.Client, cfg, logger.Nop(), store, nil)
	assert.Empty(t, stack.EnvoyIP())
	assert.Empty(t, stack.CoreDNSIP())
	assert.Empty(t, stack.NetworkID())
	assert.Empty(t, stack.CIDR())
}

func TestStack_Status_EmptyRulesEmptyStackWhenAbsent(t *testing.T) {
	f := newStackFixture(t)

	status, err := f.stack.Status(t.Context())
	require.NoError(t, err)
	assert.False(t, status.Running)
	assert.False(t, status.EnvoyHealth)
	assert.False(t, status.CoreDNSHealth)
	assert.Equal(t, 0, status.RuleCount)
	// Topology fields populated from the fake's IPAM data; exact IP
	// arithmetic is covered by TestComputeStaticIP.
	assert.NotEmpty(t, status.EnvoyIP)
	assert.NotEmpty(t, status.CoreDNSIP)
	assert.NotEmpty(t, status.NetworkID)
	assert.Equal(t, "172.20.0.0/16", f.stack.CIDR())
}

func TestStack_Status_BothContainersRunning(t *testing.T) {
	f := newStackFixture(t)
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, _ mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		return mobyclient.ContainerListResult{Items: []container.Summary{
			{ID: "envoy-1", State: container.StateRunning},
		}}, nil
	}

	status, err := f.stack.Status(t.Context())
	require.NoError(t, err)
	assert.True(t, status.Running)
	assert.True(t, status.EnvoyHealth)
	assert.True(t, status.CoreDNSHealth)
}

func TestStack_Status_PropagatesDockerError(t *testing.T) {
	// A failing ContainerList means the daemon is unreachable — Status
	// must return the error so callers distinguish "stack down" from
	// "Docker unreachable".
	f := newStackFixture(t)
	sentinel := errors.New("docker unreachable")
	f.fake.FakeAPI.ContainerListFn = func(context.Context, mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		return mobyclient.ContainerListResult{}, sentinel
	}

	_, err := f.stack.Status(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker unreachable")
}

func TestStack_Stop_NoContainersIsNoOp(t *testing.T) {
	f := newStackFixture(t)
	err := f.stack.Stop(t.Context())
	require.NoError(t, err)
	// Short-circuit path: no containers to act on, so neither Stop nor
	// Remove should have been dispatched to Docker.
	assert.NotContains(t, f.fake.FakeAPI.Calls, "ContainerStop")
	assert.NotContains(t, f.fake.FakeAPI.Calls, "ContainerRemove")
}

func TestStack_Stop_RemovesBothContainers(t *testing.T) {
	f := newStackFixture(t)

	// Both containers report as running so Stop calls Stop then Remove.
	f.fake.FakeAPI.ContainerListFn = func(_ context.Context, opts mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		id := "envoy-id"
		if _, ok := opts.Filters["name"]["clawker-coredns"]; ok {
			id = "coredns-id"
		}
		return mobyclient.ContainerListResult{Items: []container.Summary{
			{ID: id, State: container.StateRunning, Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()}},
		}}, nil
	}
	var stopped, removed []string
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: id,
				Config: &container.Config{
					Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
				},
			},
		}, nil
	}
	f.fake.FakeAPI.ContainerStopFn = func(_ context.Context, id string, _ mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		stopped = append(stopped, id)
		return mobyclient.ContainerStopResult{}, nil
	}
	f.fake.FakeAPI.ContainerRemoveFn = func(_ context.Context, id string, _ mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		removed = append(removed, id)
		return mobyclient.ContainerRemoveResult{}, nil
	}

	err := f.stack.Stop(t.Context())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"envoy-id", "coredns-id"}, stopped)
	assert.ElementsMatch(t, []string{"envoy-id", "coredns-id"}, removed)
}

func TestStack_Stop_SurfacesRemoveError(t *testing.T) {
	// A failed remove must propagate — leaving orphan containers behind
	// silently would corrupt the next EnsureRunning and block operators
	// from diagnosing shutdown failures.
	f := newStackFixture(t)
	f.fake.FakeAPI.ContainerListFn = func(context.Context, mobyclient.ContainerListOptions) (mobyclient.ContainerListResult, error) {
		return mobyclient.ContainerListResult{Items: []container.Summary{
			{ID: "envoy-id", State: container.StateRunning, Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()}},
		}}, nil
	}
	f.fake.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: id,
				Config: &container.Config{
					Labels: map[string]string{f.cfg.LabelManaged(): f.cfg.ManagedLabelValue()},
				},
			},
		}, nil
	}
	f.fake.FakeAPI.ContainerStopFn = func(context.Context, string, mobyclient.ContainerStopOptions) (mobyclient.ContainerStopResult, error) {
		return mobyclient.ContainerStopResult{}, nil
	}
	removeErr := errors.New("remove failed")
	f.fake.FakeAPI.ContainerRemoveFn = func(context.Context, string, mobyclient.ContainerRemoveOptions) (mobyclient.ContainerRemoveResult, error) {
		return mobyclient.ContainerRemoveResult{}, removeErr
	}

	err := f.stack.Stop(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removing container")
}

func TestStack_Reload_NoOpWhenStackDown(t *testing.T) {
	// Reload must regenerate configs and then no-op if containers are
	// absent — the next EnsureRunning picks up the new configs. Calling
	// restart on missing containers would otherwise error.
	f := newStackFixture(t)
	require.NoError(t, f.stack.Reload(t.Context()))

	// ensureConfigs wrote envoy.yaml and Corefile into the firewall data
	// subdir before the running-check short-circuited. If this regresses,
	// the next EnsureRunning call would start containers with stale configs.
	dataDir, err := f.cfg.FirewallDataSubdir()
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dataDir, "envoy.yaml"))
	assert.FileExists(t, filepath.Join(dataDir, "Corefile"))

	// No restart should have been dispatched.
	assert.NotContains(t, f.fake.FakeAPI.Calls, "ContainerRestart")
}

package firewall_test

import (
	"context"
	"errors"
	"testing"

	"github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	fwcp "github.com/schmitthub/clawker/internal/controlplane/firewall"
	dockermocks "github.com/schmitthub/clawker/internal/docker/mocks"
)

// managedInspectFn builds a ContainerInspectFn that returns a managed
// container with the given long ID. whail.Engine.ContainerInspect passes
// through its managed-label jail and the returned ID has to be the long
// canonical form so downstream callers see the resolved ref.
func managedInspectFn(cfg config.Config, longID string, captured *string) func(context.Context, string, mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
	return func(_ context.Context, ref string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		if captured != nil {
			*captured = ref
		}
		return mobyclient.ContainerInspectResult{
			Container: container.InspectResponse{
				ID: longID,
				Config: &container.Config{
					Labels: map[string]string{cfg.LabelManaged(): cfg.ManagedLabelValue()},
				},
			},
		}, nil
	}
}

// longHexID is a 64-char lowercase hex string suitable for use as a
// canonical Docker container ID in tests.
const longHexID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestEBPFCgroupPath(t *testing.T) {
	// Unknown driver must fall back to cgroupfs. Defaulting to systemd
	// would produce ENOENT on cgroupfs hosts; Docker Desktop (cgroupfs)
	// is where most alpha testers run.
	cases := []struct {
		driver string
		want   string
	}{
		{"systemd", "/sys/fs/cgroup/system.slice/docker-" + longHexID + ".scope"},
		{"cgroupfs", "/sys/fs/cgroup/docker/" + longHexID},
		{"", "/sys/fs/cgroup/docker/" + longHexID},
	}
	for _, tc := range cases {
		t.Run(tc.driver, func(t *testing.T) {
			assert.Equal(t, tc.want, fwcp.EBPFCgroupPath(tc.driver, longHexID))
		})
	}
}

func TestDetectCgroupDriver_PropagatesInfoError(t *testing.T) {
	// A failed Info call must propagate — silently defaulting to cgroupfs
	// would mask daemon outages and produce cryptic ENOENT downstream from
	// eBPF attach.
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	sentinel := errors.New("docker daemon unreachable")
	fake.FakeAPI.InfoFn = func(_ context.Context, _ mobyclient.InfoOptions) (mobyclient.SystemInfoResult, error) {
		return mobyclient.SystemInfoResult{}, sentinel
	}

	_, err := fwcp.DetectCgroupDriver(t.Context(), fake.Client)
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

func TestResolveContainerID_ShortCircuitsOnCanonicalID(t *testing.T) {
	// A 64-char lowercase hex input is already canonical — skip the Docker
	// round-trip. ContainerInspect is intentionally left unset so a
	// regression would panic with "not implemented".
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.FakeAPI.ContainerInspectFn = nil

	got, err := fwcp.ResolveContainerID(t.Context(), fake.Client, longHexID)
	require.NoError(t, err)
	assert.Equal(t, longHexID, got)
	assert.NotContains(t, fake.FakeAPI.Calls, "ContainerInspect")
}

func TestResolveContainerID_ResolvesNameViaInspect(t *testing.T) {
	const friendly = "clawker.myapp.dev"
	cfg := configmocks.NewBlankConfig()
	fake := dockermocks.NewFakeClient(cfg)
	var seen string
	fake.FakeAPI.ContainerInspectFn = managedInspectFn(cfg, longHexID, &seen)

	got, err := fwcp.ResolveContainerID(t.Context(), fake.Client, friendly)
	require.NoError(t, err)
	assert.Equal(t, longHexID, got)
	assert.Equal(t, friendly, seen, "inspect should receive the caller's ref verbatim")
}

func TestResolveContainerID_RejectsShortHexID(t *testing.T) {
	// A 12-char short ID has the right alphabet but wrong length — must
	// fall through to Docker so it can be expanded to the long form.
	const shortID = "0123456789ab"
	cfg := configmocks.NewBlankConfig()
	fake := dockermocks.NewFakeClient(cfg)
	var seen string
	fake.FakeAPI.ContainerInspectFn = managedInspectFn(cfg, longHexID, &seen)

	got, err := fwcp.ResolveContainerID(t.Context(), fake.Client, shortID)
	require.NoError(t, err)
	assert.Equal(t, longHexID, got)
	assert.Equal(t, shortID, seen, "short ID must be passed verbatim to ContainerInspect")
}

func TestResolveContainerID_RejectsNonHexLong(t *testing.T) {
	// 64 characters but containing a non-hex character is a friendly name,
	// not an ID. Must fall through to ContainerInspect.
	ref := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	cfg := configmocks.NewBlankConfig()
	fake := dockermocks.NewFakeClient(cfg)
	var seen string
	fake.FakeAPI.ContainerInspectFn = managedInspectFn(cfg, longHexID, &seen)

	_, err := fwcp.ResolveContainerID(t.Context(), fake.Client, ref)
	require.NoError(t, err)
	assert.Equal(t, ref, seen, "64-char non-hex ref must fall through to ContainerInspect")
}

func TestResolveContainerID_PropagatesLookupError(t *testing.T) {
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	sentinel := errors.New("no such container")
	fake.FakeAPI.ContainerInspectFn = func(_ context.Context, _ string, _ mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error) {
		return mobyclient.ContainerInspectResult{}, sentinel
	}

	_, err := fwcp.ResolveContainerID(t.Context(), fake.Client, "clawker.unknown.dev")
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

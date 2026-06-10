package firewall

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	dockermocks "github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

// overrideCPBinarySHAForTest swaps the package-init'd consts.CPBinarySHA
// (read from the CP container env in production) for the test's value.
// Same override-and-restore approach as overrideHostPathsForTest in
// container_spec_test.go — package init ran before testenv could set
// the env var.
func overrideCPBinarySHAForTest(t *testing.T, sha string) {
	t.Helper()
	orig := consts.CPBinarySHA
	consts.CPBinarySHA = sha
	t.Cleanup(func() { consts.CPBinarySHA = orig })
}

// TestStack_driftLabels_StampsStackBuildSHA pins the provenance
// contract: the sibling drift label carries consts.CPBinarySHA
// specifically (the env-injected CP-binary hash), not some other
// deterministic value — the ensureContainer tests below can't tell
// those apart because they derive desired and running labels from the
// same driftLabels() call.
func TestStack_driftLabels_StampsStackBuildSHA(t *testing.T) {
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)
	s := NewStack(nil, cfg, logger.Nop(), nil, nil)

	overrideCPBinarySHAForTest(t, "sha-v1")
	assert.Equal(t, "sha-v1", s.driftLabels()[labelStackBuildSHA])
}

// newDriftFixture builds a Stack on a FakeClient plus a running sibling
// summary whose labels match the current desired drift labels exactly;
// tests mutate the summary's labels to model staleness.
func newDriftFixture(t *testing.T) (*dockermocks.FakeClient, *Stack, container.Summary) {
	t.Helper()
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)
	fake := dockermocks.NewFakeClient(cfg)
	s := NewStack(fake.Client, cfg, logger.Nop(), nil, nil)

	labels := s.driftLabels()
	labels[cfg.LabelManaged()] = cfg.ManagedLabelValue()
	running := container.Summary{
		ID:     "envoy-existing-id",
		Names:  []string{"/" + envoyContainerName},
		State:  container.StateRunning,
		Labels: labels,
	}

	fake.SetupContainerStop()
	fake.SetupContainerRemove()
	fake.SetupContainerCreate()
	fake.SetupContainerStart()
	return fake, s, running
}

// TestStack_ensureContainer_RecreatesOnStackBuildSHADrift exercises the
// stale-binary path: a running sibling stamped with an older build SHA
// must be stopped, removed, and recreated even though infra_certs_ready
// and otel_infra_port still match. (The legacy no-label-at-all variant
// has its own test below.)
func TestStack_ensureContainer_RecreatesOnStackBuildSHADrift(t *testing.T) {
	overrideCPBinarySHAForTest(t, "sha-new")
	fake, s, running := newDriftFixture(t)
	running.Labels[labelStackBuildSHA] = "sha-old"
	fake.SetupContainerList(running)

	spec := containerSpec{
		image:     "img:test",
		staticIP:  "172.20.0.2",
		networkID: "net-test",
		labels:    s.driftLabels(),
	}
	require.NoError(t, s.ensureContainer(context.Background(), envoyContainerName, spec))

	assert.Contains(t, fake.FakeAPI.Calls, "ContainerStop", "stale sibling must be stopped")
	assert.Contains(t, fake.FakeAPI.Calls, "ContainerRemove", "stale sibling must be removed")
	assert.Contains(t, fake.FakeAPI.Calls, "ContainerCreate", "sibling must be recreated from the new spec")
}

// TestStack_ensureContainer_AdoptsOnMatchingStackBuildSHA pins the
// no-churn side: a running sibling whose full label set (including the
// build SHA) matches the desired spec is adopted as-is.
func TestStack_ensureContainer_AdoptsOnMatchingStackBuildSHA(t *testing.T) {
	overrideCPBinarySHAForTest(t, "sha-current")
	fake, s, running := newDriftFixture(t)
	fake.SetupContainerList(running)

	spec := containerSpec{
		image:     "img:test",
		staticIP:  "172.20.0.2",
		networkID: "net-test",
		labels:    s.driftLabels(),
	}
	require.NoError(t, s.ensureContainer(context.Background(), envoyContainerName, spec))

	assert.NotContains(t, fake.FakeAPI.Calls, "ContainerStop")
	assert.NotContains(t, fake.FakeAPI.Calls, "ContainerRemove")
	assert.NotContains(t, fake.FakeAPI.Calls, "ContainerCreate")
}

// TestStack_ensureContainer_RecreatesOnMissingStackBuildSHALabel pins
// the real upgrade path separately from the different-value case: every
// sibling created by a pre-SHA-label build has NO labelStackBuildSHA
// key at all. The two ride the same compare branch today only because
// specMatchesContainer reads the missing key as "" via map zero-value —
// a rewrite that tolerates absent keys would break upgrades while the
// different-value test stays green.
func TestStack_ensureContainer_RecreatesOnMissingStackBuildSHALabel(t *testing.T) {
	overrideCPBinarySHAForTest(t, "sha-new")
	fake, s, running := newDriftFixture(t)
	delete(running.Labels, labelStackBuildSHA)
	fake.SetupContainerList(running)

	spec := containerSpec{
		image:     "img:test",
		staticIP:  "172.20.0.2",
		networkID: "net-test",
		labels:    s.driftLabels(),
	}
	require.NoError(t, s.ensureContainer(context.Background(), envoyContainerName, spec))

	assert.Contains(t, fake.FakeAPI.Calls, "ContainerStop", "legacy unlabeled sibling must be stopped")
	assert.Contains(t, fake.FakeAPI.Calls, "ContainerRemove", "legacy unlabeled sibling must be removed")
	assert.Contains(t, fake.FakeAPI.Calls, "ContainerCreate", "sibling must be recreated from the new spec")
}

// TestContainerSpecs_CarryDriftLabels closes the wiring gap the tests
// above structurally cannot see: they build containerSpec literals with
// labels: s.driftLabels() themselves. If a refactor dropped that field
// from the production spec constructors, specMatchesContainer would
// iterate an empty want map and adopt every stale sibling forever —
// with every drift test still green. Assert the production specs carry
// the full drift label set.
func TestContainerSpecs_CarryDriftLabels(t *testing.T) {
	overrideCPBinarySHAForTest(t, "sha-wired")
	testenv.New(t)
	cfg := configmocks.NewIsolatedTestConfig(t)
	s := NewStack(nil, cfg, logger.Nop(), nil, nil)
	netInfo := &NetworkInfo{NetworkID: "net-test", EnvoyIP: "172.20.0.2", CoreDNSIP: "172.20.0.3"}

	want := s.driftLabels()
	require.NotEmpty(t, want)
	require.Equal(t, "sha-wired", want[labelStackBuildSHA])

	for name, spec := range map[string]containerSpec{
		"envoy":   s.envoyContainerSpec(netInfo),
		"coredns": s.corednsContainerSpec(netInfo),
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range want {
				assert.Equal(t, v, spec.labels[k], "drift label %s must be wired into the %s spec", k, name)
			}
		})
	}
}

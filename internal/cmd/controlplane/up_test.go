package controlplane

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/controlplane/manager"
	"github.com/schmitthub/clawker/controlplane/manager/mocks"
	cpmocks "github.com/schmitthub/clawker/controlplane/mocks"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	dockermocks "github.com/schmitthub/clawker/internal/docker/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
)

// testBed bundles a wired Factory with its stdout/stderr capture buffers
// and the concrete ManagerMock so tests can program per-method responses
// without reaching into the Factory struct.
type testBed struct {
	F      *cmdutil.Factory
	Mock   *mocks.ManagerMock
	Stdout *bytes.Buffer
	Stderr *bytes.Buffer
}

// newTestBed returns a testBed with a fresh ManagerMock wired as
// f.ControlPlane. Every Manager method is nil by default — tests program
// the methods they exercise; unprogrammed methods panic, which is the
// failure signal we want for "this path shouldn't call X".
func newTestBed(t *testing.T) *testBed {
	t.Helper()
	ios, _, stdout, stderr := iostreams.Test()
	mgr := &mocks.ManagerMock{}
	f := &cmdutil.Factory{
		IOStreams:    ios,
		ControlPlane: func() manager.Manager { return mgr },
	}
	return &testBed{F: f, Mock: mgr, Stdout: stdout, Stderr: stderr}
}

func upOptsFrom(tb *testBed) *UpOptions {
	return &UpOptions{
		IOStreams:    tb.F.IOStreams,
		Config:       tb.F.Config,
		Client:       tb.F.Client,
		ControlPlane: tb.F.ControlPlane,
		AdminClient:  tb.F.AdminClient,
	}
}

// withDockerFake wires a FakeClient as f.Client, listing the given
// container summaries (empty = no firewall stack containers).
func withDockerFake(tb *testBed, containers ...container.Summary) *dockermocks.FakeClient {
	fake := dockermocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(containers...)
	tb.F.Client = func(_ context.Context) (*docker.Client, error) {
		return fake.Client, nil
	}
	return fake
}

// withSettings wires a ConfigMock built from the given settings YAML
// onto the testBed's Factory. Empty YAML means "defaults" (blank config
// — firewall enabled).
func withSettings(tb *testBed, settingsYAML string) {
	tb.F.Config = func() (config.Config, error) {
		if settingsYAML == "" {
			return configmocks.NewBlankConfig(), nil
		}
		return configmocks.NewFromString("", settingsYAML), nil
	}
}

// withAdminMock wires an AdminServiceClientMock through f.AdminClient.
func withAdminMock(tb *testBed) *cpmocks.AdminServiceClientMock {
	adminMock := &cpmocks.AdminServiceClientMock{}
	tb.F.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return adminMock, nil
	}
	return adminMock
}

// TestUpRun_FirewallEnabled_BringsUpStack pins the settings-driven verb
// contract: with firewall.enable true (the default), `controlplane up`
// must bring the firewall stack up via FirewallInit after the CP itself
// is running — including on the idempotent path where the CP was already
// up (and its own startup bringup hook therefore never re-fires).
func TestUpRun_FirewallEnabled_BringsUpStack(t *testing.T) {
	tb := newTestBed(t)
	tb.Mock.EnsureRunningFunc = func(_ context.Context) error { return nil }
	withSettings(tb, "") // defaults — firewall enabled
	adminMock := withAdminMock(tb)
	adminMock.FirewallInitFunc = func(_ context.Context, _ *adminv1.FirewallInitRequest, _ ...grpc.CallOption) (*adminv1.FirewallInitResult, error) {
		return &adminv1.FirewallInitResult{EnvoyIp: "172.20.0.2"}, nil
	}

	require.NoError(t, upRun(context.Background(), upOptsFrom(tb)))
	assert.Len(t, tb.Mock.EnsureRunningCalls(), 1, "EnsureRunning must be invoked once")
	assert.Len(t, adminMock.FirewallInitCalls(), 1, "FirewallInit must be invoked once when firewall.enable is true")
	out := tb.Stdout.String()
	assert.Contains(t, out, "Control plane is up")
	assert.Contains(t, out, "Firewall stack up")
	assert.Contains(t, out, "Envoy:    172.20.0.2", "non-empty Envoy IP must print in the stack-up summary")
}

// TestUpRun_FirewallDisabled_SkipsStack pins the opt-out: with
// firewall.enable false in settings.yaml, `controlplane up` must not
// dial the AdminClient at all — bringing up a stack the user disabled
// would be a policy violation, not a convenience.
func TestUpRun_FirewallDisabled_SkipsStack(t *testing.T) {
	tb := newTestBed(t)
	tb.Mock.EnsureRunningFunc = func(_ context.Context) error { return nil }
	withSettings(tb, "firewall:\n  enable: false\n")
	withDockerFake(tb) // no stack containers — no warning expected
	dialed := false
	tb.F.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		dialed = true
		return nil, errors.New("must not dial")
	}

	require.NoError(t, upRun(context.Background(), upOptsFrom(tb)))
	assert.False(t, dialed, "AdminClient must not be dialed when firewall.enable is false")
	assert.Contains(t, tb.Stdout.String(), "Control plane is up")
	assert.NotContains(t, tb.Stdout.String(), "Firewall stack up")
	assert.NotContains(t, tb.Stderr.String(), "still running", "no warning when no stack containers exist")
}

// TestUpRun_FirewallDisabled_WarnsWhenStackStillRunning pins the
// settings/reality hint: firewall.enable false + a stack container
// still running must produce a stderr warning pointing at
// `clawker firewall down`, while the verb still succeeds. The check is
// a single label-filtered ContainerList (purpose=firewall), so any
// stack sibling matches; both names are exercised as fixture data.
func TestUpRun_FirewallDisabled_WarnsWhenStackStillRunning(t *testing.T) {
	for _, name := range []string{consts.ContainerEnvoy, consts.ContainerCoreDNS} {
		t.Run(name, func(t *testing.T) {
			tb := newTestBed(t)
			tb.Mock.EnsureRunningFunc = func(_ context.Context) error { return nil }
			withSettings(tb, "firewall:\n  enable: false\n")
			withDockerFake(tb, container.Summary{
				ID:     name + "-id",
				Names:  []string{"/" + name},
				State:  container.StateRunning,
				Labels: map[string]string{consts.LabelPurpose: consts.PurposeFirewall},
			})

			require.NoError(t, upRun(context.Background(), upOptsFrom(tb)))
			stderr := tb.Stderr.String()
			assert.Contains(t, stderr, "still running", "warning must flag the settings/reality gap")
			assert.Contains(t, stderr, "clawker firewall down", "warning must carry the remediation command")
		})
	}
}

// TestUpRun_FirewallInitError_WarnsAndFails pins the failure surface:
// a failed stack bringup must print the stack-down exposure warning and
// return an error — the CP being up is not a success when the user's
// settings say the firewall should be enforcing.
func TestUpRun_FirewallInitError_WarnsAndFails(t *testing.T) {
	tb := newTestBed(t)
	tb.Mock.EnsureRunningFunc = func(_ context.Context) error { return nil }
	withSettings(tb, "")
	adminMock := withAdminMock(tb)
	rpcErr := errors.New("envoy unhealthy")
	adminMock.FirewallInitFunc = func(_ context.Context, _ *adminv1.FirewallInitRequest, _ ...grpc.CallOption) (*adminv1.FirewallInitResult, error) {
		return nil, rpcErr
	}

	err := upRun(context.Background(), upOptsFrom(tb))
	require.Error(t, err)
	assert.ErrorIs(t, err, rpcErr)
	assert.Contains(t, tb.Stdout.String(), "Control plane is up")
	assert.Contains(t, tb.Stderr.String(), "FIREWALL STACK FAILED TO START")
}

func TestUpRun_WrapsEnsureRunningError(t *testing.T) {
	tb := newTestBed(t)
	bootErr := errors.New("healthz timed out")
	tb.Mock.EnsureRunningFunc = func(_ context.Context) error { return bootErr }
	// Config + AdminClient left nil — the CP bootstrap failure must
	// short-circuit before either is touched.

	err := upRun(context.Background(), upOptsFrom(tb))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bringing control plane up")
	assert.ErrorIs(t, err, bootErr)
	assert.Empty(t, tb.Stdout.String(), "success line must not print on failure")
}

func TestNewCmdUp_RunFReceivesOptions(t *testing.T) {
	tb := newTestBed(t)
	withSettings(tb, "")
	withAdminMock(tb)
	called := false
	cmd := NewCmdUp(tb.F, func(_ context.Context, opts *UpOptions) error {
		called = true
		require.NotNil(t, opts)
		assert.NotNil(t, opts.IOStreams)
		assert.NotNil(t, opts.Config)
		assert.NotNil(t, opts.ControlPlane)
		assert.NotNil(t, opts.AdminClient)
		return nil
	})
	cmd.SetArgs(nil)
	require.NoError(t, cmd.Execute())
	assert.True(t, called)
}

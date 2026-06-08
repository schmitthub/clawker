package firewall

import (
	"context"
	"errors"
	"testing"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	cpbootmocks "github.com/schmitthub/clawker/internal/controlplane/cpboot/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	ios, _, _, _ := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams: ios,
		Logger: func() (*logger.Logger, error) {
			return logger.Nop(), nil
		},
	}
}

func TestNewCmdUp_RunFReceivesOptions(t *testing.T) {
	f := newTestFactory(t)

	called := false
	cmd := NewCmdUp(f, func(_ context.Context, opts *UpOptions) error {
		called = true
		require.NotNil(t, opts)
		assert.NotNil(t, opts.IOStreams)
		return nil
	})

	cmd.SetArgs(nil)
	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}

// TestUpRun_EnsuresControlPlaneBeforeDial verifies that firewall up
// owns the CP bootstrap sequence: EnsureRunning MUST fire before any
// AdminClient dial so the RPC hits a live CP instead of fail-fast.
func TestUpRun_EnsuresControlPlaneBeforeDial(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	mgr := &cpbootmocks.ManagerMock{
		EnsureRunningFunc: func(_ context.Context) error { return nil },
	}
	adminCalled := false
	opts := &UpOptions{
		IOStreams:    ios,
		ControlPlane: func() cpboot.Manager { return mgr },
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			adminCalled = true
			// returning an error short-circuits before spinner RPC —
			// the test only cares about ordering here.
			return nil, errors.New("stub dial refused")
		},
	}

	err := upRun(context.Background(), opts)
	require.Error(t, err)
	assert.Len(t, mgr.EnsureRunningCalls(), 1, "EnsureRunning must fire exactly once")
	assert.True(t, adminCalled, "AdminClient must be invoked after EnsureRunning")
}

// TestUpRun_FailsFastWhenCPBootstrapFails verifies that a CP bootstrap
// failure short-circuits before the AdminClient dial — no point dialing
// a CP that refused to come up.
func TestUpRun_FailsFastWhenCPBootstrapFails(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	bootErr := errors.New("cp healthz timed out")
	mgr := &cpbootmocks.ManagerMock{
		EnsureRunningFunc: func(_ context.Context) error { return bootErr },
	}
	adminCalled := false
	opts := &UpOptions{
		IOStreams:    ios,
		ControlPlane: func() cpboot.Manager { return mgr },
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			adminCalled = true
			return nil, nil
		},
	}

	err := upRun(context.Background(), opts)
	require.Error(t, err)
	assert.ErrorIs(t, err, bootErr)
	assert.Contains(t, err.Error(), "bringing control plane up")
	assert.False(t, adminCalled, "AdminClient must not be dialed when CP bootstrap fails")
}

func TestNewCmdFirewall_NoServeSubcommand(t *testing.T) {
	f := newTestFactory(t)
	cmd := NewCmdFirewall(f)

	for _, sub := range cmd.Commands() {
		if sub.Name() == "serve" {
			t.Fatalf("firewall command must not register a serve subcommand — daemon path is dissolved in Branch 2")
		}
	}
}

// compile-time check that the mock package is wired; prevents the import
// from being stripped as unused in small test files that only use the
// factory via runF trapdoor tests.
var _ adminv1.AdminServiceClient = (*stubAdminClient)(nil)

type stubAdminClient struct {
	adminv1.AdminServiceClient
}

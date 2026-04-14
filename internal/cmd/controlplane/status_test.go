package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	cpmocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
)

// statusHarness bundles the testBed with an AdminServiceClientMock
// wired through f.AdminClient — separate from the ManagerMock on
// f.ControlPlane because status is the only command that uses both.
type statusHarness struct {
	tb        *testBed
	adminMock *cpmocks.AdminServiceClientMock
}

func newStatusHarness(t *testing.T) *statusHarness {
	t.Helper()
	tb := newTestBed(t)
	adminMock := &cpmocks.AdminServiceClientMock{}
	tb.F.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return adminMock, nil
	}
	return &statusHarness{tb: tb, adminMock: adminMock}
}

func (h *statusHarness) opts() *StatusOptions {
	return &StatusOptions{
		IOStreams:    h.tb.F.IOStreams,
		ControlPlane: h.tb.F.ControlPlane,
		AdminClient:  h.tb.F.AdminClient,
		Format:       &cmdutil.FormatFlags{},
	}
}

func TestStatusRun_StoppedCP_ReportsStoppedAndSkipsRPC(t *testing.T) {
	h := newStatusHarness(t)
	h.tb.Mock.IsRunningFunc = func(_ context.Context) (bool, error) { return false, nil }
	// Leave ProbeHealthz / FirewallStatus unprogrammed — invoking either
	// would panic and flag a regression in the short-circuit path.

	require.NoError(t, statusRun(context.Background(), h.opts()))
	assert.Contains(t, h.tb.Stdout.String(), "stopped")
	assert.Len(t, h.tb.Mock.ProbeHealthzCalls(), 0, "ProbeHealthz must not fire when container is down")
	assert.Len(t, h.adminMock.FirewallStatusCalls(), 0, "FirewallStatus must not fire when container is down")
}

func TestStatusRun_RunningCP_HealthzOKAndFirewallRPC(t *testing.T) {
	h := newStatusHarness(t)
	h.tb.Mock.IsRunningFunc = func(_ context.Context) (bool, error) { return true, nil }
	h.tb.Mock.ProbeHealthzFunc = func(_ context.Context) (int, error) { return http.StatusOK, nil }
	h.adminMock.FirewallStatusFunc = func(_ context.Context, _ *adminv1.FirewallStatusRequest, _ ...grpc.CallOption) (*adminv1.FirewallStatusResponse, error) {
		return &adminv1.FirewallStatusResponse{
			Running: true, EnvoyHealth: true, CorednsHealth: true, RuleCount: 42,
		}, nil
	}

	require.NoError(t, statusRun(context.Background(), h.opts()))
	out := h.tb.Stdout.String()
	assert.Contains(t, out, "running")
	assert.Contains(t, out, "42 active")
	require.Len(t, h.adminMock.FirewallStatusCalls(), 1)
}

// Table-driven "best-effort tolerance" for every failure site downstream
// of the IsRunning check: healthz transport error, AdminClient dial
// error, FirewallStatus RPC error. None fail the command; each surfaces
// on a dedicated row field.
func TestStatusRun_BestEffortTolerance(t *testing.T) {
	cases := []struct {
		name           string
		healthz        func(context.Context) (int, error)
		adminClient    func(context.Context) (adminv1.AdminServiceClient, error)
		firewallStatus func(context.Context, *adminv1.FirewallStatusRequest, ...grpc.CallOption) (*adminv1.FirewallStatusResponse, error)
		wantHealthzErr string
		wantFWErr      string
	}{
		{
			name:    "healthz transport error — firewall still queried",
			healthz: func(context.Context) (int, error) { return 0, errors.New("connection refused") },
			firewallStatus: func(_ context.Context, _ *adminv1.FirewallStatusRequest, _ ...grpc.CallOption) (*adminv1.FirewallStatusResponse, error) {
				return &adminv1.FirewallStatusResponse{}, nil
			},
			wantHealthzErr: "connection refused",
		},
		{
			name:    "AdminClient dial error",
			healthz: func(context.Context) (int, error) { return http.StatusOK, nil },
			adminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
				return nil, errors.New("admin dial: boom")
			},
			wantFWErr: "admin dial: boom",
		},
		{
			name:    "FirewallStatus RPC error",
			healthz: func(context.Context) (int, error) { return http.StatusOK, nil },
			firewallStatus: func(_ context.Context, _ *adminv1.FirewallStatusRequest, _ ...grpc.CallOption) (*adminv1.FirewallStatusResponse, error) {
				return nil, errors.New("admin service unavailable")
			},
			wantFWErr: "admin service unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newStatusHarness(t)
			h.tb.Mock.IsRunningFunc = func(_ context.Context) (bool, error) { return true, nil }
			h.tb.Mock.ProbeHealthzFunc = tc.healthz
			if tc.adminClient != nil {
				h.tb.F.AdminClient = tc.adminClient
			}
			if tc.firewallStatus != nil {
				h.adminMock.FirewallStatusFunc = tc.firewallStatus
			}

			// Route through the command with --json so row fields are
			// asserted directly — text-mode Contains would pass
			// spuriously if the error string happened to appear
			// elsewhere in the output.
			cmd := NewCmdStatus(h.tb.F, nil)
			cmd.SetArgs([]string{"--json"})
			require.NoError(t, cmd.Execute(),
				"all three tolerance paths must keep the command exit zero")

			var row statusRow
			require.NoError(t, json.Unmarshal(h.tb.Stdout.Bytes(), &row))
			assert.Equal(t, tc.wantHealthzErr, row.HealthzError)
			assert.Equal(t, tc.wantFWErr, row.FirewallError)
		})
	}
}

func TestStatusRun_JSON_StoppedCP(t *testing.T) {
	h := newStatusHarness(t)
	h.tb.Mock.IsRunningFunc = func(_ context.Context) (bool, error) { return false, nil }

	cmd := NewCmdStatus(h.tb.F, nil)
	cmd.SetArgs([]string{"--json"})
	require.NoError(t, cmd.Execute())

	var row statusRow
	require.NoError(t, json.Unmarshal(h.tb.Stdout.Bytes(), &row))
	assert.False(t, row.ContainerRunning)
	assert.False(t, row.HealthzOK)
	assert.Equal(t, 0, row.FirewallRuleCnt)
}

func TestStatusRun_IsRunningError(t *testing.T) {
	h := newStatusHarness(t)
	lookupErr := errors.New("docker: list failed")
	h.tb.Mock.IsRunningFunc = func(_ context.Context) (bool, error) { return false, lookupErr }

	err := statusRun(context.Background(), h.opts())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking control plane")
	assert.ErrorIs(t, err, lookupErr)
}

func TestNewCmdStatus_RunFReceivesOptions(t *testing.T) {
	tb := newTestBed(t)
	// status command uses f.AdminClient too; ensure the field passes
	// through to Options even though the default testBed leaves it nil.
	tb.F.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) { return nil, nil }

	called := false
	cmd := NewCmdStatus(tb.F, func(_ context.Context, opts *StatusOptions) error {
		called = true
		require.NotNil(t, opts)
		assert.NotNil(t, opts.IOStreams)
		assert.NotNil(t, opts.ControlPlane)
		assert.NotNil(t, opts.AdminClient)
		assert.NotNil(t, opts.Format)
		return nil
	})
	cmd.SetArgs(nil)
	require.NoError(t, cmd.Execute())
	assert.True(t, called)
}

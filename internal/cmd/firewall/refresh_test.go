package firewall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	adminv1mocks "github.com/schmitthub/clawker/api/admin/v1/mocks"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// twoRules is a representative project ruleset: one https add-domain-shaped
// allow and one ssh rule, so conversion + per-rule passthrough is exercised.
var twoRules = []config.EgressRule{
	{Dst: "registry.npmjs.org", Proto: "https", Port: "443", Action: "allow"},
	{Dst: "git.example.com", Proto: "ssh", Port: "22", Action: "allow"},
}

// harnessFloor is the selected harness's required egress floor, resolved the
// same way the command resolves it (registry default → embedded claude
// bundle; the config dir is isolated per test so no materialized bundle can
// interfere). Floor content correctness is guarded by the bundler egress
// tests — refresh tests only care that the floor is prepended.
func harnessFloor(t *testing.T) []config.EgressRule {
	t.Helper()
	blank := configmocks.NewBlankConfig()
	blank.ProjectEgressRulesFunc = func() []config.EgressRule { return nil }
	floor, err := bundler.EgressRules(blank, "")
	require.NoError(t, err)
	return floor
}

// refreshFactory wires a refresh-ready Factory: the given config with its
// ProjectEgressRules overridden to return the supplied rules, a project
// manager whose CurrentProject succeeds (or returns currentProjErr to
// simulate "no project"), and the captured streams. The config dir is
// isolated so harness floor resolution deterministically falls back to the
// embedded claude bundle.
func refreshFactory(t *testing.T, cfg *configmocks.ConfigMock, rules []config.EgressRule, currentProjErr error) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("CLAWKER_CONFIG_DIR", t.TempDir())
	f, out, errOut := testFactoryWithStreams(t)
	cfg.ProjectEgressRulesFunc = func() []config.EgressRule { return rules }
	f.Config = func() (config.Config, error) { return cfg, nil }
	f.ProjectManager = func() (project.ProjectManager, error) {
		return &projectmocks.ProjectManagerMock{
			CurrentProjectFunc: func(_ context.Context) (project.Project, error) {
				if currentProjErr != nil {
					return nil, currentProjErr
				}
				return &projectmocks.ProjectMock{}, nil
			},
		}, nil
	}
	return f, out, errOut
}

// TestRefreshCmd_SyncsProjectRules asserts refresh composes the harness
// egress floor with the project's rules and passes the full set to
// FirewallAddRules — the same sync the container-start path runs — and
// renders the per-status summary.
func TestRefreshCmd_SyncsProjectRules(t *testing.T) {
	f, out, _ := refreshFactory(t, configmocks.NewBlankConfig(), twoRules, nil)
	var got *adminv1.FirewallAddRulesRequest
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, req *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				got = req
				statuses := make([]adminv1.AddRuleStatus, len(req.GetRules()))
				for i := range statuses {
					statuses[i] = adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED
				}
				statuses[0] = adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED
				return &adminv1.FirewallAddRulesResult{
					Statuses:       statuses,
					StackRestarted: true,
				}, nil
			},
		}, nil
	}

	cmd := NewCmdRefresh(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	require.NotNil(t, got)
	floor := harnessFloor(t)
	want := adminv1.EgressRulesToProto(append(append([]config.EgressRule{}, floor...), twoRules...))
	require.Len(t, got.GetRules(), len(want))
	for i, w := range want {
		assert.Equal(t, w.GetDst(), got.GetRules()[i].GetDst())
		assert.Equal(t, w.GetProto(), got.GetRules()[i].GetProto())
		assert.Equal(t, w.GetPort(), got.GetRules()[i].GetPort())
		assert.Equal(t, w.GetAction(), got.GetRules()[i].GetAction())
	}
	assert.Contains(t, out.String(), fmt.Sprintf("1 added, 0 updated, %d unchanged", len(want)-1))
}

// TestRefreshCmd_FirewallDisabled_NoRPC asserts the settings gate fires before
// any project resolution or RPC when firewall.enable is false.
func TestRefreshCmd_FirewallDisabled_NoRPC(t *testing.T) {
	disabled := configmocks.NewFromString("", "firewall:\n  enable: false\n")
	f, _, _ := refreshFactory(t, disabled, twoRules, nil)
	called := false
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				called = true
				return &adminv1.FirewallAddRulesResult{}, nil
			},
		}, nil
	}

	cmd := NewCmdRefresh(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
	assert.False(t, called, "RPC must not fire when firewall is disabled")
}

// TestRefreshCmd_NoProject_Errors asserts a failed project resolution surfaces
// a clean error without firing the RPC.
func TestRefreshCmd_NoProject_Errors(t *testing.T) {
	f, _, _ := refreshFactory(t, configmocks.NewBlankConfig(), nil, errors.New("no project here"))
	called := false
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				called = true
				return &adminv1.FirewallAddRulesResult{}, nil
			},
		}, nil
	}

	cmd := NewCmdRefresh(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving current project")
	assert.False(t, called, "RPC must not fire when no project resolves")
}

// TestRefreshCmd_AllUnchanged_PrintsInSyncLine asserts the no-op path renders
// an "already in sync" line and skips the change summary.
func TestRefreshCmd_AllUnchanged_PrintsInSyncLine(t *testing.T) {
	f, out, errOut := refreshFactory(t, configmocks.NewBlankConfig(), twoRules, nil)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, req *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				statuses := make([]adminv1.AddRuleStatus, len(req.GetRules()))
				for i := range statuses {
					statuses[i] = adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED
				}
				return &adminv1.FirewallAddRulesResult{Statuses: statuses, StackRestarted: false}, nil
			},
		}, nil
	}

	cmd := NewCmdRefresh(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, out.String(), "already in sync")
	assert.NotContains(t, out.String(), "Refreshed firewall rules")
	assert.Empty(t, errOut.String(), "no stack-restart note on a pure no-op")
}

// TestRefreshCmd_StackNotRestarted_PrintsNote asserts that when the store
// changed but the stack was not running, the operator is told the change
// applies on next `firewall up`.
func TestRefreshCmd_StackNotRestarted_PrintsNote(t *testing.T) {
	oneRule := twoRules[:1]
	f, out, errOut := refreshFactory(t, configmocks.NewBlankConfig(), oneRule, nil)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, req *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				statuses := make([]adminv1.AddRuleStatus, len(req.GetRules()))
				for i := range statuses {
					statuses[i] = adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED
				}
				return &adminv1.FirewallAddRulesResult{
					Statuses:       statuses,
					StackRestarted: false,
				}, nil
			},
		}, nil
	}

	cmd := NewCmdRefresh(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, out.String(), "Refreshed firewall rules")
	assert.Contains(t, errOut.String(), "next `clawker firewall up`")
}

// TestRefreshCmd_StatusLengthMismatch_Errors guards the wire contract: one
// status per submitted rule. A server bug returning the wrong count surfaces
// as an error, never a wrong summary.
func TestRefreshCmd_StatusLengthMismatch_Errors(t *testing.T) {
	f, _, _ := refreshFactory(t, configmocks.NewBlankConfig(), twoRules, nil)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallAddRulesFunc: func(_ context.Context, _ *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
				return &adminv1.FirewallAddRulesResult{
					Statuses: []adminv1.AddRuleStatus{adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED},
				}, nil
			},
		}, nil
	}

	cmd := NewCmdRefresh(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "statuses for")
}

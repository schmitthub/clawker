package refresh //nolint:testpackage // exercises the unexported run function directly

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	adminv1mocks "github.com/schmitthub/clawker/api/admin/v1/mocks"
	"github.com/schmitthub/clawker/internal/bundler"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
)

// twoRules is a representative project ruleset: one https add-domain-shaped
// allow and one ssh rule, so conversion + per-rule passthrough is exercised.
func twoRules() []config.EgressRule {
	return []config.EgressRule{
		{
			Dst:                   "registry.npmjs.org",
			Proto:                 "https",
			Port:                  "443",
			Action:                "allow",
			PathRules:             nil,
			PathDefault:           "",
			InsecureSkipTLSVerify: false,
		},
		{
			Dst:                   "git.example.com",
			Proto:                 "ssh",
			Port:                  "22",
			Action:                "allow",
			PathRules:             nil,
			PathDefault:           "",
			InsecureSkipTLSVerify: false,
		},
	}
}

// harnessFloor is the selected harness's required egress floor, resolved the
// same way the command resolves it (configured default → embedded claude
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
func refreshFactory(
	t *testing.T,
	cfg *configmocks.ConfigMock,
	rules []config.EgressRule,
	currentProjErr error,
) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("CLAWKER_CONFIG_DIR", t.TempDir())
	ios, _, out, errOut := iostreams.Test()
	//nolint:exhaustruct // command tests wire only the Factory nouns refresh reads
	f := &cmdutil.Factory{IOStreams: ios}
	cfg.ProjectEgressRulesFunc = func() []config.EgressRule { return rules }
	f.Config = func() (config.Config, error) { return cfg, nil }
	f.ProjectManager = func() (project.ProjectManager, error) {
		//nolint:exhaustruct // mock wires only the project lookup refresh gates on
		return &projectmocks.ProjectManagerMock{
			CurrentProjectFunc: func(_ context.Context) (project.Project, error) {
				if currentProjErr != nil {
					return nil, currentProjErr
				}
				//nolint:exhaustruct // the gate only cares that a project resolves
				return &projectmocks.ProjectMock{}, nil
			},
		}, nil
	}
	return f, out, errOut
}

// refreshOpts extracts the run-function options from a wired Factory, mirroring
// what NewCmdRefresh does so the table can drive refreshRun directly.
func refreshOpts(f *cmdutil.Factory) *RefreshOptions {
	return &RefreshOptions{
		IOStreams:      f.IOStreams,
		Config:         f.Config,
		ProjectManager: f.ProjectManager,
		AdminClient:    f.AdminClient,
	}
}

// addRulesClient is a mock AdminService client for the single RPC refresh
// drives. respond turns the submitted request into the server's reply; nil
// means "every rule unchanged".
func addRulesClient(
	respond func(req *adminv1.FirewallAddRulesRequest) *adminv1.FirewallAddRulesResult,
) *adminv1mocks.AdminServiceClientMock {
	if respond == nil {
		respond = func(req *adminv1.FirewallAddRulesRequest) *adminv1.FirewallAddRulesResult {
			return &adminv1.FirewallAddRulesResult{
				Statuses:       statuses(len(req.GetRules()), adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED),
				StackRestarted: false,
			}
		}
	}
	//nolint:exhaustruct // mock wires only the RPCs this command drives
	return &adminv1mocks.AdminServiceClientMock{
		FirewallAddRulesFunc: func(_ context.Context, req *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
			return respond(req), nil
		},
	}
}

// statuses builds an n-long status slice with every entry set to s.
func statuses(n int, s adminv1.AddRuleStatus) []adminv1.AddRuleStatus {
	out := make([]adminv1.AddRuleStatus, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// TestNewCmdRefresh asserts the command is constructed with the Factory nouns
// refreshRun needs, rejects positional args, and dispatches to the injected
// runF.
func TestNewCmdRefresh(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErr    bool
		wantCalled bool
	}{
		{name: "no args dispatches to runF", args: []string{}, wantErr: false, wantCalled: true},
		{name: "positional arg rejected", args: []string{"example.com"}, wantErr: true, wantCalled: false},
		{name: "unknown flag rejected", args: []string{"--nope"}, wantErr: true, wantCalled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _, _ := refreshFactory(t, configmocks.NewBlankConfig(), twoRules(), nil)
			f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
				return addRulesClient(nil), nil
			}

			var gotOpts *RefreshOptions
			cmd := NewCmdRefresh(f, func(_ context.Context, opts *RefreshOptions) error {
				gotOpts = opts
				return nil
			})
			cmd.SetContext(context.Background())
			cmd.SetArgs(tt.args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			err := cmd.Execute()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if !tt.wantCalled {
				assert.Nil(t, gotOpts, "runF must not run when argument validation fails")
				return
			}
			require.NotNil(t, gotOpts)
			assert.Same(t, f.IOStreams, gotOpts.IOStreams)
			assert.NotNil(t, gotOpts.Config)
			assert.NotNil(t, gotOpts.ProjectManager)
			assert.NotNil(t, gotOpts.AdminClient)
		})
	}
}

// TestRefreshRun covers the sync contract (harness floor + project rules
// pushed through FirewallAddRules), both pre-RPC gates, the no-op summary, the
// stack-down note, and the status-count wire guard.
func TestRefreshRun(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *configmocks.ConfigMock
		rules     []config.EgressRule
		projErr   error
		respond   func(req *adminv1.FirewallAddRulesRequest) *adminv1.FirewallAddRulesResult
		wantErr   string
		wantNoRPC bool
		check     func(t *testing.T, out, errOut *bytes.Buffer, client *adminv1mocks.AdminServiceClientMock)
	}{
		{
			name:    "syncs harness floor plus project rules",
			cfg:     configmocks.NewBlankConfig(),
			rules:   twoRules(),
			projErr: nil,
			respond: func(req *adminv1.FirewallAddRulesRequest) *adminv1.FirewallAddRulesResult {
				st := statuses(len(req.GetRules()), adminv1.AddRuleStatus_ADD_RULE_STATUS_UNCHANGED)
				st[0] = adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED
				return &adminv1.FirewallAddRulesResult{Statuses: st, StackRestarted: true}
			},
			wantErr:   "",
			wantNoRPC: false,
			check:     assertFloorAndProjectRulesSynced,
		},
		{
			name:      "firewall disabled gates before any RPC",
			cfg:       configmocks.NewFromString("", "firewall:\n  enable: false\n"),
			rules:     twoRules(),
			projErr:   nil,
			respond:   nil,
			wantErr:   "disabled",
			wantNoRPC: true,
			check:     nil,
		},
		{
			name:      "unresolvable project gates before any RPC",
			cfg:       configmocks.NewBlankConfig(),
			rules:     nil,
			projErr:   errors.New("no project here"),
			respond:   nil,
			wantErr:   "resolving current project",
			wantNoRPC: true,
			check:     nil,
		},
		{
			name:      "all unchanged prints the in-sync line",
			cfg:       configmocks.NewBlankConfig(),
			rules:     twoRules(),
			projErr:   nil,
			respond:   nil,
			wantErr:   "",
			wantNoRPC: false,
			check: func(t *testing.T, out, errOut *bytes.Buffer, _ *adminv1mocks.AdminServiceClientMock) {
				t.Helper()
				assert.Contains(t, out.String(), "already in sync")
				assert.NotContains(t, out.String(), "Refreshed firewall rules")
				assert.Empty(t, errOut.String(), "no stack-restart note on a pure no-op")
			},
		},
		{
			name:    "stack not restarted prints the deferred-effect note",
			cfg:     configmocks.NewBlankConfig(),
			rules:   twoRules()[:1],
			projErr: nil,
			respond: func(req *adminv1.FirewallAddRulesRequest) *adminv1.FirewallAddRulesResult {
				return &adminv1.FirewallAddRulesResult{
					Statuses:       statuses(len(req.GetRules()), adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED),
					StackRestarted: false,
				}
			},
			wantErr:   "",
			wantNoRPC: false,
			check: func(t *testing.T, out, errOut *bytes.Buffer, _ *adminv1mocks.AdminServiceClientMock) {
				t.Helper()
				assert.Contains(t, out.String(), "Refreshed firewall rules")
				assert.Contains(t, errOut.String(), "next `clawker firewall up`")
			},
		},
		{
			name:    "status count mismatch is a wire error",
			cfg:     configmocks.NewBlankConfig(),
			rules:   twoRules(),
			projErr: nil,
			respond: func(_ *adminv1.FirewallAddRulesRequest) *adminv1.FirewallAddRulesResult {
				return &adminv1.FirewallAddRulesResult{
					Statuses:       statuses(1, adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED),
					StackRestarted: false,
				}
			},
			wantErr:   "statuses for",
			wantNoRPC: false,
			check:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, out, errOut := refreshFactory(t, tt.cfg, tt.rules, tt.projErr)
			client := addRulesClient(tt.respond)
			f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) { return client, nil }

			err := refreshRun(context.Background(), refreshOpts(f))

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tt.wantNoRPC {
				assert.Empty(t, client.FirewallAddRulesCalls(), "RPC must not fire behind a failed gate")
			}
			if tt.check != nil {
				tt.check(t, out, errOut, client)
			}
		})
	}
}

// assertFloorAndProjectRulesSynced checks that the request carried the harness
// egress floor followed by the project's own rules — the same set the
// container-start sync pushes — and that the per-status summary was rendered.
func assertFloorAndProjectRulesSynced(
	t *testing.T,
	out, _ *bytes.Buffer,
	client *adminv1mocks.AdminServiceClientMock,
) {
	t.Helper()
	require.Len(t, client.FirewallAddRulesCalls(), 1)
	got := client.FirewallAddRulesCalls()[0].In

	want := adminv1.EgressRulesToProto(append(append([]config.EgressRule{}, harnessFloor(t)...), twoRules()...))
	require.Len(t, got.GetRules(), len(want))
	for i, w := range want {
		assert.Equal(t, w.GetDst(), got.GetRules()[i].GetDst())
		assert.Equal(t, w.GetProto(), got.GetRules()[i].GetProto())
		assert.Equal(t, w.GetPort(), got.GetRules()[i].GetPort())
		assert.Equal(t, w.GetAction(), got.GetRules()[i].GetAction())
	}
	assert.Contains(t, out.String(), fmt.Sprintf("1 added, 0 updated, %d unchanged", len(want)-1))
}

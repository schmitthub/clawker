package prune //nolint:testpackage // exercises the unexported run function directly

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/shlex"
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
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/schmitthub/clawker/internal/prompter"
)

// testFactoryWithStreams returns a Factory + the captured stdout/stderr
// buffers so prune-command tests can assert on emitted text.
func testFactoryWithStreams(t *testing.T) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, out, errOut := iostreams.Test()
	//nolint:exhaustruct // prune reads only these Factory fields; the rest stay nil so an unexpected dependency panics loudly
	f := &cmdutil.Factory{
		IOStreams: ios,
		Logger: func() (*logger.Logger, error) {
			return logger.Nop(), nil
		},
	}
	return f, out, errOut
}

// twoRules is a representative project ruleset: one https add-domain-shaped
// allow and one ssh rule, so conversion + per-rule passthrough is exercised.
func twoRules() []config.EgressRule {
	//nolint:exhaustruct // the path-scoping fields are irrelevant here; prune only asserts per-rule passthrough
	return []config.EgressRule{
		{Dst: "registry.npmjs.org", Proto: "https", Port: "443", Action: "allow"},
		{Dst: "git.example.com", Proto: "ssh", Port: "22", Action: "allow"},
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
	f, out, errOut := testFactoryWithStreams(t)
	cfg.ProjectEgressRulesFunc = func() []config.EgressRule { return rules }
	f.Config = func() (config.Config, error) { return cfg, nil }
	f.ProjectManager = func() (project.ProjectManager, error) {
		//nolint:exhaustruct // prune resolves only CurrentProject; any other registry call panics loudly
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

// wirePrompter gives the factory a real Prompter over the test streams —
// non-interactive by construction, so Confirm resolves to its default.
func wirePrompter(f *cmdutil.Factory) {
	f.Prompter = func() *prompter.Prompter { return prompter.NewPrompter(f.IOStreams) }
}

// pruneClient is a mock AdminService client for the two RPCs prune composes:
// the wipe (FirewallRemoveRule all=true) and the re-sync (FirewallAddRules).
// removeStatus drives the wipe outcome; requests are read back via moq's
// recorded Calls accessors.
func pruneClient(removeStatus adminv1.RemoveRuleStatus) *adminv1mocks.AdminServiceClientMock {
	//nolint:exhaustruct // mock wires only the RPCs prune drives; any other call panics loudly
	return &adminv1mocks.AdminServiceClientMock{
		FirewallRemoveRuleFunc: func(_ context.Context, _ *adminv1.FirewallRemoveRuleRequest, _ ...grpc.CallOption) (*adminv1.FirewallRemoveRuleResult, error) {
			return &adminv1.FirewallRemoveRuleResult{Status: removeStatus, StackRestarted: true}, nil
		},
		FirewallAddRulesFunc: func(_ context.Context, req *adminv1.FirewallAddRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallAddRulesResult, error) {
			statuses := make([]adminv1.AddRuleStatus, len(req.GetRules()))
			for i := range statuses {
				statuses[i] = adminv1.AddRuleStatus_ADD_RULE_STATUS_ADDED
			}
			return &adminv1.FirewallAddRulesResult{Statuses: statuses, StackRestarted: true}, nil
		},
	}
}

// TestNewCmdPrune asserts the constructor's flag wiring: --all/-a and --yes/-y
// land on the options struct the run function receives.
func TestNewCmdPrune(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantAll bool
		wantYes bool
	}{
		{name: "no flags", input: "", wantAll: false, wantYes: false},
		{name: "all flag long", input: "--all", wantAll: true, wantYes: false},
		{name: "all flag short", input: "-a", wantAll: true, wantYes: false},
		{name: "yes flag long", input: "--yes", wantAll: false, wantYes: true},
		{name: "yes flag short", input: "-y", wantAll: false, wantYes: true},
		{name: "all and yes", input: "--all --yes", wantAll: true, wantYes: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _, _ := testFactoryWithStreams(t)

			var gotOpts *PruneOptions
			cmd := NewCmdPrune(f, func(_ context.Context, opts *PruneOptions) error {
				gotOpts = opts
				return nil
			})

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			require.NoError(t, cmd.Execute())
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantAll, gotOpts.All)
			require.Equal(t, tt.wantYes, gotOpts.Yes)
		})
	}
}

// TestPruneCmd_Default_WipesThenResyncs asserts the two-step contract: one
// all=true wipe, then the same floor+project sync refresh pushes.
func TestPruneCmd_Default_WipesThenResyncs(t *testing.T) {
	f, out, _ := refreshFactory(t, configmocks.NewBlankConfig(), twoRules(), nil)
	client := pruneClient(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) { return client, nil }

	cmd := NewCmdPrune(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--yes"})
	require.NoError(t, cmd.Execute())

	require.Len(t, client.FirewallRemoveRuleCalls(), 1, "one wipe RPC, never a per-rule loop")
	gotRemove := client.FirewallRemoveRuleCalls()[0].In
	assert.True(t, gotRemove.GetAll(), "wipe must be the all=true form")
	assert.Empty(t, gotRemove.GetDst(), "all form carries no rule selector")

	floor := harnessFloor(t)
	require.Len(t, client.FirewallAddRulesCalls(), 1)
	gotAdd := client.FirewallAddRulesCalls()[0].In
	require.Len(t, gotAdd.GetRules(), len(floor)+len(twoRules()), "re-sync pushes floor + project rules")
	assert.Contains(t, out.String(), "Removed all firewall rules")
	assert.Contains(t, out.String(), "Re-synced")
}

// TestPruneCmd_All_SkipsResyncAndProjectGates asserts --all works anywhere:
// no config gate, no project resolution, no re-sync RPC.
func TestPruneCmd_All_SkipsResyncAndProjectGates(t *testing.T) {
	f, out, _ := testFactoryWithStreams(t)
	f.ProjectManager = func() (project.ProjectManager, error) {
		return nil, errors.New("no project here")
	}
	client := pruneClient(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) { return client, nil }

	cmd := NewCmdPrune(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--all", "--yes"})
	require.NoError(t, cmd.Execute())

	require.Len(t, client.FirewallRemoveRuleCalls(), 1)
	assert.True(t, client.FirewallRemoveRuleCalls()[0].In.GetAll())
	assert.Empty(t, client.FirewallAddRulesCalls(), "--all must not re-sync")
	assert.Contains(t, out.String(), "Removed all firewall rules")
}

// TestPruneCmd_NoConfirmation_NoRPC asserts the destructive path never fires
// without approval: in a non-interactive session without --yes the prompt
// resolves to its default (no) and the command aborts before any RPC.
func TestPruneCmd_NoConfirmation_NoRPC(t *testing.T) {
	f, _, errOut := refreshFactory(t, configmocks.NewBlankConfig(), twoRules(), nil)
	wirePrompter(f)
	client := pruneClient(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) { return client, nil }

	cmd := NewCmdPrune(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, errOut.String(), "Aborted")
	assert.Empty(t, client.FirewallRemoveRuleCalls(), "no wipe without confirmation")
	assert.Empty(t, client.FirewallAddRulesCalls())
}

// TestPruneCmd_Default_FirewallDisabled_NoRPC mirrors refresh: the settings
// gate fires before any RPC when firewall.enable is false.
func TestPruneCmd_Default_FirewallDisabled_NoRPC(t *testing.T) {
	disabled := configmocks.NewFromString("", "firewall:\n  enable: false\n")
	f, _, _ := refreshFactory(t, disabled, twoRules(), nil)
	client := pruneClient(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) { return client, nil }

	cmd := NewCmdPrune(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--yes"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
	assert.Empty(t, client.FirewallRemoveRuleCalls(), "gate must fire before the wipe")
}

// TestPruneCmd_EmptyStore_StillResyncs asserts a NOT_FOUND wipe (already
// empty store) is not an error and the config re-sync still runs — the
// end state "store == config set" is the command's contract.
func TestPruneCmd_EmptyStore_StillResyncs(t *testing.T) {
	f, out, _ := refreshFactory(t, configmocks.NewBlankConfig(), twoRules(), nil)
	client := pruneClient(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_NOT_FOUND)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) { return client, nil }

	cmd := NewCmdPrune(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--yes"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, out.String(), "No firewall rules to remove")
	require.Len(t, client.FirewallAddRulesCalls(), 1, "re-sync still runs on an empty store")
	assert.NotEmpty(t, client.FirewallAddRulesCalls()[0].In.GetRules())
}

// TestPruneCmd_ComposeFailure_BeforeWipe pins the ordering invariant: a
// config composition failure (no resolvable project) must surface BEFORE the
// wipe RPC, never after the store is already emptied.
func TestPruneCmd_ComposeFailure_BeforeWipe(t *testing.T) {
	f, _, _ := refreshFactory(t, configmocks.NewBlankConfig(), nil, errors.New("no project here"))
	client := pruneClient(adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) { return client, nil }

	cmd := NewCmdPrune(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--yes"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving current project")
	assert.Empty(t, client.FirewallRemoveRuleCalls(), "wipe must not fire when the keep set cannot be composed")
}

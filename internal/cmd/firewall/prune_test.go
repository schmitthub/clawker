package firewall //nolint:testpackage // shares in-package test helpers (refreshFactory, twoRules, harnessFloor) with refresh_test.go

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	adminv1mocks "github.com/schmitthub/clawker/api/admin/v1/mocks"
	"github.com/schmitthub/clawker/internal/cmdutil"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
)

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

// TestPruneCmd_Default_WipesThenResyncs asserts the two-step contract: one
// all=true wipe, then the same floor+project sync refresh pushes.
func TestPruneCmd_Default_WipesThenResyncs(t *testing.T) {
	f, out, _ := refreshFactory(t, configmocks.NewBlankConfig(), twoRules, nil)
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
	require.Len(t, gotAdd.GetRules(), len(floor)+len(twoRules), "re-sync pushes floor + project rules")
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
	f, _, errOut := refreshFactory(t, configmocks.NewBlankConfig(), twoRules, nil)
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
	f, _, _ := refreshFactory(t, disabled, twoRules, nil)
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
	f, out, _ := refreshFactory(t, configmocks.NewBlankConfig(), twoRules, nil)
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

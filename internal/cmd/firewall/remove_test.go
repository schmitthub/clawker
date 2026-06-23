package firewall

import (
	"context"
	"fmt"
	"testing"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	adminv1mocks "github.com/schmitthub/clawker/api/admin/v1/mocks"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// newRemoveCmd creates a remove command wired with a background context for
// completion testing. Cobra's cmd.Context() returns nil on unexecuted commands.
func newRemoveCmd(t *testing.T) *cobra.Command {
	t.Helper()
	f := newTestFactory(t)
	f.AdminClient = mockAdminFunc([]*adminv1.EgressRule{
		{Dst: "zebra.example.com", Proto: "https"},
		{Dst: "alpha.example.com", Proto: "https"},
		{Dst: "middle.example.com", Proto: "https"},
	}, nil)
	cmd := NewCmdRemove(f, nil)
	cmd.SetContext(context.Background())
	return cmd
}

func TestRemoveCompletion_ReturnsSortedDomains(t *testing.T) {
	cmd := newRemoveCmd(t)
	completions, directive := cmd.ValidArgsFunction(cmd, nil, "")

	require.Len(t, completions, 3)
	assert.Equal(t, "alpha.example.com", string(completions[0]))
	assert.Equal(t, "middle.example.com", string(completions[1]))
	assert.Equal(t, "zebra.example.com", string(completions[2]))
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestRemoveCompletion_AlreadyHasArg(t *testing.T) {
	cmd := newRemoveCmd(t)
	completions, directive := cmd.ValidArgsFunction(cmd, []string{"already-set"}, "")

	assert.Empty(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestRemoveCompletion_ListError(t *testing.T) {
	f := newTestFactory(t)
	f.AdminClient = mockAdminFunc(nil, fmt.Errorf("corrupt store"))
	cmd := NewCmdRemove(f, nil)
	cmd.SetContext(context.Background())

	completions, directive := cmd.ValidArgsFunction(cmd, nil, "")

	assert.Empty(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestRemoveCompletion_ClientInitError(t *testing.T) {
	f := newTestFactory(t)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return nil, fmt.Errorf("CP unreachable")
	}

	cmd := NewCmdRemove(f, nil)
	cmd.SetContext(context.Background())

	completions, directive := cmd.ValidArgsFunction(cmd, nil, "")

	assert.Empty(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestRemoveCompletion_EmptyRules(t *testing.T) {
	f := newTestFactory(t)
	f.AdminClient = mockAdminFunc(nil, nil)
	cmd := NewCmdRemove(f, nil)
	cmd.SetContext(context.Background())

	completions, directive := cmd.ValidArgsFunction(cmd, nil, "")

	assert.Empty(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestRemoveCompletion_DeduplicatesDomains(t *testing.T) {
	f := newTestFactory(t)
	f.AdminClient = mockAdminFunc([]*adminv1.EgressRule{
		{Dst: "example.com", Proto: "https"},
		{Dst: "example.com", Proto: "ssh", Port: "22"},
		{Dst: "other.com", Proto: "https"},
	}, nil)

	cmd := NewCmdRemove(f, nil)
	cmd.SetContext(context.Background())
	completions, _ := cmd.ValidArgsFunction(cmd, nil, "")

	require.Len(t, completions, 2)
	assert.Equal(t, "example.com", string(completions[0]))
	assert.Equal(t, "other.com", string(completions[1]))
}

// mockAdminFunc returns a Factory-compatible AdminClient closure backed by an
// AdminServiceClientMock whose FirewallListRules returns the supplied rules.
func mockAdminFunc(rules []*adminv1.EgressRule, listErr error) func(context.Context) (adminv1.AdminServiceClient, error) {
	return func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallListRulesFunc: func(_ context.Context, _ *adminv1.FirewallListRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallListRulesResult, error) {
				if listErr != nil {
					return nil, listErr
				}
				return &adminv1.FirewallListRulesResult{Rules: rules}, nil
			},
		}, nil
	}
}

// TestRemoveRun_Success verifies the run path forwards dst/proto/port
// to FirewallRemoveRule and renders the success line on a matching
// rule.
func TestRemoveRun_Success(t *testing.T) {
	f := newTestFactory(t)
	var got *adminv1.FirewallRemoveRuleRequest
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallRemoveRuleFunc: func(_ context.Context, req *adminv1.FirewallRemoveRuleRequest, _ ...grpc.CallOption) (*adminv1.FirewallRemoveRuleResult, error) {
				got = req
				return &adminv1.FirewallRemoveRuleResult{StackRestarted: true, Status: adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED}, nil
			},
		}, nil
	}
	cmd := NewCmdRemove(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"example.com", "--proto", "tls", "--port", "443"})
	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, got, "FirewallRemoveRule must be called")
	assert.Equal(t, "example.com", got.GetDst())
	assert.Equal(t, "tls", got.GetProto())
	assert.Equal(t, "443", got.GetPort())
}

// TestRemoveRun_NotFound is the whole reason this RPC shrunk: a missing
// rule must surface as a CLI error, never a silent success. NOT_FOUND
// now travels as a response Status (replaces the old gRPC codes.NotFound
// wire surface); the CLI must still exit non-zero with a hint that
// names dst:proto:port.
func TestRemoveRun_NotFound(t *testing.T) {
	f := newTestFactory(t)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallRemoveRuleFunc: func(_ context.Context, _ *adminv1.FirewallRemoveRuleRequest, _ ...grpc.CallOption) (*adminv1.FirewallRemoveRuleResult, error) {
				return &adminv1.FirewallRemoveRuleResult{
					Status: adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_NOT_FOUND,
				}, nil
			},
		}, nil
	}
	cmd := NewCmdRemove(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"exmaple.com"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rule not found")
	assert.Contains(t, err.Error(), "exmaple.com")
}

// TestRemoveRun_NotFound_WithPath is the path-scoped sibling: a path
// miss must qualify the surfaced error with the path so a typo never
// silently succeeds.
func TestRemoveRun_NotFound_WithPath(t *testing.T) {
	f := newTestFactory(t)
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallRemoveRuleFunc: func(_ context.Context, _ *adminv1.FirewallRemoveRuleRequest, _ ...grpc.CallOption) (*adminv1.FirewallRemoveRuleResult, error) {
				return &adminv1.FirewallRemoveRuleResult{
					Status: adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_NOT_FOUND,
				}, nil
			},
		}, nil
	}
	cmd := NewCmdRemove(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com", "--path", "/unknown"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/unknown")
	assert.Contains(t, err.Error(), "rule not found")
}

// TestRemoveRun_WithPath_BuildsRequest asserts --path is forwarded on
// the wire so the handler can take the path-scoped branch. The success
// status PATH_REMOVED maps to the "Removed path rule" output line.
func TestRemoveRun_WithPath_BuildsRequest(t *testing.T) {
	f, out, _ := testFactoryWithStreams(t)
	var got *adminv1.FirewallRemoveRuleRequest
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallRemoveRuleFunc: func(_ context.Context, req *adminv1.FirewallRemoveRuleRequest, _ ...grpc.CallOption) (*adminv1.FirewallRemoveRuleResult, error) {
				got = req
				return &adminv1.FirewallRemoveRuleResult{StackRestarted: true, Status: adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_PATH_REMOVED}, nil
			},
		}, nil
	}
	cmd := NewCmdRemove(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com", "--path", "/v1"})
	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "api.example.com", got.GetDst())
	assert.Equal(t, "/v1", got.GetPath())
	assert.Contains(t, out.String(), "Removed path rule")
}

// TestRemoveRun_NoPath_RequestPathEmpty pins the wire-shape for the
// whole-rule removal path: GetPath() must come back empty so the handler
// stays on the existing branch.
func TestRemoveRun_NoPath_RequestPathEmpty(t *testing.T) {
	f := newTestFactory(t)
	var got *adminv1.FirewallRemoveRuleRequest
	f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
		return &adminv1mocks.AdminServiceClientMock{
			FirewallRemoveRuleFunc: func(_ context.Context, req *adminv1.FirewallRemoveRuleRequest, _ ...grpc.CallOption) (*adminv1.FirewallRemoveRuleResult, error) {
				got = req
				return &adminv1.FirewallRemoveRuleResult{StackRestarted: true, Status: adminv1.RemoveRuleStatus_REMOVE_RULE_STATUS_REMOVED}, nil
			},
		}, nil
	}
	cmd := NewCmdRemove(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"api.example.com"})
	err := cmd.Execute()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got.GetPath())
}

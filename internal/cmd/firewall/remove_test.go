package firewall

import (
	"context"
	"fmt"
	"testing"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	cpmocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
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
		{Dst: "zebra.example.com", Proto: "tls"},
		{Dst: "alpha.example.com", Proto: "tls"},
		{Dst: "middle.example.com", Proto: "tls"},
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
		{Dst: "example.com", Proto: "tls"},
		{Dst: "example.com", Proto: "ssh", Port: 22},
		{Dst: "other.com", Proto: "tls"},
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
		return &cpmocks.AdminServiceClientMock{
			FirewallListRulesFunc: func(_ context.Context, _ *adminv1.FirewallListRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallListRulesResult, error) {
				if listErr != nil {
					return nil, listErr
				}
				return &adminv1.FirewallListRulesResult{Rules: rules}, nil
			},
		}, nil
	}
}

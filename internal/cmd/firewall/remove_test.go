package firewall

import (
	"context"
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/firewall"
	fwmocks "github.com/schmitthub/clawker/internal/firewall/mocks"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRemoveCmd creates a remove command wired with a background context for
// completion testing. Cobra's cmd.Context() returns nil on unexecuted commands.
func newRemoveCmd(t *testing.T, f *cmdutil.Factory) *cobra.Command {
	t.Helper()
	cmd := NewCmdRemove(f, nil)
	cmd.SetContext(context.Background())
	return cmd
}

func TestRemoveCompletion_ReturnsSortedDomains(t *testing.T) {
	f := newTestFactory(t)
	f.Firewall = mockFirewallFunc([]config.EgressRule{
		{Dst: "zebra.example.com", Proto: "tls"},
		{Dst: "alpha.example.com", Proto: "tls"},
		{Dst: "middle.example.com", Proto: "tls"},
	}, nil)

	cmd := newRemoveCmd(t, f)
	completions, directive := cmd.ValidArgsFunction(cmd, nil, "")

	require.Len(t, completions, 3)
	assert.Equal(t, "alpha.example.com", string(completions[0]))
	assert.Equal(t, "middle.example.com", string(completions[1]))
	assert.Equal(t, "zebra.example.com", string(completions[2]))
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestRemoveCompletion_AlreadyHasArg(t *testing.T) {
	f := newTestFactory(t)
	f.Firewall = mockFirewallFunc([]config.EgressRule{
		{Dst: "example.com"},
	}, nil)

	cmd := newRemoveCmd(t, f)
	completions, directive := cmd.ValidArgsFunction(cmd, []string{"already-set"}, "")

	assert.Empty(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestRemoveCompletion_ListError(t *testing.T) {
	f := newTestFactory(t)
	f.Firewall = mockFirewallFunc(nil, fmt.Errorf("corrupt store"))

	cmd := newRemoveCmd(t, f)
	completions, directive := cmd.ValidArgsFunction(cmd, nil, "")

	assert.Empty(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestRemoveCompletion_FirewallInitError(t *testing.T) {
	f := newTestFactory(t)
	f.Firewall = func(_ context.Context) (firewall.FirewallManager, error) {
		return nil, fmt.Errorf("docker not available")
	}

	cmd := newRemoveCmd(t, f)
	completions, directive := cmd.ValidArgsFunction(cmd, nil, "")

	assert.Empty(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestRemoveCompletion_EmptyRules(t *testing.T) {
	f := newTestFactory(t)
	f.Firewall = mockFirewallFunc(nil, nil)

	cmd := newRemoveCmd(t, f)
	completions, directive := cmd.ValidArgsFunction(cmd, nil, "")

	assert.Empty(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestRemoveCompletion_DeduplicatesDomains(t *testing.T) {
	f := newTestFactory(t)
	f.Firewall = mockFirewallFunc([]config.EgressRule{
		{Dst: "example.com", Proto: "tls"},
		{Dst: "example.com", Proto: "ssh", Port: 22},
		{Dst: "other.com", Proto: "tls"},
	}, nil)

	cmd := newRemoveCmd(t, f)
	completions, _ := cmd.ValidArgsFunction(cmd, nil, "")

	require.Len(t, completions, 2)
	assert.Equal(t, "example.com", string(completions[0]))
	assert.Equal(t, "other.com", string(completions[1]))
}

// mockFirewallFunc returns a Factory-compatible Firewall closure
// backed by a FirewallManagerMock with the given List behavior.
func mockFirewallFunc(rules []config.EgressRule, listErr error) func(context.Context) (firewall.FirewallManager, error) {
	return func(_ context.Context) (firewall.FirewallManager, error) {
		return &fwmocks.FirewallManagerMock{
			ListFunc: func(_ context.Context) ([]config.EgressRule, error) {
				return rules, listErr
			},
		}, nil
	}
}

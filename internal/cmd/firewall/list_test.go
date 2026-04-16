package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	cpmocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// newListCmd creates a list command wired to a Factory with the given mock
// rules returned via the AdminServiceClient mock's FirewallListRules.
func newListCmd(t *testing.T, rules []*adminv1.EgressRule, listErr error) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	ios, _, stdout, _ := iostreams.Test()

	mock := &cpmocks.AdminServiceClientMock{
		FirewallListRulesFunc: func(_ context.Context, _ *adminv1.FirewallListRulesRequest, _ ...grpc.CallOption) (*adminv1.FirewallListRulesResult, error) {
			if listErr != nil {
				return nil, listErr
			}
			return &adminv1.FirewallListRulesResult{Rules: rules}, nil
		},
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
		AdminClient: func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return mock, nil
		},
	}

	return f, stdout
}

func TestListRun_SortsByDomain(t *testing.T) {
	rules := []*adminv1.EgressRule{
		{Dst: "zebra.example.com", Proto: "tls"},
		{Dst: "alpha.example.com", Proto: "tls"},
		{Dst: "middle.example.com", Proto: "tls"},
	}

	tests := []struct {
		name   string
		args   []string
		verify func(t *testing.T, stdout string)
	}{
		{
			name: "default table",
			args: []string{},
			verify: func(t *testing.T, stdout string) {
				t.Helper()
				alphaIdx := strings.Index(stdout, "alpha.example.com")
				middleIdx := strings.Index(stdout, "middle.example.com")
				zebraIdx := strings.Index(stdout, "zebra.example.com")
				require.NotEqual(t, -1, alphaIdx)
				require.NotEqual(t, -1, middleIdx)
				require.NotEqual(t, -1, zebraIdx)
				assert.Greater(t, middleIdx, alphaIdx)
				assert.Greater(t, zebraIdx, middleIdx)
			},
		},
		{
			name: "quiet",
			args: []string{"--quiet"},
			verify: func(t *testing.T, stdout string) {
				t.Helper()
				lines := strings.Split(strings.TrimSpace(stdout), "\n")
				require.Len(t, lines, 3)
				assert.Equal(t, "alpha.example.com", lines[0])
				assert.Equal(t, "middle.example.com", lines[1])
				assert.Equal(t, "zebra.example.com", lines[2])
			},
		},
		{
			name: "json",
			args: []string{"--json"},
			verify: func(t *testing.T, stdout string) {
				t.Helper()
				var rows []ruleRow
				require.NoError(t, json.Unmarshal([]byte(stdout), &rows))
				require.Len(t, rows, 3)
				assert.Equal(t, "alpha.example.com", rows[0].Domain)
				assert.Equal(t, "middle.example.com", rows[1].Domain)
				assert.Equal(t, "zebra.example.com", rows[2].Domain)
			},
		},
		{
			name: "template",
			args: []string{"--format", "{{.Domain}}"},
			verify: func(t *testing.T, stdout string) {
				t.Helper()
				lines := strings.Split(strings.TrimSpace(stdout), "\n")
				require.Len(t, lines, 3)
				assert.Equal(t, "alpha.example.com", lines[0])
				assert.Equal(t, "middle.example.com", lines[1])
				assert.Equal(t, "zebra.example.com", lines[2])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, stdout := newListCmd(t, rules, nil)
			cmd := NewCmdList(f, nil)
			cmd.SetContext(context.Background())
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			require.NoError(t, err)
			tt.verify(t, stdout.String())
		})
	}
}

func TestListRun_SortsByDomainProtoPort(t *testing.T) {
	rules := []*adminv1.EgressRule{
		{Dst: "api.github.com", Proto: "tcp", Port: 22},
		{Dst: "api.github.com", Proto: "tls", Port: 443},
		{Dst: "api.github.com", Proto: "http", Port: 80},
		{Dst: "alpha.example.com", Proto: "tls", Port: 443},
	}

	f, stdout := newListCmd(t, rules, nil)
	cmd := NewCmdList(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--json"})

	err := cmd.Execute()
	require.NoError(t, err)

	var rows []ruleRow
	require.NoError(t, json.Unmarshal([]byte(stdout.String()), &rows))
	require.Len(t, rows, 4)

	assert.Equal(t, "alpha.example.com", rows[0].Domain)
	assert.Equal(t, "api.github.com", rows[1].Domain)
	assert.Equal(t, "http", rows[1].Proto)
	assert.Equal(t, "api.github.com", rows[2].Domain)
	assert.Equal(t, "tcp", rows[2].Proto)
	assert.Equal(t, "api.github.com", rows[3].Domain)
	assert.Equal(t, "tls", rows[3].Proto)
}

func TestListRun_EmptyRules(t *testing.T) {
	f, stdout := newListCmd(t, nil, nil)
	cmd := NewCmdList(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "No active firewall rules.")
}

func TestListRun_ListError(t *testing.T) {
	f, _ := newListCmd(t, nil, fmt.Errorf("corrupt store"))
	cmd := NewCmdList(f, nil)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing firewall rules")
}

package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/firewall"
	fwmocks "github.com/schmitthub/clawker/internal/firewall/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newListCmd creates a list command wired to a Factory with the given mock rules.
// Returns the Factory's iostreams stdout buffer for output verification.
func newListCmd(t *testing.T, rules []config.EgressRule, listErr error) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	ios, _, stdout, _ := iostreams.Test()

	mock := &fwmocks.FirewallManagerMock{
		ListFunc: func(_ context.Context) ([]config.EgressRule, error) {
			return rules, listErr
		},
	}

	f := &cmdutil.Factory{
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
		Firewall: func(_ context.Context) (firewall.FirewallManager, error) {
			return mock, nil
		},
	}

	return f, stdout
}

func TestListRun_SortsByDomain(t *testing.T) {
	rules := []config.EgressRule{
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
				require.NotEqual(t, -1, alphaIdx, "alpha should appear in output")
				require.NotEqual(t, -1, middleIdx, "middle should appear in output")
				require.NotEqual(t, -1, zebraIdx, "zebra should appear in output")
				assert.Greater(t, middleIdx, alphaIdx, "middle should appear after alpha")
				assert.Greater(t, zebraIdx, middleIdx, "zebra should appear after middle")
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

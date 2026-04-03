package firewall

import (
	"bytes"
	"context"
	"encoding/json"
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

func newListOptions(t *testing.T, rules []config.EgressRule) (*ListOptions, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ios, _, stdout, stderr := iostreams.Test()

	mock := &fwmocks.FirewallManagerMock{
		ListFunc: func(_ context.Context) ([]config.EgressRule, error) {
			return rules, nil
		},
	}

	opts := &ListOptions{
		IOStreams: ios,
		TUI:       tui.NewTUI(ios),
		Firewall: func(_ context.Context) (firewall.FirewallManager, error) {
			return mock, nil
		},
		Format: &cmdutil.FormatFlags{},
	}

	return opts, stdout, stderr
}

func TestListRun_SortsByDomainAlphabetically(t *testing.T) {
	rules := []config.EgressRule{
		{Dst: "zebra.example.com", Proto: "tls"},
		{Dst: "alpha.example.com", Proto: "tls"},
		{Dst: "middle.example.com", Proto: "tls"},
	}

	opts, stdout, _ := newListOptions(t, rules)

	err := listRun(context.Background(), opts)
	require.NoError(t, err)

	output := stdout.String()
	alphaIdx := strings.Index(output, "alpha.example.com")
	middleIdx := strings.Index(output, "middle.example.com")
	zebraIdx := strings.Index(output, "zebra.example.com")

	require.NotEqual(t, -1, alphaIdx, "alpha should appear in output")
	require.NotEqual(t, -1, middleIdx, "middle should appear in output")
	require.NotEqual(t, -1, zebraIdx, "zebra should appear in output")
	assert.Greater(t, middleIdx, alphaIdx, "middle should appear after alpha")
	assert.Greater(t, zebraIdx, middleIdx, "zebra should appear after middle")
}

func TestListRun_SortsByDomain_QuietMode(t *testing.T) {
	rules := []config.EgressRule{
		{Dst: "zebra.example.com"},
		{Dst: "alpha.example.com"},
		{Dst: "middle.example.com"},
	}

	opts, stdout, _ := newListOptions(t, rules)
	opts.Format = &cmdutil.FormatFlags{Quiet: true}

	err := listRun(context.Background(), opts)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "alpha.example.com", lines[0])
	assert.Equal(t, "middle.example.com", lines[1])
	assert.Equal(t, "zebra.example.com", lines[2])
}

func TestListRun_SortsByDomain_JSONFormat(t *testing.T) {
	rules := []config.EgressRule{
		{Dst: "zebra.example.com", Proto: "tls"},
		{Dst: "alpha.example.com", Proto: "tls"},
	}

	opts, stdout, _ := newListOptions(t, rules)
	jsonFmt, err := cmdutil.ParseFormat("json")
	require.NoError(t, err)
	opts.Format = &cmdutil.FormatFlags{Format: jsonFmt}

	err = listRun(context.Background(), opts)
	require.NoError(t, err)

	var rows []ruleRow
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &rows))
	require.Len(t, rows, 2)
	assert.Equal(t, "alpha.example.com", rows[0].Domain)
	assert.Equal(t, "zebra.example.com", rows[1].Domain)
}

func TestListRun_EmptyRules(t *testing.T) {
	opts, stdout, _ := newListOptions(t, nil)

	err := listRun(context.Background(), opts)
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "No active firewall rules.")
}

package list //nolint:testpackage // exercises the unexported run function directly

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	adminv1mocks "github.com/schmitthub/clawker/api/admin/v1/mocks"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// newListCmd creates a list command wired to a Factory with the given mock
// rules returned via the AdminServiceClient mock's FirewallListRules.
//
//nolint:exhaustruct // mock wires only the RPC list drives; the Factory only the nouns it reads
func newListCmd(t *testing.T, rules []*adminv1.EgressRule, listErr error) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	ios, _, stdout, _ := iostreams.Test()

	mock := &adminv1mocks.AdminServiceClientMock{
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

// TestNewCmdList asserts the constructor parses the format flags onto
// ListOptions before the run function sees them.
func TestNewCmdList(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantJSON     bool
		wantQuiet    bool
		wantTemplate bool
	}{
		{name: "no flags", args: []string{}, wantJSON: false, wantQuiet: false, wantTemplate: false},
		{name: "json", args: []string{"--json"}, wantJSON: true, wantQuiet: false, wantTemplate: false},
		{
			name:         "format json",
			args:         []string{"--format", "json"},
			wantJSON:     true,
			wantQuiet:    false,
			wantTemplate: false,
		},
		{name: "quiet", args: []string{"--quiet"}, wantJSON: false, wantQuiet: true, wantTemplate: false},
		{name: "quiet short", args: []string{"-q"}, wantJSON: false, wantQuiet: true, wantTemplate: false},
		{
			name:         "template",
			args:         []string{"--format", "{{.Domain}}"},
			wantJSON:     false,
			wantQuiet:    false,
			wantTemplate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := newListCmd(t, nil, nil)

			var gotOpts *ListOptions
			cmd := NewCmdList(f, func(_ context.Context, opts *ListOptions) error {
				gotOpts = opts
				return nil
			})
			cmd.SetContext(context.Background())
			cmd.SetArgs(tt.args)

			require.NoError(t, cmd.Execute())
			require.NotNil(t, gotOpts)
			assert.Equal(t, f.IOStreams, gotOpts.IOStreams)
			assert.Equal(t, f.TUI, gotOpts.TUI)
			require.NotNil(t, gotOpts.Format)
			assert.Equal(t, tt.wantJSON, gotOpts.Format.IsJSON())
			assert.Equal(t, tt.wantQuiet, gotOpts.Format.Quiet)
			assert.Equal(t, tt.wantTemplate, gotOpts.Format.IsTemplate())
		})
	}
}

// sortingRules is a deliberately out-of-order rule set so the run function's
// sort is observable in every output mode.
func sortingRules() []*adminv1.EgressRule {
	return []*adminv1.EgressRule{
		{Dst: "zebra.example.com", Proto: "https"},
		{Dst: "alpha.example.com", Proto: "https"},
		{Dst: "middle.example.com", Proto: "https"},
	}
}

func TestListRun_SortsByDomain(t *testing.T) {
	rules := sortingRules()

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

func protoPortRules() []*adminv1.EgressRule {
	return []*adminv1.EgressRule{
		{Dst: "api.github.com", Proto: "ssh", Port: "22"},
		{Dst: "api.github.com", Proto: "https", Port: "443"},
		{Dst: "api.github.com", Proto: "https", Port: "80"},
		{Dst: "alpha.example.com", Proto: "https", Port: "443"},
	}
}

func TestListRun_SortsByDomainProtoPort(t *testing.T) {
	f, stdout := newListCmd(t, protoPortRules(), nil)
	cmd := NewCmdList(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--json"})

	err := cmd.Execute()
	require.NoError(t, err)

	var rows []ruleRow
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &rows))
	require.Len(t, rows, 4)

	// Sort key is (domain, proto, port — string compare). alpha.example.com
	// comes first; the three api.github.com rows sort by proto then port:
	// https/"443" < https/"80" (string compare) < ssh/"22".
	assert.Equal(t, "alpha.example.com", rows[0].Domain)
	assert.Equal(t, "api.github.com", rows[1].Domain)
	assert.Equal(t, "https", rows[1].Proto)
	assert.Equal(t, "443", rows[1].Port)
	assert.Equal(t, "api.github.com", rows[2].Domain)
	assert.Equal(t, "https", rows[2].Proto)
	assert.Equal(t, "80", rows[2].Port)
	assert.Equal(t, "api.github.com", rows[3].Domain)
	assert.Equal(t, "ssh", rows[3].Proto)
}

// TestListRun_JSONContract_OmitsPathFieldsWhenEmpty guards backward
// compatibility: scripts reading `firewall list --json` must not see new
// keys when no path data is present on a rule.
func TestListRun_JSONContract_OmitsPathFieldsWhenEmpty(t *testing.T) {
	rules := []*adminv1.EgressRule{
		{Dst: "example.com", Proto: "https", Port: "443"},
	}

	f, stdout := newListCmd(t, rules, nil)
	cmd := NewCmdList(f, nil)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--json"})

	require.NoError(t, cmd.Execute())

	out := stdout.String()
	assert.NotContains(t, out, "path_default")
	assert.NotContains(t, out, "paths")
}

func pathRules() []*adminv1.EgressRule {
	return []*adminv1.EgressRule{
		{
			Dst:   "api.example.com",
			Proto: "https",
			Port:  "443",
			PathRules: []*adminv1.PathRule{
				{Path: "/admin/*", Action: "deny"},
				{Path: "/v1/*", Action: "allow"},
			},
			PathDefault: "deny",
		},
	}
}

func TestListRun_WithPaths(t *testing.T) {
	rules := pathRules()

	t.Run("json includes paths sorted by path string", func(t *testing.T) {
		f, stdout := newListCmd(t, rules, nil)
		cmd := NewCmdList(f, nil)
		cmd.SetContext(context.Background())
		cmd.SetArgs([]string{"--json"})

		require.NoError(t, cmd.Execute())

		var rows []ruleRow
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &rows))
		require.Len(t, rows, 1)
		assert.Equal(t, "deny", rows[0].PathDefault)
		require.Len(t, rows[0].Paths, 2)
		assert.Equal(t, "/admin/*", rows[0].Paths[0].Path)
		assert.Equal(t, "deny", rows[0].Paths[0].Action)
		assert.Equal(t, "/v1/*", rows[0].Paths[1].Path)
		assert.Equal(t, "allow", rows[0].Paths[1].Action)
	})

	t.Run("table renders indented path sub-rows and default row", func(t *testing.T) {
		f, stdout := newListCmd(t, rules, nil)
		cmd := NewCmdList(f, nil)
		cmd.SetContext(context.Background())
		cmd.SetArgs([]string{})

		require.NoError(t, cmd.Execute())

		out := stdout.String()
		assert.Contains(t, out, "api.example.com")
		assert.Contains(t, out, "  /admin/*")
		assert.Contains(t, out, "  /v1/*")
		assert.Contains(t, out, "  path default")

		adminIdx := strings.Index(out, "/admin/*")
		v1Idx := strings.Index(out, "/v1/*")
		defaultIdx := strings.Index(out, "path default")
		assert.Less(t, adminIdx, v1Idx, "paths should sort alphabetically")
		assert.Less(t, v1Idx, defaultIdx, "path default row should follow path rows")
	})
}

func methodGatedRules() []*adminv1.EgressRule {
	return []*adminv1.EgressRule{
		{
			Dst:   "api.example.com",
			Proto: "https",
			Port:  "443",
			PathRules: []*adminv1.PathRule{
				{Path: "/blog/", Action: "allow", Methods: []string{"GET", "HEAD"}},
				{Path: "/resume/", Action: "allow"},
			},
		},
	}
}

// TestListRun_WithMethodGatedPaths verifies method-gated path rules surface
// in both output modes: JSON carries the methods array, and the table shows
// a METHODS column so the user can tell a GET-only allow from a full allow.
func TestListRun_WithMethodGatedPaths(t *testing.T) {
	rules := methodGatedRules()

	t.Run("json includes methods and omits when empty", func(t *testing.T) {
		f, stdout := newListCmd(t, rules, nil)
		cmd := NewCmdList(f, nil)
		cmd.SetContext(context.Background())
		cmd.SetArgs([]string{"--json"})

		require.NoError(t, cmd.Execute())

		var rows []ruleRow
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &rows))
		require.Len(t, rows, 1)
		require.Len(t, rows[0].Paths, 2)
		assert.Equal(t, []string{"GET", "HEAD"}, rows[0].Paths[0].Methods)
		assert.Nil(t, rows[0].Paths[1].Methods)
		// Method-less path rule must not emit a "methods" key.
		assert.NotContains(t, stdout.String(), `"methods":null`)
	})

	t.Run("table renders methods column on gated sub-row", func(t *testing.T) {
		f, stdout := newListCmd(t, rules, nil)
		cmd := NewCmdList(f, nil)
		cmd.SetContext(context.Background())
		cmd.SetArgs([]string{})

		require.NoError(t, cmd.Execute())

		out := stdout.String()
		assert.Contains(t, out, "METHODS")
		assert.Contains(t, out, "GET,HEAD")
	})
}

// denylistRules has only deny path rules and no explicit r.path_default, so
// the catch-all must be inferred as "allow".
func denylistRules() []*adminv1.EgressRule {
	return []*adminv1.EgressRule{
		{
			Dst:   "docs.example.com",
			Proto: "https",
			Port:  "443",
			PathRules: []*adminv1.PathRule{
				{Path: "/admin", Action: "deny"},
			},
			// PathDefault deliberately unset — must be inferred to "allow".
		},
	}
}

// TestListRun_WithDenylistPaths_InfersAllowDefault locks in the inferred
// path_default display for the denylist case (only deny path_rules, no
// explicit r.path_default). Without inference, the list output would
// silently hide the catch-all action and the user couldn't tell what
// Envoy actually enforces. See adminv1.EffectivePathDefault.
func TestListRun_WithDenylistPaths_InfersAllowDefault(t *testing.T) {
	rules := denylistRules()

	t.Run("table renders inferred path default row", func(t *testing.T) {
		f, stdout := newListCmd(t, rules, nil)
		cmd := NewCmdList(f, nil)
		cmd.SetContext(context.Background())
		cmd.SetArgs([]string{})

		require.NoError(t, cmd.Execute())

		out := stdout.String()
		assert.Contains(t, out, "  /admin")
		assert.Contains(t, out, "  path default")
		// ACTION follows DOMAIN, so the inferred "allow" must appear on the
		// same line, after the "path default" label.
		defaultIdx := strings.Index(out, "path default")
		require.NotEqual(t, -1, defaultIdx)
		lineEnd := strings.Index(out[defaultIdx:], "\n")
		require.NotEqual(t, -1, lineEnd)
		assert.Contains(t, out[defaultIdx:defaultIdx+lineEnd], "allow")
	})

	t.Run("json carries inferred path_default", func(t *testing.T) {
		f, stdout := newListCmd(t, rules, nil)
		cmd := NewCmdList(f, nil)
		cmd.SetContext(context.Background())
		cmd.SetArgs([]string{"--json"})

		require.NoError(t, cmd.Execute())

		var rows []ruleRow
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &rows))
		require.Len(t, rows, 1)
		assert.Equal(t, "allow", rows[0].PathDefault, "denylist mode: only-deny path rules → allow catch-all")
	})
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
	f, _ := newListCmd(t, nil, errors.New("corrupt store"))
	cmd := NewCmdList(f, nil)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing firewall rules")
}

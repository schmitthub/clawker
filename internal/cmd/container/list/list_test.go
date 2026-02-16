package list

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()
	return &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:      tui.NewTUI(tio.IOStreams),
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() *config.Config {
			return config.NewConfigForTest(nil, nil)
		},
	}, tio
}

// --- Tier 1: Flag parsing tests ---

func TestNewCmdList(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantQuiet bool
		wantAll   bool
	}{
		{
			name:  "no flags",
			input: "",
		},
		{
			name:      "quiet flag",
			input:     "-q",
			wantQuiet: true,
		},
		{
			name:      "quiet flag long",
			input:     "--quiet",
			wantQuiet: true,
		},
		{
			name:    "all flag",
			input:   "-a",
			wantAll: true,
		},
		{
			name:    "all flag long",
			input:   "--all",
			wantAll: true,
		},
		{
			name:      "both flags",
			input:     "-q -a",
			wantQuiet: true,
			wantAll:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreamstest.New()
			f := &cmdutil.Factory{IOStreams: tio.IOStreams}

			var gotOpts *ListOptions
			cmd := NewCmdList(f, func(_ context.Context, opts *ListOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantQuiet, gotOpts.Format.Quiet)
			require.Equal(t, tt.wantAll, gotOpts.All)
		})
	}
}

func TestNewCmdList_FormatFlags(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:  "json flag",
			input: "--json",
		},
		{
			name:  "format json",
			input: "--format json",
		},
		{
			name:  "format template",
			input: "--format '{{.Name}}'",
		},
		{
			name:    "quiet and json are mutually exclusive",
			input:   "-q --json",
			wantErr: "mutually exclusive",
		},
		{
			name:    "quiet and format are mutually exclusive",
			input:   "-q --format json",
			wantErr: "mutually exclusive",
		},
		{
			name:    "json and format are mutually exclusive",
			input:   "--json --format json",
			wantErr: "mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreamstest.New()
			f := &cmdutil.Factory{IOStreams: tio.IOStreams}

			cmd := NewCmdList(f, func(_ context.Context, _ *ListOptions) error {
				return nil
			})

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCmdList_Properties(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}
	cmd := NewCmdList(f, nil)

	require.Equal(t, "list", cmd.Use)
	require.Contains(t, cmd.Aliases, "ls")
	require.Contains(t, cmd.Aliases, "ps")
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("quiet"))
	require.NotNil(t, cmd.Flags().Lookup("all"))
	require.NotNil(t, cmd.Flags().Lookup("format"))
	require.NotNil(t, cmd.Flags().Lookup("json"))
	require.NotNil(t, cmd.Flags().Lookup("filter"))
	require.NotNil(t, cmd.Flags().Lookup("project"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("q"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("a"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("p"))
}

// --- Tier 2: Integration tests ---

func TestListRun_DefaultTable(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList(
		dockertest.RunningContainerFixture("myapp", "dev"),
	)

	f, tio := testFactory(t, fake)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"-a"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := tio.OutBuf.String()
	assert.Contains(t, out, "clawker.myapp.dev")
	assert.Contains(t, out, "running")
	assert.Contains(t, out, "myapp")
	assert.Contains(t, out, "dev")
	fake.AssertCalled(t, "ContainerList")
}

func TestListRun_JSONOutput(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList(
		dockertest.RunningContainerFixture("myapp", "dev"),
	)

	f, tio := testFactory(t, fake)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"-a", "--json"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := tio.OutBuf.String()
	assert.Contains(t, out, `"name": "clawker.myapp.dev"`)
	assert.Contains(t, out, `"status": "running"`)
	assert.Contains(t, out, `"project": "myapp"`)
	assert.Contains(t, out, `"agent": "dev"`)
}

func TestListRun_QuietMode(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList(
		dockertest.RunningContainerFixture("myapp", "dev"),
		dockertest.RunningContainerFixture("myapp", "worker"),
	)

	f, tio := testFactory(t, fake)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"-a", "-q"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := tio.OutBuf.String()
	assert.Contains(t, out, "clawker.myapp.dev")
	assert.Contains(t, out, "clawker.myapp.worker")
	// Quiet mode: names only, no table headers
	assert.NotContains(t, out, "STATUS")
	assert.NotContains(t, out, "PROJECT")
}

func TestListRun_TemplateOutput(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList(
		dockertest.RunningContainerFixture("myapp", "dev"),
	)

	f, tio := testFactory(t, fake)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"-a", "--format", "{{.Name}} {{.Agent}}"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := tio.OutBuf.String()
	assert.Contains(t, out, "clawker.myapp.dev dev")
}

func TestListRun_FilterByStatus(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList(
		dockertest.RunningContainerFixture("myapp", "dev"),
		dockertest.ContainerFixture("myapp", "worker", "alpine:latest"),
	)

	f, tio := testFactory(t, fake)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"-a", "--filter", "status=running"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := tio.OutBuf.String()
	assert.Contains(t, out, "clawker.myapp.dev")
	assert.NotContains(t, out, "clawker.myapp.worker")
}

func TestListRun_FilterByAgent(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList(
		dockertest.RunningContainerFixture("myapp", "dev"),
		dockertest.RunningContainerFixture("myapp", "worker"),
	)

	f, tio := testFactory(t, fake)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"-a", "--filter", "agent=dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := tio.OutBuf.String()
	assert.Contains(t, out, "clawker.myapp.dev")
	assert.NotContains(t, out, "clawker.myapp.worker")
}

func TestListRun_FilterInvalidKey(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList()

	f, tio := testFactory(t, fake)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"--filter", "invalid=value"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid filter key")
}

func TestListRun_EmptyResults(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList()

	f, tio := testFactory(t, fake)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"-a"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	assert.Empty(t, tio.OutBuf.String())
	assert.Contains(t, tio.ErrBuf.String(), "No clawker containers found.")
}

func TestListRun_EmptyResultsRunningOnly(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList()

	f, tio := testFactory(t, fake)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	assert.Contains(t, tio.ErrBuf.String(), "No running clawker containers found. Use -a to show all containers.")
}

func TestListRun_DockerConnectionError(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:      tui.NewTUI(tio.IOStreams),
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("cannot connect to Docker daemon")
		},
		Config: func() *config.Config {
			return config.NewConfigForTest(nil, nil)
		},
	}

	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestListRun_ProjectFilter(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList(
		dockertest.RunningContainerFixture("myapp", "dev"),
	)

	f, tio := testFactory(t, fake)
	cmd := NewCmdList(f, nil)
	cmd.SetArgs([]string{"-p", "myapp"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := tio.OutBuf.String()
	assert.Contains(t, out, "clawker.myapp.dev")
	fake.AssertCalled(t, "ContainerList")
}

// --- Unit tests for helper functions ---

func TestFormatCreatedTime(t *testing.T) {
	tests := []struct {
		name     string
		duration int64 // seconds ago
		expected string
	}{
		{
			name:     "less than a minute",
			duration: 30,
			expected: "Less than a minute ago",
		},
		{
			name:     "1 minute",
			duration: 60,
			expected: "1 minute ago",
		},
		{
			name:     "5 minutes",
			duration: 300,
			expected: "5 minutes ago",
		},
		{
			name:     "1 hour",
			duration: 3600,
			expected: "1 hour ago",
		},
		{
			name:     "3 hours",
			duration: 10800,
			expected: "3 hours ago",
		},
		{
			name:     "1 day",
			duration: 86400,
			expected: "1 day ago",
		},
		{
			name:     "5 days",
			duration: 432000,
			expected: "5 days ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timestamp := time.Now().Unix() - tt.duration
			result := formatCreatedTime(timestamp)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestTruncateImage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "short image name",
			input:    "nginx:latest",
			expected: "nginx:latest",
		},
		{
			name:     "exactly 40 chars",
			input:    "1234567890123456789012345678901234567890",
			expected: "1234567890123456789012345678901234567890",
		},
		{
			name:     "long image name",
			input:    "registry.example.com/organization/very-long-image-name:v1.2.3",
			expected: "registry.example.com/organization/ver...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateImage(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		name    string
		s       string
		pattern string
		want    bool
	}{
		{"exact match", "dev", "dev", true},
		{"no match", "dev", "worker", false},
		{"wildcard prefix", "dev", "de*", true},
		{"wildcard no match", "dev", "work*", false},
		{"wildcard all", "anything", "*", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, matchGlob(tt.s, tt.pattern))
		})
	}
}

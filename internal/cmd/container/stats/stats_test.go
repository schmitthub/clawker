package stats

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/shlex"
	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdStats(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAgent  bool
		wantStream bool
		wantTrunc  bool
		wantArgs   []string
		wantErr    bool
		wantErrMsg string
		needRes    bool
	}{
		{
			name:       "no flags",
			input:      "",
			wantStream: false,
			wantTrunc:  false,
			wantArgs:   []string{},
		},
		{
			name:       "with no-stream flag",
			input:      "--no-stream",
			wantStream: true,
			wantTrunc:  false,
			wantArgs:   []string{},
		},
		{
			name:       "with no-trunc flag",
			input:      "--no-trunc",
			wantStream: false,
			wantTrunc:  true,
			wantArgs:   []string{},
		},
		{
			name:       "with all flags",
			input:      "--no-stream --no-trunc",
			wantStream: true,
			wantTrunc:  true,
			wantArgs:   []string{},
		},
		{
			name:       "with container names",
			input:      "--no-stream container1 container2",
			wantStream: true,
			wantTrunc:  false,
			wantArgs:   []string{"container1", "container2"},
		},
		{
			name:      "with agent flag",
			input:     "--agent dev",
			wantAgent: true,
			wantArgs:  []string{"dev"},
			needRes:   true,
		},
		{
			name:       "with agent and no-stream flags",
			input:      "--agent dev --no-stream",
			wantAgent:  true,
			wantStream: true,
			wantArgs:   []string{"dev"},
			needRes:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			if tt.needRes {
				f.Config = func() *config.Config {
					return config.NewConfigForTest(nil, nil)
				}
			}

			var gotOpts *StatsOptions
			cmd := NewCmdStats(f, func(_ context.Context, opts *StatsOptions) error {
				gotOpts = opts
				return nil
			})

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.EqualError(t, err, tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantAgent, gotOpts.Agent)
			require.Equal(t, tt.wantStream, gotOpts.NoStream)
			require.Equal(t, tt.wantTrunc, gotOpts.NoTrunc)
			require.Equal(t, tt.wantArgs, gotOpts.Containers)
		})
	}
}

func TestCmdStats_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdStats(f, nil)

	require.Equal(t, "stats [OPTIONS] [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("no-stream"))
	require.NotNil(t, cmd.Flags().Lookup("no-trunc"))

	noStream, _ := cmd.Flags().GetBool("no-stream")
	require.False(t, noStream)

	noTrunc, _ := cmd.Flags().GetBool("no-trunc")
	require.False(t, noTrunc)
}

func TestCmdStats_AllowsNoArgs(t *testing.T) {
	f := &cmdutil.Factory{}

	var gotOpts *StatsOptions
	cmd := NewCmdStats(f, func(_ context.Context, opts *StatsOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	require.Empty(t, gotOpts.Containers)
}

func TestCmdStats_MultipleContainers(t *testing.T) {
	f := &cmdutil.Factory{}

	var gotOpts *StatsOptions
	cmd := NewCmdStats(f, func(_ context.Context, opts *StatsOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"container1", "container2", "container3"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	require.Equal(t, []string{"container1", "container2", "container3"}, gotOpts.Containers)
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    uint64
		expected string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.00KB"},
		{1536, "1.50KB"},
		{1048576, "1.00MB"},
		{1073741824, "1.00GB"},
		{1099511627776, "1.00TB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatBytes(tt.bytes)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateCPUPercent(t *testing.T) {
	tests := []struct {
		name           string
		cpuUsage       uint64
		preCPUUsage    uint64
		systemUsage    uint64
		preSystemUsage uint64
		onlineCPUs     uint32
		expected       float64
	}{
		{
			name:           "zero delta - no usage",
			cpuUsage:       1000,
			preCPUUsage:    1000,
			systemUsage:    2000,
			preSystemUsage: 2000,
			onlineCPUs:     4,
			expected:       0.0,
		},
		{
			name:           "zero system delta",
			cpuUsage:       2000,
			preCPUUsage:    1000,
			systemUsage:    2000,
			preSystemUsage: 2000,
			onlineCPUs:     4,
			expected:       0.0,
		},
		{
			name:           "zero cpu delta",
			cpuUsage:       1000,
			preCPUUsage:    1000,
			systemUsage:    3000,
			preSystemUsage: 2000,
			onlineCPUs:     4,
			expected:       0.0,
		},
		{
			name:           "normal usage single core",
			cpuUsage:       2000000000,
			preCPUUsage:    1000000000,
			systemUsage:    20000000000,
			preSystemUsage: 10000000000,
			onlineCPUs:     1,
			expected:       10.0,
		},
		{
			name:           "normal usage multi core",
			cpuUsage:       2000000000,
			preCPUUsage:    1000000000,
			systemUsage:    20000000000,
			preSystemUsage: 10000000000,
			onlineCPUs:     4,
			expected:       40.0,
		},
		{
			name:           "100% single core",
			cpuUsage:       2000000000,
			preCPUUsage:    1000000000,
			systemUsage:    2000000000,
			preSystemUsage: 1000000000,
			onlineCPUs:     1,
			expected:       100.0,
		},
		{
			name:           "50% of 8 cores",
			cpuUsage:       5000000000,
			preCPUUsage:    1000000000,
			systemUsage:    9000000000,
			preSystemUsage: 1000000000,
			onlineCPUs:     8,
			expected:       400.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := &container.StatsResponse{}
			stats.CPUStats.CPUUsage.TotalUsage = tt.cpuUsage
			stats.PreCPUStats.CPUUsage.TotalUsage = tt.preCPUUsage
			stats.CPUStats.SystemUsage = tt.systemUsage
			stats.PreCPUStats.SystemUsage = tt.preSystemUsage
			stats.CPUStats.OnlineCPUs = tt.onlineCPUs

			result := calculateCPUPercent(stats)
			require.InDelta(t, tt.expected, result, 0.01)
		})
	}
}

// --- Tier 2 tests (Cobra+Factory, real run function) ---

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

// statsJSON is a realistic stats JSON for testing.
const statsJSON = `{
	"read": "2024-01-01T00:00:01Z",
	"cpu_stats": {
		"cpu_usage": {"total_usage": 2000000000},
		"system_cpu_usage": 20000000000,
		"online_cpus": 4
	},
	"precpu_stats": {
		"cpu_usage": {"total_usage": 1000000000},
		"system_cpu_usage": 10000000000
	},
	"memory_stats": {
		"usage": 52428800,
		"limit": 1073741824
	},
	"networks": {
		"eth0": {"rx_bytes": 1024, "tx_bytes": 2048}
	},
	"pids_stats": {"current": 5}
}`

func TestStatsRun_NoStream_HappyPath(t *testing.T) {
	fake := dockertest.NewFakeClient()
	c := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", c)
	fake.SetupContainerStats(statsJSON)

	f, tio := testFactory(t, fake)
	cmd := NewCmdStats(f, nil)
	cmd.SetArgs([]string{"--no-stream", "clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	out := tio.OutBuf.String()
	assert.Contains(t, out, "CONTAINER ID")
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "CPU %")
	assert.Contains(t, out, "MEM USAGE / LIMIT")
	assert.Contains(t, out, "clawker.myapp.dev")
	// Memory: 50MB / 1GB
	assert.Contains(t, out, "50.00MB")
	// PIDs
	assert.Contains(t, out, "5")
}

func TestStatsRun_DockerConnectionError(t *testing.T) {
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

	cmd := NewCmdStats(f, nil)
	cmd.SetArgs([]string{"--no-stream", "clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestStatsRun_ContainerNotFound(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList() // empty list

	f, tio := testFactory(t, fake)
	cmd := NewCmdStats(f, nil)
	cmd.SetArgs([]string{"--no-stream", "clawker.myapp.nonexistent"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Contains(t, tio.ErrBuf.String(), "nonexistent")
	assert.Contains(t, tio.ErrBuf.String(), "not found")
}

func TestStatsRun_NoRunningContainers(t *testing.T) {
	fake := dockertest.NewFakeClient()
	fake.SetupContainerList() // empty list — no running containers

	f, tio := testFactory(t, fake)
	cmd := NewCmdStats(f, nil)
	cmd.SetArgs([]string{"--no-stream"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, tio.ErrBuf.String(), "No running containers")
}

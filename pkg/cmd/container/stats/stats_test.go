package stats

import (
	"bytes"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmd(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		output     Options
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "no flags",
			input:  "",
			output: Options{NoStream: false, NoTrunc: false},
		},
		{
			name:   "with no-stream flag",
			input:  "--no-stream",
			output: Options{NoStream: true, NoTrunc: false},
		},
		{
			name:   "with no-trunc flag",
			input:  "--no-trunc",
			output: Options{NoStream: false, NoTrunc: true},
		},
		{
			name:   "with all flags",
			input:  "--no-stream --no-trunc",
			output: Options{NoStream: true, NoTrunc: true},
		},
		{
			name:   "with container names",
			input:  "--no-stream container1 container2",
			output: Options{NoStream: true, NoTrunc: false},
		},
		{
			name:   "with agent flag",
			input:  "--agent ralph",
			output: Options{Agent: true, NoStream: false, NoTrunc: false},
		},
		{
			name:   "with agent and no-stream flags",
			input:  "--agent ralph --no-stream",
			output: Options{Agent: true, NoStream: true, NoTrunc: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var cmdOpts *Options
			cmd := NewCmd(f)

			// Override RunE to capture options instead of executing
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				cmdOpts = &Options{}
				cmdOpts.Agent, _ = cmd.Flags().GetBool("agent")
				cmdOpts.NoStream, _ = cmd.Flags().GetBool("no-stream")
				cmdOpts.NoTrunc, _ = cmd.Flags().GetBool("no-trunc")
				return nil
			}

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
			argv := testutil.SplitArgs(tt.input)

			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.EqualError(t, err, tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.output.Agent, cmdOpts.Agent)
			require.Equal(t, tt.output.NoStream, cmdOpts.NoStream)
			require.Equal(t, tt.output.NoTrunc, cmdOpts.NoTrunc)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "stats [OPTIONS] [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("no-stream"))
	require.NotNil(t, cmd.Flags().Lookup("no-trunc"))

	// Test default values
	noStream, _ := cmd.Flags().GetBool("no-stream")
	require.False(t, noStream)

	noTrunc, _ := cmd.Flags().GetBool("no-trunc")
	require.False(t, noTrunc)
}

func TestCmd_AllowsNoArgs(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Override RunE to not actually execute
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return nil
	}

	// Stats can be called with no args (shows all containers)
	cmd.SetArgs([]string{})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
}

func TestCmd_MultipleContainers(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	var capturedArgs []string
	// Override RunE to capture args
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		capturedArgs = args
		return nil
	}

	// Stats can be called with multiple containers
	cmd.SetArgs([]string{"container1", "container2", "container3"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.Equal(t, []string{"container1", "container2", "container3"}, capturedArgs)
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
			expected:       10.0, // (1B / 10B) * 1 * 100 = 10%
		},
		{
			name:           "normal usage multi core",
			cpuUsage:       2000000000,
			preCPUUsage:    1000000000,
			systemUsage:    20000000000,
			preSystemUsage: 10000000000,
			onlineCPUs:     4,
			expected:       40.0, // (1B / 10B) * 4 * 100 = 40%
		},
		{
			name:           "100% single core",
			cpuUsage:       2000000000,
			preCPUUsage:    1000000000,
			systemUsage:    2000000000,
			preSystemUsage: 1000000000,
			onlineCPUs:     1,
			expected:       100.0, // (1B / 1B) * 1 * 100 = 100%
		},
		{
			name:           "50% of 8 cores",
			cpuUsage:       5000000000,
			preCPUUsage:    1000000000,
			systemUsage:    9000000000,
			preSystemUsage: 1000000000,
			onlineCPUs:     8,
			expected:       400.0, // (4B / 8B) * 8 * 100 = 400%
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

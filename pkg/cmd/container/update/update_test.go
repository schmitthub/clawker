package update

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmd/testutil"
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
			name:  "no flags",
			input: "mycontainer",
			output: Options{
				CPUs:              0,
				CPUShares:         0,
				CPUsetCPUs:        "",
				CPUsetMems:        "",
				Memory:            "",
				MemoryReservation: "",
				MemorySwap:        "",
				PidsLimit:         0,
				BlkioWeight:       0,
			},
		},
		{
			name:  "with cpus flag",
			input: "--cpus 2 mycontainer",
			output: Options{
				CPUs: 2,
			},
		},
		{
			name:  "with memory flag",
			input: "--memory 512m mycontainer",
			output: Options{
				Memory: "512m",
			},
		},
		{
			name:  "with memory shorthand",
			input: "-m 1g mycontainer",
			output: Options{
				Memory: "1g",
			},
		},
		{
			name:  "with cpu-shares flag",
			input: "--cpu-shares 512 mycontainer",
			output: Options{
				CPUShares: 512,
			},
		},
		{
			name:  "with cpuset-cpus flag",
			input: "--cpuset-cpus 0-3 mycontainer",
			output: Options{
				CPUsetCPUs: "0-3",
			},
		},
		{
			name:  "with cpuset-mems flag",
			input: "--cpuset-mems 0,1 mycontainer",
			output: Options{
				CPUsetMems: "0,1",
			},
		},
		{
			name:  "with memory-reservation flag",
			input: "--memory-reservation 256m mycontainer",
			output: Options{
				MemoryReservation: "256m",
			},
		},
		{
			name:  "with memory-swap flag",
			input: "--memory-swap 1g mycontainer",
			output: Options{
				MemorySwap: "1g",
			},
		},
		{
			name:  "with pids-limit flag",
			input: "--pids-limit 100 mycontainer",
			output: Options{
				PidsLimit: 100,
			},
		},
		{
			name:  "with blkio-weight flag",
			input: "--blkio-weight 500 mycontainer",
			output: Options{
				BlkioWeight: 500,
			},
		},
		{
			name:  "with multiple flags",
			input: "--cpus 1.5 --memory 512m --pids-limit 200 mycontainer",
			output: Options{
				CPUs:      1.5,
				Memory:    "512m",
				PidsLimit: 200,
			},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "requires at least 1 arg(s), only received 0",
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
				cmdOpts.CPUs, _ = cmd.Flags().GetFloat64("cpus")
				cmdOpts.CPUShares, _ = cmd.Flags().GetInt64("cpu-shares")
				cmdOpts.CPUsetCPUs, _ = cmd.Flags().GetString("cpuset-cpus")
				cmdOpts.CPUsetMems, _ = cmd.Flags().GetString("cpuset-mems")
				cmdOpts.Memory, _ = cmd.Flags().GetString("memory")
				cmdOpts.MemoryReservation, _ = cmd.Flags().GetString("memory-reservation")
				cmdOpts.MemorySwap, _ = cmd.Flags().GetString("memory-swap")
				cmdOpts.PidsLimit, _ = cmd.Flags().GetInt64("pids-limit")
				cmdOpts.BlkioWeight = uint16(mustGetUint16(cmd, "blkio-weight"))
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
			require.Equal(t, tt.output.CPUs, cmdOpts.CPUs)
			require.Equal(t, tt.output.CPUShares, cmdOpts.CPUShares)
			require.Equal(t, tt.output.CPUsetCPUs, cmdOpts.CPUsetCPUs)
			require.Equal(t, tt.output.CPUsetMems, cmdOpts.CPUsetMems)
			require.Equal(t, tt.output.Memory, cmdOpts.Memory)
			require.Equal(t, tt.output.MemoryReservation, cmdOpts.MemoryReservation)
			require.Equal(t, tt.output.MemorySwap, cmdOpts.MemorySwap)
			require.Equal(t, tt.output.PidsLimit, cmdOpts.PidsLimit)
			require.Equal(t, tt.output.BlkioWeight, cmdOpts.BlkioWeight)
		})
	}
}

func mustGetUint16(cmd *cobra.Command, name string) uint16 {
	v, _ := cmd.Flags().GetUint16(name)
	return v
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "update [OPTIONS] CONTAINER [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("cpus"))
	require.NotNil(t, cmd.Flags().Lookup("cpu-shares"))
	require.NotNil(t, cmd.Flags().Lookup("cpuset-cpus"))
	require.NotNil(t, cmd.Flags().Lookup("cpuset-mems"))
	require.NotNil(t, cmd.Flags().Lookup("memory"))
	require.NotNil(t, cmd.Flags().Lookup("memory-reservation"))
	require.NotNil(t, cmd.Flags().Lookup("memory-swap"))
	require.NotNil(t, cmd.Flags().Lookup("pids-limit"))
	require.NotNil(t, cmd.Flags().Lookup("blkio-weight"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("m"))
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

	// Update can be called with multiple containers
	cmd.SetArgs([]string{"container1", "container2", "container3"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.Equal(t, []string{"container1", "container2", "container3"}, capturedArgs)
}

func TestParseMemorySize(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"1024", 1024, false},
		{"1k", 1024, false},
		{"1K", 1024, false},
		{"512k", 512 * 1024, false},
		{"1m", 1024 * 1024, false},
		{"1M", 1024 * 1024, false},
		{"512m", 512 * 1024 * 1024, false},
		{"1g", 1024 * 1024 * 1024, false},
		{"1G", 1024 * 1024 * 1024, false},
		{"2g", 2 * 1024 * 1024 * 1024, false},
		{"1t", 1024 * 1024 * 1024 * 1024, false},
		{"100b", 100, false},
		{"", 0, true},
		{"invalid", 0, true},
		{"1x", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseMemorySize(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildUpdateResources(t *testing.T) {
	tests := []struct {
		name        string
		opts        *Options
		expectCPUs  int64
		expectMem   int64
		expectPids  *int64
		expectError bool
	}{
		{
			name: "empty options",
			opts: &Options{},
		},
		{
			name: "with CPUs",
			opts: &Options{
				CPUs: 2,
			},
			expectCPUs: 2e9,
		},
		{
			name: "with memory",
			opts: &Options{
				Memory: "512m",
			},
			expectMem: 512 * 1024 * 1024,
		},
		{
			name: "with pids limit",
			opts: &Options{
				PidsLimit: 100,
			},
			expectPids: int64Ptr(100),
		},
		{
			name: "invalid memory",
			opts: &Options{
				Memory: "invalid",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources, _, err := buildUpdateResources(tt.opts)
			if tt.expectError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.expectCPUs > 0 {
				require.Equal(t, tt.expectCPUs, resources.NanoCPUs)
			}
			if tt.expectMem > 0 {
				require.Equal(t, tt.expectMem, resources.Memory)
			}
			if tt.expectPids != nil {
				require.NotNil(t, resources.PidsLimit)
				require.Equal(t, *tt.expectPids, *resources.PidsLimit)
			}
		})
	}
}

func int64Ptr(v int64) *int64 {
	return &v
}

package update

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmd(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantErrMsg string
		// Expected flag values (we check these via flag lookups, not Options struct)
		wantCPUs              float64
		wantCPUShares         int64
		wantCPUsetCPUs        string
		wantCPUsetMems        string
		wantMemory            string
		wantMemoryReservation string
		wantMemorySwap        string
		wantPidsLimit         int64
		wantBlkioWeight       uint16
	}{
		{
			name:  "no flags",
			input: "mycontainer",
		},
		{
			name:     "with cpus flag",
			input:    "--cpus 2 mycontainer",
			wantCPUs: 2,
		},
		{
			name:       "with memory flag",
			input:      "--memory 512m mycontainer",
			wantMemory: "512MiB",
		},
		{
			name:       "with memory shorthand",
			input:      "-m 1g mycontainer",
			wantMemory: "1GiB",
		},
		{
			name:          "with cpu-shares flag",
			input:         "--cpu-shares 512 mycontainer",
			wantCPUShares: 512,
		},
		{
			name:           "with cpuset-cpus flag",
			input:          "--cpuset-cpus 0-3 mycontainer",
			wantCPUsetCPUs: "0-3",
		},
		{
			name:           "with cpuset-mems flag",
			input:          "--cpuset-mems 0,1 mycontainer",
			wantCPUsetMems: "0,1",
		},
		{
			name:                  "with memory-reservation flag",
			input:                 "--memory-reservation 256m mycontainer",
			wantMemoryReservation: "256MiB",
		},
		{
			name:           "with memory-swap flag",
			input:          "--memory-swap 1g mycontainer",
			wantMemorySwap: "1GiB",
		},
		{
			name:          "with pids-limit flag",
			input:         "--pids-limit 100 mycontainer",
			wantPidsLimit: 100,
		},
		{
			name:            "with blkio-weight flag",
			input:           "--blkio-weight 500 mycontainer",
			wantBlkioWeight: 500,
		},
		{
			name:          "with multiple flags",
			input:         "--cpus 1.5 --memory 512m --pids-limit 200 mycontainer",
			wantCPUs:      1.5,
			wantMemory:    "512MiB",
			wantPidsLimit: 200,
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "requires at least 1 container argument or --agent flag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			cmd := NewCmd(f)

			// Override RunE to prevent actual execution
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
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

			// Verify flag values were parsed correctly
			if tt.wantCPUs != 0 {
				cpusFlag := cmd.Flags().Lookup("cpus")
				require.NotNil(t, cpusFlag)
				cpus := cpusFlag.Value.(*docker.NanoCPUs)
				// NanoCPUs stores value in nanoseconds, convert back
				require.InDelta(t, tt.wantCPUs, float64(cpus.Value())/1e9, 0.001)
			}
			if tt.wantCPUShares != 0 {
				v, _ := cmd.Flags().GetInt64("cpu-shares")
				require.Equal(t, tt.wantCPUShares, v)
			}
			if tt.wantCPUsetCPUs != "" {
				v, _ := cmd.Flags().GetString("cpuset-cpus")
				require.Equal(t, tt.wantCPUsetCPUs, v)
			}
			if tt.wantCPUsetMems != "" {
				v, _ := cmd.Flags().GetString("cpuset-mems")
				require.Equal(t, tt.wantCPUsetMems, v)
			}
			if tt.wantMemory != "" {
				memFlag := cmd.Flags().Lookup("memory")
				require.NotNil(t, memFlag)
				require.Equal(t, tt.wantMemory, memFlag.Value.String())
			}
			if tt.wantMemoryReservation != "" {
				memFlag := cmd.Flags().Lookup("memory-reservation")
				require.NotNil(t, memFlag)
				require.Equal(t, tt.wantMemoryReservation, memFlag.Value.String())
			}
			if tt.wantMemorySwap != "" {
				memFlag := cmd.Flags().Lookup("memory-swap")
				require.NotNil(t, memFlag)
				require.Equal(t, tt.wantMemorySwap, memFlag.Value.String())
			}
			if tt.wantPidsLimit != 0 {
				v, _ := cmd.Flags().GetInt64("pids-limit")
				require.Equal(t, tt.wantPidsLimit, v)
			}
			if tt.wantBlkioWeight != 0 {
				v, _ := cmd.Flags().GetUint16("blkio-weight")
				require.Equal(t, tt.wantBlkioWeight, v)
			}
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "update [OPTIONS] [CONTAINER...]", cmd.Use)
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

func TestBuildUpdateResources(t *testing.T) {
	tests := []struct {
		name        string
		setupOpts   func() *Options
		expectCPUs  int64
		expectMem   int64
		expectPids  *int64
		expectError bool
	}{
		{
			name:      "empty options",
			setupOpts: func() *Options { return &Options{} },
		},
		{
			name: "with CPUs",
			setupOpts: func() *Options {
				opts := &Options{}
				_ = opts.cpus.Set("2")
				return opts
			},
			expectCPUs: 2e9,
		},
		{
			name: "with memory",
			setupOpts: func() *Options {
				opts := &Options{}
				_ = opts.memory.Set("512m")
				return opts
			},
			expectMem: 512 * 1024 * 1024,
		},
		{
			name: "with pids limit",
			setupOpts: func() *Options {
				return &Options{pidsLimit: 100}
			},
			expectPids: int64Ptr(100),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.setupOpts()
			resources, _ := buildUpdateResources(opts)
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

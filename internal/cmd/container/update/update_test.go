package update

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdUpdate(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantErrMsg string
		// Expected opts fields
		wantContainers        []string
		wantAgent             bool
		wantCPUs              float64
		wantCPUShares         int64
		wantCPUsetCPUs        string
		wantCPUsetMems        string
		wantMemory            int64
		wantMemoryReservation int64
		wantMemorySwap        int64
		wantPidsLimit         int64
		wantBlkioWeight       uint16
	}{
		{
			name:           "no flags",
			input:          "mycontainer",
			wantContainers: []string{"mycontainer"},
		},
		{
			name:           "with cpus flag",
			input:          "--cpus 2 mycontainer",
			wantContainers: []string{"mycontainer"},
			wantCPUs:       2,
		},
		{
			name:           "with memory flag",
			input:          "--memory 512m mycontainer",
			wantContainers: []string{"mycontainer"},
			wantMemory:     512 * 1024 * 1024,
		},
		{
			name:           "with memory shorthand",
			input:          "-m 1g mycontainer",
			wantContainers: []string{"mycontainer"},
			wantMemory:     1024 * 1024 * 1024,
		},
		{
			name:           "with cpu-shares flag",
			input:          "--cpu-shares 512 mycontainer",
			wantContainers: []string{"mycontainer"},
			wantCPUShares:  512,
		},
		{
			name:           "with cpuset-cpus flag",
			input:          "--cpuset-cpus 0-3 mycontainer",
			wantContainers: []string{"mycontainer"},
			wantCPUsetCPUs: "0-3",
		},
		{
			name:           "with cpuset-mems flag",
			input:          "--cpuset-mems 0,1 mycontainer",
			wantContainers: []string{"mycontainer"},
			wantCPUsetMems: "0,1",
		},
		{
			name:                  "with memory-reservation flag",
			input:                 "--memory-reservation 256m mycontainer",
			wantContainers:        []string{"mycontainer"},
			wantMemoryReservation: 256 * 1024 * 1024,
		},
		{
			name:           "with memory-swap flag",
			input:          "--memory-swap 1g mycontainer",
			wantContainers: []string{"mycontainer"},
			wantMemorySwap: 1024 * 1024 * 1024,
		},
		{
			name:           "with pids-limit flag",
			input:          "--pids-limit 100 mycontainer",
			wantContainers: []string{"mycontainer"},
			wantPidsLimit:  100,
		},
		{
			name:            "with blkio-weight flag",
			input:           "--blkio-weight 500 mycontainer",
			wantContainers:  []string{"mycontainer"},
			wantBlkioWeight: 500,
		},
		{
			name:           "with multiple flags",
			input:          "--cpus 1.5 --memory 512m --pids-limit 200 mycontainer",
			wantContainers: []string{"mycontainer"},
			wantCPUs:       1.5,
			wantMemory:     512 * 1024 * 1024,
			wantPidsLimit:  200,
		},
		{
			name:           "with agent flag",
			input:          "--agent dev",
			wantContainers: []string{"dev"},
			wantAgent:      true,
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "requires at least 1 container argument or --agent flag",
		},
		{
			name:           "multiple containers",
			input:          "container1 container2 container3",
			wantContainers: []string{"container1", "container2", "container3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() config.Provider {
					return config.NewConfigForTest(nil, nil)
				},
			}

			var gotOpts *UpdateOptions
			cmd := NewCmdUpdate(f, func(_ context.Context, opts *UpdateOptions) error {
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

			// Verify containers
			require.Equal(t, tt.wantContainers, gotOpts.Containers)

			// Verify agent flag
			require.Equal(t, tt.wantAgent, gotOpts.Agent)

			// Verify flag values via opts fields
			if tt.wantCPUs != 0 {
				require.InDelta(t, tt.wantCPUs, float64(gotOpts.cpus.Value())/1e9, 0.001)
			}
			if tt.wantCPUShares != 0 {
				require.Equal(t, tt.wantCPUShares, gotOpts.cpuShares)
			}
			if tt.wantCPUsetCPUs != "" {
				require.Equal(t, tt.wantCPUsetCPUs, gotOpts.cpusetCpus)
			}
			if tt.wantCPUsetMems != "" {
				require.Equal(t, tt.wantCPUsetMems, gotOpts.cpusetMems)
			}
			if tt.wantMemory != 0 {
				require.Equal(t, tt.wantMemory, gotOpts.memory.Value())
			}
			if tt.wantMemoryReservation != 0 {
				require.Equal(t, tt.wantMemoryReservation, gotOpts.memoryReservation.Value())
			}
			if tt.wantMemorySwap != 0 {
				require.Equal(t, tt.wantMemorySwap, gotOpts.memorySwap.Value())
			}
			if tt.wantPidsLimit != 0 {
				require.Equal(t, tt.wantPidsLimit, gotOpts.pidsLimit)
			}
			if tt.wantBlkioWeight != 0 {
				require.Equal(t, tt.wantBlkioWeight, gotOpts.blkioWeight)
			}
		})
	}
}

func TestCmdUpdate_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdUpdate(f, nil)

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

func TestBuildUpdateResources(t *testing.T) {
	tests := []struct {
		name        string
		setupOpts   func() *UpdateOptions
		expectCPUs  int64
		expectMem   int64
		expectPids  *int64
		expectError bool
	}{
		{
			name:      "empty options",
			setupOpts: func() *UpdateOptions { return &UpdateOptions{} },
		},
		{
			name: "with CPUs",
			setupOpts: func() *UpdateOptions {
				opts := &UpdateOptions{}
				_ = opts.cpus.Set("2")
				return opts
			},
			expectCPUs: 2e9,
		},
		{
			name: "with memory",
			setupOpts: func() *UpdateOptions {
				opts := &UpdateOptions{}
				_ = opts.memory.Set("512m")
				return opts
			},
			expectMem: 512 * 1024 * 1024,
		},
		{
			name: "with pids limit",
			setupOpts: func() *UpdateOptions {
				return &UpdateOptions{pidsLimit: 100}
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

// --- Tier 2: Cobra+Factory integration tests ---

func testUpdateFactory(t *testing.T, fake *dockertest.FakeClient) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()
	tio := iostreamstest.New()

	return &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
	}, tio
}

func TestUpdateRun_Success(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewMockConfig())
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupContainerUpdate()

	f, tio := testUpdateFactory(t, fake)

	cmd := NewCmdUpdate(f, nil)
	cmd.SetArgs([]string{"--memory", "512m", "clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	assert.Contains(t, tio.OutBuf.String(), "clawker.myapp.dev")
	fake.AssertCalled(t, "ContainerUpdate")
}

func TestUpdateRun_DockerConnectionError(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		Client: func(_ context.Context) (*docker.Client, error) {
			return nil, fmt.Errorf("cannot connect to Docker daemon")
		},
		Config: func() config.Provider {
			return config.NewConfigForTest(nil, nil)
		},
	}

	cmd := NewCmdUpdate(f, nil)
	cmd.SetArgs([]string{"--memory", "512m", "mycontainer"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to Docker")
}

func TestUpdateRun_ContainerNotFound(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewMockConfig())
	fake.SetupContainerList() // empty list — container won't be found

	f, tio := testUpdateFactory(t, fake)

	cmd := NewCmdUpdate(f, nil)
	cmd.SetArgs([]string{"--memory", "512m", "clawker.myapp.dev"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)
	assert.Contains(t, tio.ErrBuf.String(), "clawker.myapp.dev")
}

func TestUpdateRun_PartialFailure(t *testing.T) {
	fake := dockertest.NewFakeClient(config.NewMockConfig())
	fixture := dockertest.RunningContainerFixture("myapp", "dev")
	fake.SetupFindContainer("clawker.myapp.dev", fixture)
	fake.SetupContainerUpdate()

	f, tio := testUpdateFactory(t, fake)

	cmd := NewCmdUpdate(f, nil)
	cmd.SetArgs([]string{"--memory", "512m", "clawker.myapp.dev", "clawker.myapp.missing"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.ErrorIs(t, err, cmdutil.SilentError)

	// First container succeeded
	assert.Contains(t, tio.OutBuf.String(), "clawker.myapp.dev")
	// Second container had error
	assert.Contains(t, tio.ErrBuf.String(), "clawker.myapp.missing")
}

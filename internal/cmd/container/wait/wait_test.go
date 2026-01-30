package wait

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdWait(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantErr        bool
		wantErrMsg     string
		wantContainers []string
	}{
		{
			name:           "single container",
			input:          "mycontainer",
			wantContainers: []string{"mycontainer"},
		},
		{
			name:           "multiple containers",
			input:          "container1 container2 container3",
			wantContainers: []string{"container1", "container2", "container3"},
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
			f := &cmdutil.Factory{
				Resolution: func() *config.Resolution {
					return &config.Resolution{ProjectKey: "testproject"}
				},
			}

			var gotOpts *WaitOptions
			cmd := NewCmdWait(f, func(_ context.Context, opts *WaitOptions) error {
				gotOpts = opts
				return nil
			})

			argv := []string{}
			if tt.input != "" {
				argv = testutil.SplitArgs(tt.input)
			}

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
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantContainers, gotOpts.Containers)
		})
	}
}

func TestNewCmdWait_AgentFlag(t *testing.T) {
	f := &cmdutil.Factory{
		Resolution: func() *config.Resolution {
			return &config.Resolution{ProjectKey: "testproject"}
		},
	}

	var gotOpts *WaitOptions
	cmd := NewCmdWait(f, func(_ context.Context, opts *WaitOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--agent", "ralph"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	require.True(t, gotOpts.Agent)
	require.Equal(t, []string{"ralph"}, gotOpts.Containers)
}

func TestNewCmdWait_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdWait(f, nil)

	require.Equal(t, "wait [OPTIONS] CONTAINER [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

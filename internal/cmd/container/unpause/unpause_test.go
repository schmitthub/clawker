package unpause

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewCmdUnpause(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantErr        bool
		wantErrMsg     string
		wantContainers []string
		wantAgent      bool
	}{
		{
			name:           "single container",
			args:           []string{"clawker.myapp.ralph"},
			wantContainers: []string{"clawker.myapp.ralph"},
		},
		{
			name:           "multiple containers",
			args:           []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			wantContainers: []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
		},
		{
			name:       "no container specified",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 argument",
		},
		{
			name:           "with agent flag",
			args:           []string{"--agent", "ralph"},
			wantContainers: []string{"ralph"},
			wantAgent:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Resolution: func() *config.Resolution {
					return &config.Resolution{ProjectKey: "testproject"}
				},
			}

			var gotOpts *UnpauseOptions
			cmd := NewCmdUnpause(f, func(_ context.Context, opts *UnpauseOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantContainers, gotOpts.Containers)
			require.Equal(t, tt.wantAgent, gotOpts.Agent)
		})
	}
}

func TestCmdUnpause_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdUnpause(f, nil)

	// Test command basics
	require.Equal(t, "unpause [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

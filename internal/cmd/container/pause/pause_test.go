package pause

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdPause(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantContainers []string
		wantAgent      bool
		wantErr        bool
		wantErrMsg     string
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
				Config: func() *config.Config {
					return config.NewConfigForTest(nil, nil)
				},
			}

			var gotOpts *PauseOptions
			cmd := NewCmdPause(f, func(_ context.Context, opts *PauseOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.args)

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			assert.Equal(t, tt.wantContainers, gotOpts.Containers)
			assert.Equal(t, tt.wantAgent, gotOpts.Agent)
		})
	}
}

func TestCmdPause_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdPause(f, nil)

	require.Equal(t, "pause [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

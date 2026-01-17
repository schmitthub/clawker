package pause

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmdPause(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "single container",
			args: []string{"clawker.myapp.ralph"},
		},
		{
			name: "multiple containers",
			args: []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
		},
		{
			name:       "no container specified",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "requires at least 1 arg(s), only received 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			cmd := NewCmdPause(f)

			// Override RunE to not actually execute
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				return nil
			}

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

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
		})
	}
}

func TestCmdPause_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdPause(f)

	// Test command basics
	require.Equal(t, "pause CONTAINER [CONTAINER...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

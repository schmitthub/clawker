package register

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdProjectRegister(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantName   string
		wantYes    bool
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "no arguments",
			args:     []string{},
			wantName: "",
		},
		{
			name:     "with project name",
			args:     []string{"my-project"},
			wantName: "my-project",
		},
		{
			name:    "with yes flag",
			args:    []string{"--yes"},
			wantYes: true,
		},
		{
			name:     "with name and yes flag",
			args:     []string{"--yes", "my-project"},
			wantName: "my-project",
			wantYes:  true,
		},
		{
			name:       "too many arguments",
			args:       []string{"one", "two"},
			wantErr:    true,
			wantErrMsg: "accepts at most 1 arg(s), received 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *RegisterOptions
			cmd := NewCmdProjectRegister(f, func(_ context.Context, opts *RegisterOptions) error {
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
			require.Equal(t, tt.wantName, gotOpts.Name)
			require.Equal(t, tt.wantYes, gotOpts.Yes)
		})
	}
}

func TestCmdProjectRegister_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdProjectRegister(f, nil)

	require.Equal(t, "register [project-name]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("yes"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("y"))
}

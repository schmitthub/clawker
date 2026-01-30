package attach

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestNewCmdAttach(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOpts   AttachOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "container name only",
			input:    "mycontainer",
			wantOpts: AttachOptions{SigProxy: true, container: "mycontainer"},
		},
		{
			name:     "no-stdin flag",
			input:    "--no-stdin mycontainer",
			wantOpts: AttachOptions{NoStdin: true, SigProxy: true, container: "mycontainer"},
		},
		{
			name:     "sig-proxy false",
			input:    "--sig-proxy=false mycontainer",
			wantOpts: AttachOptions{SigProxy: false, container: "mycontainer"},
		},
		{
			name:     "detach-keys flag",
			input:    "--detach-keys=ctrl-c mycontainer",
			wantOpts: AttachOptions{SigProxy: true, DetachKeys: "ctrl-c", container: "mycontainer"},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "attach: 'attach' requires 1 argument",
		},
		{
			name:       "too many arguments",
			input:      "container1 container2",
			wantErr:    true,
			wantErrMsg: "attach: 'attach' requires 1 argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *AttachOptions
			cmd := NewCmdAttach(f, func(_ context.Context, opts *AttachOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")

			argv := testutil.SplitArgs(tt.input)
			cmd.SetArgs(argv)
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
			require.Equal(t, tt.wantOpts.NoStdin, gotOpts.NoStdin)
			require.Equal(t, tt.wantOpts.SigProxy, gotOpts.SigProxy)
			require.Equal(t, tt.wantOpts.DetachKeys, gotOpts.DetachKeys)
			require.Equal(t, tt.wantOpts.container, gotOpts.container)
		})
	}
}

func TestCmdAttach_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdAttach(f, nil)

	require.Equal(t, "attach [OPTIONS] CONTAINER", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	require.NotNil(t, cmd.Flags().Lookup("no-stdin"))
	require.NotNil(t, cmd.Flags().Lookup("sig-proxy"))
	require.NotNil(t, cmd.Flags().Lookup("detach-keys"))

	sigProxy, _ := cmd.Flags().GetBool("sig-proxy")
	require.True(t, sigProxy)
}

func TestCmdAttach_ArgsParsing(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedContainer string
	}{
		{
			name:              "single container",
			args:              []string{"mycontainer"},
			expectedContainer: "mycontainer",
		},
		{
			name:              "full container name",
			args:              []string{"clawker.myapp.ralph"},
			expectedContainer: "clawker.myapp.ralph",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *AttachOptions
			cmd := NewCmdAttach(f, func(_ context.Context, opts *AttachOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.expectedContainer, gotOpts.container)
		})
	}
}

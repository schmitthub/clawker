package top

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewCmdTop(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAgent  bool
		wantArgs   []string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "with container name",
			input:    "mycontainer",
			wantArgs: []string{"mycontainer"},
		},
		{
			name:     "with container name and ps args",
			input:    "mycontainer aux",
			wantArgs: []string{"mycontainer", "aux"},
		},
		{
			name:     "with container name and multiple ps args",
			input:    "mycontainer -- -e -f",
			wantArgs: []string{"mycontainer", "-e", "-f"},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "requires at least 1 argument",
		},
		{
			name:      "with agent flag",
			input:     "--agent ralph",
			wantAgent: true,
			wantArgs:  []string{"ralph"},
		},
		{
			name:      "with agent flag and ps args",
			input:     "--agent ralph aux",
			wantAgent: true,
			wantArgs:  []string{"ralph", "aux"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() *config.Config {
					return config.NewConfig(func() (string, error) { return "/tmp/test", nil })
				},
			}

			var gotOpts *TopOptions
			cmd := NewCmdTop(f, func(_ context.Context, opts *TopOptions) error {
				gotOpts = opts
				return nil
			})

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)

			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErrMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)
			require.Equal(t, tt.wantAgent, gotOpts.Agent)
			require.Equal(t, tt.wantArgs, gotOpts.Args)
		})
	}
}

func TestCmdTop_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdTop(f, nil)

	// Test command basics
	require.Equal(t, "top CONTAINER [ps OPTIONS]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

func TestCmdTop_ArgsValidation(t *testing.T) {
	f := &cmdutil.Factory{}

	var gotOpts *TopOptions
	cmd := NewCmdTop(f, func(_ context.Context, opts *TopOptions) error {
		gotOpts = opts
		return nil
	})

	// Test with container and ps args (using -- to separate flags from args)
	cmd.SetArgs([]string{"container1", "--", "aux"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err := cmd.ExecuteC()
	require.NoError(t, err)
	require.NotNil(t, gotOpts)
	require.Equal(t, []string{"container1", "aux"}, gotOpts.Args)
}

func TestCmdTop_ArgsParsing(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedContainer string
		expectedPsArgs    int // number of ps args expected
	}{
		{
			name:              "container only",
			args:              []string{"mycontainer"},
			expectedContainer: "mycontainer",
			expectedPsArgs:    0,
		},
		{
			name:              "container with ps args",
			args:              []string{"mycontainer", "aux"},
			expectedContainer: "mycontainer",
			expectedPsArgs:    1,
		},
		{
			name:              "container with multiple ps args using separator",
			args:              []string{"mycontainer", "--", "-e", "-f"},
			expectedContainer: "mycontainer",
			expectedPsArgs:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var gotOpts *TopOptions
			cmd := NewCmdTop(f, func(_ context.Context, opts *TopOptions) error {
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
			require.Equal(t, tt.expectedContainer, gotOpts.Args[0])
			require.Equal(t, tt.expectedPsArgs, len(gotOpts.Args)-1)
		})
	}
}

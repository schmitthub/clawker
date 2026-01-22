package exec

import (
	"bytes"
	"testing"

	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmd(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOpts   Options
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "container and command",
			input:    "mycontainer ls",
			wantOpts: Options{},
		},
		{
			name:     "interactive flag",
			input:    "-i mycontainer /bin/sh",
			wantOpts: Options{Interactive: true},
		},
		{
			name:     "tty flag",
			input:    "-t mycontainer /bin/sh",
			wantOpts: Options{TTY: true},
		},
		{
			name:     "interactive and tty flags",
			input:    "-it mycontainer /bin/bash",
			wantOpts: Options{Interactive: true, TTY: true},
		},
		{
			name:     "detach flag",
			input:    "--detach mycontainer sleep 100",
			wantOpts: Options{Detach: true},
		},
		{
			name:     "env flag",
			input:    "-e FOO=bar mycontainer env",
			wantOpts: Options{Env: []string{"FOO=bar"}},
		},
		{
			name:     "multiple env flags",
			input:    "-e FOO=bar -e BAZ=qux mycontainer env",
			wantOpts: Options{Env: []string{"FOO=bar", "BAZ=qux"}},
		},
		{
			name:     "workdir flag",
			input:    "-w /tmp mycontainer pwd",
			wantOpts: Options{Workdir: "/tmp"},
		},
		{
			name:     "user flag",
			input:    "-u root mycontainer whoami",
			wantOpts: Options{User: "root"},
		},
		{
			name:     "privileged flag",
			input:    "--privileged mycontainer ls",
			wantOpts: Options{Privileged: true},
		},
		{
			name:     "with agent flag",
			input:    "--agent ralph ls",
			wantOpts: Options{Agent: true},
		},
		{
			name:     "agent with interactive and tty",
			input:    "-it --agent ralph /bin/bash",
			wantOpts: Options{Agent: true, Interactive: true, TTY: true},
		},
		{
			name:       "no arguments",
			input:      "",
			wantErr:    true,
			wantErrMsg: "exec: 'exec' requires at least 1 argument\n\nUsage:  exec [OPTIONS] [CONTAINER] COMMAND [ARG...] [flags]\n\nSee 'exec --help' for more information",
		},
		{
			name:     "container only (now valid - container is arg, no command)",
			input:    "mycontainer",
			wantOpts: Options{},
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
				cmdOpts.Agent, _ = cmd.Flags().GetBool("agent")
				cmdOpts.Interactive, _ = cmd.Flags().GetBool("interactive")
				cmdOpts.TTY, _ = cmd.Flags().GetBool("tty")
				cmdOpts.Detach, _ = cmd.Flags().GetBool("detach")
				cmdOpts.Env, _ = cmd.Flags().GetStringArray("env")
				cmdOpts.Workdir, _ = cmd.Flags().GetString("workdir")
				cmdOpts.User, _ = cmd.Flags().GetString("user")
				cmdOpts.Privileged, _ = cmd.Flags().GetBool("privileged")
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
			require.Equal(t, tt.wantOpts.Agent, cmdOpts.Agent)
			require.Equal(t, tt.wantOpts.Interactive, cmdOpts.Interactive)
			require.Equal(t, tt.wantOpts.TTY, cmdOpts.TTY)
			require.Equal(t, tt.wantOpts.Detach, cmdOpts.Detach)
			// Compare env slices - handle nil vs empty slice
			if len(tt.wantOpts.Env) == 0 {
				require.Empty(t, cmdOpts.Env)
			} else {
				require.Equal(t, tt.wantOpts.Env, cmdOpts.Env)
			}
			require.Equal(t, tt.wantOpts.Workdir, cmdOpts.Workdir)
			require.Equal(t, tt.wantOpts.User, cmdOpts.User)
			require.Equal(t, tt.wantOpts.Privileged, cmdOpts.Privileged)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmd(f)

	// Test command basics
	require.Equal(t, "exec [OPTIONS] [CONTAINER] COMMAND [ARG...]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("agent"))
	require.NotNil(t, cmd.Flags().Lookup("interactive"))
	require.NotNil(t, cmd.Flags().Lookup("tty"))
	require.NotNil(t, cmd.Flags().Lookup("detach"))
	require.NotNil(t, cmd.Flags().Lookup("env"))
	require.NotNil(t, cmd.Flags().Lookup("workdir"))
	require.NotNil(t, cmd.Flags().Lookup("user"))
	require.NotNil(t, cmd.Flags().Lookup("privileged"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("i"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("t"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("e"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("w"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("u"))
}

func TestCmd_ArgsParsing(t *testing.T) {
	tests := []struct {
		name              string
		args              []string
		expectedContainer string
		expectedCmdLen    int
	}{
		{
			name:              "container and single command",
			args:              []string{"mycontainer", "ls"},
			expectedContainer: "mycontainer",
			expectedCmdLen:    1,
		},
		{
			name:              "container and command with args",
			args:              []string{"mycontainer", "ls", "-la"},
			expectedContainer: "mycontainer",
			expectedCmdLen:    2,
		},
		{
			name:              "container and shell command",
			args:              []string{"mycontainer", "/bin/bash"},
			expectedContainer: "mycontainer",
			expectedCmdLen:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			cmd := NewCmd(f)

			var capturedContainer string
			var capturedCmdLen int

			// Override RunE to capture args
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				if len(args) >= 1 {
					capturedContainer = args[0]
					capturedCmdLen = len(args) - 1
				}
				return nil
			}

			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			require.NoError(t, err)
			require.Equal(t, tt.expectedContainer, capturedContainer)
			require.Equal(t, tt.expectedCmdLen, capturedCmdLen)
		})
	}
}

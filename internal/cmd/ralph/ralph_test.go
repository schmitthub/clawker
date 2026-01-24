package ralph

import (
	"bytes"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdRalph(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRalph(f)

	// Test parent command properties
	assert.Equal(t, "ralph", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Test subcommands exist
	subCmds := cmd.Commands()
	require.Len(t, subCmds, 3)

	var runCmd, statusCmd, resetCmd *cobra.Command
	for _, sub := range subCmds {
		switch sub.Use {
		case "run":
			runCmd = sub
		case "status":
			statusCmd = sub
		case "reset":
			resetCmd = sub
		}
	}

	require.NotNil(t, runCmd, "run subcommand should exist")
	require.NotNil(t, statusCmd, "status subcommand should exist")
	require.NotNil(t, resetCmd, "reset subcommand should exist")
}

func TestCmdRun_Flags(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRalph(f)
	runCmd, _, err := cmd.Find([]string{"run"})
	require.NoError(t, err)

	// Test required flags
	require.NotNil(t, runCmd.Flags().Lookup("agent"))

	// Test optional flags
	require.NotNil(t, runCmd.Flags().Lookup("prompt"))
	require.NotNil(t, runCmd.Flags().Lookup("prompt-file"))
	require.NotNil(t, runCmd.Flags().Lookup("max-loops"))
	require.NotNil(t, runCmd.Flags().Lookup("stagnation-threshold"))
	require.NotNil(t, runCmd.Flags().Lookup("timeout"))
	require.NotNil(t, runCmd.Flags().Lookup("reset-circuit"))
	require.NotNil(t, runCmd.Flags().Lookup("quiet"))
	require.NotNil(t, runCmd.Flags().Lookup("json"))
	require.NotNil(t, runCmd.Flags().Lookup("skip-permissions"))

	// Test shorthand flags
	require.NotNil(t, runCmd.Flags().ShorthandLookup("p"))
	require.NotNil(t, runCmd.Flags().ShorthandLookup("q"))

	// Test default values
	maxLoops, _ := runCmd.Flags().GetInt("max-loops")
	assert.Equal(t, 50, maxLoops)

	stagnationThreshold, _ := runCmd.Flags().GetInt("stagnation-threshold")
	assert.Equal(t, 3, stagnationThreshold)

	timeout, _ := runCmd.Flags().GetDuration("timeout")
	assert.Equal(t, 15*time.Minute, timeout)
}

func TestCmdRun_FlagParsing(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantErrMsg string
		checkOpts  func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name:       "missing required agent flag",
			input:      "run",
			wantErr:    true,
			wantErrMsg: "required flag(s) \"agent\" not set",
		},
		{
			name:  "with agent flag",
			input: "run --agent dev",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				agent, _ := cmd.Flags().GetString("agent")
				assert.Equal(t, "dev", agent)
			},
		},
		{
			name:  "with all flags",
			input: "run --agent dev --prompt test --max-loops 100 --stagnation-threshold 5 --timeout 30m --reset-circuit --quiet",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				agent, _ := cmd.Flags().GetString("agent")
				prompt, _ := cmd.Flags().GetString("prompt")
				maxLoops, _ := cmd.Flags().GetInt("max-loops")
				stagnationThreshold, _ := cmd.Flags().GetInt("stagnation-threshold")
				timeout, _ := cmd.Flags().GetDuration("timeout")
				resetCircuit, _ := cmd.Flags().GetBool("reset-circuit")
				quiet, _ := cmd.Flags().GetBool("quiet")

				assert.Equal(t, "dev", agent)
				assert.Equal(t, "test", prompt)
				assert.Equal(t, 100, maxLoops)
				assert.Equal(t, 5, stagnationThreshold)
				assert.Equal(t, 30*time.Minute, timeout)
				assert.True(t, resetCircuit)
				assert.True(t, quiet)
			},
		},
		{
			name:  "shorthand prompt flag",
			input: "run --agent dev -p 'Fix tests'",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				prompt, _ := cmd.Flags().GetString("prompt")
				assert.Equal(t, "Fix tests", prompt)
			},
		},
		{
			name:  "with skip-permissions flag",
			input: "run --agent dev --skip-permissions",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				skipPerms, _ := cmd.Flags().GetBool("skip-permissions")
				assert.True(t, skipPerms)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			cmd := NewCmdRalph(f)

			// Find the run subcommand
			runCmd, _, err := cmd.Find([]string{"run"})
			require.NoError(t, err)

			// Override RunE to prevent actual execution
			runCmd.RunE = func(cmd *cobra.Command, args []string) error {
				return nil
			}

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			argv := testutil.SplitArgs(tt.input)
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
			if tt.checkOpts != nil {
				tt.checkOpts(t, runCmd)
			}
		})
	}
}

func TestCmdRun_MutuallyExclusiveFlags(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRalph(f)

	// Find the run subcommand
	runCmd, _, err := cmd.Find([]string{"run"})
	require.NoError(t, err)

	// Override RunE to prevent actual execution
	runCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return nil
	}

	cmd.SetArgs(testutil.SplitArgs("run --agent dev --prompt test --prompt-file test.md"))
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	_, err = cmd.ExecuteC()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "if any flags in the group [prompt prompt-file] are set none of the others can be")
}

func TestCmdStatus_Flags(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRalph(f)
	statusCmd, _, err := cmd.Find([]string{"status"})
	require.NoError(t, err)

	// Test required flags
	require.NotNil(t, statusCmd.Flags().Lookup("agent"))

	// Test optional flags
	require.NotNil(t, statusCmd.Flags().Lookup("json"))
}

func TestCmdStatus_FlagParsing(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantErrMsg string
		checkOpts  func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name:       "missing required agent flag",
			input:      "status",
			wantErr:    true,
			wantErrMsg: "required flag(s) \"agent\" not set",
		},
		{
			name:  "with agent flag",
			input: "status --agent dev",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				agent, _ := cmd.Flags().GetString("agent")
				assert.Equal(t, "dev", agent)
			},
		},
		{
			name:  "with json flag",
			input: "status --agent dev --json",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				json, _ := cmd.Flags().GetBool("json")
				assert.True(t, json)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			cmd := NewCmdRalph(f)

			// Find the status subcommand
			statusCmd, _, err := cmd.Find([]string{"status"})
			require.NoError(t, err)

			// Override RunE to prevent actual execution
			statusCmd.RunE = func(cmd *cobra.Command, args []string) error {
				return nil
			}

			argv := testutil.SplitArgs(tt.input)
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
			if tt.checkOpts != nil {
				tt.checkOpts(t, statusCmd)
			}
		})
	}
}

func TestCmdReset_Flags(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdRalph(f)
	resetCmd, _, err := cmd.Find([]string{"reset"})
	require.NoError(t, err)

	// Test required flags
	require.NotNil(t, resetCmd.Flags().Lookup("agent"))

	// Test optional flags
	require.NotNil(t, resetCmd.Flags().Lookup("all"))
	require.NotNil(t, resetCmd.Flags().Lookup("quiet"))

	// Test shorthand flags
	require.NotNil(t, resetCmd.Flags().ShorthandLookup("q"))
}

func TestCmdReset_FlagParsing(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErr    bool
		wantErrMsg string
		checkOpts  func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name:       "missing required agent flag",
			input:      "reset",
			wantErr:    true,
			wantErrMsg: "required flag(s) \"agent\" not set",
		},
		{
			name:  "with agent flag",
			input: "reset --agent dev",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				agent, _ := cmd.Flags().GetString("agent")
				assert.Equal(t, "dev", agent)
			},
		},
		{
			name:  "with all flag",
			input: "reset --agent dev --all",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				all, _ := cmd.Flags().GetBool("all")
				assert.True(t, all)
			},
		},
		{
			name:  "with quiet flag",
			input: "reset --agent dev -q",
			checkOpts: func(t *testing.T, cmd *cobra.Command) {
				quiet, _ := cmd.Flags().GetBool("quiet")
				assert.True(t, quiet)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}
			cmd := NewCmdRalph(f)

			// Find the reset subcommand
			resetCmd, _, err := cmd.Find([]string{"reset"})
			require.NoError(t, err)

			// Override RunE to prevent actual execution
			resetCmd.RunE = func(cmd *cobra.Command, args []string) error {
				return nil
			}

			argv := testutil.SplitArgs(tt.input)
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
			if tt.checkOpts != nil {
				tt.checkOpts(t, resetCmd)
			}
		})
	}
}

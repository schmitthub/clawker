package logs

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNewCmdLogs(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		args       []string
		output     LogsOptions
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "single container",
			input:  "",
			args:   []string{"clawker.myapp.ralph"},
			output: LogsOptions{Tail: "all"},
		},
		{
			name:   "with follow flag",
			input:  "--follow",
			args:   []string{"clawker.myapp.ralph"},
			output: LogsOptions{Follow: true, Tail: "all"},
		},
		{
			name:   "with shorthand follow flag",
			input:  "-f",
			args:   []string{"clawker.myapp.ralph"},
			output: LogsOptions{Follow: true, Tail: "all"},
		},
		{
			name:   "with timestamps flag",
			input:  "--timestamps",
			args:   []string{"clawker.myapp.ralph"},
			output: LogsOptions{Timestamps: true, Tail: "all"},
		},
		{
			name:   "with shorthand timestamps flag",
			input:  "-t",
			args:   []string{"clawker.myapp.ralph"},
			output: LogsOptions{Timestamps: true, Tail: "all"},
		},
		{
			name:   "with tail flag",
			input:  "--tail 50",
			args:   []string{"clawker.myapp.ralph"},
			output: LogsOptions{Tail: "50"},
		},
		{
			name:   "with since flag",
			input:  "--since 2024-01-01T00:00:00Z",
			args:   []string{"clawker.myapp.ralph"},
			output: LogsOptions{Since: "2024-01-01T00:00:00Z", Tail: "all"},
		},
		{
			name:   "with until flag",
			input:  "--until 2024-01-02T00:00:00Z",
			args:   []string{"clawker.myapp.ralph"},
			output: LogsOptions{Until: "2024-01-02T00:00:00Z", Tail: "all"},
		},
		{
			name:   "with details flag",
			input:  "--details",
			args:   []string{"clawker.myapp.ralph"},
			output: LogsOptions{Details: true, Tail: "all"},
		},
		{
			name:   "with agent flag",
			input:  "--agent",
			args:   []string{"ralph"},
			output: LogsOptions{Agent: true, Tail: "all"},
		},
		{
			name:       "no container specified",
			input:      "",
			args:       []string{},
			wantErr:    true,
			wantErrMsg: "accepts 1 arg(s), received 0",
		},
		{
			name:       "too many containers",
			input:      "",
			args:       []string{"clawker.myapp.ralph", "clawker.myapp.writer"},
			wantErr:    true,
			wantErrMsg: "accepts 1 arg(s), received 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Config: func() *config.Config {
					return config.NewConfig(func() (string, error) { return "/tmp/test", nil })
				},
			}

			var gotOpts *LogsOptions
			cmd := NewCmdLogs(f, func(_ context.Context, opts *LogsOptions) error {
				gotOpts = opts
				return nil
			})

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			// Parse arguments
			argv := tt.args
			if tt.input != "" {
				parsed, err := shlex.Split(tt.input)
				require.NoError(t, err)
				argv = append(parsed, tt.args...)
			}

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
			require.Equal(t, tt.output.Agent, gotOpts.Agent)
			require.Equal(t, tt.output.Follow, gotOpts.Follow)
			require.Equal(t, tt.output.Timestamps, gotOpts.Timestamps)
			require.Equal(t, tt.output.Details, gotOpts.Details)
			require.Equal(t, tt.output.Since, gotOpts.Since)
			require.Equal(t, tt.output.Until, gotOpts.Until)
			require.Equal(t, tt.output.Tail, gotOpts.Tail)
		})
	}
}

func TestCmdLogs_Properties(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdLogs(f, nil)

	// Test command basics
	require.Equal(t, "logs [CONTAINER]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("follow"))
	require.NotNil(t, cmd.Flags().Lookup("timestamps"))
	require.NotNil(t, cmd.Flags().Lookup("details"))
	require.NotNil(t, cmd.Flags().Lookup("since"))
	require.NotNil(t, cmd.Flags().Lookup("until"))
	require.NotNil(t, cmd.Flags().Lookup("tail"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("t"))

	// Test default tail
	tail, _ := cmd.Flags().GetString("tail")
	require.Equal(t, "all", tail)
}

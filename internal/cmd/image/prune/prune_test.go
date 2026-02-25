package prune

import (
	"context"
	"testing"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/stretchr/testify/require"
)

func TestNewCmd(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantOpts PruneOptions
	}{
		{
			name:     "no flags",
			input:    "",
			wantOpts: PruneOptions{},
		},
		{
			name:     "force flag",
			input:    "-f",
			wantOpts: PruneOptions{Force: true},
		},
		{
			name:     "force flag long",
			input:    "--force",
			wantOpts: PruneOptions{Force: true},
		},
		{
			name:     "all flag",
			input:    "-a",
			wantOpts: PruneOptions{All: true},
		},
		{
			name:     "all flag long",
			input:    "--all",
			wantOpts: PruneOptions{All: true},
		},
		{
			name:     "both flags",
			input:    "-f -a",
			wantOpts: PruneOptions{Force: true, All: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio, in, out, errOut := iostreams.Test()
			f := &cmdutil.Factory{
				IOStreams: tio,
				Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
				Prompter:  func() *prompter.Prompter { return nil },
			}

			var gotOpts *PruneOptions
			cmd := NewCmdPrune(f, func(_ context.Context, opts *PruneOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.Flags().BoolP("help", "x", false, "")

			argv, err := shlex.Split(tt.input)
			require.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(in)
			cmd.SetOut(out)
			cmd.SetErr(errOut)

			_, err = cmd.ExecuteC()
			require.NoError(t, err)
			require.Equal(t, tt.wantOpts.Force, gotOpts.Force)
			require.Equal(t, tt.wantOpts.All, gotOpts.All)
		})
	}
}

func TestCmd_Properties(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Prompter:  func() *prompter.Prompter { return nil },
	}
	cmd := NewCmdPrune(f, nil)

	// Test command basics
	require.Equal(t, "prune [OPTIONS]", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)

	// Test flags exist
	require.NotNil(t, cmd.Flags().Lookup("force"))
	require.NotNil(t, cmd.Flags().Lookup("all"))

	// Test shorthand flags
	require.NotNil(t, cmd.Flags().ShorthandLookup("f"))
	require.NotNil(t, cmd.Flags().ShorthandLookup("a"))
}

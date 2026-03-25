package init

import (
	"bytes"
	"context"
	"testing"

	projectinit "github.com/schmitthub/clawker/internal/cmd/project/init"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testFactory builds a minimal Factory for init alias tests.
// Returns the factory and the stderr buffer for output assertions.
// Config, Logger, and ProjectManager are nil — tests must inject runF to avoid nil panics.
func testFactory(t *testing.T) (*cmdutil.Factory, *bytes.Buffer) {
	t.Helper()
	tio, _, _, errOut := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
	}, errOut
}

func TestNewCmdInit_FlagForwarding(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantName  string
		wantForce bool
		wantYes   bool
		wantErr   bool
	}{
		{name: "defaults", args: []string{}},
		{name: "yes flag", args: []string{"--yes"}, wantYes: true},
		{name: "force flag", args: []string{"--force"}, wantForce: true},
		{name: "positional arg", args: []string{"my-project"}, wantName: "my-project"},
		{name: "all flags and arg", args: []string{"--yes", "--force", "my-project"}, wantYes: true, wantForce: true, wantName: "my-project"},
		{name: "too many args", args: []string{"one", "two"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := testFactory(t)

			var gotOpts *projectinit.ProjectInitOptions
			cmd := NewCmdInit(f, func(_ context.Context, opts *projectinit.ProjectInitOptions) error {
				gotOpts = opts
				return nil
			})

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, gotOpts)

			assert.Equal(t, f.IOStreams, gotOpts.IOStreams, "IOStreams should be wired from factory")
			assert.NotNil(t, gotOpts.TUI, "TUI should be wired from factory")
			assert.Equal(t, tt.wantYes, gotOpts.Yes)
			assert.Equal(t, tt.wantForce, gotOpts.Force)
			assert.Equal(t, tt.wantName, gotOpts.Name)
		})
	}
}

func TestNewCmdInit_PrintsAliasTip(t *testing.T) {
	f, errOut := testFactory(t)

	cmd := NewCmdInit(f, func(_ context.Context, _ *projectinit.ProjectInitOptions) error {
		return nil
	})

	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())

	assert.NotEmpty(t, errOut.String(), "should print alias tip to stderr")
}

// TestNewCmdInit_FlagParityWithProjectInit catches drift between the alias and
// the delegate command's flag definitions. If project init adds a new flag,
// this test will fail — reminding you to add it to the alias too.
func TestNewCmdInit_FlagParityWithProjectInit(t *testing.T) {
	f, _ := testFactory(t)

	aliasCmd := NewCmdInit(f, nil)
	delegateCmd := projectinit.NewCmdProjectInit(f, nil)

	aliasFlags := make(map[string]bool)
	aliasCmd.Flags().VisitAll(func(f *pflag.Flag) {
		aliasFlags[f.Name] = true
	})

	delegateCmd.Flags().VisitAll(func(f *pflag.Flag) {
		assert.True(t, aliasFlags[f.Name],
			"project init has flag --%s that init alias is missing", f.Name)
	})
}

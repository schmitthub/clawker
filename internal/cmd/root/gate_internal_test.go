package root

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
)

func gateFactory(t *testing.T, seed string) *cmdutil.Factory {
	t.Helper()
	tio, _, _, errOut := iostreams.Test()
	t.Cleanup(func() {
		if errOut.Len() > 0 {
			t.Logf("stderr: %s", errOut.String())
		}
	})
	reg, err := project.NewRegistryFromString(seed)
	require.NoError(t, err)
	f := new(cmdutil.Factory)
	f.IOStreams = tio
	f.Logger = func() (*logger.Logger, error) { return logger.Nop(), nil }
	f.ProjectRegistry = func() (project.Registry, error) { return reg, nil }
	return f
}

func TestProjectGate(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	tests := []struct {
		name    string
		seed    string
		args    []string
		wantErr bool
	}{
		{name: "container outside project", seed: "", args: []string{"container", "list"}, wantErr: true},
		{name: "builtin alias outside project", seed: "", args: []string{"ps"}, wantErr: true},
		{name: "build outside project", seed: "", args: []string{"build"}, wantErr: true},
		{name: "version outside project", seed: "", args: []string{"version"}, wantErr: false},
		{name: "init outside project", seed: "", args: []string{"init"}, wantErr: false},
		{name: "project register outside project", seed: "", args: []string{"project", "register"}, wantErr: false},
		{name: "settings outside project", seed: "", args: []string{"settings", "get"}, wantErr: false},
		{name: "help outside project", seed: "", args: []string{"help"}, wantErr: false},
		{
			name:    "container inside project",
			seed:    fmt.Sprintf("projects:\n  - name: demo\n    root: %s\n", root),
			args:    []string{"container", "list"},
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := NewCmdRoot(gateFactory(t, tc.seed), "", "")
			require.NoError(t, err)
			cmd.InitDefaultHelpCmd()
			target, _, err := cmd.Find(tc.args)
			require.NoError(t, err)

			err = cmd.PersistentPreRunE(target, nil)
			if tc.wantErr {
				require.ErrorIs(t, err, project.ErrNotInProject)
				require.Contains(t, err.Error(), "clawker project register")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestUserAliasRunsAnywhere(t *testing.T) {
	cmd := newUserAliasCmd("lg", "logs $1")
	require.True(t, cmdutil.RunsAnywhere(cmd))
}

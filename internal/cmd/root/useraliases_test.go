package root

import (
	"errors"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandAlias(t *testing.T) {
	tests := []struct {
		name      string
		expansion string
		args      []string
		want      []string
		wantErr   string
	}{
		{
			name:      "no placeholders appends all args",
			expansion: "container run --rm",
			args:      []string{"-it", "@"},
			want:      []string{"container", "run", "--rm", "-it", "@"},
		},
		{
			name:      "double-digit placeholder is not clobbered by $1",
			expansion: "echoish $1 $10",
			args:      []string{"one", "2", "3", "4", "5", "6", "7", "8", "9", "ten"},
			want:      []string{"echoish", "one", "ten"},
		},
		{
			name:      "positional placeholders",
			expansion: "logs $1 --tail $2",
			args:      []string{"web", "50"},
			want:      []string{"logs", "web", "--tail", "50"},
		},
		{
			name:      "repeated placeholder",
			expansion: "cp $1:src $1:dst",
			args:      []string{"web"},
			want:      []string{"cp", "web:src", "web:dst"},
		},
		{
			name:      "args beyond max placeholder appended",
			expansion: "logs $1",
			args:      []string{"web", "--follow"},
			want:      []string{"logs", "web", "--follow"},
		},
		{
			name:      "quoted expansion tokens survive",
			expansion: `exec $1 sh -c "echo hi"`,
			args:      []string{"web"},
			want:      []string{"exec", "web", "sh", "-c", "echo hi"},
		},
		{
			name:      "not enough args",
			expansion: "logs $1 --tail $2",
			args:      []string{"web"},
			wantErr:   "not enough arguments",
		},
		{
			name:      "unbalanced quote",
			expansion: `run "broken`,
			wantErr:   "invalid expansion",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandAlias(tt.expansion, tt.args)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func newAliasTestFactory(t *testing.T, settingsYAML string) *cmdutil.Factory {
	t.Helper()
	tio, _, _, _ := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		Config: func() (config.Config, error) {
			return configmocks.NewFromString("", settingsYAML), nil
		},
	}
}

// findOwnCommand returns root's direct child with the given name, without
// cobra's prefix matching.
func findOwnCommand(root *cobra.Command, name string) *cobra.Command {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func TestRegisterUserAliases(t *testing.T) {
	t.Run("registers alias with expansion short", func(t *testing.T) {
		f := newAliasTestFactory(t, "aliases:\n  v: version\n")
		root, err := NewCmdRoot(f, "", "")
		require.NoError(t, err)

		cmd := findOwnCommand(root, "v")
		require.NotNil(t, cmd, "alias v should be registered")
		assert.Equal(t, `Alias for "version"`, cmd.Short)
		assert.True(t, cmd.DisableFlagParsing)
		assert.Equal(t, "version", cmd.Annotations[AnnotationAliasExpansion])
	})

	t.Run("existing command wins collision", func(t *testing.T) {
		f := newAliasTestFactory(t, "aliases:\n  run: version\n")
		root, err := NewCmdRoot(f, "", "")
		require.NoError(t, err)

		cmd := findOwnCommand(root, "run")
		require.NotNil(t, cmd)
		assert.Empty(t, cmd.Annotations[AnnotationAliasExpansion], "builtin run must not be replaced")
	})

	t.Run("empty expansion disables alias", func(t *testing.T) {
		f := newAliasTestFactory(t, "aliases:\n  off: \"\"\n")
		root, err := NewCmdRoot(f, "", "")
		require.NoError(t, err)
		assert.Nil(t, findOwnCommand(root, "off"))
	})

	t.Run("multiword name skipped", func(t *testing.T) {
		f := newAliasTestFactory(t, "aliases:\n  \"two words\": version\n")
		root, err := NewCmdRoot(f, "", "")
		require.NoError(t, err)
		assert.Nil(t, findOwnCommand(root, "two words"))
		assert.Nil(t, findOwnCommand(root, "two"))
	})

	t.Run("cyclic chain skipped, valid chain registered", func(t *testing.T) {
		f := newAliasTestFactory(t, "aliases:\n  a: b\n  b: a\n  c: a extra\n  d: version\n  e: d --verbose\n")
		root, err := NewCmdRoot(f, "", "")
		require.NoError(t, err)

		assert.Nil(t, findOwnCommand(root, "a"), "a<->b cycle")
		assert.Nil(t, findOwnCommand(root, "b"), "a<->b cycle")
		assert.Nil(t, findOwnCommand(root, "c"), "c heads into the a<->b cycle")
		assert.NotNil(t, findOwnCommand(root, "d"))
		assert.NotNil(t, findOwnCommand(root, "e"), "chain e->d->version is acyclic")
	})

	t.Run("nil config closure leaves root buildable", func(t *testing.T) {
		f := &cmdutil.Factory{}
		root, err := NewCmdRoot(f, "", "")
		require.NoError(t, err)
		require.NotNil(t, root)
	})

	t.Run("config error leaves root buildable", func(t *testing.T) {
		tio, _, _, _ := iostreams.Test()
		f := &cmdutil.Factory{
			IOStreams: tio,
			Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
			Config:    func() (config.Config, error) { return nil, errors.New("corrupt settings") },
		}
		root, err := NewCmdRoot(f, "", "")
		require.NoError(t, err)
		require.NotNil(t, root)
	})

}

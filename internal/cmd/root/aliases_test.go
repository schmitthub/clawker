package root

import (
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestTopLevelAliases(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	root := &cobra.Command{Use: "clawker"}
	registerAliases(root, f)

	// Verify we're testing all expected aliases
	require.Equal(t, 20, len(topLevelAliases), "expected 20 aliases in topLevelAliases")

	for _, alias := range topLevelAliases {
		// Extract command name from Use field (first word before space)
		name := strings.Split(alias.Use, " ")[0]

		t.Run(name, func(t *testing.T) {
			cmd, _, err := root.Find([]string{name})
			require.NoError(t, err, "alias %q should be found", name)
			require.NotNil(t, cmd, "alias %q should not be nil", name)
			require.Equal(t, name, cmd.Name(), "alias should have correct name")
			require.Equal(t, alias.Use, cmd.Use, "alias should have correct Use field")
			require.NotEmpty(t, cmd.Short, "alias %q should have non-empty Short description", name)
			require.NotNil(t, cmd.RunE, "alias %q should have RunE set", name)

			if alias.Example != "" {
				require.Equal(t, alias.Example, cmd.Example, "alias %q should have custom Example", name)
				// Custom examples should reference top-level command, not subcommand
				require.Contains(t, cmd.Example, "clawker "+name,
					"custom Example should reference top-level command")
			}
		})
	}
}

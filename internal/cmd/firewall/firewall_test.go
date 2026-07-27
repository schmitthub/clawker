package firewall //nolint:testpackage // exercises the parent constructor beside its registration list

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
)

// newTestFactory returns a minimal Factory for parent-command construction.
//
//nolint:exhaustruct // test factory carries only the nouns the parent needs
func newTestFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	ios, _, _, _ := iostreams.Test() //nolint:dogsled // only the streams handle is needed
	return &cmdutil.Factory{
		IOStreams: ios,
		Logger: func() (*logger.Logger, error) {
			return logger.Nop(), nil
		},
	}
}

// TestNewCmdFirewall_RegistersSubcommands pins the registration list: every
// subcommand package must be wired, and nothing else.
func TestNewCmdFirewall_RegistersSubcommands(t *testing.T) {
	cmd := NewCmdFirewall(newTestFactory(t))

	want := []string{
		"up", "down", "status", "list", "add", "remove",
		"reload", "refresh", "prune", "disable", "enable",
		"bypass", "rotate-ca",
	}
	got := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		got = append(got, sub.Name())
	}
	assert.ElementsMatch(t, want, got)
	require.Len(t, got, len(want))
}

func TestNewCmdFirewall_NoServeSubcommand(t *testing.T) {
	cmd := NewCmdFirewall(newTestFactory(t))

	for _, sub := range cmd.Commands() {
		if sub.Name() == "serve" {
			t.Fatalf(
				"firewall command must not register a serve subcommand — no host-side daemon; stack lifecycle is owned by the CP container",
			)
		}
	}
}

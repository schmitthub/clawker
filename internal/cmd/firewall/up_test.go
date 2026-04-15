package firewall

import (
	"context"
	"testing"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	ios, _, _, _ := iostreams.Test()
	return &cmdutil.Factory{
		IOStreams: ios,
		Logger: func() (*logger.Logger, error) {
			return logger.Nop(), nil
		},
	}
}

func TestNewCmdUp_RunFReceivesOptions(t *testing.T) {
	f := newTestFactory(t)

	called := false
	cmd := NewCmdUp(f, func(_ context.Context, opts *UpOptions) error {
		called = true
		require.NotNil(t, opts)
		assert.NotNil(t, opts.IOStreams)
		return nil
	})

	cmd.SetArgs(nil)
	err := cmd.Execute()
	require.NoError(t, err)
	assert.True(t, called)
}

func TestNewCmdFirewall_NoServeSubcommand(t *testing.T) {
	f := newTestFactory(t)
	cmd := NewCmdFirewall(f)

	for _, sub := range cmd.Commands() {
		if sub.Name() == "serve" {
			t.Fatalf("firewall command must not register a serve subcommand — daemon path is dissolved in Branch 2")
		}
	}
}

// compile-time check that the mock package is wired; prevents the import
// from being stripped as unused in small test files that only use the
// factory via runF trapdoor tests.
var _ adminv1.AdminServiceClient = (*stubAdminClient)(nil)

type stubAdminClient struct {
	adminv1.AdminServiceClient
}

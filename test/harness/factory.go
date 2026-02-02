package harness

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
)

// NewTestFactory returns a fully-wired Factory suitable for integration tests
// that execute real Cobra commands (run, create, start, exec). The returned
// factory provides real Docker client construction, the harness config/settings,
// and no-op host proxy closures (tests use SecurityFirewallDisabled).
func NewTestFactory(t *testing.T, h *Harness) (*cmdutil.Factory, *iostreams.TestIOStreams) {
	t.Helper()

	tio := iostreams.NewTestIOStreams()
	cfg := config.NewConfig(func() (string, error) { return h.ProjectDir, nil })
	f := &cmdutil.Factory{
		WorkDir:  func() (string, error) { return h.ProjectDir, nil },
		IOStreams: tio.IOStreams,
		Client: func(ctx context.Context) (*docker.Client, error) {
			return docker.NewClient(ctx, cfg)
		},
		Config: func() *config.Config {
			return cfg
		},
		HostProxy: func() *hostproxy.Manager {
			return hostproxy.NewManager()
		},
		Prompter: func() *prompter.Prompter { return nil },
	}
	return f, tio
}

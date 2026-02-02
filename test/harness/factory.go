package harness

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompts"
)

// NewTestFactory returns a fully-wired Factory suitable for integration tests
// that execute real Cobra commands (run, create, start, exec). The returned
// factory provides real Docker client construction, the harness config/settings,
// and no-op host proxy closures (tests use SecurityFirewallDisabled).
func NewTestFactory(t *testing.T, h *Harness) (*cmdutil.Factory, *iostreams.TestIOStreams) {
	t.Helper()

	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: tio.IOStreams,
		Client: func(ctx context.Context) (*docker.Client, error) {
			return docker.NewClient(ctx)
		},
		Config: func() (*config.Project, error) {
			return h.Config, nil
		},
		Settings: func() (*config.Settings, error) {
			return config.DefaultSettings(), nil
		},
		EnsureHostProxy:         func() error { return nil },
		HostProxyEnvVar:         func() string { return "" },
		SettingsLoader:          func() (*config.SettingsLoader, error) { return nil, nil },
		InvalidateSettingsCache: func() {},
		Prompter:                func() *prompts.Prompter { return nil },
		Resolution: func() *config.Resolution {
			return &config.Resolution{
				ProjectKey: h.Project,
				WorkDir:    h.ProjectDir,
			}
		},
	}
	return f, tio
}

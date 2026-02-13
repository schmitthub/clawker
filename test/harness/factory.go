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
	"github.com/schmitthub/clawker/internal/tui"
)

// NewTestFactory returns a fully-wired Factory suitable for integration tests
// that execute real Cobra commands (run, create, start, exec). The returned
// factory uses the harness config (h.Config) with test-safe defaults applied,
// provides real Docker client construction, and host proxy with t.Cleanup teardown.
//
// The harness config carries the test's project name and settings. applyTestDefaults
// fills in only fields the test hasn't explicitly set:
//   - ClaudeCode strategy "fresh" (skips CopyToVolume — no temp busybox containers)
//   - UseHostAuth false (skips second CopyToVolume for host auth)
//   - Firewall disabled, host proxy disabled (no daemon processes)
//
// The factory's Docker client uses ChownImage set to TestChownImage so that
// any CopyToVolume calls use the locally-built labeled image instead of pulling
// busybox:latest from DockerHub.
//
// Tests that explicitly need copy mode (e.g., test/internals/containerfs_test.go)
// set their own ClaudeCodeConfig directly and don't use NewTestFactory.
func NewTestFactory(t *testing.T, h *Harness) (*cmdutil.Factory, *iostreams.TestIOStreams) {
	t.Helper()

	tio := iostreams.NewTestIOStreams()

	// Use the harness config with test-safe defaults applied
	applyTestDefaults(h.Config)
	cfg := config.NewConfigForTest(h.Config, nil)

	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		Client: func(ctx context.Context) (*docker.Client, error) {
			c, err := docker.NewClient(ctx, cfg, docker.WithLabels(docker.TestLabelConfig(t.Name())))
			if err != nil {
				return nil, err
			}
			c.ChownImage = TestChownImage
			return c, nil
		},
		Config: func() *config.Config {
			return cfg
		},
		HostProxy: func() hostproxy.HostProxyService {
			mgr := hostproxy.NewManager()
			t.Cleanup(func() {
				_ = mgr.StopDaemon()
			})
			return mgr
		},
		Prompter: func() *prompter.Prompter { return nil },
	}
	return f, tio
}

// applyTestDefaults sets test-safe defaults on a project config without
// overriding values the test explicitly set. This ensures tests that need
// specific config (e.g., copy mode for containerfs tests) keep their settings,
// while tests that don't care get safe defaults that minimize Docker resources.
func applyTestDefaults(cfg *config.Project) {
	if cfg.Agent.ClaudeCode == nil {
		hostAuth := false
		cfg.Agent.ClaudeCode = &config.ClaudeCodeConfig{
			Config:      config.ClaudeCodeConfigOptions{Strategy: "fresh"},
			UseHostAuth: &hostAuth,
		}
	}
	if cfg.Security.Firewall == nil {
		cfg.Security.Firewall = &config.FirewallConfig{Enable: false}
	}
	if cfg.Security.EnableHostProxy == nil {
		disabled := false
		cfg.Security.EnableHostProxy = &disabled
	}
}

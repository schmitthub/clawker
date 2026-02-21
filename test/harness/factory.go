package harness

import (
	"context"
	"fmt"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/tui"
	"gopkg.in/yaml.v3"
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
func NewTestFactory(t *testing.T, h *Harness) (*cmdutil.Factory, *iostreamstest.TestIOStreams) {
	t.Helper()

	tio := iostreamstest.New()

	// Use the harness config with test-safe defaults applied
	applyTestDefaults(h.Config)
	cfg := configFromProject(h.Config)

	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		Client: func(ctx context.Context) (*docker.Client, error) {
			c, err := docker.NewClient(ctx, cfg, docker.WithLabels(docker.TestLabelConfig(cfg, t.Name())))
			if err != nil {
				return nil, err
			}
			c.ChownImage = TestChownImage
			return c, nil
		},
		Config: func() (config.Config, error) {
			return cfg, nil
		},
		HostProxy: func() hostproxy.HostProxyService {
			mgr, err := hostproxy.NewManager(cfg)
			if err != nil {
				t.Fatalf("failed to create host proxy manager: %v", err)
			}
			t.Cleanup(func() {
				_ = mgr.StopDaemon()
			})
			return mgr
		},
		Prompter: func() *prompter.Prompter { return nil },
	}
	return f, tio
}

// configFromProject constructs a config.Config mock from a *config.Project schema.
// It marshals the project to YAML, prepends the project name (yaml:"-" field is
// not marshaled), and uses configmocks.NewFromString for a fully-wired mock.
func configFromProject(project *config.Project) config.Config {
	yamlData, err := yaml.Marshal(project)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal project config: %v", err))
	}
	// Name has yaml:"name,omitempty" so yaml.Marshal includes it.
	cfgYAML := string(yamlData)
	return configmocks.NewFromString(cfgYAML)
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

package harness

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/firewall"
	firewallmocks "github.com/schmitthub/clawker/internal/firewall/mocks"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/hostproxy/hostproxytest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
)

// FactoryOptions holds dependency constructor overrides.
// Nil fields use test fakes (configmocks, logger.Nop, dockertest.FakeClient, etc.).
// Set a field to the real constructor (e.g. config.NewConfig) for integration tests.
type FactoryOptions struct {
	Config         func() (config.Config, error)
	Client         func(context.Context, config.Config, *logger.Logger, ...docker.ClientOption) (*docker.Client, error)
	ProjectManager func(config.Config, *logger.Logger, project.GitManagerFactory) (project.ProjectManager, error)
	GitManager     func(string) (*git.GitManager, error)
	HostProxy      func(config.Config, *logger.Logger) (*hostproxy.Manager, error)
	SocketBridge   func(config.Config, *logger.Logger) socketbridge.SocketBridgeManager
	Firewall       func(*docker.Client, config.Config, *logger.Logger) (*firewall.Manager, error)
}

// NewFactory constructs a *cmdutil.Factory with lazy singletons.
// All nouns share a single Config and Logger instance.
// Nil options fields use test fakes. Pass real constructors for integration tests.
func NewFactory(t *testing.T, opts *FactoryOptions) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	if opts == nil {
		opts = &FactoryOptions{}
	}

	tio, in, out, errOut := iostreams.Test()

	f := &cmdutil.Factory{
		Version:   "test",
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
	}

	// --- Config ---
	var (
		cfgOnce sync.Once
		cfg     config.Config
		cfgErr  error
	)
	resolveConfig := func() (config.Config, error) {
		cfgOnce.Do(func() {
			if opts.Config != nil {
				cfg, cfgErr = opts.Config()
			} else {
				cfg = configmocks.NewBlankConfig()
			}
		})
		return cfg, cfgErr
	}
	f.Config = resolveConfig

	// --- Logger ---
	f.Logger = func() (*logger.Logger, error) {
		c, err := resolveConfig()
		if err != nil {
			return nil, err
		}
		dir, err := c.LogsSubdir()
		if err != nil {
			return nil, err
		}
		return logger.New(logger.Options{
			LogsDir: dir,
		})
	}

	// --- Client ---
	var (
		clientOnce sync.Once
		client     *docker.Client
		clientErr  error
	)
	resolveClient := func(ctx context.Context) (*docker.Client, error) {
		clientOnce.Do(func() {
			if opts.Client != nil {
				c, cErr := resolveConfig()
				if cErr != nil {
					clientErr = cErr
					return
				}
				client, clientErr = opts.Client(ctx, c, logger.Nop(),
					docker.WithLabels(docker.TestLabelConfig(c, t.Name())))
			} else {
				c, _ := resolveConfig()
				fake := dockertest.NewFakeClient(c)
				client = fake.Client
			}
		})
		return client, clientErr
	}
	f.Client = resolveClient

	// --- ProjectManager ---
	var (
		pmOnce sync.Once
		pm     project.ProjectManager
		pmErr  error
	)
	f.ProjectManager = func() (project.ProjectManager, error) {
		pmOnce.Do(func() {
			if opts.ProjectManager != nil {
				c, cErr := resolveConfig()
				if cErr != nil {
					pmErr = cErr
					return
				}
				pm, pmErr = opts.ProjectManager(c, logger.Nop(), nil)
			}
		})
		return pm, pmErr
	}

	// --- GitManager ---
	f.GitManager = func() (*git.GitManager, error) {
		if opts.GitManager != nil {
			c, cErr := resolveConfig()
			if cErr != nil {
				return nil, cErr
			}
			root, rErr := c.GetProjectRoot()
			if rErr != nil {
				return nil, rErr
			}
			return opts.GitManager(root)
		}
		return nil, nil
	}

	// --- HostProxy ---
	f.HostProxy = func() hostproxy.HostProxyService {
		if opts.HostProxy != nil {
			c, cErr := resolveConfig()
			if cErr != nil {
				t.Fatalf("harness: config for host proxy: %v", cErr)
			}
			m, mErr := opts.HostProxy(c, logger.Nop())
			if mErr != nil {
				t.Fatalf("harness: host proxy: %v", mErr)
			}
			return m
		}
		return hostproxytest.NewMockManager()
	}

	// --- SocketBridge ---
	f.SocketBridge = func() socketbridge.SocketBridgeManager {
		if opts.SocketBridge != nil {
			c, cErr := resolveConfig()
			if cErr != nil {
				t.Fatalf("harness: config for socket bridge: %v", cErr)
			}
			return opts.SocketBridge(c, logger.Nop())
		}
		return nil
	}

	// --- Firewall ---
	var (
		fwOnce sync.Once
		fwMgr  firewall.FirewallManager
		fwErr  error
	)
	f.Firewall = func(ctx context.Context) (firewall.FirewallManager, error) {
		fwOnce.Do(func() {
			if opts.Firewall != nil {
				cl, clErr := resolveClient(ctx)
				if clErr != nil {
					fwErr = clErr
					return
				}
				c, cErr := resolveConfig()
				if cErr != nil {
					fwErr = cErr
					return
				}
				fwMgr, fwErr = opts.Firewall(cl, c, logger.Nop())
			} else {
				fwMgr = &firewallmocks.FirewallManagerMock{
					IsRunningFunc: func(_ context.Context) bool { return false },
				}
			}
		})
		return fwMgr, fwErr
	}

	// --- Prompter ---
	f.Prompter = func() *prompter.Prompter {
		return prompter.NewPrompter(tio)
	}

	return f, in, out, errOut
}

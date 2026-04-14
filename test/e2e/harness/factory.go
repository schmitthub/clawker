package harness

import (
	"bytes"
	"context"
	"sync"
	"testing"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/controlplane"
	cpmocks "github.com/schmitthub/clawker/internal/controlplane/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/mocks"
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
// Some nil fields use test fakes (configmocks, mocks.FakeClient,
// hostproxytest.MockManager, cpmocks.AdminServiceClientMock). Logger always
// uses logger.New (real file logger). ProjectManager, GitManager, and
// SocketBridge default to nil. Set a field to the real constructor
// (e.g. config.NewConfig) for integration tests.
type FactoryOptions struct {
	Config         func(...config.NewConfigOption) (config.Config, error)
	Client         func(context.Context, config.Config, *logger.Logger, ...docker.ClientOption) (*docker.Client, error)
	ProjectManager func(config.Config, *logger.Logger, project.GitManagerFactory) (project.ProjectManager, error)
	GitManager     func(string) (*git.GitManager, error)
	HostProxy      func(config.Config, *logger.Logger) (*hostproxy.Manager, error)
	SocketBridge   func(config.Config, *logger.Logger) socketbridge.SocketBridgeManager
	// AdminClient optionally provides a real CP AdminService client.
	// When nil the harness wires a no-op AdminServiceClientMock.
	AdminClient func(context.Context, config.Config, *logger.Logger) (adminv1.AdminServiceClient, error)
	// ControlPlane optionally provides a real Manager that drives the
	// host-side CP container lifecycle. When nil the harness wires a
	// no-op ManagerMock (every method returns zero values / nil) so
	// tests that don't exercise the CP verbs never bootstrap a real CP.
	ControlPlane func(config.Config, *logger.Logger) controlplane.Manager
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
				fake := mocks.NewFakeClient(c)
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

	// --- AdminClient ---
	var (
		adminOnce sync.Once
		adminCli  adminv1.AdminServiceClient
		adminErr  error
	)
	f.AdminClient = func(ctx context.Context) (adminv1.AdminServiceClient, error) {
		adminOnce.Do(func() {
			if opts.AdminClient != nil {
				c, cErr := resolveConfig()
				if cErr != nil {
					adminErr = cErr
					return
				}
				adminCli, adminErr = opts.AdminClient(ctx, c, logger.Nop())
			} else {
				adminCli = &cpmocks.AdminServiceClientMock{}
			}
		})
		return adminCli, adminErr
	}

	// --- ControlPlane ---
	var (
		cpOnce sync.Once
		cpMgr  controlplane.Manager
	)
	f.ControlPlane = func() controlplane.Manager {
		cpOnce.Do(func() {
			if opts.ControlPlane != nil {
				c, cErr := resolveConfig()
				if cErr != nil {
					t.Fatalf("harness: config for control plane: %v", cErr)
				}
				cpMgr = opts.ControlPlane(c, logger.Nop())
			} else {
				cpMgr = &cpmocks.ManagerMock{}
			}
		})
		return cpMgr
	}

	// --- Prompter ---
	f.Prompter = func() *prompter.Prompter {
		return prompter.NewPrompter(tio)
	}

	return f, in, out, errOut
}

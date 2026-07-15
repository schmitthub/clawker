package harness

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	adminv1mocks "github.com/schmitthub/clawker/api/admin/v1/mocks"
	"github.com/schmitthub/clawker/controlplane/adminclient"
	"github.com/schmitthub/clawker/controlplane/manager"
	cpbootmocks "github.com/schmitthub/clawker/controlplane/manager/mocks"
	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/bundle/componentcheck"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
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
	"github.com/schmitthub/clawker/internal/state"
	"github.com/schmitthub/clawker/internal/tui"
)

// harnessAdminKeepalive mirrors the production adminClientKeepalive in
// internal/cmd/factory/default.go. Values must stay in lockstep — the
// harness wires a production-identical AdminClient closure so E2E
// exercises the exact keepalive policy the CLI ships with.
var harnessAdminKeepalive = keepalive.ClientParameters{
	Time:                30 * time.Second,
	Timeout:             10 * time.Second,
	PermitWithoutStream: false,
}

// harnessHTTPTimeout mirrors the production httpClientFunc client timeout in
// internal/cmd/factory/default.go.
const harnessHTTPTimeout = 30 * time.Second

// cacheableState mirrors the production helper in internal/cmd/factory/
// default.go. Ready/Connecting/Idle states are safe to reuse; TransientFailure
// and Shutdown require a rebuild.
func cacheableState(s connectivity.State) bool {
	return s == connectivity.Ready || s == connectivity.Connecting || s == connectivity.Idle
}

// FactoryOptions holds dependency constructor overrides.
// Some nil fields use test fakes (configmocks, mocks.FakeClient,
// hostproxytest.MockManager, adminv1mocks.AdminServiceClientMock). Logger always
// uses logger.New (real file logger). ProjectManager, GitManager, and
// SocketBridge default to nil. Set a field to the real constructor
// (e.g. config.NewConfig) for integration tests.
type FactoryOptions struct {
	Config         func(...config.NewConfigOption) (config.Config, error)
	Client         func(context.Context, config.Config, *logger.Logger, ...docker.ClientOption) (*docker.Client, error)
	ProjectManager func(*logger.Logger, project.GitManagerFactory, string, *project.Registry) (project.ProjectManager, error)
	GitManager     func(string) (*git.GitManager, error)
	HostProxy      func(config.Config, *logger.Logger) (*hostproxy.Manager, error)
	SocketBridge   func(config.Config, *logger.Logger) socketbridge.SocketBridgeManager
	// UseRealAdminClient, when true, wires a production-identical
	// AdminClient closure — the exact `adminClientFunc` in
	// internal/cmd/factory/default.go (mutex-guarded cache +
	// cacheableState re-dial on TransientFailure/Shutdown +
	// keepalive params + adminclient.Dial). Pure dial — does NOT
	// bootstrap the CP; lifecycle is owned by container-start and
	// explicit `controlplane up`, so E2E tests fail fast when the
	// CP is down (matching CLI behavior). When false the harness
	// wires a no-op AdminServiceClientMock.
	UseRealAdminClient bool
	// ControlPlane optionally provides a real Manager that drives the
	// host-side CP container lifecycle. When nil the harness wires a
	// no-op ManagerMock (every method returns zero values / nil) so
	// tests that don't exercise the CP verbs never bootstrap a real CP.
	ControlPlane func(config.Config, *logger.Logger) manager.Manager
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
			if opts.Config == nil {
				cfg = configmocks.NewBlankConfig()
				return
			}
			// Mirror configFunc in internal/cmd/factory/default.go: anchor
			// project-layer walk-up at the registry-resolved root. A bare
			// constructor call would DISABLE walk-up entirely — the project
			// .clawker.yaml would silently never load. Outside a registered
			// project the anchor degrades to empty (walk-up off), matching
			// production.
			reg, regErr := f.ProjectRegistry()
			if regErr != nil {
				cfgErr = fmt.Errorf("harness: project registry for config walk-up: %w", regErr)
				return
			}
			root, rootErr := reg.CurrentRoot()
			if rootErr != nil && !errors.Is(rootErr, project.ErrNotInProject) {
				cfgErr = fmt.Errorf("harness: resolving project root for config walk-up: %w", rootErr)
				return
			}
			cfg, cfgErr = opts.Config(config.WithProjectRoot(root))
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

	// --- ProjectRegistry ---
	// Production-default registry (data-dir resolution), shared by
	// ProjectManager, GitManager, and commands — mirrors f.ProjectRegistry
	// wiring in internal/cmd/factory/default.go.
	var (
		regOnce sync.Once
		reg     *project.Registry
		regErr  error
	)
	f.ProjectRegistry = func() (*project.Registry, error) {
		regOnce.Do(func() {
			reg, regErr = project.NewRegistry()
		})
		return reg, regErr
	}

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
				r, rErr := f.ProjectRegistry()
				if rErr != nil {
					pmErr = rErr
					return
				}
				pm, pmErr = opts.ProjectManager(logger.Nop(), nil, c.Project().Name, r)
			}
		})
		return pm, pmErr
	}

	// --- BundleManager ---
	// Mirrors bundleManagerFunc in internal/cmd/factory/default.go. With no
	// ProjectManager option wired, the manager is built without a roots
	// provider — cache GC stays off, fail-closed, per the
	// bundle.WithRegisteredRoots contract.
	var (
		bmOnce sync.Once
		bm     *bundle.Manager
		bmErr  error
	)
	f.BundleManager = func() (*bundle.Manager, error) {
		bmOnce.Do(func() {
			c, cErr := resolveConfig()
			if cErr != nil {
				bmErr = fmt.Errorf("bundle manager: loading config: %w", cErr)
				return
			}
			if opts.ProjectManager == nil {
				bm = bundle.NewManager(c, componentcheck.Validate)
				return
			}
			bm = bundle.NewManager(
				c,
				componentcheck.Validate,
				bundle.WithRegisteredRoots(func(ctx context.Context) ([]string, error) {
					pmgr, mgrErr := f.ProjectManager()
					if mgrErr != nil {
						return nil, fmt.Errorf("bundle GC roots: loading project manager: %w", mgrErr)
					}
					entries, listErr := pmgr.List(ctx)
					if listErr != nil {
						return nil, fmt.Errorf("bundle GC roots: listing registered projects: %w", listErr)
					}
					var roots []string
					for _, e := range entries {
						roots = append(roots, e.Root)
						for _, wt := range e.Worktrees {
							roots = append(roots, wt.Path)
						}
					}
					return roots, nil
				}),
			)
		})
		return bm, bmErr
	}

	// --- GitManager ---
	f.GitManager = func() (*git.GitManager, error) {
		if opts.GitManager != nil {
			r, rErr := f.ProjectRegistry()
			if rErr != nil {
				return nil, rErr
			}
			root, rootErr := r.CurrentRoot()
			if rootErr != nil {
				return nil, rootErr
			}
			return opts.GitManager(root)
		}
		return nil, nil
	}

	// --- HostProxy ---
	f.HostProxy = func() hostproxy.Service {
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
	// Production-identical pure-dial closure. Mirrors adminClientFunc in
	// internal/cmd/factory/default.go — mutex-guarded cache + cacheableState
	// re-dial on TransientFailure/Shutdown + keepalive params. Does NOT
	// bootstrap the CP — that's owned by container-start (and explicit
	// `controlplane up`). Any divergence from production is a bug: E2E
	// must exercise the same code path the CLI ships with.
	if opts.UseRealAdminClient {
		var (
			adminMu     sync.Mutex
			adminConn   *grpc.ClientConn
			adminClient adminv1.AdminServiceClient
		)
		f.AdminClient = func(ctx context.Context) (adminv1.AdminServiceClient, error) {
			adminMu.Lock()
			defer adminMu.Unlock()

			if adminConn != nil {
				if cacheableState(adminConn.GetState()) {
					return adminClient, nil
				}
				_ = adminConn.Close()
				adminConn = nil
				adminClient = nil
			}

			cfg, err := resolveConfig()
			if err != nil {
				return nil, fmt.Errorf("admin client: config: %w", err)
			}

			cp := cfg.Settings().ControlPlane
			newClient, newConn, err := adminclient.Dial(ctx, cp.AdminPort, cp.HydraPublicPort,
				grpc.WithKeepaliveParams(harnessAdminKeepalive),
			)
			if err != nil {
				return nil, fmt.Errorf("admin client: dial: %w", err)
			}
			adminConn = newConn
			adminClient = newClient
			return adminClient, nil
		}
	} else {
		// cleanupTestEnvironment runs `firewall down` through this mock —
		// wire that RPC as a no-op success so teardown never trips a nil moq
		// func. Every other RPC stays nil on purpose: moq panics loudly when
		// a test drives an RPC it didn't opt into (UseRealAdminClient).
		mockAdmin := &adminv1mocks.AdminServiceClientMock{}
		mockAdmin.FirewallRemoveFunc = func(context.Context, *adminv1.FirewallRemoveRequest, ...grpc.CallOption) (*adminv1.FirewallRemoveResult, error) {
			return &adminv1.FirewallRemoveResult{}, nil
		}
		f.AdminClient = func(_ context.Context) (adminv1.AdminServiceClient, error) {
			return mockAdmin, nil
		}
	}

	// --- ControlPlane ---
	var (
		cpOnce sync.Once
		cpMgr  manager.Manager
	)
	f.ControlPlane = func() manager.Manager {
		cpOnce.Do(func() {
			if opts.ControlPlane != nil {
				c, cErr := resolveConfig()
				if cErr != nil {
					t.Fatalf("harness: config for control plane: %v", cErr)
				}
				log, lErr := f.Logger()
				if lErr != nil {
					t.Fatalf("harness: logger for control plane: %v", lErr)
				}
				cpMgr = opts.ControlPlane(c, log)
			} else {
				// Truly no-op: every Manager method wired to return zero
				// values, so tests that never exercise the CP verbs don't
				// panic on a nil moq func (and never bootstrap a real CP).
				cpMgr = &cpbootmocks.ManagerMock{
					EnsureRunningFunc: func(context.Context) error { return nil },
					StopFunc:          func(context.Context) error { return nil },
					IsRunningFunc:     func(context.Context) (bool, error) { return false, nil },
					ProbeHealthzFunc:  func(context.Context) (int, error) { return 0, nil },
				}
			}
		})
		return cpMgr
	}

	// --- CLIState ---
	// Mirrors cliStateFunc in internal/cmd/factory/default.go: state.New is
	// self-contained (resolves under the test's isolated XDG dirs).
	var (
		stateOnce sync.Once
		st        state.StateStore
		stateErr  error
	)
	f.CLIState = func() (state.StateStore, error) {
		stateOnce.Do(func() {
			if st, stateErr = state.New(); stateErr != nil {
				stateErr = fmt.Errorf("harness: cli state: %w", stateErr)
			}
		})
		return st, stateErr
	}

	// --- HttpClient ---
	// Mirrors httpClientFunc in internal/cmd/factory/default.go (30s-timeout
	// stdlib client; error reserved).
	var (
		httpOnce   sync.Once
		httpClient *http.Client
	)
	f.HttpClient = func() (*http.Client, error) {
		httpOnce.Do(func() {
			httpClient = &http.Client{Timeout: harnessHTTPTimeout}
		})
		return httpClient, nil
	}

	// --- Prompter ---
	f.Prompter = func() *prompter.Prompter {
		return prompter.NewPrompter(tio)
	}

	return f, in, out, errOut
}

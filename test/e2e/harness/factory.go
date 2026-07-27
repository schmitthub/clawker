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
	"github.com/schmitthub/clawker/internal/logger/logcfg"
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
// hostproxytest.MockManager, adminv1mocks.AdminServiceClientMock). Logger is
// always production-mirroring (settings-driven, once-cached). ProjectManager
// and GitManager left nil return a descriptive harness error (never
// (nil, nil)); SocketBridge left nil resolves to a nil manager. Set a field
// to the real constructor (e.g. config.NewConfig) for integration tests.
type FactoryOptions struct {
	Config         func(...config.NewConfigOption) (config.Config, error)
	Client         func(context.Context, config.Config, *logger.Logger, ...docker.ClientOption) (*docker.Client, error)
	ProjectManager func(*logger.Logger, project.GitManagerFactory, string, project.Registry) (project.ProjectManager, error)
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
	// UseRealControlPlane, when true, wires the production Manager
	// exactly as controlPlaneFunc in internal/cmd/factory/default.go
	// does — manager.NewManager(f.Client, f.Config, f.Logger) — so the
	// CP manager observes the same cached Docker singleton and settings
	// snapshot as the rest of the harness Factory. When false the
	// harness wires a no-op ManagerMock (every method returns zero
	// values / nil) so tests that don't exercise the CP verbs never
	// bootstrap a real CP.
	UseRealControlPlane bool
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
	// Once-cached like loggerLazy in internal/cmd/factory/default.go, and
	// the same settings→logger assembly the CLI ships with (logcfg.New:
	// file switch, rotation knobs, OTEL export). The shared instance is
	// what the failure dump of clawker.log depends on.
	var (
		logOnce sync.Once
		log     *logger.Logger
		logErr  error
	)
	f.Logger = func() (*logger.Logger, error) {
		logOnce.Do(func() {
			var c config.Config
			c, logErr = resolveConfig()
			if logErr != nil {
				return
			}
			log, logErr = logcfg.New(c)
			if logErr != nil {
				logErr = fmt.Errorf("harness: logger: %w", logErr)
			}
		})
		return log, logErr
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
				l, lErr := f.Logger()
				if lErr != nil {
					clientErr = fmt.Errorf("harness: logger for docker client: %w", lErr)
					return
				}
				client, clientErr = opts.Client(ctx, c, l,
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
		reg     project.Registry
		regErr  error
	)
	f.ProjectRegistry = func() (project.Registry, error) {
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
			if opts.ProjectManager == nil {
				// Production always returns a manager or a real error —
				// (nil, nil) is a state the CLI can never produce, and a
				// command that trips it should fail loudly, not nil-deref.
				pmErr = errors.New("harness: ProjectManager not wired — set FactoryOptions.ProjectManager")
				return
			}
			c, cErr := resolveConfig()
			if cErr != nil {
				pmErr = cErr
				return
			}
			l, lErr := f.Logger()
			if lErr != nil {
				pmErr = fmt.Errorf("harness: logger for project manager: %w", lErr)
				return
			}
			r, rErr := f.ProjectRegistry()
			if rErr != nil {
				pmErr = rErr
				return
			}
			pm, pmErr = opts.ProjectManager(l, nil, c.ProjectName(), r)
		})
		return pm, pmErr
	}

	// --- BundleManager ---
	// Mirrors bundleManagerFunc in internal/cmd/factory/default.go: the
	// roots provider is attached unconditionally, exactly as production
	// does. With no ProjectManager option wired the provider surfaces the
	// harness's descriptive error on the first GC pass — still fail-closed
	// (an errored roots union never collects), but loud instead of
	// silently disabling GC.
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
		if opts.GitManager == nil {
			// Same contract as ProjectManager: never (nil, nil).
			return nil, errors.New("harness: GitManager not wired — set FactoryOptions.GitManager")
		}
		r, rErr := f.ProjectRegistry()
		if rErr != nil {
			return nil, fmt.Errorf("harness: project registry: %w", rErr)
		}
		root, rootErr := r.CurrentRoot()
		if rootErr != nil {
			return nil, fmt.Errorf("harness: resolving project root: %w", rootErr)
		}
		return opts.GitManager(root)
	}

	// --- HostProxy ---
	// Once-cached like hostProxyFunc in internal/cmd/factory/default.go —
	// the mock manager is stateful (EnsureRunning flips Running), so a
	// fresh instance per call would discard the transition and hide
	// created-vs-started ordering bugs. Panics mirror production: these
	// closures can resolve off the test goroutine, where t.Fatalf only
	// runtime.Goexits that goroutine and hangs the test.
	var (
		hpOnce sync.Once
		hpSvc  hostproxy.Service
	)
	f.HostProxy = func() hostproxy.Service {
		//nolint:forbidigo // the Factory noun returns no error; production hostProxyFunc panics identically, and t.Fatalf off the test goroutine would hang instead of fail
		hpOnce.Do(func() {
			if opts.HostProxy == nil {
				hpSvc = hostproxytest.NewMockManager()
				return
			}
			c, cErr := resolveConfig()
			if cErr != nil {
				panic(fmt.Errorf("harness: config for host proxy: %w", cErr))
			}
			l, lErr := f.Logger()
			if lErr != nil {
				panic(fmt.Errorf("harness: logger for host proxy: %w", lErr))
			}
			m, mErr := opts.HostProxy(c, l)
			if mErr != nil {
				panic(fmt.Errorf("harness: host proxy: %w", mErr))
			}
			hpSvc = m
		})
		return hpSvc
	}

	// --- SocketBridge ---
	// Once-cached like socketBridgeFunc in internal/cmd/factory/default.go.
	var (
		sbOnce sync.Once
		sbMgr  socketbridge.SocketBridgeManager
	)
	f.SocketBridge = func() socketbridge.SocketBridgeManager {
		//nolint:forbidigo // same contract as HostProxy above: no error return, production panics identically
		sbOnce.Do(func() {
			if opts.SocketBridge == nil {
				return
			}
			c, cErr := resolveConfig()
			if cErr != nil {
				panic(fmt.Errorf("harness: config for socket bridge: %w", cErr))
			}
			l, lErr := f.Logger()
			if lErr != nil {
				panic(fmt.Errorf("harness: logger for socket bridge: %w", lErr))
			}
			sbMgr = opts.SocketBridge(c, l)
		})
		return sbMgr
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

			cp := cfg.ControlPlaneSettings()
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
	// Real branch is byte-for-byte controlPlaneFunc in
	// internal/cmd/factory/default.go: the Manager shares the Factory's
	// Client/Config/Logger closures, so it observes the same cached
	// Docker singleton (with test labels) as every other noun.
	var (
		cpOnce sync.Once
		cpMgr  manager.Manager
	)
	f.ControlPlane = func() manager.Manager {
		cpOnce.Do(func() {
			if opts.UseRealControlPlane {
				cpMgr = manager.NewManager(f.Client, f.Config, f.Logger)
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

package factory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/controlplane/adminclient"
	"github.com/schmitthub/clawker/controlplane/manager"
	"github.com/schmitthub/clawker/internal/bundle"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/state"
	"github.com/schmitthub/clawker/internal/tui"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"
)

// adminClientKeepalive is the keepalive policy the CLI applies to the
// long-lived CP AdminService connection. Time is how long an idle
// connection sits before the client pings; Timeout is how long we
// wait for the ping ack before declaring the path dead;
// PermitWithoutStream is false because the CLI only pings when an
// RPC is in flight.
var adminClientKeepalive = keepalive.ClientParameters{
	Time:                30 * time.Second,
	Timeout:             10 * time.Second,
	PermitWithoutStream: false,
}

// cacheableState reports whether the closure should return the cached
// AdminServiceClient without rebuilding. grpc.ClientConn auto-reconnects
// with backoff in Idle/Connecting, and Ready is obviously healthy —
// only TransientFailure (repeated backoff failures) and Shutdown
// (closed) warrant tearing down and rebuilding the conn.
func cacheableState(s connectivity.State) bool {
	return s == connectivity.Ready || s == connectivity.Connecting || s == connectivity.Idle
}

// New creates a fully-wired Factory with lazy-initialized dependency closures.
// Called exactly once at the CLI entry point (internal/clawker/cmd.go).
// Tests should NOT import this package — construct &cmdutil.Factory{} directly.
func New(version string) *cmdutil.Factory {
	f := &cmdutil.Factory{
		Version:         version,
		ProjectRegistry: projectRegistryFunc(), // no dependencies; sole constructor of registry storage
		CLIState:        cliStateFunc(),        // no dependencies; state.New is self-contained
	}

	f.Config = configFunc(f)                 // depends on ProjectRegistry (walk-up anchor)
	f.ProjectManager = projectManagerFunc(f) // depends on Config (name override) + Logger + ProjectRegistry
	f.Logger = loggerLazy(f)                 // depends on Config
	f.HostProxy = hostProxyFunc(f)           // depends on Config
	f.SocketBridge = socketBridgeFunc(f)     // depends on Config
	f.IOStreams = ioStreams()                // TTY/color/CI detection
	f.TUI = tuiFunc(f)                       // needs IOStreams
	f.Client = clientFunc(f)                 // depends on Config
	f.GitManager = gitManagerFunc(f)         // anchors at the registry-resolved project root, no Config dependency
	f.Prompter = prompterFunc(f)
	f.AdminClient = adminClientFunc(f)     // depends on Config
	f.ControlPlane = controlPlaneFunc(f)   // depends on Config, Logger, Client
	f.HttpClient = httpClientFunc()        // stdlib *http.Client; tests substitute via custom RoundTripper
	f.BundleManager = bundleManagerFunc(f) // depends on Config

	return f
}

// bundleManagerFunc returns a lazy constructor for the bundle-model facade. It
// resolves the loaded config once (sync.Once-cached) and binds a Manager to it;
// the Manager's resolver reads the embedded floor, the loose convention dirs,
// and the host bundle cache on demand. Depends only on Config.
func bundleManagerFunc(f *cmdutil.Factory) func() (*bundle.Manager, error) {
	var (
		once sync.Once
		mgr  *bundle.Manager
		err  error
	)
	return func() (*bundle.Manager, error) {
		once.Do(func() {
			var cfg config.Config
			cfg, err = f.Config()
			if err != nil {
				err = fmt.Errorf("bundle manager: loading config: %w", err)
				return
			}
			mgr = bundle.NewManager(cfg)
		})
		return mgr, err
	}
}

// cliStateFunc returns a sync.Once-cached lazy closure that yields the CLI
// runtime-state facade (state.New() — the update-check cache + changelog cursor).
// It takes no dependencies; the error is real, since state.New() can fail on a
// disk or migration error.
func cliStateFunc() func() (state.StateStore, error) {
	var (
		once sync.Once
		st   state.StateStore
		err  error
	)
	return func() (state.StateStore, error) {
		once.Do(func() {
			st, err = state.New()
		})
		return st, err
	}
}

// httpClientFunc returns a lazy closure that yields a shared *http.Client
// for outbound HTTP from the CLI. First consumer:
// bundler.ResolveHarnessVersion for registry lookups during harness
// version resolution. Matches cli/cli's HttpClient factory
// shape — *http.Client is the noun, http.RoundTripper is the stdlib mock
// seam.
//
// A 30s timeout is applied at the client level so a hung npm response
// doesn't stall builds indefinitely. Default Transport (net/http) handles
// connection pooling, keep-alives, and standard env-var proxy resolution.
//
// The (*http.Client, error) signature is intentional even though constructing a
// plain client is infallible today: the error is RESERVED so a future fallible
// transport (custom CA bundle, proxy resolution, an auth round-tripper) can
// surface a failure without rippling a signature change through every caller.
// Until then it is always nil.
func httpClientFunc() func() (*http.Client, error) {
	var (
		once   sync.Once
		client *http.Client
		err    error // reserved; see doc — currently never assigned
	)
	return func() (*http.Client, error) {
		once.Do(func() {
			client = &http.Client{Timeout: 30 * time.Second}
		})
		return client, err
	}
}

// loggerLazy returns a lazy closure that creates the Logger once.
func loggerLazy(f *cmdutil.Factory) func() (*logger.Logger, error) {
	var (
		once sync.Once
		log  *logger.Logger
		err  error
	)
	return func() (*logger.Logger, error) {
		once.Do(func() {
			log, err = newLogger(f)
		})
		return log, err
	}
}

func newLogger(f *cmdutil.Factory) (*logger.Logger, error) {
	cfg, err := f.Config()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	settings := cfg.SettingsStore().Read()
	loggingCfg := settings.Logging

	// File logging is on by default for user diagnostics.
	// Only skip if explicitly disabled via settings.yaml.
	if loggingCfg.FileEnabled != nil && !*loggingCfg.FileEnabled {
		return logger.Nop(), nil
	}

	logsDir, err := cfg.LogsSubdir()
	if err != nil {
		return nil, fmt.Errorf("failed to get logs subdir: %w", err)
	}
	monitoringCfg := settings.Monitoring

	// Build OTEL config from settings if enabled. CLI runs on the host and
	// reaches the collector via its host-published OTLP/gRPC port —
	// logger.New uses otlploggrpc (see internal/logger/logger.go::
	// newOtelProvider). Dialing the OtelCollectorPort (HTTP, 4318) with
	// a gRPC exporter returns 415 Unsupported Media Type and silently
	// drops every record; use OtelGRPCPort (4317) instead.
	var otelCfg *logger.OtelOptions
	if loggingCfg.Otel.Enabled != nil && *loggingCfg.Otel.Enabled {
		endpoint := fmt.Sprintf("%s:%d", monitoringCfg.OtelCollectorHost, monitoringCfg.OtelGRPCPort)
		otelCfg = &logger.OtelOptions{
			Endpoint:       endpoint,
			Insecure:       true,
			Timeout:        time.Duration(loggingCfg.Otel.TimeoutSeconds) * time.Second,
			MaxQueueSize:   loggingCfg.Otel.MaxQueueSize,
			ExportInterval: time.Duration(loggingCfg.Otel.ExportIntervalSeconds) * time.Second,
			ServiceName:    "clawker-cli",
		}
	}

	compress := true
	if loggingCfg.Compress != nil {
		compress = *loggingCfg.Compress
	}
	opts := logger.Options{
		LogsDir: logsDir,

		MaxSizeMB:  loggingCfg.MaxSizeMB,
		MaxAgeDays: loggingCfg.MaxAgeDays,
		MaxBackups: loggingCfg.MaxBackups,
		Compress:   compress,
		Otel:       otelCfg,
	}
	l, err := logger.New(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize logger: %w", err)
	}
	return l, nil

}

func projectManagerFunc(f *cmdutil.Factory) func() (project.ProjectManager, error) {
	var (
		once sync.Once
		svc  project.ProjectManager
		err  error
	)

	return func() (project.ProjectManager, error) {
		once.Do(func() {
			cfg, cfgErr := f.Config()
			if cfgErr != nil {
				err = fmt.Errorf("failed to get config: %w", cfgErr)
				return
			}
			log, logErr := f.Logger()
			if logErr != nil {
				err = fmt.Errorf("failed to get logger: %w", logErr)
				return
			}
			reg, regErr := f.ProjectRegistry()
			if regErr != nil {
				err = fmt.Errorf("loading project registry: %w", regErr)
				return
			}
			// The clawker.yaml `name:` override is config-owned; resolve it here
			// and pass it down as a primitive so PM stays config-free. This is a
			// one-way edge (PM reads config); config never reads PM — its anchor
			// comes from the shared registry facade.
			svc, err = project.NewProjectManager(log, nil, cfg.Project().Name, reg)
		})
		return svc, err
	}
}

func tuiFunc(f *cmdutil.Factory) *tui.TUI {
	ios := f.IOStreams
	return tui.NewTUI(ios)
}

// ioStreams creates an IOStreams with TTY/color/CI detection.
func ioStreams() *iostreams.IOStreams {
	ios := iostreams.System()
	return ios
}

// clientFunc returns a lazy closure that creates a Docker client once.
func clientFunc(f *cmdutil.Factory) func(context.Context) (*docker.Client, error) {
	var (
		once      sync.Once
		client    *docker.Client
		clientErr error
	)
	return func(ctx context.Context) (*docker.Client, error) {
		once.Do(func() {
			cfg, err := f.Config()
			if err != nil {
				clientErr = fmt.Errorf("failed to get config: %w", err)
				return
			}
			log, logErr := f.Logger()
			if logErr != nil {
				clientErr = fmt.Errorf("failed to get logger: %w", logErr)
				return
			}
			client, clientErr = docker.NewClient(ctx, cfg, log)
		})
		return client, clientErr
	}
}

// adminClientFunc returns a lazy closure that dials the CP AdminService.
// Pure dial — does NOT bootstrap the CP container. The CP is brought up
// only by flows that need it (agent container start; explicit `clawker
// controlplane up` / `clawker firewall up`). Admin commands invoked when
// the CP is down fail fast — `controlplane down` stays down.
//
// First call dials with mTLS + OAuth2 JWT; subsequent calls return the
// cached client unless the gRPC connection has entered TransientFailure
// or Shutdown, in which case the closure rebuilds.
//
// Keepalive params (Time: 30s, Timeout: 10s, PermitWithoutStream: false)
// let long-running CLI processes (monitor, bypass dashboard) detect
// dead paths before the next RPC hangs.
func adminClientFunc(f *cmdutil.Factory) func(context.Context) (adminv1.AdminServiceClient, error) {
	var (
		mu     sync.Mutex
		conn   *grpc.ClientConn
		client adminv1.AdminServiceClient
	)
	return func(ctx context.Context) (adminv1.AdminServiceClient, error) {
		mu.Lock()
		defer mu.Unlock()

		if conn != nil {
			if cacheableState(conn.GetState()) {
				return client, nil
			}
			_ = conn.Close()
			conn = nil
			client = nil
		}

		cfg, err := f.Config()
		if err != nil {
			return nil, fmt.Errorf("admin client: config: %w", err)
		}

		cp := cfg.Settings().ControlPlane
		newClient, newConn, err := adminclient.Dial(ctx, cp.AdminPort, cp.HydraPublicPort,
			grpc.WithKeepaliveParams(adminClientKeepalive),
		)
		if err != nil {
			return nil, fmt.Errorf("admin client: dial: %w", err)
		}
		conn = newConn
		client = newClient
		return client, nil
	}
}

// hostProxyFunc returns a lazy closure that creates a host proxy manager once.
func hostProxyFunc(f *cmdutil.Factory) func() hostproxy.Service {
	var (
		once sync.Once
		svc  hostproxy.Service
	)
	return func() hostproxy.Service {
		once.Do(func() {
			cfg, err := f.Config()
			if err != nil {
				panic(fmt.Errorf("failed to get config for host proxy manager: %w", err))
			}
			log, err := f.Logger()
			if err != nil {
				panic(fmt.Errorf("failed to get logger for host proxy manager: %w", err))
			}
			m, mErr := hostproxy.NewManager(cfg, log)
			if mErr != nil {
				panic(fmt.Errorf("failed to create host proxy manager: %w", mErr))
			}
			svc = m
		})
		return svc
	}
}

// socketBridgeFunc returns a lazy closure that creates a socket bridge manager once.
func socketBridgeFunc(f *cmdutil.Factory) func() socketbridge.SocketBridgeManager {
	var (
		once    sync.Once
		manager socketbridge.SocketBridgeManager
	)
	return func() socketbridge.SocketBridgeManager {
		once.Do(func() {
			cfg, err := f.Config()
			if err != nil {
				panic(fmt.Errorf("failed to get config for socket bridge manager: %w", err))
			}
			log, err := f.Logger()
			if err != nil {
				panic(fmt.Errorf("failed to get logger for socket bridge manager: %w", err))
			}
			manager = socketbridge.NewManager(cfg, log)
		})
		return manager
	}
}

// projectRegistryFunc returns a lazy closure that constructs the project
// registry facade once. This is the only production constructor of registry
// storage — config walk-up anchoring, the git manager, the project manager,
// and commands all share the instance through f.ProjectRegistry.
func projectRegistryFunc() func() (*project.Registry, error) {
	var (
		once sync.Once
		reg  *project.Registry
		err  error
	)
	return func() (*project.Registry, error) {
		once.Do(func() {
			reg, err = project.NewRegistry()
		})
		return reg, err
	}
}

// configFunc returns a lazy closure that creates a Config gateway once.
// The project root that bounds project-config walk-up is resolved here via
// the shared registry facade (f.ProjectRegistry — registry read through
// storage, no config, no project manager) and handed to config as a plain
// anchor path. Empty anchor (CWD not within a registered project) disables
// walk-up. config never reaches back to the project manager, so the
// dependency is one-way.
func configFunc(f *cmdutil.Factory) func() (config.Config, error) {
	var cachedConfig config.Config
	var configError error
	return func() (config.Config, error) {
		if cachedConfig != nil || configError != nil {
			return cachedConfig, configError
		}
		reg, err := f.ProjectRegistry()
		if err != nil {
			configError = fmt.Errorf("loading project registry for config walk-up: %w", err)
			return nil, configError
		}
		// CurrentRoot returns ErrNotInProject when CWD is not within a
		// registered project — a normal condition (global-scope agents have no
		// project). That degrades to an empty anchor, which disables walk-up.
		// Any other error is unexpected and is surfaced rather than swallowed.
		root, err := reg.CurrentRoot()
		if err != nil && !errors.Is(err, project.ErrNotInProject) {
			configError = fmt.Errorf("resolving project root for config walk-up: %w", err)
			return nil, configError
		}
		cachedConfig, configError = config.NewConfig(config.WithProjectRoot(root))
		return cachedConfig, configError
	}
}

// prompterFunc returns a closure that creates a new Prompter.
func prompterFunc(f *cmdutil.Factory) func() *prompter.Prompter {
	return func() *prompter.Prompter {
		return prompter.NewPrompter(f.IOStreams)
	}
}

// gitManagerFunc returns a lazy closure that creates a GitManager once.
// Uses the project root resolved through the shared registry facade as the
// git repository path. Returns error if not in a registered project or not a
// git repository.
func gitManagerFunc(f *cmdutil.Factory) func() (*git.GitManager, error) {
	var (
		once   sync.Once
		mgr    *git.GitManager
		mgrErr error
	)
	return func() (*git.GitManager, error) {
		once.Do(func() {
			reg, err := f.ProjectRegistry()
			if err != nil {
				mgrErr = fmt.Errorf("loading project registry: %w", err)
				return
			}
			projectRoot, err := reg.CurrentRoot()
			if err != nil {
				mgrErr = fmt.Errorf("failed to get project root: %w", err)
				return
			}
			mgr, mgrErr = git.NewGitManager(projectRoot)
		})
		return mgr, mgrErr
	}
}

// controlPlaneFunc returns a lazy closure that constructs a
// manager.Manager once. The Manager shares the Factory's Client,
// Config, and Logger closures so every caller — `clawker controlplane
// up/down/status` and any future break-glass verb — observes the same
// cached Docker singleton and settings snapshot as the rest of the CLI.
func controlPlaneFunc(f *cmdutil.Factory) func() manager.Manager {
	var (
		once sync.Once
		mgr  manager.Manager
	)
	return func() manager.Manager {
		once.Do(func() {
			mgr = manager.NewManager(f.Client, f.Config, f.Logger)
		})
		return mgr
	}
}

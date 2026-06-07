package factory

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/controlplane/adminclient"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"
)

// adminClientKeepalive is the keepalive policy the CLI applies to the
// long-lived CP AdminService connection. Values match the CP
// server-side config in internal/controlplane/server.go. Time is how
// long an idle connection sits before the client pings; Timeout is
// how long we wait for the ping ack before declaring the path dead;
// PermitWithoutStream is false because the CLI only pings when an
// RPC is in flight (CP server enforces the same via
// MinTime/PermitWithoutStream).
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
		Version: version,
		Config:  configFunc(),
	}

	f.Logger = loggerLazy(f)                 // depends on Config
	f.HostProxy = hostProxyFunc(f)           // depends on Config
	f.SocketBridge = socketBridgeFunc(f)     // depends on Config
	f.IOStreams = ioStreams()                // TTY/color/CI detection
	f.TUI = tuiFunc(f)                       // needs IOStreams
	f.ProjectManager = projectManagerFunc(f) // depends on Config
	f.Client = clientFunc(f)                 // depends on Config
	f.GitManager = gitManagerFunc(f)         // depends on Config
	f.Prompter = prompterFunc(f)
	f.AdminClient = adminClientFunc(f)   // depends on Config
	f.ControlPlane = controlPlaneFunc(f) // depends on Config, Logger, Client
	f.HttpClient = httpClientFunc()      // stdlib *http.Client; tests substitute via custom RoundTripper

	return f
}

// httpClientFunc returns a lazy closure that yields a shared *http.Client
// for outbound HTTP from the CLI. First consumer:
// bundler.ResolveLatestClaudeCodeVersion for npm registry lookups during
// Claude Code version resolution. Matches cli/cli's HttpClient factory
// shape — *http.Client is the noun, http.RoundTripper is the stdlib mock
// seam.
//
// A 30s timeout is applied at the client level so a hung npm response
// doesn't stall builds indefinitely. Default Transport (net/http) handles
// connection pooling, keep-alives, and standard env-var proxy resolution.
func httpClientFunc() func() *http.Client {
	var (
		once   sync.Once
		client *http.Client
	)
	return func() *http.Client {
		once.Do(func() {
			client = &http.Client{Timeout: 30 * time.Second}
		})
		return client
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
			svc, err = project.NewProjectManager(cfg, log, nil)
		})
		return svc, err
	}
}

func tuiFunc(f *cmdutil.Factory) *tui.TUI {
	ios := f.IOStreams
	return tui.NewTUI(ios)
}

// ioStreams creates an IOStreams with TTY/color/CI detection and initializes the logger.
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
// dead paths before the next RPC hangs. Values match the CP server-side
// configuration.
func adminClientFunc(f *cmdutil.Factory) func(context.Context) (adminv1.AdminServiceClient, error) {
	var (
		mu           sync.Mutex
		conn         *grpc.ClientConn
		client       adminv1.AdminServiceClient
		loggerWarned bool
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
		// Logger is best-effort: a logger failure must not block the admin
		// dial, but when present it carries the clock-skew degrade lines that
		// tie a later "Token used before issued" 500 back to root cause. If the
		// logger itself is broken those breadcrumbs vanish into a Nop, so warn
		// once on stderr — an operator must know logging is down rather than
		// silently getting a degrade-line-free run.
		log, logErr := f.Logger()
		if logErr != nil && !loggerWarned {
			loggerWarned = true
			if f.IOStreams != nil {
				cs := f.IOStreams.ColorScheme()
				fmt.Fprintf(f.IOStreams.ErrOut, "%s logger init failed; clock-skew diagnostics will be unavailable: %v\n",
					cs.WarningIcon(), logErr)
			}
		}
		newClient, newConn, err := adminclient.Dial(ctx, cp.AdminPort, cp.HydraPublicPort, log,
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
func hostProxyFunc(f *cmdutil.Factory) func() hostproxy.HostProxyService {
	var (
		once    sync.Once
		manager hostproxy.HostProxyService
	)
	return func() hostproxy.HostProxyService {
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
			manager = m
		})
		return manager
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

// configFunc returns a lazy closure that creates a Config gateway once.
// Config uses os.Getwd internally for project resolution.
func configFunc() func() (config.Config, error) {
	var cachedConfig config.Config
	var configError error
	return func() (config.Config, error) {
		if cachedConfig != nil || configError != nil {
			return cachedConfig, configError
		}
		cachedConfig, configError = config.NewConfig()
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
// Uses project root from Config.ProjectCfg.RootDir() as the git repository path.
// Returns error if not in a registered project or not a git repository.
func gitManagerFunc(f *cmdutil.Factory) func() (*git.GitManager, error) {
	var (
		once   sync.Once
		mgr    *git.GitManager
		mgrErr error
	)
	return func() (*git.GitManager, error) {
		once.Do(func() {
			cfg, err := f.Config()
			if err != nil {
				mgrErr = fmt.Errorf("failed to get config: %w", err)
				return
			}
			projectRoot, err := cfg.GetProjectRoot()
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
// cpboot.Manager once. The Manager shares the Factory's Client,
// Config, and Logger closures so every caller — `clawker controlplane
// up/down/status` and any future break-glass verb — observes the same
// cached Docker singleton and settings snapshot as the rest of the CLI.
func controlPlaneFunc(f *cmdutil.Factory) func() cpboot.Manager {
	var (
		once sync.Once
		mgr  cpboot.Manager
	)
	return func() cpboot.Manager {
		once.Do(func() {
			mgr = cpboot.NewManager(f.Client, f.Config, f.Logger)
		})
		return mgr
	}
}

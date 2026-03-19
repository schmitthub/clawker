package factory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	mobyclient "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/firewall"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
)

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
	f.Firewall = firewallFunc(f) // depends on Config, Logger, Client

	return f
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

	// Build OTEL config from settings if enabled
	var otelCfg *logger.OtelOptions
	if loggingCfg.Otel.Enabled != nil && *loggingCfg.Otel.Enabled {
		endpoint := monitoringCfg.OtelCollectorEndpoint
		endpoint = strings.TrimSpace(endpoint)
		if i := strings.Index(endpoint, "://"); i >= 0 {
			endpoint = endpoint[i+3:]
		}
		if endpoint != "" {
			otelCfg = &logger.OtelOptions{
				Endpoint:       endpoint,
				Insecure:       true,
				Timeout:        time.Duration(loggingCfg.Otel.TimeoutSeconds) * time.Second,
				MaxQueueSize:   loggingCfg.Otel.MaxQueueSize,
				ExportInterval: time.Duration(loggingCfg.Otel.ExportIntervalSeconds) * time.Second,
			}
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

// firewallFunc returns a lazy closure that creates a FirewallManager once.
func firewallFunc(f *cmdutil.Factory) func(context.Context) (firewall.FirewallManager, error) {
	var (
		once sync.Once
		mgr  firewall.FirewallManager
		err  error
	)
	return func(ctx context.Context) (firewall.FirewallManager, error) {
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
			dockerClient, clientErr := mobyclient.New(mobyclient.FromEnv)
			if clientErr != nil {
				err = fmt.Errorf("failed to get docker client: %w", clientErr)
				return
			}
			mgr, err = firewall.NewManager(dockerClient, cfg, log)
			if err != nil {
				err = fmt.Errorf("failed to create firewall manager: %w", err)
				return
			}
		})
		return mgr, err
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

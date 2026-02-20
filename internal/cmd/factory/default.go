package factory

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

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

	f.HostProxy = hostProxyFunc(f)       // depends on Config
	f.SocketBridge = socketBridgeFunc(f)  // depends on Config
	f.IOStreams = ioStreams(f)             // needs f.Config() for logger settings
	f.TUI = tuiFunc(f)                    // needs IOStreams
	f.ProjectManager = projectManagerFunc(f) // depends on Config + IOStreams
	f.Client = clientFunc(f)              // depends on Config
	f.GitManager = gitManagerFunc(f)      // depends on Config
	f.Prompter = prompterFunc(f)

	return f
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
			svc = project.NewProjectManager(cfg)
		})
		return svc, err
	}
}

func tuiFunc(f *cmdutil.Factory) *tui.TUI {
	ios := f.IOStreams
	return tui.NewTUI(ios)
}

// ioStreams creates an IOStreams with TTY/color/CI detection and initializes the logger.
func ioStreams(f *cmdutil.Factory) *iostreams.IOStreams {
	ios := iostreams.System()

	cfg, err := f.Config()
	if err != nil {
		return ios
	}

	settings := cfg.Settings()
	loggingCfg := settings.Logging
	monitoringCfg := settings.Monitoring

	// Build OTEL config from settings if enabled
	var otelCfg *logger.OtelLogConfig
	if loggingCfg.Otel.Enabled != nil && *loggingCfg.Otel.Enabled {
		otelCfg = &logger.OtelLogConfig{
			Endpoint:       monitoringCfg.OtelCollectorEndpoint,
			Insecure:       true,
			Timeout:        time.Duration(loggingCfg.Otel.TimeoutSeconds) * time.Second,
			MaxQueueSize:   loggingCfg.Otel.MaxQueueSize,
			ExportInterval: time.Duration(loggingCfg.Otel.ExportIntervalSeconds) * time.Second,
		}
		if otelCfg.Endpoint == "" && monitoringCfg.OtelCollectorHost != "" && monitoringCfg.OtelCollectorPort > 0 {
			otelCfg.Endpoint = fmt.Sprintf("http://%s:%d", monitoringCfg.OtelCollectorHost, monitoringCfg.OtelCollectorPort)
		}
	}

	logsDir := filepath.Join(config.ConfigDir(), "logs")
	if err := logger.NewLogger(&logger.Options{
		LogsDir: logsDir,
		FileConfig: &logger.LoggingConfig{
			FileEnabled: loggingCfg.FileEnabled,
			MaxSizeMB:   loggingCfg.MaxSizeMB,
			MaxAgeDays:  loggingCfg.MaxAgeDays,
			MaxBackups:  loggingCfg.MaxBackups,
			Compress:    loggingCfg.Compress,
		},
		OtelConfig: otelCfg,
	}); err != nil {
		logger.Init()
	}

	ios.Logger = &logger.Log
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
			client, clientErr = docker.NewClient(ctx, cfg)
			if clientErr == nil {
				docker.WireBuildKit(client)
			}
		})
		return client, clientErr
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
				logger.Warn().Err(err).Msg("failed to get config for host proxy manager, using defaults")
				cfg = nil
			}
			m, mErr := hostproxy.NewManager(cfg)
			if mErr != nil {
				logger.Error().Err(mErr).Msg("failed to create host proxy manager")
				return
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
				logger.Warn().Err(err).Msg("failed to get config for socket bridge manager, using defaults")
				cfg = nil
			}
			manager = socketbridge.NewManager(cfg)
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

package factory

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
)

// New creates a fully-wired Factory with lazy-initialized dependency closures.
// Called exactly once at the CLI entry point (internal/clawker/cmd.go).
// Tests should NOT import this package — construct &cmdutil.Factory{} directly.
func New(version string) *cmdutil.Factory {
	f := &cmdutil.Factory{
		Version:      version,
		Config:       configFunc(),
		HostProxy:    hostProxyFunc(),
		SocketBridge: socketBridgeFunc(),
	}

	f.IOStreams = ioStreams(f)       // needs f.Config() for logger settings
	f.TUI = tui.NewTUI(f.IOStreams)  // needs IOStreams
	f.Client = clientFunc(f)         // depends on Config
	f.GitManager = gitManagerFunc(f) // depends on Config
	f.Prompter = prompterFunc(f)

	return f
}

// ioStreams creates an IOStreams with TTY/color/CI detection and initializes the logger.
func ioStreams(f *cmdutil.Factory) *iostreams.IOStreams {
	ios := iostreams.System()

	// CLAWKER_SPINNER_DISABLED is clawker-specific config
	if os.Getenv("CLAWKER_SPINNER_DISABLED") != "" {
		ios.SetSpinnerDisabled(true)
	}

	// Initialize logger from settings — config gateway resolves ENV > config > defaults
	settings := f.Config().UserSettings()

	logsDir, err := config.LogsDir()
	if err != nil {
		logger.Init()
		ios.Logger = &logger.Log
		return ios
	}

	// Build OTEL config from settings if enabled
	var otelCfg *logger.OtelLogConfig
	if settings.Logging.Otel.IsEnabled() {
		otelCfg = &logger.OtelLogConfig{
			Endpoint:       settings.Monitoring.OtelCollectorEndpoint(),
			Insecure:       true,
			Timeout:        time.Duration(settings.Logging.Otel.GetTimeoutSeconds()) * time.Second,
			MaxQueueSize:   settings.Logging.Otel.GetMaxQueueSize(),
			ExportInterval: time.Duration(settings.Logging.Otel.GetExportIntervalSeconds()) * time.Second,
		}
	}

	if err := logger.NewLogger(&logger.Options{
		LogsDir: logsDir,
		FileConfig: &logger.LoggingConfig{
			FileEnabled: settings.Logging.FileEnabled,
			MaxSizeMB:   settings.Logging.MaxSizeMB,
			MaxAgeDays:  settings.Logging.MaxAgeDays,
			MaxBackups:  settings.Logging.MaxBackups,
			Compress:    settings.Logging.Compress,
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
			cfg, ok := f.Config().(*config.Config)
			if !ok {
				clientErr = fmt.Errorf("factory config provider must be *config.Config")
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
func hostProxyFunc() func() hostproxy.HostProxyService {
	var (
		once    sync.Once
		manager hostproxy.HostProxyService
	)
	return func() hostproxy.HostProxyService {
		once.Do(func() {
			manager = hostproxy.NewManager()
		})
		return manager
	}
}

// socketBridgeFunc returns a lazy closure that creates a socket bridge manager once.
func socketBridgeFunc() func() socketbridge.SocketBridgeManager {
	var (
		once    sync.Once
		manager socketbridge.SocketBridgeManager
	)
	return func() socketbridge.SocketBridgeManager {
		once.Do(func() {
			manager = socketbridge.NewManager()
		})
		return manager
	}
}

// configFunc returns a lazy closure that creates a Config gateway once.
// Config uses os.Getwd internally for project resolution.
func configFunc() func() config.Provider {
	var (
		once sync.Once
		cfg  config.Provider
	)
	return func() config.Provider {
		once.Do(func() {
			loaded, err := config.NewConfig()
			if err != nil {
				cfg = config.NewConfigForTest(nil, nil)
				return
			}
			cfg = loaded
		})
		return cfg
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
			cfg := f.Config()
			if cfg.ProjectCfg() == nil {
				mgrErr = fmt.Errorf("no project configuration found; run 'clawker init' or ensure clawker.yaml exists")
				return
			}
			projectRoot := cfg.ProjectCfg().RootDir()
			if projectRoot == "" {
				mgrErr = fmt.Errorf("not in a registered project directory")
				return
			}
			mgr, mgrErr = git.NewGitManager(projectRoot)
		})
		return mgr, mgrErr
	}
}

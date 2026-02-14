package factory

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/internal/tui"
)

// New creates a fully-wired Factory with lazy-initialized dependency closures.
// Called exactly once at the CLI entry point (internal/clawker/cmd.go).
// Tests should NOT import this package — construct &cmdutil.Factory{} directly.
func New(version string) *cmdutil.Factory {
	ios := ioStreams()

	f := &cmdutil.Factory{
		Version: version,

		Config:       configFunc(),
		IOStreams:    ios,
		TUI:          tui.NewTUI(ios),
		HostProxy:    hostProxyFunc(),
		SocketBridge: socketBridgeFunc(),
	}

	f.Client = clientFunc(f)         // depends on Config
	f.GitManager = gitManagerFunc(f) // depends on Config
	f.Prompter = prompterFunc(f)

	return f
}

// ioStreams creates an IOStreams with TTY/color/CI detection.
func ioStreams() *iostreams.IOStreams {
	ios := iostreams.System()

	// CLAWKER_SPINNER_DISABLED is clawker-specific config
	if os.Getenv("CLAWKER_SPINNER_DISABLED") != "" {
		ios.SetSpinnerDisabled(true)
	}

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
			client, clientErr = docker.NewClient(ctx, f.Config())
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
func configFunc() func() *config.Config {
	var (
		once sync.Once
		cfg  *config.Config
	)
	return func() *config.Config {
		once.Do(func() {
			cfg = config.NewConfig()
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
			if cfg.Project == nil {
				mgrErr = fmt.Errorf("no project configuration found; run 'clawker init' or ensure clawker.yaml exists")
				return
			}
			projectRoot := cfg.Project.RootDir()
			if projectRoot == "" {
				mgrErr = fmt.Errorf("not in a registered project directory")
				return
			}
			mgr, mgrErr = git.NewGitManager(projectRoot)
		})
		return mgr, mgrErr
	}
}

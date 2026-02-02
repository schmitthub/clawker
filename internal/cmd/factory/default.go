package factory

import (
	"context"
	"os"
	"sync"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
)

// New creates a fully-wired Factory with lazy-initialized dependency closures.
// Called exactly once at the CLI entry point (internal/clawker/cmd.go).
// Tests should NOT import this package — construct &cmdutil.Factory{} directly.
func New(version, commit string) *cmdutil.Factory {
	f := &cmdutil.Factory{
		Version: version,
		Commit:  commit,
	}

	f.IOStreams = ioStreams()
	f.WorkDir = workDirFunc()
	f.Client = clientFunc()
	f.HostProxy = hostProxyFunc()
	f.Config = configFunc(f)
	f.Prompter = prompterFunc(f)

	return f
}

// ioStreams creates an IOStreams with TTY/color/CI detection.
func ioStreams() *iostreams.IOStreams {
	ios := iostreams.NewIOStreams()

	// Auto-detect color support
	if ios.IsOutputTTY() {
		ios.DetectTerminalTheme()
		// Respect NO_COLOR environment variable
		if os.Getenv("NO_COLOR") != "" {
			ios.SetColorEnabled(false)
		}
	} else {
		ios.SetColorEnabled(false)
	}

	// Respect CI environment (disable prompts)
	if os.Getenv("CI") != "" {
		ios.SetNeverPrompt(true)
	}

	return ios
}

// workDirFunc returns a lazy closure that resolves the working directory once.
func workDirFunc() func() string {
	var (
		once sync.Once
		wd   string
	)
	return func() string {
		once.Do(func() {
			wd, _ = os.Getwd()
		})
		return wd
	}
}

// clientFunc returns a lazy closure that creates a Docker client once.
func clientFunc() func(context.Context) (*docker.Client, error) {
	var (
		once      sync.Once
		client    *docker.Client
		clientErr error
	)
	return func(ctx context.Context) (*docker.Client, error) {
		once.Do(func() {
			client, clientErr = docker.NewClient(ctx)
			if clientErr == nil {
				docker.WireBuildKit(client)
			}
		})
		return client, clientErr
	}
}

// hostProxyFunc returns a lazy closure that creates a host proxy manager once.
func hostProxyFunc() func() *hostproxy.Manager {
	var (
		once    sync.Once
		manager *hostproxy.Manager
	)
	return func() *hostproxy.Manager {
		once.Do(func() {
			manager = hostproxy.NewManager()
		})
		return manager
	}
}

// configFunc returns a lazy closure that creates a Config gateway once.
func configFunc(f *cmdutil.Factory) func() *config.Config {
	var (
		once sync.Once
		cfg  *config.Config
	)
	return func() *config.Config {
		once.Do(func() {
			cfg = config.NewConfig(f.WorkDir)
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

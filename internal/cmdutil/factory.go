package cmdutil

import (
	"context"
	"os"
	"sync"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
)

// Factory provides shared dependencies for CLI commands.
// It uses lazy initialization for expensive resources like Docker engine connections.
type Factory struct {
	// Configuration from flags (set before command execution)
	WorkDir        string
	BuildOutputDir string // Directory for build artifacts (versions.json, dockerfiles)
	Debug          bool

	// Version info (set at build time via ldflags)
	Version string
	Commit  string

	// IO streams for input/output (for testability)
	IOStreams *IOStreams

	// Lazy-loaded dependencies
	clientOnce sync.Once
	client     *docker.Client
	clientErr  error

	configOnce   sync.Once
	configLoader *config.Loader
	configData   *config.Config
	configErr    error

	settingsOnce   sync.Once
	settingsLoader *config.SettingsLoader
	settingsData   *config.Settings
	settingsErr    error

	hostProxyOnce    sync.Once
	hostProxyManager *hostproxy.Manager
}

// New creates a new Factory with the given version information.
func New(version, commit string) *Factory {
	ios := NewIOStreams()

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

	return &Factory{
		Version:   version,
		Commit:    commit,
		IOStreams: ios,
	}
}

// Client returns a lazily-initialized Docker client.
// The client wraps pkg/whail.Engine with clawker-specific label configuration.
func (f *Factory) Client(ctx context.Context) (*docker.Client, error) {
	f.clientOnce.Do(func() {
		f.client, f.clientErr = docker.NewClient(ctx)
	})
	return f.client, f.clientErr
}

// CloseClient closes the Docker client if it was initialized.
func (f *Factory) CloseClient() {
	if f.client != nil {
		f.client.Close()
	}
}

// ConfigLoader returns a config loader for the working directory.
func (f *Factory) ConfigLoader() *config.Loader {
	f.configOnce.Do(func() {
		f.configLoader = config.NewLoader(f.WorkDir)
	})
	return f.configLoader
}

// Config returns the loaded configuration (loads on first call).
// Subsequent calls return the cached configuration.
func (f *Factory) Config() (*config.Config, error) {
	if f.configData != nil || f.configErr != nil {
		return f.configData, f.configErr
	}
	f.configData, f.configErr = f.ConfigLoader().Load()
	return f.configData, f.configErr
}

// ResetConfig clears the cached configuration, forcing a reload on next access.
func (f *Factory) ResetConfig() {
	f.configData = nil
	f.configErr = nil
}

// SettingsLoader returns the user settings loader (lazily initialized).
func (f *Factory) SettingsLoader() (*config.SettingsLoader, error) {
	f.settingsOnce.Do(func() {
		f.settingsLoader, f.settingsErr = config.NewSettingsLoader()
	})
	return f.settingsLoader, f.settingsErr
}

// Settings returns the loaded user settings (loads on first call).
// Subsequent calls return the cached settings.
// If the settings file doesn't exist, returns default empty settings.
func (f *Factory) Settings() (*config.Settings, error) {
	if f.settingsData != nil || f.settingsErr != nil {
		return f.settingsData, f.settingsErr
	}
	loader, err := f.SettingsLoader()
	if err != nil {
		f.settingsErr = err
		return nil, err
	}
	f.settingsData, f.settingsErr = loader.Load()
	return f.settingsData, f.settingsErr
}

// InvalidateSettingsCache clears the cached settings, forcing a reload on next access.
// Note: This only clears the data cache, not the loader. The settings file path
// is determined at loader creation and remains fixed for the Factory lifetime.
func (f *Factory) InvalidateSettingsCache() {
	f.settingsData = nil
	f.settingsErr = nil
}

// HostProxy returns the host proxy manager (lazily initialized).
// The manager handles the lifecycle of the host proxy server that allows
// containers to perform actions on the host machine.
func (f *Factory) HostProxy() *hostproxy.Manager {
	f.hostProxyOnce.Do(func() {
		f.hostProxyManager = hostproxy.NewManager()
	})
	return f.hostProxyManager
}

// EnsureHostProxy starts the host proxy server if it's not already running.
// Returns an error if the server fails to start.
func (f *Factory) EnsureHostProxy() error {
	return f.HostProxy().EnsureRunning()
}

// StopHostProxy gracefully stops the host proxy server.
func (f *Factory) StopHostProxy(ctx context.Context) error {
	if f.hostProxyManager == nil {
		return nil
	}
	return f.hostProxyManager.Stop(ctx)
}

// HostProxyEnvVar returns the environment variable string for containers
// to connect to the host proxy, or empty string if proxy is not running.
// Note: There's a small race between IsRunning() and ProxyURL() checks.
// This is accepted as the failure mode is benign - container would get
// a URL to a non-running server, and curl would fail gracefully.
func (f *Factory) HostProxyEnvVar() string {
	if f.hostProxyManager == nil || !f.hostProxyManager.IsRunning() {
		return ""
	}
	return "CLAWKER_HOST_PROXY=" + f.hostProxyManager.ProxyURL()
}

// Prompter returns a new Prompter using the Factory's IOStreams.
// Use this for interactive user prompts that respect TTY detection.
func (f *Factory) Prompter() *Prompter {
	return NewPrompter(f.IOStreams)
}

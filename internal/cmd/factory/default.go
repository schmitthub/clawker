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
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/prompts"
	"github.com/schmitthub/clawker/pkg/whail/buildkit"
)

// New creates a fully-wired Factory with lazy-initialized dependency closures.
// Called exactly once at the CLI entry point (internal/clawker/cmd.go).
// Tests should NOT import this package — construct &cmdutil.Factory{} directly.
func New(version, commit string) *cmdutil.Factory {
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

	f := &cmdutil.Factory{
		Version:   version,
		Commit:    commit,
		IOStreams: ios,
	}

	// --- Lazy dependency closures ---

	// Docker client
	var (
		clientOnce sync.Once
		client     *docker.Client
		clientErr  error
	)
	f.Client = func(ctx context.Context) (*docker.Client, error) {
		clientOnce.Do(func() {
			client, clientErr = docker.NewClient(ctx)
			if clientErr == nil {
				client.BuildKitImageBuilder = buildkit.NewImageBuilder(client.APIClient)
			}
		})
		return client, clientErr
	}
	f.CloseClient = func() {
		if client != nil {
			client.Close()
		}
	}

	// Registry
	var (
		registryOnce   sync.Once
		registryLoader *config.RegistryLoader
		registryData   *config.ProjectRegistry
		registryErr    error
	)
	initRegistry := func() {
		registryOnce.Do(func() {
			registryLoader, registryErr = config.NewRegistryLoader()
			if registryErr == nil {
				registryData, registryErr = registryLoader.Load()
			}
		})
	}
	f.RegistryLoader = func() (*config.RegistryLoader, error) {
		initRegistry()
		return registryLoader, registryErr
	}
	f.Registry = func() (*config.ProjectRegistry, error) {
		initRegistry()
		return registryData, registryErr
	}

	// Resolution
	var (
		resolutionOnce sync.Once
		resolution     *config.Resolution
	)
	f.Resolution = func() *config.Resolution {
		resolutionOnce.Do(func() {
			registry, err := f.Registry()
			if err != nil {
				logger.Warn().Err(err).Msg("failed to load project registry; operating without project context")
				resolution = &config.Resolution{WorkDir: f.WorkDir}
				return
			}
			if registry == nil {
				resolution = &config.Resolution{WorkDir: f.WorkDir}
				return
			}
			resolver := config.NewResolver(registry)
			resolution = resolver.Resolve(f.WorkDir)
		})
		return resolution
	}

	// Config
	var (
		configOnce   sync.Once
		configLoader *config.Loader
		configData   *config.Config
		configErr    error
	)
	f.ConfigLoader = func() *config.Loader {
		configOnce.Do(func() {
			var opts []config.LoaderOption

			res := f.Resolution()
			if res.Found() {
				opts = append(opts,
					config.WithProjectRoot(res.ProjectRoot()),
					config.WithProjectKey(res.ProjectKey),
				)
			}

			opts = append(opts, config.WithUserDefaults(""))
			configLoader = config.NewLoader(f.WorkDir, opts...)
		})
		return configLoader
	}
	f.Config = func() (*config.Config, error) {
		if configData != nil || configErr != nil {
			return configData, configErr
		}
		configData, configErr = f.ConfigLoader().Load()
		return configData, configErr
	}
	f.ResetConfig = func() {
		configData = nil
		configErr = nil
	}

	// Settings
	var (
		settingsOnce   sync.Once
		settingsLoader *config.SettingsLoader
		settingsData   *config.Settings
		settingsErr    error
	)
	f.SettingsLoader = func() (*config.SettingsLoader, error) {
		settingsOnce.Do(func() {
			var opts []config.SettingsLoaderOption

			res := f.Resolution()
			if res.Found() {
				opts = append(opts, config.WithProjectSettingsRoot(res.ProjectRoot()))
			}

			settingsLoader, settingsErr = config.NewSettingsLoader(opts...)
		})
		return settingsLoader, settingsErr
	}
	f.Settings = func() (*config.Settings, error) {
		if settingsData != nil || settingsErr != nil {
			return settingsData, settingsErr
		}
		loader, err := f.SettingsLoader()
		if err != nil {
			settingsErr = err
			return nil, err
		}
		settingsData, settingsErr = loader.Load()
		return settingsData, settingsErr
	}
	f.InvalidateSettingsCache = func() {
		settingsData = nil
		settingsErr = nil
	}

	// Host proxy
	var (
		hostProxyOnce    sync.Once
		hostProxyManager *hostproxy.Manager
	)
	f.HostProxy = func() *hostproxy.Manager {
		hostProxyOnce.Do(func() {
			hostProxyManager = hostproxy.NewManager()
		})
		return hostProxyManager
	}
	f.EnsureHostProxy = func() error {
		return f.HostProxy().EnsureRunning()
	}
	f.StopHostProxy = func(ctx context.Context) error {
		if hostProxyManager == nil {
			return nil
		}
		return hostProxyManager.Stop(ctx)
	}
	f.HostProxyEnvVar = func() string {
		if hostProxyManager == nil || !hostProxyManager.IsRunning() {
			return ""
		}
		return "CLAWKER_HOST_PROXY=" + hostProxyManager.ProxyURL()
	}

	// Prompter
	f.Prompter = func() *prompts.Prompter {
		return prompts.NewPrompter(f.IOStreams)
	}

	// RuntimeEnv — config-derived env vars injected at container creation time
	f.RuntimeEnv = func() []string {
		cfg, err := f.Config()
		if err != nil {
			return nil
		}
		return docker.RuntimeEnv(cfg)
	}

	// BuildKitEnabled — detects BuildKit support from env var or daemon ping
	f.BuildKitEnabled = func(ctx context.Context) (bool, error) {
		client, err := f.Client(ctx)
		if err != nil {
			return false, err
		}
		return docker.BuildKitEnabled(ctx, client.APIClient)
	}

	return f
}

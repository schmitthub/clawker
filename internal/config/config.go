package config

import (
	"fmt"
	"sync"

	"github.com/schmitthub/clawker/internal/logger"
)

// Config is the top-level configuration gateway. It lazily loads and caches
// project config, user settings, and project resolution.
type Config struct {
	workDir func() (string, error)

	// Project config (clawker.yaml)
	projectOnce   sync.Once
	projectLoader *Loader
	project       *Project
	projectErr    error

	// Settings (settings.yaml)
	settingsOnce   sync.Once
	settingsLoader *SettingsLoader
	settings       *Settings
	settingsErr    error

	// Registry + Resolution
	registryOnce   sync.Once
	registryLoader *RegistryLoader
	registryErr    error
	resolutionOnce sync.Once
	resolution     *Resolution
}

// NewConfig creates a new Config gateway.
func NewConfig(workDir func() (string, error)) *Config {
	return &Config{workDir: workDir}
}

// NewConfigForTest creates a Config gateway pre-populated with the given project
// and settings. Resolution returns an empty Resolution with the given workDir.
// This is intended for unit tests that don't need real config file loading.
func NewConfigForTest(workDir string, project *Project, settings *Settings) *Config {
	c := &Config{workDir: func() (string, error) { return workDir, nil }}
	c.projectOnce.Do(func() {
		c.project = project
	})
	c.settingsOnce.Do(func() {
		c.settings = settings
	})
	c.resolutionOnce.Do(func() {
		projectKey := ""
		if project != nil {
			projectKey = project.Project
		}
		c.resolution = &Resolution{
			ProjectKey: projectKey,
			WorkDir:    workDir,
		}
	})
	return c
}

// Project returns the project configuration from clawker.yaml.
// Results are cached after first load.
func (c *Config) Project() (*Project, error) {
	c.projectOnce.Do(func() {
		wd, err := c.workDir()
		if err != nil {
			c.projectErr = fmt.Errorf("failed to get working directory: %w", err)
			return
		}

		var opts []LoaderOption

		res := c.Resolution()
		if res.Found() {
			opts = append(opts,
				WithProjectRoot(res.ProjectRoot()),
				WithProjectKey(res.ProjectKey),
			)
		}

		opts = append(opts, WithUserDefaults(""))
		c.projectLoader = NewLoader(wd, opts...)
		c.project, c.projectErr = c.projectLoader.Load()
	})
	return c.project, c.projectErr
}

// Settings returns the user settings from settings.yaml.
// Results are cached after first load.
func (c *Config) Settings() (*Settings, error) {
	c.settingsOnce.Do(func() {
		var opts []SettingsLoaderOption

		res := c.Resolution()
		if res.Found() {
			opts = append(opts, WithProjectSettingsRoot(res.ProjectRoot()))
		}

		c.settingsLoader, c.settingsErr = NewSettingsLoader(opts...)
		if c.settingsErr != nil {
			return
		}
		c.settings, c.settingsErr = c.settingsLoader.Load()
	})
	return c.settings, c.settingsErr
}

// SettingsLoader returns the underlying settings loader for write operations
// (e.g., saving updated default image). Lazily initialized.
func (c *Config) SettingsLoader() (*SettingsLoader, error) {
	// Intentional side-effect: calling Settings() ensures the settings loader
	// is lazily initialized via settingsOnce. The returned values are discarded
	// because we access the cached settingsLoader and settingsErr directly.
	_, _ = c.Settings()
	return c.settingsLoader, c.settingsErr
}

// Resolution returns the project resolution (project key, entry, workdir).
// Results are cached after first resolution.
func (c *Config) Resolution() *Resolution {
	c.resolutionOnce.Do(func() {
		wd, err := c.workDir()
		if err != nil {
			logger.Warn().Err(err).Msg("failed to get working directory; operating without project context")
			c.resolution = &Resolution{}
			return
		}
		registry, regErr := c.registry()
		if regErr != nil {
			logger.Warn().Err(regErr).Msg("failed to load project registry; operating without project context")
			c.resolution = &Resolution{WorkDir: wd}
			return
		}
		if registry == nil {
			c.resolution = &Resolution{WorkDir: wd}
			return
		}
		resolver := NewResolver(registry)
		c.resolution = resolver.Resolve(wd)
	})
	return c.resolution
}

// Registry returns the registry loader for write operations
// (e.g., project register, project init).
func (c *Config) Registry() (*RegistryLoader, error) {
	c.initRegistry()
	return c.registryLoader, c.registryErr
}

func (c *Config) initRegistry() {
	c.registryOnce.Do(func() {
		c.registryLoader, c.registryErr = NewRegistryLoader()
	})
}

func (c *Config) registry() (*ProjectRegistry, error) {
	c.initRegistry()
	if c.registryErr != nil {
		return nil, c.registryErr
	}
	return c.registryLoader.Load()
}

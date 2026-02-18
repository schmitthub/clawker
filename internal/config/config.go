package config

import (
	"os"

	"github.com/schmitthub/clawker/internal/logger"
)

// Config is a facade providing access to all configuration.
// All fields are eagerly loaded from the current working directory.
type Config struct {
	Project    *Project
	Settings   *Settings
	projectKey string
	workDir    string
	Registry   Registry

	// Internal loaders for operations that need them
	projectLoader   *ProjectLoader
	settingsLoader  SettingsLoader
	registryInitErr error // stored for later diagnostics
}

// NewConfig creates a Config facade by loading all configuration from the
// current working directory.
func NewConfig() (*Config, error) {
	c := &Config{}
	if err := c.Reload(); err != nil {
		return nil, err
	}
	return c, nil
}

// NewConfigForTest creates a Config facade with pre-populated values.
// This is intended for unit tests that don't need real config file loading.
//
// Limitations:
//   - Project.RootDir() returns "" (no registry entry)
//   - Worktree methods (GetOrCreateWorktreeDir, etc.) will fail without a registry
//   - Tests needing full runtime context should use NewConfig() with os.Chdir()
//     to a temp directory containing clawker.yaml and a registered project
func NewConfigForTest(project *Project, settings *Settings) *Config {
	if project == nil {
		project = DefaultProject()
	}
	if settings == nil {
		settings = DefaultSettings()
	}
	if project.Project != "" {
		project.setRuntimeContext(&ProjectEntry{Name: project.Project}, nil)
	}

	return &Config{
		Project:    project,
		Settings:   settings,
		projectKey: project.Project,
	}
}

// NewConfigForTestWithEntry creates a Config facade with a full project entry.
// This is intended for integration tests that need worktree methods to work.
// The configDir is used for registry operations (worktree directory management).
func NewConfigForTestWithEntry(project *Project, settings *Settings, entry *ProjectEntry, configDir string) *Config {
	if project == nil {
		project = DefaultProject()
	}
	if settings == nil {
		settings = DefaultSettings()
	}
	if entry == nil {
		entry = &ProjectEntry{}
	}

	// Create a registry loader pointing to the test config dir
	var registry *RegistryLoader
	if configDir != "" {
		registry = NewRegistryLoaderWithPath(configDir)
	}

	// Inject full runtime context for tests
	if project.Project != "" {
		project.setRuntimeContext(entry, registry)
	}

	return &Config{
		Project:    project,
		Settings:   settings,
		projectKey: project.Project,
		workDir:    entry.Root,
		Registry:   registry,
	}
}

// Reload initializes all configuration from the current working directory.
func (c *Config) Reload() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	c.workDir = wd

	// Reset per-load state.
	c.projectLoader = nil
	c.settingsLoader = nil
	c.registryInitErr = nil
	c.projectKey = ""

	if err := c.loadRegistry(); err != nil {
		return err
	}

	projectKey, projectEntry := c.lookupProjectContext(wd)
	c.projectKey = projectKey

	if err := c.loadProject(wd, projectKey, projectEntry); err != nil {
		return err
	}
	if err := c.loadSettings(projectEntry); err != nil {
		return err
	}

	return nil
}

func (c *Config) loadRegistry() error {
	var err error
	c.Registry, err = NewRegistryLoader()
	if err != nil {
		c.registryInitErr = err
		logger.Warn().Err(err).Msg("failed to initialize registry loader")
	}
	return nil
}

func (c *Config) lookupProjectContext(wd string) (string, ProjectEntry) {
	if c.Registry == nil {
		return "", ProjectEntry{}
	}

	registry, err := c.Registry.Load()
	if err != nil {
		logger.Warn().Err(err).Msg("failed to load project registry; operating without project context")
		return "", ProjectEntry{}
	}
	if registry == nil {
		return "", ProjectEntry{}
	}

	key, entry := registry.Lookup(wd)
	return key, entry
}

func (c *Config) loadProject(wd, projectKey string, projectEntry ProjectEntry) error {
	var opts []ProjectLoaderOption
	if projectKey != "" {
		opts = append(opts,
			WithProjectRoot(projectEntry.Root),
			WithProjectKey(projectKey),
		)
	}

	opts = append(opts, WithUserDefaults(""))
	c.projectLoader = NewProjectLoader(wd, opts...)

	project, err := c.projectLoader.Load()
	if err != nil {
		if IsConfigNotFound(err) {
			logger.Debug().Msg("no clawker.yaml found; using defaults")
			project = DefaultProject()
		} else {
			return err
		}
	}

	if projectKey != "" && project.Project == "" {
		project.Project = projectKey
	}
	if projectKey != "" {
		project.setRuntimeContext(&projectEntry, c.Registry)
	}

	c.Project = project
	return nil
}

func (c *Config) loadSettings(projectEntry ProjectEntry) error {
	var opts []SettingsLoaderOption
	if projectEntry.Root != "" {
		opts = append(opts, WithProjectSettingsRoot(projectEntry.Root))
	}

	loader, err := NewSettingsLoader(opts...)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to initialize settings loader; using defaults")
		c.Settings = DefaultSettings()
		return nil
	}
	c.settingsLoader = loader

	settings, err := loader.Load()
	if err != nil {
		logger.Warn().Err(err).Msg("failed to load settings; using defaults")
		c.Settings = DefaultSettings()
		return nil
	}
	c.Settings = settings
	return nil
}

func (c *Config) ProjectCfg() *Project {
	return c.Project
}

func (c *Config) UserSettings() *Settings {
	return c.Settings
}

func (c *Config) ProjectKey() string {
	return c.projectKey
}

func (c *Config) ProjectFound() bool {
	return c.projectKey != ""
}

func (c *Config) WorkDir() string {
	return c.workDir
}

func (c *Config) ProjectRegistry() (Registry, error) {
	if c.Registry != nil {
		return c.Registry, nil
	}
	if c.registryInitErr != nil {
		return nil, c.registryInitErr
	}
	return nil, nil
}

// SettingsLoader returns the underlying settings loader for write operations
// (e.g., saving updated default image). May return nil if settings failed to load.
func (c *Config) SettingsLoader() SettingsLoader {
	return c.settingsLoader
}

// ProjectLoader returns the underlying project loader.
// May return nil if project loading was skipped.
func (c *Config) ProjectLoader() *ProjectLoader {
	return c.projectLoader
}

// RegistryInitErr returns the error that occurred during registry initialization,
// if any. This can be used to provide better error messages when Registry is nil.
func (c *Config) RegistryInitErr() error {
	return c.registryInitErr
}

// SetSettingsLoader sets the settings loader for write operations.
// Used by NewConfigForTest variants and fawker to inject a test-friendly loader.
func (c *Config) SetSettingsLoader(sl SettingsLoader) {
	if sl == nil {
		panic("SetSettingsLoader: nil loader")
	}
	c.settingsLoader = sl
}

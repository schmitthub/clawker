package config

import (
	"fmt"
	"os"

	"github.com/schmitthub/clawker/internal/logger"
)

// Config is a facade providing access to all configuration.
// All fields are eagerly loaded from the current working directory.
type Config struct {
	// Project is the project configuration from clawker.yaml.
	// Never nil - uses defaults if no config file exists.
	// Contains runtime context (Key, RootDir, worktree methods) after loading.
	Project *Project

	// Settings is the user settings from settings.yaml.
	// Never nil - uses defaults if no settings file exists.
	Settings *Settings

	// Resolution is the project resolution (project key, entry, workdir).
	// Never nil - empty resolution if not in a registered project.
	Resolution *Resolution

	// Registry is the registry for write operations.
	// May be nil if registry initialization failed. Check RegistryInitErr() for details.
	Registry Registry

	// Internal loaders for operations that need them
	projectLoader   *Loader
	settingsLoader  SettingsLoader
	registryInitErr error // stored for later diagnostics
}

// NewConfig creates a Config facade by loading all configuration from the
// current working directory. This function always succeeds - missing files
// result in default values, and errors are logged but don't prevent creation.
func NewConfig() *Config {
	c := &Config{}
	c.load()
	return c
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
		project = DefaultConfig()
	}
	if settings == nil {
		settings = DefaultSettings()
	}

	resolution := &Resolution{}
	if project.Project != "" {
		resolution.ProjectKey = project.Project
	}

	// Inject minimal runtime context for tests
	if resolution.Found() {
		project.setRuntimeContext(&resolution.ProjectEntry, nil)
	}

	return &Config{
		Project:    project,
		Settings:   settings,
		Resolution: resolution,
	}
}

// NewConfigForTestWithEntry creates a Config facade with a full project entry.
// This is intended for integration tests that need worktree methods to work.
// The configDir is used for registry operations (worktree directory management).
func NewConfigForTestWithEntry(project *Project, settings *Settings, entry *ProjectEntry, configDir string) *Config {
	if project == nil {
		project = DefaultConfig()
	}
	if settings == nil {
		settings = DefaultSettings()
	}
	if entry == nil {
		entry = &ProjectEntry{}
	}

	resolution := &Resolution{}
	if project.Project != "" {
		resolution.ProjectKey = project.Project
		resolution.ProjectEntry = *entry
		resolution.WorkDir = entry.Root
	}

	// Create a registry loader pointing to the test config dir
	var registry *RegistryLoader
	if configDir != "" {
		registry = NewRegistryLoaderWithPath(configDir)
	}

	// Inject full runtime context for tests
	if resolution.Found() {
		project.setRuntimeContext(&resolution.ProjectEntry, registry)
	}

	return &Config{
		Project:    project,
		Settings:   settings,
		Resolution: resolution,
		Registry:   registry,
	}
}

// load initializes all configuration from the current working directory.
func (c *Config) load() {
	wd, err := os.Getwd()
	if err != nil {
		// This is catastrophic - we can't do anything useful without knowing our location
		fmt.Fprintf(os.Stderr, "Error: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}

	// Load registry first (needed for resolution)
	c.Registry, err = NewRegistryLoader()
	if err != nil {
		c.registryInitErr = err
		logger.Warn().Err(err).Msg("failed to initialize registry loader")
	}

	// Resolve project from registry
	c.Resolution = c.resolveProject(wd)

	// Load project config
	c.loadProject(wd)

	// Inject runtime context into ProjectCfg
	if c.Resolution.Found() {
		c.Project.setRuntimeContext(&c.Resolution.ProjectEntry, c.Registry)
	}

	// Load settings
	c.loadSettings()
}

// resolveProject resolves the working directory to a registered project.
func (c *Config) resolveProject(wd string) *Resolution {
	if c.Registry == nil {
		return &Resolution{WorkDir: wd}
	}

	registry, err := c.Registry.Load()
	if err != nil {
		logger.Warn().Err(err).Msg("failed to load project registry; operating without project context")
		return &Resolution{WorkDir: wd}
	}
	if registry == nil {
		return &Resolution{WorkDir: wd}
	}

	resolver := NewResolver(registry)
	return resolver.Resolve(wd)
}

// loadProject loads the project configuration from clawker.yaml.
func (c *Config) loadProject(wd string) {
	var opts []LoaderOption

	if c.Resolution.Found() {
		opts = append(opts,
			WithProjectRoot(c.Resolution.ProjectRoot()),
			WithProjectKey(c.Resolution.ProjectKey),
		)
	}

	opts = append(opts, WithUserDefaults(""))
	c.projectLoader = NewLoader(wd, opts...)

	project, err := c.projectLoader.Load()
	if err != nil {
		if IsConfigNotFound(err) {
			// No config file is fine - use defaults
			logger.Debug().Msg("no clawker.yaml found; using defaults")
			c.Project = DefaultConfig()
			return
		}
		// Config exists but is invalid - this is a fatal error
		// The user expects their config to be used, not silently replaced with defaults
		fmt.Fprintf(os.Stderr, "\nError: clawker.yaml is invalid\n\n%v\n\nFix the configuration file and try again.\n", err)
		os.Exit(1)
	}
	c.Project = project
}

// loadSettings loads the user settings from settings.yaml.
func (c *Config) loadSettings() {
	var opts []SettingsLoaderOption

	if c.Resolution.Found() {
		opts = append(opts, WithProjectSettingsRoot(c.Resolution.ProjectRoot()))
	}

	loader, err := NewSettingsLoader(opts...)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to initialize settings loader; using defaults")
		c.Settings = DefaultSettings()
		return
	}
	c.settingsLoader = loader

	settings, err := loader.Load()
	if err != nil {
		logger.Warn().Err(err).Msg("failed to load settings; using defaults")
		c.Settings = DefaultSettings()
		return
	}
	c.Settings = settings
}

// SettingsLoader returns the underlying settings loader for write operations
// (e.g., saving updated default image). May return nil if settings failed to load.
func (c *Config) SettingsLoader() SettingsLoader {
	return c.settingsLoader
}

// ProjectLoader returns the underlying project loader.
// May return nil if project loading was skipped.
func (c *Config) ProjectLoader() *Loader {
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

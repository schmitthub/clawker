package cmdutil

import (
	"context"
	"sync"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/engine"
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

	// Lazy-loaded dependencies - legacy (use Client() instead)
	engineOnce sync.Once
	engine     *engine.Engine
	engineErr  error

	// Lazy-loaded dependencies - new architecture
	clientOnce sync.Once
	client     *docker.Client
	clientErr  error

	configOnce   sync.Once
	configLoader *config.Loader
	configData   *config.Config
	configErr    error
}

// New creates a new Factory with the given version information.
func New(version, commit string) *Factory {
	return &Factory{
		Version: version,
		Commit:  commit,
	}
}

// Engine returns a lazily-initialized Docker engine.
// The engine is created once and cached for subsequent calls.
func (f *Factory) Engine(ctx context.Context) (*engine.Engine, error) {
	f.engineOnce.Do(func() {
		f.engine, f.engineErr = engine.NewEngine(ctx)
	})
	return f.engine, f.engineErr
}

// CloseEngine closes the Docker engine if it was initialized.
// Deprecated: Use CloseClient() instead with the new docker.Client.
func (f *Factory) CloseEngine() {
	if f.engine != nil {
		f.engine.Close()
	}
}

// Client returns a lazily-initialized Docker client.
// The client wraps pkg/whail.Engine with clawker-specific label configuration.
// This is the preferred method over Engine() for new code.
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

package whail

import (
	"context"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// EngineOptions configures the behavior of the Engine.
type EngineOptions struct {
	// LabelPrefix is the prefix for all managed labels (e.g., "com.myapp").
	// Used to construct the managed label key: "{LabelPrefix}.{ManagedLabel}".
	LabelPrefix string

	// ManagedLabel is the label key suffix that marks resources as managed.
	// Default: "managed". Combined with LabelPrefix to form the full key.
	// Example: with LabelPrefix="com.myapp" and ManagedLabel="managed",
	// the full key is "com.myapp.managed=true".
	ManagedLabel string

	// Labels configures labels for different resource types.
	Labels LabelConfig
}

// DefaultManagedLabel is the default label suffix for marking managed resources.
const DefaultManagedLabel = "managed"

// Engine wraps the Docker client with automatic label-based resource isolation.
// All list operations automatically inject filters to only return resources
// managed by this engine (identified by the configured label prefix).
type Engine struct {
	cli     *client.Client
	options EngineOptions

	// Precomputed values for efficiency
	managedLabelKey   string // e.g., "com.myapp.managed"
	managedLabelValue string // always "true"
}

// NewEngine creates a new Engine with the given options.
// It connects to the Docker daemon and verifies the connection.
func NewEngine(ctx context.Context, opts EngineOptions) (*Engine, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, ErrDockerNotRunning(err)
	}

	// Apply defaults
	if opts.ManagedLabel == "" {
		opts.ManagedLabel = DefaultManagedLabel
	}

	engine := &Engine{
		cli:               cli,
		options:           opts,
		managedLabelKey:   opts.LabelPrefix + "." + opts.ManagedLabel,
		managedLabelValue: "true",
	}

	// Verify connection
	if err := engine.HealthCheck(ctx); err != nil {
		cli.Close()
		return nil, err
	}

	return engine, nil
}

// HealthCheck verifies the Docker daemon is reachable.
func (e *Engine) HealthCheck(ctx context.Context) error {
	_, err := e.cli.Ping(ctx)
	if err != nil {
		return ErrDockerNotRunning(err)
	}
	return nil
}

// Close releases Docker client resources.
func (e *Engine) Close() error {
	return e.cli.Close()
}

// Client returns the underlying Docker client for advanced operations.
// Use with caution - direct client usage bypasses label filtering.
func (e *Engine) Client() *client.Client {
	return e.cli
}

// Options returns the engine options.
func (e *Engine) Options() EngineOptions {
	return e.options
}

// ManagedLabelKey returns the full managed label key (e.g., "com.myapp.managed").
func (e *Engine) ManagedLabelKey() string {
	return e.managedLabelKey
}

// ManagedLabelValue returns the managed label value (always "true").
func (e *Engine) ManagedLabelValue() string {
	return e.managedLabelValue
}

// injectManagedFilter adds the managed label filter to existing filters.
// This ensures all list operations only return managed resources.
func (e *Engine) injectManagedFilter(existing filters.Args) filters.Args {
	if existing.Len() == 0 {
		existing = filters.NewArgs()
	}
	existing.Add("label", e.managedLabelKey+"="+e.managedLabelValue)
	return existing
}

// newManagedFilter creates a new filter with just the managed label.
func (e *Engine) newManagedFilter() filters.Args {
	return filters.NewArgs(
		filters.Arg("label", e.managedLabelKey+"="+e.managedLabelValue),
	)
}

// managedLabels returns the base labels that mark a resource as managed.
func (e *Engine) managedLabels() map[string]string {
	return map[string]string{
		e.managedLabelKey: e.managedLabelValue,
	}
}

// containerLabels returns labels for a container, including managed label.
func (e *Engine) containerLabels(extra ...map[string]string) map[string]string {
	base := e.managedLabels()
	configLabels := e.options.Labels.ContainerLabels()
	all := append([]map[string]string{base, configLabels}, extra...)
	return MergeLabels(all...)
}

// volumeLabels returns labels for a volume, including managed label.
func (e *Engine) volumeLabels(extra ...map[string]string) map[string]string {
	base := e.managedLabels()
	configLabels := e.options.Labels.VolumeLabels()
	all := append([]map[string]string{base, configLabels}, extra...)
	return MergeLabels(all...)
}

// networkLabels returns labels for a network, including managed label.
func (e *Engine) networkLabels(extra ...map[string]string) map[string]string {
	base := e.managedLabels()
	configLabels := e.options.Labels.NetworkLabels()
	all := append([]map[string]string{base, configLabels}, extra...)
	return MergeLabels(all...)
}

// imageLabels returns labels for an image, including managed label.
func (e *Engine) imageLabels(extra ...map[string]string) map[string]string {
	base := e.managedLabels()
	configLabels := e.options.Labels.ImageLabels()
	all := append([]map[string]string{base, configLabels}, extra...)
	return MergeLabels(all...)
}

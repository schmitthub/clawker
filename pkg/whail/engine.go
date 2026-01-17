package whail

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"
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
	// cli     *client.Client
	client.APIClient
	// logger *log.Logger // TODO: Add logger
	options EngineOptions

	// Precomputed values for efficiency
	managedLabelKey   string // e.g., "com.myapp.managed"
	managedLabelValue string // always "true"
}

// New creates a new Engine with default options.
// The caller is responsible for calling Close() when done.
func New(ctx context.Context) (*Engine, error) {
	return NewWithOptions(ctx, EngineOptions{})
}

// NewWithOptions creates a new Engine with the given options.
// It connects to the Docker daemon and verifies the connection.
func NewWithOptions(ctx context.Context, opts EngineOptions) (*Engine, error) {
	// Apply defaults
	if opts.ManagedLabel == "" {
		opts.ManagedLabel = DefaultManagedLabel
	}

	// Create the underlying Docker client using moby/moby v0.2.1 API
	realClient, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	// logger := opts.Logger
	// if logger == nil {
	// 	logger = log.Default()
	// }

	e := &Engine{
		APIClient:         realClient,
		options:           opts,
		managedLabelKey:   opts.LabelPrefix + "." + opts.ManagedLabel,
		managedLabelValue: "true",
		// logger:    logger,
	}

	// Verify connectivity

	if err := e.HealthCheck(ctx); err != nil {
		return nil, err
	}
	// logger.Printf("[Engine] Connected to Docker daemon")

	return e, nil
}

// NewFromExisting wraps an existing APIClient (useful for testing with mocks).
func NewFromExisting(c client.APIClient) *Engine {
	return &Engine{
		APIClient: c,
		// logger:    log.Default(),
	}
}

// HealthCheck verifies the Docker daemon is reachable.
func (e *Engine) HealthCheck(ctx context.Context) error {
	_, err := e.Ping(ctx, client.PingOptions{})
	if err != nil {
		return ErrDockerNotRunning(err)
	}
	return nil
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
// Returns a new client.Filters - does not mutate the input.
func (e *Engine) injectManagedFilter(existing client.Filters) client.Filters {
	// Always create a fresh filter to avoid mutating caller's filters.
	// Context is everything, kid - you don't touch another man's state.
	// client.Filters.Add returns a new Filters (immutable pattern).
	result := existing.Clone()

	// Add managed filter to the copy
	result = result.Add("label", e.managedLabelKey+"="+e.managedLabelValue)
	return result
}

// newManagedFilter creates a new filter with just the managed label.
func (e *Engine) newManagedFilter() client.Filters {
	return client.Filters{}.Add("label", e.managedLabelKey+"="+e.managedLabelValue)
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

func (e *Engine) isManagedLabelPresent(labels map[string]string) bool {
	val, ok := labels[e.managedLabelKey]
	return ok && val == e.managedLabelValue
}

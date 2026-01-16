package whail

import (
	"context"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// NetworkCreate creates a new network with managed labels automatically applied.
// The provided labels are merged with the engine's configured labels.
func (e *Engine) NetworkCreate(ctx context.Context, name string, options network.CreateOptions, extraLabels ...map[string]string) (network.CreateResponse, error) {
	labels := e.networkLabels(extraLabels...)
	// Merge labels into options instead of ignoring them
	if options.Labels == nil {
		options.Labels = labels
	} else {
		options.Labels = MergeLabels(options.Labels, labels)
	}

	// Set default driver if not specified
	if options.Driver == "" {
		options.Driver = "bridge"
	}

	resp, err := e.APIClient.NetworkCreate(ctx, name, options)
	if err != nil {
		return network.CreateResponse{}, ErrNetworkCreateFailed(name, err)
	}
	return resp, nil
}

// NetworkRemove removes a network.
func (e *Engine) NetworkRemove(ctx context.Context, name string) error {
	isManaged, err := e.IsNetworkManaged(ctx, name)
	if err != nil || !isManaged {
		return ErrNetworkNotFound(name, err)
	}
	return e.APIClient.NetworkRemove(ctx, name)
}

// NetworkInspect inspects a network.
func (e *Engine) NetworkInspect(ctx context.Context, name string, options network.InspectOptions) (network.Inspect, error) {
	isManaged, err := e.IsNetworkManaged(ctx, name)
	if err != nil || !isManaged {
		return network.Inspect{}, ErrNetworkNotFound(name, err)
	}
	return e.APIClient.NetworkInspect(ctx, name, options)
}

// NetworkExists checks if a network exists.
func (e *Engine) NetworkExists(ctx context.Context, name string) (bool, error) {
	_, err := e.APIClient.NetworkInspect(ctx, name, network.InspectOptions{})
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// NetworkList lists networks matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) NetworkList(ctx context.Context, extraFilters ...map[string]string) ([]network.Summary, error) {
	f := e.newManagedFilter()
	for _, labels := range extraFilters {
		for k, v := range labels {
			f.Add("label", k+"="+v)
		}
	}
	return e.APIClient.NetworkList(ctx, network.ListOptions{Filters: f})
}

// EnsureNetwork creates a network if it doesn't exist.
// Returns the network ID.
func (e *Engine) EnsureNetwork(ctx context.Context, name string, options network.CreateOptions, verbose bool, extraLabels ...map[string]string) (string, error) {
	exists, err := e.NetworkExists(ctx, name)
	if err != nil {
		return "", err
	}
	if exists {
		info, err := e.NetworkInspect(ctx, name, network.InspectOptions{
			Verbose: verbose,
			Scope:   options.Scope,
		})
		if err != nil {
			return "", err
		}
		return info.ID, nil
	}
	resp, err := e.NetworkCreate(ctx, name, options, extraLabels...)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// IsNetworkManaged checks if a network has the managed label.
func (e *Engine) IsNetworkManaged(ctx context.Context, name string) (bool, error) {
	info, err := e.APIClient.NetworkInspect(ctx, name, network.InspectOptions{})
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}

	val, ok := info.Labels[e.managedLabelKey]
	return ok && val == e.managedLabelValue, nil
}

// NetworksPrune removes all unused managed networks.
// The managed label filter is automatically injected to ensure only
// managed networks are affected.
func (e *Engine) NetworksPrune(ctx context.Context) (network.PruneReport, error) {
	f := e.newManagedFilter()
	report, err := e.APIClient.NetworksPrune(ctx, f)
	if err != nil {
		return network.PruneReport{}, ErrNetworksPruneFailed(err)
	}
	return report, nil
}

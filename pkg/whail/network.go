package whail

import (
	"context"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

// NetworkCreate creates a new network with managed labels automatically applied.
// The provided labels are merged with the engine's configured labels.
func (e *Engine) NetworkCreate(ctx context.Context, name string, options client.NetworkCreateOptions, extraLabels ...map[string]string) (client.NetworkCreateResult, error) {
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
		return client.NetworkCreateResult{}, ErrNetworkCreateFailed(name, err)
	}
	return resp, nil
}

// NetworkRemove removes a network.
func (e *Engine) NetworkRemove(ctx context.Context, name string) (client.NetworkRemoveResult, error) {
	isManaged, err := e.IsNetworkManaged(ctx, name)
	if err != nil || !isManaged {
		return client.NetworkRemoveResult{}, ErrNetworkNotFound(name, err)
	}
	result, err := e.APIClient.NetworkRemove(ctx, name, client.NetworkRemoveOptions{})
	if err != nil {
		return client.NetworkRemoveResult{}, ErrNetworkRemoveFailed(name, err)
	}
	return result, nil
}

// NetworkInspect inspects a network.
func (e *Engine) NetworkInspect(ctx context.Context, name string, options client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
	isManaged, err := e.IsNetworkManaged(ctx, name)
	if err != nil || !isManaged {
		return client.NetworkInspectResult{}, ErrNetworkNotFound(name, err)
	}
	result, err := e.APIClient.NetworkInspect(ctx, name, options)
	if err != nil {
		return client.NetworkInspectResult{}, ErrNetworkNotFound(name, err)
	}
	return result, nil
}

// NetworkExists checks if a network exists.
func (e *Engine) NetworkExists(ctx context.Context, name string) (bool, error) {
	_, err := e.APIClient.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// NetworkList lists networks matching the filter.
// The managed label filter is automatically injected.
func (e *Engine) NetworkList(ctx context.Context, extraFilters ...map[string]string) (client.NetworkListResult, error) {
	f := e.newManagedFilter()
	for _, labels := range extraFilters {
		for k, v := range labels {
			f = f.Add("label", k+"="+v)
		}
	}
	result, err := e.APIClient.NetworkList(ctx, client.NetworkListOptions{Filters: f})
	if err != nil {
		return client.NetworkListResult{}, ErrNetworkListFailed(err)
	}
	return result, nil
}

// EnsureNetwork creates a network if it doesn't exist.
// Returns the network ID.
func (e *Engine) EnsureNetwork(ctx context.Context, name string, options client.NetworkCreateOptions, verbose bool, extraLabels ...map[string]string) (string, error) {
	exists, err := e.NetworkExists(ctx, name)
	if err != nil {
		return "", ErrNetworkEnsureFailed(name, err)
	}
	if exists {
		info, err := e.NetworkInspect(ctx, name, client.NetworkInspectOptions{
			Verbose: verbose,
			Scope:   options.Scope,
		})
		if err != nil {
			return "", ErrNetworkEnsureFailed(name, err)
		}
		return info.Network.ID, nil
	}
	resp, err := e.NetworkCreate(ctx, name, options, extraLabels...)
	if err != nil {
		return "", ErrNetworkEnsureFailed(name, err)
	}
	return resp.ID, nil
}

// IsNetworkManaged checks if a network has the managed label.
func (e *Engine) IsNetworkManaged(ctx context.Context, name string) (bool, error) {
	result, err := e.APIClient.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	val, ok := result.Network.Labels[e.managedLabelKey]
	return ok && val == e.managedLabelValue, nil
}

// NetworksPrune removes all unused managed networks.
// The managed label filter is automatically injected to ensure only
// managed networks are affected.
func (e *Engine) NetworksPrune(ctx context.Context) (client.NetworkPruneResult, error) {
	f := e.newManagedFilter()
	result, err := e.APIClient.NetworkPrune(ctx, client.NetworkPruneOptions{Filters: f})
	if err != nil {
		return client.NetworkPruneResult{}, ErrNetworksPruneFailed(err)
	}
	return result, nil
}

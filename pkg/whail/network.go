package whail

import (
	"context"
	"errors"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// EnsureNetworkOptions configures network creation/ensure behavior.
// Embeds Docker SDK's NetworkCreateOptions for forward compatibility.
type EnsureNetworkOptions struct {
	client.NetworkCreateOptions // Embedded: Driver, Options, Labels, Scope, etc.

	Name        string // Network name (required)
	Verbose     bool   // Verbose output during ensure
	ExtraLabels Labels // Additional labels to merge with managed labels
}

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
	// Ensure managed label cannot be overridden by extra labels.
	options.Labels[e.managedLabelKey] = e.managedLabelValue

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
func (e *Engine) EnsureNetwork(ctx context.Context, opts EnsureNetworkOptions) (string, error) {
	if opts.Name == "" {
		return "", errors.New("network name is required")
	}

	exists, err := e.NetworkExists(ctx, opts.Name)
	if err != nil {
		return "", ErrNetworkEnsureFailed(opts.Name, err)
	}
	if exists {
		info, err := e.NetworkInspect(ctx, opts.Name, client.NetworkInspectOptions{
			Verbose: opts.Verbose,
			Scope:   opts.Scope,
		})
		if err != nil {
			return "", ErrNetworkEnsureFailed(opts.Name, err)
		}
		return info.Network.ID, nil
	}

	resp, err := e.NetworkCreate(ctx, opts.Name, opts.NetworkCreateOptions, opts.ExtraLabels...)
	if err != nil {
		return "", ErrNetworkEnsureFailed(opts.Name, err)
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

// NetworkConnect connects a container to a network.
// Only connects to managed networks.
func (e *Engine) NetworkConnect(ctx context.Context, network, containerID string, config *network.EndpointSettings) (client.NetworkConnectResult, error) {
	isManaged, err := e.IsNetworkManaged(ctx, network)
	if err != nil {
		return client.NetworkConnectResult{}, ErrNetworkConnectFailed(network, containerID, err)
	}
	if !isManaged {
		return client.NetworkConnectResult{}, ErrNetworkNotFound(network, nil)
	}

	opts := client.NetworkConnectOptions{
		Container:      containerID,
		EndpointConfig: config,
	}
	result, err := e.APIClient.NetworkConnect(ctx, network, opts)
	if err != nil {
		return client.NetworkConnectResult{}, ErrNetworkConnectFailed(network, containerID, err)
	}
	return result, nil
}

// NetworkDisconnect disconnects a container from a network.
// Only disconnects from managed networks.
func (e *Engine) NetworkDisconnect(ctx context.Context, network, containerID string, force bool) (client.NetworkDisconnectResult, error) {
	isManaged, err := e.IsNetworkManaged(ctx, network)
	if err != nil {
		return client.NetworkDisconnectResult{}, ErrNetworkDisconnectFailed(network, containerID, err)
	}
	if !isManaged {
		return client.NetworkDisconnectResult{}, ErrNetworkNotFound(network, nil)
	}

	opts := client.NetworkDisconnectOptions{
		Container: containerID,
		Force:     force,
	}
	result, err := e.APIClient.NetworkDisconnect(ctx, network, opts)
	if err != nil {
		return client.NetworkDisconnectResult{}, ErrNetworkDisconnectFailed(network, containerID, err)
	}
	return result, nil
}

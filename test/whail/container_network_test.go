package whail_test

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/pkg/whail"
)

// TestContainerCreateEnsureNetworkReplacesDefaultEndpoint sends the request the clawker CLI sends
// when no --network flag is set: an endpoint under the "default" key. The container must start on
// the ensured network only.
func TestContainerCreateEnsureNetworkReplacesDefaultEndpoint(t *testing.T) {
	requireBuildKit(t)

	engine := newTestEngine(t)
	tag := uniqueTag("ensure-net-default-ep")
	buildImage(t, engine, tag, writeDockerfile(t, `FROM alpine:latest`), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	networkName := "whail-test-ensure-net-default-ep-" + time.Now().Format("150405.000000")
	callerNetworking := new(network.NetworkingConfig)
	callerNetworking.EndpointsConfig = map[string]*network.EndpointSettings{"default": new(network.EndpointSettings)}
	config := new(container.Config)
	config.Image = tag
	config.Cmd = []string{"sleep", "300"}
	ensure := new(whail.EnsureNetworkOptions)
	ensure.Name = networkName
	var createOpts whail.ContainerCreateOptions
	createOpts.Config = config
	createOpts.HostConfig = new(container.HostConfig)
	createOpts.NetworkingConfig = callerNetworking
	createOpts.EnsureNetwork = ensure

	resp, err := engine.ContainerCreate(ctx, createOpts)
	if err != nil {
		t.Fatalf("ContainerCreate with EnsureNetwork failed: %v", err)
	}
	t.Cleanup(func() { removeContainerAndNetwork(t, engine, resp.ID, networkName) })

	var startOpts whail.ContainerStartOptions
	startOpts.ContainerID = resp.ID
	if _, err = engine.ContainerStart(ctx, startOpts); err != nil {
		t.Fatalf("ContainerStart failed: %v", err)
	}

	var inspectOpts client.ContainerInspectOptions
	inspect, err := engine.APIClient.ContainerInspect(ctx, resp.ID, inspectOpts)
	if err != nil {
		t.Fatalf("Failed to inspect container: %v", err)
	}
	networks := inspect.Container.NetworkSettings.Networks
	names := slices.Sorted(maps.Keys(networks))
	if len(networks) != 1 {
		t.Fatalf("Container should be on exactly 1 network, got %d: %v", len(networks), names)
	}
	if _, ok := networks[networkName]; !ok {
		t.Fatalf("Container is not connected to network %q, connected to: %v", networkName, names)
	}
	if _, ok := callerNetworking.EndpointsConfig["default"]; !ok || len(callerNetworking.EndpointsConfig) != 1 {
		t.Errorf("caller NetworkingConfig was mutated: %v", callerNetworking.EndpointsConfig)
	}
}

// removeContainerAndNetwork stops the container with a short grace period, force-removes it and
// then removes its network. Failures are logged, not fatal: cleanup must not mask the test result.
func removeContainerAndNetwork(t *testing.T, engine *whail.Engine, containerID, networkName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stopTimeout := 5
	if _, err := engine.ContainerStop(ctx, containerID, &stopTimeout); err != nil {
		t.Logf("stop container: %v", err)
	}
	if _, err := engine.ContainerRemove(ctx, containerID, true); err != nil {
		t.Logf("remove container: %v", err)
	}
	if _, err := engine.NetworkRemove(ctx, networkName); err != nil {
		t.Logf("remove network: %v", err)
	}
}

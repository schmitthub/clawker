package internals

import (
	"context"
	"io"
	"testing"

	"github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/test/harness"
)

// testDockerCleanup removes all test containers and volumes created by the client.
func testDockerCleanup(ctx context.Context, t *testing.T, c *docker.Client) {
	t.Helper()

	containers, err := c.ListContainers(ctx, true)
	if err != nil {
		t.Logf("cleanup: failed to list containers: %v", err)
		return
	}

	for _, ctr := range containers {
		if err := c.RemoveContainerWithVolumes(ctx, ctr.ID, true); err != nil {
			t.Logf("cleanup: failed to remove container %s: %v", ctr.Name, err)
		}
	}
}

func TestNewClient_Integration(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	client, err := docker.NewClient(ctx, _testCfg, docker.WithLabels(docker.TestLabelConfig(_testCfg, t.Name())))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	// Verify embedded engine is configured correctly
	if client.Engine == nil {
		t.Error("embedded Engine is nil")
	}

	// Verify managed label key matches clawker convention
	if got := client.ManagedLabelKey(); got != _testCfg.LabelManaged() {
		t.Errorf("ManagedLabelKey() = %q, want %q", got, _testCfg.LabelManaged())
	}

	// Verify managed label value
	if got := client.ManagedLabelValue(); got != _testCfg.ManagedLabelValue() {
		t.Errorf("ManagedLabelValue() = %q, want %q", got, _testCfg.ManagedLabelValue())
	}
}

func TestListContainersEmpty_Integration(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	client, err := docker.NewClient(ctx, _testCfg, docker.WithLabels(docker.TestLabelConfig(_testCfg, t.Name())))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	t.Cleanup(func() { testDockerCleanup(context.Background(), t, client) })

	// List should work even when no containers exist
	containers, err := client.ListContainers(ctx, true)
	if err != nil {
		t.Errorf("ListContainers() error = %v", err)
	}
	// Note: may have existing containers from other tests, so just verify no error
	_ = containers
}

func TestClientContainerLifecycle_Integration(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	client, err := docker.NewClient(ctx, _testCfg, docker.WithLabels(docker.TestLabelConfig(_testCfg, t.Name())))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	t.Cleanup(func() { testDockerCleanup(context.Background(), t, client) })

	// Pull alpine image first to ensure it's available
	pullReader, err := client.ImagePull(ctx, "alpine:latest", dockerclient.ImagePullOptions{})
	if err != nil {
		t.Fatalf("ImagePull() error = %v", err)
	}
	io.Copy(io.Discard, pullReader)
	pullReader.Close()

	project := "clienttest"
	agent := "lifecycle"
	containerName, err := docker.ContainerName(project, agent)
	if err != nil {
		t.Fatal(err)
	}

	// Create container using embedded engine methods with our labels
	labels := client.ContainerLabels(project, agent, "test", "alpine:latest", "/test")

	createResp, err := client.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Config: &container.Config{
			Image:  "alpine:latest",
			Cmd:    []string{"sleep", "300"},
			Labels: labels,
		},
		Name: containerName,
	})
	if err != nil {
		t.Fatalf("ContainerCreate() error = %v", err)
	}

	// Start container
	if _, err := client.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: createResp.ID}); err != nil {
		t.Fatalf("ContainerStart() error = %v", err)
	}

	// Find by agent
	foundName, foundCtr, err := client.FindContainerByAgent(ctx, project, agent)
	if err != nil {
		t.Errorf("FindContainerByAgent() error = %v", err)
	}
	if foundName != containerName {
		t.Errorf("FindContainerByAgent() name = %q, want %q", foundName, containerName)
	}
	if foundCtr == nil {
		t.Error("FindContainerByAgent() returned nil container")
	}

	// List by project
	containers, err := client.ListContainersByProject(ctx, project, true)
	if err != nil {
		t.Errorf("ListContainersByProject() error = %v", err)
	}
	found := false
	for _, c := range containers {
		if c.ID == createResp.ID {
			found = true
			if c.Project != project {
				t.Errorf("container.ProjectCfg = %q, want %q", c.Project, project)
			}
			if c.Agent != agent {
				t.Errorf("container.Agent = %q, want %q", c.Agent, agent)
			}
			break
		}
	}
	if !found {
		t.Error("container not found in ListContainersByProject()")
	}

	// Remove with volumes
	if err := client.RemoveContainerWithVolumes(ctx, createResp.ID, true); err != nil {
		t.Errorf("RemoveContainerWithVolumes() error = %v", err)
	}

	// Verify removed
	_, ctr, err := client.FindContainerByAgent(ctx, project, agent)
	if err != nil {
		t.Errorf("FindContainerByAgent() error after remove = %v", err)
	}
	if ctr != nil {
		t.Error("container should not exist after remove")
	}
}

func TestFindContainerByAgentNotFound_Integration(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	client, err := docker.NewClient(ctx, _testCfg, docker.WithLabels(docker.TestLabelConfig(_testCfg, t.Name())))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	// Finding non-existent container should return nil, not error
	name, ctr, err := client.FindContainerByAgent(ctx, "nonexistent", "container")
	if err != nil {
		t.Errorf("FindContainerByAgent() error = %v, want nil", err)
	}
	if ctr != nil {
		t.Error("FindContainerByAgent() should return nil for non-existent container")
	}
	// Name should still be returned even if container doesn't exist
	expectedName, err := docker.ContainerName("nonexistent", "container")
	if err != nil {
		t.Fatalf("ContainerName() error = %v", err)
	}
	if name != expectedName {
		t.Errorf("FindContainerByAgent() name = %q, want %q", name, expectedName)
	}
}

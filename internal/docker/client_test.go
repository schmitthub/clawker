package docker

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/container"
)

// Note: These tests require Docker to be running.

// testCleanup removes all test containers and volumes
func testCleanup(ctx context.Context, t *testing.T, c *Client) {
	t.Helper()

	// List all test containers
	containers, err := c.ListContainers(ctx, true)
	if err != nil {
		t.Logf("cleanup: failed to list containers: %v", err)
		return
	}

	// Remove each container with volumes
	for _, ctr := range containers {
		if err := c.RemoveContainerWithVolumes(ctx, ctr.ID, true); err != nil {
			t.Logf("cleanup: failed to remove container %s: %v", ctr.Name, err)
		}
	}
}

func TestNewClient(t *testing.T) {
	ctx := context.Background()

	client, err := NewClient(ctx)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	// Verify embedded engine is configured correctly
	if client.Engine == nil {
		t.Error("embedded Engine is nil")
	}

	// Verify managed label key matches clawker convention
	if got := client.ManagedLabelKey(); got != LabelManaged {
		t.Errorf("ManagedLabelKey() = %q, want %q", got, LabelManaged)
	}

	// Verify managed label value
	if got := client.ManagedLabelValue(); got != ManagedLabelValue {
		t.Errorf("ManagedLabelValue() = %q, want %q", got, ManagedLabelValue)
	}
}

func TestListContainersEmpty(t *testing.T) {
	ctx := context.Background()

	client, err := NewClient(ctx)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	defer testCleanup(ctx, t, client)

	// List should work even when no containers exist
	containers, err := client.ListContainers(ctx, true)
	if err != nil {
		t.Errorf("ListContainers() error = %v", err)
	}
	// Note: may have existing containers from other tests, so just verify no error
	_ = containers
}

func TestClientContainerLifecycle(t *testing.T) {
	ctx := context.Background()

	client, err := NewClient(ctx)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	defer testCleanup(ctx, t, client)

	project := "clienttest"
	agent := "lifecycle"
	containerName := ContainerName(project, agent)

	// Create container using embedded engine methods with our labels
	labels := ContainerLabels(project, agent, "test", "alpine:latest", "/test")

	createResp, err := client.ContainerCreate(ctx, &container.Config{
		Image:  "alpine:latest",
		Cmd:    []string{"sleep", "300"},
		Labels: labels,
	}, nil, nil, nil, containerName)
	if err != nil {
		t.Fatalf("ContainerCreate() error = %v", err)
	}

	// Start container
	if err := client.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
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
				t.Errorf("container.Project = %q, want %q", c.Project, project)
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

func TestFindContainerByAgentNotFound(t *testing.T) {
	ctx := context.Background()

	client, err := NewClient(ctx)
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
	expectedName := ContainerName("nonexistent", "container")
	if name != expectedName {
		t.Errorf("FindContainerByAgent() name = %q, want %q", name, expectedName)
	}
}

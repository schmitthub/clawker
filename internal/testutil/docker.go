package testutil

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/docker"
)

const (
	// TestLabel is the label used to identify test resources
	TestLabel = "com.clawker.test"
	// TestLabelValue is the value for the test label
	TestLabelValue = "true"
	// ClawkerManagedLabel is the standard clawker managed label
	ClawkerManagedLabel = "com.clawker.managed"
)

// RequireDocker skips the test if Docker is not available.
// Use this at the start of integration tests.
func RequireDocker(t *testing.T) {
	t.Helper()
	if !isDockerAvailable() {
		t.Skip("Docker is not available, skipping test")
	}
}

// SkipIfNoDocker is an alias for RequireDocker for clearer semantics.
func SkipIfNoDocker(t *testing.T) {
	RequireDocker(t)
}

// isDockerAvailable checks if Docker is running and accessible.
func isDockerAvailable() bool {
	ctx := context.Background()
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return false
	}
	defer cli.Close()

	_, err = cli.Ping(ctx, client.PingOptions{})
	return err == nil
}

// NewTestClient creates a clawker Docker client for testing.
// The client is automatically closed when the test completes.
func NewTestClient(t *testing.T) *docker.Client {
	t.Helper()
	RequireDocker(t)

	ctx := context.Background()
	c, err := docker.NewClient(ctx)
	if err != nil {
		t.Fatalf("failed to create Docker client: %v", err)
	}

	t.Cleanup(func() {
		c.Close()
	})

	return c
}

// NewRawDockerClient creates a raw Docker SDK client for testing.
// Use this when you need direct SDK access without clawker's label filtering.
// The client is automatically closed when the test completes.
func NewRawDockerClient(t *testing.T) *client.Client {
	t.Helper()
	RequireDocker(t)

	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("failed to create Docker client: %v", err)
	}

	t.Cleanup(func() {
		cli.Close()
	})

	return cli
}

// AddTestLabels adds the test label to a label map for resource identification.
// Returns a new map with test labels added.
func AddTestLabels(labels map[string]string) map[string]string {
	if labels == nil {
		labels = make(map[string]string)
	}
	result := make(map[string]string)
	for k, v := range labels {
		result[k] = v
	}
	result[TestLabel] = TestLabelValue
	return result
}

// AddClawkerLabels adds both clawker and test labels to a map.
// This creates resources that are managed by clawker AND marked for test cleanup.
func AddClawkerLabels(labels map[string]string, project, agent string) map[string]string {
	result := AddTestLabels(labels)
	result[ClawkerManagedLabel] = "true"
	result["com.clawker.project"] = project
	result["com.clawker.agent"] = agent
	return result
}

// CleanupProjectResources removes all Docker resources associated with a project.
// This cleans up containers, volumes, networks, and images with the project label.
func CleanupProjectResources(ctx context.Context, c *docker.Client, project string) error {
	var errs []error

	// Stop and remove containers
	f := client.Filters{}.Add("label", "com.clawker.project="+project)
	containers, err := c.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return err
	}

	for _, cont := range containers.Items {
		// Stop if running
		if cont.State == "running" {
			if _, err := c.ContainerStop(ctx, cont.ID, nil); err != nil {
				errs = append(errs, fmt.Errorf("stop container %s: %w", cont.ID[:12], err))
			}
		}
		// Remove with force
		if _, err := c.ContainerRemove(ctx, cont.ID, true); err != nil {
			errs = append(errs, fmt.Errorf("remove container %s: %w", cont.ID[:12], err))
		}
	}

	// Remove volumes - VolumeList in whail takes map[string]string not Filters
	volumes, err := c.VolumeList(ctx, map[string]string{"com.clawker.project": project})
	if err != nil {
		return err
	}

	for _, vol := range volumes.Items {
		if _, err := c.VolumeRemove(ctx, vol.Name, true); err != nil {
			errs = append(errs, fmt.Errorf("remove volume %s: %w", vol.Name, err))
		}
	}

	// Remove networks
	networks, err := c.NetworkList(ctx, map[string]string{"com.clawker.project": project})
	if err != nil {
		return err
	}

	for _, net := range networks.Items {
		if _, err := c.NetworkRemove(ctx, net.ID); err != nil {
			errs = append(errs, fmt.Errorf("remove network %s: %w", net.ID[:12], err))
		}
	}

	// Remove images
	images, err := c.ImageList(ctx, client.ImageListOptions{
		Filters: f,
	})
	if err != nil {
		return err
	}

	for _, img := range images.Items {
		if _, err := c.ImageRemove(ctx, img.ID, client.ImageRemoveOptions{Force: true}); err != nil {
			errs = append(errs, fmt.Errorf("remove image %s: %w", img.ID[:12], err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %w", errors.Join(errs...))
	}
	return nil
}

// CleanupTestResources removes all Docker resources marked with the test label.
// Use this for cleanup after tests that create resources outside of projects.
func CleanupTestResources(ctx context.Context, cli *client.Client) error {
	var errs []error
	f := client.Filters{}.Add("label", TestLabel+"="+TestLabelValue)

	// Remove containers with test label
	containers, err := cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return err
	}

	for _, cont := range containers.Items {
		if cont.State == "running" {
			if _, err := cli.ContainerStop(ctx, cont.ID, client.ContainerStopOptions{}); err != nil {
				errs = append(errs, fmt.Errorf("stop container %s: %w", cont.ID[:12], err))
			}
		}
		if _, err := cli.ContainerRemove(ctx, cont.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			errs = append(errs, fmt.Errorf("remove container %s: %w", cont.ID[:12], err))
		}
	}

	// Remove volumes with test label
	volumeResp, err := cli.VolumeList(ctx, client.VolumeListOptions{Filters: f})
	if err != nil {
		return err
	}

	for _, vol := range volumeResp.Items {
		if _, err := cli.VolumeRemove(ctx, vol.Name, client.VolumeRemoveOptions{Force: true}); err != nil {
			errs = append(errs, fmt.Errorf("remove volume %s: %w", vol.Name, err))
		}
	}

	// Remove networks with test label
	networks, err := cli.NetworkList(ctx, client.NetworkListOptions{Filters: f})
	if err != nil {
		return err
	}

	for _, net := range networks.Items {
		if _, err := cli.NetworkRemove(ctx, net.ID, client.NetworkRemoveOptions{}); err != nil {
			errs = append(errs, fmt.Errorf("remove network %s: %w", net.ID[:12], err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %w", errors.Join(errs...))
	}
	return nil
}

// ContainerExists checks if a container exists by name.
func ContainerExists(ctx context.Context, cli *client.Client, name string) bool {
	_, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	return err == nil
}

// ContainerIsRunning checks if a container is running by name.
func ContainerIsRunning(ctx context.Context, cli *client.Client, name string) bool {
	info, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return false
	}
	return info.Container.State.Running
}

// WaitForContainerRunning waits for a container to exist and be in running state.
// Polls every 500ms until the context is cancelled or the container is running.
func WaitForContainerRunning(ctx context.Context, cli *client.Client, name string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for container %s to be running: %w", name, ctx.Err())
		case <-ticker.C:
			if ContainerIsRunning(ctx, cli, name) {
				return nil
			}
		}
	}
}

// VolumeExists checks if a volume exists by name.
func VolumeExists(ctx context.Context, cli *client.Client, name string) bool {
	_, err := cli.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	return err == nil
}

// NetworkExists checks if a network exists by name.
func NetworkExists(ctx context.Context, cli *client.Client, name string) bool {
	_, err := cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	return err == nil
}

// BuildTestImageOptions configures image building for tests.
type BuildTestImageOptions struct {
	// SuppressOutput suppresses build output (default: true for cleaner test output)
	SuppressOutput bool
	// NoCache disables Docker build cache (default: false)
	NoCache bool
}

// BuildTestImage builds a clawker image for e2e testing using the harness configuration.
// The image is tagged uniquely for the test and automatically cleaned up when the test completes.
// Returns the image tag that can be used to run containers.
func BuildTestImage(t *testing.T, h *Harness, opts BuildTestImageOptions) string {
	t.Helper()
	RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create a unique tag for this test run (microsecond precision for parallel safety)
	timestamp := time.Now().Format("150405.000000")
	imageTag := fmt.Sprintf("clawker-e2e-%s:%s", h.Project, timestamp)

	// Get Docker client
	dockerClient := NewTestClient(t)

	// Create builder with harness config
	builder := build.NewBuilder(dockerClient, h.Config, h.ProjectDir)

	// Build labels that mark this as a test image
	labels := map[string]string{
		TestLabel:              TestLabelValue,
		ClawkerManagedLabel:    "true",
		"com.clawker.project":  h.Project,
		"com.clawker.e2e-test": "true",
	}

	// Build the image
	buildOpts := build.Options{
		NoCache:        opts.NoCache,
		Labels:         labels,
		SuppressOutput: opts.SuppressOutput,
	}

	t.Logf("Building test image: %s", imageTag)
	if err := builder.Build(ctx, imageTag, buildOpts); err != nil {
		t.Fatalf("failed to build test image: %v", err)
	}

	// Register cleanup to remove the image after the test
	// Reuse existing dockerClient from outer scope (which embeds whail.Engine)
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := dockerClient.ImageRemove(cleanupCtx, imageTag, client.ImageRemoveOptions{Force: true}); err != nil {
			t.Logf("WARNING: failed to cleanup test image %s: %v", imageTag, err)
		}
	})

	return imageTag
}

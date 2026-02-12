package dockertest_test

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/container"
	moby "github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
)

func TestNewFakeClient(t *testing.T) {
	t.Run("constructs without panic", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		if fake == nil {
			t.Fatal("NewFakeClient() returned nil")
		}
		if fake.Client == nil {
			t.Fatal("NewFakeClient().Client is nil")
		}
		if fake.FakeAPI == nil {
			t.Fatal("NewFakeClient().FakeAPI is nil")
		}
	})

	t.Run("client engine is non-nil", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		if fake.Client.Engine == nil {
			t.Fatal("NewFakeClient().Client.Engine is nil")
		}
	})
}

func TestListContainers(t *testing.T) {
	ctx := context.Background()

	t.Run("returns containers from SetupContainerList", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fixture := dockertest.RunningContainerFixture("myapp", "ralph")
		fake.SetupContainerList(fixture)

		containers, err := fake.Client.ListContainers(ctx, true)
		if err != nil {
			t.Fatalf("ListContainers() error: %v", err)
		}
		if len(containers) != 1 {
			t.Fatalf("ListContainers() returned %d containers, want 1", len(containers))
		}
		if containers[0].Project != "myapp" {
			t.Errorf("containers[0].Project = %q, want %q", containers[0].Project, "myapp")
		}
		if containers[0].Agent != "ralph" {
			t.Errorf("containers[0].Agent = %q, want %q", containers[0].Agent, "ralph")
		}
		if containers[0].Status != "running" {
			t.Errorf("containers[0].Status = %q, want %q", containers[0].Status, "running")
		}
	})

	t.Run("returns empty list when no containers", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerList()

		containers, err := fake.Client.ListContainers(ctx, true)
		if err != nil {
			t.Fatalf("ListContainers() error: %v", err)
		}
		if len(containers) != 0 {
			t.Errorf("ListContainers() returned %d containers, want 0", len(containers))
		}
	})

	t.Run("returns multiple containers", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerList(
			dockertest.RunningContainerFixture("myapp", "ralph"),
			dockertest.ContainerFixture("myapp", "dev", "alpine:latest"),
		)

		containers, err := fake.Client.ListContainers(ctx, true)
		if err != nil {
			t.Fatalf("ListContainers() error: %v", err)
		}
		if len(containers) != 2 {
			t.Fatalf("ListContainers() returned %d containers, want 2", len(containers))
		}
		if containers[0].Agent != "ralph" {
			t.Errorf("containers[0].Agent = %q, want %q", containers[0].Agent, "ralph")
		}
		if containers[1].Agent != "dev" {
			t.Errorf("containers[1].Agent = %q, want %q", containers[1].Agent, "dev")
		}
	})

	t.Run("records ContainerList call", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerList()

		_, _ = fake.Client.ListContainers(ctx, true)
		fake.AssertCalled(t, "ContainerList")
	})
}

func TestFindContainerByAgent(t *testing.T) {
	ctx := context.Background()

	t.Run("finds container with matching fixture", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fixture := dockertest.RunningContainerFixture("myapp", "ralph")
		fake.SetupFindContainer("clawker.myapp.ralph", fixture)

		name, ctr, err := fake.Client.FindContainerByAgent(ctx, "myapp", "ralph")
		if err != nil {
			t.Fatalf("FindContainerByAgent() error: %v", err)
		}
		if name != "clawker.myapp.ralph" {
			t.Errorf("name = %q, want %q", name, "clawker.myapp.ralph")
		}
		if ctr == nil {
			t.Fatal("FindContainerByAgent() returned nil container")
		}
		if ctr.ID != fixture.ID {
			t.Errorf("ctr.ID = %q, want %q", ctr.ID, fixture.ID)
		}
	})
}

func TestContainerFixture(t *testing.T) {
	t.Run("includes clawker labels", func(t *testing.T) {
		c := dockertest.ContainerFixture("myapp", "ralph", "node:20")
		if c.Labels[docker.LabelManaged] != "true" {
			t.Errorf("managed label = %q, want %q", c.Labels[docker.LabelManaged], "true")
		}
		if c.Labels[docker.LabelProject] != "myapp" {
			t.Errorf("project label = %q, want %q", c.Labels[docker.LabelProject], "myapp")
		}
		if c.Labels[docker.LabelAgent] != "ralph" {
			t.Errorf("agent label = %q, want %q", c.Labels[docker.LabelAgent], "ralph")
		}
		if c.Labels[docker.LabelImage] != "node:20" {
			t.Errorf("image label = %q, want %q", c.Labels[docker.LabelImage], "node:20")
		}
	})

	t.Run("omits project label when empty", func(t *testing.T) {
		c := dockertest.ContainerFixture("", "ralph", "node:20")
		if _, hasProject := c.Labels[docker.LabelProject]; hasProject {
			t.Error("expected no project label when project is empty")
		}
	})

	t.Run("defaults to exited state", func(t *testing.T) {
		c := dockertest.ContainerFixture("myapp", "ralph", "node:20")
		if string(c.State) != "exited" {
			t.Errorf("State = %q, want %q", c.State, "exited")
		}
	})
}

func TestRunningContainerFixture(t *testing.T) {
	t.Run("is in running state", func(t *testing.T) {
		c := dockertest.RunningContainerFixture("myapp", "ralph")
		if string(c.State) != "running" {
			t.Errorf("State = %q, want %q", c.State, "running")
		}
		if c.Image != "node:20-slim" {
			t.Errorf("Image = %q, want %q", c.Image, "node:20-slim")
		}
	})
}

func TestImageExists(t *testing.T) {
	ctx := context.Background()

	t.Run("returns true when image exists", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupImageExists("node:20", true)

		exists, err := fake.Client.ImageExists(ctx, "node:20")
		if err != nil {
			t.Fatalf("ImageExists() error: %v", err)
		}
		if !exists {
			t.Error("ImageExists() = false, want true")
		}
	})

	t.Run("returns false when image not found", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupImageExists("node:20", false)

		exists, err := fake.Client.ImageExists(ctx, "node:20")
		if err != nil {
			t.Fatalf("ImageExists() error: %v", err)
		}
		if exists {
			t.Error("ImageExists() = true, want false")
		}
	})
}

func TestSetupContainerCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("returns fake container ID", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerCreate()

		resp, err := fake.Client.ContainerCreate(ctx, dockertest.MinimalCreateOpts())
		if err != nil {
			t.Fatalf("ContainerCreate() error: %v", err)
		}
		if resp.ID == "" {
			t.Fatal("ContainerCreate() returned empty ID")
		}
		fake.AssertCalled(t, "ContainerCreate")
	})
}

func TestSetupContainerStart(t *testing.T) {
	ctx := context.Background()

	t.Run("succeeds without error", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerStart()

		_, err := fake.Client.ContainerStart(ctx, dockertest.MinimalStartOpts("sha256:fakecontainer1234567890abcdef"))
		if err != nil {
			t.Fatalf("ContainerStart() error: %v", err)
		}
		fake.AssertCalled(t, "ContainerStart")
	})
}

func TestSetupVolumeExists(t *testing.T) {
	ctx := context.Background()

	t.Run("returns true when volume exists", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupVolumeExists("myvolume", true)

		exists, err := fake.Client.VolumeExists(ctx, "myvolume")
		if err != nil {
			t.Fatalf("VolumeExists() error: %v", err)
		}
		if !exists {
			t.Error("VolumeExists() = false, want true")
		}
	})

	t.Run("returns false when volume not found", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupVolumeExists("myvolume", false)

		exists, err := fake.Client.VolumeExists(ctx, "myvolume")
		if err != nil {
			t.Fatalf("VolumeExists() error: %v", err)
		}
		if exists {
			t.Error("VolumeExists() = true, want false")
		}
	})

	t.Run("wildcard applies to all volumes", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupVolumeExists("", false)

		exists, err := fake.Client.VolumeExists(ctx, "any-volume")
		if err != nil {
			t.Fatalf("VolumeExists() error: %v", err)
		}
		if exists {
			t.Error("VolumeExists() = true, want false for wildcard not-found")
		}
	})
}

func TestSetupNetworkExists(t *testing.T) {
	ctx := context.Background()

	t.Run("returns true when network exists", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupNetworkExists("clawker-net", true)

		exists, err := fake.Client.NetworkExists(ctx, "clawker-net")
		if err != nil {
			t.Fatalf("NetworkExists() error: %v", err)
		}
		if !exists {
			t.Error("NetworkExists() = false, want true")
		}
	})

	t.Run("returns false when network not found", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupNetworkExists("clawker-net", false)

		exists, err := fake.Client.NetworkExists(ctx, "clawker-net")
		if err != nil {
			t.Fatalf("NetworkExists() error: %v", err)
		}
		if exists {
			t.Error("NetworkExists() = true, want false")
		}
	})
}

func TestSetupImageList(t *testing.T) {
	ctx := context.Background()

	t.Run("returns image summaries", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupImageList(dockertest.ImageSummaryFixture("alpine:latest"))

		result, err := fake.Client.ImageList(ctx, moby.ImageListOptions{})
		if err != nil {
			t.Fatalf("ImageList() error: %v", err)
		}
		if len(result.Items) != 1 {
			t.Fatalf("ImageList() returned %d items, want 1", len(result.Items))
		}
		if result.Items[0].RepoTags[0] != "alpine:latest" {
			t.Errorf("result.Items[0].RepoTags[0] = %q, want %q", result.Items[0].RepoTags[0], "alpine:latest")
		}
	})

	t.Run("returns empty list", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupImageList()

		result, err := fake.Client.ImageList(ctx, moby.ImageListOptions{})
		if err != nil {
			t.Fatalf("ImageList() error: %v", err)
		}
		if len(result.Items) != 0 {
			t.Errorf("ImageList() returned %d items, want 0", len(result.Items))
		}
	})
}

func TestSetupBuildKit(t *testing.T) {
	ctx := context.Background()

	t.Run("BuildKit path is invoked", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		capture := fake.SetupBuildKit()

		err := fake.Client.BuildImage(ctx, nil, dockertest.BuildKitBuildOpts(
			"test:latest", "/tmp/build",
		))
		if err != nil {
			t.Fatalf("BuildImage() error: %v", err)
		}
		if capture.CallCount != 1 {
			t.Fatalf("expected BuildKit builder to be called once, got %d", capture.CallCount)
		}
		if capture.Opts.Tags[0] != "test:latest" {
			t.Errorf("expected tag %q, got %q", "test:latest", capture.Opts.Tags[0])
		}
		if capture.Opts.ContextDir != "/tmp/build" {
			t.Errorf("expected ContextDir %q, got %q", "/tmp/build", capture.Opts.ContextDir)
		}
	})

	t.Run("managed labels are injected", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		capture := fake.SetupBuildKit()

		err := fake.Client.BuildImage(ctx, nil, dockertest.BuildKitBuildOpts(
			"test:latest", "/tmp/build",
		))
		if err != nil {
			t.Fatalf("BuildImage() error: %v", err)
		}

		if capture.Opts.Labels[docker.LabelManaged] != "true" {
			t.Errorf("expected managed label %q=true, got %q", docker.LabelManaged, capture.Opts.Labels[docker.LabelManaged])
		}
	})
}

func TestSetupVolumeCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("succeeds and returns volume with name", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupVolumeCreate()

		result, err := fake.Client.VolumeCreate(ctx, moby.VolumeCreateOptions{
			Name: "test-volume",
		})
		if err != nil {
			t.Fatalf("VolumeCreate() error: %v", err)
		}
		if result.Volume.Name != "test-volume" {
			t.Errorf("VolumeCreate() name = %q, want %q", result.Volume.Name, "test-volume")
		}
		fake.AssertCalled(t, "VolumeCreate")
	})
}

func TestSetupNetworkCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("succeeds and returns network ID", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupNetworkCreate()

		result, err := fake.Client.NetworkCreate(ctx, "test-net", moby.NetworkCreateOptions{})
		if err != nil {
			t.Fatalf("NetworkCreate() error: %v", err)
		}
		if result.ID == "" {
			t.Error("NetworkCreate() returned empty ID")
		}
		fake.AssertCalled(t, "NetworkCreate")
	})
}

func TestSetupContainerAttach(t *testing.T) {
	ctx := context.Background()

	t.Run("returns hijacked response that reads EOF", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerAttach()

		containerID := "sha256:fakecontainer1234567890abcdef"
		result, err := fake.Client.ContainerAttach(ctx, containerID, moby.ContainerAttachOptions{
			Stream: true,
			Stdout: true,
			Stderr: true,
		})
		if err != nil {
			t.Fatalf("ContainerAttach() error: %v", err)
		}
		defer result.Close()

		// Server side is closed, so reading should return EOF
		buf := make([]byte, 1)
		_, readErr := result.Reader.Read(buf)
		if readErr == nil {
			t.Error("expected read error (EOF) from closed pipe, got nil")
		}
		fake.AssertCalled(t, "ContainerAttach")
	})
}

func TestSetupContainerWait(t *testing.T) {
	ctx := context.Background()

	t.Run("returns exit code 0", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerWait(0)

		containerID := "sha256:fakecontainer1234567890abcdef"
		waitResult := fake.Client.ContainerWait(ctx, containerID, container.WaitConditionNextExit)

		select {
		case resp := <-waitResult.Result:
			if resp.StatusCode != 0 {
				t.Errorf("ContainerWait() exit code = %d, want 0", resp.StatusCode)
			}
		case err := <-waitResult.Error:
			t.Fatalf("ContainerWait() error: %v", err)
		}
		fake.AssertCalled(t, "ContainerWait")
	})

	t.Run("returns non-zero exit code", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerWait(42)

		containerID := "sha256:fakecontainer1234567890abcdef"
		waitResult := fake.Client.ContainerWait(ctx, containerID, container.WaitConditionNextExit)

		select {
		case resp := <-waitResult.Result:
			if resp.StatusCode != 42 {
				t.Errorf("ContainerWait() exit code = %d, want 42", resp.StatusCode)
			}
		case err := <-waitResult.Error:
			t.Fatalf("ContainerWait() error: %v", err)
		}
	})
}

func TestSetupContainerResize(t *testing.T) {
	ctx := context.Background()

	t.Run("succeeds without error", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerResize()

		containerID := "sha256:fakecontainer1234567890abcdef"
		_, err := fake.Client.ContainerResize(ctx, containerID, 24, 80)
		if err != nil {
			t.Fatalf("ContainerResize() error: %v", err)
		}
		fake.AssertCalled(t, "ContainerResize")
	})
}

func TestSetupContainerRemove(t *testing.T) {
	ctx := context.Background()

	t.Run("succeeds without error", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerRemove()

		containerID := "sha256:fakecontainer1234567890abcdef"
		_, err := fake.Client.ContainerRemove(ctx, containerID, false)
		if err != nil {
			t.Fatalf("ContainerRemove() error: %v", err)
		}
		fake.AssertCalled(t, "ContainerRemove")
	})
}

func TestAssertions(t *testing.T) {
	ctx := context.Background()

	t.Run("AssertCalled passes after call", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerList()
		_, _ = fake.Client.ListContainers(ctx, true)
		fake.AssertCalled(t, "ContainerList")
	})

	t.Run("AssertNotCalled passes when not called", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.AssertNotCalled(t, "ContainerList")
	})

	t.Run("AssertCalledN with exact count", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerList()
		_, _ = fake.Client.ListContainers(ctx, true)
		_, _ = fake.Client.ListContainers(ctx, false)
		fake.AssertCalledN(t, "ContainerList", 2)
	})

	t.Run("Reset clears call log", func(t *testing.T) {
		fake := dockertest.NewFakeClient()
		fake.SetupContainerList()
		_, _ = fake.Client.ListContainers(ctx, true)
		fake.AssertCalled(t, "ContainerList")

		fake.Reset()
		fake.AssertNotCalled(t, "ContainerList")
	})
}

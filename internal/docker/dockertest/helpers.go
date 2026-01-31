package dockertest

import (
	"context"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	dockerimage "github.com/moby/moby/api/types/image"

	"github.com/schmitthub/clawker/internal/docker"
)

// ContainerFixture builds a container.Summary with proper clawker labels.
// The container is in "exited" state by default.
func ContainerFixture(project, agent, image string) container.Summary {
	name := docker.ContainerName(project, agent)
	labels := map[string]string{
		docker.LabelManaged: docker.ManagedLabelValue,
		docker.LabelAgent:   agent,
		docker.LabelImage:   image,
	}
	if project != "" {
		labels[docker.LabelProject] = project
	}

	return container.Summary{
		ID:     "sha256:" + name + "-fake-id",
		Names:  []string{"/" + name},
		Image:  image,
		State:  "exited",
		Labels: labels,
	}
}

// RunningContainerFixture builds a container.Summary in "running" state
// with a default image of "node:20-slim".
func RunningContainerFixture(project, agent string) container.Summary {
	c := ContainerFixture(project, agent, "node:20-slim")
	c.State = "running"
	return c
}

// SetupContainerList configures the fake to return the given containers
// from ContainerList calls. The whail jail will inject the managed label
// filter automatically, so callers only need to provide containers with
// proper clawker labels.
func (f *FakeClient) SetupContainerList(containers ...container.Summary) {
	f.FakeAPI.ContainerListFn = func(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{
			Items: containers,
		}, nil
	}
}

// SetupFindContainer configures the fake so that FindContainerByAgent
// returns the given container when the matching name is inspected.
// It sets up ContainerInspect to return managed inspect data plus a
// ContainerList that includes the container (FindContainerByName uses
// list + name filter internally).
func (f *FakeClient) SetupFindContainer(name string, c container.Summary) {
	// FindContainerByName uses ContainerList with name filter
	f.FakeAPI.ContainerListFn = func(_ context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error) {
		return client.ContainerListResult{
			Items: []container.Summary{c},
		}, nil
	}

	// ContainerInspect is used by whail's jail to verify management
	f.FakeAPI.ContainerInspectFn = func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		if id != c.ID {
			return client.ContainerInspectResult{}, notFoundError(id)
		}
		return client.ContainerInspectResult{
			Container: container.InspectResponse{
				ID:   c.ID,
				Name: "/" + name,
				Config: &container.Config{
					Image:  c.Image,
					Labels: c.Labels,
				},
			},
		}, nil
	}
}

// SetupImageExists configures the fake to report whether an image exists.
// When exists is true, ImageInspect returns a minimal result.
// When exists is false, ImageInspect returns a not-found error.
func (f *FakeClient) SetupImageExists(ref string, exists bool) {
	f.FakeAPI.ImageInspectFn = func(_ context.Context, image string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		if image != ref {
			return client.ImageInspectResult{}, notFoundError(image)
		}
		if exists {
			return client.ImageInspectResult{
				InspectResponse: dockerimage.InspectResponse{
					ID: "sha256:fake-image-id",
				},
			}, nil
		}
		return client.ImageInspectResult{}, notFoundError(image)
	}
}

// notFoundError creates an error that satisfies errdefs.IsNotFound.
type errNotFound struct {
	msg string
}

func (e errNotFound) Error() string { return e.msg }
func (e errNotFound) NotFound()     {}

func notFoundError(ref string) error {
	return errNotFound{msg: "No such image: " + ref}
}

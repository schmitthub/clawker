package integration

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/resolver"
	"github.com/schmitthub/clawker/test/harness"
)

const resolverTestImageBase = "alpine:latest"

// imageResolverState holds per-test state for image resolver tests.
type imageResolverState struct {
	dockerClient    *docker.Client
	projectName     string
	latestImageTag  string
	versionedTag    string
	otherProjectTag string
}

// setupImageResolverTests creates test images and returns test state.
// Cleanup is registered via t.Cleanup.
func setupImageResolverTests(t *testing.T) *imageResolverState {
	t.Helper()
	harness.RequireDocker(t)

	ctx := context.Background()

	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("failed to create Docker client: %v", err)
	}

	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Fatalf("Docker not running: %v", err)
	}

	// Create unique identifiers for this test run
	timestamp := time.Now().UnixNano()
	state := &imageResolverState{
		projectName:     fmt.Sprintf("resolver-test-%d", timestamp),
		latestImageTag:  fmt.Sprintf("resolver-test-%d:latest", timestamp),
		versionedTag:    fmt.Sprintf("resolver-test-%d:v1.0", timestamp),
		otherProjectTag: fmt.Sprintf("resolver-test-other-%d:latest", timestamp),
	}

	// Pull base image
	reader, err := cli.ImagePull(ctx, resolverTestImageBase, client.ImagePullOptions{})
	if err != nil {
		t.Fatalf("failed to pull base image: %v", err)
	}
	defer reader.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)

	// Create docker.Client for tests
	state.dockerClient, err = docker.NewClient(ctx)
	if err != nil {
		t.Fatalf("failed to create docker client: %v", err)
	}

	// Build test images
	latestID, err := buildResolverTestImage(ctx, cli, state.latestImageTag, map[string]string{
		docker.LabelManaged: docker.ManagedLabelValue,
		docker.LabelProject: state.projectName,
	})
	if err != nil {
		t.Fatalf("failed to build latest image: %v", err)
	}

	versionedID, err := buildResolverTestImage(ctx, cli, state.versionedTag, map[string]string{
		docker.LabelManaged: docker.ManagedLabelValue,
		docker.LabelProject: state.projectName,
	})
	if err != nil {
		t.Fatalf("failed to build versioned image: %v", err)
	}

	otherID, err := buildResolverTestImage(ctx, cli, state.otherProjectTag, map[string]string{
		docker.LabelManaged: docker.ManagedLabelValue,
		docker.LabelProject: "other-project",
	})
	if err != nil {
		t.Fatalf("failed to build other project image: %v", err)
	}

	// Register cleanup
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		state.dockerClient.Close()

		for _, id := range []string{latestID, versionedID, otherID} {
			if id != "" {
				if _, err := cli.ImageRemove(cleanupCtx, id, client.ImageRemoveOptions{Force: true, PruneChildren: true}); err != nil {
					t.Logf("WARNING: failed to remove test image %s: %v", id[:12], err)
				}
			}
		}
		for _, tag := range []string{state.latestImageTag, state.versionedTag, state.otherProjectTag} {
			if _, err := cli.ImageRemove(cleanupCtx, tag, client.ImageRemoveOptions{Force: true}); err != nil {
				t.Logf("WARNING: failed to remove test image %s: %v", tag, err)
			}
		}
		cli.Close()
	})

	return state
}

func buildResolverTestImage(ctx context.Context, cli *client.Client, tag string, labels map[string]string) (string, error) {
	dockerfile := "FROM " + resolverTestImageBase + "\nCMD [\"echo\", \"test\"]\n"
	buildOpts := client.ImageBuildOptions{
		Tags:       []string{tag},
		Labels:     labels,
		Dockerfile: "Dockerfile",
		Remove:     true,
	}

	tarBuf := new(bytes.Buffer)
	if err := createResolverTestTar(tarBuf, dockerfile); err != nil {
		return "", err
	}

	resp, err := cli.ImageBuild(ctx, tarBuf, buildOpts)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)

	inspect, err := cli.ImageInspect(ctx, tag)
	if err != nil {
		return "", err
	}

	return inspect.ID, nil
}

func createResolverTestTar(buf *bytes.Buffer, dockerfile string) error {
	name := "Dockerfile"
	content := []byte(dockerfile)
	size := len(content)

	header := make([]byte, 512)
	copy(header[0:100], name)
	copy(header[100:108], fmt.Sprintf("%07o\x00", 0644))
	copy(header[108:116], fmt.Sprintf("%07o\x00", 0))
	copy(header[116:124], fmt.Sprintf("%07o\x00", 0))
	copy(header[124:136], fmt.Sprintf("%011o\x00", size))
	copy(header[136:148], fmt.Sprintf("%011o\x00", time.Now().Unix()))
	header[156] = '0'

	copy(header[148:156], "        ")
	var checksum int64
	for _, b := range header {
		checksum += int64(b)
	}
	copy(header[148:156], fmt.Sprintf("%06o\x00 ", checksum))

	buf.Write(header)
	buf.Write(content)

	padding := 512 - (size % 512)
	if padding < 512 {
		buf.Write(make([]byte, padding))
	}

	buf.Write(make([]byte, 1024))

	return nil
}

func TestFindProjectImage_Integration(t *testing.T) {
	state := setupImageResolverTests(t)
	ctx := context.Background()

	t.Run("image matches with :latest tag", func(t *testing.T) {
		result, err := resolver.FindProjectImage(ctx, state.dockerClient, state.projectName)
		if err != nil {
			t.Errorf("FindProjectImage() unexpected error = %v", err)
			return
		}
		if result == "" {
			t.Errorf("FindProjectImage() returned empty string, expected image with :latest suffix")
			return
		}
		expectedSuffix := ":latest"
		if len(result) < len(expectedSuffix) || result[len(result)-len(expectedSuffix):] != expectedSuffix {
			t.Errorf("FindProjectImage() = %q, want suffix %q", result, expectedSuffix)
		}
	})

	t.Run("no matching images for nonexistent project", func(t *testing.T) {
		result, err := resolver.FindProjectImage(ctx, state.dockerClient, "nonexistent-project-xyz")
		if err != nil {
			t.Errorf("FindProjectImage() unexpected error = %v", err)
			return
		}
		if result != "" {
			t.Errorf("FindProjectImage() = %q, want empty string for nonexistent project", result)
		}
	})

	t.Run("finds correct project image among multiple", func(t *testing.T) {
		result, err := resolver.FindProjectImage(ctx, state.dockerClient, "other-project")
		if err != nil {
			t.Errorf("FindProjectImage() unexpected error = %v", err)
			return
		}
		if result == "" {
			t.Errorf("FindProjectImage() returned empty string, expected image for other-project")
			return
		}
		if result != state.otherProjectTag {
			t.Errorf("FindProjectImage() = %q, want %q", result, state.otherProjectTag)
		}
	})
}

func TestFindProjectImage_NoLatestTag(t *testing.T) {
	state := setupImageResolverTests(t)
	ctx := context.Background()

	result, err := resolver.FindProjectImage(ctx, state.dockerClient, "project-with-absolutely-no-images")
	if err != nil {
		t.Errorf("FindProjectImage() unexpected error: %v", err)
		return
	}
	if result != "" {
		t.Errorf("FindProjectImage() = %q, want empty string for project with no images", result)
	}
}

func TestResolveImageWithSource_ProjectImage(t *testing.T) {
	state := setupImageResolverTests(t)
	ctx := context.Background()

	t.Run("finds project image with :latest tag", func(t *testing.T) {
		cfg := &config.Config{
			Project:      state.projectName,
			DefaultImage: "fallback:latest",
		}

		result, err := resolver.ResolveImageWithSource(ctx, state.dockerClient, cfg, nil)
		if err != nil {
			t.Fatalf("ResolveImageWithSource() unexpected error: %v", err)
		}

		if result == nil {
			t.Fatal("ResolveImageWithSource() returned nil, expected project image")
		}

		if result.Source != resolver.ImageSourceProject {
			t.Errorf("ResolveImageWithSource().Source = %q, want %q", result.Source, resolver.ImageSourceProject)
		}

		if result.Reference != state.latestImageTag {
			t.Errorf("ResolveImageWithSource().Reference = %q, want %q", result.Reference, state.latestImageTag)
		}
	})

	t.Run("falls back to default when no project image", func(t *testing.T) {
		cfg := &config.Config{
			Project:      "nonexistent-project-xyz",
			DefaultImage: "fallback:latest",
		}

		result, err := resolver.ResolveImageWithSource(ctx, state.dockerClient, cfg, nil)
		if err != nil {
			t.Fatalf("ResolveImageWithSource() unexpected error: %v", err)
		}

		if result == nil {
			t.Fatal("ResolveImageWithSource() returned nil, expected default image")
		}

		if result.Source != resolver.ImageSourceDefault {
			t.Errorf("ResolveImageWithSource().Source = %q, want %q", result.Source, resolver.ImageSourceDefault)
		}

		if result.Reference != "fallback:latest" {
			t.Errorf("ResolveImageWithSource().Reference = %q, want %q", result.Reference, "fallback:latest")
		}
	})
}

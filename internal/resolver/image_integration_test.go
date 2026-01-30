//go:build integration

package resolver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
)

// Integration tests for image resolution functions that require Docker

// Test images created during setup for FindProjectImage tests
var (
	testDockerClient     *docker.Client
	testProjectName      string
	testLatestImageTag   string
	testVersionedTag     string
	testOtherProjectTag  string
	testLatestImageID    string
	testVersionedImageID string
	testOtherProjectID   string
	dockerAvailable      bool
)

const testImageBase = "alpine:latest"

func TestMain(m *testing.M) {
	ctx := context.Background()

	// Check Docker is available
	cli, err := client.New(client.FromEnv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Docker not available: %v\n", err)
		os.Exit(1)
	}
	defer cli.Close()

	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		fmt.Fprintf(os.Stderr, "Docker not running: %v\n", err)
		os.Exit(1)
	}

	dockerAvailable = true

	// Create unique identifiers for this test run
	timestamp := time.Now().UnixNano()
	testProjectName = fmt.Sprintf("resolver-test-%d", timestamp)
	testLatestImageTag = fmt.Sprintf("resolver-test-%d:latest", timestamp)
	testVersionedTag = fmt.Sprintf("resolver-test-%d:v1.0", timestamp)
	testOtherProjectTag = fmt.Sprintf("resolver-test-other-%d:latest", timestamp)

	// Setup: Create test client and images
	if err := setupDockerTests(ctx, cli); err != nil {
		fmt.Fprintf(os.Stderr, "Setup failed: %v\n", err)
		cleanupDockerTests(ctx, cli)
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// Cleanup
	cleanupDockerTests(ctx, cli)

	os.Exit(code)
}

func setupDockerTests(ctx context.Context, cli *client.Client) error {
	var err error

	// Pull base image
	reader, err := cli.ImagePull(ctx, testImageBase, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull base image: %w", err)
	}
	defer reader.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)

	// Create docker.Client for tests
	testDockerClient, err = docker.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}

	// Build test image with :latest-suffixed tag and matching project label
	testLatestImageID, err = buildTestImage(ctx, cli, testLatestImageTag, map[string]string{
		docker.LabelManaged: docker.ManagedLabelValue,
		docker.LabelProject: testProjectName,
	})
	if err != nil {
		return fmt.Errorf("failed to build latest image: %w", err)
	}

	// Build test image with versioned tag (no :latest)
	testVersionedImageID, err = buildTestImage(ctx, cli, testVersionedTag, map[string]string{
		docker.LabelManaged: docker.ManagedLabelValue,
		docker.LabelProject: testProjectName,
	})
	if err != nil {
		return fmt.Errorf("failed to build versioned image: %w", err)
	}

	// Build test image for different project
	testOtherProjectID, err = buildTestImage(ctx, cli, testOtherProjectTag, map[string]string{
		docker.LabelManaged: docker.ManagedLabelValue,
		docker.LabelProject: "other-project",
	})
	if err != nil {
		return fmt.Errorf("failed to build other project image: %w", err)
	}

	return nil
}

func buildTestImage(ctx context.Context, cli *client.Client, tag string, labels map[string]string) (string, error) {
	dockerfile := "FROM " + testImageBase + "\nCMD [\"echo\", \"test\"]\n"
	buildOpts := client.ImageBuildOptions{
		Tags:       []string{tag},
		Labels:     labels,
		Dockerfile: "Dockerfile",
		Remove:     true,
	}

	tarBuf := new(bytes.Buffer)
	if err := createTarWithDockerfile(tarBuf, dockerfile); err != nil {
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

func createTarWithDockerfile(buf *bytes.Buffer, dockerfile string) error {
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

func cleanupDockerTests(ctx context.Context, cli *client.Client) {
	if testDockerClient != nil {
		testDockerClient.Close()
	}

	// Remove test images
	for _, id := range []string{testLatestImageID, testVersionedImageID, testOtherProjectID} {
		if id != "" {
			_, _ = cli.ImageRemove(ctx, id, client.ImageRemoveOptions{Force: true, PruneChildren: true})
		}
	}

	// Also try removing by tag in case IDs didn't work
	for _, tag := range []string{testLatestImageTag, testVersionedTag, testOtherProjectTag} {
		_, _ = cli.ImageRemove(ctx, tag, client.ImageRemoveOptions{Force: true})
	}
}

func TestFindProjectImage_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("image matches with :latest tag", func(t *testing.T) {
		result, err := FindProjectImage(ctx, testDockerClient, testProjectName)
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
		result, err := FindProjectImage(ctx, testDockerClient, "nonexistent-project-xyz")
		if err != nil {
			t.Errorf("FindProjectImage() unexpected error = %v", err)
			return
		}
		if result != "" {
			t.Errorf("FindProjectImage() = %q, want empty string for nonexistent project", result)
		}
	})

	t.Run("finds correct project image among multiple", func(t *testing.T) {
		result, err := FindProjectImage(ctx, testDockerClient, "other-project")
		if err != nil {
			t.Errorf("FindProjectImage() unexpected error = %v", err)
			return
		}
		if result == "" {
			t.Errorf("FindProjectImage() returned empty string, expected image for other-project")
			return
		}
		if result != testOtherProjectTag {
			t.Errorf("FindProjectImage() = %q, want %q", result, testOtherProjectTag)
		}
	})
}

func TestFindProjectImage_NoLatestTag(t *testing.T) {
	ctx := context.Background()

	result, err := FindProjectImage(ctx, testDockerClient, "project-with-absolutely-no-images")
	if err != nil {
		t.Errorf("FindProjectImage() unexpected error: %v", err)
		return
	}
	if result != "" {
		t.Errorf("FindProjectImage() = %q, want empty string for project with no images", result)
	}
}

func TestResolveImageWithSource_ProjectImage(t *testing.T) {
	ctx := context.Background()

	t.Run("finds project image with :latest tag", func(t *testing.T) {
		cfg := &config.Config{
			Project:      testProjectName,
			DefaultImage: "fallback:latest",
		}

		result, err := ResolveImageWithSource(ctx, testDockerClient, cfg, nil)
		if err != nil {
			t.Fatalf("ResolveImageWithSource() unexpected error: %v", err)
		}

		if result == nil {
			t.Fatal("ResolveImageWithSource() returned nil, expected project image")
		}

		if result.Source != ImageSourceProject {
			t.Errorf("ResolveImageWithSource().Source = %q, want %q", result.Source, ImageSourceProject)
		}

		if result.Reference != testLatestImageTag {
			t.Errorf("ResolveImageWithSource().Reference = %q, want %q", result.Reference, testLatestImageTag)
		}
	})

	t.Run("falls back to default when no project image", func(t *testing.T) {
		cfg := &config.Config{
			Project:      "nonexistent-project-xyz",
			DefaultImage: "fallback:latest",
		}

		result, err := ResolveImageWithSource(ctx, testDockerClient, cfg, nil)
		if err != nil {
			t.Fatalf("ResolveImageWithSource() unexpected error: %v", err)
		}

		if result == nil {
			t.Fatal("ResolveImageWithSource() returned nil, expected default image")
		}

		if result.Source != ImageSourceDefault {
			t.Errorf("ResolveImageWithSource().Source = %q, want %q", result.Source, ImageSourceDefault)
		}

		if result.Reference != "fallback:latest" {
			t.Errorf("ResolveImageWithSource().Reference = %q, want %q", result.Reference, "fallback:latest")
		}
	})
}

package whail

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

const (
	testLabelPrefix = "com.whail.test"
	testImageBase   = "alpine:latest"
)

var (
	testEngine       *Engine
	managedImageID   string
	unmanagedImageID string
	testImageTag     string
	unmanagedTag     string
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	// Check Docker is available
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Skipping tests: Docker not available: %v\n", err)
		os.Exit(0)
	}
	defer cli.Close()

	if _, err := cli.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Skipping tests: Docker not running: %v\n", err)
		os.Exit(0)
	}

	// Create unique image tags for this test run
	timestamp := time.Now().UnixNano()
	testImageTag = fmt.Sprintf("whail-test-managed:%d", timestamp)
	unmanagedTag = fmt.Sprintf("whail-test-unmanaged:%d", timestamp)

	// Setup: Create test engine and images
	if err := setup(ctx, cli); err != nil {
		fmt.Fprintf(os.Stderr, "Setup failed: %v\n", err)
		cleanup(ctx, cli)
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// Cleanup: Always remove test images
	cleanup(ctx, cli)

	os.Exit(code)
}

func setup(ctx context.Context, cli *client.Client) error {
	var err error

	// Pull base image
	reader, err := cli.ImagePull(ctx, testImageBase, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull base image: %w", err)
	}
	defer reader.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)

	// Create managed image with whail labels
	managedImageID, err = buildTestImage(ctx, cli, testImageTag, map[string]string{
		testLabelPrefix + ".managed": "true",
		testLabelPrefix + ".purpose": "test",
	})
	if err != nil {
		return fmt.Errorf("failed to build managed image: %w", err)
	}

	// Create unmanaged image without whail labels
	unmanagedImageID, err = buildTestImage(ctx, cli, unmanagedTag, map[string]string{
		"some.other.label": "value",
	})
	if err != nil {
		return fmt.Errorf("failed to build unmanaged image: %w", err)
	}

	// Create test engine
	testEngine, err = NewWithOptions(ctx, EngineOptions{
		LabelPrefix:  testLabelPrefix,
		ManagedLabel: "managed",
		Labels: LabelConfig{
			Default: map[string]string{testLabelPrefix + ".purpose": "test"},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create engine: %w", err)
	}

	return nil
}

func buildTestImage(ctx context.Context, cli *client.Client, tag string, labels map[string]string) (string, error) {
	dockerfile := "FROM " + testImageBase + "\nCMD [\"echo\", \"test\"]\n"
	buildOpts := types.ImageBuildOptions{
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

	inspect, _, err := cli.ImageInspectWithRaw(ctx, tag)
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

func cleanup(ctx context.Context, cli *client.Client) {
	if testEngine != nil {
		testEngine.Close()
	}

	if managedImageID != "" {
		cli.ImageRemove(ctx, managedImageID, image.RemoveOptions{Force: true, PruneChildren: true})
	}
	if unmanagedImageID != "" {
		cli.ImageRemove(ctx, unmanagedImageID, image.RemoveOptions{Force: true, PruneChildren: true})
	}

	cli.ImageRemove(ctx, testImageTag, image.RemoveOptions{Force: true, PruneChildren: true})
	cli.ImageRemove(ctx, unmanagedTag, image.RemoveOptions{Force: true, PruneChildren: true})
}

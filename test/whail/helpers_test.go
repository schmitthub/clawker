package whail_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/buildkit"
)

const (
	testLabelPrefix  = "com.whail.test"
	testManagedLabel = "managed"
	testManagedKey   = testLabelPrefix + "." + testManagedLabel
	testManagedValue = "true"
)

// requireDocker skips the test if Docker is unavailable.
func requireDocker(t *testing.T) {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("Docker client unavailable: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("Docker daemon not responding: %v", err)
	}
}

// requireBuildKit skips the test if BuildKit is unavailable.
func requireBuildKit(t *testing.T) {
	t.Helper()
	requireDocker(t)

	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Skipf("Docker client unavailable: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	enabled, err := whail.BuildKitEnabled(ctx, cli)
	if err != nil {
		t.Skipf("BuildKit detection failed: %v", err)
	}
	if !enabled {
		t.Skip("BuildKit not enabled")
	}
}

// newTestEngine creates a whail.Engine configured for testing with
// com.whail.test labels and BuildKit wired. Cleans up on test end.
func newTestEngine(t *testing.T) *whail.Engine {
	t.Helper()
	ctx := context.Background()

	engine, err := whail.NewWithOptions(ctx, whail.EngineOptions{
		LabelPrefix:  testLabelPrefix,
		ManagedLabel: testManagedLabel,
	})
	if err != nil {
		t.Fatalf("newTestEngine: %v", err)
	}

	engine.BuildKitImageBuilder = buildkit.NewImageBuilder(engine.APIClient)

	t.Cleanup(func() {
		engine.APIClient.Close()
	})
	return engine
}

// newRawClient creates a raw moby client for verification (label inspection,
// image removal). Cleans up on test end.
func newRawClient(t *testing.T) *client.Client {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatalf("newRawClient: %v", err)
	}
	t.Cleanup(func() { cli.Close() })
	return cli
}

// uniqueTag generates a unique image tag for test isolation.
func uniqueTag(prefix string) string {
	return fmt.Sprintf("whail-bk-test-%s:%s", prefix, time.Now().Format("150405.000000"))
}

// writeDockerfile writes a Dockerfile to a temp directory and returns the dir path.
func writeDockerfile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(content), 0644); err != nil {
		t.Fatalf("writeDockerfile: %v", err)
	}
	return dir
}

// writeContextFile writes an additional file into the build context directory.
func writeContextFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writeContextFile: %v", err)
	}
}

// buildImage calls engine.ImageBuildKit, fails on error, and registers cleanup
// to remove the image via raw client.
func buildImage(t *testing.T, engine *whail.Engine, tag, contextDir string, labels map[string]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	err := engine.ImageBuildKit(ctx, whail.ImageBuildKitOptions{
		Tags:           []string{tag},
		ContextDir:     contextDir,
		Labels:         labels,
		SuppressOutput: true,
	})
	if err != nil {
		t.Fatalf("buildImage(%s): %v", tag, err)
	}

	rawCli := newRawClient(t)
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanCancel()
		_, err := rawCli.ImageRemove(cleanCtx, tag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
		if err != nil {
			t.Logf("WARNING: failed to remove image %s: %v", tag, err)
		}
	})
}

// execInImage creates a managed container from the given image via the whail
// engine, runs cmd inside it, and returns stdout. The container is cleaned up
// via t.Cleanup.
func execInImage(t *testing.T, engine *whail.Engine, imageTag string, cmd ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create container through whail engine (image is managed, so this works)
	createResp, err := engine.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Config: &container.Config{
			Image: imageTag,
			Cmd:   []string{"sleep", "infinity"},
		},
	})
	if err != nil {
		t.Fatalf("execInImage: container create: %v", err)
	}
	containerID := createResp.ID

	t.Cleanup(func() {
		cleanCtx := context.Background()
		_, _ = engine.ContainerStop(cleanCtx, containerID, nil)
		_, _ = engine.ContainerRemove(cleanCtx, containerID, true)
	})

	// Start container
	if _, err := engine.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: containerID}); err != nil {
		t.Fatalf("execInImage: container start: %v", err)
	}

	// Create exec
	execResp, err := engine.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	})
	if err != nil {
		t.Fatalf("execInImage: exec create: %v", err)
	}

	// Attach to exec
	hijacked, err := engine.APIClient.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		t.Fatalf("execInImage: exec attach: %v", err)
	}
	defer hijacked.Close()

	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, hijacked.Reader)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("execInImage: read output: %v", err)
	}

	// Check exit code
	inspectResp, err := engine.APIClient.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
	if err != nil {
		t.Fatalf("execInImage: exec inspect: %v", err)
	}
	if inspectResp.ExitCode != 0 {
		t.Fatalf("execInImage: command %v exited %d: stdout=%q stderr=%q",
			cmd, inspectResp.ExitCode, stdout.String(), stderr.String())
	}

	return strings.TrimSpace(stdout.String())
}

// cleanupTestImages removes all images with the com.whail.test.managed=true label.
func cleanupTestImages(ctx context.Context, cli *client.Client) error {
	images, err := cli.ImageList(ctx, client.ImageListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", testManagedKey+"="+testManagedValue),
	})
	if err != nil {
		return fmt.Errorf("list test images: %w", err)
	}

	var errs []error
	for _, img := range images.Items {
		_, err := cli.ImageRemove(ctx, img.ID, client.ImageRemoveOptions{Force: true, PruneChildren: true})
		if err != nil {
			errs = append(errs, fmt.Errorf("remove image %s: %w", img.ID[:12], err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

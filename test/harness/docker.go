package harness

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/hostproxy"
)

// _blankCfg provides label constants for the harness package.
// These are fixed values that don't vary by config, so a blank config is sufficient.
var _blankCfg = configmocks.NewBlankConfig()

var (
	// TestLabel is the label used to identify test resources.
	TestLabel = _blankCfg.LabelTest()
	// TestLabelValue is the value for the test label.
	TestLabelValue = _blankCfg.ManagedLabelValue()
	// ClawkerManagedLabel is the standard clawker managed label.
	ClawkerManagedLabel = _blankCfg.LabelManaged()
	// LabelTestName is the label key for the originating test function name.
	LabelTestName = _blankCfg.LabelTestName()
)

// RunTestMain wraps testing.M.Run with cleanup of test-labeled Docker resources
// and host-proxy daemons. It acquires an exclusive file lock to prevent concurrent
// integration test runs from piling up containers and processes. Stale resources
// from previous (possibly killed) runs are cleaned before tests start, and again
// after tests complete — including on SIGINT/SIGTERM (e.g. Ctrl+C). Use from TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(harness.RunTestMain(m)) }
func RunTestMain(m *testing.M) int {
	// Acquire exclusive lock to prevent concurrent integration test runs
	// from piling up containers and host-proxy daemons.
	lockFile, err := acquireTestLock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return 1
	}
	defer releaseTestLock(lockFile)

	cleanup := func() {
		// Always kill host-proxy daemons, even if Docker is unavailable
		cleanupHostProxy()

		if !isDockerAvailable() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cli, err := client.New(client.FromEnv)
		if err != nil {
			return
		}
		defer cli.Close()
		_ = CleanupTestResources(ctx, cli)
	}

	// Catch SIGINT/SIGTERM so Ctrl+C still cleans up.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cleanup()
		os.Exit(1)
	}()

	// Clean stale resources from previous runs
	cleanup()

	code := m.Run()

	signal.Stop(sig)
	cleanup()

	return code
}

// acquireTestLock acquires an exclusive file lock to prevent concurrent
// integration test runs from piling up containers and processes.
func acquireTestLock() (*os.File, error) {
	lockDir := config.ConfigDir()
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("cannot create lock directory: %w", err)
	}
	lockPath := filepath.Join(lockDir, "integration-test.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another integration test run is active (lock: %s)", lockPath)
	}
	return f, nil
}

// releaseTestLock releases the exclusive file lock.
func releaseTestLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

// cleanupHostProxy stops any running host-proxy daemon.
func cleanupHostProxy() {
	m, err := hostproxy.NewManager(_blankCfg)
	if err != nil {
		return
	}
	if m.IsRunning() {
		_ = m.StopDaemon()
	}
}

// RequireDocker skips the test if Docker is not available.
// Use this at the start of internals tests.
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
	c, err := docker.NewClient(ctx, _blankCfg, docker.WithLabels(docker.TestLabelConfig(_blankCfg, t.Name())))
	if err != nil {
		t.Fatalf("failed to create Docker client: %v", err)
	}

	t.Cleanup(func() {
		c.Close()
	})

	return c
}

// Deprecated: NewRawDockerClient creates a raw Docker SDK client for testing.
// Prefer NewTestClient which returns *docker.Client with automatic test label injection.
// This function remains for infrastructure code (e.g., BuildLightImage prune, RunTestMain cleanup)
// that needs raw SDK access for operations whail overrides with different signatures.
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
// The testName parameter sets the test name label for leak tracing.
func AddClawkerLabels(labels map[string]string, project, agent, testName string) map[string]string {
	result := AddTestLabels(labels)
	result[ClawkerManagedLabel] = _blankCfg.ManagedLabelValue()
	result[_blankCfg.LabelProject()] = project
	result[_blankCfg.LabelAgent()] = agent
	if testName != "" {
		result[LabelTestName] = testName
	}
	return result
}

// CleanupProjectResources removes all Docker resources associated with a project.
// This cleans up containers, volumes, networks, and images with the project label.
func CleanupProjectResources(ctx context.Context, c *docker.Client, project string) error {
	var errs []error

	// Stop and remove containers
	f := client.Filters{}.Add("label", _blankCfg.LabelProject()+"="+project)
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
	volumes, err := c.VolumeList(ctx, map[string]string{_blankCfg.LabelProject(): project})
	if err != nil {
		return err
	}

	for _, vol := range volumes.Items {
		if _, err := c.VolumeRemove(ctx, vol.Name, true); err != nil {
			errs = append(errs, fmt.Errorf("remove volume %s: %w", vol.Name, err))
		}
	}

	// Remove networks
	networks, err := c.NetworkList(ctx, map[string]string{_blankCfg.LabelProject(): project})
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

	// Remove images with test label (including dangling intermediates)
	images, err := cli.ImageList(ctx, client.ImageListOptions{All: true, Filters: f})
	if err != nil {
		return err
	}

	for _, img := range images.Items {
		if _, err := cli.ImageRemove(ctx, img.ID, client.ImageRemoveOptions{Force: true, PruneChildren: true}); err != nil {
			errs = append(errs, fmt.Errorf("remove image %s: %w", img.ID[:12], err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %w", errors.Join(errs...))
	}
	return nil
}

// ContainerExists checks if a container exists by name.
func ContainerExists(ctx context.Context, cli *docker.Client, name string) bool {
	_, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	return err == nil
}

// ContainerIsRunning checks if a container is running by name.
func ContainerIsRunning(ctx context.Context, cli *docker.Client, name string) bool {
	info, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return false
	}
	return info.Container.State.Running
}

// WaitForContainerRunning waits for a container to exist and be in running state.
// Polls every 500ms until the context is cancelled or the container is running.
// Fails fast with exit code information if the container enters a terminal state
// (exited or dead) instead of timing out silently.
func WaitForContainerRunning(ctx context.Context, cli *docker.Client, name string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for container %s to be running: %w", name, ctx.Err())
		case <-ticker.C:
			info, err := cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
			if err != nil {
				// Container doesn't exist yet or inspect failed, continue polling
				continue
			}

			// Container is running - success
			if info.Container.State.Running {
				return nil
			}

			// Container in terminal state - fail fast with useful info
			status := info.Container.State.Status
			if status == "exited" || status == "dead" {
				return fmt.Errorf("container %s exited (code %d) while waiting for running state",
					name, info.Container.State.ExitCode)
			}
			// Other states (created, paused, restarting) - continue polling
		}
	}
}

// VolumeExists checks if a volume exists by name.
func VolumeExists(ctx context.Context, cli *docker.Client, name string) bool {
	_, err := cli.VolumeInspect(ctx, name)
	return err == nil
}

// NetworkExists checks if a network exists by name.
func NetworkExists(ctx context.Context, cli *docker.Client, name string) bool {
	_, err := cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	return err == nil
}

// ContainerExitDiagnostics contains comprehensive exit information for debugging.
type ContainerExitDiagnostics struct {
	ExitCode        int
	OOMKilled       bool
	Error           string // Docker's state error field
	Logs            string // Last N lines of logs
	LogError        error  // Error retrieving logs, if any
	StartedAt       string // ISO 8601 timestamp
	FinishedAt      string // ISO 8601 timestamp
	HasClawkerError bool
	ClawkerErrorMsg string
	FirewallFailed  bool
}

// GetContainerExitDiagnostics retrieves detailed exit information for a stopped container.
// logTailLines specifies how many lines from the end of logs to include (0 = all logs).
func GetContainerExitDiagnostics(ctx context.Context, cli *docker.Client, containerID string, logTailLines int) (*ContainerExitDiagnostics, error) {
	info, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return nil, err
	}

	// Get logs with tail limit to avoid huge log output
	logs, logErr := getContainerLogsTail(ctx, cli, containerID, logTailLines)

	// Strip Docker multiplexed stream headers
	logs = StripDockerStreamHeaders(logs)

	hasError, errorMsg := CheckForErrorPattern(logs)

	return &ContainerExitDiagnostics{
		ExitCode:        info.Container.State.ExitCode,
		OOMKilled:       info.Container.State.OOMKilled,
		Error:           info.Container.State.Error,
		Logs:            logs,
		LogError:        logErr,
		StartedAt:       info.Container.State.StartedAt,
		FinishedAt:      info.Container.State.FinishedAt,
		HasClawkerError: hasError,
		ClawkerErrorMsg: errorMsg,
		FirewallFailed: strings.Contains(logs, "Firewall initialization failed") ||
			strings.Contains(logs, "ERROR: Failed to fetch GitHub IP") ||
			strings.Contains(logs, "ERROR: Failed to detect host IP"),
	}, nil
}

// getContainerLogsTail retrieves container logs with an optional tail limit.
func getContainerLogsTail(ctx context.Context, cli *docker.Client, containerID string, tailLines int) (string, error) {
	opts := client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     false,
	}
	if tailLines > 0 {
		opts.Tail = strconv.Itoa(tailLines)
	}

	reader, err := cli.ContainerLogs(ctx, containerID, opts)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	var buf bytes.Buffer
	_, err = io.Copy(&buf, reader)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// StripDockerStreamHeaders removes Docker's multiplexed stream headers.
// Docker prefixes each frame with an 8-byte header: [stream_type][padding][size].
func StripDockerStreamHeaders(data string) string {
	var result strings.Builder
	b := []byte(data)

	for len(b) > 0 {
		// Check if we have a valid header
		if len(b) >= 8 {
			streamType := b[0]
			// Valid stream types are 0 (stdin), 1 (stdout), 2 (stderr)
			if streamType <= 2 && b[1] == 0 && b[2] == 0 && b[3] == 0 {
				// Parse payload size (big endian uint32)
				size := uint32(b[4])<<24 | uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7])
				if len(b) >= int(8+size) {
					// Extract payload and skip header
					result.Write(b[8 : 8+size])
					b = b[8+size:]
					continue
				}
			}
		}
		// If no valid header found, just append remaining bytes
		result.Write(b)
		break
	}

	return result.String()
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
	builder := docker.NewBuilder(dockerClient, h.Config, h.ProjectDir)

	// Build labels that mark this as a test image
	labels := map[string]string{
		TestLabel:                TestLabelValue,
		ClawkerManagedLabel:      _blankCfg.ManagedLabelValue(),
		_blankCfg.LabelProject(): h.Project,
		_blankCfg.LabelE2ETest(): _blankCfg.ManagedLabelValue(),
	}

	// Build the image
	buildOpts := docker.BuilderOptions{
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

// BuildSimpleTestImageOptions configures simple image building for tests.
type BuildSimpleTestImageOptions struct {
	// SuppressOutput suppresses build output (default: true for cleaner test output)
	SuppressOutput bool
	// NoCache disables Docker build cache (default: false)
	NoCache bool
	// Project is the project name for labeling (optional)
	Project string
}

// BuildSimpleTestImage builds a simple test image from a Dockerfile string.
// This is useful for tests that don't need the full clawker infrastructure (Claude Code, etc.).
// The image is tagged uniquely and automatically cleaned up when the test completes.
// Uses docker.Client (wrapping whail.Engine) so the image is built through clawker's
// label management system and is visible to managed image queries.
func BuildSimpleTestImage(t *testing.T, dockerfile string, opts BuildSimpleTestImageOptions) string {
	t.Helper()
	RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Create a unique tag for this test run
	timestamp := time.Now().Format("150405.000000")
	project := opts.Project
	if project == "" {
		project = "simple-test"
	}
	imageTag := fmt.Sprintf("clawker-simple-%s:%s", project, timestamp)

	// Use clawker Docker client (wraps whail.Engine) — NOT raw moby client
	dc := NewTestClient(t)

	// Create tar archive with just the Dockerfile
	buildCtx, err := createSimpleBuildContext(dockerfile)
	if err != nil {
		t.Fatalf("failed to create build context: %v", err)
	}

	// Build labels — managed label is auto-injected by whail, but we add
	// project label and test label explicitly
	labels := map[string]string{
		TestLabel: TestLabelValue,
	}
	if opts.Project != "" {
		labels[_blankCfg.LabelProject()] = opts.Project
	}

	t.Logf("Building simple test image: %s", imageTag)
	if err := dc.BuildImage(ctx, buildCtx, docker.BuildImageOpts{
		Tags:           []string{imageTag},
		NoCache:        opts.NoCache,
		Labels:         labels,
		SuppressOutput: opts.SuppressOutput,
	}); err != nil {
		t.Fatalf("failed to build simple test image: %v", err)
	}

	// Register cleanup — uses the same whail-wrapped client
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := dc.ImageRemove(cleanupCtx, imageTag, client.ImageRemoveOptions{Force: true}); err != nil {
			t.Logf("WARNING: failed to cleanup simple test image %s: %v", imageTag, err)
		}
	})

	return imageTag
}

// createSimpleBuildContext creates a minimal tar archive containing just a Dockerfile.
func createSimpleBuildContext(dockerfile string) (io.Reader, error) {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)

	// Add Dockerfile
	header := &tar.Header{
		Name: "Dockerfile",
		Mode: 0644,
		Size: int64(len(dockerfile)),
	}
	if err := tw.WriteHeader(header); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(dockerfile)); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf, nil
}

// TestChownImage is the stable tag for the test chown image used by CopyToVolume.
const TestChownImage = "clawker-test-chown:latest"

// BuildTestChownImage builds a minimal busybox-based image with test labels
// for CopyToVolume operations. Uses docker.Client (wrapping whail.Engine) so
// the image is built through clawker's label management system — managed labels
// are auto-injected and the image is visible to managed image queries.
//
// The image uses a stable tag (TestChownImage) and is only built if it doesn't
// already exist, making it safe to call multiple times per test run. The image
// is NOT cleaned up per-test since it's shared; CleanupTestResources removes
// it by label.
func BuildTestChownImage(t *testing.T) {
	t.Helper()
	RequireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Use clawker Docker client (wraps whail.Engine) — NOT raw moby client
	dc := NewTestClient(t)

	// Check if already exists — uses whail's managed label check, which is
	// correct since we build this image through whail (so it has managed labels)
	exists, err := dc.ImageExists(ctx, TestChownImage)
	if err != nil {
		t.Fatalf("failed to check for test chown image: %v", err)
	}
	if exists {
		return // Already built
	}

	// Build from busybox with test labels
	// Note: managed label is auto-injected by whail
	dockerfile := fmt.Sprintf("FROM busybox:latest\nLABEL %s=%s\n",
		TestLabel, TestLabelValue)
	buildCtx, err := createSimpleBuildContext(dockerfile)
	if err != nil {
		t.Fatalf("failed to create chown image build context: %v", err)
	}

	if err := dc.BuildImage(ctx, buildCtx, docker.BuildImageOpts{
		Tags: []string{TestChownImage},
		Labels: map[string]string{
			TestLabel: TestLabelValue,
		},
		SuppressOutput: true,
	}); err != nil {
		t.Fatalf("failed to build test chown image: %v", err)
	}

	t.Logf("Built test chown image: %s", TestChownImage)
}

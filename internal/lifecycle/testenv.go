package lifecycle

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/build"
	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestEnv provides a complete test environment for container lifecycle tests.
// It sets up:
// - Temporary working directory with clawker.yaml
// - Pre-built test image with clawker labels
// - Docker client configured with clawker labels
// - Factory for command execution
type TestEnv struct {
	T            *testing.T
	Ctx          context.Context
	Cancel       context.CancelFunc
	Factory      *cmdutil.Factory
	Client       *docker.Client
	Config       *config.Config
	WorkDir      string // Temp directory with clawker.yaml
	ImageTag     string // Built test image
	ProjectName  string // Unique project name for this test run
	cleanupFuncs []func()
}

// TestEnvOptions configures the test environment.
type TestEnvOptions struct {
	// ProjectConfig allows mutation of the default test config.
	// The mutator function receives the default config and can modify it for specific test cases.
	// If nil, the default config is used unchanged.
	ProjectConfig func(cfg *config.Config)

	// UseClawkerImage builds a real clawker image using ProjectGenerator instead of
	// a simple alpine-based test image. When true, the image includes entrypoint scripts,
	// firewall init, git credential helpers, etc.
	UseClawkerImage bool

	// FirewallConfig overrides the default firewall configuration.
	// If nil and UseClawkerImage is false, firewall is disabled for faster tests.
	// Deprecated: Use ProjectConfig instead for new tests.
	FirewallConfig *config.FirewallConfig

	// BaseImage is the Docker image to use as a base.
	// Defaults to "alpine:latest" for non-clawker tests.
	// Deprecated: Use ProjectConfig instead for new tests.
	BaseImage string

	// SkipImageBuild skips building a custom test image and uses the base directly.
	// Deprecated: Use ProjectConfig instead for new tests.
	SkipImageBuild bool
}

// NewTestEnv creates a new test environment with default options.
func NewTestEnv(t *testing.T) *TestEnv {
	return NewTestEnvWithOptions(t, TestEnvOptions{})
}

// NewTestEnvWithOptions creates a new test environment with custom options.
func NewTestEnvWithOptions(t *testing.T, opts TestEnvOptions) *TestEnv {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute) // Extended for clawker image builds

	// Generate unique project name for isolation
	projectName := fmt.Sprintf("lifecycle-test-%d", time.Now().UnixNano())

	// Create temp working directory
	workDir := t.TempDir()

	// Set default base image
	baseImage := opts.BaseImage
	if baseImage == "" {
		baseImage = "alpine:latest"
	}

	// Create test config - either use ProjectConfig mutator or legacy options
	cfg := defaultTestConfig(projectName)

	if opts.ProjectConfig != nil {
		// Apply mutator to customize config for this test case
		opts.ProjectConfig(cfg)
	} else {
		// Legacy path: use old options for backwards compatibility
		cfg.Build.Image = baseImage
		// Configure firewall (disabled by default for faster tests)
		if opts.FirewallConfig != nil {
			cfg.Security.Firewall = opts.FirewallConfig
		} else {
			cfg.Security.Firewall = &config.FirewallConfig{
				Enable: false,
			}
		}
		// Disable host proxy for faster legacy tests
		cfg.Security.EnableHostProxy = boolPtr(false)
	}

	// Write clawker.yaml to temp dir
	writeConfig(t, workDir, cfg)

	// Create factory pointing to test directory
	factory := cmdutil.New("test", "test")
	factory.WorkDir = workDir
	factory.IOStreams = cmdutil.NewTestIOStreams().IOStreams

	// Create Docker client
	dockerClient, err := docker.NewClient(ctx)
	require.NoError(t, err, "failed to create Docker client")

	env := &TestEnv{
		T:           t,
		Ctx:         ctx,
		Cancel:      cancel,
		Factory:     factory,
		Client:      dockerClient,
		Config:      cfg,
		WorkDir:     workDir,
		ProjectName: projectName,
	}

	// Register cleanup for Docker client
	env.cleanupFuncs = append(env.cleanupFuncs, func() {
		dockerClient.Close()
	})

	// Build or prepare test image
	if opts.SkipImageBuild {
		env.ImageTag = baseImage
	} else if opts.UseClawkerImage || opts.ProjectConfig != nil {
		// Build real clawker image using ProjectGenerator
		imageTag := buildClawkerImage(t, ctx, dockerClient, cfg, workDir)
		env.ImageTag = imageTag
		env.cleanupFuncs = append(env.cleanupFuncs, func() {
			cleanupImage(ctx, dockerClient, imageTag)
		})
	} else {
		// Build simple test image (legacy path)
		imageTag := buildTestImage(t, ctx, dockerClient, projectName)
		env.ImageTag = imageTag
		env.cleanupFuncs = append(env.cleanupFuncs, func() {
			cleanupImage(ctx, dockerClient, imageTag)
		})
	}

	return env
}

// NewTestEnvWithFirewall creates a test environment with a specific firewall configuration.
func NewTestEnvWithFirewall(t *testing.T, firewallConfig *config.FirewallConfig) *TestEnv {
	return NewTestEnvWithOptions(t, TestEnvOptions{
		FirewallConfig: firewallConfig,
	})
}

// Cleanup removes all test resources created by this environment.
// Should be called via defer after creating the environment.
func (te *TestEnv) Cleanup() {
	te.T.Helper()

	// Cancel context first to stop any ongoing operations
	te.Cancel()

	// Use a new context for cleanup since the original may be cancelled
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cleanupCancel()

	// Remove all containers created by this test project
	te.cleanupContainers(cleanupCtx)

	// Remove all volumes created by this test project
	te.cleanupVolumes(cleanupCtx)

	// Run registered cleanup functions
	for _, fn := range te.cleanupFuncs {
		fn()
	}
}

// cleanupContainers removes all containers belonging to this test project.
func (te *TestEnv) cleanupContainers(ctx context.Context) {
	containers, err := te.Client.ListContainersByProject(ctx, te.ProjectName, true)
	if err != nil {
		te.T.Logf("warning: failed to list containers for cleanup: %v", err)
		return
	}

	for _, c := range containers {
		if _, err := te.Client.ContainerRemove(ctx, c.ID, true); err != nil {
			te.T.Logf("warning: failed to remove container %s: %v", c.Name, err)
		}
	}
}

// cleanupVolumes removes all volumes belonging to this test project.
func (te *TestEnv) cleanupVolumes(ctx context.Context) {
	// Use raw filter map for the project label
	filter := map[string]string{
		docker.LabelProject: te.ProjectName,
	}
	volumes, err := te.Client.VolumeList(ctx, filter)
	if err != nil {
		te.T.Logf("warning: failed to list volumes for cleanup: %v", err)
		return
	}

	for _, v := range volumes.Items {
		if _, err := te.Client.VolumeRemove(ctx, v.Name, true); err != nil {
			te.T.Logf("warning: failed to remove volume %s: %v", v.Name, err)
		}
	}
}

// ContainerName returns the full container name for an agent.
func (te *TestEnv) ContainerName(agent string) string {
	return docker.ContainerName(te.ProjectName, agent)
}

// WaitForContainerState waits for a container to reach a specific state.
// Returns an error if the timeout is reached before the state is achieved.
func (te *TestEnv) WaitForContainerState(containerID string, state string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Use raw APIClient to get full container info
		info, err := te.Client.APIClient.ContainerInspect(te.Ctx, containerID, client.ContainerInspectOptions{})
		if err != nil {
			return fmt.Errorf("failed to inspect container: %w", err)
		}
		if info.Container.State.Status == container.ContainerState(state) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for container to reach state %q", state)
}

// GetContainerLogs retrieves the logs from a container.
func (te *TestEnv) GetContainerLogs(containerID string) (string, error) {
	logs, err := te.Client.ContainerLogs(te.Ctx, containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return "", err
	}
	defer logs.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(logs); err != nil {
		return "", fmt.Errorf("failed to read container logs: %w", err)
	}
	return buf.String(), nil
}

// writeConfig writes a config struct to clawker.yaml in the given directory.
func writeConfig(t *testing.T, dir string, cfg *config.Config) {
	t.Helper()

	data, err := yaml.Marshal(cfg)
	require.NoError(t, err, "failed to marshal config")

	configPath := filepath.Join(dir, config.ConfigFileName)
	err = os.WriteFile(configPath, data, 0644)
	require.NoError(t, err, "failed to write config file")
}

// defaultTestConfig creates a base config with sensible defaults for clawker image tests.
// Test cases mutate this config to test specific variations.
func defaultTestConfig(projectName string) *config.Config {
	return &config.Config{
		Version: "1",
		Project: projectName,
		Build: config.BuildConfig{
			Image:    "buildpack-deps:bookworm-scm", // Debian by default
			Packages: []string{"git", "curl"},       // Minimal for tests
		},
		Security: config.SecurityConfig{
			Firewall:        &config.FirewallConfig{Enable: true}, // Default on for realistic tests
			EnableHostProxy: boolPtr(false),                       // Disable for tests (no host interaction)
			DockerSocket:    false,
			CapAdd:          []string{"NET_ADMIN"}, // Required for firewall
			GitCredentials: &config.GitCredentialsConfig{
				ForwardHTTPS:  boolPtr(false), // Disable for tests
				ForwardSSH:    boolPtr(false), // Disable for tests
				CopyGitConfig: boolPtr(false), // Disable for tests
			},
		},
		Workspace: config.WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
	}
}

// sanitizeTestName converts a test name to a valid resource name component.
func sanitizeTestName(name string) string {
	// Replace path separators and spaces with dashes
	sanitized := strings.ReplaceAll(name, "/", "-")
	sanitized = strings.ReplaceAll(sanitized, " ", "-")
	sanitized = strings.ToLower(sanitized)
	// Remove any characters that aren't alphanumeric or dashes
	re := regexp.MustCompile(`[^a-z0-9-]`)
	sanitized = re.ReplaceAllString(sanitized, "")
	// Truncate to a reasonable length
	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}
	return sanitized
}

// buildClawkerImage builds a real clawker image using ProjectGenerator.
// This creates an image with all init scripts (entrypoint, firewall, git credentials, etc.).
func buildClawkerImage(t *testing.T, ctx context.Context, dockerClient *docker.Client, cfg *config.Config, workDir string) string {
	t.Helper()

	// Generate image tag
	imageTag := fmt.Sprintf("clawker-%s:latest", cfg.Project)

	t.Logf("Building clawker image %s...", imageTag)

	// Use ProjectGenerator to create the build context
	generator := build.NewProjectGenerator(cfg, workDir)

	buildCtx, err := generator.GenerateBuildContext()
	require.NoError(t, err, "failed to generate build context")

	// Build options with clawker labels
	opts := docker.BuildImageOpts{
		Tags:           []string{imageTag},
		Dockerfile:     "Dockerfile",
		Labels:         docker.ImageLabels(cfg.Project, cfg.Version),
		SuppressOutput: true, // Suppress output unless verbose
		NetworkMode:    "host", // Allow npm/git access during build
	}

	// Check for verbose mode
	if os.Getenv("CLAWKER_TEST_VERBOSE") != "" {
		opts.SuppressOutput = false
	}

	err = dockerClient.BuildImage(ctx, buildCtx, opts)
	require.NoError(t, err, "failed to build clawker test image")

	t.Logf("Successfully built clawker image %s", imageTag)
	return imageTag
}

// buildTestImage builds a simple test image with clawker labels.
func buildTestImage(t *testing.T, ctx context.Context, dockerClient *docker.Client, projectName string) string {
	t.Helper()

	imageTag := fmt.Sprintf("clawker-test-%s:latest", projectName)

	// Simple Dockerfile for testing
	dockerfile := `FROM alpine:latest
CMD ["sleep", "300"]
`

	// Build context as tar
	tarBuf := new(bytes.Buffer)
	if err := createTarWithDockerfile(tarBuf, dockerfile); err != nil {
		t.Fatalf("failed to create build context: %v", err)
	}

	// Build options with clawker labels
	opts := docker.BuildImageOpts{
		Tags:           []string{imageTag},
		Dockerfile:     "Dockerfile",
		Labels:         docker.ImageLabels(projectName, imageTag),
		SuppressOutput: true,
	}

	if err := dockerClient.BuildImage(ctx, tarBuf, opts); err != nil {
		t.Fatalf("failed to build test image: %v", err)
	}

	return imageTag
}

// createTarWithDockerfile creates a minimal tar archive containing a Dockerfile.
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

// cleanupImage removes a test image.
func cleanupImage(ctx context.Context, dockerClient *docker.Client, imageTag string) {
	_, _ = dockerClient.ImageRemove(ctx, imageTag, client.ImageRemoveOptions{Force: true, PruneChildren: true})
}

// boolPtr returns a pointer to a bool value.
func boolPtr(b bool) *bool {
	return &b
}

// SkipIfNoDocker skips the test if Docker is not available.
func SkipIfNoDocker(t *testing.T) {
	t.Helper()

	if os.Getenv("CLAWKER_INTEGRATION_TESTS") == "" {
		t.Skip("set CLAWKER_INTEGRATION_TESTS=1 to run integration tests")
	}

	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("Docker not available: %v", err)
	}
	defer cli.Close()

	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("Docker not running: %v", err)
	}
}

// ProjectRoot returns the project root directory by finding the go.mod file.
func ProjectRoot(t *testing.T) string {
	t.Helper()

	// Start from current directory and walk up
	dir, err := os.Getwd()
	require.NoError(t, err)

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

// GenerateAgentName creates a unique agent name for tests.
func GenerateAgentName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// ContainerHasLabel checks if a container has a specific label with the expected value.
func (te *TestEnv) ContainerHasLabel(containerID, key, expectedValue string) bool {
	info, err := te.Client.APIClient.ContainerInspect(te.Ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		return false
	}
	if info.Container.Config == nil {
		return false
	}
	value, ok := info.Container.Config.Labels[key]
	return ok && value == expectedValue
}

// ContainerIsManaged checks if a container has the clawker managed label.
func (te *TestEnv) ContainerIsManaged(containerID string) bool {
	return te.ContainerHasLabel(containerID, docker.LabelManaged, docker.ManagedLabelValue)
}

// AssertContainerRunning asserts that a container is in the running state.
func (te *TestEnv) AssertContainerRunning(containerID string) {
	te.T.Helper()
	info, err := te.Client.APIClient.ContainerInspect(te.Ctx, containerID, client.ContainerInspectOptions{})
	require.NoError(te.T, err, "failed to inspect container")
	require.True(te.T, info.Container.State.Running, "container should be running")
}

// AssertContainerNotRunning asserts that a container is NOT in the running state.
func (te *TestEnv) AssertContainerNotRunning(containerID string) {
	te.T.Helper()
	info, err := te.Client.APIClient.ContainerInspect(te.Ctx, containerID, client.ContainerInspectOptions{})
	require.NoError(te.T, err, "failed to inspect container")
	require.False(te.T, info.Container.State.Running, "container should not be running")
}

// FindContainerByAgent finds a container by agent name.
// Returns the container.Summary from the Docker SDK.
func (te *TestEnv) FindContainerByAgent(agent string) (*container.Summary, error) {
	containerName, ctr, err := te.Client.FindContainerByAgent(te.Ctx, te.ProjectName, agent)
	if err != nil {
		return nil, err
	}
	if ctr == nil {
		return nil, fmt.Errorf("container %s not found", containerName)
	}
	return ctr, nil
}

// RequireContainerExists asserts that a container exists and returns it.
func (te *TestEnv) RequireContainerExists(agent string) *container.Summary {
	te.T.Helper()
	ctr, err := te.FindContainerByAgent(agent)
	require.NoError(te.T, err, "container should exist")
	require.NotNil(te.T, ctr, "container should not be nil")
	return ctr
}

// LogContainerState logs the current state of a container for debugging.
func (te *TestEnv) LogContainerState(containerID string) {
	te.T.Helper()
	info, err := te.Client.APIClient.ContainerInspect(te.Ctx, containerID, client.ContainerInspectOptions{})
	if err != nil {
		te.T.Logf("failed to inspect container %s: %v", containerID, err)
		return
	}
	te.T.Logf("Container %s state: %s (running: %v, exit code: %d)",
		strings.TrimPrefix(info.Container.Name, "/"),
		info.Container.State.Status,
		info.Container.State.Running,
		info.Container.State.ExitCode)
}

// ============================================================================
// Label Verification Helpers
// ============================================================================

// VerifyContainerLabels checks all required clawker labels are present on a container.
func (te *TestEnv) VerifyContainerLabels(containerID string) {
	te.T.Helper()
	info, err := te.Client.APIClient.ContainerInspect(te.Ctx, containerID, client.ContainerInspectOptions{})
	require.NoError(te.T, err, "failed to inspect container")

	labels := info.Container.Config.Labels
	require.Equal(te.T, docker.ManagedLabelValue, labels[docker.LabelManaged], "container should have managed label")
	require.Equal(te.T, te.ProjectName, labels[docker.LabelProject], "container should have project label")
	require.NotEmpty(te.T, labels[docker.LabelAgent], "container should have agent label")
}

// VerifyImageLabels checks all required clawker labels on a built image.
func (te *TestEnv) VerifyImageLabels(imageID string) {
	te.T.Helper()
	info, err := te.Client.APIClient.ImageInspect(te.Ctx, imageID)
	require.NoError(te.T, err, "failed to inspect image")

	labels := info.Config.Labels
	require.Equal(te.T, docker.ManagedLabelValue, labels[docker.LabelManaged], "image should have managed label")
	require.Equal(te.T, te.ProjectName, labels[docker.LabelProject], "image should have project label")
}

// VerifyVolumeLabels checks all required clawker labels on volumes attached to container.
func (te *TestEnv) VerifyVolumeLabels(containerID string) {
	te.T.Helper()
	info, err := te.Client.APIClient.ContainerInspect(te.Ctx, containerID, client.ContainerInspectOptions{})
	require.NoError(te.T, err, "failed to inspect container")

	for _, mount := range info.Container.Mounts {
		if mount.Type == "volume" {
			vol, err := te.Client.VolumeInspect(te.Ctx, mount.Name)
			require.NoError(te.T, err, "failed to inspect volume %s", mount.Name)
			require.Equal(te.T, docker.ManagedLabelValue, vol.Volume.Labels[docker.LabelManaged],
				"volume %s should have managed label", mount.Name)
			require.Equal(te.T, te.ProjectName, vol.Volume.Labels[docker.LabelProject],
				"volume %s should have project label", mount.Name)
		}
	}
}

// ============================================================================
// Image Verification Helpers
// ============================================================================

// VerifyImageUser checks the default user of an image.
func (te *TestEnv) VerifyImageUser(imageID, expectedUser string) {
	te.T.Helper()
	info, err := te.Client.APIClient.ImageInspect(te.Ctx, imageID)
	require.NoError(te.T, err, "failed to inspect image")
	require.Equal(te.T, expectedUser, info.Config.User, "image should have correct default user")
}

// VerifyImageWorkdir checks the default workdir of an image.
func (te *TestEnv) VerifyImageWorkdir(imageID, expectedWorkdir string) {
	te.T.Helper()
	info, err := te.Client.APIClient.ImageInspect(te.Ctx, imageID)
	require.NoError(te.T, err, "failed to inspect image")
	require.Equal(te.T, expectedWorkdir, info.Config.WorkingDir, "image should have correct workdir")
}

// VerifyImageHasScripts checks that specific scripts exist in /usr/local/bin using a throwaway container.
func (te *TestEnv) VerifyImageHasScripts(imageID string, scripts []string) {
	te.T.Helper()
	containerID := te.createThrowawayContainer(imageID)
	defer te.removeContainer(containerID)

	for _, script := range scripts {
		path := "/usr/local/bin/" + script
		_, err := te.ExecInContainer(containerID, "test", "-f", path)
		require.NoError(te.T, err, "script %s should exist at %s", script, path)
	}
}

// VerifyImageMissingScripts checks that specific scripts do NOT exist.
func (te *TestEnv) VerifyImageMissingScripts(imageID string, scripts []string) {
	te.T.Helper()
	containerID := te.createThrowawayContainer(imageID)
	defer te.removeContainer(containerID)

	for _, script := range scripts {
		path := "/usr/local/bin/" + script
		_, err := te.ExecInContainer(containerID, "test", "-f", path)
		require.Error(te.T, err, "script %s should NOT exist at %s", script, path)
	}
}

// ============================================================================
// Container Execution Helpers
// ============================================================================

// ExecOptions contains options for container exec.
type ExecOptions struct {
	User    string   // User to run as (empty = container default)
	Env     []string // Additional environment variables
	WorkDir string   // Working directory
}

// ExecInContainer executes a command in a running container and returns the combined output.
func (te *TestEnv) ExecInContainer(containerID string, cmd ...string) (string, error) {
	return te.ExecInContainerWithOpts(containerID, cmd, ExecOptions{})
}

// ExecInContainerWithOpts executes a command in a container with custom options.
func (te *TestEnv) ExecInContainerWithOpts(containerID string, cmd []string, opts ExecOptions) (string, error) {
	execConfig := client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	if opts.User != "" {
		execConfig.User = opts.User
	}
	if opts.Env != nil {
		execConfig.Env = opts.Env
	}
	if opts.WorkDir != "" {
		execConfig.WorkingDir = opts.WorkDir
	}

	// Use whail.Engine's ExecCreate (which checks managed label)
	execID, err := te.Client.ExecCreate(te.Ctx, containerID, execConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create exec: %w", err)
	}

	// Use embedded APIClient's ExecAttach
	resp, err := te.Client.ExecAttach(te.Ctx, execID.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to attach to exec: %w", err)
	}
	defer resp.Close()

	var output bytes.Buffer
	if _, err := io.Copy(&output, resp.Reader); err != nil {
		return "", fmt.Errorf("failed to read exec output: %w", err)
	}

	// Check exit code
	inspect, err := te.Client.ExecInspect(te.Ctx, execID.ID, client.ExecInspectOptions{})
	if err != nil {
		return output.String(), fmt.Errorf("failed to inspect exec: %w", err)
	}
	if inspect.ExitCode != 0 {
		return output.String(), fmt.Errorf("command exited with code %d: %s", inspect.ExitCode, output.String())
	}

	return output.String(), nil
}

// VerifyProcessRunning checks if a process is running in the container.
func (te *TestEnv) VerifyProcessRunning(containerID, processName string) {
	te.T.Helper()
	output, err := te.ExecInContainer(containerID, "ps", "aux")
	require.NoError(te.T, err, "ps aux should succeed")
	require.Contains(te.T, output, processName, "%s process should be running", processName)
}

// VerifyProcessNotRunning checks that a process is NOT running in the container.
func (te *TestEnv) VerifyProcessNotRunning(containerID, processName string) {
	te.T.Helper()
	output, err := te.ExecInContainer(containerID, "ps", "aux")
	require.NoError(te.T, err, "ps aux should succeed")
	require.NotContains(te.T, output, processName, "%s process should NOT be running", processName)
}

// ============================================================================
// Container Lifecycle Helpers
// ============================================================================

// RunContainer creates and starts a container with the test image, returning container ID.
func (te *TestEnv) RunContainer(agentName string, cmd ...string) string {
	te.T.Helper()

	containerName := te.ContainerName(agentName)

	// Default command if not specified
	if len(cmd) == 0 {
		cmd = []string{"sleep", "300"}
	}

	createOpts := whail.ContainerCreateOptions{
		Config: &container.Config{
			Image: te.ImageTag,
			Cmd:   cmd,
		},
		HostConfig: &container.HostConfig{
			CapAdd: te.Config.Security.CapAdd, // Pass through capabilities (e.g., NET_ADMIN for firewall)
		},
		Name: containerName,
		ExtraLabels: whail.Labels{
			{docker.LabelProject: te.ProjectName},
			{docker.LabelAgent: agentName},
		},
	}

	resp, err := te.Client.ContainerCreate(te.Ctx, createOpts)
	require.NoError(te.T, err, "container create should not error")
	require.NotEmpty(te.T, resp.ID, "container ID should not be empty")

	containerID := resp.ID

	// Start container
	_, err = te.Client.ContainerStart(te.Ctx, whail.ContainerStartOptions{
		ContainerID: containerID,
	})
	require.NoError(te.T, err, "container start should not error")

	// Wait a moment for container to be running
	time.Sleep(500 * time.Millisecond)

	return containerID
}

// CreateContainer creates a container without starting it.
func (te *TestEnv) CreateContainer(agentName string, cmd ...string) string {
	te.T.Helper()

	containerName := te.ContainerName(agentName)

	// Default command if not specified
	if len(cmd) == 0 {
		cmd = []string{"sleep", "300"}
	}

	createOpts := whail.ContainerCreateOptions{
		Config: &container.Config{
			Image: te.ImageTag,
			Cmd:   cmd,
		},
		HostConfig: &container.HostConfig{
			CapAdd: te.Config.Security.CapAdd,
		},
		Name: containerName,
		ExtraLabels: whail.Labels{
			{docker.LabelProject: te.ProjectName},
			{docker.LabelAgent: agentName},
		},
	}

	resp, err := te.Client.ContainerCreate(te.Ctx, createOpts)
	require.NoError(te.T, err, "container create should not error")
	return resp.ID
}

// StartContainer starts a stopped container.
func (te *TestEnv) StartContainer(containerID string) {
	te.T.Helper()
	_, err := te.Client.ContainerStart(te.Ctx, whail.ContainerStartOptions{
		ContainerID: containerID,
	})
	require.NoError(te.T, err, "container start should not error")
}

// StopContainer stops a running container.
func (te *TestEnv) StopContainer(containerID string) {
	te.T.Helper()
	_, err := te.Client.ContainerStop(te.Ctx, containerID, nil)
	require.NoError(te.T, err, "container stop should not error")
}

// RemoveContainer removes a container.
func (te *TestEnv) RemoveContainer(containerID string) {
	te.T.Helper()
	_, err := te.Client.ContainerRemove(te.Ctx, containerID, true)
	require.NoError(te.T, err, "container remove should not error")
}

// AssertContainerStopped waits for and asserts a container is stopped.
func (te *TestEnv) AssertContainerStopped(containerID string) {
	te.T.Helper()
	err := te.WaitForContainerState(containerID, ContainerStateStopped, 30*time.Second)
	require.NoError(te.T, err, "container should reach stopped state")
	te.AssertContainerNotRunning(containerID)
}

// AssertContainerNotExists asserts a container does not exist.
func (te *TestEnv) AssertContainerNotExists(containerID string) {
	te.T.Helper()
	_, err := te.Client.APIClient.ContainerInspect(te.Ctx, containerID, client.ContainerInspectOptions{})
	require.Error(te.T, err, "container should not exist")
}

// createThrowawayContainer creates a short-lived container for testing image contents.
func (te *TestEnv) createThrowawayContainer(imageID string) string {
	te.T.Helper()

	agentName := fmt.Sprintf("throwaway-%d", time.Now().UnixNano())
	containerName := te.ContainerName(agentName)

	createOpts := whail.ContainerCreateOptions{
		Config: &container.Config{
			Image: imageID,
			Cmd:   []string{"sleep", "60"},
		},
		Name: containerName,
		ExtraLabels: whail.Labels{
			{docker.LabelProject: te.ProjectName},
			{docker.LabelAgent: agentName},
		},
	}

	resp, err := te.Client.ContainerCreate(te.Ctx, createOpts)
	require.NoError(te.T, err, "failed to create throwaway container")

	_, err = te.Client.ContainerStart(te.Ctx, whail.ContainerStartOptions{
		ContainerID: resp.ID,
	})
	require.NoError(te.T, err, "failed to start throwaway container")

	// Wait for container to be fully running
	time.Sleep(500 * time.Millisecond)

	return resp.ID
}

// removeContainer removes a container without requiring it to succeed.
func (te *TestEnv) removeContainer(containerID string) {
	_, _ = te.Client.ContainerStop(te.Ctx, containerID, nil)
	_, _ = te.Client.ContainerRemove(te.Ctx, containerID, true)
}

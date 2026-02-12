package harness

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/socketbridge"
	"github.com/schmitthub/clawker/pkg/whail"
)

// containerConfig holds options for RunContainer.
type containerConfig struct {
	capAdd    []string
	user      string
	cmd       []string
	env       []string
	extraHost []string
	mounts    []mount.Mount
}

// ContainerOpt configures a test container.
type ContainerOpt func(*containerConfig)

// WithCapAdd adds Linux capabilities to the container (e.g., "NET_ADMIN", "NET_RAW").
func WithCapAdd(caps ...string) ContainerOpt {
	return func(c *containerConfig) {
		c.capAdd = append(c.capAdd, caps...)
	}
}

// WithUser sets the user to run as inside the container.
func WithUser(user string) ContainerOpt {
	return func(c *containerConfig) {
		c.user = user
	}
}

// WithCmd sets the command to run in the container.
func WithCmd(cmd ...string) ContainerOpt {
	return func(c *containerConfig) {
		c.cmd = cmd
	}
}

// WithEnv adds environment variables (KEY=VALUE format).
func WithEnv(env ...string) ContainerOpt {
	return func(c *containerConfig) {
		c.env = append(c.env, env...)
	}
}

// WithExtraHost adds extra host entries (e.g., "host.docker.internal:host-gateway").
func WithExtraHost(hosts ...string) ContainerOpt {
	return func(c *containerConfig) {
		c.extraHost = append(c.extraHost, hosts...)
	}
}

// WithMounts adds bind or volume mounts to the container.
func WithMounts(mounts ...mount.Mount) ContainerOpt {
	return func(c *containerConfig) {
		c.mounts = append(c.mounts, mounts...)
	}
}

// RunningContainer represents a container started by RunContainer.
type RunningContainer struct {
	ID   string
	Name string
}

// ExecResult holds the result of executing a command in a container.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// CleanOutput returns the Stdout with Docker stream headers stripped.
func (r *ExecResult) CleanOutput() string {
	return StripDockerStreamHeaders(r.Stdout)
}

// Exec executes a command in the container and returns the result.
func (c *RunningContainer) Exec(ctx context.Context, dc *docker.Client, cmd ...string) (*ExecResult, error) {
	execResp, err := dc.ExecCreate(ctx, c.ID, docker.ExecCreateOptions{
		AttachStdin:  false,
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create failed: %w", err)
	}

	hijacked, err := dc.ExecAttach(ctx, execResp.ID, docker.ExecAttachOptions{TTY: false})
	if err != nil {
		return nil, fmt.Errorf("exec attach failed: %w", err)
	}
	defer hijacked.Close()

	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, hijacked.Reader)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read exec output: %w", err)
	}

	// Get exit code
	inspectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	inspectResp, err := dc.ExecInspect(inspectCtx, execResp.ID, docker.ExecInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("exec inspect failed: %w", err)
	}

	return &ExecResult{
		ExitCode: inspectResp.ExitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

// WaitForFile waits for a file to exist in the container.
func (c *RunningContainer) WaitForFile(ctx context.Context, dc *docker.Client, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for file %s", path)
		}
		result, err := c.Exec(ctx, dc, "test", "-f", path)
		if err == nil && result.ExitCode == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// GetLogs retrieves container logs.
func (c *RunningContainer) GetLogs(ctx context.Context, rawCli *client.Client) (string, error) {
	reader, err := rawCli.ContainerLogs(ctx, c.ID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}
	return string(data), nil
}

// UniqueContainerName generates a unique test container name.
// Returns: "clawker-test-<short-test-name>-<timestamp>-<random>"
func UniqueContainerName(t *testing.T) string {
	t.Helper()
	name := t.Name()
	// Shorten test name: take last segment after / and truncate
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	// Replace invalid chars
	name = strings.NewReplacer("_", "-", " ", "-").Replace(name)
	// Truncate
	if len(name) > 30 {
		name = name[:30]
	}
	name = strings.ToLower(name)
	suffix := time.Now().Format("150405.000000")
	randBytes := make([]byte, 2)
	_, _ = rand.Read(randBytes)
	randHex := hex.EncodeToString(randBytes)
	return fmt.Sprintf("clawker-test-%s-%s-%s", name, suffix, randHex)
}

// RunContainer creates and starts a container from the given image, returning a
// RunningContainer with automatic cleanup registered via t.Cleanup.
//
// If GPG is available on the host, socket forwarding is automatically configured
// by setting CLAWKER_REMOTE_SOCKETS and starting the socket bridge. This allows
// TDD tests to validate socket forwarding without explicit configuration.
//
// For production code, socket forwarding config comes from clawker.yaml via RuntimeEnv.
func RunContainer(t *testing.T, dc *docker.Client, image string, opts ...ContainerOpt) *RunningContainer {
	t.Helper()

	cfg := &containerConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Auto-detect socket forwarding for test harness
	// Production code uses RuntimeEnv with config from clawker.yaml
	gpgAvailable := isHostGPGAvailable()
	sshAvailable := os.Getenv("SSH_AUTH_SOCK") != ""
	socketCfg := SocketBridgeConfig{
		GPGEnabled: gpgAvailable,
		SSHEnabled: sshAvailable,
	}

	if gpgAvailable || sshAvailable {
		envVal := BuildRemoteSocketsEnv(socketCfg)
		if envVal != "" {
			cfg.env = append(cfg.env, "CLAWKER_REMOTE_SOCKETS="+envVal)
		}
	}

	name := UniqueContainerName(t)
	labels := AddTestLabels(map[string]string{
		ClawkerManagedLabel: "true",
	})

	cmd := cfg.cmd
	if len(cmd) == 0 {
		cmd = []string{"sleep", "infinity"}
	}

	ctx := context.Background()

	createResp, err := dc.ContainerCreate(ctx, whail.ContainerCreateOptions{
		Config: &container.Config{
			Image:  image,
			Cmd:    cmd,
			Labels: labels,
			Env:    cfg.env,
			User:   cfg.user,
		},
		HostConfig: &container.HostConfig{
			CapAdd:     cfg.capAdd,
			ExtraHosts: cfg.extraHost,
			Mounts:     cfg.mounts,
		},
		Name: name,
	})
	if err != nil {
		t.Fatalf("RunContainer: create failed: %v", err)
	}

	if _, err := dc.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: createResp.ID}); err != nil {
		t.Fatalf("RunContainer: start failed: %v", err)
	}

	ctr := &RunningContainer{
		ID:   createResp.ID,
		Name: name,
	}

	// Start socket bridge for GPG/SSH forwarding if enabled
	if gpgAvailable || sshAvailable {
		t.Logf("RunContainer: starting socket bridge (GPG=%v, SSH=%v)", gpgAvailable, sshAvailable)
		_, err := StartSocketBridge(t, createResp.ID, socketCfg)
		if err != nil {
			t.Logf("WARNING: failed to start socket bridge: %v", err)
		}
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := dc.ContainerStop(cleanupCtx, createResp.ID, nil); err != nil {
			t.Logf("WARNING: failed to stop container %s: %v", name, err)
		}
		if _, err := dc.ContainerRemove(cleanupCtx, createResp.ID, true); err != nil {
			t.Logf("WARNING: failed to remove container %s: %v", name, err)
		}
	})

	return ctr
}

// isHostGPGAvailable checks if GPG is available on the host with signing keys.
func isHostGPGAvailable() bool {
	cmd := exec.Command("gpg", "--list-secret-keys", "--keyid-format", "long")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), "sec")
}

// BuildLightImage builds a lightweight Alpine test image with all *.sh scripts
// from internal/bundler/assets/ and internal/hostproxy/internals/ baked in at /usr/local/bin/. The image is
// content-addressed and cached across tests in the same run, producing a single
// shared image regardless of which scripts individual tests use.
//
// The scripts parameter is accepted for backward compatibility but ignored.
//
// The image includes bash, iptables, ipset, iproute2, curl, openssh-client, sudo.
// A "claude" user is created matching production containers.
//
// Cleanup is handled by CleanupTestResources (label-based) — call it from
// TestMain to guarantee removal even after killed runs.
func BuildLightImage(t *testing.T, dc *docker.Client, _ ...string) string {
	t.Helper()

	projectRoot, err := FindProjectRoot()
	if err != nil {
		t.Fatalf("BuildLightImage: failed to find project root: %v", err)
	}

	// Read shell scripts from both bundler/assets and hostproxy/internals
	assetsDir := filepath.Join(projectRoot, "internal", "bundler", "assets")
	internalsDir := filepath.Join(projectRoot, "internal", "hostproxy", "internals")

	var allScripts []string // .sh files
	var goSources []string  // .go files
	scriptContents := make(map[string][]byte)

	// Read .sh files from bundler/assets (entrypoint.sh, init-firewall.sh, statusline.sh)
	assetsEntries, err := os.ReadDir(assetsDir)
	if err != nil {
		t.Fatalf("BuildLightImage: failed to read assets dir: %v", err)
	}
	for _, entry := range assetsEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sh" {
			continue
		}
		name := entry.Name()
		content, err := os.ReadFile(filepath.Join(assetsDir, name))
		if err != nil {
			t.Fatalf("BuildLightImage: failed to read %s: %v", name, err)
		}
		scriptContents[name] = content
		allScripts = append(allScripts, name)
	}

	// Read .sh files from hostproxy/internals (host-open.sh, git-credential-clawker.sh)
	internalsEntries, err := os.ReadDir(internalsDir)
	if err != nil {
		t.Fatalf("BuildLightImage: failed to read internals dir: %v", err)
	}
	for _, entry := range internalsEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sh" {
			continue
		}
		name := entry.Name()
		content, err := os.ReadFile(filepath.Join(internalsDir, name))
		if err != nil {
			t.Fatalf("BuildLightImage: failed to read %s: %v", name, err)
		}
		scriptContents[name] = content
		allScripts = append(allScripts, name)
	}

	// Read Go sources from hostproxy/internals/cmd/ subdirectories
	goSourcePaths := []struct {
		path     string
		basename string
	}{
		{filepath.Join(internalsDir, "cmd", "callback-forwarder", "main.go"), "callback-forwarder.go"},
		{filepath.Join(internalsDir, "cmd", "clawker-socket-server", "main.go"), "clawker-socket-server.go"},
	}
	for _, gs := range goSourcePaths {
		content, err := os.ReadFile(gs.path)
		if err != nil {
			t.Fatalf("BuildLightImage: failed to read %s: %v", gs.path, err)
		}
		scriptContents[gs.basename] = content
		goSources = append(goSources, gs.basename)
	}

	// Generate Dockerfile with all scripts and Go sources
	dockerfile := generateLightDockerfile(allScripts, goSources)

	// Compute content hash for cache key
	hasher := sha256.New()
	hasher.Write([]byte(dockerfile))
	for _, name := range allScripts {
		hasher.Write([]byte(name))
		hasher.Write(scriptContents[name])
	}
	for _, name := range goSources {
		hasher.Write([]byte(name))
		hasher.Write(scriptContents[name])
	}
	hashShort := hex.EncodeToString(hasher.Sum(nil))[:12]
	imageTag := fmt.Sprintf("clawker-light:%s", hashShort)

	// Check if image already exists (cache hit)
	ctx := context.Background()
	exists, existsErr := dc.ImageExists(ctx, imageTag)
	if existsErr != nil {
		t.Logf("BuildLightImage: ImageExists check failed: %v", existsErr)
	}
	if exists {
		t.Logf("BuildLightImage: using cached image %s", imageTag)
		return imageTag
	}

	// Build tar context
	buildCtx, err := createLightBuildContext(dockerfile, allScripts, goSources, scriptContents)
	if err != nil {
		t.Fatalf("BuildLightImage: failed to create build context: %v", err)
	}

	// Build via clawker Docker client (wraps whail.Engine) — NOT raw moby client.
	// Using dc.BuildImage ensures managed labels + test labels are auto-injected
	// (dev.clawker.managed=true, dev.clawker.test=true, dev.clawker.test.name).
	labels := map[string]string{
		TestLabel: TestLabelValue,
	}

	t.Logf("BuildLightImage: building %s with %d scripts", imageTag, len(allScripts))
	err = dc.BuildImage(ctx, buildCtx, docker.BuildImageOpts{
		Tags:           []string{imageTag},
		Labels:         labels,
		SuppressOutput: true,
	})
	if err != nil {
		t.Fatalf("BuildLightImage: build failed: %v", err)
	}

	// Prune dangling intermediate images from multi-stage build.
	// Docker's legacy builder keeps intermediate stage images (Go builder stages)
	// as dangling images without labels. These are unmanaged build cache artifacts
	// that won't be cleaned by whail's label-based cleanup.
	// Uses raw client because whail.ImagesPrune only targets managed images.
	pruneClient := NewRawDockerClient(t)
	danglingFilter := client.Filters{}.Add("dangling", "true")
	if _, err := pruneClient.ImagePrune(ctx, client.ImagePruneOptions{
		Filters: danglingFilter,
	}); err != nil {
		t.Logf("BuildLightImage: WARNING: failed to prune dangling images: %v", err)
	}

	return imageTag
}

// generateLightDockerfile creates a minimal Dockerfile for test images.
func generateLightDockerfile(scripts []string, goSources []string) string {
	var sb strings.Builder

	// Add Go builder stages for each .go source
	for _, goFile := range goSources {
		binaryName := strings.TrimSuffix(goFile, ".go")
		stageName := binaryName + "-builder"
		fmt.Fprintf(&sb, "FROM golang:1.23-alpine AS %s\n", stageName)
		sb.WriteString("WORKDIR /build\n")
		fmt.Fprintf(&sb, "COPY %s .\n", goFile)
		fmt.Fprintf(&sb, "RUN CGO_ENABLED=0 go build -ldflags=\"-s -w\" -o %s %s\n\n", binaryName, goFile)
	}

	sb.WriteString("FROM alpine:3.21\n")
	fmt.Fprintf(&sb, "LABEL %s=%s %s=true\n", TestLabel, TestLabelValue, ClawkerManagedLabel)
	sb.WriteString("RUN apk add --no-cache bash curl jq git iptables ipset iproute2 openssh-client openssl coreutils grep sed procps sudo bind-tools gnupg file\n")
	fmt.Fprintf(&sb, "RUN adduser -D -u %d -s /bin/bash -h /home/claude claude\n", config.ContainerUID)
	sb.WriteString("RUN mkdir -p /var/run/clawker /home/claude/.ssh /home/claude/.claude /home/claude/.clawker-share /workspace && chown -R claude:claude /home/claude /var/run/clawker /workspace\n")
	// Configure NOPASSWD sudo for firewall script (matches production Dockerfile)
	sb.WriteString("RUN echo 'claude ALL=(root) NOPASSWD: /usr/local/bin/init-firewall.sh' > /etc/sudoers.d/claude-firewall && chmod 0440 /etc/sudoers.d/claude-firewall\n")

	if len(scripts) > 0 {
		sb.WriteString("COPY scripts/ /usr/local/bin/\n")
		sb.WriteString("RUN chmod +x /usr/local/bin/*.sh\n")
	}

	// Copy compiled Go binaries from builder stages
	for _, goFile := range goSources {
		binaryName := strings.TrimSuffix(goFile, ".go")
		stageName := binaryName + "-builder"
		fmt.Fprintf(&sb, "COPY --from=%s /build/%s /usr/local/bin/%s\n", stageName, binaryName, binaryName)
	}
	if len(goSources) > 0 {
		sb.WriteString("RUN chmod +x")
		for _, goFile := range goSources {
			fmt.Fprintf(&sb, " /usr/local/bin/%s", strings.TrimSuffix(goFile, ".go"))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("USER claude\n")
	sb.WriteString("WORKDIR /workspace\n")
	sb.WriteString("CMD [\"sleep\", \"infinity\"]\n")

	return sb.String()
}

// createLightBuildContext creates a tar archive with Dockerfile, scripts, and Go sources.
func createLightBuildContext(dockerfile string, scripts []string, goSources []string, contents map[string][]byte) (io.Reader, error) {
	buf := new(bytes.Buffer)
	tw := tar.NewWriter(buf)

	// Add Dockerfile
	if err := addTarFile(tw, "Dockerfile", []byte(dockerfile)); err != nil {
		return nil, err
	}

	// Add shell scripts under scripts/ directory
	for _, name := range scripts {
		if err := addTarFile(tw, "scripts/"+name, contents[name]); err != nil {
			return nil, err
		}
	}

	// Add Go sources at root level (for builder stage COPY)
	for _, name := range goSources {
		if err := addTarFile(tw, name, contents[name]); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf, nil
}

// addTarFile adds a single file to a tar writer.
func addTarFile(tw *tar.Writer, name string, content []byte) error {
	header := &tar.Header{
		Name: name,
		Mode: 0755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(content)
	return err
}

// SocketBridgeConfig defines which sockets to forward.
type SocketBridgeConfig struct {
	GPGEnabled bool // Forward GPG agent
	SSHEnabled bool // Forward SSH agent
}

// DefaultGPGSocketPath returns the default GPG socket path for the claude user.
func DefaultGPGSocketPath() string {
	return "/home/claude/.gnupg/S.gpg-agent"
}

// DefaultSSHSocketPath returns the default SSH socket path for the claude user.
func DefaultSSHSocketPath() string {
	return "/home/claude/.ssh/agent.sock"
}

// BuildRemoteSocketsEnv builds the CLAWKER_REMOTE_SOCKETS env var value.
func BuildRemoteSocketsEnv(cfg SocketBridgeConfig) string {
	var sockets []map[string]string
	if cfg.GPGEnabled {
		sockets = append(sockets, map[string]string{
			"path": DefaultGPGSocketPath(),
			"type": "gpg-agent",
		})
	}
	if cfg.SSHEnabled {
		sockets = append(sockets, map[string]string{
			"path": DefaultSSHSocketPath(),
			"type": "ssh-agent",
		})
	}
	if len(sockets) == 0 {
		return ""
	}
	data, _ := json.Marshal(sockets)
	return string(data)
}

// WithSocketForwarding configures socket forwarding for a container.
// Returns a ContainerOpt that sets the CLAWKER_REMOTE_SOCKETS env var.
func WithSocketForwarding(cfg SocketBridgeConfig) ContainerOpt {
	return func(c *containerConfig) {
		envVal := BuildRemoteSocketsEnv(cfg)
		if envVal != "" {
			c.env = append(c.env, "CLAWKER_REMOTE_SOCKETS="+envVal)
		}
	}
}

// StartSocketBridge starts a socket bridge for the given container.
// It launches the socket-forwarder via docker exec and handles the muxrpc protocol.
// The bridge runs in a goroutine and should be stopped via the returned stop function.
// Returns a stop function and any error from starting the bridge.
func StartSocketBridge(t *testing.T, containerID string, cfg SocketBridgeConfig) (stop func(), err error) {
	t.Helper()

	bridge := socketbridge.NewBridge(containerID, cfg.GPGEnabled)

	ctx, cancel := context.WithCancel(context.Background())

	// Start bridge in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- bridge.Start(ctx)
	}()

	// Wait for ready or error
	select {
	case err := <-errCh:
		if err != nil {
			cancel() // Only cancel on error
			return nil, fmt.Errorf("socket bridge start failed: %w", err)
		}
		// Success - don't cancel! The context keeps the docker exec running.
	case <-time.After(30 * time.Second):
		cancel()
		return nil, fmt.Errorf("socket bridge start timed out")
	}

	// Return stop function
	stop = func() {
		cancel()
		bridge.Stop()
	}

	t.Cleanup(stop)
	return stop, nil
}

// UniqueAgentName generates a unique agent name suitable for parallel test isolation.
// Format: "test-<short-test-name>-<timestamp>-<random>"
func UniqueAgentName(t *testing.T) string {
	t.Helper()
	name := t.Name()
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.NewReplacer("_", "-", " ", "-").Replace(name)
	if len(name) > 20 {
		name = name[:20]
	}
	name = strings.ToLower(name)
	randBytes := make([]byte, 2)
	_, _ = rand.Read(randBytes)
	randHex := hex.EncodeToString(randBytes)
	return fmt.Sprintf("test-%s-%s-%s", name, time.Now().Format("150405"), randHex)
}

// WithConfigVolume creates a named config volume and returns a ContainerOpt
// that mounts it at /home/claude/.claude. The volume is cleaned up via t.Cleanup.
//
// Use this when you want one-step volume creation + mount. If you create the
// volume separately (e.g., via EnsureVolume + InitContainerConfig), use
// WithVolumeMount instead to avoid duplicate creation/cleanup.
func WithConfigVolume(t *testing.T, dc *docker.Client, project, agent string) ContainerOpt {
	t.Helper()
	volumeName := docker.VolumeName(project, agent, "config")
	ctx := context.Background()

	_, err := dc.EnsureVolume(ctx, volumeName, nil)
	if err != nil {
		t.Fatalf("WithConfigVolume: failed to create volume %s: %v", volumeName, err)
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := dc.VolumeRemove(cleanupCtx, volumeName, true); err != nil {
			t.Logf("WARNING: failed to remove config volume %s: %v", volumeName, err)
		}
	})

	return WithMounts(mount.Mount{
		Type:   mount.TypeVolume,
		Source: volumeName,
		Target: "/home/claude/.claude",
	})
}

// WithVolumeMount returns a ContainerOpt that mounts an existing volume at the
// given target path. Unlike WithConfigVolume, it does not create the volume or
// register cleanup — use when the volume is managed separately (e.g., via
// EnsureVolume + InitContainerConfig before container start).
func WithVolumeMount(volumeName, target string) ContainerOpt {
	return WithMounts(mount.Mount{
		Type:   mount.TypeVolume,
		Source: volumeName,
		Target: target,
	})
}

// FileExists checks if a file exists at path inside the container.
func (c *RunningContainer) FileExists(ctx context.Context, dc *docker.Client, path string) bool {
	result, err := c.Exec(ctx, dc, "test", "-f", path)
	if err != nil {
		return false
	}
	return result.ExitCode == 0
}

// DirExists checks if a directory exists at path inside the container.
func (c *RunningContainer) DirExists(ctx context.Context, dc *docker.Client, path string) bool {
	result, err := c.Exec(ctx, dc, "test", "-d", path)
	if err != nil {
		return false
	}
	return result.ExitCode == 0
}

// ReadFile reads the content of a file inside the container.
// Returns the trimmed content and any error from exec or non-zero exit code.
func (c *RunningContainer) ReadFile(ctx context.Context, dc *docker.Client, path string) (string, error) {
	result, err := c.Exec(ctx, dc, "cat", path)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("cat %s failed (exit %d): %s", path, result.ExitCode, result.Stderr)
	}
	return strings.TrimSpace(result.Stdout), nil
}

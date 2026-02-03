package harness

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/pkg/whail"
)

// containerConfig holds options for RunContainer.
type containerConfig struct {
	capAdd    []string
	user      string
	cmd       []string
	env       []string
	extraHost []string
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
func RunContainer(t *testing.T, dc *docker.Client, image string, opts ...ContainerOpt) *RunningContainer {
	t.Helper()

	cfg := &containerConfig{}
	for _, opt := range opts {
		opt(cfg)
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
		},
		Name: name,
	})
	if err != nil {
		t.Fatalf("RunContainer: create failed: %v", err)
	}

	if _, err := dc.ContainerStart(ctx, whail.ContainerStartOptions{ContainerID: createResp.ID}); err != nil {
		t.Fatalf("RunContainer: start failed: %v", err)
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

	return &RunningContainer{
		ID:   createResp.ID,
		Name: name,
	}
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
		{filepath.Join(internalsDir, "cmd", "ssh-agent-proxy", "main.go"), "ssh-agent-proxy.go"},
		{filepath.Join(internalsDir, "cmd", "callback-forwarder", "main.go"), "callback-forwarder.go"},
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

	// Build via raw Docker SDK ImageBuild (not BuildKit)
	rawClient := NewRawDockerClient(t)

	labels := map[string]string{
		TestLabel:           TestLabelValue,
		ClawkerManagedLabel: "true",
	}

	buildOpts := client.ImageBuildOptions{
		Tags:        []string{imageTag},
		Dockerfile:  "Dockerfile",
		Labels:      labels,
		Remove:      true,
		ForceRemove: true,
	}

	t.Logf("BuildLightImage: building %s with %d scripts", imageTag, len(allScripts))
	resp, err := rawClient.ImageBuild(ctx, buildCtx, buildOpts)
	if err != nil {
		t.Fatalf("BuildLightImage: build failed: %v", err)
	}
	defer resp.Body.Close()

	// Consume build output
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("BuildLightImage: failed to read build output: %v", err)
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
	sb.WriteString("RUN apk add --no-cache bash curl jq git iptables ipset iproute2 openssh-client openssl coreutils grep sed procps sudo bind-tools\n")
	sb.WriteString("RUN adduser -D -s /bin/bash -h /home/claude claude\n")
	sb.WriteString("RUN mkdir -p /var/run/clawker /home/claude/.ssh /home/claude/.claude /workspace && chown -R claude:claude /home/claude /var/run/clawker /workspace\n")

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

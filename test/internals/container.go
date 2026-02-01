// Package integration provides reusable test infrastructure for internals testing
// clawker scripts and components in lightweight containers.
package integration

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// LightContainer provides a builder for lightweight test containers.
// These containers include only the scripts needed for testing,
// without the full Claude Code setup.
type LightContainer struct {
	// BaseImage is the container base image (e.g., "alpine:latest", "debian:bookworm-slim")
	BaseImage string

	// Scripts are script files to copy into the container from internal/build/templates/
	Scripts []string

	// Env are environment variables to set in the container
	Env map[string]string

	// Cmd is the command to run in the container
	Cmd []string

	// CapAdd are Linux capabilities to add (e.g., "NET_ADMIN", "NET_RAW")
	CapAdd []string

	// ExposedPorts are ports to expose (e.g., "8080/tcp")
	ExposedPorts []string

	// WaitStrategy defines how to wait for the container to be ready
	WaitStrategy wait.Strategy

	// Files are additional files to copy into the container (dest -> content)
	Files map[string]string

	// User is the user to run as (default: root)
	User string
}

// ContainerResult holds the result of starting a LightContainer.
type ContainerResult struct {
	Container testcontainers.Container
	Host      string
	Ports     map[string]string // ExposedPort -> MappedPort
}

// NewLightContainer creates a new LightContainer builder with defaults.
func NewLightContainer() *LightContainer {
	return &LightContainer{
		BaseImage: "alpine:latest",
		Env:       make(map[string]string),
		Files:     make(map[string]string),
	}
}

// WithBaseImage sets the base image.
func (c *LightContainer) WithBaseImage(image string) *LightContainer {
	c.BaseImage = image
	return c
}

// WithScripts adds scripts to copy from internal/build/templates/.
func (c *LightContainer) WithScripts(scripts ...string) *LightContainer {
	c.Scripts = append(c.Scripts, scripts...)
	return c
}

// WithEnv adds environment variables.
func (c *LightContainer) WithEnv(key, value string) *LightContainer {
	c.Env[key] = value
	return c
}

// WithEnvMap adds multiple environment variables.
func (c *LightContainer) WithEnvMap(env map[string]string) *LightContainer {
	for k, v := range env {
		c.Env[k] = v
	}
	return c
}

// WithCmd sets the container command.
func (c *LightContainer) WithCmd(cmd ...string) *LightContainer {
	c.Cmd = cmd
	return c
}

// WithCapabilities adds Linux capabilities.
func (c *LightContainer) WithCapabilities(caps ...string) *LightContainer {
	c.CapAdd = append(c.CapAdd, caps...)
	return c
}

// WithExposedPorts adds ports to expose.
func (c *LightContainer) WithExposedPorts(ports ...string) *LightContainer {
	c.ExposedPorts = append(c.ExposedPorts, ports...)
	return c
}

// WithWaitStrategy sets the wait strategy.
func (c *LightContainer) WithWaitStrategy(strategy wait.Strategy) *LightContainer {
	c.WaitStrategy = strategy
	return c
}

// WithFile adds a file to copy into the container.
func (c *LightContainer) WithFile(destPath, content string) *LightContainer {
	c.Files[destPath] = content
	return c
}

// WithUser sets the user to run as.
func (c *LightContainer) WithUser(user string) *LightContainer {
	c.User = user
	return c
}

// Start creates and starts the container, returning a ContainerResult.
// The caller is responsible for terminating the container.
func (c *LightContainer) Start(ctx context.Context, t *testing.T) (*ContainerResult, error) {
	t.Helper()

	// Create container request
	req := testcontainers.ContainerRequest{
		Image:        c.BaseImage,
		Env:          c.Env,
		Cmd:          c.Cmd,
		ExposedPorts: c.ExposedPorts,
		CapAdd:       c.CapAdd,
		WaitingFor:   c.WaitStrategy,
		User:         c.User,
	}

	// Use GenericContainer
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Register cleanup
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("WARNING: failed to terminate container: %v", err)
		}
	})

	// Get host
	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	// Get mapped ports
	ports := make(map[string]string)
	for _, port := range c.ExposedPorts {
		mappedPort, err := container.MappedPort(ctx, nat.Port(port))
		if err != nil {
			return nil, fmt.Errorf("failed to get mapped port for %s: %w", port, err)
		}
		ports[port] = mappedPort.Port()
	}

	return &ContainerResult{
		Container: container,
		Host:      host,
		Ports:     ports,
	}, nil
}

// buildDockerfile generates a Dockerfile for the container.
func (c *LightContainer) buildDockerfile() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("FROM %s\n", c.BaseImage))

	// Install basic tools based on image type
	if strings.Contains(c.BaseImage, "alpine") {
		sb.WriteString("RUN apk add --no-cache bash curl jq git\n")
	} else if strings.Contains(c.BaseImage, "debian") || strings.Contains(c.BaseImage, "ubuntu") {
		sb.WriteString("RUN apt-get update && apt-get install -y bash curl jq git && rm -rf /var/lib/apt/lists/*\n")
	}

	// Add capabilities require iptables for firewall tests
	for _, cap := range c.CapAdd {
		if cap == "NET_ADMIN" || cap == "NET_RAW" {
			if strings.Contains(c.BaseImage, "alpine") {
				sb.WriteString("RUN apk add --no-cache iptables\n")
			} else {
				sb.WriteString("RUN apt-get update && apt-get install -y iptables && rm -rf /var/lib/apt/lists/*\n")
			}
			break
		}
	}

	// Copy scripts
	for _, script := range c.Scripts {
		basename := filepath.Base(script)
		sb.WriteString(fmt.Sprintf("COPY %s /usr/local/bin/%s\n", script, basename))
		sb.WriteString(fmt.Sprintf("RUN chmod +x /usr/local/bin/%s\n", basename))
	}

	// Set user
	if c.User != "" {
		sb.WriteString(fmt.Sprintf("USER %s\n", c.User))
	}

	return sb.String()
}

// StartFromDockerfile creates and starts a container from a Dockerfile in testdata/.
func StartFromDockerfile(ctx context.Context, t *testing.T, dockerfilePath string, opts ...func(*testcontainers.ContainerRequest)) (*ContainerResult, error) {
	t.Helper()

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    filepath.Dir(dockerfilePath),
			Dockerfile: filepath.Base(dockerfilePath),
		},
	}

	// Apply options
	for _, opt := range opts {
		opt(&req)
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("WARNING: failed to terminate container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	return &ContainerResult{
		Container: container,
		Host:      host,
		Ports:     make(map[string]string),
	}, nil
}

// ExecResult holds the result of executing a command in a container.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// CleanOutput returns the Stdout with Docker stream headers stripped.
func (r *ExecResult) CleanOutput() string {
	return harness.StripDockerStreamHeaders(r.Stdout)
}

// Exec executes a command in the container and returns the result.
func (r *ContainerResult) Exec(ctx context.Context, cmd []string) (*ExecResult, error) {
	code, reader, err := r.Container.Exec(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("exec failed: %w", err)
	}

	output, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read exec output: %w", err)
	}

	return &ExecResult{
		ExitCode: code,
		Stdout:   string(output),
	}, nil
}

// WaitForFile waits for a file to exist in the container.
func (r *ContainerResult) WaitForFile(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for file %s", path)
		}

		result, err := r.Exec(ctx, []string{"test", "-f", path})
		if err == nil && result.ExitCode == 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			// Continue polling
		}
	}
}

// GetLogs retrieves container logs.
func (r *ContainerResult) GetLogs(ctx context.Context) (string, error) {
	reader, err := r.Container.Logs(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %w", err)
	}
	defer reader.Close()

	logs, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}

	return string(logs), nil
}

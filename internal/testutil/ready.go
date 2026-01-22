package testutil

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"
)

// Timeout constants for different test scenarios.
const (
	// DefaultReadyTimeout is the default timeout for waiting for container readiness.
	DefaultReadyTimeout = 60 * time.Second

	// E2EReadyTimeout is the timeout for E2E tests which may take longer.
	E2EReadyTimeout = 120 * time.Second

	// CIReadyTimeout is the timeout for CI environments which may be slower.
	CIReadyTimeout = 180 * time.Second

	// BypassCommandTimeout is the timeout for entrypoint bypass commands.
	BypassCommandTimeout = 10 * time.Second
)

// Ready signal constants matching what the entrypoint emits.
const (
	// ReadyFilePath is the path to the ready signal file inside containers.
	ReadyFilePath = "/var/run/clawker/ready"

	// ReadyLogPrefix is the prefix for the ready signal log line.
	ReadyLogPrefix = "[clawker] ready"

	// ErrorLogPrefix is the prefix for error signal log lines.
	ErrorLogPrefix = "[clawker] error"
)

// GetReadyTimeout returns the appropriate timeout for waiting on ready signals.
// It checks the CLAWKER_READY_TIMEOUT environment variable first.
func GetReadyTimeout() time.Duration {
	if envTimeout := os.Getenv("CLAWKER_READY_TIMEOUT"); envTimeout != "" {
		if secs, err := strconv.Atoi(envTimeout); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}

	// Check if running in CI
	if os.Getenv("CI") == "true" || os.Getenv("GITHUB_ACTIONS") == "true" {
		return CIReadyTimeout
	}

	return DefaultReadyTimeout
}

// WaitForReadyFile waits for the ready signal file to exist in the container.
// Returns nil when the file exists, or an error if timeout is reached or exec fails.
func WaitForReadyFile(ctx context.Context, cli *client.Client, containerID string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for ready file: %w", ctx.Err())
		case <-ticker.C:
			// Execute check command
			exists, err := checkFileExists(ctx, cli, containerID, ReadyFilePath)
			if err != nil {
				// Check if container is still running
				info, inspectErr := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
				if inspectErr == nil && !info.Container.State.Running {
					// Container exited - fail fast with useful error
					exitCode := info.Container.State.ExitCode
					return fmt.Errorf("container exited (code %d) while waiting for ready file", exitCode)
				}
				// Transient error (container starting up) - continue waiting
				continue
			}
			if exists {
				return nil
			}
		}
	}
}

// checkFileExists checks if a file exists in a container using exec.
func checkFileExists(ctx context.Context, cli *client.Client, containerID, path string) (bool, error) {
	execConfig := client.ExecCreateOptions{
		Cmd:          []string{"test", "-f", path},
		AttachStdout: false,
		AttachStderr: false,
	}

	execResp, err := cli.ExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return false, err
	}

	startResp, err := cli.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return false, err
	}
	defer startResp.Close()

	// Wait for exec to complete and get exit code
	for {
		inspect, err := cli.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
		if err != nil {
			return false, err
		}
		if !inspect.Running {
			return inspect.ExitCode == 0, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}


// WaitForContainerCompletion waits for a short-lived container to complete.
// For containers that exit quickly (like echo hello), this function:
// 1. Checks if container is still running - if so, waits for ready file
// 2. If container already exited with code 0, verifies ready signal in logs
// This is the appropriate wait function for entrypoint bypass commands.
func WaitForContainerCompletion(ctx context.Context, cli *client.Client, containerID string) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for container completion: %w", ctx.Err())
		case <-ticker.C:
			// Check container state
			info, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
			if err != nil {
				return fmt.Errorf("failed to inspect container: %w", err)
			}

			if info.Container.State.Running {
				// Container still running - try to check ready file
				exists, err := checkFileExists(ctx, cli, containerID, ReadyFilePath)
				if err != nil {
					// Exec failed, maybe container is exiting - continue polling
					continue
				}
				if exists {
					return nil
				}
				// Ready file doesn't exist yet, continue polling
				continue
			}

			// Container has exited - check if it was successful
			if info.Container.State.ExitCode != 0 {
				return fmt.Errorf("container exited with code %d", info.Container.State.ExitCode)
			}

			// Container exited with code 0, verify ready signal was emitted
			logs, err := GetContainerLogs(ctx, cli, containerID)
			if err != nil {
				return fmt.Errorf("failed to get container logs: %w", err)
			}

			if !strings.Contains(logs, ReadyLogPrefix) {
				return fmt.Errorf("container exited but ready signal not found in logs")
			}

			return nil
		}
	}
}

// WaitForHealthy waits for the container to be healthy using Docker's HEALTHCHECK.
// Returns nil when healthy, or an error if timeout is reached.
func WaitForHealthy(ctx context.Context, cli *client.Client, containerID string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for healthy state: %w", ctx.Err())
		case <-ticker.C:
			info, err := cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
			if err != nil {
				continue
			}

			// Check if container has health info
			if info.Container.State.Health != nil {
				switch info.Container.State.Health.Status {
				case "healthy":
					return nil
				case "unhealthy":
					return fmt.Errorf("container is unhealthy")
				}
				// "starting" state - continue waiting
			} else {
				// No health check configured, fall back to running state
				if info.Container.State.Running {
					return nil
				}
			}
		}
	}
}

// WaitForLogPattern waits for a specific pattern to appear in container logs.
// Returns nil when the pattern is found, or an error if timeout is reached.
func WaitForLogPattern(ctx context.Context, cli *client.Client, containerID, pattern string) error {
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern: %w", err)
	}

	// Create a new context for log streaming that we can cancel
	logCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	logs, err := cli.ContainerLogs(logCtx, containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: false,
	})
	if err != nil {
		return fmt.Errorf("failed to get container logs: %w", err)
	}
	defer logs.Close()

	// Read logs line by line
	reader := bufio.NewReader(logs)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for log pattern %q: %w", pattern, ctx.Err())
		default:
			// Docker multiplexed logs have 8-byte header, but we'll handle raw text
			line, err := reader.ReadString('\n')
			if err == io.EOF {
				// Wait a bit and continue
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if err != nil {
				// Check if context was cancelled
				if ctx.Err() != nil {
					return fmt.Errorf("timeout waiting for log pattern %q: %w", pattern, ctx.Err())
				}
				// Connection-level errors indicate container may be gone
				errStr := err.Error()
				if strings.Contains(errStr, "connection reset") ||
					strings.Contains(errStr, "broken pipe") ||
					strings.Contains(errStr, "use of closed") {
					return fmt.Errorf("lost connection to container while waiting for pattern %q: %w", pattern, err)
				}
				// Other transient errors - continue
				continue
			}

			// Strip Docker's multiplexed stream header if present (8 bytes)
			if len(line) > 8 && (line[0] == 1 || line[0] == 2) && line[1] == 0 && line[2] == 0 && line[3] == 0 {
				line = line[8:]
			}

			if compiled.MatchString(line) {
				return nil
			}
		}
	}
}

// WaitForReadyLog waits for the ready signal log line.
// This is a convenience wrapper around WaitForLogPattern.
func WaitForReadyLog(ctx context.Context, cli *client.Client, containerID string) error {
	return WaitForLogPattern(ctx, cli, containerID, regexp.QuoteMeta(ReadyLogPrefix))
}

// CheckForErrorPattern checks container logs for error patterns.
// Returns (true, error message) if an error pattern is found, (false, "") otherwise.
func CheckForErrorPattern(logs string) (bool, string) {
	lines := strings.Split(logs, "\n")
	for _, line := range lines {
		// Strip Docker multiplexed header if present
		if len(line) > 8 && (line[0] == 1 || line[0] == 2) && line[1] == 0 && line[2] == 0 && line[3] == 0 {
			line = line[8:]
		}

		if strings.Contains(line, ErrorLogPrefix) {
			// Extract the error message
			if idx := strings.Index(line, "msg="); idx >= 0 {
				return true, strings.TrimSpace(line[idx+4:])
			}
			return true, strings.TrimPrefix(line, ErrorLogPrefix)
		}
	}
	return false, ""
}

// GetContainerLogs retrieves all logs from a container.
func GetContainerLogs(ctx context.Context, cli *client.Client, containerID string) (string, error) {
	logs, err := cli.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     false,
		Timestamps: false,
	})
	if err != nil {
		return "", err
	}
	defer logs.Close()

	var buf strings.Builder
	_, err = io.Copy(&buf, logs)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// VerifyProcessRunning checks if a process matching the pattern is running in the container.
// Uses pgrep -f to search for the pattern in the full command line.
// Returns nil if process is found, error otherwise.
func VerifyProcessRunning(ctx context.Context, cli *client.Client, containerID, pattern string) error {
	execConfig := client.ExecCreateOptions{
		Cmd:          []string{"pgrep", "-f", pattern},
		AttachStdout: true,
		AttachStderr: true,
	}

	execResp, err := cli.ExecCreate(ctx, containerID, execConfig)
	if err != nil {
		return fmt.Errorf("failed to create exec: %w", err)
	}

	attachResp, err := cli.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("failed to attach to exec: %w", err)
	}
	defer attachResp.Close()

	// Read output
	var buf strings.Builder
	// Note: io.Copy error is ignored because pgrep exit code is the authoritative result.
	// The buffer may be incomplete but we only need to verify process exists.
	_, _ = io.Copy(&buf, attachResp.Conn)

	// Wait for exec to complete and get exit code
	for {
		inspect, err := cli.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
		if err != nil {
			return fmt.Errorf("failed to inspect exec: %w", err)
		}
		if !inspect.Running {
			if inspect.ExitCode != 0 {
				return fmt.Errorf("process matching %q not found (pgrep exit code %d)", pattern, inspect.ExitCode)
			}
			// Process found - pgrep returns 0 and outputs matching PIDs
			output := strings.TrimSpace(buf.String())
			if output == "" {
				return fmt.Errorf("process matching %q not found (no PIDs returned)", pattern)
			}
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// VerifyClaudeCodeRunning is a convenience wrapper to verify Claude Code is running.
// Searches for a process with "claude" in the command line.
func VerifyClaudeCodeRunning(ctx context.Context, cli *client.Client, containerID string) error {
	return VerifyProcessRunning(ctx, cli, containerID, "claude")
}

// ReadyFileContent represents the parsed content of the ready signal file.
type ReadyFileContent struct {
	Timestamp int64
	PID       int
}

// ParseReadyFile parses the content of a ready signal file.
// Format: ts=<timestamp> pid=<pid>
func ParseReadyFile(content string) (*ReadyFileContent, error) {
	result := &ReadyFileContent{}

	for _, part := range strings.Fields(content) {
		if strings.HasPrefix(part, "ts=") {
			ts, err := strconv.ParseInt(strings.TrimPrefix(part, "ts="), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid timestamp: %w", err)
			}
			result.Timestamp = ts
		} else if strings.HasPrefix(part, "pid=") {
			pid, err := strconv.Atoi(strings.TrimPrefix(part, "pid="))
			if err != nil {
				return nil, fmt.Errorf("invalid pid: %w", err)
			}
			result.PID = pid
		}
	}

	if result.Timestamp == 0 {
		return nil, fmt.Errorf("missing timestamp in ready file")
	}

	return result, nil
}

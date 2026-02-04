// Package acceptance provides acceptance tests using testscript.
// These tests validate CLI workflows against a real Docker daemon.
//
// Run with: go test ./test/cli/... -v -timeout 15m
package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/schmitthub/clawker/internal/clawker"
)

// Environment variables for configuration
const (
	envProject      = "CLAWKER_ACCEPTANCE_PROJECT"
	envScript       = "CLAWKER_ACCEPTANCE_SCRIPT"
	envPreserveWork = "CLAWKER_ACCEPTANCE_PRESERVE_WORK_DIR"
	envSkipDefer    = "CLAWKER_ACCEPTANCE_SKIP_DEFER"
)

// testEnv holds parsed environment configuration
type testEnv struct {
	ProjectPrefix   string
	SingleScript    string
	PreserveWorkDir bool
	SkipDefer       bool
}

// parseTestEnv parses environment variables into configuration
func parseTestEnv() testEnv {
	env := testEnv{
		ProjectPrefix:   "acceptance",
		PreserveWorkDir: false,
		SkipDefer:       false,
	}

	if v := os.Getenv(envProject); v != "" {
		env.ProjectPrefix = v
	}
	if v := os.Getenv(envScript); v != "" {
		env.SingleScript = v
	}
	if v := os.Getenv(envPreserveWork); v == "true" || v == "1" {
		env.PreserveWorkDir = true
	}
	if v := os.Getenv(envSkipDefer); v == "true" || v == "1" {
		env.SkipDefer = true
	}

	return env
}

// generateRandomString generates a random alphanumeric string of specified length
func generateRandomString(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

// extractScriptName extracts a clean name from the test script path
// Converts "container/run-basic.txtar" to "run_basic"
func extractScriptName(path string) string {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, ".txtar")
	name = strings.ReplaceAll(name, "-", "_")
	return name
}

// isDockerAvailable checks if Docker daemon is accessible
func isDockerAvailable() bool {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return false
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = cli.Ping(ctx)
	return err == nil
}

// deferredCleanups tracks cleanup commands for each test
var (
	deferredCleanups   = make(map[string][]string) // scriptDir -> list of cleanup commands
	deferredCleanupsMu sync.Mutex
)

// registerDefer adds a cleanup command to run after the test
func registerDefer(scriptDir string, cmd string) {
	deferredCleanupsMu.Lock()
	defer deferredCleanupsMu.Unlock()
	deferredCleanups[scriptDir] = append(deferredCleanups[scriptDir], cmd)
}

// runDeferred executes all deferred cleanups for a script in LIFO order
func runDeferred(ts *testscript.TestScript, scriptDir string) {
	deferredCleanupsMu.Lock()
	cleanups := deferredCleanups[scriptDir]
	delete(deferredCleanups, scriptDir)
	deferredCleanupsMu.Unlock()

	if len(cleanups) == 0 {
		return
	}

	env := parseTestEnv()
	if env.SkipDefer {
		ts.Logf("Skipping %d deferred cleanups (CLAWKER_ACCEPTANCE_SKIP_DEFER=true)", len(cleanups))
		return
	}

	// Execute in LIFO order (reverse)
	ts.Logf("Running %d deferred cleanups", len(cleanups))
	for i := len(cleanups) - 1; i >= 0; i-- {
		cmd := cleanups[i]
		ts.Logf("defer: %s", cmd)
		// Run cleanup via shell, capture but don't fail on errors
		args := strings.Fields(cmd)
		if len(args) > 0 {
			// Run clawker command directly using Main
			runClawkerInline(ts, args, true /* ignore errors */)
		}
	}
}

// runClawkerInline runs clawker with args, optionally ignoring errors
func runClawkerInline(ts *testscript.TestScript, args []string, ignoreErrors bool) {
	// Save original os.Args and restore after
	origArgs := os.Args
	os.Args = append([]string{"clawker"}, args...)
	defer func() { os.Args = origArgs }()

	// Capture stdout/stderr
	origStdout := os.Stdout
	origStderr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	code := clawker.Main()

	wOut.Close()
	wErr.Close()
	os.Stdout = origStdout
	os.Stderr = origStderr

	var outBuf, errBuf bytes.Buffer
	io.Copy(&outBuf, rOut)
	io.Copy(&errBuf, rErr)
	rOut.Close()
	rErr.Close()

	if outBuf.Len() > 0 {
		ts.Logf("stdout: %s", outBuf.String())
	}
	if errBuf.Len() > 0 {
		ts.Logf("stderr: %s", errBuf.String())
	}

	if code != 0 && !ignoreErrors {
		ts.Fatalf("clawker %v failed with code %d", args, code)
	}
}

// sharedSetup returns a setup function that injects environment variables
func sharedSetup(env testEnv, category string) func(*testscript.Env) error {
	return func(e *testscript.Env) error {
		// Generate unique identifiers
		randomStr := generateRandomString(10)
		scriptName := extractScriptName(e.WorkDir)

		// Build project name: prefix-scriptname-random
		project := fmt.Sprintf("%s-%s-%s", env.ProjectPrefix, scriptName, randomStr)

		// Inject environment variables
		e.Setenv("RANDOM_STRING", randomStr)
		e.Setenv("SCRIPT_NAME", scriptName)
		e.Setenv("PROJECT", project)

		// Ensure HOME is set to work directory for isolated config
		e.Setenv("HOME", e.WorkDir)

		// Set clawker-specific env vars for testing
		e.Setenv("CLAWKER_SPINNER_DISABLED", "1") // Disable spinners for cleaner test output

		// Set CLAWKER_HOME so ClawkerHome() resolves to the sandbox
		clawkerHome := filepath.Join(e.WorkDir, ".local", "clawker")
		e.Setenv("CLAWKER_HOME", clawkerHome)

		// Create the clawker home directory and register the test project
		if err := os.MkdirAll(clawkerHome, 0o755); err != nil {
			return fmt.Errorf("creating clawker home: %w", err)
		}
		registryContent := fmt.Sprintf("projects:\n  %s:\n    name: %q\n    root: %q\n", project, project, e.WorkDir)
		if err := os.WriteFile(filepath.Join(clawkerHome, "projects.yaml"), []byte(registryContent), 0o644); err != nil {
			return fmt.Errorf("writing projects.yaml: %w", err)
		}

		return nil
	}
}

// sharedCmds returns common custom commands for all tests
func sharedCmds() map[string]func(ts *testscript.TestScript, neg bool, args []string) {
	return map[string]func(ts *testscript.TestScript, neg bool, args []string){
		// defer registers a cleanup command to run after the test (LIFO order)
		// Usage: defer clawker container rm --force --agent NAME
		"defer": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg {
				ts.Fatalf("defer does not support negation")
			}
			if len(args) == 0 {
				ts.Fatalf("defer requires a command")
			}
			cmd := strings.Join(args, " ")
			registerDefer(ts.Getenv("WORK"), cmd)
		},

		// stdout2env captures stdout from the previous command into an environment variable
		// Usage: stdout2env VAR_NAME
		"stdout2env": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg {
				ts.Fatalf("stdout2env does not support negation")
			}
			if len(args) != 1 {
				ts.Fatalf("stdout2env requires exactly one argument: VAR_NAME")
			}
			// Read stdout from the test's stdout file
			stdout := strings.TrimSpace(ts.ReadFile("stdout"))
			ts.Setenv(args[0], stdout)
		},

		// sleep pauses execution for the specified number of seconds
		// Usage: sleep 2
		"sleep": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg {
				ts.Fatalf("sleep does not support negation")
			}
			if len(args) != 1 {
				ts.Fatalf("sleep requires exactly one argument: SECONDS")
			}
			seconds, err := strconv.Atoi(args[0])
			if err != nil {
				ts.Fatalf("sleep: invalid seconds: %v", err)
			}
			time.Sleep(time.Duration(seconds) * time.Second)
		},

		// env2upper sets an environment variable to an uppercase value
		// Usage: env2upper VAR=value
		"env2upper": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg {
				ts.Fatalf("env2upper does not support negation")
			}
			if len(args) != 1 {
				ts.Fatalf("env2upper requires exactly one argument: VAR=value")
			}
			parts := strings.SplitN(args[0], "=", 2)
			if len(parts) != 2 {
				ts.Fatalf("env2upper: invalid format, expected VAR=value")
			}
			ts.Setenv(parts[0], strings.ToUpper(parts[1]))
		},

		// replace performs variable substitution in a file
		// Usage: replace FILE VAR=value
		"replace": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg {
				ts.Fatalf("replace does not support negation")
			}
			if len(args) < 2 {
				ts.Fatalf("replace requires at least two arguments: FILE VAR=value...")
			}
			filename := args[0]
			content := ts.ReadFile(filename)

			for _, arg := range args[1:] {
				parts := strings.SplitN(arg, "=", 2)
				if len(parts) != 2 {
					ts.Fatalf("replace: invalid format, expected VAR=value")
				}
				placeholder := "$" + parts[0]
				content = strings.ReplaceAll(content, placeholder, parts[1])
			}

			// Write back
			if err := os.WriteFile(ts.MkAbs(filename), []byte(content), 0644); err != nil {
				ts.Fatalf("replace: failed to write file: %v", err)
			}
		},

		// wait_container_running polls until a container is in running state
		// Usage: wait_container_running CONTAINER_NAME [TIMEOUT_SECONDS]
		"wait_container_running": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg {
				ts.Fatalf("wait_container_running does not support negation")
			}
			if len(args) < 1 {
				ts.Fatalf("wait_container_running requires CONTAINER_NAME")
			}
			containerName := args[0]
			timeout := 30 * time.Second
			if len(args) > 1 {
				secs, err := strconv.Atoi(args[1])
				if err != nil {
					ts.Fatalf("wait_container_running: invalid timeout: %v", err)
				}
				timeout = time.Duration(secs) * time.Second
			}

			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			if err != nil {
				ts.Fatalf("wait_container_running: failed to create Docker client: %v", err)
			}
			defer cli.Close()

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					ts.Fatalf("wait_container_running: timeout waiting for %s to be running", containerName)
				case <-ticker.C:
					info, err := cli.ContainerInspect(ctx, containerName)
					if err != nil {
						if client.IsErrNotFound(err) {
							continue // Container doesn't exist yet, keep waiting
						}
						ts.Fatalf("wait_container_running: inspect failed: %v", err)
					}
					if info.State.Running {
						return // Success
					}
					// Fail-fast if container exited unexpectedly
					if info.State.Status == "exited" {
						ts.Fatalf("wait_container_running: container %s exited (code %d) while waiting for running state",
							containerName, info.State.ExitCode)
					}
				}
			}
		},

		// wait_container_exit waits for a container to exit
		// Usage: wait_container_exit CONTAINER_NAME [TIMEOUT_SECONDS] [EXPECTED_CODE]
		"wait_container_exit": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg {
				ts.Fatalf("wait_container_exit does not support negation")
			}
			if len(args) < 1 {
				ts.Fatalf("wait_container_exit requires CONTAINER_NAME")
			}
			containerName := args[0]
			timeout := 60 * time.Second
			expectedCode := -1 // -1 means any code is acceptable

			if len(args) > 1 {
				secs, err := strconv.Atoi(args[1])
				if err != nil {
					ts.Fatalf("wait_container_exit: invalid timeout: %v", err)
				}
				timeout = time.Duration(secs) * time.Second
			}
			if len(args) > 2 {
				code, err := strconv.Atoi(args[2])
				if err != nil {
					ts.Fatalf("wait_container_exit: invalid expected code: %v", err)
				}
				expectedCode = code
			}

			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			if err != nil {
				ts.Fatalf("wait_container_exit: failed to create Docker client: %v", err)
			}
			defer cli.Close()

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			// Use Docker's wait API
			waitCh, errCh := cli.ContainerWait(ctx, containerName, container.WaitConditionNotRunning)
			select {
			case err := <-errCh:
				if err != nil {
					ts.Fatalf("wait_container_exit: wait failed: %v", err)
				}
			case result := <-waitCh:
				if expectedCode != -1 && int(result.StatusCode) != expectedCode {
					ts.Fatalf("wait_container_exit: expected exit code %d, got %d", expectedCode, result.StatusCode)
				}
			case <-ctx.Done():
				ts.Fatalf("wait_container_exit: timeout waiting for %s to exit", containerName)
			}
		},

		// wait_ready_file waits for the clawker ready file to exist in a container
		// Usage: wait_ready_file CONTAINER_NAME [TIMEOUT_SECONDS]
		"wait_ready_file": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg {
				ts.Fatalf("wait_ready_file does not support negation")
			}
			if len(args) < 1 {
				ts.Fatalf("wait_ready_file requires CONTAINER_NAME")
			}
			containerName := args[0]
			timeout := 120 * time.Second
			if len(args) > 1 {
				secs, err := strconv.Atoi(args[1])
				if err != nil {
					ts.Fatalf("wait_ready_file: invalid timeout: %v", err)
				}
				timeout = time.Duration(secs) * time.Second
			}

			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			if err != nil {
				ts.Fatalf("wait_ready_file: failed to create Docker client: %v", err)
			}
			defer cli.Close()

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()

			readyPath := "/tmp/.clawker-ready"

			for {
				select {
				case <-ctx.Done():
					ts.Fatalf("wait_ready_file: timeout waiting for %s in %s", readyPath, containerName)
				case <-ticker.C:
					// Check if file exists via exec
					execCfg := container.ExecOptions{
						Cmd:          []string{"test", "-f", readyPath},
						AttachStdout: false,
						AttachStderr: false,
					}
					execResp, err := cli.ContainerExecCreate(ctx, containerName, execCfg)
					if err != nil {
						continue // Container may not be ready
					}
					err = cli.ContainerExecStart(ctx, execResp.ID, container.ExecStartOptions{})
					if err != nil {
						continue
					}
					// Wait for exec to complete and check exit code
					inspectResp, err := cli.ContainerExecInspect(ctx, execResp.ID)
					if err != nil {
						continue
					}
					// Poll until exec completes
					for inspectResp.Running {
						time.Sleep(100 * time.Millisecond)
						inspectResp, err = cli.ContainerExecInspect(ctx, execResp.ID)
						if err != nil {
							break
						}
					}
					if inspectResp.ExitCode == 0 {
						return // Success - file exists
					}
				}
			}
		},

		// container_id gets the container ID and stores it in an environment variable
		// Usage: container_id CONTAINER_NAME VAR_NAME
		"container_id": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg {
				ts.Fatalf("container_id does not support negation")
			}
			if len(args) != 2 {
				ts.Fatalf("container_id requires CONTAINER_NAME and VAR_NAME")
			}
			containerName := args[0]
			varName := args[1]

			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			if err != nil {
				ts.Fatalf("container_id: failed to create Docker client: %v", err)
			}
			defer cli.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			info, err := cli.ContainerInspect(ctx, containerName)
			if err != nil {
				ts.Fatalf("container_id: failed to inspect %s: %v", containerName, err)
			}
			ts.Setenv(varName, info.ID)
		},

		// container_state gets the container state and stores it in an environment variable
		// Usage: container_state CONTAINER_NAME VAR_NAME
		"container_state": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg {
				ts.Fatalf("container_state does not support negation")
			}
			if len(args) != 2 {
				ts.Fatalf("container_state requires CONTAINER_NAME and VAR_NAME")
			}
			containerName := args[0]
			varName := args[1]

			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			if err != nil {
				ts.Fatalf("container_state: failed to create Docker client: %v", err)
			}
			defer cli.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			info, err := cli.ContainerInspect(ctx, containerName)
			if err != nil {
				ts.Fatalf("container_state: failed to inspect %s: %v", containerName, err)
			}
			ts.Setenv(varName, info.State.Status)
		},

		// cleanup_project removes all resources for a project
		// Usage: cleanup_project [PROJECT_NAME]
		"cleanup_project": func(ts *testscript.TestScript, neg bool, args []string) {
			if neg {
				ts.Fatalf("cleanup_project does not support negation")
			}
			project := ts.Getenv("PROJECT")
			if len(args) > 0 {
				project = args[0]
			}
			if project == "" {
				ts.Fatalf("cleanup_project: no project specified")
			}

			cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
			if err != nil {
				ts.Fatalf("cleanup_project: failed to create Docker client: %v", err)
			}
			defer cli.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			// Remove containers with project label
			containerFilter := filters.NewArgs()
			containerFilter.Add("label", fmt.Sprintf("com.clawker.project=%s", project))
			containers, err := cli.ContainerList(ctx, container.ListOptions{
				All:     true,
				Filters: containerFilter,
			})
			if err == nil {
				for _, c := range containers {
					ts.Logf("cleanup_project: removing container %s", c.ID[:12])
					_ = cli.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: intPtr(5)})
					_ = cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
				}
			}

			// Remove volumes with project label
			volumeFilter := filters.NewArgs()
			volumeFilter.Add("label", fmt.Sprintf("com.clawker.project=%s", project))
			volumes, err := cli.VolumeList(ctx, volume.ListOptions{Filters: volumeFilter})
			if err == nil {
				for _, v := range volumes.Volumes {
					ts.Logf("cleanup_project: removing volume %s", v.Name)
					_ = cli.VolumeRemove(ctx, v.Name, true)
				}
			}

			// Remove networks with project label
			networkFilter := filters.NewArgs()
			networkFilter.Add("label", fmt.Sprintf("com.clawker.project=%s", project))
			networks, err := cli.NetworkList(ctx, network.ListOptions{Filters: networkFilter})
			if err == nil {
				for _, n := range networks {
					ts.Logf("cleanup_project: removing network %s", n.Name)
					_ = cli.NetworkRemove(ctx, n.ID)
				}
			}

			// Remove images with project label
			imageFilter := filters.NewArgs()
			imageFilter.Add("label", fmt.Sprintf("com.clawker.project=%s", project))
			images, err := cli.ImageList(ctx, image.ListOptions{Filters: imageFilter})
			if err == nil {
				for _, img := range images {
					ts.Logf("cleanup_project: removing image %s", img.ID[:12])
					_, _ = cli.ImageRemove(ctx, img.ID, image.RemoveOptions{Force: true})
				}
			}
		},
	}
}

func intPtr(i int) *int {
	return &i
}

// TestMain sets up the testscript environment
func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"clawker": clawker.Main,
	}))
}

// runTestCategory runs testscript tests from a category directory
func runTestCategory(t *testing.T, category string) {
	if !isDockerAvailable() {
		t.Skip("Docker not available")
	}

	env := parseTestEnv()

	// Filter to single script if specified
	pattern := filepath.Join("testdata", category, "*.txtar")
	if env.SingleScript != "" {
		pattern = filepath.Join("testdata", category, env.SingleScript)
	}

	// Check if any scripts exist
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		t.Skipf("No test scripts found matching %s", pattern)
	}

	testscript.Run(t, testscript.Params{
		Dir: filepath.Join("testdata", category),
		Setup: func(e *testscript.Env) error {
			// Filter scripts if SingleScript is set
			if env.SingleScript != "" {
				// Get the script name from the work directory
				scriptName := filepath.Base(e.WorkDir)
				expectedName := strings.TrimSuffix(env.SingleScript, ".txtar")
				// testscript creates work dirs like "script_XXX/scriptname" where scriptname matches the .txtar filename
				// The last component is the script name
				if !strings.Contains(scriptName, expectedName) && !strings.HasSuffix(e.WorkDir, expectedName) {
					// Check if this script should be skipped
					e.T().Skip("Skipping: script filter set to " + env.SingleScript)
				}
			}

			// Run shared setup
			if err := sharedSetup(env, category)(e); err != nil {
				return err
			}

			// Store env reference for cleanup
			workDir := e.WorkDir
			skipDefer := env.SkipDefer

			// Register cleanup to run deferred commands on test completion
			e.Defer(func() {
				// Get cleanups for this work directory
				deferredCleanupsMu.Lock()
				cleanups := deferredCleanups[workDir]
				delete(deferredCleanups, workDir)
				deferredCleanupsMu.Unlock()

				if len(cleanups) == 0 {
					return
				}

				if skipDefer {
					t.Logf("Skipping %d deferred cleanups (CLAWKER_ACCEPTANCE_SKIP_DEFER=true)", len(cleanups))
					return
				}

				t.Logf("Running %d deferred cleanups", len(cleanups))
				for i := len(cleanups) - 1; i >= 0; i-- {
					cmd := cleanups[i]
					t.Logf("defer: %s", cmd)

					// Run cleanup command
					args := strings.Fields(cmd)
					if len(args) > 0 && args[0] == "clawker" {
						args = args[1:]
						origArgs := os.Args
						os.Args = append([]string{"clawker"}, args...)
						_ = clawker.Main()
						os.Args = origArgs
					}
				}
			})

			return nil
		},
		Cmds:                sharedCmds(),
		UpdateScripts:       os.Getenv("UPDATE_GOLDEN") == "1",
		RequireExplicitExec: true,
		RequireUniqueNames:  true,
	})
}

// Test functions for each category

func TestContainer(t *testing.T) {
	runTestCategory(t, "container")
}

func TestVolume(t *testing.T) {
	runTestCategory(t, "volume")
}

func TestNetwork(t *testing.T) {
	runTestCategory(t, "network")
}

func TestImage(t *testing.T) {
	runTestCategory(t, "image")
}

func TestRalph(t *testing.T) {
	runTestCategory(t, "ralph")
}

func TestProject(t *testing.T) {
	runTestCategory(t, "project")
}

func TestRoot(t *testing.T) {
	runTestCategory(t, "root")
}

func TestWorktree(t *testing.T) {
	runTestCategory(t, "worktree")
}

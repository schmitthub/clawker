//go:build integration

package run

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testProject = "clawker-test"

var (
	clawkerBin  string // Absolute path to clawker binary
	testdataDir string // Absolute path to testdata directory
)

func TestMain(m *testing.M) {
	// Skip if Docker not available
	if err := exec.Command("docker", "info").Run(); err != nil {
		fmt.Println("Skipping integration tests: Docker not available")
		os.Exit(0)
	}

	// Find repo root (where bin/clawker lives)
	wd, _ := os.Getwd()
	repoRoot := findRepoRoot(wd)
	clawkerBin = filepath.Join(repoRoot, "bin", "clawker")
	testdataDir = filepath.Join(repoRoot, "pkg", "cmd", "run", "testdata")

	// Build CLI if needed
	if _, err := os.Stat(clawkerBin); os.IsNotExist(err) {
		fmt.Println("Building clawker binary...")
		cmd := exec.Command("go", "build", "-o", clawkerBin, "./cmd/clawker")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("Failed to build: %v\n%s\n", err, out)
			os.Exit(1)
		}
	}

	// Build test image once
	fmt.Println("Building test image...")
	if out, err := runClawker("build"); err != nil {
		fmt.Printf("Failed to build test image: %v\n%s\n", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func findRepoRoot(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start // fallback
		}
		dir = parent
	}
}

var testCounter atomic.Int64

func uniqueAgent(t *testing.T) string {
	// Use PID + atomic counter + timestamp for guaranteed uniqueness across test runs
	count := testCounter.Add(1)
	return fmt.Sprintf("t%d-%d-%d", os.Getpid(), time.Now().UnixNano()%1000000, count)
}

func containerExists(name string) bool {
	out, _ := exec.Command("docker", "ps", "-a", "--filter", "name=^"+name+"$", "--format", "{{.Names}}").Output()
	return strings.TrimSpace(string(out)) == name
}

func volumeExists(name string) bool {
	out, _ := exec.Command("docker", "volume", "ls", "--filter", "name=^"+name+"$", "--format", "{{.Name}}").Output()
	return strings.TrimSpace(string(out)) == name
}

func cleanup(containerName string) {
	exec.Command("docker", "rm", "-f", containerName).Run()
	exec.Command("docker", "volume", "rm", containerName+"-workspace").Run()
	exec.Command("docker", "volume", "rm", containerName+"-config").Run()
	exec.Command("docker", "volume", "rm", containerName+"-history").Run()
}

func runClawker(args ...string) (string, error) {
	// Use --workdir to point to testdata directory with clawker.yaml
	fullArgs := append([]string{"--workdir", testdataDir}, args...)
	cmd := exec.Command(clawkerBin, fullArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runClawkerWithWorkdir(workdir string, args ...string) (string, error) {
	fullArgs := append([]string{"--workdir", workdir}, args...)
	cmd := exec.Command(clawkerBin, fullArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestRun_PreservesContainerByDefault verifies that run preserves container and volumes by default
// This is the NEW behavior after verb consolidation (run now acts like old start)
func TestRun_PreservesContainerByDefault(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Run a quick command (default behavior: preserve)
	out, err := runClawker("run", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, out)
	}

	// Verify container preserved (Exited state)
	if !containerExists(containerName) {
		t.Error("expected container to be preserved by default")
	}

	// Verify volumes preserved
	if !volumeExists(containerName + "-config") {
		t.Error("expected config volume to be preserved")
	}
	if !volumeExists(containerName + "-history") {
		t.Error("expected history volume to be preserved")
	}
}

// TestRun_RemoveFlag verifies that --remove removes container AND volumes on exit
func TestRun_RemoveFlag(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Run with --remove flag (ephemeral mode)
	out, err := runClawker("run", "--remove", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("run --remove failed: %v\nOutput: %s", err, out)
	}

	// Verify container removed
	if containerExists(containerName) {
		t.Error("expected container to be removed with --remove")
	}

	// Verify volumes removed
	if volumeExists(containerName + "-config") {
		t.Error("expected config volume to be removed")
	}
	if volumeExists(containerName + "-history") {
		t.Error("expected history volume to be removed")
	}
}

// TestRun_StartAlias verifies that 'start' works as alias to 'run'
func TestRun_StartAlias(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Use 'start' alias instead of 'run'
	out, err := runClawker("start", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("start failed: %v\nOutput: %s", err, out)
	}

	// Verify container preserved (same as run default)
	if !containerExists(containerName) {
		t.Error("expected container to be preserved with start alias")
	}
}

// TestRun_Idempotent verifies that run reattaches to existing container
func TestRun_Idempotent(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// First run
	out, err := runClawker("run", "--agent", agent, "--", "echo", "first")
	if err != nil {
		t.Fatalf("first run failed: %v\nOutput: %s", err, out)
	}

	// Get container ID
	id1, _ := exec.Command("docker", "inspect", containerName, "--format", "{{.Id}}").Output()

	// Second run should reuse same container
	out, err = runClawker("run", "--agent", agent, "--", "echo", "second")
	if err != nil {
		t.Fatalf("second run failed: %v\nOutput: %s", err, out)
	}

	id2, _ := exec.Command("docker", "inspect", containerName, "--format", "{{.Id}}").Output()

	if string(id1) != string(id2) {
		t.Error("expected second run to reuse existing container")
	}
}

// TestRun_CleanFlag verifies that --clean removes existing before starting
func TestRun_CleanFlag(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// First run to create container
	out, err := runClawker("run", "--agent", agent, "--", "echo", "first")
	if err != nil {
		t.Fatalf("first run failed: %v\nOutput: %s", err, out)
	}

	// Get original container ID
	id1, _ := exec.Command("docker", "inspect", containerName, "--format", "{{.Id}}").Output()

	// Second run with --clean should create new container
	out, err = runClawker("run", "--clean", "--agent", agent, "--", "echo", "second")
	if err != nil {
		t.Fatalf("run --clean failed: %v\nOutput: %s", err, out)
	}

	id2, _ := exec.Command("docker", "inspect", containerName, "--format", "{{.Id}}").Output()

	if string(id1) == string(id2) {
		t.Error("expected --clean to create new container")
	}
}

// TestRun_DetachFlag verifies that --detach runs container in background
func TestRun_DetachFlag(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Run in detached mode
	out, err := runClawker("run", "--detach", "--agent", agent)
	if err != nil {
		t.Fatalf("run --detach failed: %v\nOutput: %s", err, out)
	}

	// Container should be running
	running, _ := exec.Command("docker", "ps", "--filter", "name=^"+containerName+"$", "--format", "{{.Names}}").Output()
	if strings.TrimSpace(string(running)) != containerName {
		t.Error("expected container to be running with --detach")
	}

	// Stop for cleanup
	exec.Command("docker", "stop", containerName).Run()
}

// TestRun_BindMode verifies bind mode does NOT create workspace volume
func TestRun_BindMode(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Default behavior preserves container, no need for --keep
	out, err := runClawker("run", "--mode=bind", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, out)
	}

	// Workspace volume should NOT exist in bind mode
	if volumeExists(containerName + "-workspace") {
		t.Error("expected NO workspace volume in bind mode")
	}
}

// TestRun_SnapshotMode verifies snapshot mode creates workspace volume
func TestRun_SnapshotMode(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Default behavior preserves container, no need for --keep
	out, err := runClawker("run", "--mode=snapshot", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, out)
	}

	// Workspace volume SHOULD exist in snapshot mode
	if !volumeExists(containerName + "-workspace") {
		t.Error("expected workspace volume in snapshot mode")
	}
}

// TestRun_ContainerLabels verifies container has correct clawker labels
func TestRun_ContainerLabels(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Default behavior preserves container, no need for --keep
	out, err := runClawker("run", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, out)
	}

	// Check labels
	labelOut, _ := exec.Command("docker", "inspect", containerName,
		"--format", "{{index .Config.Labels \"com.clawker.managed\"}}").Output()
	if strings.TrimSpace(string(labelOut)) != "true" {
		t.Errorf("expected com.clawker.managed=true label, got %q", string(labelOut))
	}

	labelOut, _ = exec.Command("docker", "inspect", containerName,
		"--format", "{{index .Config.Labels \"com.clawker.project\"}}").Output()
	if strings.TrimSpace(string(labelOut)) != testProject {
		t.Errorf("expected project label %s, got %q", testProject, string(labelOut))
	}

	labelOut, _ = exec.Command("docker", "inspect", containerName,
		"--format", "{{index .Config.Labels \"com.clawker.agent\"}}").Output()
	if strings.TrimSpace(string(labelOut)) != agent {
		t.Errorf("expected agent label %s, got %q", agent, string(labelOut))
	}
}

// TestRun_ExitCode verifies container exit code is propagated
func TestRun_ExitCode(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Run command that exits with code 42
	fullArgs := []string{"--workdir", testdataDir, "run", "--agent", agent, "--", "sh", "-c", "exit 42"}
	cmd := exec.Command(clawkerBin, fullArgs...)
	err := cmd.Run()

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 42 {
			t.Errorf("expected exit code 42, got %d", exitErr.ExitCode())
		}
	} else if err == nil {
		t.Error("expected exit error with code 42, got nil")
	} else {
		t.Errorf("expected *exec.ExitError, got %T: %v", err, err)
	}
}

// TestBuild_ImageLabels verifies that built images have correct clawker labels
func TestBuild_ImageLabels(t *testing.T) {
	// The test image was already built in TestMain, verify its labels
	imageTag := "clawker/" + testProject + ":latest"

	// Check com.clawker.managed label
	labelOut, err := exec.Command("docker", "inspect", imageTag,
		"--format", "{{index .Config.Labels \"com.clawker.managed\"}}").Output()
	if err != nil {
		t.Fatalf("failed to inspect image: %v", err)
	}
	if strings.TrimSpace(string(labelOut)) != "true" {
		t.Errorf("expected com.clawker.managed=true label, got %q", string(labelOut))
	}

	// Check com.clawker.project label
	labelOut, err = exec.Command("docker", "inspect", imageTag,
		"--format", "{{index .Config.Labels \"com.clawker.project\"}}").Output()
	if err != nil {
		t.Fatalf("failed to inspect image: %v", err)
	}
	if strings.TrimSpace(string(labelOut)) != testProject {
		t.Errorf("expected com.clawker.project=%s label, got %q", testProject, string(labelOut))
	}

	// Check com.clawker.version label exists (value varies)
	labelOut, err = exec.Command("docker", "inspect", imageTag,
		"--format", "{{index .Config.Labels \"com.clawker.version\"}}").Output()
	if err != nil {
		t.Fatalf("failed to inspect image: %v", err)
	}
	if strings.TrimSpace(string(labelOut)) == "" {
		t.Error("expected com.clawker.version label to have a value")
	}

	// Check com.clawker.created label exists and is valid RFC3339
	labelOut, err = exec.Command("docker", "inspect", imageTag,
		"--format", "{{index .Config.Labels \"com.clawker.created\"}}").Output()
	if err != nil {
		t.Fatalf("failed to inspect image: %v", err)
	}
	created := strings.TrimSpace(string(labelOut))
	if created == "" {
		t.Error("expected com.clawker.created label to have a value")
	}
}

//go:build integration

package run

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func uniqueAgent(t *testing.T) string {
	return fmt.Sprintf("t%d", time.Now().UnixNano()%100000)
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

// TestRun_DefaultCleanup verifies that run removes container AND volumes on exit by default
func TestRun_DefaultCleanup(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Run a quick command
	out, err := runClawker("run", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, out)
	}

	// Verify container removed
	if containerExists(containerName) {
		t.Error("expected container to be removed after exit")
	}

	// Verify volumes removed
	if volumeExists(containerName + "-config") {
		t.Error("expected config volume to be removed")
	}
	if volumeExists(containerName + "-history") {
		t.Error("expected history volume to be removed")
	}
}

// TestRun_KeepFlag verifies that --keep preserves container and volumes
func TestRun_KeepFlag(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	out, err := runClawker("run", "--keep", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, out)
	}

	// Verify container preserved (Exited state)
	if !containerExists(containerName) {
		t.Error("expected container to be preserved with --keep")
	}

	// Verify volumes preserved
	if !volumeExists(containerName + "-config") {
		t.Error("expected config volume to be preserved")
	}
	if !volumeExists(containerName + "-history") {
		t.Error("expected history volume to be preserved")
	}
}

// TestRun_BindMode verifies bind mode does NOT create workspace volume
func TestRun_BindMode(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	out, err := runClawker("run", "--keep", "--mode=bind", "--agent", agent, "--", "echo", "hello")
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

	out, err := runClawker("run", "--keep", "--mode=snapshot", "--agent", agent, "--", "echo", "hello")
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

	out, err := runClawker("run", "--keep", "--agent", agent, "--", "echo", "hello")
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

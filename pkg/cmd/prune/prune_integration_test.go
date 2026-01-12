//go:build integration

package prune

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
	clawkerBin  string
	testdataDir string
)

func TestMain(m *testing.M) {
	// Skip if Docker not available
	if err := exec.Command("docker", "info").Run(); err != nil {
		fmt.Println("Skipping integration tests: Docker not available")
		os.Exit(0)
	}

	// Find repo root
	wd, _ := os.Getwd()
	repoRoot := findRepoRoot(wd)
	clawkerBin = filepath.Join(repoRoot, "bin", "clawker")
	testdataDir = filepath.Join(repoRoot, "pkg", "cmd", "prune", "testdata")

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
			return start
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

func containerRunning(name string) bool {
	out, _ := exec.Command("docker", "ps", "--filter", "name=^"+name+"$", "--format", "{{.Names}}").Output()
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
	fullArgs := append([]string{"--workdir", testdataDir}, args...)
	cmd := exec.Command(clawkerBin, fullArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestPrune_RemovesStoppedContainers verifies stopped containers are removed
func TestPrune_RemovesStoppedContainers(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create stopped container
	_, err := runClawker("run", "--keep", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if !containerExists(containerName) {
		t.Fatal("setup failed: container not created")
	}

	// Run prune (default mode - removes stopped containers)
	_, err = runClawker("prune")
	if err != nil {
		t.Fatalf("prune failed: %v", err)
	}

	// Verify container was removed
	if containerExists(containerName) {
		t.Error("expected stopped container to be removed")
	}
}

// TestPrune_SkipsRunningContainers verifies running containers are NOT removed
func TestPrune_SkipsRunningContainers(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Start running container
	_, err := runClawker("start", "--detach", "--agent", agent)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if !containerRunning(containerName) {
		t.Fatal("setup failed: container not running")
	}

	// Run prune
	runClawker("prune")

	// Verify running container was NOT removed
	if !containerRunning(containerName) {
		t.Error("expected running container to be preserved")
	}
}

// TestPrune_DefaultNoVolumes verifies volumes are NOT removed without --all
func TestPrune_DefaultNoVolumes(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create container with volumes, then stop it
	_, err := runClawker("run", "--keep", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	configVolume := containerName + "-config"
	if !volumeExists(configVolume) {
		t.Fatal("setup failed: config volume not created")
	}

	// Run prune (default mode - should NOT remove volumes)
	runClawker("prune")

	// Verify volumes are preserved
	if !volumeExists(configVolume) {
		t.Error("expected volumes to be preserved without --all")
	}
}

// TestPrune_AllRemovesVolumes verifies --all removes volumes
func TestPrune_AllRemovesVolumes(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create container with volumes, then stop it
	_, err := runClawker("run", "--keep", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	configVolume := containerName + "-config"
	historyVolume := containerName + "-history"

	if !volumeExists(configVolume) {
		t.Fatal("setup failed: config volume not created")
	}

	// Run prune --all --force (skip prompt)
	_, err = runClawker("prune", "--all", "--force")
	if err != nil {
		t.Fatalf("prune --all failed: %v", err)
	}

	// Verify volumes were removed
	if volumeExists(configVolume) {
		t.Error("expected config volume to be removed with --all")
	}
	if volumeExists(historyVolume) {
		t.Error("expected history volume to be removed with --all")
	}
}

// TestPrune_AllRemovesContainers verifies --all removes stopped containers
func TestPrune_AllRemovesContainers(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create stopped container
	_, err := runClawker("run", "--keep", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if !containerExists(containerName) {
		t.Fatal("setup failed: container not created")
	}

	// Run prune --all --force
	_, err = runClawker("prune", "--all", "--force")
	if err != nil {
		t.Fatalf("prune --all failed: %v", err)
	}

	// Verify container was removed
	if containerExists(containerName) {
		t.Error("expected container to be removed with --all")
	}
}

// TestPrune_NoResources verifies graceful handling when nothing to prune
func TestPrune_NoResources(t *testing.T) {
	// Run prune with no clawker resources
	out, err := runClawker("prune")
	if err != nil {
		t.Fatalf("prune failed with no resources: %v", err)
	}

	// Should not error, may output "No clawker resources to remove"
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Errorf("unexpected error in output: %s", out)
	}
}

// TestPrune_ForceSkipsPrompt verifies --force skips confirmation
func TestPrune_ForceSkipsPrompt(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create stopped container with volumes
	_, err := runClawker("run", "--keep", "--agent", agent, "--", "echo", "hello")
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Run prune --all --force (should not hang waiting for input)
	done := make(chan bool, 1)
	go func() {
		runClawker("prune", "--all", "--force")
		done <- true
	}()

	select {
	case <-done:
		// Success - completed without waiting for input
	case <-time.After(10 * time.Second):
		t.Fatal("prune --all --force should not wait for input")
	}

	// Verify resources were removed
	if containerExists(containerName) {
		t.Error("expected container to be removed")
	}
}

//go:build integration

package rm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testProject = "claucker-test"

var (
	clauckerBin string
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
	clauckerBin = filepath.Join(repoRoot, "bin", "claucker")
	testdataDir = filepath.Join(repoRoot, "pkg", "cmd", "rm", "testdata")

	// Build CLI if needed
	if _, err := os.Stat(clauckerBin); os.IsNotExist(err) {
		fmt.Println("Building claucker binary...")
		cmd := exec.Command("go", "build", "-o", clauckerBin, "./cmd/claucker")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("Failed to build: %v\n%s\n", err, out)
			os.Exit(1)
		}
	}

	// Build test image once
	fmt.Println("Building test image...")
	if out, err := runClaucker("build"); err != nil {
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

func runClaucker(args ...string) (string, error) {
	fullArgs := append([]string{"--workdir", testdataDir}, args...)
	cmd := exec.Command(clauckerBin, fullArgs...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestRm_ByName verifies removes specific container
func TestRm_ByName(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "claucker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create stopped container
	runClaucker("run", "--keep", "--agent", agent, "--", "echo", "hello")

	// Remove by name
	_, err := runClaucker("rm", "-n", containerName)
	if err != nil {
		t.Fatalf("rm by name failed: %v", err)
	}

	if containerExists(containerName) {
		t.Error("expected container to be removed")
	}
}

// TestRm_ByProject verifies removes all project containers
func TestRm_ByProject(t *testing.T) {
	agent1 := uniqueAgent(t) + "a"
	agent2 := uniqueAgent(t) + "b"
	container1 := "claucker." + testProject + "." + agent1
	container2 := "claucker." + testProject + "." + agent2
	defer cleanup(container1)
	defer cleanup(container2)

	// Create two stopped containers
	runClaucker("run", "--keep", "--agent", agent1, "--", "echo", "hello")
	runClaucker("run", "--keep", "--agent", agent2, "--", "echo", "hello")

	// Remove by project
	_, err := runClaucker("rm", "-p", testProject)
	if err != nil {
		t.Fatalf("rm by project failed: %v", err)
	}

	if containerExists(container1) || containerExists(container2) {
		t.Error("expected all project containers removed")
	}
}

// TestRm_RemovesVolumes verifies associated volumes deleted
func TestRm_RemovesVolumes(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "claucker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create container with volumes
	runClaucker("run", "--keep", "--agent", agent, "--", "echo", "hello")

	// Verify volumes exist
	if !volumeExists(containerName + "-config") {
		t.Fatal("setup failed: config volume not created")
	}

	// Remove
	runClaucker("rm", "-n", containerName)

	// Volumes should be removed
	if volumeExists(containerName + "-config") {
		t.Error("expected volumes to be removed")
	}
}

// TestRm_ForceRunning verifies force removes running container
func TestRm_ForceRunning(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "claucker." + testProject + "." + agent
	defer cleanup(containerName)

	// Start running container
	runClaucker("start", "--detach", "--agent", agent)
	if !containerRunning(containerName) {
		t.Fatal("setup failed: container not running")
	}

	// Force remove
	_, err := runClaucker("rm", "-f", "-n", containerName)
	if err != nil {
		t.Fatalf("rm -f failed: %v", err)
	}

	if containerExists(containerName) {
		t.Error("expected running container force removed")
	}
}

// TestRm_RunningWithoutForce verifies running container is gracefully stopped then removed
// NOTE: Unlike docker rm, claucker rm first tries to stop running containers gracefully.
// The -f flag is only needed if the graceful stop fails.
func TestRm_RunningWithoutForce(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "claucker." + testProject + "." + agent
	defer cleanup(containerName)

	// Start running container
	runClaucker("start", "--detach", "--agent", agent)
	if !containerRunning(containerName) {
		t.Fatal("setup failed: container not running")
	}

	// Remove without force - should succeed (gracefully stops first)
	_, err := runClaucker("rm", "-n", containerName)
	if err != nil {
		t.Fatalf("rm without -f failed: %v", err)
	}

	// Container should be removed
	if containerExists(containerName) {
		t.Error("expected container to be removed after graceful stop")
	}
}

// TestRm_NonExistent verifies graceful error for missing container
func TestRm_NonExistent(t *testing.T) {
	// Remove non-existent container
	_, err := runClaucker("rm", "-n", "claucker.nonexistent.agent")
	// Should return error but not panic
	if err == nil {
		t.Error("expected error when removing non-existent container")
	}
}

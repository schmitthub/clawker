//go:build integration

package remove

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
	testdataDir = filepath.Join(repoRoot, "pkg", "cmd", "remove", "testdata")

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

var testCounter atomic.Int64

func uniqueAgent(t *testing.T) string {
	// Use atomic counter + timestamp for guaranteed uniqueness across parallel tests
	count := testCounter.Add(1)
	return fmt.Sprintf("t%d-%d", time.Now().UnixNano()%100000, count)
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

// TestRm_ByName verifies removes specific container
func TestRm_ByName(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create stopped container (default behavior now preserves)
	runClawker("run", "--agent", agent, "--", "echo", "hello")

	// Remove by name
	_, err := runClawker("rm", "-n", containerName)
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
	container1 := "clawker." + testProject + "." + agent1
	container2 := "clawker." + testProject + "." + agent2
	defer cleanup(container1)
	defer cleanup(container2)

	// Create two stopped containers (default behavior now preserves)
	runClawker("run", "--agent", agent1, "--", "echo", "hello")
	runClawker("run", "--agent", agent2, "--", "echo", "hello")

	// Remove by project
	_, err := runClawker("rm", "-p", testProject)
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
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create container with volumes (default behavior now preserves)
	runClawker("run", "--agent", agent, "--", "echo", "hello")

	// Verify volumes exist
	if !volumeExists(containerName + "-config") {
		t.Fatal("setup failed: config volume not created")
	}

	// Remove
	runClawker("rm", "-n", containerName)

	// Volumes should be removed
	if volumeExists(containerName + "-config") {
		t.Error("expected volumes to be removed")
	}
}

// TestRm_ForceRunning verifies force removes running container
func TestRm_ForceRunning(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Start running container (start is now alias to run)
	runClawker("run", "--detach", "--agent", agent)
	if !containerRunning(containerName) {
		t.Fatal("setup failed: container not running")
	}

	// Force remove
	_, err := runClawker("rm", "-f", "-n", containerName)
	if err != nil {
		t.Fatalf("rm -f failed: %v", err)
	}

	if containerExists(containerName) {
		t.Error("expected running container force removed")
	}
}

// TestRm_RunningWithoutForce verifies running container is gracefully stopped then removed
// NOTE: Unlike docker rm, clawker rm first tries to stop running containers gracefully.
// The -f flag is only needed if the graceful stop fails.
func TestRm_RunningWithoutForce(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Start running container (start is now alias to run)
	runClawker("run", "--detach", "--agent", agent)
	if !containerRunning(containerName) {
		t.Fatal("setup failed: container not running")
	}

	// Remove without force - should succeed (gracefully stops first)
	_, err := runClawker("rm", "-n", containerName)
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
	_, err := runClawker("rm", "-n", "clawker.nonexistent.agent")
	// Should return error but not panic
	if err == nil {
		t.Error("expected error when removing non-existent container")
	}
}

// ============= New --unused flag tests =============

// TestRm_UnusedFlag_RemovesStoppedContainers verifies --unused removes stopped containers
func TestRm_UnusedFlag_RemovesStoppedContainers(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create stopped container
	runClawker("run", "--agent", agent, "--", "echo", "hello")
	if !containerExists(containerName) {
		t.Fatal("setup failed: container not created")
	}

	// Remove with --unused flag
	_, err := runClawker("remove", "--unused", "--force")
	if err != nil {
		t.Fatalf("remove --unused failed: %v", err)
	}

	// Stopped container should be removed
	if containerExists(containerName) {
		t.Error("expected stopped container to be removed with --unused")
	}
}

// TestRm_UnusedFlag_SkipsRunningContainers verifies --unused skips running containers
func TestRm_UnusedFlag_SkipsRunningContainers(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create running container
	runClawker("run", "--detach", "--agent", agent)
	if !containerRunning(containerName) {
		t.Fatal("setup failed: container not running")
	}

	// Remove with --unused flag
	runClawker("remove", "--unused", "--force")

	// Running container should NOT be removed
	if !containerRunning(containerName) {
		t.Error("expected running container to be skipped with --unused")
	}

	// Stop for cleanup
	exec.Command("docker", "stop", containerName).Run()
}

// TestRm_UnusedFlag_WithAll_RemovesVolumes verifies --unused --all removes volumes
func TestRm_UnusedFlag_WithAll_RemovesVolumes(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create container with volumes then stop it
	runClawker("run", "--agent", agent, "--", "echo", "hello")
	if !volumeExists(containerName + "-config") {
		t.Fatal("setup failed: volume not created")
	}

	// Remove with --unused --all --force
	_, err := runClawker("remove", "--unused", "--all", "--force")
	if err != nil {
		t.Fatalf("remove --unused --all failed: %v", err)
	}

	// Container and volumes should be removed
	if containerExists(containerName) {
		t.Error("expected container to be removed with --unused --all")
	}
	if volumeExists(containerName + "-config") {
		t.Error("expected volumes to be removed with --unused --all")
	}
}

// TestRm_UnusedFlag_NoUnused verifies no-op when no unused containers
func TestRm_UnusedFlag_NoUnused(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)
	// Create running container
	runClawker("run", "--detach", "--agent", agent)
	if !containerRunning(containerName) {
		t.Fatal("setup failed: container not running")
	}

	// Remove with --unused flag
	out, err := runClawker("remove", "--unused", "--force")
	if err != nil {
		t.Fatalf("remove --unused failed: %v", err)
	}

	// Output should indicate no unused containers found
	if !strings.Contains(out, "No unused containers found") {
		t.Error("expected message indicating no unused containers found")
	}

	// Cleanup
	exec.Command("docker", "stop", containerName).Run()
}

// TestRm_UnusedFlag_WithAll_RemovesImages verifies --unused --all removes dangling untagged clawker created images
func TestRm_UnusedFlag_WithAll_RemovesImages(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)
	// Create container then stop it
	runClawker("run", "--agent", agent, "--", "echo", "hello")
	if !containerExists(containerName) {
		t.Fatal("setup failed: container not created")
	}

	// Create dangling image by committing container and removing tag
	out, err := exec.Command("docker", "commit", containerName).CombinedOutput()
	if err != nil {
		t.Fatalf("setup failed: docker commit failed: %v\n%s", err, out)
	}
	imageID := strings.TrimSpace(string(out))
	exec.Command("docker", "rmi", imageID).Run() // Remove tag to make dangling

	// Remove with --unused --all --force
	_, err = runClawker("remove", "--unused", "--all", "--force")
	if err != nil {
		t.Fatalf("remove --unused --all failed: %v", err)
	}

	// Dangling image should be removed
	out, _ = exec.Command("docker", "images", "-f", "dangling=true", "-q").Output()
	danglingImages := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, img := range danglingImages {
		if img == imageID {
			t.Error("expected dangling image to be removed with --unused --all")
		}
	}
}

// ============= 'prune' alias tests =============

// TestRm_PruneAlias verifies 'prune' works as alias to 'remove --unused'
func TestRm_PruneAlias(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// Create stopped container
	runClawker("run", "--agent", agent, "--", "echo", "hello")
	if !containerExists(containerName) {
		t.Fatal("setup failed: container not created")
	}

	// Use prune alias (equivalent to remove --unused)
	_, err := runClawker("prune", "--force")
	if err != nil {
		t.Fatalf("prune failed: %v", err)
	}

	// Stopped container should be removed
	if containerExists(containerName) {
		t.Error("expected stopped container to be removed with prune")
	}
}

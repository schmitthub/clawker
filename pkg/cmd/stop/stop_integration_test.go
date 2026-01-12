//go:build integration

package stop

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
	testdataDir = filepath.Join(repoRoot, "pkg", "cmd", "stop", "testdata")

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

func imageExists(name string) bool {
	out, _ := exec.Command("docker", "images", "--filter", "reference="+name, "--format", "{{.Repository}}").Output()
	return strings.TrimSpace(string(out)) != ""
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

// TestStop_StopsContainer verifies container stopped and removed
func TestStop_StopsContainer(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "claucker." + testProject + "." + agent
	defer cleanup(containerName)

	// Start container
	runClaucker("start", "--detach", "--agent", agent)
	if !containerRunning(containerName) {
		t.Fatal("setup failed: container not running")
	}

	// Stop
	_, err := runClaucker("stop", "--agent", agent)
	if err != nil {
		t.Fatalf("stop failed: %v", err)
	}

	// Container should not exist
	if containerExists(containerName) {
		t.Error("expected container to be removed")
	}
}

// TestStop_PreservesVolumes verifies volumes preserved by default
func TestStop_PreservesVolumes(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "claucker." + testProject + "." + agent
	defer cleanup(containerName)

	// Start and stop
	runClaucker("start", "--detach", "--agent", agent)
	runClaucker("stop", "--agent", agent)

	// Volumes should still exist
	if !volumeExists(containerName + "-config") {
		t.Error("expected config volume to be preserved")
	}
	if !volumeExists(containerName + "-history") {
		t.Error("expected history volume to be preserved")
	}
}

// TestStop_CleanFlag verifies --clean removes volumes + image
func TestStop_CleanFlag(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "claucker." + testProject + "." + agent
	defer cleanup(containerName)

	// Start and stop with --clean
	runClaucker("start", "--detach", "--agent", agent)
	_, err := runClaucker("stop", "--clean", "--agent", agent)
	if err != nil {
		t.Fatalf("stop --clean failed: %v", err)
	}

	// Volumes should be removed
	if volumeExists(containerName + "-config") {
		t.Error("expected config volume to be removed with --clean")
	}
	if volumeExists(containerName + "-history") {
		t.Error("expected history volume to be removed with --clean")
	}

	// Image should be removed
	if imageExists("claucker-" + testProject) {
		t.Error("expected image to be removed with --clean")
	}
}

// TestStop_SpecificAgent verifies only stops named agent
func TestStop_SpecificAgent(t *testing.T) {
	agent1 := uniqueAgent(t) + "a"
	agent2 := uniqueAgent(t) + "b"
	container1 := "claucker." + testProject + "." + agent1
	container2 := "claucker." + testProject + "." + agent2
	defer cleanup(container1)
	defer cleanup(container2)

	// Start two containers
	runClaucker("start", "--detach", "--agent", agent1)
	runClaucker("start", "--detach", "--agent", agent2)

	// Stop only agent1
	runClaucker("stop", "--agent", agent1)

	// agent1 stopped, agent2 still running
	if containerExists(container1) {
		t.Error("agent1 should be stopped")
	}
	if !containerRunning(container2) {
		t.Error("agent2 should still be running")
	}
}

// TestStop_ForceFlag verifies force kills container
func TestStop_ForceFlag(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "claucker." + testProject + "." + agent
	defer cleanup(containerName)

	// Start container
	runClaucker("start", "--detach", "--agent", agent)

	// Force stop
	_, err := runClaucker("stop", "--force", "--agent", agent)
	if err != nil {
		t.Fatalf("stop --force failed: %v", err)
	}

	if containerExists(containerName) {
		t.Error("expected container to be force stopped")
	}
}

// TestStop_AlreadyStopped verifies handles stopped container gracefully
func TestStop_AlreadyStopped(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "claucker." + testProject + "." + agent
	defer cleanup(containerName)

	// Start and stop
	runClaucker("start", "--detach", "--agent", agent)
	runClaucker("stop", "--agent", agent)

	// Stop again - should not panic
	_, _ = runClaucker("stop", "--agent", agent)
	// Just verify it doesn't crash
}

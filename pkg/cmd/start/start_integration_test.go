//go:build integration

package start

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
	testdataDir = filepath.Join(repoRoot, "pkg", "cmd", "start", "testdata")

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

func networkExists(name string) bool {
	err := exec.Command("docker", "network", "inspect", name).Run()
	return err == nil
}

func hasPortBinding(containerName, hostPort string) bool {
	out, _ := exec.Command("docker", "inspect", containerName,
		"--format", "{{json .HostConfig.PortBindings}}").Output()
	return strings.Contains(string(out), hostPort)
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

// TestStart_CreatesContainer verifies first start creates container + volumes
func TestStart_CreatesContainer(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	out, err := runClawker("start", "--detach", "--agent", agent)
	if err != nil {
		t.Fatalf("start failed: %v\n%s", err, out)
	}

	if !containerRunning(containerName) {
		t.Error("expected container to be running")
	}

	if !volumeExists(containerName + "-config") {
		t.Error("expected config volume")
	}
	if !volumeExists(containerName + "-history") {
		t.Error("expected history volume")
	}

	if !networkExists("clawker-net") {
		t.Error("expected clawker-net network")
	}
}

// TestStart_Idempotent verifies second start reuses existing container
func TestStart_Idempotent(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// First start
	_, err := runClawker("start", "--detach", "--agent", agent)
	if err != nil {
		t.Fatalf("first start failed: %v", err)
	}

	id1, _ := exec.Command("docker", "inspect", containerName, "--format", "{{.Id}}").Output()

	// Stop container (but don't remove)
	exec.Command("docker", "stop", containerName).Run()

	// Second start should reuse
	_, err = runClawker("start", "--detach", "--agent", agent)
	if err != nil {
		t.Fatalf("second start failed: %v", err)
	}

	id2, _ := exec.Command("docker", "inspect", containerName, "--format", "{{.Id}}").Output()
	if string(id1) != string(id2) {
		t.Error("expected start to reuse existing container")
	}
}

// TestStart_CleanFlag verifies --clean removes existing before start
func TestStart_CleanFlag(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	// First start
	runClawker("start", "--detach", "--agent", agent)
	id1, _ := exec.Command("docker", "inspect", containerName, "--format", "{{.Id}}").Output()

	// Stop
	exec.Command("docker", "stop", containerName).Run()

	// Start with --clean
	_, err := runClawker("start", "--detach", "--clean", "--agent", agent)
	if err != nil {
		t.Fatalf("start --clean failed: %v", err)
	}

	id2, _ := exec.Command("docker", "inspect", containerName, "--format", "{{.Id}}").Output()
	if string(id1) == string(id2) {
		t.Error("expected --clean to create new container")
	}
}

// TestStart_BindMode verifies bind mode creates no workspace volume
func TestStart_BindMode(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	out, err := runClawker("start", "--detach", "--mode=bind", "--agent", agent)
	if err != nil {
		t.Fatalf("start --mode=bind failed: %v\n%s", err, out)
	}

	if volumeExists(containerName + "-workspace") {
		t.Error("expected NO workspace volume in bind mode")
	}
}

// TestStart_SnapshotMode verifies snapshot mode creates workspace volume
func TestStart_SnapshotMode(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	out, err := runClawker("start", "--detach", "--mode=snapshot", "--agent", agent)
	if err != nil {
		t.Fatalf("start --mode=snapshot failed: %v\n%s", err, out)
	}

	if !volumeExists(containerName + "-workspace") {
		t.Error("expected workspace volume in snapshot mode")
	}
}

// TestStart_ContainerLabels verifies correct clawker labels
func TestStart_ContainerLabels(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	out, err := runClawker("start", "--detach", "--agent", agent)
	if err != nil {
		t.Fatalf("start failed: %v\n%s", err, out)
	}

	labelOut, _ := exec.Command("docker", "inspect", containerName,
		"--format", "{{index .Config.Labels \"com.clawker.managed\"}}").Output()
	if strings.TrimSpace(string(labelOut)) != "true" {
		t.Errorf("expected com.clawker.managed=true, got %q", string(labelOut))
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

// TestStart_DetachFlag verifies container runs in background
func TestStart_DetachFlag(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	_, err := runClawker("start", "--detach", "--agent", agent)
	if err != nil {
		t.Fatalf("start --detach failed: %v", err)
	}

	if !containerRunning(containerName) {
		t.Error("expected container running in background")
	}
}

// TestStart_PortPublish verifies port binding created
func TestStart_PortPublish(t *testing.T) {
	agent := uniqueAgent(t)
	containerName := "clawker." + testProject + "." + agent
	defer cleanup(containerName)

	_, err := runClawker("start", "--detach", "--agent", agent, "-p", "18080:8080")
	if err != nil {
		t.Fatalf("start with port failed: %v", err)
	}

	if !hasPortBinding(containerName, "18080") {
		t.Error("expected port 18080 binding")
	}
}

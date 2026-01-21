package lifecycle

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// buildClawkerBinary builds the clawker binary for CLI tests.
// Returns the path to the built binary.
func buildClawkerBinary(t *testing.T) string {
	t.Helper()

	projectRoot := ProjectRoot(t)
	binaryPath := filepath.Join(projectRoot, "bin", "clawker-test")

	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/clawker")
	cmd.Dir = projectRoot
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "failed to build clawker binary: %s", string(output))

	t.Cleanup(func() {
		os.Remove(binaryPath)
	})

	return binaryPath
}

// runClawkerCommand executes the clawker binary with the given arguments.
// Returns combined output and any error.
func runClawkerCommand(t *testing.T, binaryPath, workDir string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "CLAWKER_INTEGRATION_TESTS=1")

	output, err := cmd.CombinedOutput()
	return string(output), err
}

// ContainerStateRunning is a helper constant for container state checks.
const ContainerStateRunning = "running"

// ContainerStateStopped is a helper constant for container state checks.
const ContainerStateStopped = "exited"

// ContainerStateCreated is a helper constant for container state checks.
const ContainerStateCreated = "created"

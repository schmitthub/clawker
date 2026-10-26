package e2e_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

// newSandboxHarness wires the real Config/Docker/ControlPlane stack, writes
// projectYAML as the project's config, then registers and builds the
// project.
func newSandboxHarness(t *testing.T, projectYAML string) *harness.Harness {
	t.Helper()
	h := harness.New(t, func(o *harness.FactoryOptions) {
		o.Config = config.NewConfig
		o.Client = docker.NewClient
		o.ProjectManager = project.NewProjectManager
		o.UseRealControlPlane = true
		o.UseRealAdminClient = true
	})
	setup := h.NewIsolatedFS(nil)
	// The test CP must use this test environment's CA.
	harness.EnsureNoControlPlane(t, 30*time.Second)
	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, projectYAML)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed\nstdout: %s\nstderr: %s",
		regRes.Stdout, regRes.Stderr)
	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)
	return h
}

// TestSandbox_NoNewPrivileges_E2E pins the default privilege posture of the
// agent process: the kernel no-new-privileges flag is set, so setuid
// binaries in the image cannot raise the agent's privileges.
func TestSandbox_NoNewPrivileges_E2E(t *testing.T) {
	h := newSandboxHarness(t, "\n")

	res := h.RunInContainer("privs-default", "grep", "NoNewPrivs", "/proc/self/status")
	require.NoError(t, res.Err, "probe failed\nstdout: %s\nstderr: %s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "NoNewPrivs:\t1", "agent process must run with no-new-privileges")
}

// TestSandbox_DockerSocketGroup_E2E pins the security.docker_socket path:
// the boot step gives the mounted socket to the docker group, and the
// agent process is a member of that group. The image adds the user to the
// group at build time because clawkerd resolves the user's groups once, at
// startup, before any boot step runs.
func TestSandbox_DockerSocketGroup_E2E(t *testing.T) {
	h := newSandboxHarness(t, `
security:
  docker_socket: true
`)

	res := h.RunInContainer("privs-socket", "sh", "-c",
		"id -nG; stat -c 'sock=%G' /var/run/docker.sock")
	require.NoError(t, res.Err, "probe failed\nstdout: %s\nstderr: %s", res.Stdout, res.Stderr)
	assert.Contains(t, strings.Fields(res.Stdout), "docker",
		"agent process must be in the docker group when the socket is mounted")
	assert.Contains(t, res.Stdout, "sock=docker", "socket must belong to the docker group")
}

package e2e

// E2E coverage for the clawkerd registration handshake. Authored
// alongside Branch 4 and deferred to the host-side review pass per
// initiative E2E policy — agents do not run E2E tests.
//
// IMPORTANT: these tests assume the CLI's container-start path is wired
// to:
//   1. Generate a fresh per-agent bootstrap (cert + key + CA + assertion + verifier).
//   2. Call AdminService.AnnounceAgent before docker start.
//   3. docker cp the bootstrap material to the container's /run/clawker/bootstrap.
//   4. Set CLAWKER_CP_HYDRA_URL / CLAWKER_CP_AGENT_ADDR / CLAWKER_AGENT_NAME env.
//
// Until those wires land in run/start (currently a known gap — see the
// Branch 4 plan memory), `clawker container run` produces a container
// without /run/clawker/bootstrap, the entrypoint's gate skips clawkerd
// launch, and these tests time out at the ListAgents poll. The user is
// expected to run them on the host after Task 7's CLI wiring lands.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adminv1 "github.com/schmitthub/clawker/api/admin/v1"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

// newAgentRegistrationHarness mirrors newFirewallHarness — production
// Config, real docker client, real ProjectManager, real CP. Required
// services include the CP because every Register call goes through
// the CP's agent listener.
func newAgentRegistrationHarness(t *testing.T) *harness.Harness {
	t.Helper()
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	t.Cleanup(func() { h.RequireServicesWereRunning(t, "controlplane") })
	setup := h.NewIsolatedFS(nil)

	setup.WriteYAML(t, testenv.ProjectConfig, setup.ProjectDir, `
build:
  image: "buildpack-deps:bookworm-scm"
agent:
  claude_code:
    use_host_auth: false
security:
  firewall:
    enable: false
`)

	regRes := h.Run("project", "register", "testproject")
	require.NoError(t, regRes.Err, "register failed: %s", regRes.Stderr)

	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed: %s", buildRes.Stderr)

	return h
}

// waitForAgent polls AdminService.ListAgents until an agent matching
// name appears (or the deadline elapses). Returns the matching Agent
// or fails the test.
func waitForAgent(t *testing.T, h *harness.Harness, name string, deadline time.Duration) *adminv1.Agent {
	t.Helper()
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		res := h.Run("controlplane", "agents", "--json")
		if res.Err == nil && strings.Contains(res.Stdout, name) {
			// Round-trip via ListAgents through the AdminClient so the
			// returned Agent struct matches what production tooling
			// would observe.
			f := res.Factory
			adminClient, err := f.AdminClient(context.Background())
			require.NoError(t, err)
			lr, err := adminClient.ListAgents(context.Background(), &adminv1.ListAgentsRequest{})
			require.NoError(t, err)
			for _, a := range lr.Agents {
				if a.AgentName == name {
					return a
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("agent %q never appeared in ListAgents within %s", name, deadline)
	return nil
}

// waitForEviction is the inverse: polls until the named agent is
// absent. Used after container stop to assert dockerevents drove the
// agentregistry.EvictByContainerID path.
func waitForEviction(t *testing.T, h *harness.Harness, name string, deadline time.Duration) {
	t.Helper()
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		res := h.Run("controlplane", "agents", "--json")
		if res.Err == nil && !strings.Contains(res.Stdout, name) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("agent %q never evicted within %s", name, deadline)
}

// TestClawkerdRegister_HappyPath drives the announce → register →
// idle → stop → evict lifecycle through the CLI. Exercises every layer
// the build wires together: bundler-emitted clawkerd in the image,
// CLI-side bootstrap material delivery, AnnounceAgent slot reservation,
// AgentService.Register cross-checks, agentregistry insertion,
// dockerevents-driven eviction.
func TestClawkerdRegister_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E clawkerd registration test")
	}
	h := newAgentRegistrationHarness(t)

	const agentName = "clawker.testproject.happy"

	// Start the agent container detached so the test can poll registry
	// state while the daemon idles inside.
	runRes := h.Run("container", "run", "--detach", "--agent", "happy", "@", "sleep", "60")
	require.NoError(t, runRes.Err, "container run failed: %s", runRes.Stderr)

	agent := waitForAgent(t, h, agentName, 30*time.Second)

	// Sanity-check the registry entry's shape: thumbprint must be 64
	// lowercase-hex chars (SHA-256 over the agent's mTLS leaf), and
	// the container_id must be a non-empty Docker container ID.
	require.NotEmpty(t, agent.ContainerId)
	require.Len(t, agent.CertThumbprint, 64)
	for _, r := range agent.CertThumbprint {
		require.True(t, (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'),
			"cert_thumbprint must be lowercase hex: %q", agent.CertThumbprint)
	}
	require.True(t, agent.RegisteredAtUnix > 0)

	stopRes := h.Run("container", "stop", "--agent", "happy")
	require.NoError(t, stopRes.Err, "container stop failed: %s", stopRes.Stderr)

	waitForEviction(t, h, agentName, 30*time.Second)

	// Container removal cleans up labels — final ListAgents should be
	// empty for this name.
	rmRes := h.Run("container", "remove", "--agent", "happy")
	assert.NoError(t, rmRes.Err)
}

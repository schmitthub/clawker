package e2e

// E2E coverage for the clawkerd registration handshake. Per initiative
// E2E policy, agents do not run these tests — operator runs them on
// the host (`go test ./test/e2e/...` or `make test-e2e`).
//
// The CLI's container-start path drives the full registration flow:
//   1. Generate a fresh per-agent bootstrap (cert + key + CA + assertion + verifier).
//   2. Call AdminService.AnnounceAgent before docker start.
//   3. CopyToContainer the bootstrap material to /run/clawker/bootstrap.
//   4. Set CLAWKER_CP_HYDRA_URL / CLAWKER_CP_AGENT_ADDR / CLAWKER_AGENT env.
//
// All four steps are wired in `internal/cmd/container/shared` (run/start
// invoke `GenerateAgentBootstrap` + `AnnounceAgent` + `WriteAgentBootstrapToContainer`
// before the docker start; entrypoint backgrounds clawkerd whenever
// /run/clawker/bootstrap exists). The tests below assert the full
// announce → Connect → idle → stop → evict lifecycle end to end.

import (
	"context"
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
`)
	// Master firewall switch lives in settings.yaml, NOT
	// clawker.yaml's `security.firewall` (which only holds per-project
	// add_domains/rules; FirewallConfig vs FirewallSettings split). The
	// project's `security.firewall.enable` field is silently dropped at
	// load time. Mirrors the canonical pattern in firewall_test.go.
	setup.WriteYAML(t, testenv.Settings, setup.Dirs.Config, `
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
// the composite (project, name) identity appears (or the deadline
// elapses). Match is on the SHORT form — registry.Entry.AgentName is
// stored as the user-typed short name (e.g. "happy"), and the
// canonical form clawker.<project>.<agent> is composed only on the
// peer cert CN at mint time and never appears on the wire. Project
// pairs with name to form the composite identity the CP keys agents
// by, so two projects with the same short agent name register
// disjoint entries.
func waitForAgent(t *testing.T, h *harness.Harness, name, project string, deadline time.Duration) *adminv1.Agent {
	t.Helper()
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		res := h.Run("controlplane", "agents", "--json")
		if res.Err == nil {
			f := res.Factory
			adminClient, err := f.AdminClient(context.Background())
			require.NoError(t, err)
			lr, err := adminClient.ListAgents(context.Background(), &adminv1.ListAgentsRequest{})
			require.NoError(t, err)
			for _, a := range lr.Agents {
				if a.AgentName == name && a.Project == project {
					return a
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("agent %q (project %q) never appeared in ListAgents within %s", name, project, deadline)
	return nil
}

// waitForEviction is the inverse: polls until the composite (project,
// name) entry is absent from ListAgents. Used after container stop to
// assert dockerevents drove the agentregistry.EvictByContainerID path.
func waitForEviction(t *testing.T, h *harness.Harness, name, project string, deadline time.Duration) {
	t.Helper()
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		res := h.Run("controlplane", "agents", "--json")
		if res.Err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		f := res.Factory
		adminClient, err := f.AdminClient(context.Background())
		require.NoError(t, err)
		lr, err := adminClient.ListAgents(context.Background(), &adminv1.ListAgentsRequest{})
		require.NoError(t, err)
		found := false
		for _, a := range lr.Agents {
			if a.AgentName == name && a.Project == project {
				found = true
				break
			}
		}
		if !found {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("agent %q (project %q) never evicted within %s", name, project, deadline)
}

// TestClawkerdRegister_HappyPath drives the announce → register →
// idle → stop → evict lifecycle through the CLI. Exercises every layer
// the build wires together: bundler-emitted clawkerd in the image,
// CLI-side bootstrap material delivery, AnnounceAgent slot reservation,
// AgentService.Connect cross-checks, agentregistry insertion,
// dockerevents-driven eviction.
func TestClawkerdRegister_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E clawkerd registration test")
	}
	h := newAgentRegistrationHarness(t)

	// agentName is the SHORT form the CLI passes via --agent and the
	// CP stores in registry.Entry.AgentName. The canonical form
	// "clawker.testproject.happy" is bound to the per-agent cert CN
	// at mint time and never appears on the wire.
	const agentName = "happy"
	const projectName = "testproject"

	// Start the agent container detached so the test can poll registry
	// state while the daemon idles inside.
	runRes := h.Run("container", "run", "--detach", "--agent", agentName, "@", "sleep", "60")
	require.NoError(t, runRes.Err, "container run failed: %s", runRes.Stderr)

	agent := waitForAgent(t, h, agentName, projectName, 30*time.Second)

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

	stopRes := h.Run("container", "stop", "--agent", agentName)
	require.NoError(t, stopRes.Err, "container stop failed: %s", stopRes.Stderr)

	waitForEviction(t, h, agentName, projectName, 30*time.Second)

	// Container removal cleans up labels — final ListAgents should be
	// empty for this name.
	rmRes := h.Run("container", "remove", "--agent", agentName)
	assert.NoError(t, rmRes.Err)
}

package commands

import (
	"context"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/cmd/container/create"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/test/harness"
	"github.com/schmitthub/clawker/test/harness/builders"
	"github.com/stretchr/testify/require"
)

// _blankCfg provides label constants via the config interface, shared across the package.
var _blankCfg = configmocks.NewBlankConfig()

// TestContainerCreate_AgentNameApplied tests that the --agent flag value is
// properly applied to the container name and labels.
//
// This test catches a bug where opts.AgentName gets overwritten with the empty
// opts.Agent field, causing containers to get random names instead of the
// specified agent name.
func TestContainerCreate_AgentNameApplied(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	// Create harness with minimal config (no firewall, no host proxy)
	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("create-agent-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)

	// Change to project directory so config can be found
	h.Chdir()

	// Create Docker client for verification and cleanup
	client := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), client, "create-agent-test"); err != nil {
			t.Logf("WARNING: cleanup failed for create-agent-test: %v", err)
		}
	}()

	// Generate unique agent name for this test
	agentName := "test-agent-" + time.Now().Format("150405.000000")
	expectedContainerName := "clawker.create-agent-test." + agentName

	// Create factory — uses h.Config so the client carries the correct project
	f, ios := harness.NewTestFactory(t, h)

	// Create and execute the create command with --agent flag
	cmd := create.NewCmdCreate(f, nil)
	cmd.SetArgs([]string{
		"--agent", agentName,
		"alpine:latest",
	})

	err := cmd.Execute()
	require.NoError(t, err, "create command failed: stderr=%s", ios.ErrBuf.String())

	// Verify container exists with correct name
	containers, err := client.ListContainersByProject(ctx, "create-agent-test", true)
	require.NoError(t, err, "failed to list containers")
	require.Len(t, containers, 1, "expected exactly one container")

	container := containers[0]

	// THIS ASSERTION WILL FAIL - catching the bug where --agent is ignored
	// The bug causes containers to get random names instead of the specified agent
	require.Equal(t, expectedContainerName, container.Name,
		"container name should be clawker.<project>.<agent>, but got %s (--agent flag was likely ignored)", container.Name)

	// Also verify the agent label matches
	require.Equal(t, agentName, container.Agent,
		"agent label should match the --agent flag value")

	// Verify other clawker labels are present and correct
	require.Equal(t, "create-agent-test", container.Project, "project label mismatch")

	// Use ContainerInspect to verify the full label set
	info, err := client.ContainerInspect(ctx, container.ID, docker.ContainerInspectOptions{})
	require.NoError(t, err, "failed to inspect container")

	labels := info.Container.Config.Labels
	require.Equal(t, "true", labels[_blankCfg.LabelManaged()], "managed label missing")
	require.Equal(t, "create-agent-test", labels[_blankCfg.LabelProject()], "project label in inspect mismatch")
	require.Equal(t, agentName, labels[_blankCfg.LabelAgent()], "agent label in inspect mismatch")
}

// TestContainerCreate_NameFlagApplied tests that the --name flag (alias for --agent)
// is also properly applied to the container name and labels.
func TestContainerCreate_NameFlagApplied(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("create-name-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)

	h.Chdir()

	client := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), client, "create-name-test"); err != nil {
			t.Logf("WARNING: cleanup failed for create-name-test: %v", err)
		}
	}()

	// Use --name flag (should work the same as --agent)
	agentName := "test-name-" + time.Now().Format("150405.000000")
	expectedContainerName := "clawker.create-name-test." + agentName

	f, ios := harness.NewTestFactory(t, h)

	cmd := create.NewCmdCreate(f, nil)
	cmd.SetArgs([]string{
		"--name", agentName,
		"alpine:latest",
	})

	err := cmd.Execute()
	require.NoError(t, err, "create command failed: stderr=%s", ios.ErrBuf.String())

	containers, err := client.ListContainersByProject(ctx, "create-name-test", true)
	require.NoError(t, err, "failed to list containers")
	require.Len(t, containers, 1, "expected exactly one container")

	container := containers[0]

	// Verify container name follows the pattern
	require.Equal(t, expectedContainerName, container.Name,
		"container name should be clawker.<project>.<agent> when using --name flag")
	require.Equal(t, agentName, container.Agent, "agent label should match the --name flag value")
}

// TestContainerCreate_NoAgentGetsRandomName tests that when no --agent flag is
// provided, the container gets a randomly generated name.
func TestContainerCreate_NoAgentGetsRandomName(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	h := harness.NewHarness(t,
		harness.WithConfigBuilder(
			builders.MinimalValidConfig().
				WithProject("create-random-test").
				WithSecurity(builders.SecurityFirewallDisabled()),
		),
	)

	h.Chdir()

	client := harness.NewTestClient(t)
	defer func() {
		if err := harness.CleanupProjectResources(context.Background(), client, "create-random-test"); err != nil {
			t.Logf("WARNING: cleanup failed for create-random-test: %v", err)
		}
	}()

	f, ios := harness.NewTestFactory(t, h)

	// Create without --agent flag
	cmd := create.NewCmdCreate(f, nil)
	cmd.SetArgs([]string{
		"alpine:latest",
	})

	err := cmd.Execute()
	require.NoError(t, err, "create command failed: stderr=%s", ios.ErrBuf.String())

	containers, err := client.ListContainersByProject(ctx, "create-random-test", true)
	require.NoError(t, err, "failed to list containers")
	require.Len(t, containers, 1, "expected exactly one container")

	container := containers[0]

	// Verify container name starts with clawker.project.
	require.Contains(t, container.Name, "clawker.create-random-test.",
		"container name should start with clawker.<project>.")

	// Verify agent name was generated (not empty)
	require.NotEmpty(t, container.Agent, "agent name should be generated when --agent not provided")

	// Verify the name follows the pattern clawker.project.agent
	expectedPrefix := "clawker.create-random-test." + container.Agent
	require.Equal(t, expectedPrefix, container.Name,
		"container name should match clawker.<project>.<generated-agent>")
}

// TestContainerCreate_InvalidAgentName tests that invalid agent names are rejected
// before any Docker resources are created. This exercises the full command pipeline:
// CLI flags → Cobra → shared.CreateContainer → docker.ContainerName → ValidateResourceName.
func TestContainerCreate_InvalidAgentName(t *testing.T) {
	harness.RequireDocker(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		agent     string
		flag      string // "--agent" or "--name"
		errSubstr string
	}{
		{"hyphen_start", "--rm", "--agent", "cannot start with a hyphen"},
		{"dot_start", ".hidden", "--agent", "only [a-zA-Z0-9]"},
		{"contains_space", "my agent", "--agent", "only [a-zA-Z0-9]"},
		{"contains_at", "my@agent", "--agent", "only [a-zA-Z0-9]"},
		{"contains_slash", "my/agent", "--agent", "only [a-zA-Z0-9]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := harness.NewHarness(t,
				harness.WithConfigBuilder(
					builders.MinimalValidConfig().
						WithProject("create-invalid-test").
						WithSecurity(builders.SecurityFirewallDisabled()),
				),
			)
			h.Chdir()

			client := harness.NewTestClient(t)
			defer func() {
				_ = harness.CleanupProjectResources(context.Background(), client, "create-invalid-test")
			}()

			f, _ := harness.NewTestFactory(t, h)
			cmd := create.NewCmdCreate(f, nil)
			cmd.SetArgs([]string{tt.flag, tt.agent, "alpine:latest"})

			err := cmd.Execute()
			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid agent name")
			require.Contains(t, err.Error(), tt.errSubstr)

			// Verify no containers were created — validation should reject before any Docker ops.
			containers, listErr := client.ListContainersByProject(ctx, "create-invalid-test", true)
			require.NoError(t, listErr)
			require.Empty(t, containers, "no containers should exist for invalid name %q", tt.agent)
		})
	}
}

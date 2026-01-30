//go:build integration

package create

import (
	"context"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestCreateIntegration_AgentNameApplied tests that the --agent flag value is
// properly applied to the container name and labels.
//
// This test catches a bug where opts.AgentName gets overwritten with the empty
// opts.Agent field, causing containers to get random names instead of the
// specified agent name.
func TestCreateIntegration_AgentNameApplied(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	// Create harness with minimal config (no firewall, no host proxy)
	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("create-agent-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)

	// Change to project directory so config can be found
	h.Chdir()

	// Create Docker client for verification and cleanup
	client := testutil.NewTestClient(t)
	defer testutil.CleanupProjectResources(ctx, client, "create-agent-test")

	// Generate unique agent name for this test
	agentName := "test-agent-" + time.Now().Format("150405.000000")
	expectedContainerName := "clawker.create-agent-test." + agentName

	// Create factory pointing to harness project directory
	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	// Create and execute the create command with --agent flag
	cmd := NewCmdCreate(f, nil)
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
	require.Equal(t, "true", labels["com.clawker.managed"], "managed label missing")
	require.Equal(t, "create-agent-test", labels["com.clawker.project"], "project label in inspect mismatch")
	require.Equal(t, agentName, labels["com.clawker.agent"], "agent label in inspect mismatch")
}

// TestCreateIntegration_NameFlagApplied tests that the --name flag (alias for --agent)
// is also properly applied to the container name and labels.
func TestCreateIntegration_NameFlagApplied(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("create-name-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)

	h.Chdir()

	client := testutil.NewTestClient(t)
	defer testutil.CleanupProjectResources(ctx, client, "create-name-test")

	// Use --name flag (should work the same as --agent)
	agentName := "test-name-" + time.Now().Format("150405.000000")
	expectedContainerName := "clawker.create-name-test." + agentName

	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	cmd := NewCmdCreate(f, nil)
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

// TestCreateIntegration_NoAgentGetsRandomName tests that when no --agent flag is
// provided, the container gets a randomly generated name.
func TestCreateIntegration_NoAgentGetsRandomName(t *testing.T) {
	testutil.RequireDocker(t)
	ctx := context.Background()

	h := testutil.NewHarness(t,
		testutil.WithConfigBuilder(
			testutil.MinimalValidConfig().
				WithProject("create-random-test").
				WithSecurity(testutil.SecurityFirewallDisabled()),
		),
	)

	h.Chdir()

	client := testutil.NewTestClient(t)
	defer testutil.CleanupProjectResources(ctx, client, "create-random-test")

	ios := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		WorkDir:   h.ProjectDir,
		IOStreams: ios.IOStreams,
	}

	// Create without --agent flag
	cmd := NewCmdCreate(f, nil)
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

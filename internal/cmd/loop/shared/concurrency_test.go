package shared

import (
	"context"
	"fmt"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// _testCfg provides label accessors for test fixtures without hardcoding strings.
var _testCfg = configmocks.NewBlankConfig()

func TestCheckConcurrency_NoRunningContainers(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList() // empty list

	tio := iostreamstest.New()
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio.IOStreams,
	})
	require.NoError(t, err)
	assert.Equal(t, ActionProceed, action)
}

func TestCheckConcurrency_DifferentWorkDir(t *testing.T) {
	ctr := dockertest.RunningContainerFixture("myproj", "loop-agent")
	ctr.Labels[_testCfg.LabelWorkdir()] = "/other/dir"
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr)

	tio := iostreamstest.New()
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio.IOStreams,
	})
	require.NoError(t, err)
	assert.Equal(t, ActionProceed, action)
}

func TestCheckConcurrency_SameWorkDir_NonInteractive(t *testing.T) {
	ctr := dockertest.RunningContainerFixture("myproj", "loop-agent")
	ctr.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr)

	tio := iostreamstest.New()
	// Non-interactive: no Prompter set
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio.IOStreams,
	})
	require.NoError(t, err)
	assert.Equal(t, ActionProceed, action)
	assert.Contains(t, tio.ErrBuf.String(), "already running")
}

func testPrompterWithSelection(tio *iostreamstest.TestIOStreams, selection int) func() *prompter.Prompter {
	tio.SetInteractive(true)
	tio.InBuf.SetInput(selectionInput(selection))
	return func() *prompter.Prompter {
		return prompter.NewPrompter(tio.IOStreams)
	}
}

// selectionInput converts a 0-based selection index to the 1-based input string.
func selectionInput(idx int) string {
	return fmt.Sprintf("%d\n", idx+1)
}

func TestCheckConcurrency_SameWorkDir_Interactive_Worktree(t *testing.T) {
	ctr := dockertest.RunningContainerFixture("myproj", "loop-agent")
	ctr.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr)

	tio := iostreamstest.New()
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio.IOStreams,
		Prompter:  testPrompterWithSelection(tio, 0), // "Use a worktree"
	})
	require.NoError(t, err)
	assert.Equal(t, ActionWorktree, action)
}

func TestCheckConcurrency_SameWorkDir_Interactive_Proceed(t *testing.T) {
	ctr := dockertest.RunningContainerFixture("myproj", "loop-agent")
	ctr.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr)

	tio := iostreamstest.New()
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio.IOStreams,
		Prompter:  testPrompterWithSelection(tio, 1), // "Proceed anyway"
	})
	require.NoError(t, err)
	assert.Equal(t, ActionProceed, action)
}

func TestCheckConcurrency_SameWorkDir_Interactive_Abort(t *testing.T) {
	ctr := dockertest.RunningContainerFixture("myproj", "loop-agent")
	ctr.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr)

	tio := iostreamstest.New()
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio.IOStreams,
		Prompter:  testPrompterWithSelection(tio, 2), // "Abort"
	})
	require.NoError(t, err)
	assert.Equal(t, ActionAbort, action)
}

func TestCheckConcurrency_DockerListError(t *testing.T) {
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerListError(fmt.Errorf("docker daemon unavailable"))

	tio := iostreamstest.New()
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio.IOStreams,
	})
	require.Error(t, err)
	assert.Equal(t, ActionProceed, action)
	assert.Contains(t, err.Error(), "checking for concurrent sessions")
}

func TestCheckConcurrency_MultipleRunning_WarnsAndProceeds(t *testing.T) {
	ctr1 := dockertest.RunningContainerFixture("myproj", "loop-agent1")
	ctr1.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	ctr2 := dockertest.RunningContainerFixture("myproj", "loop-agent2")
	ctr2.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	fake := dockertest.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr1, ctr2)

	tio := iostreamstest.New()
	// Non-interactive should still warn and proceed
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio.IOStreams,
	})
	require.NoError(t, err)
	assert.Equal(t, ActionProceed, action)
	assert.Contains(t, tio.ErrBuf.String(), "already running")
}

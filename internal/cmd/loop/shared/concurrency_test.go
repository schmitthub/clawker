package shared

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker/mock"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/prompter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// _testCfg provides label accessors for test fixtures without hardcoding strings.
var _testCfg = configmocks.NewBlankConfig()

func TestCheckConcurrency_NoRunningContainers(t *testing.T) {
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList() // empty list

	tio, _, _, _ := iostreams.Test()
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio,
	})
	require.NoError(t, err)
	assert.Equal(t, ActionProceed, action)
}

func TestCheckConcurrency_DifferentWorkDir(t *testing.T) {
	ctr := mock.RunningContainerFixture("myproj", "loop-agent")
	ctr.Labels[_testCfg.LabelWorkdir()] = "/other/dir"
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr)

	tio, _, _, _ := iostreams.Test()
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio,
	})
	require.NoError(t, err)
	assert.Equal(t, ActionProceed, action)
}

func TestCheckConcurrency_SameWorkDir_NonInteractive(t *testing.T) {
	ctr := mock.RunningContainerFixture("myproj", "loop-agent")
	ctr.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr)

	tio, _, _, errOut := iostreams.Test()
	// Non-interactive: no Prompter set
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio,
	})
	require.NoError(t, err)
	assert.Equal(t, ActionProceed, action)
	assert.Contains(t, errOut.String(), "already running")
}

func testPrompterWithSelection(tio *iostreams.IOStreams, in *bytes.Buffer, selection int) func() *prompter.Prompter {
	in.WriteString(selectionInput(selection))
	return func() *prompter.Prompter {
		return prompter.NewPrompter(tio)
	}
}

// selectionInput converts a 0-based selection index to the 1-based input string.
func selectionInput(idx int) string {
	return fmt.Sprintf("%d\n", idx+1)
}

func TestCheckConcurrency_SameWorkDir_Interactive_Worktree(t *testing.T) {
	ctr := mock.RunningContainerFixture("myproj", "loop-agent")
	ctr.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr)

	tio, in, _, _ := iostreams.Test()
	tio.SetStdinTTY(true)
	tio.SetStdoutTTY(true)
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio,
		Prompter:  testPrompterWithSelection(tio, in, 0), // "Use a worktree"
	})
	require.NoError(t, err)
	assert.Equal(t, ActionWorktree, action)
}

func TestCheckConcurrency_SameWorkDir_Interactive_Proceed(t *testing.T) {
	ctr := mock.RunningContainerFixture("myproj", "loop-agent")
	ctr.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr)

	tio, in, _, _ := iostreams.Test()
	tio.SetStdinTTY(true)
	tio.SetStdoutTTY(true)
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio,
		Prompter:  testPrompterWithSelection(tio, in, 1), // "Proceed anyway"
	})
	require.NoError(t, err)
	assert.Equal(t, ActionProceed, action)
}

func TestCheckConcurrency_SameWorkDir_Interactive_Abort(t *testing.T) {
	ctr := mock.RunningContainerFixture("myproj", "loop-agent")
	ctr.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr)

	tio, in, _, _ := iostreams.Test()
	tio.SetStdinTTY(true)
	tio.SetStdoutTTY(true)
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio,
		Prompter:  testPrompterWithSelection(tio, in, 2), // "Abort"
	})
	require.NoError(t, err)
	assert.Equal(t, ActionAbort, action)
}

func TestCheckConcurrency_DockerListError(t *testing.T) {
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerListError(fmt.Errorf("docker daemon unavailable"))

	tio, _, _, _ := iostreams.Test()
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio,
	})
	require.Error(t, err)
	assert.Equal(t, ActionProceed, action)
	assert.Contains(t, err.Error(), "checking for concurrent sessions")
}

func TestCheckConcurrency_MultipleRunning_WarnsAndProceeds(t *testing.T) {
	ctr1 := mock.RunningContainerFixture("myproj", "loop-agent1")
	ctr1.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	ctr2 := mock.RunningContainerFixture("myproj", "loop-agent2")
	ctr2.Labels[_testCfg.LabelWorkdir()] = "/workspace"
	fake := mock.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupContainerList(ctr1, ctr2)

	tio, _, _, errOut := iostreams.Test()
	// Non-interactive should still warn and proceed
	action, err := CheckConcurrency(context.Background(), &ConcurrencyCheckConfig{
		Client:    fake.Client,
		Project:   "myproj",
		WorkDir:   "/workspace",
		IOStreams: tio,
	})
	require.NoError(t, err)
	assert.Equal(t, ActionProceed, action)
	assert.Contains(t, errOut.String(), "already running")
}

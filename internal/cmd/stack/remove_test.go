package stack_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	stackcmd "github.com/schmitthub/clawker/internal/cmd/stack"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

func TestStackRemove_Registered(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	dir := writeStackDir(t, t.TempDir(), "my-rust")

	f, _ := newTestFactory(t, cfg)
	reg := stackcmd.NewCmdStackRegister(f, nil)
	reg.SetArgs([]string{dir})
	require.NoError(t, reg.Execute())
	require.Contains(t, cfg.Project().Stacks, "my-rust")

	rm := stackcmd.NewCmdStackRemove(f, nil)
	rm.SetArgs([]string{"my-rust"})
	require.NoError(t, rm.Execute())

	_, stillThere := cfg.Project().Stacks["my-rust"]
	assert.False(t, stillThere, "registration should be removed")
}

func TestStackRemove_ShippedRejected(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	f, _ := newTestFactory(t, cfg)

	rm := stackcmd.NewCmdStackRemove(f, nil)
	rm.SetArgs([]string{"go"})
	err := rm.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "built-in")
}

func TestStackRemove_Unknown(t *testing.T) {
	cfg := configmocks.NewIsolatedTestConfig(t)
	f, _ := newTestFactory(t, cfg)

	rm := stackcmd.NewCmdStackRemove(f, nil)
	rm.SetArgs([]string{"nonexistent"})
	err := rm.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

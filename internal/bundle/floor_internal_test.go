package bundle

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFloorNames pins the relocated embedded floor: harnesses, stacks, and the
// monitoring extension all resolve as bare-named peer components under
// internal/bundle/assets, structurally identical to a loose tier.
func TestFloorNames(t *testing.T) {
	assert.Equal(t, []string{"claude", "codex"}, FloorNames(ComponentHarness))
	assert.Equal(t, []string{"go", "node", "python", "rust"}, FloorNames(ComponentStack))
	assert.Equal(t, []string{"claude-code"}, FloorNames(ComponentMonitoring),
		"monitoring moved out of the harness dir into a peer floor dir")
}

func TestFloorFS_LoadsManifest(t *testing.T) {
	// The claude harness manifest is reachable, and its monitoring: declaration
	// was stripped (monitoring is a floor peer now).
	src, err := FloorFS(ComponentHarness, "claude")
	require.NoError(t, err)
	raw, err := fs.ReadFile(src, "harness.yaml")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "monitoring:",
		"the claude harness must not declare monitoring after relocation")

	// The claude-code monitoring unit loads from the peer floor dir.
	monSrc, err := FloorFS(ComponentMonitoring, "claude-code")
	require.NoError(t, err)
	_, err = fs.ReadFile(monSrc, "monitoring.yaml")
	require.NoError(t, err)
}

func TestFloorFS_Missing(t *testing.T) {
	_, err := FloorFS(ComponentStack, "does-not-exist")
	require.Error(t, err)
}

func TestFloorComponent(t *testing.T) {
	c, ok := floorComponent(ComponentStack, "node")
	require.True(t, ok)
	assert.Equal(t, TierFloor, c.Provenance.Tier)
	assert.Equal(t, "node", c.Address.String())
	assert.False(t, c.Address.Qualified())

	_, ok = floorComponent(ComponentStack, "nope")
	assert.False(t, ok)
}

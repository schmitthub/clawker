package ralph

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCmdTUI(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdTUI(f)

	// Test command properties
	assert.Equal(t, "tui", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)

	// Test that RunE is set
	require.NotNil(t, cmd.RunE)

	// Test no required flags (tui reads project from config)
	flags := cmd.Flags()
	assert.Equal(t, 0, flags.NFlag())
}

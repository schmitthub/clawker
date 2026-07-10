package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAddress_BareAndQualified(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		a, err := ParseAddress("node")
		require.NoError(t, err)
		assert.False(t, a.Qualified())
		assert.Equal(t, "node", a.Name)
		assert.Equal(t, "node", a.String())
		assert.Equal(t, BundleID{Namespace: "", Name: ""}, a.ID())
	})

	t.Run("qualified", func(t *testing.T) {
		a, err := ParseAddress("acme.tools.node")
		require.NoError(t, err)
		assert.True(t, a.Qualified())
		assert.Equal(t, "acme", a.Namespace)
		assert.Equal(t, "tools", a.Bundle)
		assert.Equal(t, "node", a.Name)
		assert.Equal(t, "acme.tools.node", a.String())
		assert.Equal(t, BundleID{Namespace: "acme", Name: "tools"}, a.ID())
	})

	t.Run("invalid segment count", func(t *testing.T) {
		_, err := ParseAddress("acme.tools")
		assert.Error(t, err)
	})
}

// TestBundleID_String pins the dotted identity spelling — the same form the
// bundle-level CLI arguments accept (bundle remove <namespace.name>), never a
// filesystem path.
func TestBundleID_String(t *testing.T) {
	id := BundleID{Namespace: "acme", Name: "tools"}
	assert.Equal(t, "acme.tools", id.String())
}

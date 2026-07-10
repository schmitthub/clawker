package consts_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/consts"
)

// Conformance: E21 — the shared name rule that fails a bad registered/declared name at config load.
func TestValidateName(t *testing.T) {
	valid := []string{"go", "node", "my-rust", "a", "a1", "claude-code", strings.Repeat("a", 32)}
	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, consts.ValidateName(name))
		})
	}

	invalid := []string{
		"",                      // empty
		strings.Repeat("a", 33), // too long
		"-node",                 // leading hyphen
		"node-",                 // trailing hyphen
		"Node",                  // uppercase
		"my_rust",               // underscore
		"my.rust",               // dot
		"no/slash",              // slash
		"sp ace",                // space
		"café",                  // unicode
	}
	for _, name := range invalid {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, consts.ValidateName(name))
		})
	}
}

// Conformance: E21 — the harness name rule (reserved aliases + slug) enforced at config load.
func TestValidateHarnessName(t *testing.T) {
	for _, reserved := range []string{consts.ImageTagDefaultAlias, consts.ImageTagLatest, consts.ImageTagBase} {
		t.Run(reserved, func(t *testing.T) {
			assert.ErrorContains(t, consts.ValidateHarnessName(reserved), "reserved")
		})
	}

	t.Run("valid harness name", func(t *testing.T) {
		assert.NoError(t, consts.ValidateHarnessName("claude"))
	})

	t.Run("invalid slug still rejected", func(t *testing.T) {
		assert.Error(t, consts.ValidateHarnessName("Claude_Code"))
	})
}

// SplitAddress classifies a component address as bare (floor/loose) or a
// fully-qualified namespace.bundle.component triple, validating every segment.
func TestSplitAddress(t *testing.T) {
	t.Run("bare name", func(t *testing.T) {
		ns, bundle, name, qualified, err := consts.SplitAddress("node")
		require.NoError(t, err)
		assert.False(t, qualified)
		assert.Empty(t, ns)
		assert.Empty(t, bundle)
		assert.Equal(t, "node", name)
	})

	t.Run("qualified triple", func(t *testing.T) {
		ns, bundle, name, qualified, err := consts.SplitAddress("acme.tools.rust")
		require.NoError(t, err)
		assert.True(t, qualified)
		assert.Equal(t, "acme", ns)
		assert.Equal(t, "tools", bundle)
		assert.Equal(t, "rust", name)
	})

	t.Run("reserved namespace is structurally valid", func(t *testing.T) {
		// SplitAddress is structural only — reserved-namespace rejection is
		// ValidateNamespace/ValidateAddress' job, not the splitter's.
		_, _, _, qualified, err := consts.SplitAddress("clawker.tools.rust")
		require.NoError(t, err)
		assert.True(t, qualified)
	})

	invalid := []string{
		"",                // empty
		"a.b",             // two segments
		"a.b.c.d",         // four segments
		".tools.rust",     // empty namespace segment
		"acme..rust",      // empty bundle segment
		"acme.tools.",     // empty name segment
		"Acme.tools.rust", // uppercase segment
		"acme.tools.Rust", // uppercase trailing segment
		"Node",            // bare, bad slug
		"my_rust",         // bare, underscore
	}
	for _, addr := range invalid {
		t.Run("invalid/"+addr, func(t *testing.T) {
			_, _, _, _, err := consts.SplitAddress(addr)
			assert.Error(t, err)
		})
	}
}

// JoinAddress is the inverse of SplitAddress: it spells the dotted
// three-segment address and round-trips through the splitter.
// ValidateAddress is the reserved-aware form validator: it rejects a
// qualified address whose namespace segment is reserved.
func TestValidateAddress(t *testing.T) {
	valid := []string{"node", "acme.tools.rust", "clawkerish.tools.rust"}
	for _, addr := range valid {
		t.Run("valid/"+addr, func(t *testing.T) {
			assert.NoError(t, consts.ValidateAddress(addr))
		})
	}

	t.Run("reserved namespace rejected", func(t *testing.T) {
		for _, addr := range []string{
			"clawker.tools.rust",
			"clawker-inc.tools.rust",
			"inc-clawker.tools.rust",
			"official.tools.rust",
		} {
			assert.ErrorContains(t, consts.ValidateAddress(addr), "reserved")
		}
	})

	t.Run("malformed rejected", func(t *testing.T) {
		assert.Error(t, consts.ValidateAddress("a.b"))
	})
}

// ValidateComponentRef is the selection-key validator: bare uses the plain
// name rule (no tag-alias reservation, that stays bare-harness-only), and
// qualified is validated per segment without the reserved-namespace gate.
func TestValidateComponentRef(t *testing.T) {
	t.Run("bare name", func(t *testing.T) {
		assert.NoError(t, consts.ValidateComponentRef("rust"))
	})

	t.Run("bare tag alias is not reserved here", func(t *testing.T) {
		// The image-tag alias reservation is ValidateHarnessName's concern and
		// applies only to bare harness names — not to a generic component ref.
		assert.NoError(t, consts.ValidateComponentRef(consts.ImageTagDefaultAlias))
	})

	t.Run("qualified triple", func(t *testing.T) {
		assert.NoError(t, consts.ValidateComponentRef("acme.tools.rust"))
	})

	t.Run("qualified reserved namespace is structurally accepted", func(t *testing.T) {
		// Selection keys are structural; the reserved gate is enforced at the
		// bundle manifest front door, not on every selection.
		assert.NoError(t, consts.ValidateComponentRef("clawker.tools.rust"))
	})

	t.Run("malformed rejected", func(t *testing.T) {
		require.Error(t, consts.ValidateComponentRef("a.b"))
		assert.Error(t, consts.ValidateComponentRef("Rust"))
	})
}

// ValidateNamespace layers the reserved-set rejection onto the shared name rule.
func TestValidateNamespace(t *testing.T) {
	valid := []string{"acme", "clawkerish", "myclawker", "a", "some-org"}
	for _, ns := range valid {
		t.Run("valid/"+ns, func(t *testing.T) {
			assert.NoError(t, consts.ValidateNamespace(ns))
		})
	}

	reserved := []string{"clawker", "clawker-inc", "inc-clawker", "official"}
	for _, ns := range reserved {
		t.Run("reserved/"+ns, func(t *testing.T) {
			assert.ErrorContains(t, consts.ValidateNamespace(ns), "reserved")
		})
	}

	t.Run("bad slug rejected", func(t *testing.T) {
		require.Error(t, consts.ValidateNamespace("Acme"))
		assert.Error(t, consts.ValidateNamespace(""))
	})
}

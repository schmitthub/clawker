package consts_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

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

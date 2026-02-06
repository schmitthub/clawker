package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoLipglossImport ensures that no non-test Go files in this package
// import lipgloss directly. All lipgloss usage should flow through iostreams.
func TestNoLipglossImport(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	for _, entry := range entries {
		name := entry.Name()

		// Only check .go source files, skip test files.
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		data, err := os.ReadFile(filepath.Clean(name))
		require.NoError(t, err, "reading %s", name)

		assert.NotContains(t, string(data), `"github.com/charmbracelet/lipgloss"`,
			"%s must not import lipgloss directly — use iostreams re-exports", name)
	}
}

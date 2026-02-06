package iostreams

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoBubbleTeaImport ensures that no non-test Go files in this package
// import bubbletea or bubbles directly. Those belong exclusively in the tui package.
func TestNoBubbleTeaImport(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	forbidden := []string{
		`"github.com/charmbracelet/bubbletea"`,
		`"github.com/charmbracelet/bubbles`,
	}

	for _, entry := range entries {
		name := entry.Name()

		// Only check .go source files, skip test files.
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		data, err := os.ReadFile(filepath.Clean(name))
		require.NoError(t, err, "reading %s", name)

		content := string(data)
		for _, imp := range forbidden {
			assert.NotContains(t, content, imp,
				"%s must not import bubbletea/bubbles — those belong in tui", name)
		}
	}
}

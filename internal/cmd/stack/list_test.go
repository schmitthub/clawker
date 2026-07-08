package stack_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	stackcmd "github.com/schmitthub/clawker/internal/cmd/stack"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
)

const listProjectYAML = `stacks:
  go: { path: ./stacks/go }
  my-rust: { path: ./stacks/my-rust }
`

type listStackRow struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Source  string `json:"source"`
	Shadows string `json:"shadows"`
}

func TestStackList_JSON(t *testing.T) {
	cfg := configmocks.NewFromString(listProjectYAML, "")
	f, out := newTestFactory(t, cfg)

	cmd := stackcmd.NewCmdStackList(f, nil)
	cmd.SetArgs([]string{"--json"})
	require.NoError(t, cmd.Execute())

	var rows []listStackRow
	require.NoError(t, json.Unmarshal(out.Bytes(), &rows))

	byName := map[string]listStackRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}

	// A project registration shadowing a shipped stack.
	goRow, ok := byName["go"]
	require.True(t, ok)
	assert.Equal(t, "project", goRow.Source)
	assert.Equal(t, "shipped", goRow.Shadows)
	assert.Equal(t, "./stacks/go", goRow.Path)

	// A project-only registration (no shipped shadow).
	rustRow, ok := byName["my-rust"]
	require.True(t, ok)
	assert.Equal(t, "project", rustRow.Source)
	assert.Empty(t, rustRow.Shadows)

	// A shipped stack with no project registration.
	nodeRow, ok := byName["node"]
	require.True(t, ok)
	assert.Equal(t, "shipped", nodeRow.Source)
	assert.Equal(t, "(built-in)", nodeRow.Path)
}

func TestStackList_Quiet(t *testing.T) {
	cfg := configmocks.NewFromString(listProjectYAML, "")
	f, out := newTestFactory(t, cfg)

	cmd := stackcmd.NewCmdStackList(f, nil)
	cmd.SetArgs([]string{"-q"})
	require.NoError(t, cmd.Execute())

	names := strings.Fields(out.String())
	assert.Contains(t, names, "go")
	assert.Contains(t, names, "my-rust")
	assert.Contains(t, names, "node")
	// Sorted, so the project entries interleave with shipped names.
	assert.Equal(t, names, sortedCopy(names))
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

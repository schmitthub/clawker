package cmdutil_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
)

func TestResolveRegistryPath(t *testing.T) {
	root := filepath.FromSlash("/home/dev/proj")
	cwd := filepath.FromSlash("/home/dev/proj/sub")

	tests := []struct {
		name       string
		root       string
		cwd        string
		input      string
		wantAbs    string
		wantStored string
	}{
		{
			name:       "relative inside root stored relative to root",
			root:       root,
			cwd:        root,
			input:      filepath.FromSlash("./stacks/my-rust"),
			wantAbs:    filepath.FromSlash("/home/dev/proj/stacks/my-rust"),
			wantStored: filepath.FromSlash("stacks/my-rust"),
		},
		{
			name:       "relative from subdir re-anchored to root",
			root:       root,
			cwd:        cwd,
			input:      filepath.FromSlash("../stacks/my-rust"),
			wantAbs:    filepath.FromSlash("/home/dev/proj/stacks/my-rust"),
			wantStored: filepath.FromSlash("stacks/my-rust"),
		},
		{
			name:       "absolute inside root stored relative to root",
			root:       root,
			cwd:        cwd,
			input:      filepath.FromSlash("/home/dev/proj/tools/codex"),
			wantAbs:    filepath.FromSlash("/home/dev/proj/tools/codex"),
			wantStored: filepath.FromSlash("tools/codex"),
		},
		{
			name:       "absolute outside root stored absolute",
			root:       root,
			cwd:        cwd,
			input:      filepath.FromSlash("/opt/shared/rust"),
			wantAbs:    filepath.FromSlash("/opt/shared/rust"),
			wantStored: filepath.FromSlash("/opt/shared/rust"),
		},
		{
			name:       "no project root stores absolute",
			root:       "",
			cwd:        cwd,
			input:      filepath.FromSlash("./stacks/my-rust"),
			wantAbs:    filepath.FromSlash("/home/dev/proj/sub/stacks/my-rust"),
			wantStored: filepath.FromSlash("/home/dev/proj/sub/stacks/my-rust"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := cmdutil.ResolveRegistryPath(tt.root, tt.cwd, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.wantAbs, resolved.Abs)
			assert.Equal(t, tt.wantStored, resolved.Stored)
		})
	}
}

func TestMergeRegistryRows(t *testing.T) {
	shipped := []string{"go", "node", "python"}
	registered := map[string]string{
		"go":      "./stacks/go", // shadows shipped
		"my-rust": "./stacks/my-rust",
		"empty":   "", // empty path is NOT a registration
	}

	rows := cmdutil.MergeRegistryRows(shipped, registered)
	byName := map[string]cmdutil.RegistryRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}

	// "empty" has no path, so it must not appear at all.
	_, hasEmpty := byName["empty"]
	assert.False(t, hasEmpty, "empty-path entry is not a registration")

	assert.Equal(t, cmdutil.RegistrySourceProject, byName["go"].Source)
	assert.Equal(t, cmdutil.RegistrySourceShipped, byName["go"].Shadows)
	assert.Equal(t, cmdutil.RegistrySourceProject, byName["my-rust"].Source)
	assert.Empty(t, byName["my-rust"].Shadows)
	assert.Equal(t, cmdutil.RegistrySourceShipped, byName["node"].Source)
	assert.Equal(t, cmdutil.RegistryBuiltinPath, byName["node"].Path)

	// Rows are sorted by name.
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	assert.Equal(t, []string{"go", "my-rust", "node", "python"}, names)
}

func TestResolveRegistryPath_Rejects(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"tilde", "~/stacks/rust"},
		{"env var", "$HOME/stacks/rust"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cmdutil.ResolveRegistryPath("/root", "/root", tt.input)
			require.Error(t, err)
		})
	}
}

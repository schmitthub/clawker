package project_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRegistry writes a project registry containing the given roots (in
// order) to the isolated data dir. Empty strings produce entries with a blank
// root.
func writeRegistry(t *testing.T, env *testenv.Env, roots ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("projects:\n")
	for i, root := range roots {
		fmt.Fprintf(&b, "  - name: p%d\n    root: %q\n", i, root)
	}
	env.WriteYAML(t, testenv.ProjectRegistry, "", b.String())
}

// mkdirAll creates (and returns) a directory underneath the isolated temp
// base. Paths derive from env.Dirs.Base, which testenv has already
// symlink-resolved (macOS /var → /private/var), so expected and resolved
// paths compare equal.
func mkdirAll(t *testing.T, env *testenv.Env, elem ...string) string {
	t.Helper()
	dir := filepath.Join(append([]string{env.Dirs.Base}, elem...)...)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

func TestRegistry_ResolveRoot(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, env *testenv.Env) (cwd, wantRoot string)
		wantErr error
	}{
		{
			name: "cwd deep inside registered root resolves to that root",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				root := mkdirAll(t, env, "proj")
				cwd := mkdirAll(t, env, "proj", "pkg", "deep")
				writeRegistry(t, env, root)
				return cwd, root
			},
		},
		{
			name: "nested registered roots resolve to the deepest",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				outer := mkdirAll(t, env, "outer")
				inner := mkdirAll(t, env, "outer", "nested")
				cwd := mkdirAll(t, env, "outer", "nested", "src")
				writeRegistry(t, env, outer, inner)
				return cwd, inner
			},
		},
		{
			name: "prefix sibling does not match",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				root := mkdirAll(t, env, "a", "foo")
				cwd := mkdirAll(t, env, "a", "foobar")
				writeRegistry(t, env, root)
				return cwd, ""
			},
			wantErr: project.ErrNotInProject,
		},
		{
			name: "cwd equal to registered root matches",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				root := mkdirAll(t, env, "proj")
				writeRegistry(t, env, root)
				return root, root
			},
		},
		{
			name: "cwd outside all registered roots",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				root := mkdirAll(t, env, "proj")
				cwd := mkdirAll(t, env, "elsewhere")
				writeRegistry(t, env, root)
				return cwd, ""
			},
			wantErr: project.ErrNotInProject,
		},
		{
			name: "missing registry file",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				cwd := mkdirAll(t, env, "proj")
				return cwd, ""
			},
			wantErr: project.ErrNotInProject,
		},
		{
			name: "empty root entries are skipped",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				root := mkdirAll(t, env, "proj")
				cwd := mkdirAll(t, env, "proj", "sub")
				writeRegistry(t, env, "", root)
				return cwd, root
			},
		},
		{
			name: "only empty root entries yields no match",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				cwd := mkdirAll(t, env, "proj")
				writeRegistry(t, env, "")
				// Pin the process cwd to the temp base: without the blank-root
				// guard, resolveRootPath("") anchors at the process cwd via
				// filepath.Abs and would spuriously match Base as an ancestor
				// of Base/proj.
				t.Chdir(env.Dirs.Base)
				return cwd, ""
			},
			wantErr: project.ErrNotInProject,
		},
		{
			name: "root registered via symlink matches real-path cwd",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				real := mkdirAll(t, env, "real")
				cwd := mkdirAll(t, env, "real", "sub")
				link := filepath.Join(env.Dirs.Base, "link")
				require.NoError(t, os.Symlink(real, link))
				writeRegistry(t, env, link)
				return cwd, real
			},
		},
		{
			// The returned anchor stays in cwd's own (symlinked) path form so
			// it remains a string-ancestor of cwd for config walk-up.
			name: "root registered by real path matches symlinked cwd",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				real := mkdirAll(t, env, "real")
				mkdirAll(t, env, "real", "sub")
				link := filepath.Join(env.Dirs.Base, "link")
				require.NoError(t, os.Symlink(real, link))
				writeRegistry(t, env, real)
				return filepath.Join(link, "sub"), link
			},
		},
		{
			// A symlink that shortcuts to a deeper directory changes component
			// depth between logical and resolved forms; the suffix mapping
			// cannot be verified, and the logical cwd has no project ancestor
			// in its own path form, so resolution reports not-in-project
			// rather than returning a resolved-space anchor that would break
			// config walk-up.
			name: "depth-changing symlinked cwd yields ErrNotInProject",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				real := mkdirAll(t, env, "real")
				deep := mkdirAll(t, env, "real", "a", "b")
				shortcut := filepath.Join(env.Dirs.Base, "shortcut")
				require.NoError(t, os.Symlink(deep, shortcut))
				writeRegistry(t, env, real)
				return shortcut, ""
			},
			wantErr: project.ErrNotInProject,
		},
		{
			name: "uncleaned cwd is cleaned before matching",
			setup: func(t *testing.T, env *testenv.Env) (string, string) {
				root := mkdirAll(t, env, "proj")
				sub := mkdirAll(t, env, "proj", "sub")
				writeRegistry(t, env, root)
				sep := string(filepath.Separator)
				cwd := sub + sep + "." + sep // trailing "/./" — not cleaned by the caller
				return cwd, root
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testenv.New(t)
			cwd, wantRoot := tt.setup(t, env)

			got, err := env.Registry(t).ResolveRoot(cwd)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, wantRoot, got)
		})
	}
}

// TestRegistry_CurrentRoot_ConfigWalkUpSeam proves the factory chain end to end:
// Registry.CurrentRoot (logical, PWD-honoring os.Getwd) → config.NewConfig with
// the result as walk-up anchor. The anchor returned through a symlinked cwd
// must never trip storage's anchor-not-ancestor guard, which compares against
// the same logical cwd.
func TestRegistry_CurrentRoot_ConfigWalkUpSeam(t *testing.T) {
	t.Run("symlinked cwd anchor survives config walk-up", func(t *testing.T) {
		env := testenv.New(t)
		real := mkdirAll(t, env, "real")
		mkdirAll(t, env, "real", "sub")
		link := filepath.Join(env.Dirs.Base, "link")
		require.NoError(t, os.Symlink(real, link))
		writeRegistry(t, env, real)

		// t.Chdir sets PWD, so os.Getwd reports the logical symlinked path.
		t.Chdir(filepath.Join(link, "sub"))

		root, err := env.Registry(t).CurrentRoot()
		require.NoError(t, err)
		assert.Equal(t, link, root, "root must be in cwd's own (symlinked) path form")

		// The anchor must be accepted by storage's walk-up ancestor guard.
		cfg, err := config.NewConfig(config.WithProjectRoot(root))
		require.NoError(t, err)
		require.NotNil(t, cfg)
	})

	t.Run("depth-changing symlinked cwd degrades to not-in-project", func(t *testing.T) {
		env := testenv.New(t)
		real := mkdirAll(t, env, "real")
		deep := mkdirAll(t, env, "real", "a", "b")
		shortcut := filepath.Join(env.Dirs.Base, "shortcut")
		require.NoError(t, os.Symlink(deep, shortcut))
		writeRegistry(t, env, real)

		t.Chdir(shortcut)

		root, err := env.Registry(t).CurrentRoot()
		assert.ErrorIs(t, err, project.ErrNotInProject)
		assert.Empty(t, root)

		// Mirror the factory's degradation: ErrNotInProject → empty root →
		// walk-up disabled. Config construction must still succeed; the guard
		// never fires.
		cfg, err := config.NewConfig(config.WithProjectRoot(""))
		require.NoError(t, err)
		require.NotNil(t, cfg)
	})
}

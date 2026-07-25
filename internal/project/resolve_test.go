package project_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/testenv"
)

// registryYAML renders a project registry containing the given roots, in
// order. Empty strings produce entries with a blank root.
func registryYAML(roots ...string) string {
	var b strings.Builder
	b.WriteString("projects:\n")
	for i, root := range roots {
		fmt.Fprintf(&b, "  - name: p%d\n    root: %q\n", i, root)
	}
	return b.String()
}

// seedRegistry builds an in-memory registry from the given roots — resolution
// is a function of registry content, not of where the file lives, so these
// cases seed the data layer instead of the filesystem. The file-backed path is
// covered by TestRegistry_CurrentRoot_ConfigWalkUpSeam and the manager
// lifecycle tests.
//
//nolint:ireturn // returns the project.Registry domain interface by design — its impl stays package-private
func seedRegistry(t *testing.T, roots ...string) project.Registry {
	t.Helper()
	reg, err := project.NewRegistryFromString(registryYAML(roots...))
	require.NoError(t, err)
	return reg
}

// writeRegistry writes a project registry containing the given roots to the
// isolated data dir, so a registry constructed afterwards discovers it.
func writeRegistry(t *testing.T, env *testenv.Env, roots ...string) {
	t.Helper()
	env.WriteYAML(t, testenv.ProjectRegistry, "", registryYAML(roots...))
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
		setup   func(t *testing.T, env *testenv.Env) (reg project.Registry, cwd, wantRoot string)
		wantErr error
	}{
		{
			name: "cwd deep inside registered root resolves to that root",
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				root := mkdirAll(t, env, "proj")
				cwd := mkdirAll(t, env, "proj", "pkg", "deep")
				reg := seedRegistry(t, root)
				return reg, cwd, root
			},
		},
		{
			name: "nested registered roots resolve to the deepest",
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				outer := mkdirAll(t, env, "outer")
				inner := mkdirAll(t, env, "outer", "nested")
				cwd := mkdirAll(t, env, "outer", "nested", "src")
				reg := seedRegistry(t, outer, inner)
				return reg, cwd, inner
			},
		},
		{
			name: "prefix sibling does not match",
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				root := mkdirAll(t, env, "a", "foo")
				cwd := mkdirAll(t, env, "a", "foobar")
				reg := seedRegistry(t, root)
				return reg, cwd, ""
			},
			wantErr: project.ErrNotInProject,
		},
		{
			name: "cwd equal to registered root matches",
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				root := mkdirAll(t, env, "proj")
				reg := seedRegistry(t, root)
				return reg, root, root
			},
		},
		{
			name: "cwd outside all registered roots",
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				root := mkdirAll(t, env, "proj")
				cwd := mkdirAll(t, env, "elsewhere")
				reg := seedRegistry(t, root)
				return reg, cwd, ""
			},
			wantErr: project.ErrNotInProject,
		},
		{
			name: "empty registry",
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				cwd := mkdirAll(t, env, "proj")
				return seedRegistry(t), cwd, ""
			},
			wantErr: project.ErrNotInProject,
		},
		{
			name: "empty root entries are skipped",
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				root := mkdirAll(t, env, "proj")
				cwd := mkdirAll(t, env, "proj", "sub")
				reg := seedRegistry(t, "", root)
				return reg, cwd, root
			},
		},
		{
			name: "only empty root entries yields no match",
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				cwd := mkdirAll(t, env, "proj")
				reg := seedRegistry(t, "")
				// Pin the process cwd to the temp base: without the blank-root
				// guard, resolveRootPath("") anchors at the process cwd via
				// filepath.Abs and would spuriously match Base as an ancestor
				// of Base/proj.
				t.Chdir(env.Dirs.Base)
				return reg, cwd, ""
			},
			wantErr: project.ErrNotInProject,
		},
		{
			name: "root registered via symlink matches real-path cwd",
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				real := mkdirAll(t, env, "real")
				cwd := mkdirAll(t, env, "real", "sub")
				link := filepath.Join(env.Dirs.Base, "link")
				require.NoError(t, os.Symlink(real, link))
				reg := seedRegistry(t, link)
				return reg, cwd, real
			},
		},
		{
			// The returned anchor stays in cwd's own (symlinked) path form so
			// it remains a string-ancestor of cwd for config walk-up.
			name: "root registered by real path matches symlinked cwd",
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				real := mkdirAll(t, env, "real")
				mkdirAll(t, env, "real", "sub")
				link := filepath.Join(env.Dirs.Base, "link")
				require.NoError(t, os.Symlink(real, link))
				reg := seedRegistry(t, real)
				return reg, filepath.Join(link, "sub"), link
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
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				real := mkdirAll(t, env, "real")
				deep := mkdirAll(t, env, "real", "a", "b")
				shortcut := filepath.Join(env.Dirs.Base, "shortcut")
				require.NoError(t, os.Symlink(deep, shortcut))
				reg := seedRegistry(t, real)
				return reg, shortcut, ""
			},
			wantErr: project.ErrNotInProject,
		},
		{
			name: "uncleaned cwd is cleaned before matching",
			setup: func(t *testing.T, env *testenv.Env) (project.Registry, string, string) {
				t.Helper()
				root := mkdirAll(t, env, "proj")
				sub := mkdirAll(t, env, "proj", "sub")
				reg := seedRegistry(t, root)
				sep := string(filepath.Separator)
				cwd := sub + sep + "." + sep // trailing "/./" — not cleaned by the caller
				return reg, cwd, root
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testenv.New(t)
			reg, cwd, wantRoot := tt.setup(t, env)

			got, err := reg.ResolveRoot(cwd)
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

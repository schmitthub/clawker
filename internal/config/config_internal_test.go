package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- stripScopePrefix ---

func Test_stripScopePrefix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"project.build.image", "build.image"},
		{"settings.logging", "logging"},
		{"registry.projects", "projects"},
		{"nodots", "nodots"},
		{"project.version", "version"},
		{"settings.monitoring.telemetry.metrics_path", "monitoring.telemetry.metrics_path"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, stripScopePrefix(tt.input))
		})
	}
}

// --- readFileToMap ---

func Test_readFileToMap(t *testing.T) {
	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.yaml")
		require.NoError(t, os.WriteFile(path, []byte(""), 0o644))
		m, err := readFileToMap(path)
		require.NoError(t, err)
		assert.Empty(t, m)
	})

	t.Run("valid YAML", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "valid.yaml")
		require.NoError(t, os.WriteFile(path, []byte("build:\n  image: alpine\n"), 0o644))
		m, err := readFileToMap(path)
		require.NoError(t, err)
		assert.Contains(t, m, "build")
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := readFileToMap("/nonexistent/path.yaml")
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("malformed YAML", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.yaml")
		require.NoError(t, os.WriteFile(path, []byte("build: [unclosed"), 0o644))
		_, err := readFileToMap(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parsing")
	})

	t.Run("whitespace only", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "ws.yaml")
		require.NoError(t, os.WriteFile(path, []byte("   \n\n  \n"), 0o644))
		m, err := readFileToMap(path)
		require.NoError(t, err)
		assert.Empty(t, m)
	})
}

// --- schemaForScope ---

func Test_schemaForScope(t *testing.T) {
	t.Run("ScopeProject", func(t *testing.T) {
		s := schemaForScope(ScopeProject)
		_, ok := s.(*Project)
		assert.True(t, ok, "expected *Project, got %T", s)
	})

	t.Run("ScopeSettings", func(t *testing.T) {
		s := schemaForScope(ScopeSettings)
		_, ok := s.(*Settings)
		assert.True(t, ok, "expected *Settings, got %T", s)
	})

	t.Run("ScopeRegistry", func(t *testing.T) {
		s := schemaForScope(ScopeRegistry)
		_, ok := s.(*ProjectRegistry)
		assert.True(t, ok, "expected *ProjectRegistry, got %T", s)
	})

	t.Run("unknown scope panics", func(t *testing.T) {
		assert.Panics(t, func() {
			schemaForScope("bogus")
		})
	})
}

// --- scopeForKey ---

func Test_scopeForKey(t *testing.T) {
	tests := []struct {
		key   string
		scope ConfigScope
	}{
		{"name", ScopeProject},
		{"build", ScopeProject},
		{"build.image", ScopeProject},
		{"version", ScopeProject},
		{"agent", ScopeProject},
		{"workspace", ScopeProject},
		{"security", ScopeProject},
		{"loop", ScopeProject},
		{"logging", ScopeSettings},
		{"monitoring", ScopeSettings},
		{"host_proxy", ScopeSettings},
		{"default_image", ScopeSettings},
		{"projects", ScopeRegistry},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			scope, err := scopeForKey(tt.key)
			require.NoError(t, err)
			assert.Equal(t, tt.scope, scope)
		})
	}

	t.Run("unknown root errors", func(t *testing.T) {
		_, err := scopeForKey("bogus.key")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no ownership mapping")
	})
}

// --- checkDuplicateTopLevelKeys ---

func Test_checkDuplicateTopLevelKeys(t *testing.T) {
	t.Run("no duplicates", func(t *testing.T) {
		err := checkDuplicateTopLevelKeys("build:\n  image: alpine\nagent:\n  shell: bash\n")
		require.NoError(t, err)
	})

	t.Run("duplicate key", func(t *testing.T) {
		err := checkDuplicateTopLevelKeys("build:\n  image: alpine\nbuild:\n  image: debian\n")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate key")
		assert.Contains(t, err.Error(), "build")
	})

	t.Run("empty content", func(t *testing.T) {
		err := checkDuplicateTopLevelKeys("")
		require.NoError(t, err)
	})

	t.Run("comment only", func(t *testing.T) {
		err := checkDuplicateTopLevelKeys("# just a comment\n")
		require.NoError(t, err)
	})
}

// --- collectLeafPaths ---

func Test_collectLeafPaths(t *testing.T) {
	t.Run("Project includes build.image", func(t *testing.T) {
		paths := collectLeafPaths(reflect.TypeOf(Project{}), "")
		assert.Contains(t, paths, "build.image")
	})

	t.Run("Settings includes logging.file_enabled", func(t *testing.T) {
		paths := collectLeafPaths(reflect.TypeOf(Settings{}), "")
		assert.Contains(t, paths, "logging.file_enabled")
	})

	t.Run("Settings includes host_proxy.daemon.poll_interval", func(t *testing.T) {
		paths := collectLeafPaths(reflect.TypeOf(Settings{}), "")
		assert.Contains(t, paths, "host_proxy.daemon.poll_interval")
	})
}

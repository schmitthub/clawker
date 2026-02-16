package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSortedUnion(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want []string
	}{
		{
			name: "both nil",
			a:    nil,
			b:    nil,
			want: nil,
		},
		{
			name: "a nil",
			a:    nil,
			b:    []string{"x", "y"},
			want: []string{"x", "y"},
		},
		{
			name: "b nil",
			a:    []string{"x", "y"},
			b:    nil,
			want: []string{"x", "y"},
		},
		{
			name: "no overlap",
			a:    []string{"a", "b"},
			b:    []string{"c", "d"},
			want: []string{"a", "b", "c", "d"},
		},
		{
			name: "with overlap",
			a:    []string{"a", "b", "c"},
			b:    []string{"b", "c", "d"},
			want: []string{"a", "b", "c", "d"},
		},
		{
			name: "identical",
			a:    []string{"x", "y"},
			b:    []string{"x", "y"},
			want: []string{"x", "y"},
		},
		{
			name: "result sorted",
			a:    []string{"z", "m"},
			b:    []string{"a", "f"},
			want: []string{"a", "f", "m", "z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sortedUnion(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMergeMaps(t *testing.T) {
	tests := []struct {
		name     string
		base     map[string]string
		override map[string]string
		want     map[string]string
	}{
		{
			name:     "both nil",
			base:     nil,
			override: nil,
			want:     nil,
		},
		{
			name:     "base nil",
			base:     nil,
			override: map[string]string{"A": "1"},
			want:     map[string]string{"A": "1"},
		},
		{
			name:     "override nil",
			base:     map[string]string{"A": "1"},
			override: nil,
			want:     map[string]string{"A": "1"},
		},
		{
			name:     "no overlap",
			base:     map[string]string{"A": "1"},
			override: map[string]string{"B": "2"},
			want:     map[string]string{"A": "1", "B": "2"},
		},
		{
			name:     "override wins conflict",
			base:     map[string]string{"KEY": "base"},
			override: map[string]string{"KEY": "override"},
			want:     map[string]string{"KEY": "override"},
		},
		{
			name:     "case preserved",
			base:     map[string]string{"FoO": "bar"},
			override: map[string]string{"BaZ": "qux"},
			want:     map[string]string{"FoO": "bar", "BaZ": "qux"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeMaps(tt.base, tt.override)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestApplyEnvMapOverrides(t *testing.T) {
	t.Run("overrides matching key", func(t *testing.T) {
		target := map[string]string{"FOO": "original"}
		t.Setenv("CLAWKER_AGENT_ENV_FOO", "replaced")

		applyEnvMapOverrides(target, "CLAWKER_AGENT_ENV_")
		assert.Equal(t, "replaced", target["FOO"])
	})

	t.Run("adds new key", func(t *testing.T) {
		target := map[string]string{"EXISTING": "val"}
		t.Setenv("CLAWKER_AGENT_ENV_NEW", "newval")

		applyEnvMapOverrides(target, "CLAWKER_AGENT_ENV_")
		assert.Equal(t, "newval", target["NEW"])
		assert.Equal(t, "val", target["EXISTING"])
	})

	t.Run("ignores non-matching prefix", func(t *testing.T) {
		target := map[string]string{"FOO": "original"}
		t.Setenv("OTHER_PREFIX_FOO", "nope")

		applyEnvMapOverrides(target, "CLAWKER_AGENT_ENV_")
		assert.Equal(t, "original", target["FOO"])
	})

	t.Run("nil target is noop", func(t *testing.T) {
		t.Setenv("CLAWKER_AGENT_ENV_FOO", "val")
		applyEnvMapOverrides(nil, "CLAWKER_AGENT_ENV_") // should not panic
	})

	t.Run("empty key after prefix is skipped", func(t *testing.T) {
		target := map[string]string{"FOO": "original"}
		// Set env var where key after prefix is empty
		t.Setenv("CLAWKER_AGENT_ENV_", "val")

		applyEnvMapOverrides(target, "CLAWKER_AGENT_ENV_")
		assert.Equal(t, "original", target["FOO"])
		_, exists := target[""]
		assert.False(t, exists)
	})
}

func TestApplyEnvSliceAppend(t *testing.T) {
	t.Run("appends from env var", func(t *testing.T) {
		t.Setenv("CLAWKER_AGENT_FROM_ENV", "NEW_VAR,ANOTHER")

		got := applyEnvSliceAppend([]string{"EXISTING"}, "CLAWKER_AGENT_FROM_ENV")
		assert.Equal(t, []string{"ANOTHER", "EXISTING", "NEW_VAR"}, got)
	})

	t.Run("deduplicates", func(t *testing.T) {
		t.Setenv("CLAWKER_AGENT_FROM_ENV", "EXISTING,NEW")

		got := applyEnvSliceAppend([]string{"EXISTING"}, "CLAWKER_AGENT_FROM_ENV")
		assert.Equal(t, []string{"EXISTING", "NEW"}, got)
	})

	t.Run("empty env var is noop", func(t *testing.T) {
		os.Unsetenv("CLAWKER_AGENT_FROM_ENV")

		got := applyEnvSliceAppend([]string{"A", "B"}, "CLAWKER_AGENT_FROM_ENV")
		assert.Equal(t, []string{"A", "B"}, got)
	})

	t.Run("trims whitespace", func(t *testing.T) {
		t.Setenv("CLAWKER_AGENT_FROM_ENV", " X , Y , Z ")

		got := applyEnvSliceAppend(nil, "CLAWKER_AGENT_FROM_ENV")
		assert.Equal(t, []string{"X", "Y", "Z"}, got)
	})

	t.Run("skips empty segments", func(t *testing.T) {
		t.Setenv("CLAWKER_AGENT_FROM_ENV", "A,,B,")

		got := applyEnvSliceAppend(nil, "CLAWKER_AGENT_FROM_ENV")
		assert.Equal(t, []string{"A", "B"}, got)
	})
}

func TestPostMerge(t *testing.T) {
	t.Run("union slices from both configs", func(t *testing.T) {
		clearClawkerEnv(t)

		userYAML := []byte(`
agent:
  from_env:
    - GH_TOKEN
    - FARTS
  includes:
    - user-include.md
`)
		projectYAML := []byte(`
agent:
  from_env:
    - GH_TOKEN
    - CONTEXT7_API_KEY
  includes:
    - project-include.md
`)

		cfg := &Project{
			Security: SecurityConfig{Firewall: &FirewallConfig{}},
		}

		err := postMerge(cfg, userYAML, projectYAML)
		require.NoError(t, err)

		assert.Equal(t, []string{"CONTEXT7_API_KEY", "FARTS", "GH_TOKEN"}, cfg.Agent.FromEnv)
		assert.Equal(t, []string{"project-include.md", "user-include.md"}, cfg.Agent.Includes)
	})

	t.Run("merge maps with project winning", func(t *testing.T) {
		clearClawkerEnv(t)

		userYAML := []byte(`
agent:
  env:
    FOO: user-foo
    BAR: user-bar
`)
		projectYAML := []byte(`
agent:
  env:
    FOO: project-foo
    BAZ: project-baz
`)

		cfg := &Project{
			Security: SecurityConfig{Firewall: &FirewallConfig{}},
		}

		err := postMerge(cfg, userYAML, projectYAML)
		require.NoError(t, err)

		assert.Equal(t, map[string]string{
			"FOO": "project-foo",
			"BAR": "user-bar",
			"BAZ": "project-baz",
		}, cfg.Agent.Env)
	})

	t.Run("preserves map key case", func(t *testing.T) {
		clearClawkerEnv(t)

		projectYAML := []byte(`
agent:
  env:
    My_Mixed_Case_Key: value
`)

		cfg := &Project{
			Security: SecurityConfig{Firewall: &FirewallConfig{}},
		}

		err := postMerge(cfg, nil, projectYAML)
		require.NoError(t, err)

		_, ok := cfg.Agent.Env["My_Mixed_Case_Key"]
		assert.True(t, ok, "should preserve original case")
	})

	t.Run("nil firewall does not panic", func(t *testing.T) {
		clearClawkerEnv(t)

		cfg := &Project{
			Security: SecurityConfig{Firewall: nil},
		}

		err := postMerge(cfg, nil, nil)
		require.NoError(t, err)
	})

	t.Run("env var map override applied after merge", func(t *testing.T) {
		clearClawkerEnv(t)
		t.Setenv("CLAWKER_AGENT_ENV_FOO", "from-env")

		projectYAML := []byte(`
agent:
  env:
    FOO: from-yaml
`)

		cfg := &Project{
			Security: SecurityConfig{Firewall: &FirewallConfig{}},
		}

		err := postMerge(cfg, nil, projectYAML)
		require.NoError(t, err)

		assert.Equal(t, "from-env", cfg.Agent.Env["FOO"])
	})

	t.Run("env var slice append applied after union", func(t *testing.T) {
		clearClawkerEnv(t)
		t.Setenv("CLAWKER_SECURITY_FIREWALL_ADD_DOMAINS", "env-domain.com")

		projectYAML := []byte(`
security:
  firewall:
    add_domains:
      - yaml-domain.com
`)

		cfg := &Project{
			Security: SecurityConfig{Firewall: &FirewallConfig{}},
		}

		err := postMerge(cfg, nil, projectYAML)
		require.NoError(t, err)

		assert.Equal(t, []string{"env-domain.com", "yaml-domain.com"}, cfg.Security.Firewall.AddDomains)
	})

	t.Run("build_args merged from both configs", func(t *testing.T) {
		clearClawkerEnv(t)

		userYAML := []byte(`
build:
  build_args:
    ARG1: user-val
`)
		projectYAML := []byte(`
build:
  build_args:
    ARG1: project-val
    ARG2: project-only
`)

		cfg := &Project{
			Security: SecurityConfig{Firewall: &FirewallConfig{}},
		}

		err := postMerge(cfg, userYAML, projectYAML)
		require.NoError(t, err)

		assert.Equal(t, map[string]string{
			"ARG1": "project-val",
			"ARG2": "project-only",
		}, cfg.Build.BuildArgs)
	})
}

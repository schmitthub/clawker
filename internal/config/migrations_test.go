package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMigrateRunInstructionsToStrings(t *testing.T) {
	t.Run("converts legacy format", func(t *testing.T) {
		raw := map[string]any{
			"build": map[string]any{
				"instructions": map[string]any{
					"user_run": []any{
						map[string]any{"cmd": "npm ci"},
						map[string]any{"cmd": "pip install -r requirements.txt"},
					},
					"root_run": []any{
						map[string]any{"cmd": "apt-get update"},
					},
				},
			},
		}

		changed := migrateRunInstructionsToStrings(raw)

		assert.True(t, changed)
		inst := raw["build"].(map[string]any)["instructions"].(map[string]any)
		assert.Equal(t, []any{"npm ci", "pip install -r requirements.txt"}, inst["user_run"])
		assert.Equal(t, []any{"apt-get update"}, inst["root_run"])
	})

	t.Run("skips already migrated", func(t *testing.T) {
		raw := map[string]any{
			"build": map[string]any{
				"instructions": map[string]any{
					"user_run": []any{"npm ci", "pip install"},
				},
			},
		}
		assert.False(t, migrateRunInstructionsToStrings(raw))
	})

	t.Run("drops alpine/debian only entries", func(t *testing.T) {
		raw := map[string]any{
			"build": map[string]any{
				"instructions": map[string]any{
					"user_run": []any{
						map[string]any{"cmd": "npm ci"},
						map[string]any{"alpine": "apk add python3"},
						map[string]any{"cmd": ""},
					},
				},
			},
		}

		changed := migrateRunInstructionsToStrings(raw)
		assert.True(t, changed)
		inst := raw["build"].(map[string]any)["instructions"].(map[string]any)
		assert.Equal(t, []any{"npm ci"}, inst["user_run"])
	})

	t.Run("no-op without build", func(t *testing.T) {
		assert.False(t, migrateRunInstructionsToStrings(map[string]any{"agent": map[string]any{}}))
	})

	t.Run("no-op without instructions", func(t *testing.T) {
		assert.False(t, migrateRunInstructionsToStrings(map[string]any{"build": map[string]any{"image": "alpine"}}))
	})

	t.Run("no-op with empty list", func(t *testing.T) {
		raw := map[string]any{
			"build": map[string]any{
				"instructions": map[string]any{"user_run": []any{}},
			},
		}
		assert.False(t, migrateRunInstructionsToStrings(raw))
	})

	t.Run("multiline cmd preserved", func(t *testing.T) {
		multiline := "ARCH=$(dpkg --print-architecture) && \\\n  curl -fsSL | tar -C /usr/local -xzf -"
		raw := map[string]any{
			"build": map[string]any{
				"instructions": map[string]any{
					"root_run": []any{map[string]any{"cmd": multiline}},
				},
			},
		}

		changed := migrateRunInstructionsToStrings(raw)
		assert.True(t, changed)
		inst := raw["build"].(map[string]any)["instructions"].(map[string]any)
		assert.Equal(t, []any{multiline}, inst["root_run"])
	})
}

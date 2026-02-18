package config

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		clawkerYAML string
		settingsYAML string
		wantKey     []string
		wantVal     string
		wantErr     bool
	}{
		{
			name: "from_files",
			clawkerYAML: `version: "2"
build:
  image: "ubuntu:22.04"
`,
			wantKey: []string{"build", "image"},
			wantVal: "ubuntu:22.04",
		},
		{
			name:    "no_files",
			wantKey: []string{"build", "image"},
			wantErr: true,
		},
		{
			name:        "invalid_yaml",
			clawkerYAML: `{{{invalid`,
			wantErr:     true,
		},
		{
			name: "partial_merges_settings",
			clawkerYAML: `version: "1"
build:
  image: "node:20-slim"
`,
			settingsYAML: `default_image: "custom:latest"
`,
			wantKey: []string{"default_image"},
			wantVal: "custom:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset singleton
			once = sync.Once{}
			cfg = nil
			loadErr = nil

			tmpDir := t.TempDir()
			origDir, _ := os.Getwd()
			t.Cleanup(func() { _ = os.Chdir(origDir) })
			_ = os.Chdir(tmpDir)

			homeDir := t.TempDir()
			t.Setenv("CLAWKER_HOME", homeDir)

			if tt.clawkerYAML != "" {
				_ = os.WriteFile(filepath.Join(tmpDir, "clawker.yaml"), []byte(tt.clawkerYAML), 0o644)
			}
			if tt.settingsYAML != "" {
				_ = os.WriteFile(filepath.Join(homeDir, "settings.yaml"), []byte(tt.settingsYAML), 0o644)
			}

			c, err := Read(nil)
			if tt.wantErr {
				if tt.name == "invalid_yaml" {
					if err == nil {
						t.Fatal("expected error for invalid yaml, got nil")
					}
					return
				}
				// no_files case: Read returns a fallback (empty config), Get should fail
				if err != nil {
					return // error during load is also acceptable
				}
				_, getErr := c.Get(tt.wantKey)
				if getErr == nil {
					t.Fatal("expected KeyNotFoundError, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Read() error = %v", err)
			}

			got, err := c.Get(tt.wantKey)
			if err != nil {
				t.Fatalf("Get(%v) error = %v", tt.wantKey, err)
			}
			if got != tt.wantVal {
				t.Errorf("Get(%v) = %q, want %q", tt.wantKey, got, tt.wantVal)
			}
		})
	}
}

func TestGetSet(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		getKey  []string
		setKey  []string
		setVal  string
		keysKey []string
		wantGet string
		wantErr bool
		wantKeys []string
	}{
		{
			name:    "get_nested",
			yaml:    "build:\n  image: \"node:20-slim\"\n",
			getKey:  []string{"build", "image"},
			wantGet: "node:20-slim",
		},
		{
			name:    "get_missing",
			yaml:    "build:\n  image: \"node:20-slim\"\n",
			getKey:  []string{"nonexistent"},
			wantErr: true,
		},
		{
			name:    "set_then_get",
			yaml:    "",
			setKey:  []string{"foo", "bar"},
			setVal:  "baz",
			getKey:  []string{"foo", "bar"},
			wantGet: "baz",
		},
		{
			name:     "keys",
			yaml:     "build:\n  image: \"node:20-slim\"\n  dockerfile: \"Dockerfile\"\n",
			keysKey:  []string{"build"},
			wantKeys: []string{"dockerfile", "image"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ReadFromString(tt.yaml)

			if tt.setKey != nil {
				c.Set(tt.setKey, tt.setVal)
			}

			if tt.keysKey != nil {
				keys, err := c.Keys(tt.keysKey)
				if err != nil {
					t.Fatalf("Keys(%v) error = %v", tt.keysKey, err)
				}
				if len(keys) != len(tt.wantKeys) {
					t.Fatalf("Keys(%v) = %v, want %v", tt.keysKey, keys, tt.wantKeys)
				}
				for i, k := range keys {
					if k != tt.wantKeys[i] {
						t.Errorf("Keys(%v)[%d] = %q, want %q", tt.keysKey, i, k, tt.wantKeys[i])
					}
				}
				return
			}

			got, err := c.Get(tt.getKey)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var knf *KeyNotFoundError
				if !isKeyNotFoundError(err, &knf) {
					t.Fatalf("expected KeyNotFoundError, got %T: %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Get(%v) error = %v", tt.getKey, err)
			}
			if got != tt.wantGet {
				t.Errorf("Get(%v) = %q, want %q", tt.getKey, got, tt.wantGet)
			}
		})
	}
}

func isKeyNotFoundError(err error, target **KeyNotFoundError) bool {
	knf, ok := err.(*KeyNotFoundError)
	if ok && target != nil {
		*target = knf
	}
	return ok
}

func TestStubs(t *testing.T) {
	t.Run("NewBlankConfig", func(t *testing.T) {
		c := NewBlankConfig()
		if c == nil {
			t.Fatal("NewBlankConfig() returned nil")
		}
		// Should have defaults from DefaultConfigYAML
		val, err := c.Get([]string{"version"})
		if err != nil {
			t.Fatalf("Get version error = %v", err)
		}
		if val != "1" {
			t.Errorf("version = %q, want %q", val, "1")
		}
	})

	t.Run("NewFromString", func(t *testing.T) {
		c := NewFromString("custom_key: custom_value\n")
		val, err := c.Get([]string{"custom_key"})
		if err != nil {
			t.Fatalf("Get error = %v", err)
		}
		if val != "custom_value" {
			t.Errorf("custom_key = %q, want %q", val, "custom_value")
		}
	})

	t.Run("NewIsolatedTestConfig", func(t *testing.T) {
		// Reset singleton
		once = sync.Once{}
		cfg = nil
		loadErr = nil

		c, readConfigs := NewIsolatedTestConfig(t)
		if c == nil {
			t.Fatal("NewIsolatedTestConfig() returned nil config")
		}

		// Set a value and write
		c.Set([]string{"test_key"}, "test_value")
		if err := Write(c); err != nil {
			t.Fatalf("Write() error = %v", err)
		}

		// Read back what was written
		var configBuf, settingsBuf bytes.Buffer
		readConfigs(&configBuf, &settingsBuf)

		if configBuf.Len() == 0 {
			t.Error("expected config data to be written")
		}
	})
}

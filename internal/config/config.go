// Package config provides types for interacting with clawker configuration files.
// It loads clawker.yaml (project) and settings.yaml (user) into one merged
// in-memory Config backed by viper, with key-path traversal via Get/Set/Keys/Remove.
package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/viper"
)

const (
	// ClawkerHomeEnv is the environment variable for the clawker home directory.
	ClawkerHomeEnv = "CLAWKER_HOME"
	// DefaultClawkerDir is the default directory path under user home.
	DefaultClawkerDir = ".local/clawker"
)

var (
	cfg     *Config
	once    sync.Once
	loadErr error
)

// defaultConfigStr is the YAML string used by NewBlankConfig to seed defaults.
var defaultConfigStr = DefaultConfigYAML

// Config is an in-memory representation of clawker configuration files.
// It can be thought of as a map where entries consist of a key that
// corresponds to either a string value or a map value, allowing for
// multi-level maps.
type Config struct {
	v  *viper.Viper
	mu sync.RWMutex
}

// Get retrieves a string value from Config.
// The keys argument is a sequence of key values so that nested
// entries can be retrieved. Returns "", KeyNotFoundError if any
// of the keys cannot be found.
func (c *Config) Get(keys []string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	path := strings.Join(keys, ".")
	if !c.v.IsSet(path) {
		return "", &KeyNotFoundError{Key: keys[len(keys)-1]}
	}
	return c.v.GetString(path), nil
}

// Keys enumerates a Config's immediate child keys.
// The keys argument is a sequence of key values so that nested
// map values can have their keys enumerated.
// Returns nil, KeyNotFoundError if any of the keys cannot be found.
func (c *Config) Keys(keys []string) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var allKeys []string
	if len(keys) == 0 {
		allKeys = c.v.AllKeys()
	} else {
		path := strings.Join(keys, ".")
		sub := c.v.Sub(path)
		if sub == nil {
			return nil, &KeyNotFoundError{Key: keys[len(keys)-1]}
		}
		allKeys = sub.AllKeys()
	}

	// Extract immediate children (first segment before ".")
	seen := make(map[string]bool)
	var result []string
	for _, k := range allKeys {
		top := k
		if i := strings.Index(k, "."); i >= 0 {
			top = k[:i]
		}
		if !seen[top] {
			seen[top] = true
			result = append(result, top)
		}
	}
	sort.Strings(result)
	return result, nil
}

// Set sets a string value in Config.
// The keys argument is a sequence of key values so that nested
// entries can be set. If any of the keys do not exist they will
// be created.
func (c *Config) Set(keys []string, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := strings.Join(keys, ".")
	c.v.Set(path, value)
}

// Remove removes an entry from Config.
// The keys argument is a sequence of key values so that nested
// entries can be removed. Returns KeyNotFoundError if any key
// is not found.
func (c *Config) Remove(keys []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := strings.Join(keys, ".")
	if !c.v.IsSet(path) {
		return &KeyNotFoundError{Key: keys[len(keys)-1]}
	}

	// Viper lacks native remove; rebuild from the settings map.
	all := c.v.AllSettings()
	if err := removeFromMap(all, keys); err != nil {
		return err
	}

	newV := viper.New()
	newV.SetConfigType("yaml")
	if err := newV.MergeConfigMap(all); err != nil {
		return err
	}
	c.v = newV
	return nil
}

// removeFromMap deletes a nested key from a map[string]interface{}.
func removeFromMap(m map[string]interface{}, keys []string) error {
	if len(keys) == 1 {
		delete(m, keys[0])
		return nil
	}
	child, ok := m[keys[0]]
	if !ok {
		return &KeyNotFoundError{Key: keys[0]}
	}
	childMap, ok := child.(map[string]interface{})
	if !ok {
		return &KeyNotFoundError{Key: keys[0]}
	}
	return removeFromMap(childMap, keys[1:])
}

// ReadFromString takes a YAML string and returns a Config.
func ReadFromString(str string) *Config {
	v := viper.New()
	v.SetConfigType("yaml")
	if str != "" {
		_ = v.ReadConfig(strings.NewReader(str))
	}
	return &Config{v: v}
}

// Read loads configuration files from the local filesystem and
// returns a Config. A copy of the fallback configuration will
// be returned when there are no configuration files to load.
var Read = func(fallback *Config) (*Config, error) {
	once.Do(func() {
		cfg, loadErr = load(fallback)
	})
	return cfg, loadErr
}

// Write writes Config to the clawker home directory.
func Write(c *Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	home := clawkerHome()
	path := filepath.Join(home, "clawker.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o771); err != nil {
		return err
	}
	return c.v.WriteConfigAs(path)
}

// load reads clawker.yaml from the current directory and merges
// settings.yaml from clawkerHome(). Returns fallback if no files found.
func load(fallback *Config) (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")

	// clawker.yaml from current directory
	v.SetConfigName("clawker")
	v.AddConfigPath(".")
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	// settings.yaml from clawker home
	home := clawkerHome()
	settingsFile := filepath.Join(home, "settings.yaml")
	if _, err := os.Stat(settingsFile); err == nil {
		v.SetConfigFile(settingsFile)
		if err := v.MergeInConfig(); err != nil {
			return nil, err
		}
	}

	// If nothing loaded, use fallback
	if len(v.AllKeys()) == 0 && fallback != nil {
		return ReadFromString(defaultConfigStr), nil
	}

	return &Config{v: v}, nil
}

// clawkerHome returns the clawker home directory.
// Checks CLAWKER_HOME env var first, then defaults to ~/.local/clawker.
func clawkerHome() string {
	if home := os.Getenv(ClawkerHomeEnv); home != "" {
		return home
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, DefaultClawkerDir)
}

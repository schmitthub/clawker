// Package config provides types for interacting with clawker configuration files.
// It loads clawker.yaml (project) and settings.yaml (user) into one merged
// in-memory Config backed by viper, with key-path traversal via Get/Set/Keys/Remove.
// Most of this code is based on [github.com/cli/cli/blob/trunk/pkg/config/config.go](github.com/cli/cli/blob/trunk/pkg/config/config.go).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/viper"
)

const (
	appData          = "AppData"
	clawkerConfigDir = "CLAWKER_CONFIG"
	xdgConfigHome    = "XDG_CONFIG_HOME"
)

// Config is the public configuration contract.
// Add methods here as the config contract grows.
type Config interface {
	RequiredFirewallDomains() []string
}

type configImpl struct {
	v *viper.Viper
}

func newViperConfig() *viper.Viper {
	v := viper.New()
	v.SetEnvPrefix("CLAWKER")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	setDefaults(v)
	return v
}

func newConfig(v *viper.Viper) *configImpl {
	return &configImpl{
		v: v,
	}
}

// NewConfig loads all clawker configuration files into a single Config.
// Precedence (highest to lowest): project config > project registry > user config > settings
func NewConfig() (Config, error) {
	c := newConfig(newViperConfig())
	if err := c.load(loadOptions{
		settingsFile:          settingsConfigFile(),
		userProjectConfigFile: userProjectConfigFile(),
		projectRegistryPath:   projectRegistryPath(),
	}); err != nil {
		return nil, err
	}
	return c, nil
}

// ReadFromString takes a YAML string and returns a Config.
// Useful for testing or constructing configs programmatically.
func ReadFromString(str string) (Config, error) {
	v := newViperConfig()
	v.SetConfigType("yaml")
	if str != "" {
		err := v.ReadConfig(strings.NewReader(str))
		if err != nil {
			return nil, fmt.Errorf("parsing config from string: %w", err)
		}
	}
	return newConfig(v), nil
}

func (c *configImpl) RequiredFirewallDomains() []string {
	return append([]string(nil), requiredFirewallDomains...)
}

type loadOptions struct {
	settingsFile          string
	userProjectConfigFile string
	projectRegistryPath   string
}

func (c *configImpl) load(opts loadOptions) error {
	files := []string{
		opts.settingsFile,
		opts.userProjectConfigFile,
		opts.projectRegistryPath,
	}

	for i, f := range files {
		c.v.SetConfigFile(f)
		var err error
		if i == 0 {
			err = c.v.ReadInConfig()
		} else {
			err = c.v.MergeInConfig()
		}
		if err != nil {
			return fmt.Errorf("loading config %s: %w", f, err)
		}
	}

	return c.mergeProjectConfig()
}

func (c *configImpl) mergeProjectConfig() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting cwd: %w", err)
	}

	projects := c.v.GetStringMap("projects")
	for key := range projects {
		root := c.v.GetString(fmt.Sprintf("projects.%s.root", key))
		if filepath.Clean(root) == filepath.Clean(cwd) {
			c.v.SetConfigFile(filepath.Join(root, "clawker.yaml"))
			if err := c.v.MergeInConfig(); err != nil {
				return fmt.Errorf("loading project config for %s: %w", key, err)
			}
			return nil
		}
	}

	return nil
}

// ConfigDir returns the clawker config directory.
func ConfigDir() string {
	if a := os.Getenv(clawkerConfigDir); a != "" {
		return a
	}
	if b := os.Getenv(xdgConfigHome); b != "" {
		return filepath.Join(b, "clawker")
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv(appData); c != "" {
			return filepath.Join(c, "clawker")
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".config", "clawker")
}

func settingsConfigFile() string {
	return filepath.Join(ConfigDir(), "settings.yaml")
}

func userProjectConfigFile() string {
	return filepath.Join(ConfigDir(), "clawker.yaml")
}

func projectRegistryPath() string {
	return filepath.Join(ConfigDir(), "projects.yaml")
}

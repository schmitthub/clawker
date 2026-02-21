package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ConfigDir returns the clawker config directory.
func ConfigDir() string {
	if a := os.Getenv(clawkerConfigDirEnv); a != "" {
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

func DataDir() string {
	if a := os.Getenv(clawkerDataDirEnv); a != "" {
		return a
	}
	if b := os.Getenv(xdgDataHome); b != "" {
		return filepath.Join(b, "clawker")
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv(localAppData); c != "" {
			return filepath.Join(c, "clawker")
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".local", "share", "clawker")
}

func StateDir() string {
	if a := os.Getenv(clawkerStateDirEnv); a != "" {
		return a
	}
	if b := os.Getenv(xdgStateHome); b != "" {
		return filepath.Join(b, "clawker")
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv(appData); c != "" {
			return filepath.Join(c, "clawker", "state")
		}
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".local", "state", "clawker")
}

func settingsConfigFile() string {
	path, err := SettingsFilePath()
	if err != nil {
		return filepath.Join(ConfigDir(), clawkerSettingsFileName)
	}
	return path
}

func userProjectConfigFile() string {
	path, err := UserProjectConfigFilePath()
	if err != nil {
		return filepath.Join(ConfigDir(), clawkerProjectConfigFileName)
	}
	return path
}

func projectRegistryPath() string {
	path, err := ProjectRegistryFilePath()
	if err != nil {
		return filepath.Join(ConfigDir(), clawkerProjectsFileName)
	}
	return path
}

func (c *configImpl) GetProjectRoot() (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.projectRootFromCurrentDir()
}

func (c *configImpl) GetProjectIgnoreFile() (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	root, err := c.projectRootFromCurrentDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, clawkerIgnoreFileName), nil
}

func (c *configImpl) projectRootFromCurrentDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting cwd: %w", err)
	}
	cwd = filepath.Clean(cwd)

	projectsRaw := c.v.Get("registry.projects")
	projectRoots := make([]string, 0)
	switch projects := projectsRaw.(type) {
	case map[string]any:
		for key := range projects {
			root := filepath.Clean(c.v.GetString(fmt.Sprintf("registry.projects.%s.root", key)))
			if root != "" {
				projectRoots = append(projectRoots, root)
			}
		}
	case []any:
		for _, rawEntry := range projects {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				continue
			}
			root, ok := entry["root"].(string)
			if !ok || root == "" {
				continue
			}
			projectRoots = append(projectRoots, filepath.Clean(root))
		}
	}

	bestMatch := ""
	for _, root := range projectRoots {
		rel, relErr := filepath.Rel(root, cwd)
		if relErr != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			if len(root) > len(bestMatch) {
				bestMatch = root
			}
		}
	}

	if bestMatch == "" {
		return "", fmt.Errorf("%w: %s", ErrNotInProject, cwd)
	}

	return bestMatch, nil
}

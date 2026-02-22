package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/schmitthub/clawker/internal/storage"
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

func (c *configImpl) GetProjectRoot() (string, error) {
	root, err := storage.ResolveProjectRoot()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNotInProject, err)
	}
	return root, nil
}

func (c *configImpl) GetProjectIgnoreFile() (string, error) {
	root, err := c.GetProjectRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, clawkerIgnoreFileName), nil
}

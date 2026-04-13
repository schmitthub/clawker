package config

import (
	"fmt"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// ConfigDir delegates to consts.ConfigDir. Kept for backward compatibility —
// new callers should import internal/consts directly.
func ConfigDir() string { return consts.ConfigDir() }

// DataDir delegates to consts.DataDir.
func DataDir() string { return consts.DataDir() }

// StateDir delegates to consts.StateDir.
func StateDir() string { return consts.StateDir() }

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
	return filepath.Join(root, consts.IgnoreFile), nil
}

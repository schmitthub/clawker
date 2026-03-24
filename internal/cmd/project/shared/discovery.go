// Package shared provides utilities shared across project subcommands.
package shared

import (
	"path/filepath"
	"strings"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/storage"
)

// HasLocalProjectConfig checks whether a project config file exists in dir.
//
// It first checks the factory-constructed config's discovered layers (which
// covers registered projects via walk-up). If no layer is found under dir —
// e.g. because the project isn't registered yet — it constructs a temporary
// store with storage.WithDirs to probe the directory using the same
// dual-placement and extension rules as the storage engine.
func HasLocalProjectConfig(cfg config.Config, dir string) bool {
	dirPrefix := filepath.Clean(dir) + string(filepath.Separator)

	// Fast path: check already-discovered layers from the factory config.
	for _, layer := range cfg.ProjectStore().Layers() {
		if isLayerUnderDir(layer, dirPrefix) {
			return true
		}
	}

	// Slow path: probe the directory directly for unregistered projects.
	// Derive filenames from the config interface so nothing is hardcoded here.
	mainFile := cfg.ProjectConfigFileName() // "clawker.yaml"
	ext := filepath.Ext(mainFile)           // ".yaml"
	base := strings.TrimSuffix(mainFile, ext)
	localFile := base + ".local" + ext // "clawker.local.yaml"

	probe, err := storage.NewStore[config.Project](
		storage.WithFilenames(mainFile, localFile),
		storage.WithDirs(dir),
		storage.WithMigrations(config.ProjectMigrations()...),
	)
	if err != nil {
		return false
	}
	return len(probe.Layers()) > 0
}

// isLayerUnderDir checks if a layer's file resides in dir (flat form) or
// in dir/.clawker/ (dir form). Layers from parent directories or the
// user-level config directory are excluded.
func isLayerUnderDir(layer storage.LayerInfo, dirPrefix string) bool {
	clean := filepath.Clean(layer.Path)

	// Flat form: dir/.clawker.yaml
	if filepath.Dir(clean)+string(filepath.Separator) == dirPrefix {
		return true
	}

	// Dir form: dir/.clawker/clawker.yaml
	parent := filepath.Dir(clean)
	if filepath.Dir(parent)+string(filepath.Separator) == dirPrefix && filepath.Base(parent) == ".clawker" {
		return true
	}

	return false
}

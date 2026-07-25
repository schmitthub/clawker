// Package shared provides utilities shared across project subcommands.
package shared

import (
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// HasLocalProjectConfig checks whether a project config file exists in dir.
//
// It first checks the factory-constructed config's discovered layers (which
// covers registered projects via walk-up). If no layer is found under dir —
// e.g. because the project isn't registered yet — it asks config to probe the
// directory (config.ProjectConfigExistsIn), which applies the same
// dual-placement and extension rules a real load would.
//
// A probe that cannot answer (an unreadable or unparseable file that even the
// migrations cannot repair) reports no local config: the callers use this to
// decide whether they are about to claim a fresh directory, and a file they
// cannot load is not a project config they can carry forward. The load error
// itself resurfaces, fully rendered, the moment any command actually reads
// that config.
func HasLocalProjectConfig(cfg config.Config, dir string) bool {
	dirPrefix := filepath.Clean(dir) + string(filepath.Separator)

	// Fast path: check already-discovered layers from the factory config.
	for _, layer := range cfg.ProjectStore().Layers() {
		if isLayerUnderDir(layer, dirPrefix) {
			return true
		}
	}

	// Slow path: probe the directory directly for unregistered projects.
	exists, err := config.ProjectConfigExistsIn(dir)
	if err != nil {
		return false
	}
	return exists
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
	if filepath.Dir(parent)+string(filepath.Separator) == dirPrefix && filepath.Base(parent) == consts.DotClawkerDir {
		return true
	}

	return false
}

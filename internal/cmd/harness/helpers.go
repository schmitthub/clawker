package harness

import (
	"path/filepath"

	"github.com/schmitthub/clawker/internal/config"
)

// deriveName returns the default registry name for a harness bundle directory:
// its base name. The dir name IS the harness name.
func deriveName(absDir string) string {
	return filepath.Base(absDir)
}

// pathKey returns the dotted clawker.yaml path where a harness's registration
// path is stored: harnesses.<name>.path. The harnesses.<name> map entry also
// carries per-harness init config (mount_projects/env/post_init/...), so the
// register/remove path targets ONLY the .path leaf and never clobbers those.
func pathKey(name string) string {
	return "harnesses." + name + ".path"
}

// entryKey returns the dotted clawker.yaml path of the whole harness entry:
// harnesses.<name>. Removing this drops the entire entry — used only when the
// entry carries no per-harness init config beyond the registration path.
func entryKey(name string) string {
	return "harnesses." + name
}

// hasInitConfig reports whether a harness entry carries per-harness init config
// beyond its registration path. When true, removing the registration must drop
// only the .path leaf so the init config survives.
func hasInitConfig(h config.HarnessConfig) bool {
	return h.MountProjects != nil ||
		len(h.EnvFile) > 0 ||
		len(h.FromEnv) > 0 ||
		len(h.Env) > 0 ||
		h.PostInit != "" ||
		h.PreRun != "" ||
		h.Config.Strategy != ""
}

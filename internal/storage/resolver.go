package storage

import (
	"os"
	"path/filepath"
	"runtime"
)

const (
	clawkerConfigDirEnv = "CLAWKER_CONFIG_DIR"
	clawkerDataDirEnv   = "CLAWKER_DATA_DIR"
	clawkerStateDirEnv  = "CLAWKER_STATE_DIR"
	clawkerCacheDirEnv  = "CLAWKER_CACHE_DIR"

	xdgConfigHome = "XDG_CONFIG_HOME"
	xdgDataHome   = "XDG_DATA_HOME"
	xdgStateHome  = "XDG_STATE_HOME"
	xdgCacheHome  = "XDG_CACHE_HOME"

	appData      = "AppData"
	localAppData = "LOCALAPPDATA"
)

// resolveDir resolves a directory path using the precedence:
// clawkerEnv > xdgEnv > platform default with defaultSuffix.
// On Windows, falls back to AppData before the POSIX-style default.
func resolveDir(clawkerEnv, xdgEnv, defaultSuffix string) string {
	if v := os.Getenv(clawkerEnv); v != "" {
		return v
	}
	if v := os.Getenv(xdgEnv); v != "" {
		return filepath.Join(v, "clawker")
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv(appData); v != "" {
			return filepath.Join(v, "clawker")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, defaultSuffix)
}

// configDir returns the clawker config directory.
// CLAWKER_CONFIG_DIR > XDG_CONFIG_HOME > %AppData%\clawker (Windows) > ~/.config/clawker
func configDir() string {
	return resolveDir(clawkerConfigDirEnv, xdgConfigHome, filepath.Join(".config", "clawker"))
}

// dataDir returns the clawker data directory.
// CLAWKER_DATA_DIR > XDG_DATA_HOME > %LOCALAPPDATA%\clawker (Windows) > ~/.local/share/clawker
func dataDir() string {
	if runtime.GOOS == "windows" {
		if v := os.Getenv(clawkerDataDirEnv); v != "" {
			return v
		}
		if v := os.Getenv(xdgDataHome); v != "" {
			return filepath.Join(v, "clawker")
		}
		if v := os.Getenv(localAppData); v != "" {
			return filepath.Join(v, "clawker")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", "clawker")
	}
	return resolveDir(clawkerDataDirEnv, xdgDataHome, filepath.Join(".local", "share", "clawker"))
}

// stateDir returns the clawker state directory.
// CLAWKER_STATE_DIR > XDG_STATE_HOME > %AppData%\clawker\state (Windows) > ~/.local/state/clawker
func stateDir() string {
	if runtime.GOOS == "windows" {
		if v := os.Getenv(clawkerStateDirEnv); v != "" {
			return v
		}
		if v := os.Getenv(xdgStateHome); v != "" {
			return filepath.Join(v, "clawker")
		}
		if v := os.Getenv(appData); v != "" {
			return filepath.Join(v, "clawker", "state")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "state", "clawker")
	}
	return resolveDir(clawkerStateDirEnv, xdgStateHome, filepath.Join(".local", "state", "clawker"))
}

// cacheDir returns the clawker cache directory.
// CLAWKER_CACHE_DIR > XDG_CACHE_HOME > %LOCALAPPDATA%\clawker\cache (Windows)
// > ~/.cache/clawker > os.TempDir()/clawker-cache
//
// Unlike config/data/state, cache falls back to os.TempDir() when no home
// directory is available — cache is transient and can live anywhere.
func cacheDir() string {
	if v := os.Getenv(clawkerCacheDirEnv); v != "" {
		return v
	}
	if v := os.Getenv(xdgCacheHome); v != "" {
		return filepath.Join(v, "clawker")
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv(localAppData); v != "" {
			return filepath.Join(v, "clawker", "cache")
		}
	}
	if home, _ := os.UserHomeDir(); home != "" {
		return filepath.Join(home, ".cache", "clawker")
	}
	return filepath.Join(os.TempDir(), "clawker-cache")
}

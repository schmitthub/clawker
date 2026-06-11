package consts

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestXDGDirResolutionPrecedence pins the resolution contract
// (CLAWKER_*_DIR > XDG_*_HOME/clawker > home fallback) that
// internal/storage delegates to. A precedence regression here would
// silently relocate every config/data/state/cache file.
func TestXDGDirResolutionPrecedence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fallback paths")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		name       string
		clawkerEnv string
		xdgEnv     string
		resolve    func() string
		homeSuffix []string
	}{
		{"config", EnvConfigDir, xdgConfigHome, ConfigDir, []string{".config", NamePrefix}},
		{"data", EnvDataDir, xdgDataHome, DataDir, []string{".local", "share", NamePrefix}},
		{"state", EnvStateDir, xdgStateHome, StateDir, []string{".local", "state", NamePrefix}},
		{"cache", EnvCacheDir, xdgCacheHome, CacheDir, []string{".cache", NamePrefix}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			explicit := t.TempDir()
			xdg := t.TempDir()

			t.Setenv(tc.clawkerEnv, explicit)
			t.Setenv(tc.xdgEnv, xdg)
			if got := tc.resolve(); got != explicit {
				t.Errorf("CLAWKER override: got %q, want %q", got, explicit)
			}

			t.Setenv(tc.clawkerEnv, "")
			if got, want := tc.resolve(), filepath.Join(xdg, NamePrefix); got != want {
				t.Errorf("XDG fallback: got %q, want %q", got, want)
			}

			t.Setenv(tc.xdgEnv, "")
			if got, want := tc.resolve(), filepath.Join(append([]string{home}, tc.homeSuffix...)...); got != want {
				t.Errorf("home fallback: got %q, want %q", got, want)
			}
		})
	}
}

// TestCacheDirTempFallback pins the cache-only TempDir fallback for
// homeless environments (cache is transient and can live anywhere;
// config/data/state intentionally have no such fallback).
func TestCacheDirTempFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fallback paths")
	}
	t.Setenv(EnvCacheDir, "")
	t.Setenv(xdgCacheHome, "")
	t.Setenv("HOME", "")

	want := filepath.Join(os.TempDir(), NamePrefix+"-cache")
	if got := CacheDir(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

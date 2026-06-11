package consts

import (
	"os"
	"runtime"
	"testing"
)

// TestEnsureAuthDirsCoversAllAccessors guards the authSubdirs contract:
// every Auth*Dir accessor's directory must be created by EnsureAuthDirs
// with the tightened 0o700 mode. An accessor whose segment is missing
// from authSubdirs would fall through to subdirPathUnder's default
// 0o755, silently loosening key-material directory perms.
func TestEnsureAuthDirsCoversAllAccessors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission modes")
	}
	t.Setenv(EnvDataDir, t.TempDir())

	if err := EnsureAuthDirs(); err != nil {
		t.Fatalf("EnsureAuthDirs: %v", err)
	}

	accessors := map[string]func() (string, error){
		"AuthCADir":      AuthCADir,
		"AuthCLIDir":     AuthCLIDir,
		"AuthTLSDir":     AuthTLSDir,
		"AuthOtelDir":    AuthOtelDir,
		"AuthCPDir":      AuthCPDir,
		"AuthInfraCADir": AuthInfraCADir,
	}
	if len(accessors) != len(authSubdirs) {
		t.Errorf("accessor table has %d entries, authSubdirs has %d — keep them in sync", len(accessors), len(authSubdirs))
	}
	for name, accessor := range accessors {
		dir, err := accessor()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("%s: stat %s: %v", name, dir, err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("%s: %s has mode %o, want 700 — its subdir segment is missing from authSubdirs", name, dir, perm)
		}
	}
}

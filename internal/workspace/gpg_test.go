package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestContainerGPGAgentPath(t *testing.T) {
	if ContainerGPGAgentPath != "/home/claude/.gnupg/S.gpg-agent" {
		t.Errorf("ContainerGPGAgentPath = %q, want %q", ContainerGPGAgentPath, "/home/claude/.gnupg/S.gpg-agent")
	}
}

func TestGetGPGExtraSocketPath_NoGPGConf(t *testing.T) {
	// If gpgconf is not available, GetGPGExtraSocketPath should return empty
	// We can't easily mock the command, so we test the expected behavior on systems with gpgconf
	socketPath := GetGPGExtraSocketPath()

	// Check if gpgconf exists
	if _, err := exec.LookPath("gpgconf"); err != nil {
		// gpgconf not available, should return empty
		if socketPath != "" {
			t.Errorf("GetGPGExtraSocketPath() = %q without gpgconf, want empty", socketPath)
		}
		return
	}

	// gpgconf is available, should return a non-empty path (unless GPG not configured)
	t.Logf("GPG extra socket path: %q", socketPath)
}

func TestIsGPGAgentAvailable_NoGPGConf(t *testing.T) {
	// Without gpgconf or with invalid socket, should return false
	if runtime.GOOS == "windows" {
		t.Skip("GPG agent forwarding not supported on Windows")
	}

	// We can't easily force GetGPGExtraSocketPath to return empty,
	// so we just test that the function doesn't panic
	available := IsGPGAgentAvailable()
	t.Logf("IsGPGAgentAvailable() = %v", available)
}

func TestIsGPGAgentAvailable_Linux_NonexistentSocket(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Test only applies to Linux")
	}

	// On Linux, if the socket doesn't exist, should return false
	// This test relies on gpgconf returning a path that may not exist
	socketPath := GetGPGExtraSocketPath()
	if socketPath == "" {
		t.Skip("gpgconf not available or GPG not configured")
	}

	// If socket doesn't exist, IsGPGAgentAvailable should return false
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		if IsGPGAgentAvailable() {
			t.Error("IsGPGAgentAvailable() = true with nonexistent socket, want false")
		}
	}
}

func TestUseGPGAgentProxy(t *testing.T) {
	if runtime.GOOS != "darwin" {
		// On non-macOS, should always be false
		if UseGPGAgentProxy() {
			t.Error("UseGPGAgentProxy() = true on non-macOS, want false")
		}
		return
	}

	// On macOS, depends on IsGPGAgentAvailable
	// If gpgconf is available, it should return based on socket availability
	available := IsGPGAgentAvailable()
	proxyNeeded := UseGPGAgentProxy()
	if proxyNeeded != available {
		t.Errorf("UseGPGAgentProxy() = %v, IsGPGAgentAvailable() = %v, want equal", proxyNeeded, available)
	}
}

func TestGetGPGAgentMounts_NoSocket(t *testing.T) {
	if runtime.GOOS == "darwin" {
		// On macOS, should return nil (uses host proxy instead)
		mounts := GetGPGAgentMounts()
		if mounts != nil {
			t.Errorf("GetGPGAgentMounts() on macOS returned %v, want nil (uses host proxy)", mounts)
		}
		return
	}

	// On Linux without a valid socket, should return nil
	socketPath := GetGPGExtraSocketPath()
	if socketPath == "" {
		mounts := GetGPGAgentMounts()
		if mounts != nil {
			t.Errorf("GetGPGAgentMounts() with no GPG socket returned %v, want nil", mounts)
		}
		return
	}

	// If socket path exists but socket doesn't, should return nil
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		mounts := GetGPGAgentMounts()
		if mounts != nil {
			t.Errorf("GetGPGAgentMounts() with nonexistent socket returned %v, want nil", mounts)
		}
	}
}

func TestGetGPGAgentMounts_Linux_ValidSocket(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Test only applies to Linux")
	}

	socketPath := GetGPGExtraSocketPath()
	if socketPath == "" {
		t.Skip("gpgconf not available or GPG not configured")
	}

	// Create a temp file to simulate GPG socket
	tmpDir := t.TempDir()
	testSocketPath := filepath.Join(tmpDir, "S.gpg-agent.extra")
	f, err := os.Create(testSocketPath)
	if err != nil {
		t.Fatalf("failed to create temp socket file: %v", err)
	}
	f.Close()

	// We can't easily inject a custom socket path into GetGPGAgentMounts,
	// but we can test that the function returns the correct structure
	// when the actual socket exists
	if _, err := os.Stat(socketPath); err == nil {
		mounts := GetGPGAgentMounts()
		if len(mounts) != 1 {
			t.Fatalf("GetGPGAgentMounts() returned %d mounts, want 1", len(mounts))
		}

		m := mounts[0]
		if m.Source != socketPath {
			t.Errorf("mount.Source = %q, want %q", m.Source, socketPath)
		}
		if m.Target != ContainerGPGAgentPath {
			t.Errorf("mount.Target = %q, want %q", m.Target, ContainerGPGAgentPath)
		}
	}
}

func TestGetGPGAgentMounts_MacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Test only applies to macOS")
	}

	// On macOS, should return nil (uses host proxy instead)
	mounts := GetGPGAgentMounts()
	if mounts != nil {
		t.Errorf("GetGPGAgentMounts() on macOS returned %v, want nil (uses host proxy)", mounts)
	}
}

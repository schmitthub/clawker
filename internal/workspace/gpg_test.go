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

func TestIsGPGAgentAvailable_NonexistentSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GPG agent forwarding not supported on Windows")
	}

	// If the socket doesn't exist, should return false
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
	// UseGPGAgentProxy now always returns false - socket mounting works on Docker Desktop
	if UseGPGAgentProxy() {
		t.Error("UseGPGAgentProxy() = true, want false (socket mounting is preferred)")
	}
}

func TestGetGPGAgentMounts_NoSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GPG agent forwarding not supported on Windows")
	}

	// Without a valid socket, should return nil
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

func TestGetGPGAgentMounts_ValidSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GPG agent forwarding not supported on Windows")
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

	socketPath := GetGPGExtraSocketPath()
	if socketPath == "" {
		t.Skip("gpgconf not available or GPG not configured")
	}

	// If socket exists, macOS should now return mounts (socket mounting works on Docker Desktop)
	if _, err := os.Stat(socketPath); err == nil {
		mounts := GetGPGAgentMounts()
		if len(mounts) != 1 {
			t.Fatalf("GetGPGAgentMounts() on macOS returned %d mounts, want 1 (socket mounting is preferred)", len(mounts))
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

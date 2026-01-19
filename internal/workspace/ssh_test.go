package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestContainerSSHAgentPath(t *testing.T) {
	if ContainerSSHAgentPath != "/tmp/ssh-agent.sock" {
		t.Errorf("ContainerSSHAgentPath = %q, want %q", ContainerSSHAgentPath, "/tmp/ssh-agent.sock")
	}
}

func TestIsSSHAgentAvailable_NoSSHAuthSock(t *testing.T) {
	// Unset SSH_AUTH_SOCK
	t.Setenv("SSH_AUTH_SOCK", "")

	// Without SSH_AUTH_SOCK, agent should not be available
	// This behavior is consistent across all platforms
	if IsSSHAgentAvailable() {
		t.Error("IsSSHAgentAvailable() = true with empty SSH_AUTH_SOCK, want false")
	}
}

func TestIsSSHAgentAvailable_WithSSHAuthSock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SSH agent forwarding not supported on Windows")
	}

	// Create a temp file to simulate SSH socket
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "agent.sock")
	f, err := os.Create(sockPath)
	if err != nil {
		t.Fatalf("failed to create temp socket file: %v", err)
	}
	f.Close()

	t.Setenv("SSH_AUTH_SOCK", sockPath)

	// On macOS, availability depends on just SSH_AUTH_SOCK being set
	// On Linux, it also checks if the socket exists
	available := IsSSHAgentAvailable()

	if runtime.GOOS == "darwin" {
		// macOS just checks if SSH_AUTH_SOCK is set
		if !available {
			t.Error("IsSSHAgentAvailable() = false on macOS with SSH_AUTH_SOCK set, want true")
		}
	} else if runtime.GOOS == "linux" {
		// Linux also checks if socket exists - we created a temp file so it should work
		if !available {
			t.Error("IsSSHAgentAvailable() = false on Linux with valid socket, want true")
		}
	}
}

func TestIsSSHAgentAvailable_Linux_NonexistentSocket(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Test only applies to Linux")
	}

	// Set to a nonexistent path
	t.Setenv("SSH_AUTH_SOCK", "/nonexistent/path/to/socket")

	if IsSSHAgentAvailable() {
		t.Error("IsSSHAgentAvailable() = true on Linux with nonexistent socket, want false")
	}
}

func TestUseSSHAgentProxy(t *testing.T) {
	if runtime.GOOS != "darwin" {
		// On non-macOS, should always be false
		t.Setenv("SSH_AUTH_SOCK", "/some/socket")
		if UseSSHAgentProxy() {
			t.Error("UseSSHAgentProxy() = true on non-macOS, want false")
		}
		return
	}

	// On macOS, depends on IsSSHAgentAvailable
	t.Setenv("SSH_AUTH_SOCK", "")
	if UseSSHAgentProxy() {
		t.Error("UseSSHAgentProxy() = true on macOS without SSH agent, want false")
	}

	t.Setenv("SSH_AUTH_SOCK", "/some/socket")
	if !UseSSHAgentProxy() {
		t.Error("UseSSHAgentProxy() = false on macOS with SSH agent, want true")
	}
}

func TestGetSSHAgentMounts_NoSSHAuthSock(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")

	mounts := GetSSHAgentMounts()
	if mounts != nil {
		t.Errorf("GetSSHAgentMounts() with no SSH_AUTH_SOCK returned %v, want nil", mounts)
	}
}

func TestGetSSHAgentMounts_Linux_ValidSocket(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Test only applies to Linux")
	}

	// Create a temp file to simulate SSH socket
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "agent.sock")
	f, err := os.Create(sockPath)
	if err != nil {
		t.Fatalf("failed to create temp socket file: %v", err)
	}
	f.Close()

	t.Setenv("SSH_AUTH_SOCK", sockPath)

	mounts := GetSSHAgentMounts()
	if len(mounts) != 1 {
		t.Fatalf("GetSSHAgentMounts() returned %d mounts, want 1", len(mounts))
	}

	m := mounts[0]
	if m.Source != sockPath {
		t.Errorf("mount.Source = %q, want %q", m.Source, sockPath)
	}
	if m.Target != ContainerSSHAgentPath {
		t.Errorf("mount.Target = %q, want %q", m.Target, ContainerSSHAgentPath)
	}
}

func TestGetSSHAgentMounts_MacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Test only applies to macOS")
	}

	t.Setenv("SSH_AUTH_SOCK", "/some/socket")

	// On macOS, should return nil (uses host proxy instead)
	mounts := GetSSHAgentMounts()
	if mounts != nil {
		t.Errorf("GetSSHAgentMounts() on macOS returned %v, want nil (uses host proxy)", mounts)
	}
}

func TestGetSSHAgentEnvVar_NoSSHAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")

	envVar := GetSSHAgentEnvVar()
	if envVar != "" {
		t.Errorf("GetSSHAgentEnvVar() with no SSH agent = %q, want empty", envVar)
	}
}

func TestGetSSHAgentEnvVar_MacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Test only applies to macOS")
	}

	t.Setenv("SSH_AUTH_SOCK", "/some/socket")

	// On macOS, should return empty (entrypoint sets it)
	envVar := GetSSHAgentEnvVar()
	if envVar != "" {
		t.Errorf("GetSSHAgentEnvVar() on macOS = %q, want empty (entrypoint handles it)", envVar)
	}
}

func TestGetSSHAgentEnvVar_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Test only applies to Linux")
	}

	// Create a temp file to simulate SSH socket
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "agent.sock")
	f, err := os.Create(sockPath)
	if err != nil {
		t.Fatalf("failed to create temp socket file: %v", err)
	}
	f.Close()

	t.Setenv("SSH_AUTH_SOCK", sockPath)

	envVar := GetSSHAgentEnvVar()
	if envVar != ContainerSSHAgentPath {
		t.Errorf("GetSSHAgentEnvVar() on Linux = %q, want %q", envVar, ContainerSSHAgentPath)
	}
}

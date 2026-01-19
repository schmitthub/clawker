package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostGitConfigStagingPath(t *testing.T) {
	if HostGitConfigStagingPath != "/tmp/host-gitconfig" {
		t.Errorf("HostGitConfigStagingPath = %q, want %q", HostGitConfigStagingPath, "/tmp/host-gitconfig")
	}
}

func TestGitConfigExists_FileExists(t *testing.T) {
	// Create a temp home directory with .gitconfig
	tmpHome := t.TempDir()
	gitconfigPath := filepath.Join(tmpHome, ".gitconfig")
	if err := os.WriteFile(gitconfigPath, []byte("[user]\nname = Test\n"), 0644); err != nil {
		t.Fatalf("failed to create temp gitconfig: %v", err)
	}

	// Override HOME env var
	t.Setenv("HOME", tmpHome)

	if !GitConfigExists() {
		t.Error("GitConfigExists() = false with valid gitconfig, want true")
	}
}

func TestGitConfigExists_NoFile(t *testing.T) {
	// Create a temp home directory without .gitconfig
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	if GitConfigExists() {
		t.Error("GitConfigExists() = true with no gitconfig, want false")
	}
}

func TestGitConfigExists_Directory(t *testing.T) {
	// Create a temp home directory where .gitconfig is a directory
	tmpHome := t.TempDir()
	gitconfigPath := filepath.Join(tmpHome, ".gitconfig")
	if err := os.Mkdir(gitconfigPath, 0755); err != nil {
		t.Fatalf("failed to create gitconfig directory: %v", err)
	}

	t.Setenv("HOME", tmpHome)

	if GitConfigExists() {
		t.Error("GitConfigExists() = true when .gitconfig is a directory, want false")
	}
}

func TestGetGitConfigMount_FileExists(t *testing.T) {
	// Create a temp home directory with .gitconfig
	tmpHome := t.TempDir()
	gitconfigPath := filepath.Join(tmpHome, ".gitconfig")
	if err := os.WriteFile(gitconfigPath, []byte("[user]\nname = Test\n"), 0644); err != nil {
		t.Fatalf("failed to create temp gitconfig: %v", err)
	}

	t.Setenv("HOME", tmpHome)

	mounts := GetGitConfigMount()
	if len(mounts) != 1 {
		t.Fatalf("GetGitConfigMount() returned %d mounts, want 1", len(mounts))
	}

	m := mounts[0]
	if m.Source != gitconfigPath {
		t.Errorf("mount.Source = %q, want %q", m.Source, gitconfigPath)
	}
	if m.Target != HostGitConfigStagingPath {
		t.Errorf("mount.Target = %q, want %q", m.Target, HostGitConfigStagingPath)
	}
	if !m.ReadOnly {
		t.Error("mount.ReadOnly = false, want true")
	}
}

func TestGetGitConfigMount_NoFile(t *testing.T) {
	// Create a temp home directory without .gitconfig
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	mounts := GetGitConfigMount()
	if mounts != nil {
		t.Errorf("GetGitConfigMount() with no gitconfig = %v, want nil", mounts)
	}
}

func TestGetGitConfigMount_Directory(t *testing.T) {
	// Create a temp home directory where .gitconfig is a directory
	tmpHome := t.TempDir()
	gitconfigPath := filepath.Join(tmpHome, ".gitconfig")
	if err := os.Mkdir(gitconfigPath, 0755); err != nil {
		t.Fatalf("failed to create gitconfig directory: %v", err)
	}

	t.Setenv("HOME", tmpHome)

	mounts := GetGitConfigMount()
	if mounts != nil {
		t.Errorf("GetGitConfigMount() when .gitconfig is directory = %v, want nil", mounts)
	}
}

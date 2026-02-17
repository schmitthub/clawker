package workspace

import (
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/moby/moby/api/types/mount"
)

func TestBindStrategy_GetMounts(t *testing.T) {
	t.Run("returns bind mount without patterns", func(t *testing.T) {
		s := NewBindStrategy(Config{
			HostPath:   "/host/path",
			RemotePath: "/workspace",
		})

		mounts, err := s.GetMounts()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(mounts) != 1 {
			t.Fatalf("expected 1 mount, got %d", len(mounts))
		}
		if mounts[0].Type != mount.TypeBind {
			t.Errorf("mount.Type = %v, want %v", mounts[0].Type, mount.TypeBind)
		}
		if mounts[0].Source != "/host/path" {
			t.Errorf("mount.Source = %q, want %q", mounts[0].Source, "/host/path")
		}
		if mounts[0].Target != "/workspace" {
			t.Errorf("mount.Target = %q, want %q", mounts[0].Target, "/workspace")
		}
	})

	t.Run("adds tmpfs overlays for matching directories", func(t *testing.T) {
		hostDir := t.TempDir()
		// Create directories that should be masked
		for _, d := range []string{"node_modules/foo", "dist", ".venv/lib"} {
			if err := os.MkdirAll(filepath.Join(hostDir, d), 0755); err != nil {
				t.Fatal(err)
			}
		}
		// Create a directory that should NOT be masked
		if err := os.MkdirAll(filepath.Join(hostDir, "src"), 0755); err != nil {
			t.Fatal(err)
		}

		s := NewBindStrategy(Config{
			HostPath:       hostDir,
			RemotePath:     "/workspace",
			IgnorePatterns: []string{"node_modules/", "dist/", ".venv/"},
		})

		mounts, err := s.GetMounts()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have bind mount + 3 tmpfs overlays
		if len(mounts) != 4 {
			t.Fatalf("expected 4 mounts (1 bind + 3 tmpfs), got %d", len(mounts))
		}

		// First mount should be the bind mount
		if mounts[0].Type != mount.TypeBind {
			t.Errorf("first mount should be bind, got %v", mounts[0].Type)
		}

		// Remaining should be tmpfs overlays
		tmpfsTargets := make(map[string]bool)
		for _, m := range mounts[1:] {
			if m.Type != mount.TypeTmpfs {
				t.Errorf("expected tmpfs mount, got %v", m.Type)
			}
			tmpfsTargets[m.Target] = true
		}

		for _, dir := range []string{"node_modules", "dist", ".venv"} {
			target := path.Join("/workspace", dir)
			if !tmpfsTargets[target] {
				t.Errorf("expected tmpfs overlay for %q, not found in %v", target, tmpfsTargets)
			}
		}
	})

	t.Run("no overlays when patterns are empty", func(t *testing.T) {
		hostDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(hostDir, "node_modules"), 0755); err != nil {
			t.Fatal(err)
		}

		s := NewBindStrategy(Config{
			HostPath:       hostDir,
			RemotePath:     "/workspace",
			IgnorePatterns: []string{},
		})

		mounts, err := s.GetMounts()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(mounts) != 1 {
			t.Errorf("expected 1 mount (bind only), got %d", len(mounts))
		}
	})

	t.Run("does not mask .git directory", func(t *testing.T) {
		hostDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(hostDir, ".git/objects"), 0755); err != nil {
			t.Fatal(err)
		}

		s := NewBindStrategy(Config{
			HostPath:       hostDir,
			RemotePath:     "/workspace",
			IgnorePatterns: []string{".git/"},
		})

		mounts, err := s.GetMounts()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should only have the primary bind mount — .git is never masked
		if len(mounts) != 1 {
			t.Errorf("expected 1 mount (bind only, .git never masked), got %d", len(mounts))
		}
	})

	t.Run("returns error when host path does not exist", func(t *testing.T) {
		s := NewBindStrategy(Config{
			HostPath:       "/nonexistent/path/that/does/not/exist",
			RemotePath:     "/workspace",
			IgnorePatterns: []string{"node_modules/"},
		})

		_, err := s.GetMounts()
		if err == nil {
			t.Error("expected error for nonexistent host path with patterns, got nil")
		}
	})
}

func TestSnapshotStrategy_GetMounts(t *testing.T) {
	t.Run("returns volume mount with nil error", func(t *testing.T) {
		s := &SnapshotStrategy{
			config: Config{
				RemotePath: "/workspace",
			},
			volumeName: "clawker.test.dev-workspace",
		}

		mounts, err := s.GetMounts()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(mounts) != 1 {
			t.Fatalf("expected 1 mount, got %d", len(mounts))
		}
		if mounts[0].Type != mount.TypeVolume {
			t.Errorf("mount.Type = %v, want %v", mounts[0].Type, mount.TypeVolume)
		}
		if mounts[0].Source != "clawker.test.dev-workspace" {
			t.Errorf("mount.Source = %q, want %q", mounts[0].Source, "clawker.test.dev-workspace")
		}
	})
}

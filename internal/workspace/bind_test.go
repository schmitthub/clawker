package workspace

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
)

func assertTmpfsOwnedByContainer(t *testing.T, m mount.Mount) {
	t.Helper()

	if m.TmpfsOptions == nil {
		t.Fatal("expected tmpfs options, got nil")
	}
	if m.TmpfsOptions.Mode != 0o755 {
		t.Fatalf("tmpfs mode = %o, want %o", m.TmpfsOptions.Mode, 0o755)
	}

	got := map[string]string{}
	for _, opt := range m.TmpfsOptions.Options {
		if len(opt) == 2 {
			got[opt[0]] = opt[1]
		}
	}

	if got["uid"] != strconv.Itoa(config.ContainerUID) {
		t.Fatalf("tmpfs uid option = %q, want %q", got["uid"], strconv.Itoa(config.ContainerUID))
	}
	if got["gid"] != strconv.Itoa(config.ContainerGID) {
		t.Fatalf("tmpfs gid option = %q, want %q", got["gid"], strconv.Itoa(config.ContainerGID))
	}
}

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
			assertTmpfsOwnedByContainer(t, m)
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

	t.Run("adds overlay for directory pattern that does not yet exist", func(t *testing.T) {
		hostDir := t.TempDir()

		s := NewBindStrategy(Config{
			HostPath:       hostDir,
			RemotePath:     "/workspace",
			IgnorePatterns: []string{"node_modules/"},
		})

		mounts, err := s.GetMounts()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(mounts) != 2 {
			t.Fatalf("expected 2 mounts (1 bind + 1 tmpfs), got %d", len(mounts))
		}

		if mounts[1].Type != mount.TypeTmpfs {
			t.Fatalf("expected second mount to be tmpfs, got %v", mounts[1].Type)
		}
		assertTmpfsOwnedByContainer(t, mounts[1])

		wantTarget := path.Join("/workspace", "node_modules")
		if mounts[1].Target != wantTarget {
			t.Errorf("tmpfs target = %q, want %q", mounts[1].Target, wantTarget)
		}
	})

	t.Run("dedupes overlays from static and discovered directories", func(t *testing.T) {
		hostDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(hostDir, "node_modules/cache"), 0755); err != nil {
			t.Fatal(err)
		}

		s := NewBindStrategy(Config{
			HostPath:       hostDir,
			RemotePath:     "/workspace",
			IgnorePatterns: []string{"node_modules/"},
		})

		mounts, err := s.GetMounts()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		tmpfsCount := 0
		wantTarget := path.Join("/workspace", "node_modules")
		for _, m := range mounts {
			if m.Type == mount.TypeTmpfs && m.Target == wantTarget {
				tmpfsCount++
			}
		}

		if tmpfsCount != 1 {
			t.Fatalf("expected exactly one tmpfs mount for %q, got %d (mounts=%v)", wantTarget, tmpfsCount, mounts)
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

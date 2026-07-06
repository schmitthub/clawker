package workspace

import (
	"strings"
	"testing"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/consts"
)

func TestGetShareVolumeMount(t *testing.T) {
	hostPath := "/tmp/test-clawker-share"
	m := GetShareVolumeMount(hostPath)

	if m.Type != mount.TypeBind {
		t.Errorf("Type = %v, want %v", m.Type, mount.TypeBind)
	}

	if m.Source != hostPath {
		t.Errorf("Source = %q, want %q", m.Source, hostPath)
	}

	if m.Target != ShareStagingPath {
		t.Errorf("Target = %q, want %q", m.Target, ShareStagingPath)
	}

	if !m.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
}

func TestGetHostStateMount(t *testing.T) {
	hostPath := "/home/alice/.claude/projects"
	m, err := GetHostStateMount(hostPath, ".claude/projects")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Type != mount.TypeBind {
		t.Errorf("Type = %v, want %v", m.Type, mount.TypeBind)
	}
	if m.Source != hostPath {
		t.Errorf("Source = %q, want %q", m.Source, hostPath)
	}
	want := consts.ContainerHomeDir + "/.claude/projects"
	if m.Target != want {
		t.Errorf("Target = %q, want %q", m.Target, want)
	}
	// RW is intentional — auto-memory and session jsonls are written from inside the container.
	if m.ReadOnly {
		t.Error("ReadOnly = true, want false (auto-memory needs RW)")
	}
}

func TestGetHostStateMount_RejectsRelativePath(t *testing.T) {
	_, err := GetHostStateMount("relative/path", ".claude/projects")
	if err == nil {
		t.Fatal("GetHostStateMount() error = nil, want error about absolute path")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("error message = %q, should mention 'must be absolute'", err.Error())
	}
}

func TestGetHostStateMount_RejectsEmptyPath(t *testing.T) {
	_, err := GetHostStateMount("", ".claude/projects")
	if err == nil {
		t.Fatal("GetHostStateMount() error = nil, want error about absolute path")
	}
}

func TestShareConstants(t *testing.T) {
	if SharePurpose != "share" {
		t.Errorf("SharePurpose = %q, want %q", SharePurpose, "share")
	}

	if ShareStagingPath != "/home/claude/.clawker-share" {
		t.Errorf("ShareStagingPath = %q, want %q", ShareStagingPath, "/home/claude/.clawker-share")
	}
}

func TestConfigVolumeResult(t *testing.T) {
	// Zero value: nothing created.
	var result ConfigVolumeResult
	if result.CreatedByName["config"] {
		t.Error("CreatedByName zero value should report false")
	}
	if result.HistoryCreated {
		t.Error("HistoryCreated zero value should be false")
	}
}

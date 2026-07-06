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

// TestGetConfigVolumeMounts_InfraVolumes pins the clawker infra mounts: the
// history volume at /commandhistory and the lifecycle volume at
// $HOME/.clawker — the latter is what keeps the post-init marker alive
// across container recreation (marker lifetime must match the config
// volumes post_init mutates).
func TestGetConfigVolumeMounts_InfraVolumes(t *testing.T) {
	mounts, err := GetConfigVolumeMounts("proj", "agent", nil)
	if err != nil {
		t.Fatal(err)
	}

	byTarget := map[string]mount.Mount{}
	for _, m := range mounts {
		byTarget[m.Target] = m
	}

	clawkerTarget := consts.ContainerHomeDir + "/" + consts.DotClawkerDir
	cm, ok := byTarget[clawkerTarget]
	if !ok {
		t.Fatalf("no mount at %s — post-init marker would die with the container", clawkerTarget)
	}
	if cm.Type != mount.TypeVolume {
		t.Errorf("clawker mount Type = %v, want volume", cm.Type)
	}
	if !strings.HasSuffix(cm.Source, "-"+consts.VolumePurposeClawker) {
		t.Errorf("clawker mount Source = %q, want -%s suffix", cm.Source, consts.VolumePurposeClawker)
	}

	hm, ok := byTarget["/commandhistory"]
	if !ok {
		t.Fatal("no history mount at /commandhistory")
	}
	if !strings.HasSuffix(hm.Source, "-"+consts.VolumePurposeHistory) {
		t.Errorf("history mount Source = %q, want -%s suffix", hm.Source, consts.VolumePurposeHistory)
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

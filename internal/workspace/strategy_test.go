package workspace

import (
	"testing"

	"github.com/moby/moby/api/types/mount"
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

func TestShareConstants(t *testing.T) {
	if SharePurpose != "share" {
		t.Errorf("SharePurpose = %q, want %q", SharePurpose, "share")
	}

	if ShareStagingPath != "/home/claude/.clawker-share" {
		t.Errorf("ShareStagingPath = %q, want %q", ShareStagingPath, "/home/claude/.clawker-share")
	}
}

func TestConfigVolumeResult(t *testing.T) {
	// ConfigVolumeResult should exist as a struct with ConfigCreated and HistoryCreated fields.
	var result ConfigVolumeResult

	// Zero value should be false for both fields.
	if result.ConfigCreated {
		t.Error("ConfigCreated zero value should be false")
	}
	if result.HistoryCreated {
		t.Error("HistoryCreated zero value should be false")
	}

	// Set fields and verify.
	result.ConfigCreated = true
	result.HistoryCreated = true
	if !result.ConfigCreated {
		t.Error("ConfigCreated should be true after setting")
	}
	if !result.HistoryCreated {
		t.Error("HistoryCreated should be true after setting")
	}
}

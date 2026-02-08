package workspace

import (
	"testing"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/docker"
)

func TestGetGlobalsVolumeMount(t *testing.T) {
	m := GetGlobalsVolumeMount()

	if m.Type != mount.TypeVolume {
		t.Errorf("Type = %v, want %v", m.Type, mount.TypeVolume)
	}

	wantSource := docker.GlobalVolumeName(GlobalsPurpose)
	if m.Source != wantSource {
		t.Errorf("Source = %q, want %q", m.Source, wantSource)
	}

	if m.Target != GlobalsStagingPath {
		t.Errorf("Target = %q, want %q", m.Target, GlobalsStagingPath)
	}
}

func TestGlobalsConstants(t *testing.T) {
	if GlobalsPurpose != "globals" {
		t.Errorf("GlobalsPurpose = %q, want %q", GlobalsPurpose, "globals")
	}

	if GlobalsStagingPath != "/home/claude/.clawker-globals" {
		t.Errorf("GlobalsStagingPath = %q, want %q", GlobalsStagingPath, "/home/claude/.clawker-globals")
	}
}

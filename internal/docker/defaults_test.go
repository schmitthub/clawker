package docker

import "testing"

func TestDefaultImageTag(t *testing.T) {
	expected := "clawker-default:latest"
	if DefaultImageTag != expected {
		t.Errorf("DefaultImageTag = %q, want %q", DefaultImageTag, expected)
	}
}

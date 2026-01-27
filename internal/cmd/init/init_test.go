package init

import (
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
)

func TestNewCmdInit(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdInit(f)

	// Check command use (user-only setup, no args)
	if cmd.Use != "init" {
		t.Errorf("expected Use 'init', got '%s'", cmd.Use)
	}

	// Check yes flag exists
	yesFlag := cmd.Flags().Lookup("yes")
	if yesFlag == nil {
		t.Error("expected --yes flag to exist")
	}
	if yesFlag.Shorthand != "y" {
		t.Errorf("expected --yes shorthand 'y', got '%s'", yesFlag.Shorthand)
	}

	// Check that args are not accepted (NoArgs)
	if cmd.Args == nil {
		t.Error("expected Args to be set (NoArgs)")
	}
}

func TestDefaultImageTag(t *testing.T) {
	// Verify the default image tag constant from cmdutil
	expected := "clawker-default:latest"
	if cmdutil.DefaultImageTag != expected {
		t.Errorf("expected DefaultImageTag %q, got %q", expected, cmdutil.DefaultImageTag)
	}
}

func TestFlavorOptions(t *testing.T) {
	// Verify flavor options are defined
	flavorOptions := cmdutil.DefaultFlavorOptions()
	if len(flavorOptions) == 0 {
		t.Error("expected flavor options to be defined")
	}

	// Check that bookworm (recommended) is first
	if flavorOptions[0].Name != "bookworm" {
		t.Errorf("expected first flavor option to be 'bookworm', got %q", flavorOptions[0].Name)
	}

	// Check all options have name and description
	for i, opt := range flavorOptions {
		if opt.Name == "" {
			t.Errorf("flavor option %d has empty name", i)
		}
		if opt.Description == "" {
			t.Errorf("flavor option %d has empty description", i)
		}
	}

	// Verify expected flavors exist
	expectedFlavors := []string{"bookworm", "trixie", "alpine3.22", "alpine3.23"}
	for _, expected := range expectedFlavors {
		found := false
		for _, opt := range flavorOptions {
			if opt.Name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected flavor %q to exist in flavor options", expected)
		}
	}
}

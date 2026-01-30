package init

import (
	"context"
	"testing"

	intbuild "github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestNewCmdInit(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *InitOptions
	cmd := NewCmdInit(f, func(_ context.Context, opts *InitOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotOpts == nil {
		t.Fatal("expected runF to be called")
	}

	if gotOpts.IOStreams != tio.IOStreams {
		t.Error("expected IOStreams to be set from factory")
	}

	if gotOpts.Yes {
		t.Error("expected Yes to be false by default")
	}
}

func TestNewCmdInit_YesFlag(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *InitOptions
	cmd := NewCmdInit(f, func(_ context.Context, opts *InitOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--yes"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotOpts == nil {
		t.Fatal("expected runF to be called")
	}

	if !gotOpts.Yes {
		t.Error("expected Yes to be true when --yes flag is set")
	}
}

func TestDefaultImageTag(t *testing.T) {
	// Verify the default image tag constant from cmdutil
	expected := "clawker-default:latest"
	if intbuild.DefaultImageTag != expected {
		t.Errorf("expected DefaultImageTag %q, got %q", expected, intbuild.DefaultImageTag)
	}
}

func TestFlavorOptions(t *testing.T) {
	// Verify flavor options are defined
	flavorOptions := intbuild.DefaultFlavorOptions()
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

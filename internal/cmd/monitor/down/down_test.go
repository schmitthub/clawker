package down

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestNewCmdDown(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *DownOptions
	cmd := NewCmdDown(f, func(_ context.Context, opts *DownOptions) error {
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
	if gotOpts.Volumes {
		t.Error("expected Volumes to default to false")
	}
}

func TestNewCmdDown_VolumesFlag(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *DownOptions
	cmd := NewCmdDown(f, func(_ context.Context, opts *DownOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--volumes"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotOpts == nil {
		t.Fatal("expected runF to be called")
	}
	if !gotOpts.Volumes {
		t.Error("expected Volumes to be true when --volumes flag is set")
	}
}

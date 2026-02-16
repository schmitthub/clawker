package init

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
)

func TestNewCmdInit(t *testing.T) {
	tio := iostreamstest.New()
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
	if gotOpts.Force {
		t.Error("expected Force to default to false")
	}
}

func TestNewCmdInit_ForceFlag(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *InitOptions
	cmd := NewCmdInit(f, func(_ context.Context, opts *InitOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--force"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotOpts == nil {
		t.Fatal("expected runF to be called")
	}
	if !gotOpts.Force {
		t.Error("expected Force to be true when --force flag is set")
	}
}

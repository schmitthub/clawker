package check

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
)

func TestNewCmdCheck(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *CheckOptions
	cmd := NewCmdCheck(f, func(_ context.Context, opts *CheckOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotOpts == nil {
		t.Fatal("runF was not called")
	}

	if gotOpts.IOStreams != tio.IOStreams {
		t.Error("expected IOStreams to be set from factory")
	}
}

func TestNewCmdCheck_metadata(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	cmd := NewCmdCheck(f, nil)

	if cmd.Use != "check" {
		t.Errorf("expected Use 'check', got '%s'", cmd.Use)
	}

	if cmd.Short == "" {
		t.Error("expected Short to be set")
	}

	if cmd.Example == "" {
		t.Error("expected Example to be set")
	}
}

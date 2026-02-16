package status

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
)

func TestNewCmdStatus(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *StatusOptions
	cmd := NewCmdStatus(f, func(_ context.Context, opts *StatusOptions) error {
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
}

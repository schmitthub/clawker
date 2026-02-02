package check

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestNewCmdCheck(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams, WorkDir: func() string { return "/tmp/test" }}

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

	if gotOpts.WorkDir() != "/tmp/test" {
		t.Errorf("expected WorkDir '/tmp/test', got '%s'", gotOpts.WorkDir())
	}
}

func TestNewCmdCheck_metadata(t *testing.T) {
	tio := iostreams.NewTestIOStreams()
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

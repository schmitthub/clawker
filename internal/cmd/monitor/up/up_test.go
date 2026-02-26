package up

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
)

func TestNewCmdUp(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
	}

	var gotOpts *UpOptions
	cmd := NewCmdUp(f, func(_ context.Context, opts *UpOptions) error {
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
	if gotOpts.IOStreams != tio {
		t.Error("expected IOStreams to be set from factory")
	}
	if !gotOpts.Detach {
		t.Error("expected Detach to default to true")
	}
}

func TestNewCmdUp_DetachFalse(t *testing.T) {
	tio, _, _, _ := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
	}

	var gotOpts *UpOptions
	cmd := NewCmdUp(f, func(_ context.Context, opts *UpOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--detach=false"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotOpts == nil {
		t.Fatal("expected runF to be called")
	}
	if gotOpts.Detach {
		t.Error("expected Detach to be false when --detach=false")
	}
}

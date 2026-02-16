package generate

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
)

func TestNewCmdGenerate(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *GenerateOptions
	cmd := NewCmdGenerate(f, func(_ context.Context, opts *GenerateOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"latest", "2.1"})
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

	if len(gotOpts.Versions) != 2 || gotOpts.Versions[0] != "latest" || gotOpts.Versions[1] != "2.1" {
		t.Errorf("expected Versions [latest, 2.1], got %v", gotOpts.Versions)
	}

	if gotOpts.SkipFetch {
		t.Error("expected SkipFetch to be false by default")
	}

	if !gotOpts.Cleanup {
		t.Error("expected Cleanup to be true by default")
	}

	if gotOpts.OutputDir != "" {
		t.Errorf("expected OutputDir to be empty, got %q", gotOpts.OutputDir)
	}
}

func TestNewCmdGenerate_NoArgs(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *GenerateOptions
	cmd := NewCmdGenerate(f, func(_ context.Context, opts *GenerateOptions) error {
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

	if len(gotOpts.Versions) != 0 {
		t.Errorf("expected empty Versions, got %v", gotOpts.Versions)
	}
}

func TestNewCmdGenerate_Flags(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{IOStreams: tio.IOStreams}

	var gotOpts *GenerateOptions
	cmd := NewCmdGenerate(f, func(_ context.Context, opts *GenerateOptions) error {
		gotOpts = opts
		return nil
	})

	cmd.SetArgs([]string{"--skip-fetch", "--cleanup=false", "--output", "/tmp/build", "latest"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotOpts == nil {
		t.Fatal("expected runF to be called")
	}

	if !gotOpts.SkipFetch {
		t.Error("expected SkipFetch to be true")
	}

	if gotOpts.Cleanup {
		t.Error("expected Cleanup to be false")
	}

	if gotOpts.OutputDir != "/tmp/build" {
		t.Errorf("expected OutputDir '/tmp/build', got %q", gotOpts.OutputDir)
	}

	if len(gotOpts.Versions) != 1 || gotOpts.Versions[0] != "latest" {
		t.Errorf("expected Versions [latest], got %v", gotOpts.Versions)
	}
}

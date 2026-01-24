package cmdutil

import (
	"testing"
)

func TestNewIOStreams(t *testing.T) {
	ios := NewIOStreams()

	if ios == nil {
		t.Fatal("NewIOStreams() returned nil")
	}
	if ios.In == nil {
		t.Error("NewIOStreams().In is nil")
	}
	if ios.Out == nil {
		t.Error("NewIOStreams().Out is nil")
	}
	if ios.ErrOut == nil {
		t.Error("NewIOStreams().ErrOut is nil")
	}
	if ios.isInputTTY != -1 {
		t.Errorf("NewIOStreams().isInputTTY = %d, want -1", ios.isInputTTY)
	}
	if ios.isOutputTTY != -1 {
		t.Errorf("NewIOStreams().isOutputTTY = %d, want -1", ios.isOutputTTY)
	}
}

func TestIOStreams_TTY(t *testing.T) {
	// Test with non-TTY (test buffers)
	ios := NewTestIOStreams()

	// Default is non-interactive
	if ios.IsInputTTY() {
		t.Error("TestIOStreams should not be input TTY by default")
	}
	if ios.IsOutputTTY() {
		t.Error("TestIOStreams should not be output TTY by default")
	}
	if ios.IsInteractive() {
		t.Error("TestIOStreams should not be interactive by default")
	}
}

func TestNewTestIOStreams(t *testing.T) {
	ios := NewTestIOStreams()

	if ios == nil {
		t.Fatal("NewTestIOStreams() returned nil")
	}
	if ios.IOStreams == nil {
		t.Fatal("NewTestIOStreams().IOStreams is nil")
	}
	if ios.InBuf == nil {
		t.Error("NewTestIOStreams().InBuf is nil")
	}
	if ios.OutBuf == nil {
		t.Error("NewTestIOStreams().OutBuf is nil")
	}
	if ios.ErrBuf == nil {
		t.Error("NewTestIOStreams().ErrBuf is nil")
	}

	// Verify buffers are connected to IOStreams
	if ios.IOStreams.In != ios.InBuf {
		t.Error("IOStreams.In is not connected to InBuf")
	}
	if ios.IOStreams.Out != ios.OutBuf {
		t.Error("IOStreams.Out is not connected to OutBuf")
	}
	if ios.IOStreams.ErrOut != ios.ErrBuf {
		t.Error("IOStreams.ErrOut is not connected to ErrBuf")
	}

	// Verify non-interactive by default
	if ios.isInputTTY != 0 {
		t.Errorf("NewTestIOStreams().isInputTTY = %d, want 0", ios.isInputTTY)
	}
	if ios.isOutputTTY != 0 {
		t.Errorf("NewTestIOStreams().isOutputTTY = %d, want 0", ios.isOutputTTY)
	}
}

func TestSetInteractive(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
		wantInput   bool
		wantOutput  bool
	}{
		{
			name:        "set interactive true",
			interactive: true,
			wantInput:   true,
			wantOutput:  true,
		},
		{
			name:        "set interactive false",
			interactive: false,
			wantInput:   false,
			wantOutput:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ios := NewTestIOStreams()
			ios.SetInteractive(tt.interactive)

			if ios.IsInputTTY() != tt.wantInput {
				t.Errorf("IsInputTTY() = %v, want %v", ios.IsInputTTY(), tt.wantInput)
			}
			if ios.IsOutputTTY() != tt.wantOutput {
				t.Errorf("IsOutputTTY() = %v, want %v", ios.IsOutputTTY(), tt.wantOutput)
			}
			if ios.IsInteractive() != (tt.wantInput && tt.wantOutput) {
				t.Errorf("IsInteractive() = %v, want %v", ios.IsInteractive(), tt.wantInput && tt.wantOutput)
			}
		})
	}
}

func TestTestBuffer(t *testing.T) {
	t.Run("Write and String", func(t *testing.T) {
		buf := &testBuffer{}
		n, err := buf.Write([]byte("hello"))
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		if n != 5 {
			t.Errorf("Write() = %d, want 5", n)
		}
		if buf.String() != "hello" {
			t.Errorf("String() = %q, want %q", buf.String(), "hello")
		}
	})

	t.Run("Read", func(t *testing.T) {
		buf := &testBuffer{}
		buf.SetInput("world")

		p := make([]byte, 10)
		n, err := buf.Read(p)
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if n != 5 {
			t.Errorf("Read() = %d, want 5", n)
		}
		if string(p[:n]) != "world" {
			t.Errorf("Read() data = %q, want %q", string(p[:n]), "world")
		}
	})

	t.Run("Read empty returns EOF", func(t *testing.T) {
		buf := &testBuffer{}
		p := make([]byte, 10)
		n, err := buf.Read(p)
		if n != 0 {
			t.Errorf("Read() = %d, want 0", n)
		}
		if err == nil {
			t.Error("Read() expected EOF error")
		}
	})

	t.Run("Reset", func(t *testing.T) {
		buf := &testBuffer{}
		buf.Write([]byte("hello"))
		buf.Reset()
		if buf.String() != "" {
			t.Errorf("String() after Reset() = %q, want empty", buf.String())
		}
	})

	t.Run("SetInput", func(t *testing.T) {
		buf := &testBuffer{}
		buf.SetInput("test input")
		if buf.String() != "test input" {
			t.Errorf("String() after SetInput() = %q, want %q", buf.String(), "test input")
		}
	})
}

func TestBoolToInt(t *testing.T) {
	if boolToInt(true) != 1 {
		t.Error("boolToInt(true) should return 1")
	}
	if boolToInt(false) != 0 {
		t.Error("boolToInt(false) should return 0")
	}
}

package iostreams

import (
	"strings"
	"testing"
)

func TestPrintSuccess(t *testing.T) {
	t.Run("disabled colors", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.PrintSuccess("build complete: %s", "v1.0")

		output := tio.ErrBuf.String()
		if !strings.Contains(output, "[ok]") {
			t.Errorf("expected [ok] fallback, got: %q", output)
		}
		if !strings.Contains(output, "build complete: v1.0") {
			t.Errorf("expected formatted message, got: %q", output)
		}
	})

	t.Run("enabled colors", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.PrintSuccess("done")

		output := tio.ErrBuf.String()
		if !strings.Contains(output, "✓") {
			t.Errorf("expected ✓ icon, got: %q", output)
		}
		if !strings.Contains(output, "done") {
			t.Errorf("expected message text, got: %q", output)
		}
	})

	t.Run("writes to ErrOut", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.PrintSuccess("ok")

		if tio.OutBuf.String() != "" {
			t.Errorf("should not write to OutBuf, got: %q", tio.OutBuf.String())
		}
		if tio.ErrBuf.String() == "" {
			t.Error("should write to ErrBuf")
		}
	})
}

func TestPrintWarning(t *testing.T) {
	t.Run("disabled colors", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.PrintWarning("disk space low: %d%%", 5)

		output := tio.ErrBuf.String()
		if !strings.Contains(output, "[warn]") {
			t.Errorf("expected [warn] fallback, got: %q", output)
		}
		if !strings.Contains(output, "disk space low: 5%") {
			t.Errorf("expected formatted message, got: %q", output)
		}
	})

	t.Run("enabled colors", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.PrintWarning("caution")

		output := tio.ErrBuf.String()
		if !strings.Contains(output, "!") {
			t.Errorf("expected ! icon, got: %q", output)
		}
		if !strings.Contains(output, "caution") {
			t.Errorf("expected message text, got: %q", output)
		}
	})
}

func TestPrintInfo(t *testing.T) {
	t.Run("disabled colors", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.PrintInfo("using image %s", "node:20")

		output := tio.ErrBuf.String()
		if !strings.Contains(output, "[info]") {
			t.Errorf("expected [info] fallback, got: %q", output)
		}
		if !strings.Contains(output, "using image node:20") {
			t.Errorf("expected formatted message, got: %q", output)
		}
	})

	t.Run("enabled colors", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.PrintInfo("note")

		output := tio.ErrBuf.String()
		if !strings.Contains(output, "ℹ") {
			t.Errorf("expected ℹ icon, got: %q", output)
		}
		if !strings.Contains(output, "note") {
			t.Errorf("expected message text, got: %q", output)
		}
	})
}

func TestPrintFailure(t *testing.T) {
	t.Run("disabled colors", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.PrintFailure("connection refused: %s", "localhost:5432")

		output := tio.ErrBuf.String()
		if !strings.Contains(output, "[error]") {
			t.Errorf("expected [error] fallback, got: %q", output)
		}
		if !strings.Contains(output, "connection refused: localhost:5432") {
			t.Errorf("expected formatted message, got: %q", output)
		}
	})

	t.Run("enabled colors", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.PrintFailure("failed")

		output := tio.ErrBuf.String()
		if !strings.Contains(output, "✗") {
			t.Errorf("expected ✗ icon, got: %q", output)
		}
		if !strings.Contains(output, "failed") {
			t.Errorf("expected message text, got: %q", output)
		}
	})
}

func TestPrintEmpty(t *testing.T) {
	t.Run("no hints", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.PrintEmpty("containers")

		output := tio.ErrBuf.String()
		if !strings.Contains(output, "No containers found") {
			t.Errorf("expected 'No containers found', got: %q", output)
		}
	})

	t.Run("with hints", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.PrintEmpty("volumes", "Run 'clawker volume create' to create one")

		output := tio.ErrBuf.String()
		if !strings.Contains(output, "No volumes found") {
			t.Errorf("expected 'No volumes found', got: %q", output)
		}
		if !strings.Contains(output, "Run 'clawker volume create' to create one") {
			t.Errorf("expected hint text, got: %q", output)
		}
	})

	t.Run("with multiple hints", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.PrintEmpty("images", "hint1", "hint2")

		output := tio.ErrBuf.String()
		if !strings.Contains(output, "hint1") || !strings.Contains(output, "hint2") {
			t.Errorf("expected both hints, got: %q", output)
		}
	})

	t.Run("writes to ErrOut", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.PrintEmpty("things")

		if tio.OutBuf.String() != "" {
			t.Errorf("should not write to OutBuf, got: %q", tio.OutBuf.String())
		}
		if tio.ErrBuf.String() == "" {
			t.Error("should write to ErrBuf")
		}
	})
}

func TestPrintMessages_AllWriteToErrOut(t *testing.T) {
	methods := []struct {
		name string
		call func(*IOStreams)
	}{
		{"PrintSuccess", func(ios *IOStreams) { ios.PrintSuccess("msg") }},
		{"PrintWarning", func(ios *IOStreams) { ios.PrintWarning("msg") }},
		{"PrintInfo", func(ios *IOStreams) { ios.PrintInfo("msg") }},
		{"PrintFailure", func(ios *IOStreams) { ios.PrintFailure("msg") }},
		{"PrintEmpty", func(ios *IOStreams) { ios.PrintEmpty("items") }},
	}

	for _, m := range methods {
		t.Run(m.name, func(t *testing.T) {
			tio := NewTestIOStreams()
			m.call(tio.IOStreams)

			if tio.OutBuf.String() != "" {
				t.Errorf("%s wrote to stdout: %q", m.name, tio.OutBuf.String())
			}
			if tio.ErrBuf.String() == "" {
				t.Errorf("%s did not write to stderr", m.name)
			}
		})
	}
}

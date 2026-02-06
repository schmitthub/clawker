package iostreams

import (
	"errors"
	"strings"
	"testing"
)

// --- RenderHeader ---

func TestRenderHeader(t *testing.T) {
	t.Run("title only plain", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderHeader("Containers")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "Containers") {
			t.Errorf("expected 'Containers', got: %q", output)
		}
	})

	t.Run("title with subtitle plain", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderHeader("Containers", "3 running")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "Containers") {
			t.Errorf("expected 'Containers', got: %q", output)
		}
		if !strings.Contains(output, "3 running") {
			t.Errorf("expected '3 running', got: %q", output)
		}
	})

	t.Run("styled mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.RenderHeader("Title")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "Title") {
			t.Errorf("styled output should contain 'Title', got: %q", output)
		}
	})

	t.Run("writes to Out", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderHeader("Test")
		if tio.OutBuf.String() == "" {
			t.Error("expected output in OutBuf")
		}
		if tio.ErrBuf.String() != "" {
			t.Errorf("expected no output in ErrBuf, got: %q", tio.ErrBuf.String())
		}
	})
}

// --- RenderDivider ---

func TestRenderDivider(t *testing.T) {
	t.Run("plain mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetTerminalSize(40, 24)
		tio.IOStreams.RenderDivider()
		output := tio.OutBuf.String()
		if !strings.Contains(output, "─") && !strings.Contains(output, "-") {
			t.Errorf("expected divider chars, got: %q", output)
		}
	})

	t.Run("styled mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.SetTerminalSize(40, 24)
		tio.IOStreams.RenderDivider()
		output := tio.OutBuf.String()
		if !strings.Contains(output, "─") {
			t.Errorf("expected ─ divider, got: %q", output)
		}
	})

	t.Run("writes to Out", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetTerminalSize(40, 24)
		tio.IOStreams.RenderDivider()
		if tio.OutBuf.String() == "" {
			t.Error("expected output in OutBuf")
		}
	})
}

// --- RenderLabeledDivider ---

func TestRenderLabeledDivider(t *testing.T) {
	t.Run("plain mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetTerminalSize(40, 24)
		tio.IOStreams.RenderLabeledDivider("Section")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "Section") {
			t.Errorf("expected 'Section' label, got: %q", output)
		}
	})

	t.Run("styled mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.SetTerminalSize(40, 24)
		tio.IOStreams.RenderLabeledDivider("Details")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "Details") {
			t.Errorf("expected 'Details' label, got: %q", output)
		}
		if !strings.Contains(output, "─") {
			t.Errorf("expected ─ divider segments, got: %q", output)
		}
	})

	t.Run("label too long falls back to divider", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetTerminalSize(10, 24) // Very narrow
		tio.IOStreams.RenderLabeledDivider("VeryLongLabelThatWontFit")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "─") {
			t.Errorf("fallback should still render divider, got: %q", output)
		}
	})
}

// --- RenderBadge ---

func TestRenderBadge(t *testing.T) {
	t.Run("default style", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.RenderBadge("ACTIVE")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "ACTIVE") {
			t.Errorf("expected 'ACTIVE', got: %q", output)
		}
	})

	t.Run("custom style", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.RenderBadge("ERROR", func(s string) string { return BadgeErrorStyle.Render(s) })
		output := tio.OutBuf.String()
		if !strings.Contains(output, "ERROR") {
			t.Errorf("expected 'ERROR', got: %q", output)
		}
	})

	t.Run("plain mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderBadge("TAG")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "[TAG]") {
			t.Errorf("expected '[TAG]' in plain mode, got: %q", output)
		}
	})
}

// --- RenderKeyValue ---

func TestRenderKeyValue(t *testing.T) {
	t.Run("plain mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderKeyValue("Name", "web-app")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "Name") || !strings.Contains(output, "web-app") {
			t.Errorf("expected key and value, got: %q", output)
		}
	})

	t.Run("styled mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.RenderKeyValue("Status", "running")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "Status") || !strings.Contains(output, "running") {
			t.Errorf("expected key and value, got: %q", output)
		}
	})
}

// --- RenderKeyValueBlock ---

func TestRenderKeyValueBlock(t *testing.T) {
	t.Run("multiple pairs", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderKeyValueBlock(
			KeyValuePair{Key: "Name", Value: "web"},
			KeyValuePair{Key: "Status", Value: "running"},
			KeyValuePair{Key: "Image", Value: "nginx:latest"},
		)
		output := tio.OutBuf.String()
		if !strings.Contains(output, "Name") || !strings.Contains(output, "web") {
			t.Errorf("missing Name/web: %q", output)
		}
		if !strings.Contains(output, "Status") || !strings.Contains(output, "running") {
			t.Errorf("missing Status/running: %q", output)
		}
		if !strings.Contains(output, "Image") || !strings.Contains(output, "nginx:latest") {
			t.Errorf("missing Image/nginx: %q", output)
		}
	})

	t.Run("styled mode alignment", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.RenderKeyValueBlock(
			KeyValuePair{Key: "N", Value: "v1"},
			KeyValuePair{Key: "LongerKey", Value: "v2"},
		)
		output := tio.OutBuf.String()
		lines := strings.Split(strings.TrimSpace(output), "\n")
		if len(lines) != 2 {
			t.Fatalf("expected 2 lines, got %d: %q", len(lines), output)
		}
		if !strings.Contains(output, "v1") || !strings.Contains(output, "v2") {
			t.Errorf("styled alignment missing values: %q", output)
		}
	})

	t.Run("empty pairs", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderKeyValueBlock()
		output := tio.OutBuf.String()
		if output != "" {
			t.Errorf("expected empty output, got: %q", output)
		}
	})
}

// --- RenderStatus ---

func TestRenderStatus(t *testing.T) {
	t.Run("running status", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.RenderStatus("web", "running")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "web") {
			t.Errorf("expected label 'web', got: %q", output)
		}
		if !strings.Contains(output, "●") && !strings.Contains(output, "RUNNING") {
			t.Errorf("expected running indicator, got: %q", output)
		}
	})

	t.Run("stopped status plain", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderStatus("db", "stopped")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "db") {
			t.Errorf("expected label 'db', got: %q", output)
		}
		if !strings.Contains(output, "STOPPED") {
			t.Errorf("expected STOPPED text, got: %q", output)
		}
	})
}

// --- RenderEmptyState ---

func TestRenderEmptyState(t *testing.T) {
	t.Run("plain mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderEmptyState("No containers found")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "No containers found") {
			t.Errorf("expected message, got: %q", output)
		}
	})

	t.Run("styled mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.RenderEmptyState("Nothing to show")
		output := tio.OutBuf.String()
		if !strings.Contains(output, "Nothing to show") {
			t.Errorf("expected message, got: %q", output)
		}
	})
}

// --- RenderError ---

func TestRenderError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderError(nil)
		if tio.ErrBuf.String() != "" {
			t.Errorf("nil error should produce no output, got: %q", tio.ErrBuf.String())
		}
	})

	t.Run("plain mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderError(errors.New("connection refused"))
		output := tio.ErrBuf.String()
		if !strings.Contains(output, "connection refused") {
			t.Errorf("expected error message, got: %q", output)
		}
	})

	t.Run("styled mode", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.SetColorEnabled(true)
		tio.IOStreams.RenderError(errors.New("timeout"))
		output := tio.ErrBuf.String()
		if !strings.Contains(output, "timeout") {
			t.Errorf("expected error message, got: %q", output)
		}
		if !strings.Contains(output, "✗") {
			t.Errorf("expected ✗ icon, got: %q", output)
		}
	})

	t.Run("writes to ErrOut", func(t *testing.T) {
		tio := NewTestIOStreams()
		tio.IOStreams.RenderError(errors.New("err"))
		if tio.OutBuf.String() != "" {
			t.Errorf("should not write to OutBuf, got: %q", tio.OutBuf.String())
		}
		if tio.ErrBuf.String() == "" {
			t.Error("should write to ErrBuf")
		}
	})
}

// --- All renders write to correct stream ---

func TestRenders_WriteToOut(t *testing.T) {
	methods := []struct {
		name string
		call func(*IOStreams)
	}{
		{"RenderHeader", func(ios *IOStreams) { ios.RenderHeader("T") }},
		{"RenderDivider", func(ios *IOStreams) { ios.RenderDivider() }},
		{"RenderLabeledDivider", func(ios *IOStreams) { ios.RenderLabeledDivider("L") }},
		{"RenderBadge", func(ios *IOStreams) { ios.RenderBadge("B") }},
		{"RenderKeyValue", func(ios *IOStreams) { ios.RenderKeyValue("K", "V") }},
		{"RenderStatus", func(ios *IOStreams) { ios.RenderStatus("L", "running") }},
		{"RenderEmptyState", func(ios *IOStreams) { ios.RenderEmptyState("M") }},
	}

	for _, m := range methods {
		t.Run(m.name, func(t *testing.T) {
			tio := NewTestIOStreams()
			tio.SetTerminalSize(40, 24)
			m.call(tio.IOStreams)

			if tio.OutBuf.String() == "" {
				t.Errorf("%s should write to Out", m.name)
			}
			if tio.ErrBuf.String() != "" {
				t.Errorf("%s should not write to ErrOut, got: %q", m.name, tio.ErrBuf.String())
			}
		})
	}
}

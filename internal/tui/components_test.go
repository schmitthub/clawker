package tui

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestRenderHeader(t *testing.T) {
	tests := []struct {
		name         string
		cfg          HeaderConfig
		wantContains []string
	}{
		{
			name: "title only",
			cfg: HeaderConfig{
				Title: "DASHBOARD",
				Width: 80,
			},
			wantContains: []string{"DASHBOARD"},
		},
		{
			name: "with subtitle",
			cfg: HeaderConfig{
				Title:    "DASHBOARD",
				Subtitle: "myproject",
				Width:    80,
			},
			wantContains: []string{"DASHBOARD", "myproject"},
		},
		{
			name: "with timestamp",
			cfg: HeaderConfig{
				Title:     "DASHBOARD",
				Timestamp: "12:00",
				Width:     80,
			},
			wantContains: []string{"DASHBOARD", "12:00"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderHeader(tt.cfg)
			for _, want := range tt.wantContains {
				assert.Contains(t, result, want)
			}
		})
	}
}

func TestRenderStatus(t *testing.T) {
	tests := []struct {
		name         string
		cfg          StatusConfig
		wantContains string
	}{
		{"running", StatusConfig{Status: "running"}, "RUNNING"},
		{"stopped", StatusConfig{Status: "stopped"}, "STOPPED"},
		{"error", StatusConfig{Status: "error"}, "ERROR"},
		{"custom label", StatusConfig{Status: "running", Label: "Active"}, "Active"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderStatus(tt.cfg)
			assert.Contains(t, result, tt.wantContains)
		})
	}
}

func TestRenderBadge(t *testing.T) {
	result := RenderBadge("TEST", func(s string) string { return iostreams.BadgeStyle.Render(s) })
	assert.Contains(t, result, "TEST")
}

func TestRenderBadge_Default(t *testing.T) {
	result := RenderBadge("TEST")
	assert.Contains(t, result, "TEST")
}

func TestRenderCountBadge(t *testing.T) {
	tests := []struct {
		name  string
		count int
		label string
	}{
		{"simple", 5, "tasks"},
		{"zero", 0, "items"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderCountBadge(tt.count, tt.label)
			assert.Contains(t, result, tt.label)
		})
	}
}

func TestRenderProgress(t *testing.T) {
	tests := []struct {
		name         string
		cfg          ProgressConfig
		wantContains string
	}{
		{
			name: "fraction",
			cfg: ProgressConfig{
				Current: 3,
				Total:   10,
				ShowBar: false,
			},
			wantContains: "3/10",
		},
		{
			name: "bar",
			cfg: ProgressConfig{
				Current: 5,
				Total:   10,
				Width:   12,
				ShowBar: true,
			},
			wantContains: "[",
		},
		{
			name: "zero total",
			cfg: ProgressConfig{
				Current: 0,
				Total:   0,
				ShowBar: false,
			},
			wantContains: "-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderProgress(tt.cfg)
			assert.Contains(t, result, tt.wantContains)
		})
	}
}

func TestRenderDivider(t *testing.T) {
	tests := []struct {
		name  string
		width int
		want  bool // true if should contain divider char
	}{
		{"normal", 20, true},
		{"zero", 0, false},
		{"negative", -5, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderDivider(tt.width)
			if tt.want {
				assert.NotEmpty(t, result)
			} else {
				assert.Empty(t, result)
			}
		})
	}
}

func TestRenderLabeledDivider(t *testing.T) {
	result := RenderLabeledDivider("Section", 40)
	assert.Contains(t, result, "Section")
}

func TestRenderEmptyState(t *testing.T) {
	result := RenderEmptyState("No items", 40, 10)
	assert.Contains(t, result, "No items")
}

func TestRenderError(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		result := RenderError(nil, 80)
		assert.Empty(t, result)
	})

	t.Run("with error", func(t *testing.T) {
		result := RenderError(errors.New("something went wrong"), 80)
		assert.Contains(t, result, "Error:")
		assert.Contains(t, result, "something went wrong")
	})

	t.Run("with wrap", func(t *testing.T) {
		// When width is narrow, the error message gets wrapped with newlines
		result := RenderError(errors.New("a very long error message that should be wrapped"), 30)
		assert.Contains(t, result, "Error:")
		// Check key parts exist (wrapping introduces newlines)
		assert.Contains(t, result, "very long error")
		assert.Contains(t, result, "wrapped")
	})
}

func TestRenderLabelValue(t *testing.T) {
	result := RenderLabelValue("Name", "Ralph")
	assert.Contains(t, result, "Name")
	assert.Contains(t, result, "Ralph")
}

func TestRenderKeyValueTable(t *testing.T) {
	tests := []struct {
		name  string
		pairs []KeyValuePair
		width int
	}{
		{
			name: "simple",
			pairs: []KeyValuePair{
				{Key: "Name", Value: "Ralph"},
				{Key: "Status", Value: "Running"},
			},
			width: 40,
		},
		{
			name:  "empty",
			pairs: []KeyValuePair{},
			width: 40,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderKeyValueTable(tt.pairs, tt.width)
			for _, p := range tt.pairs {
				assert.Contains(t, result, p.Key)
				assert.Contains(t, result, p.Value)
			}
		})
	}
}

func TestRenderTable(t *testing.T) {
	cfg := TableConfig{
		Headers: []string{"Name", "Status"},
		Rows: [][]string{
			{"agent1", "running"},
			{"agent2", "stopped"},
		},
		Width: 40,
	}

	result := RenderTable(cfg)
	assert.Contains(t, result, "Name")
	assert.Contains(t, result, "Status")
	assert.Contains(t, result, "agent1")
	assert.Contains(t, result, "agent2")
}

func TestRenderTable_Empty(t *testing.T) {
	cfg := TableConfig{
		Headers: []string{},
		Rows:    [][]string{},
		Width:   40,
	}

	result := RenderTable(cfg)
	assert.Empty(t, result)
}

func TestRenderPercentage(t *testing.T) {
	tests := []struct {
		name  string
		value float64
	}{
		{"low", 30.5},
		{"medium", 65.0},
		{"high", 85.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderPercentage(tt.value)
			assert.Contains(t, result, "%")
		})
	}
}

func TestRenderBytes(t *testing.T) {
	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{"bytes", 512, "512 B"},
		{"kilobytes", 2048, "2.0 KB"},
		{"megabytes", 1048576 * 2, "2.0 MB"},
		{"gigabytes", 1073741824 * 3, "3.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderBytes(tt.bytes)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestRenderTag(t *testing.T) {
	result := RenderTag("production", func(s string) string { return iostreams.SuccessStyle.Render(s) })
	assert.Contains(t, result, "production")
}

func TestRenderTag_Default(t *testing.T) {
	result := RenderTag("production")
	assert.Contains(t, result, "production")
}

func TestRenderTags(t *testing.T) {
	tests := []struct {
		name string
		tags []string
	}{
		{"multiple", []string{"tag1", "tag2"}},
		{"single", []string{"tag1"}},
		{"empty", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderTags(tt.tags, func(s string) string { return iostreams.BadgeStyle.Render(s) })
			for _, tag := range tt.tags {
				assert.Contains(t, result, tag)
			}
		})
	}
}

func TestRenderTags_Default(t *testing.T) {
	result := RenderTags([]string{"tag1", "tag2"})
	assert.Contains(t, result, "tag1")
	assert.Contains(t, result, "tag2")
}

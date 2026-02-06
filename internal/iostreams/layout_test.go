package iostreams

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitHorizontal(t *testing.T) {
	tests := []struct {
		name      string
		width     int
		cfg       SplitConfig
		wantLeft  int
		wantRight int
	}{
		{
			name:  "50/50 split",
			width: 100,
			cfg: SplitConfig{
				Ratio: 0.5,
				Gap:   0,
			},
			wantLeft:  50,
			wantRight: 50,
		},
		{
			name:  "40/60 split with gap",
			width: 101,
			cfg: SplitConfig{
				Ratio: 0.4,
				Gap:   1,
			},
			wantLeft:  40,
			wantRight: 60,
		},
		{
			name:  "respects min first",
			width: 50,
			cfg: SplitConfig{
				Ratio:    0.1,
				MinFirst: 20,
				Gap:      0,
			},
			wantLeft:  20,
			wantRight: 30,
		},
		{
			name:  "respects min second",
			width: 50,
			cfg: SplitConfig{
				Ratio:     0.9,
				MinSecond: 20,
				Gap:       0,
			},
			wantLeft:  30,
			wantRight: 20,
		},
		{
			name:  "zero width",
			width: 0,
			cfg: SplitConfig{
				Ratio: 0.5,
				Gap:   1,
			},
			wantLeft:  0,
			wantRight: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left, right := SplitHorizontal(tt.width, tt.cfg)
			assert.Equal(t, tt.wantLeft, left)
			assert.Equal(t, tt.wantRight, right)
		})
	}
}

func TestSplitVertical(t *testing.T) {
	tests := []struct {
		name       string
		height     int
		cfg        SplitConfig
		wantTop    int
		wantBottom int
	}{
		{
			name:   "50/50 split",
			height: 24,
			cfg: SplitConfig{
				Ratio: 0.5,
				Gap:   0,
			},
			wantTop:    12,
			wantBottom: 12,
		},
		{
			name:   "with gap",
			height: 25,
			cfg: SplitConfig{
				Ratio: 0.5,
				Gap:   1,
			},
			wantTop:    12,
			wantBottom: 12,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top, bottom := SplitVertical(tt.height, tt.cfg)
			assert.Equal(t, tt.wantTop, top)
			assert.Equal(t, tt.wantBottom, bottom)
		})
	}
}

func TestStack(t *testing.T) {
	tests := []struct {
		name       string
		spacing    int
		components []string
		want       string
	}{
		{
			name:       "no spacing",
			spacing:    0,
			components: []string{"a", "b", "c"},
			want:       "a\nb\nc",
		},
		{
			name:       "with spacing",
			spacing:    1,
			components: []string{"a", "b"},
			want:       "a\n\nb",
		},
		{
			name:       "filters empty",
			spacing:    0,
			components: []string{"a", "", "c"},
			want:       "a\nc",
		},
		{
			name:       "all empty",
			spacing:    0,
			components: []string{"", ""},
			want:       "",
		},
		{
			name:       "no components",
			spacing:    0,
			components: []string{},
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Stack(tt.spacing, tt.components...)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestRow(t *testing.T) {
	tests := []struct {
		name       string
		spacing    int
		components []string
		wantParts  []string
	}{
		{
			name:       "joins with spacing",
			spacing:    2,
			components: []string{"a", "b"},
			wantParts:  []string{"a", "b"},
		},
		{
			name:       "filters empty",
			spacing:    1,
			components: []string{"a", "", "c"},
			wantParts:  []string{"a", "c"},
		},
		{
			name:       "all empty returns empty",
			spacing:    0,
			components: []string{"", ""},
			wantParts:  nil,
		},
		{
			name:       "no components",
			spacing:    0,
			components: []string{},
			wantParts:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Row(tt.spacing, tt.components...)
			if tt.wantParts == nil {
				assert.Empty(t, result)
			} else {
				for _, part := range tt.wantParts {
					assert.Contains(t, result, part)
				}
			}
		})
	}
}

func TestColumns(t *testing.T) {
	result := Columns(80, 2, "col1", "col2", "col3")
	assert.Contains(t, result, "col1")
	assert.Contains(t, result, "col2")
	assert.Contains(t, result, "col3")

	// Empty returns empty
	assert.Empty(t, Columns(80, 2))
}

func TestCenterInRect(t *testing.T) {
	result := CenterInRect("test", 20, 5)
	assert.Contains(t, result, "test")
	lines := strings.Split(result, "\n")
	assert.True(t, len(lines) > 0)
}

func TestAlignLeft(t *testing.T) {
	result := AlignLeft("test", 10)
	assert.Contains(t, result, "test")
}

func TestAlignRight(t *testing.T) {
	result := AlignRight("test", 10)
	assert.Contains(t, result, "test")
}

func TestAlignCenter(t *testing.T) {
	result := AlignCenter("test", 10)
	assert.Contains(t, result, "test")
}

func TestFlexRow(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		left   string
		center string
		right  string
	}{
		{
			name:   "all parts",
			width:  40,
			left:   "LEFT",
			center: "CENTER",
			right:  "RIGHT",
		},
		{
			name:   "empty center",
			width:  40,
			left:   "LEFT",
			center: "",
			right:  "RIGHT",
		},
		{
			name:   "only left",
			width:  40,
			left:   "LEFT",
			center: "",
			right:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FlexRow(tt.width, tt.left, tt.center, tt.right)
			if tt.left != "" {
				assert.Contains(t, result, tt.left)
			}
			if tt.center != "" {
				assert.Contains(t, result, tt.center)
			}
			if tt.right != "" {
				assert.Contains(t, result, tt.right)
			}
		})
	}
}

func TestGrid(t *testing.T) {
	tests := []struct {
		name  string
		cfg   GridConfig
		items []string
	}{
		{
			name: "2x2 grid",
			cfg: GridConfig{
				Columns: 2,
				Gap:     1,
				Width:   40,
			},
			items: []string{"a", "b", "c", "d"},
		},
		{
			name: "empty items",
			cfg: GridConfig{
				Columns: 2,
				Gap:     1,
				Width:   40,
			},
			items: []string{},
		},
		{
			name: "zero columns",
			cfg: GridConfig{
				Columns: 0,
				Width:   40,
			},
			items: []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Grid(tt.cfg, tt.items...)
			if tt.cfg.Columns <= 0 || len(tt.items) == 0 {
				assert.Empty(t, result)
			} else {
				for _, item := range tt.items {
					assert.Contains(t, result, item)
				}
			}
		})
	}
}

func TestBox(t *testing.T) {
	cfg := BoxConfig{
		Width:   20,
		Height:  5,
		Padding: 1,
	}
	result := Box(cfg, "content")
	assert.Contains(t, result, "content")
}

func TestResponsiveLayout(t *testing.T) {
	layout := ResponsiveLayout{
		Compact: func(w int) string { return "compact" },
		Normal:  func(w int) string { return "normal" },
		Wide:    func(w int) string { return "wide" },
	}

	tests := []struct {
		name  string
		width int
		want  string
	}{
		{"compact", 50, "compact"},
		{"normal", 80, "normal"},
		{"wide", 120, "wide"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := layout.Render(tt.width)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestResponsiveLayout_Fallback(t *testing.T) {
	layout := ResponsiveLayout{
		Compact: func(w int) string { return "compact" },
	}

	assert.Equal(t, "compact", layout.Render(50))
	assert.Equal(t, "compact", layout.Render(80))
	assert.Equal(t, "compact", layout.Render(120))
}

func TestResponsiveLayout_PassesWidth(t *testing.T) {
	layout := ResponsiveLayout{
		Compact: func(w int) string { return "" },
		Normal: func(w int) string {
			assert.Equal(t, 90, w)
			return "got width"
		},
	}

	result := layout.Render(90)
	assert.Equal(t, "got width", result)
}

func TestDefaultSplitConfig(t *testing.T) {
	cfg := DefaultSplitConfig()
	assert.Equal(t, 0.5, cfg.Ratio)
	assert.Equal(t, 10, cfg.MinFirst)
	assert.Equal(t, 10, cfg.MinSecond)
	assert.Equal(t, 1, cfg.Gap)
}

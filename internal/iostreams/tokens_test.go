package iostreams

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpacingTokenValues(t *testing.T) {
	assert.Equal(t, 0, SpaceNone)
	assert.Equal(t, 1, SpaceXS)
	assert.Equal(t, 2, SpaceSM)
	assert.Equal(t, 4, SpaceMD)
	assert.Equal(t, 8, SpaceLG)
}

func TestWidthBreakpointValues(t *testing.T) {
	assert.Equal(t, 60, WidthCompact)
	assert.Equal(t, 80, WidthNormal)
	assert.Equal(t, 120, WidthWide)

	// Breakpoints should be in ascending order
	assert.Less(t, WidthCompact, WidthNormal)
	assert.Less(t, WidthNormal, WidthWide)
}

func TestLayoutMode_String(t *testing.T) {
	tests := []struct {
		name string
		mode LayoutMode
		want string
	}{
		{"compact", LayoutCompact, "compact"},
		{"normal", LayoutNormal, "normal"},
		{"wide", LayoutWide, "wide"},
		{"unknown", LayoutMode(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.mode.String())
		})
	}
}

func TestGetLayoutMode(t *testing.T) {
	tests := []struct {
		name  string
		width int
		want  LayoutMode
	}{
		{"very narrow", 40, LayoutCompact},
		{"compact threshold", 60, LayoutCompact},
		{"below normal", 79, LayoutCompact},
		{"normal threshold", 80, LayoutNormal},
		{"normal range", 100, LayoutNormal},
		{"below wide", 119, LayoutNormal},
		{"wide threshold", 120, LayoutWide},
		{"very wide", 200, LayoutWide},
		{"zero width", 0, LayoutCompact},
		{"negative width", -1, LayoutCompact},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetLayoutMode(tt.width))
		})
	}
}

func TestGetContentWidth(t *testing.T) {
	tests := []struct {
		name       string
		totalWidth int
		padding    int
		want       int
	}{
		{"standard", 80, 1, 76},
		{"no padding", 80, 0, 78},
		{"large padding", 80, 4, 70},
		{"narrow", 20, 1, 16},
		{"too narrow", 4, 2, 0},
		{"zero width", 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetContentWidth(tt.totalWidth, tt.padding))
		})
	}
}

func TestGetContentHeight(t *testing.T) {
	tests := []struct {
		name         string
		totalHeight  int
		headerHeight int
		footerHeight int
		want         int
	}{
		{"standard", 24, 3, 1, 20},
		{"no header/footer", 24, 0, 0, 24},
		{"large header", 24, 10, 2, 12},
		{"too small", 5, 3, 3, 0},
		{"zero height", 0, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetContentHeight(tt.totalHeight, tt.headerHeight, tt.footerHeight))
		})
	}
}

func TestMinInt(t *testing.T) {
	tests := []struct {
		name string
		a, b int
		want int
	}{
		{"a smaller", 5, 10, 5},
		{"b smaller", 10, 5, 5},
		{"equal", 5, 5, 5},
		{"negative", -5, 5, -5},
		{"both negative", -10, -5, -10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, MinInt(tt.a, tt.b))
		})
	}
}

func TestMaxInt(t *testing.T) {
	tests := []struct {
		name string
		a, b int
		want int
	}{
		{"a larger", 10, 5, 10},
		{"b larger", 5, 10, 10},
		{"equal", 5, 5, 5},
		{"negative", -5, 5, 5},
		{"both negative", -10, -5, -5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, MaxInt(tt.a, tt.b))
		})
	}
}

func TestClampInt(t *testing.T) {
	tests := []struct {
		name     string
		value    int
		min, max int
		want     int
	}{
		{"within range", 5, 0, 10, 5},
		{"at min", 0, 0, 10, 0},
		{"at max", 10, 0, 10, 10},
		{"below min", -5, 0, 10, 0},
		{"above max", 15, 0, 10, 10},
		{"negative range", -5, -10, -1, -5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ClampInt(tt.value, tt.min, tt.max))
		})
	}
}

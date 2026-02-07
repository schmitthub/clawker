package iostreams

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

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

func TestCenterInRect(t *testing.T) {
	result := CenterInRect("test", 20, 5)
	assert.Contains(t, result, "test")
	lines := strings.Split(result, "\n")
	assert.True(t, len(lines) > 0)
}

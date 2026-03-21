package storeui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithTitle(t *testing.T) {
	opts := editOptions{skipPaths: make(map[string]bool)}
	WithTitle("My Title")(&opts)
	assert.Equal(t, "My Title", opts.title)
}

func TestWithOverrides(t *testing.T) {
	overrides := []Override{
		{Path: "build.image", Label: ptr("Image")},
	}
	opts := editOptions{skipPaths: make(map[string]bool)}
	WithOverrides(overrides)(&opts)
	assert.Len(t, opts.overrides, 1)
	assert.Equal(t, "build.image", opts.overrides[0].Path)
}

func TestWithSkipPaths(t *testing.T) {
	opts := editOptions{skipPaths: make(map[string]bool)}
	WithSkipPaths("a.b", "c.d")(&opts)
	assert.True(t, opts.skipPaths["a.b"])
	assert.True(t, opts.skipPaths["c.d"])
	assert.False(t, opts.skipPaths["e.f"])
}

func TestEditOptions_Defaults(t *testing.T) {
	opts := editOptions{
		title:     "Configuration Editor",
		skipPaths: make(map[string]bool),
	}
	assert.Equal(t, "Configuration Editor", opts.title)
	assert.Empty(t, opts.overrides)
	assert.Empty(t, opts.skipPaths)
}

package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPanel(t *testing.T) {
	cfg := PanelConfig{
		Title:   "Test Panel",
		Width:   40,
		Height:  10,
		Focused: true,
		Padding: 2,
	}

	p := NewPanel(cfg)
	assert.Equal(t, "Test Panel", p.title)
	assert.Equal(t, 40, p.width)
	assert.Equal(t, 10, p.height)
	assert.True(t, p.focused)
	assert.Equal(t, 2, p.padding)
}

func TestDefaultPanelConfig(t *testing.T) {
	cfg := DefaultPanelConfig()
	assert.Equal(t, 40, cfg.Width)
	assert.Equal(t, 10, cfg.Height)
	assert.False(t, cfg.Focused)
	assert.Equal(t, 1, cfg.Padding)
}

func TestPanelModel_SetContent(t *testing.T) {
	p := NewPanel(DefaultPanelConfig())
	p = p.SetContent("Hello World")
	assert.Equal(t, "Hello World", p.content)
}

func TestPanelModel_SetTitle(t *testing.T) {
	p := NewPanel(DefaultPanelConfig())
	p = p.SetTitle("New Title")
	assert.Equal(t, "New Title", p.title)
}

func TestPanelModel_SetFocused(t *testing.T) {
	p := NewPanel(DefaultPanelConfig())
	assert.False(t, p.focused)

	p = p.SetFocused(true)
	assert.True(t, p.focused)
}

func TestPanelModel_SetWidth(t *testing.T) {
	p := NewPanel(DefaultPanelConfig())
	p = p.SetWidth(60)
	assert.Equal(t, 60, p.width)
}

func TestPanelModel_SetHeight(t *testing.T) {
	p := NewPanel(DefaultPanelConfig())
	p = p.SetHeight(20)
	assert.Equal(t, 20, p.height)
}

func TestPanelModel_SetPadding(t *testing.T) {
	p := NewPanel(DefaultPanelConfig())
	p = p.SetPadding(3)
	assert.Equal(t, 3, p.padding)
}

func TestPanelModel_View(t *testing.T) {
	tests := []struct {
		name       string
		cfg        PanelConfig
		content    string
		wantParts  []string
	}{
		{
			name: "with title and content",
			cfg: PanelConfig{
				Title:  "Test",
				Width:  40,
				Height: 10,
			},
			content:   "Hello World",
			wantParts: []string{"Test", "Hello World"},
		},
		{
			name: "focused panel",
			cfg: PanelConfig{
				Title:   "Focused",
				Width:   40,
				Height:  10,
				Focused: true,
			},
			content:   "Content",
			wantParts: []string{"Focused", "Content"},
		},
		{
			name: "no title",
			cfg: PanelConfig{
				Width:  40,
				Height: 10,
			},
			content:   "Just content",
			wantParts: []string{"Just content"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPanel(tt.cfg).SetContent(tt.content)
			view := p.View()
			for _, part := range tt.wantParts {
				assert.Contains(t, view, part)
			}
		})
	}
}

func TestPanelModel_Getters(t *testing.T) {
	p := NewPanel(PanelConfig{
		Title:   "Test",
		Width:   50,
		Height:  15,
		Focused: true,
	}).SetContent("Content")

	assert.Equal(t, 50, p.Width())
	assert.Equal(t, 15, p.Height())
	assert.Equal(t, "Test", p.Title())
	assert.Equal(t, "Content", p.Content())
	assert.True(t, p.IsFocused())
}

func TestRenderInfoPanel(t *testing.T) {
	result := RenderInfoPanel("Info", "Some information", 40)
	assert.Contains(t, result, "Info")
	assert.Contains(t, result, "Some information")
}

func TestRenderDetailPanel(t *testing.T) {
	pairs := []KeyValuePair{
		{Key: "Name", Value: "Test"},
		{Key: "Status", Value: "Active"},
	}

	result := RenderDetailPanel("Details", pairs, 50)
	assert.Contains(t, result, "Details")
	assert.Contains(t, result, "Name")
	assert.Contains(t, result, "Test")
}

func TestPanelGroup(t *testing.T) {
	p1 := NewPanel(PanelConfig{Title: "Panel 1", Width: 30, Height: 10})
	p2 := NewPanel(PanelConfig{Title: "Panel 2", Width: 30, Height: 10})
	p3 := NewPanel(PanelConfig{Title: "Panel 3", Width: 30, Height: 10})

	g := NewPanelGroup(p1, p2, p3)
	assert.Equal(t, 3, len(g.Panels()))
	assert.Equal(t, 0, g.FocusedIndex())
}

func TestPanelGroup_FocusNext(t *testing.T) {
	p1 := NewPanel(PanelConfig{Title: "Panel 1", Width: 30, Height: 10})
	p2 := NewPanel(PanelConfig{Title: "Panel 2", Width: 30, Height: 10})

	g := NewPanelGroup(p1, p2)

	g = g.FocusNext()
	assert.Equal(t, 1, g.FocusedIndex())

	g = g.FocusNext()
	assert.Equal(t, 0, g.FocusedIndex()) // Wraps around
}

func TestPanelGroup_FocusPrev(t *testing.T) {
	p1 := NewPanel(PanelConfig{Title: "Panel 1", Width: 30, Height: 10})
	p2 := NewPanel(PanelConfig{Title: "Panel 2", Width: 30, Height: 10})

	g := NewPanelGroup(p1, p2)

	g = g.FocusPrev()
	assert.Equal(t, 1, g.FocusedIndex()) // Wraps around

	g = g.FocusPrev()
	assert.Equal(t, 0, g.FocusedIndex())
}

func TestPanelGroup_Focus(t *testing.T) {
	p1 := NewPanel(PanelConfig{Title: "Panel 1", Width: 30, Height: 10})
	p2 := NewPanel(PanelConfig{Title: "Panel 2", Width: 30, Height: 10})
	p3 := NewPanel(PanelConfig{Title: "Panel 3", Width: 30, Height: 10})

	g := NewPanelGroup(p1, p2, p3)

	g = g.Focus(2)
	assert.Equal(t, 2, g.FocusedIndex())

	// Invalid index should be ignored
	g = g.Focus(10)
	assert.Equal(t, 2, g.FocusedIndex())

	g = g.Focus(-1)
	assert.Equal(t, 2, g.FocusedIndex())
}

func TestPanelGroup_Add(t *testing.T) {
	p1 := NewPanel(PanelConfig{Title: "Panel 1", Width: 30, Height: 10})
	p2 := NewPanel(PanelConfig{Title: "Panel 2", Width: 30, Height: 10})

	g := NewPanelGroup(p1)
	assert.Equal(t, 1, len(g.Panels()))

	g = g.Add(p2)
	assert.Equal(t, 2, len(g.Panels()))
}

func TestPanelGroup_RenderHorizontal(t *testing.T) {
	p1 := NewPanel(PanelConfig{Title: "Panel 1", Width: 20, Height: 5}).SetContent("Content 1")
	p2 := NewPanel(PanelConfig{Title: "Panel 2", Width: 20, Height: 5}).SetContent("Content 2")

	g := NewPanelGroup(p1, p2)
	result := g.RenderHorizontal(1)

	assert.Contains(t, result, "Panel 1")
	assert.Contains(t, result, "Panel 2")
}

func TestPanelGroup_RenderVertical(t *testing.T) {
	p1 := NewPanel(PanelConfig{Title: "Panel 1", Width: 40, Height: 5}).SetContent("Content 1")
	p2 := NewPanel(PanelConfig{Title: "Panel 2", Width: 40, Height: 5}).SetContent("Content 2")

	g := NewPanelGroup(p1, p2)
	result := g.RenderVertical(1)

	assert.Contains(t, result, "Panel 1")
	assert.Contains(t, result, "Panel 2")
}

func TestPanelGroup_EmptyGroup(t *testing.T) {
	g := NewPanelGroup()

	// Operations on empty group should be safe
	g = g.FocusNext()
	g = g.FocusPrev()
	g = g.Focus(0)

	assert.Equal(t, 0, len(g.Panels()))
	assert.Empty(t, g.RenderHorizontal(1))
	assert.Empty(t, g.RenderVertical(1))
}

func TestPanelGroup_FocusedPanel(t *testing.T) {
	p1 := NewPanel(PanelConfig{Title: "Panel 1", Width: 30, Height: 10})
	g := NewPanelGroup(p1)

	focused := g.FocusedPanel()
	assert.Equal(t, "Panel 1", focused.Title())

	// Empty group returns empty panel
	emptyGroup := NewPanelGroup()
	emptyFocused := emptyGroup.FocusedPanel()
	assert.Empty(t, emptyFocused.Title())
}

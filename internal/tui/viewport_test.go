package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestNewViewport(t *testing.T) {
	v := NewViewport(ViewportConfig{
		Width:  80,
		Height: 24,
		Title:  "Test Viewport",
	})

	assert.Equal(t, "Test Viewport", v.Title())
	assert.Equal(t, 80, v.Width())
	assert.Equal(t, 24, v.Height())
}

func TestViewportModel_SetContent(t *testing.T) {
	v := NewViewport(ViewportConfig{Width: 80, Height: 10})
	v = v.SetContent("Hello World")

	view := v.View()
	assert.Contains(t, view, "Hello World")
}

func TestViewportModel_SetSize(t *testing.T) {
	v := NewViewport(ViewportConfig{Width: 80, Height: 24})
	v = v.SetSize(120, 40)

	assert.Equal(t, 120, v.Width())
	assert.Equal(t, 40, v.Height())
}

func TestViewportModel_SetTitle(t *testing.T) {
	v := NewViewport(ViewportConfig{Width: 80, Height: 24, Title: "Old"})
	v = v.SetTitle("New Title")

	assert.Equal(t, "New Title", v.Title())
}

func TestViewportModel_ScrollToTop(t *testing.T) {
	v := NewViewport(ViewportConfig{Width: 80, Height: 3})
	v = v.SetContent("line 1\nline 2\nline 3\nline 4\nline 5")

	v = v.ScrollToTop()
	assert.True(t, v.AtTop())
}

func TestViewportModel_ScrollToBottom(t *testing.T) {
	v := NewViewport(ViewportConfig{Width: 80, Height: 3})
	v = v.SetContent("line 1\nline 2\nline 3\nline 4\nline 5")

	v = v.ScrollToBottom()
	assert.True(t, v.AtBottom())
}

func TestViewportModel_Init(t *testing.T) {
	v := NewViewport(ViewportConfig{Width: 80, Height: 24})
	cmd := v.Init()

	// Init may return nil or a command; either is fine.
	_ = cmd
}

func TestViewportModel_Update(t *testing.T) {
	v := NewViewport(ViewportConfig{Width: 80, Height: 24})
	v = v.SetContent("some content")

	// Send a generic key message — should not panic.
	updated, cmd := v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	_ = cmd
	_ = updated.View()
}

func TestViewportModel_View_WithTitle(t *testing.T) {
	v := NewViewport(ViewportConfig{
		Width:  40,
		Height: 10,
		Title:  "My Panel",
	})
	v = v.SetContent("Panel content")

	view := v.View()
	assert.Contains(t, view, "My Panel")
	assert.Contains(t, view, "Panel content")
}

func TestViewportModel_View_WithoutTitle(t *testing.T) {
	v := NewViewport(ViewportConfig{
		Width:  40,
		Height: 10,
	})
	v = v.SetContent("Just content")

	view := v.View()
	assert.Contains(t, view, "Just content")
}

func TestViewportModel_AtTopAtBottom(t *testing.T) {
	v := NewViewport(ViewportConfig{Width: 80, Height: 3})
	v = v.SetContent("a\nb\nc\nd\ne")

	v = v.ScrollToTop()
	assert.True(t, v.AtTop())

	v = v.ScrollToBottom()
	assert.True(t, v.AtBottom())
}

func TestViewportModel_ScrollPercent(t *testing.T) {
	v := NewViewport(ViewportConfig{Width: 80, Height: 3})
	v = v.SetContent("a\nb\nc\nd\ne")

	v = v.ScrollToTop()
	assert.Equal(t, 0.0, v.ScrollPercent())

	v = v.ScrollToBottom()
	assert.Equal(t, 1.0, v.ScrollPercent())
}

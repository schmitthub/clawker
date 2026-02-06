package tui

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// ViewportConfig configures a viewport component.
type ViewportConfig struct {
	Width   int
	Height  int
	Title   string
	Content string
}

// ViewportModel wraps a bubbles viewport with consistent styling.
// It provides scrollable content within a bordered panel.
type ViewportModel struct {
	viewport viewport.Model
	title    string
}

// NewViewport creates a new viewport with the given configuration.
func NewViewport(cfg ViewportConfig) ViewportModel {
	vp := viewport.New(cfg.Width, cfg.Height)
	vp.Style = PanelStyle
	if cfg.Content != "" {
		vp.SetContent(cfg.Content)
	}
	return ViewportModel{
		viewport: vp,
		title:    cfg.Title,
	}
}

// SetContent sets the viewport content.
func (v ViewportModel) SetContent(s string) ViewportModel {
	v.viewport.SetContent(s)
	return v
}

// SetSize sets the viewport dimensions.
func (v ViewportModel) SetSize(width, height int) ViewportModel {
	v.viewport.Width = width
	v.viewport.Height = height
	return v
}

// SetTitle sets the viewport title.
func (v ViewportModel) SetTitle(title string) ViewportModel {
	v.title = title
	return v
}

// ScrollToTop scrolls to the top of the content.
func (v ViewportModel) ScrollToTop() ViewportModel {
	v.viewport.GotoTop()
	return v
}

// ScrollToBottom scrolls to the bottom of the content.
func (v ViewportModel) ScrollToBottom() ViewportModel {
	v.viewport.GotoBottom()
	return v
}

// AtTop returns true if the viewport is scrolled to the top.
func (v ViewportModel) AtTop() bool {
	return v.viewport.AtTop()
}

// AtBottom returns true if the viewport is scrolled to the bottom.
func (v ViewportModel) AtBottom() bool {
	return v.viewport.AtBottom()
}

// ScrollPercent returns the scroll position as a percentage (0.0 to 1.0).
func (v ViewportModel) ScrollPercent() float64 {
	return v.viewport.ScrollPercent()
}

// Title returns the viewport title.
func (v ViewportModel) Title() string {
	return v.title
}

// Width returns the viewport width.
func (v ViewportModel) Width() int {
	return v.viewport.Width
}

// Height returns the viewport height.
func (v ViewportModel) Height() int {
	return v.viewport.Height
}

// Init implements tea.Model.
func (v ViewportModel) Init() tea.Cmd {
	return v.viewport.Init()
}

// Update implements tea.Model.
func (v ViewportModel) Update(msg tea.Msg) (ViewportModel, tea.Cmd) {
	var cmd tea.Cmd
	v.viewport, cmd = v.viewport.Update(msg)
	return v, cmd
}

// View renders the viewport.
func (v ViewportModel) View() string {
	content := v.viewport.View()
	if v.title != "" {
		header := PanelTitleStyle.Render(v.title)
		return Stack(0, header, content)
	}
	return content
}

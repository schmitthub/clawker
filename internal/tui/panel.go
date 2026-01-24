package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// PanelConfig configures a panel component.
type PanelConfig struct {
	Title   string
	Width   int
	Height  int
	Focused bool
	Padding int
}

// DefaultPanelConfig returns sensible defaults for a panel.
func DefaultPanelConfig() PanelConfig {
	return PanelConfig{
		Width:   40,
		Height:  10,
		Focused: false,
		Padding: 1,
	}
}

// PanelModel represents a bordered content container.
type PanelModel struct {
	title   string
	content string
	width   int
	height  int
	focused bool
	padding int
}

// NewPanel creates a new panel with the given configuration.
func NewPanel(cfg PanelConfig) PanelModel {
	return PanelModel{
		title:   cfg.Title,
		width:   cfg.Width,
		height:  cfg.Height,
		focused: cfg.Focused,
		padding: cfg.Padding,
	}
}

// SetContent sets the panel's content.
func (p PanelModel) SetContent(content string) PanelModel {
	p.content = content
	return p
}

// SetTitle sets the panel's title.
func (p PanelModel) SetTitle(title string) PanelModel {
	p.title = title
	return p
}

// SetFocused sets whether the panel is focused.
func (p PanelModel) SetFocused(focused bool) PanelModel {
	p.focused = focused
	return p
}

// SetWidth sets the panel's width.
func (p PanelModel) SetWidth(width int) PanelModel {
	p.width = width
	return p
}

// SetHeight sets the panel's height.
func (p PanelModel) SetHeight(height int) PanelModel {
	p.height = height
	return p
}

// SetPadding sets the panel's internal padding.
func (p PanelModel) SetPadding(padding int) PanelModel {
	p.padding = padding
	return p
}

// View renders the panel.
func (p PanelModel) View() string {
	var style lipgloss.Style
	if p.focused {
		style = PanelActiveStyle
	} else {
		style = PanelStyle
	}

	// Calculate content dimensions
	contentWidth := max(p.width-2-(p.padding*2), 0) // borders + padding

	contentHeight := p.height - 2 // borders
	if p.title != "" {
		contentHeight-- // Title takes a line
	}
	contentHeight = max(contentHeight, 0)

	// Build content
	var content strings.Builder

	// Add title if present
	if p.title != "" {
		titleStyle := PanelTitleStyle.Width(contentWidth)
		content.WriteString(titleStyle.Render(p.title))
		content.WriteString("\n")
	}

	// Add content
	if p.content != "" {
		content.WriteString(p.content)
	}

	// Apply final style
	finalStyle := style.
		Width(p.width).
		Height(p.height).
		Padding(0, p.padding)

	return finalStyle.Render(content.String())
}

// Width returns the panel's width.
func (p PanelModel) Width() int {
	return p.width
}

// Height returns the panel's height.
func (p PanelModel) Height() int {
	return p.height
}

// Title returns the panel's title.
func (p PanelModel) Title() string {
	return p.title
}

// Content returns the panel's content.
func (p PanelModel) Content() string {
	return p.content
}

// IsFocused returns whether the panel is focused.
func (p PanelModel) IsFocused() bool {
	return p.focused
}

// RenderInfoPanel renders a simple info panel with title and content.
func RenderInfoPanel(title, content string, width int) string {
	panel := NewPanel(PanelConfig{
		Title:   title,
		Width:   width,
		Padding: 1,
	}).SetContent(content)

	return panel.View()
}

// RenderDetailPanel renders a panel with key-value pairs.
func RenderDetailPanel(title string, pairs []KeyValuePair, width int) string {
	content := RenderKeyValueTable(pairs, width-4) // Account for border + padding
	panel := NewPanel(PanelConfig{
		Title:   title,
		Width:   width,
		Padding: 1,
	}).SetContent(content)

	return panel.View()
}

// RenderScrollablePanel renders a panel that shows scrollable content.
func RenderScrollablePanel(title string, lines []string, offset, visibleLines, width int) string {
	if len(lines) == 0 {
		return RenderInfoPanel(title, EmptyStateStyle.Render("No content"), width)
	}

	// Calculate visible range
	start := max(offset, 0)
	end := min(offset+visibleLines, len(lines))

	// Get visible lines
	visible := lines[start:end]
	content := strings.Join(visible, "\n")

	// Add scroll indicator if needed
	if len(lines) > visibleLines {
		indicator := MutedStyle.Render(fmt.Sprintf(" [%d-%d/%d]", start+1, end, len(lines)))
		title = title + indicator
	}

	return RenderInfoPanel(title, content, width)
}

// PanelGroup manages a group of panels with focus handling.
type PanelGroup struct {
	panels     []PanelModel
	focusIndex int
}

// NewPanelGroup creates a new panel group.
func NewPanelGroup(panels ...PanelModel) PanelGroup {
	g := PanelGroup{
		panels:     panels,
		focusIndex: 0,
	}
	if len(panels) > 0 {
		g.panels[0] = g.panels[0].SetFocused(true)
	}
	return g
}

// Add adds a panel to the group.
func (g PanelGroup) Add(panel PanelModel) PanelGroup {
	g.panels = append(g.panels, panel)
	return g
}

// FocusNext moves focus to the next panel.
func (g PanelGroup) FocusNext() PanelGroup {
	if len(g.panels) == 0 {
		return g
	}
	g.panels[g.focusIndex] = g.panels[g.focusIndex].SetFocused(false)
	g.focusIndex = (g.focusIndex + 1) % len(g.panels)
	g.panels[g.focusIndex] = g.panels[g.focusIndex].SetFocused(true)
	return g
}

// FocusPrev moves focus to the previous panel.
func (g PanelGroup) FocusPrev() PanelGroup {
	if len(g.panels) == 0 {
		return g
	}
	g.panels[g.focusIndex] = g.panels[g.focusIndex].SetFocused(false)
	g.focusIndex--
	if g.focusIndex < 0 {
		g.focusIndex = len(g.panels) - 1
	}
	g.panels[g.focusIndex] = g.panels[g.focusIndex].SetFocused(true)
	return g
}

// Focus sets focus to a specific panel index.
func (g PanelGroup) Focus(index int) PanelGroup {
	if len(g.panels) == 0 || index < 0 || index >= len(g.panels) {
		return g
	}
	g.panels[g.focusIndex] = g.panels[g.focusIndex].SetFocused(false)
	g.focusIndex = index
	g.panels[g.focusIndex] = g.panels[g.focusIndex].SetFocused(true)
	return g
}

// FocusedPanel returns the currently focused panel.
func (g PanelGroup) FocusedPanel() PanelModel {
	if len(g.panels) == 0 {
		return PanelModel{}
	}
	return g.panels[g.focusIndex]
}

// FocusedIndex returns the index of the focused panel.
func (g PanelGroup) FocusedIndex() int {
	return g.focusIndex
}

// Panels returns all panels in the group.
func (g PanelGroup) Panels() []PanelModel {
	return g.panels
}

// RenderHorizontal renders panels in a horizontal row.
func (g PanelGroup) RenderHorizontal(gap int) string {
	if len(g.panels) == 0 {
		return ""
	}

	var views []string
	for _, p := range g.panels {
		views = append(views, p.View())
	}

	spacer := strings.Repeat(" ", gap)
	return lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(views, spacer))
}

// RenderVertical renders panels in a vertical stack.
func (g PanelGroup) RenderVertical(gap int) string {
	if len(g.panels) == 0 {
		return ""
	}

	var views []string
	for _, p := range g.panels {
		views = append(views, p.View())
	}

	spacer := strings.Repeat("\n", gap)
	return strings.Join(views, "\n"+spacer)
}

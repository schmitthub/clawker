package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ListItem represents an item that can be displayed in a list.
type ListItem interface {
	// Title returns the main display text.
	Title() string
	// Description returns optional secondary text.
	Description() string
	// FilterValue returns the string used for filtering.
	FilterValue() string
}

// SimpleListItem is a basic implementation of ListItem.
type SimpleListItem struct {
	ItemTitle       string
	ItemDescription string
}

// Title implements ListItem.
func (i SimpleListItem) Title() string {
	return i.ItemTitle
}

// Description implements ListItem.
func (i SimpleListItem) Description() string {
	return i.ItemDescription
}

// FilterValue implements ListItem.
func (i SimpleListItem) FilterValue() string {
	return i.ItemTitle
}

// ListConfig configures a list component.
type ListConfig struct {
	Width            int
	Height           int
	ShowDescriptions bool
	Wrap             bool
}

// DefaultListConfig returns sensible defaults for a list.
func DefaultListConfig() ListConfig {
	return ListConfig{
		Width:            40,
		Height:           10,
		ShowDescriptions: true,
		Wrap:             false,
	}
}

// ListModel is a lightweight selectable list component.
type ListModel struct {
	items            []ListItem
	selectedIndex    int
	width            int
	height           int
	showDescriptions bool
	wrap             bool
	offset           int // For scrolling
}

// NewList creates a new list with the given configuration.
func NewList(cfg ListConfig) ListModel {
	return ListModel{
		items:            nil,
		selectedIndex:    0,
		width:            cfg.Width,
		height:           cfg.Height,
		showDescriptions: cfg.ShowDescriptions,
		wrap:             cfg.Wrap,
		offset:           0,
	}
}

// SetItems sets the list items.
func (m ListModel) SetItems(items []ListItem) ListModel {
	m.items = items
	if m.selectedIndex >= len(items) {
		m.selectedIndex = max(len(items)-1, 0)
	}
	m.updateOffset()
	return m
}

// SetWidth sets the list width.
func (m ListModel) SetWidth(width int) ListModel {
	m.width = width
	return m
}

// SetHeight sets the list height.
func (m ListModel) SetHeight(height int) ListModel {
	m.height = height
	m.updateOffset()
	return m
}

// SetShowDescriptions sets whether to show descriptions.
func (m ListModel) SetShowDescriptions(show bool) ListModel {
	m.showDescriptions = show
	return m
}

// SetWrap sets whether selection wraps around.
func (m ListModel) SetWrap(wrap bool) ListModel {
	m.wrap = wrap
	return m
}

// Update handles key messages for navigation.
func (m ListModel) Update(msg tea.Msg) (ListModel, tea.Cmd) {
	if len(m.items) == 0 {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case IsUp(msg):
			m = m.SelectPrev()
		case IsDown(msg):
			m = m.SelectNext()
		case keyMatches(msg, "home"):
			m = m.SelectFirst()
		case keyMatches(msg, "end"):
			m = m.SelectLast()
		case keyMatches(msg, "pgup"):
			m = m.PageUp()
		case keyMatches(msg, "pgdown"):
			m = m.PageDown()
		}
	}

	return m, nil
}

// keyMatches checks if a key message matches a specific key string.
func keyMatches(msg tea.KeyMsg, k string) bool {
	return msg.String() == k
}

// View renders the list.
func (m ListModel) View() string {
	if len(m.items) == 0 {
		return EmptyStateStyle.Render("No items")
	}

	visibleCount := m.visibleItemCount()
	start := m.offset
	end := min(start+visibleCount, len(m.items))

	var lines []string
	for i := start; i < end; i++ {
		item := m.items[i]
		line := m.renderItem(item, i == m.selectedIndex)
		lines = append(lines, line)
	}

	// Add scroll indicators if needed
	result := strings.Join(lines, "\n")

	if len(m.items) > visibleCount {
		// Show scroll position indicator
		indicator := m.renderScrollIndicator()
		result = result + "\n" + indicator
	}

	return result
}

// renderItem renders a single list item.
func (m ListModel) renderItem(item ListItem, selected bool) string {
	style := ListItemStyle
	prefix := "  "

	if selected {
		style = ListItemSelectedStyle
		prefix = "> "
	}

	title := Truncate(item.Title(), m.width-4)
	line := prefix + style.Render(title)

	if m.showDescriptions && item.Description() != "" {
		desc := Truncate(item.Description(), m.width-6)
		line += "\n    " + ListItemDimStyle.Render(desc)
	}

	return line
}

// renderScrollIndicator renders a scroll position indicator.
func (m ListModel) renderScrollIndicator() string {
	if len(m.items) == 0 {
		return ""
	}

	pos := m.selectedIndex + 1
	total := len(m.items)
	indicator := MutedStyle.Render(strings.Repeat(" ", m.width-10))
	indicator += MutedStyle.Render("[")
	indicator += CountStyle.Render(fmt.Sprintf("%d/%d", pos, total))
	indicator += MutedStyle.Render("]")

	return indicator
}

// visibleItemCount returns the number of items that can be displayed.
func (m ListModel) visibleItemCount() int {
	itemHeight := 1
	if m.showDescriptions {
		itemHeight = 2
	}
	return max(m.height/itemHeight, 1)
}

// updateOffset ensures the selected item is visible.
func (m *ListModel) updateOffset() {
	if len(m.items) == 0 {
		m.offset = 0
		return
	}

	visible := m.visibleItemCount()

	// Ensure selected item is visible
	if m.selectedIndex < m.offset {
		m.offset = m.selectedIndex
	} else if m.selectedIndex >= m.offset+visible {
		m.offset = m.selectedIndex - visible + 1
	}

	// Clamp offset
	m.offset = ClampInt(m.offset, 0, max(len(m.items)-visible, 0))
}

// SelectNext moves selection to the next item.
func (m ListModel) SelectNext() ListModel {
	if len(m.items) == 0 {
		return m
	}

	m.selectedIndex++
	if m.selectedIndex >= len(m.items) {
		if m.wrap {
			m.selectedIndex = 0
		} else {
			m.selectedIndex = len(m.items) - 1
		}
	}
	m.updateOffset()
	return m
}

// SelectPrev moves selection to the previous item.
func (m ListModel) SelectPrev() ListModel {
	if len(m.items) == 0 {
		return m
	}

	m.selectedIndex--
	if m.selectedIndex < 0 {
		if m.wrap {
			m.selectedIndex = len(m.items) - 1
		} else {
			m.selectedIndex = 0
		}
	}
	m.updateOffset()
	return m
}

// SelectFirst moves selection to the first item.
func (m ListModel) SelectFirst() ListModel {
	m.selectedIndex = 0
	m.updateOffset()
	return m
}

// SelectLast moves selection to the last item.
func (m ListModel) SelectLast() ListModel {
	if len(m.items) > 0 {
		m.selectedIndex = len(m.items) - 1
	}
	m.updateOffset()
	return m
}

// Select moves selection to a specific index.
func (m ListModel) Select(index int) ListModel {
	if len(m.items) == 0 {
		return m
	}
	m.selectedIndex = ClampInt(index, 0, len(m.items)-1)
	m.updateOffset()
	return m
}

// PageUp moves selection up by one page.
func (m ListModel) PageUp() ListModel {
	visible := m.visibleItemCount()
	m.selectedIndex = max(m.selectedIndex-visible, 0)
	m.updateOffset()
	return m
}

// PageDown moves selection down by one page.
func (m ListModel) PageDown() ListModel {
	visible := m.visibleItemCount()
	m.selectedIndex = min(m.selectedIndex+visible, len(m.items)-1)
	m.updateOffset()
	return m
}

// SelectedItem returns the currently selected item, or nil if empty.
func (m ListModel) SelectedItem() ListItem {
	if len(m.items) == 0 || m.selectedIndex >= len(m.items) {
		return nil
	}
	return m.items[m.selectedIndex]
}

// SelectedIndex returns the index of the selected item.
func (m ListModel) SelectedIndex() int {
	return m.selectedIndex
}

// Items returns all items in the list.
func (m ListModel) Items() []ListItem {
	return m.items
}

// Len returns the number of items.
func (m ListModel) Len() int {
	return len(m.items)
}

// IsEmpty returns true if the list has no items.
func (m ListModel) IsEmpty() bool {
	return len(m.items) == 0
}

package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestSimpleListItem(t *testing.T) {
	item := SimpleListItem{
		ItemTitle:       "Test Title",
		ItemDescription: "Test Description",
	}

	assert.Equal(t, "Test Title", item.Title())
	assert.Equal(t, "Test Description", item.Description())
	assert.Equal(t, "Test Title", item.FilterValue())
}

func TestNewList(t *testing.T) {
	cfg := ListConfig{
		Width:            50,
		Height:           20,
		ShowDescriptions: true,
		Wrap:             true,
	}

	list := NewList(cfg)
	assert.Equal(t, 50, list.width)
	assert.Equal(t, 20, list.height)
	assert.True(t, list.showDescriptions)
	assert.True(t, list.wrap)
	assert.Empty(t, list.items)
}

func TestDefaultListConfig(t *testing.T) {
	cfg := DefaultListConfig()
	assert.Equal(t, 40, cfg.Width)
	assert.Equal(t, 10, cfg.Height)
	assert.True(t, cfg.ShowDescriptions)
	assert.False(t, cfg.Wrap)
}

func TestListModel_SetItems(t *testing.T) {
	list := NewList(DefaultListConfig())
	items := []ListItem{
		SimpleListItem{ItemTitle: "Item 1"},
		SimpleListItem{ItemTitle: "Item 2"},
	}

	list = list.SetItems(items)
	assert.Equal(t, 2, list.Len())
	assert.False(t, list.IsEmpty())
}

func TestListModel_SetWidth(t *testing.T) {
	list := NewList(DefaultListConfig())
	list = list.SetWidth(60)
	assert.Equal(t, 60, list.width)
}

func TestListModel_SetHeight(t *testing.T) {
	list := NewList(DefaultListConfig())
	list = list.SetHeight(15)
	assert.Equal(t, 15, list.height)
}

func TestListModel_SetShowDescriptions(t *testing.T) {
	list := NewList(DefaultListConfig())
	list = list.SetShowDescriptions(false)
	assert.False(t, list.showDescriptions)
}

func TestListModel_SetWrap(t *testing.T) {
	list := NewList(DefaultListConfig())
	list = list.SetWrap(true)
	assert.True(t, list.wrap)
}

func TestListModel_SelectNext(t *testing.T) {
	list := NewList(DefaultListConfig()).SetItems([]ListItem{
		SimpleListItem{ItemTitle: "Item 1"},
		SimpleListItem{ItemTitle: "Item 2"},
		SimpleListItem{ItemTitle: "Item 3"},
	})

	assert.Equal(t, 0, list.SelectedIndex())

	list = list.SelectNext()
	assert.Equal(t, 1, list.SelectedIndex())

	list = list.SelectNext()
	assert.Equal(t, 2, list.SelectedIndex())

	// Without wrap, stays at end
	list = list.SelectNext()
	assert.Equal(t, 2, list.SelectedIndex())
}

func TestListModel_SelectNext_WithWrap(t *testing.T) {
	list := NewList(DefaultListConfig()).
		SetWrap(true).
		SetItems([]ListItem{
			SimpleListItem{ItemTitle: "Item 1"},
			SimpleListItem{ItemTitle: "Item 2"},
		})

	list = list.SelectNext()
	assert.Equal(t, 1, list.SelectedIndex())

	// With wrap, goes back to start
	list = list.SelectNext()
	assert.Equal(t, 0, list.SelectedIndex())
}

func TestListModel_SelectPrev(t *testing.T) {
	list := NewList(DefaultListConfig()).SetItems([]ListItem{
		SimpleListItem{ItemTitle: "Item 1"},
		SimpleListItem{ItemTitle: "Item 2"},
		SimpleListItem{ItemTitle: "Item 3"},
	}).Select(2)

	list = list.SelectPrev()
	assert.Equal(t, 1, list.SelectedIndex())

	list = list.SelectPrev()
	assert.Equal(t, 0, list.SelectedIndex())

	// Without wrap, stays at start
	list = list.SelectPrev()
	assert.Equal(t, 0, list.SelectedIndex())
}

func TestListModel_SelectPrev_WithWrap(t *testing.T) {
	list := NewList(DefaultListConfig()).
		SetWrap(true).
		SetItems([]ListItem{
			SimpleListItem{ItemTitle: "Item 1"},
			SimpleListItem{ItemTitle: "Item 2"},
		})

	// With wrap, goes to end from start
	list = list.SelectPrev()
	assert.Equal(t, 1, list.SelectedIndex())
}

func TestListModel_SelectFirst(t *testing.T) {
	list := NewList(DefaultListConfig()).SetItems([]ListItem{
		SimpleListItem{ItemTitle: "Item 1"},
		SimpleListItem{ItemTitle: "Item 2"},
		SimpleListItem{ItemTitle: "Item 3"},
	}).Select(2)

	list = list.SelectFirst()
	assert.Equal(t, 0, list.SelectedIndex())
}

func TestListModel_SelectLast(t *testing.T) {
	list := NewList(DefaultListConfig()).SetItems([]ListItem{
		SimpleListItem{ItemTitle: "Item 1"},
		SimpleListItem{ItemTitle: "Item 2"},
		SimpleListItem{ItemTitle: "Item 3"},
	})

	list = list.SelectLast()
	assert.Equal(t, 2, list.SelectedIndex())
}

func TestListModel_Select(t *testing.T) {
	list := NewList(DefaultListConfig()).SetItems([]ListItem{
		SimpleListItem{ItemTitle: "Item 1"},
		SimpleListItem{ItemTitle: "Item 2"},
		SimpleListItem{ItemTitle: "Item 3"},
	})

	list = list.Select(1)
	assert.Equal(t, 1, list.SelectedIndex())

	// Out of range gets clamped
	list = list.Select(10)
	assert.Equal(t, 2, list.SelectedIndex())

	list = list.Select(-5)
	assert.Equal(t, 0, list.SelectedIndex())
}

func TestListModel_SelectedItem(t *testing.T) {
	items := []ListItem{
		SimpleListItem{ItemTitle: "Item 1"},
		SimpleListItem{ItemTitle: "Item 2"},
	}
	list := NewList(DefaultListConfig()).SetItems(items)

	item := list.SelectedItem()
	assert.Equal(t, "Item 1", item.Title())

	list = list.SelectNext()
	item = list.SelectedItem()
	assert.Equal(t, "Item 2", item.Title())
}

func TestListModel_SelectedItem_Empty(t *testing.T) {
	list := NewList(DefaultListConfig())
	item := list.SelectedItem()
	assert.Nil(t, item)
}

func TestListModel_View(t *testing.T) {
	list := NewList(DefaultListConfig()).SetItems([]ListItem{
		SimpleListItem{ItemTitle: "Item 1", ItemDescription: "Desc 1"},
		SimpleListItem{ItemTitle: "Item 2", ItemDescription: "Desc 2"},
	})

	view := list.View()
	assert.Contains(t, view, "Item 1")
	assert.Contains(t, view, "Item 2")
}

func TestListModel_View_Empty(t *testing.T) {
	list := NewList(DefaultListConfig())
	view := list.View()
	assert.Contains(t, view, "No items")
}

func TestListModel_Update(t *testing.T) {
	list := NewList(DefaultListConfig()).SetItems([]ListItem{
		SimpleListItem{ItemTitle: "Item 1"},
		SimpleListItem{ItemTitle: "Item 2"},
	})

	// Test down key
	downMsg := tea.KeyMsg{Type: tea.KeyDown}
	list, _ = list.Update(downMsg)
	assert.Equal(t, 1, list.SelectedIndex())

	// Test up key
	upMsg := tea.KeyMsg{Type: tea.KeyUp}
	list, _ = list.Update(upMsg)
	assert.Equal(t, 0, list.SelectedIndex())
}

func TestListModel_Update_Empty(t *testing.T) {
	list := NewList(DefaultListConfig())

	// Should handle empty list gracefully
	downMsg := tea.KeyMsg{Type: tea.KeyDown}
	list, _ = list.Update(downMsg)
	assert.Equal(t, 0, list.SelectedIndex())
}

func TestListModel_Items(t *testing.T) {
	items := []ListItem{
		SimpleListItem{ItemTitle: "Item 1"},
		SimpleListItem{ItemTitle: "Item 2"},
	}
	list := NewList(DefaultListConfig()).SetItems(items)

	assert.Equal(t, items, list.Items())
}

func TestListModel_Len(t *testing.T) {
	list := NewList(DefaultListConfig()).SetItems([]ListItem{
		SimpleListItem{ItemTitle: "Item 1"},
		SimpleListItem{ItemTitle: "Item 2"},
		SimpleListItem{ItemTitle: "Item 3"},
	})

	assert.Equal(t, 3, list.Len())
}

func TestListModel_IsEmpty(t *testing.T) {
	list := NewList(DefaultListConfig())
	assert.True(t, list.IsEmpty())

	list = list.SetItems([]ListItem{SimpleListItem{ItemTitle: "Item 1"}})
	assert.False(t, list.IsEmpty())
}

func TestListModel_PageUp(t *testing.T) {
	// Create list with more items than visible
	items := make([]ListItem, 20)
	for i := range items {
		items[i] = SimpleListItem{ItemTitle: "Item"}
	}

	list := NewList(ListConfig{
		Width:            40,
		Height:           5,
		ShowDescriptions: false,
	}).SetItems(items).Select(15)

	list = list.PageUp()
	// Should move up by approximately one page
	assert.Less(t, list.SelectedIndex(), 15)
}

func TestListModel_PageDown(t *testing.T) {
	items := make([]ListItem, 20)
	for i := range items {
		items[i] = SimpleListItem{ItemTitle: "Item"}
	}

	list := NewList(ListConfig{
		Width:            40,
		Height:           5,
		ShowDescriptions: false,
	}).SetItems(items)

	list = list.PageDown()
	// Should move down by approximately one page
	assert.Greater(t, list.SelectedIndex(), 0)
}

func TestListModel_SetItems_AdjustsSelection(t *testing.T) {
	// Start with 5 items, select last one
	items := make([]ListItem, 5)
	for i := range items {
		items[i] = SimpleListItem{ItemTitle: "Item"}
	}

	list := NewList(DefaultListConfig()).SetItems(items).Select(4)
	assert.Equal(t, 4, list.SelectedIndex())

	// Reduce to 2 items, selection should be clamped
	newItems := items[:2]
	list = list.SetItems(newItems)
	assert.Equal(t, 1, list.SelectedIndex())
}

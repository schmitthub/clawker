package storeui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListEditor_ParsesCommaSeparated(t *testing.T) {
	m := newListEditor("packages", "git, curl, ripgrep")
	assert.Equal(t, []string{"git", "curl", "ripgrep"}, m.items)
}

func TestListEditor_EmptyValue(t *testing.T) {
	m := newListEditor("packages", "")
	assert.Empty(t, m.items)
}

func TestListEditor_AddItem(t *testing.T) {
	m := newListEditor("packages", "git")

	// Press 'a' to start adding.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	assert.Equal(t, listAdding, m.state)

	// Type "curl".
	for _, r := range "curl" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	// Press Enter to confirm.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, listBrowsing, m.state)
	assert.Equal(t, []string{"git", "curl"}, m.items)
}

func TestListEditor_EditItem(t *testing.T) {
	m := newListEditor("packages", "git, curl")

	// Press 'e' to edit first item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	assert.Equal(t, listEditing, m.state)

	// Clear and type "wget".
	m.input.SetValue("wget")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.Equal(t, listBrowsing, m.state)
	assert.Equal(t, "wget", m.items[0])
}

func TestListEditor_DeleteItem(t *testing.T) {
	m := newListEditor("packages", "git, curl, ripgrep")

	// Delete first item.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	assert.Equal(t, []string{"curl", "ripgrep"}, m.items)
}

func TestListEditor_NavigateUpDown(t *testing.T) {
	m := newListEditor("packages", "a, b, c")
	assert.Equal(t, 0, m.cursor)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, 1, m.cursor)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 0, m.cursor)

	// Can't go above 0.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 0, m.cursor)
}

func TestListEditor_EnterConfirms(t *testing.T) {
	m := newListEditor("packages", "git")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, m.IsConfirmed())
	assert.Equal(t, "git", m.Value())
}

func TestListEditor_EscCancels(t *testing.T) {
	m := newListEditor("packages", "git")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.True(t, m.IsCancelled())
}

func TestListEditor_EscFromEditReturnsToBrowse(t *testing.T) {
	m := newListEditor("packages", "git")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	require.Equal(t, listEditing, m.state)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, listBrowsing, m.state)
	assert.Equal(t, "git", m.items[0]) // unchanged
}

func TestListEditor_DeleteLastItemClampsCursor(t *testing.T) {
	m := newListEditor("packages", "a, b")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // cursor = 1
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	assert.Equal(t, 0, m.cursor) // clamped to 0
	assert.Equal(t, []string{"a"}, m.items)
}

func TestListEditor_ViewShowsItems(t *testing.T) {
	m := newListEditor("packages", "git, curl")
	view := m.View()
	assert.Contains(t, view, "git")
	assert.Contains(t, view, "curl")
	assert.Contains(t, view, "packages")
}

package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestDefaultKeyMap(t *testing.T) {
	km := DefaultKeyMap()

	// Verify all key bindings are defined
	assert.NotEmpty(t, km.Quit.Keys())
	assert.NotEmpty(t, km.Up.Keys())
	assert.NotEmpty(t, km.Down.Keys())
	assert.NotEmpty(t, km.Left.Keys())
	assert.NotEmpty(t, km.Right.Keys())
	assert.NotEmpty(t, km.Enter.Keys())
	assert.NotEmpty(t, km.Escape.Keys())
	assert.NotEmpty(t, km.Help.Keys())
	assert.NotEmpty(t, km.Tab.Keys())
}

func TestIsQuit(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		isQuit bool
	}{
		{"q key", "q", true},
		{"ctrl+c", "ctrl+c", true},
		{"other key", "a", false},
		{"enter", "enter", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tt.key)}
			if tt.key == "ctrl+c" {
				msg = tea.KeyMsg{Type: tea.KeyCtrlC}
			} else if tt.key == "enter" {
				msg = tea.KeyMsg{Type: tea.KeyEnter}
			} else if tt.key == "q" {
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
			} else if tt.key == "a" {
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
			}
			assert.Equal(t, tt.isQuit, IsQuit(msg))
		})
	}
}

func TestIsUp(t *testing.T) {
	tests := []struct {
		name string
		key  string
		isUp bool
	}{
		{"up arrow", "up", true},
		{"k key", "k", true},
		{"other key", "a", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var msg tea.KeyMsg
			if tt.key == "up" {
				msg = tea.KeyMsg{Type: tea.KeyUp}
			} else if tt.key == "k" {
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}
			} else {
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tt.key)}
			}
			assert.Equal(t, tt.isUp, IsUp(msg))
		})
	}
}

func TestIsDown(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		isDown bool
	}{
		{"down arrow", "down", true},
		{"j key", "j", true},
		{"other key", "a", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var msg tea.KeyMsg
			if tt.key == "down" {
				msg = tea.KeyMsg{Type: tea.KeyDown}
			} else if tt.key == "j" {
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}
			} else {
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tt.key)}
			}
			assert.Equal(t, tt.isDown, IsDown(msg))
		})
	}
}

func TestIsEnter(t *testing.T) {
	enterMsg := tea.KeyMsg{Type: tea.KeyEnter}
	otherMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}

	assert.True(t, IsEnter(enterMsg))
	assert.False(t, IsEnter(otherMsg))
}

func TestIsEscape(t *testing.T) {
	escMsg := tea.KeyMsg{Type: tea.KeyEsc}
	otherMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}

	assert.True(t, IsEscape(escMsg))
	assert.False(t, IsEscape(otherMsg))
}

func TestIsHelp(t *testing.T) {
	helpMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}
	otherMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}

	assert.True(t, IsHelp(helpMsg))
	assert.False(t, IsHelp(otherMsg))
}

func TestIsTab(t *testing.T) {
	tabMsg := tea.KeyMsg{Type: tea.KeyTab}
	otherMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}

	assert.True(t, IsTab(tabMsg))
	assert.False(t, IsTab(otherMsg))
}

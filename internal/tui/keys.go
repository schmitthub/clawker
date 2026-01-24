package tui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// KeyMap defines common key bindings used across TUI components.
type KeyMap struct {
	Quit   key.Binding
	Up     key.Binding
	Down   key.Binding
	Left   key.Binding
	Right  key.Binding
	Enter  key.Binding
	Escape key.Binding
	Help   key.Binding
	Tab    key.Binding
}

// DefaultKeyMap returns the default key bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("up/k", "move up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("down/j", "move down"),
		),
		Left: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("left/h", "move left"),
		),
		Right: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("right/l", "move right"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
		Escape: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next"),
		),
	}
}

// Key matching helpers for use in Update functions.

// IsQuit returns true if the key message matches quit keys.
func IsQuit(msg tea.KeyMsg) bool {
	return key.Matches(msg, DefaultKeyMap().Quit)
}

// IsUp returns true if the key message matches up navigation keys.
func IsUp(msg tea.KeyMsg) bool {
	return key.Matches(msg, DefaultKeyMap().Up)
}

// IsDown returns true if the key message matches down navigation keys.
func IsDown(msg tea.KeyMsg) bool {
	return key.Matches(msg, DefaultKeyMap().Down)
}

// IsLeft returns true if the key message matches left navigation keys.
func IsLeft(msg tea.KeyMsg) bool {
	return key.Matches(msg, DefaultKeyMap().Left)
}

// IsRight returns true if the key message matches right navigation keys.
func IsRight(msg tea.KeyMsg) bool {
	return key.Matches(msg, DefaultKeyMap().Right)
}

// IsEnter returns true if the key message matches enter key.
func IsEnter(msg tea.KeyMsg) bool {
	return key.Matches(msg, DefaultKeyMap().Enter)
}

// IsEscape returns true if the key message matches escape key.
func IsEscape(msg tea.KeyMsg) bool {
	return key.Matches(msg, DefaultKeyMap().Escape)
}

// IsHelp returns true if the key message matches help key.
func IsHelp(msg tea.KeyMsg) bool {
	return key.Matches(msg, DefaultKeyMap().Help)
}

// IsTab returns true if the key message matches tab key.
func IsTab(msg tea.KeyMsg) bool {
	return key.Matches(msg, DefaultKeyMap().Tab)
}

package tui

import tea "github.com/charmbracelet/bubbletea"

// FieldEditor is the contract for custom field editors plugged into
// [FieldBrowserModel] via the [BrowserField.Editor] factory.
//
// All built-in editors (ListEditorModel, TextareaEditorModel, KVEditorModel,
// ItemListEditorModel) satisfy this interface. Domain adapters can implement
// their own editors as well.
type FieldEditor interface {
	tea.Model
	Value() string     // Edited value as a string (YAML for complex types).
	IsConfirmed() bool // True when the user accepted the edit.
	IsCancelled() bool // True when the user cancelled the edit.
	Err() string       // Current validation error message, or "" if none.
}

// StructFieldDef describes a single field within a struct, used by
// [FormEditorModel] and [ItemListEditorModel] to display and edit
// struct members without domain knowledge.
type StructFieldDef struct {
	Key   string // YAML key (e.g. "cmd", "alpine")
	Label string // Human-readable label (e.g. "Command", "Alpine")
}

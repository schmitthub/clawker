package config

import (
	"testing"

	"github.com/schmitthub/clawker/internal/storeui"
	"github.com/stretchr/testify/assert"
)

// --- Schema interface enforcement ---

func TestProjectFields_AllFieldsHaveDescriptions(t *testing.T) {
	fs := Project{}.Fields()
	for _, f := range fs.All() {
		assert.NotEmptyf(t, f.Description(), "field %q has no desc tag", f.Path())
	}
}

func TestSettingsFields_AllFieldsHaveDescriptions(t *testing.T) {
	fs := Settings{}.Fields()
	for _, f := range fs.All() {
		assert.NotEmptyf(t, f.Description(), "field %q has no desc tag", f.Path())
	}
}

// --- Parity with storeui.WalkFields ---
// TODO: Remove when storeui.WalkFields is deprecated (Task 4 of storage-schema-contract initiative).

func TestProjectFields_ParityWithWalkFields(t *testing.T) {
	walked := storeui.WalkFields(Project{})
	schema := Project{}.Fields()

	walkedPaths := make(map[string]bool, len(walked))
	for _, f := range walked {
		walkedPaths[f.Path] = true
	}

	schemaPaths := make(map[string]bool, schema.Len())
	for _, f := range schema.All() {
		schemaPaths[f.Path()] = true
	}

	assert.Equal(t, walkedPaths, schemaPaths,
		"NormalizeFields and WalkFields should discover identical field paths")
}

func TestSettingsFields_ParityWithWalkFields(t *testing.T) {
	walked := storeui.WalkFields(Settings{})
	schema := Settings{}.Fields()

	walkedPaths := make(map[string]bool, len(walked))
	for _, f := range walked {
		walkedPaths[f.Path] = true
	}

	schemaPaths := make(map[string]bool, schema.Len())
	for _, f := range schema.All() {
		schemaPaths[f.Path()] = true
	}

	assert.Equal(t, walkedPaths, schemaPaths,
		"NormalizeFields and WalkFields should discover identical field paths")
}

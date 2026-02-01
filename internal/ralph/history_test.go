package ralph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryStore_SessionHistory(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	store := NewHistoryStore(tmpDir)

	project := "test-project"
	agent := "test-agent"

	// Load empty History
	history, err := store.LoadSessionHistory(project, agent)
	require.NoError(t, err)
	assert.Equal(t, project, history.Project)
	assert.Equal(t, agent, history.Agent)
	assert.Empty(t, history.Entries)

	// Add entry
	err = store.AddSessionEntry(project, agent, "created", StatusPending, "", 0)
	require.NoError(t, err)

	// Load and verify
	history, err = store.LoadSessionHistory(project, agent)
	require.NoError(t, err)
	assert.Len(t, history.Entries, 1)
	assert.Equal(t, "created", history.Entries[0].Event)
	assert.Equal(t, StatusPending, history.Entries[0].Status)

	// Add more entries
	err = store.AddSessionEntry(project, agent, "updated", StatusPending, "", 1)
	require.NoError(t, err)
	err = store.AddSessionEntry(project, agent, "updated", StatusComplete, "", 2)
	require.NoError(t, err)

	// Verify all entries
	history, err = store.LoadSessionHistory(project, agent)
	require.NoError(t, err)
	assert.Len(t, history.Entries, 3)
}

func TestHistoryStore_SessionHistoryTrimming(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewHistoryStore(tmpDir)

	project := "test-project"
	agent := "test-agent"

	// Add more than MaxHistoryEntries
	for i := 0; i < MaxHistoryEntries+10; i++ {
		err := store.AddSessionEntry(project, agent, "updated", StatusPending, "", i)
		require.NoError(t, err)
	}

	// Verify trimmed to max
	history, err := store.LoadSessionHistory(project, agent)
	require.NoError(t, err)
	assert.Len(t, history.Entries, MaxHistoryEntries)

	// Verify oldest entries were removed (first entry should be loop 10)
	assert.Equal(t, 10, history.Entries[0].LoopCount)
}

func TestHistoryStore_CircuitHistory(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewHistoryStore(tmpDir)

	project := "test-project"
	agent := "test-agent"

	// Load empty History
	history, err := store.LoadCircuitHistory(project, agent)
	require.NoError(t, err)
	assert.Equal(t, project, history.Project)
	assert.Equal(t, agent, history.Agent)
	assert.Empty(t, history.Entries)

	// Add entry
	err = store.AddCircuitEntry(project, agent, "closed", "tripped", "no progress", 3, 0, 0, 0)
	require.NoError(t, err)

	// Load and verify
	history, err = store.LoadCircuitHistory(project, agent)
	require.NoError(t, err)
	assert.Len(t, history.Entries, 1)
	assert.Equal(t, "closed", history.Entries[0].FromState)
	assert.Equal(t, "tripped", history.Entries[0].ToState)
	assert.Equal(t, "no progress", history.Entries[0].Reason)
	assert.Equal(t, 3, history.Entries[0].NoProgressCount)

	// Add reset entry
	err = store.AddCircuitEntry(project, agent, "tripped", "closed", "manual reset", 0, 0, 0, 0)
	require.NoError(t, err)

	// Verify
	history, err = store.LoadCircuitHistory(project, agent)
	require.NoError(t, err)
	assert.Len(t, history.Entries, 2)
	assert.Equal(t, "manual reset", history.Entries[1].Reason)
}

func TestHistoryStore_CircuitHistoryTrimming(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewHistoryStore(tmpDir)

	project := "test-project"
	agent := "test-agent"

	// Add more than MaxHistoryEntries
	for i := 0; i < MaxHistoryEntries+10; i++ {
		err := store.AddCircuitEntry(project, agent, "closed", "tripped", "test", i, 0, 0, 0)
		require.NoError(t, err)
	}

	// Verify trimmed to max
	history, err := store.LoadCircuitHistory(project, agent)
	require.NoError(t, err)
	assert.Len(t, history.Entries, MaxHistoryEntries)

	// Verify oldest entries were removed (first entry should have count 10)
	assert.Equal(t, 10, history.Entries[0].NoProgressCount)
}

func TestHistoryStore_DeleteSessionHistory(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewHistoryStore(tmpDir)

	project := "test-project"
	agent := "test-agent"

	// Add entry
	err := store.AddSessionEntry(project, agent, "created", StatusPending, "", 0)
	require.NoError(t, err)

	// Verify file exists
	path := filepath.Join(tmpDir, project+"."+agent+".session.json")
	_, err = os.Stat(path)
	require.NoError(t, err)

	// Delete
	err = store.DeleteSessionHistory(project, agent)
	require.NoError(t, err)

	// Verify file deleted
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestHistoryStore_DeleteCircuitHistory(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewHistoryStore(tmpDir)

	project := "test-project"
	agent := "test-agent"

	// Add entry
	err := store.AddCircuitEntry(project, agent, "closed", "tripped", "test", 3, 0, 0, 0)
	require.NoError(t, err)

	// Verify file exists
	path := filepath.Join(tmpDir, project+"."+agent+".circuit.json")
	_, err = os.Stat(path)
	require.NoError(t, err)

	// Delete
	err = store.DeleteCircuitHistory(project, agent)
	require.NoError(t, err)

	// Verify file deleted
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestHistoryStore_DeleteNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewHistoryStore(tmpDir)

	// Delete non-existent should not error
	err := store.DeleteSessionHistory("nonexistent", "agent")
	assert.NoError(t, err)

	err = store.DeleteCircuitHistory("nonexistent", "agent")
	assert.NoError(t, err)
}

func TestHistoryStore_EntryWithError(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewHistoryStore(tmpDir)

	project := "test-project"
	agent := "test-agent"

	// Add entry with error
	err := store.AddSessionEntry(project, agent, "updated", StatusPending, "something went wrong", 5)
	require.NoError(t, err)

	// Verify error is stored
	history, err := store.LoadSessionHistory(project, agent)
	require.NoError(t, err)
	assert.Equal(t, "something went wrong", history.Entries[0].Error)
}

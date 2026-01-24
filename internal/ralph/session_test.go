package ralph

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionStore_SaveLoadSession(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	store := NewSessionStore(tmpDir)

	// Create session
	session := NewSession("test-project", "test-agent", "Fix the bug")
	session.LoopsCompleted = 5
	session.TotalTasksCompleted = 10
	session.TotalFilesModified = 3
	session.Status = StatusPending

	// Save
	err := store.SaveSession(session)
	require.NoError(t, err)

	// Load
	loaded, err := store.LoadSession("test-project", "test-agent")
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, session.Project, loaded.Project)
	assert.Equal(t, session.Agent, loaded.Agent)
	assert.Equal(t, session.LoopsCompleted, loaded.LoopsCompleted)
	assert.Equal(t, session.TotalTasksCompleted, loaded.TotalTasksCompleted)
	assert.Equal(t, session.TotalFilesModified, loaded.TotalFilesModified)
	assert.Equal(t, session.InitialPrompt, loaded.InitialPrompt)
}

func TestSessionStore_LoadSession_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewSessionStore(tmpDir)

	loaded, err := store.LoadSession("nonexistent", "agent")
	require.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestSessionStore_DeleteSession(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewSessionStore(tmpDir)

	// Save a session
	session := NewSession("test-project", "test-agent", "")
	err := store.SaveSession(session)
	require.NoError(t, err)

	// Verify it exists
	loaded, err := store.LoadSession("test-project", "test-agent")
	require.NoError(t, err)
	require.NotNil(t, loaded)

	// Delete
	err = store.DeleteSession("test-project", "test-agent")
	require.NoError(t, err)

	// Verify deleted
	loaded, err = store.LoadSession("test-project", "test-agent")
	require.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestSessionStore_SaveLoadCircuitState(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewSessionStore(tmpDir)

	now := time.Now()
	state := &CircuitState{
		Project:         "test-project",
		Agent:           "test-agent",
		NoProgressCount: 2,
		Tripped:         true,
		TripReason:      "no progress",
		TrippedAt:       &now,
	}

	// Save
	err := store.SaveCircuitState(state)
	require.NoError(t, err)

	// Load
	loaded, err := store.LoadCircuitState("test-project", "test-agent")
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, state.Project, loaded.Project)
	assert.Equal(t, state.Agent, loaded.Agent)
	assert.Equal(t, state.NoProgressCount, loaded.NoProgressCount)
	assert.Equal(t, state.Tripped, loaded.Tripped)
	assert.Equal(t, state.TripReason, loaded.TripReason)
}

func TestSessionStore_LoadCircuitState_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewSessionStore(tmpDir)

	loaded, err := store.LoadCircuitState("nonexistent", "agent")
	require.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestSessionStore_DeleteCircuitState(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewSessionStore(tmpDir)

	// Save state
	state := &CircuitState{
		Project: "test-project",
		Agent:   "test-agent",
		Tripped: true,
	}
	err := store.SaveCircuitState(state)
	require.NoError(t, err)

	// Delete
	err = store.DeleteCircuitState("test-project", "test-agent")
	require.NoError(t, err)

	// Verify deleted
	loaded, err := store.LoadCircuitState("test-project", "test-agent")
	require.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestNewSession(t *testing.T) {
	before := time.Now()
	session := NewSession("myproject", "myagent", "Do the thing")
	after := time.Now()

	assert.Equal(t, "myproject", session.Project)
	assert.Equal(t, "myagent", session.Agent)
	assert.Equal(t, "Do the thing", session.InitialPrompt)
	assert.Equal(t, StatusPending, session.Status)
	assert.True(t, session.StartedAt.After(before) || session.StartedAt.Equal(before))
	assert.True(t, session.StartedAt.Before(after) || session.StartedAt.Equal(after))
}

func TestSession_Update(t *testing.T) {
	session := NewSession("project", "agent", "")

	// Update with progress
	status := &Status{
		Status:         StatusPending,
		TasksCompleted: 3,
		FilesModified:  5,
	}
	session.Update(status, nil)

	assert.Equal(t, 1, session.LoopsCompleted)
	assert.Equal(t, 3, session.TotalTasksCompleted)
	assert.Equal(t, 5, session.TotalFilesModified)
	assert.Equal(t, 0, session.NoProgressCount)
	assert.Empty(t, session.LastError)

	// Update without progress
	noProgress := &Status{Status: StatusPending}
	session.Update(noProgress, nil)

	assert.Equal(t, 2, session.LoopsCompleted)
	assert.Equal(t, 3, session.TotalTasksCompleted) // Unchanged
	assert.Equal(t, 5, session.TotalFilesModified)  // Unchanged
	assert.Equal(t, 1, session.NoProgressCount)

	// Update with error
	session.Update(nil, assert.AnError)
	assert.Equal(t, 3, session.LoopsCompleted)
	assert.Equal(t, assert.AnError.Error(), session.LastError)
	assert.Equal(t, 2, session.NoProgressCount)
}

func TestSessionStore_DirectoryCreation(t *testing.T) {
	tmpDir := t.TempDir()
	deepDir := filepath.Join(tmpDir, "deep", "nested", "path")
	store := NewSessionStore(deepDir)

	session := NewSession("project", "agent", "")
	err := store.SaveSession(session)
	require.NoError(t, err)

	// Verify directory was created
	sessionDir := filepath.Join(deepDir, "sessions")
	_, err = os.Stat(sessionDir)
	require.NoError(t, err)
}

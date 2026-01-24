package ralph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Session represents the persistent state of a Ralph loop session.
type Session struct {
	// Project is the clawker project name.
	Project string `json:"project"`

	// Agent is the agent name.
	Agent string `json:"agent"`

	// StartedAt is when the session started.
	StartedAt time.Time `json:"started_at"`

	// UpdatedAt is when the session was last updated.
	UpdatedAt time.Time `json:"updated_at"`

	// LoopsCompleted is the number of loops completed.
	LoopsCompleted int `json:"loops_completed"`

	// Status is the last known status.
	Status string `json:"status"`

	// NoProgressCount is the current count without progress.
	NoProgressCount int `json:"no_progress_count"`

	// TotalTasksCompleted is the cumulative tasks completed.
	TotalTasksCompleted int `json:"total_tasks_completed"`

	// TotalFilesModified is the cumulative files modified.
	TotalFilesModified int `json:"total_files_modified"`

	// LastError is the last error message, if any.
	LastError string `json:"last_error,omitempty"`

	// InitialPrompt is the prompt that started the session.
	InitialPrompt string `json:"initial_prompt,omitempty"`
}

// CircuitState represents the persistent state of the circuit breaker.
type CircuitState struct {
	Project         string     `json:"project"`
	Agent           string     `json:"agent"`
	NoProgressCount int        `json:"no_progress_count"`
	Tripped         bool       `json:"tripped"`
	TripReason      string     `json:"trip_reason,omitempty"`
	TrippedAt       *time.Time `json:"tripped_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// SessionStore manages session and circuit breaker persistence.
type SessionStore struct {
	baseDir string
}

// NewSessionStore creates a new session store at the given base directory.
func NewSessionStore(baseDir string) *SessionStore {
	return &SessionStore{baseDir: baseDir}
}

// DefaultSessionStore returns a session store using the default clawker directory.
func DefaultSessionStore() (*SessionStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	baseDir := filepath.Join(home, ".local", "clawker", "ralph")
	return NewSessionStore(baseDir), nil
}

// sessionPath returns the path for a session file.
func (s *SessionStore) sessionPath(project, agent string) string {
	return filepath.Join(s.baseDir, "sessions", fmt.Sprintf("%s.%s.json", project, agent))
}

// circuitPath returns the path for a circuit breaker state file.
func (s *SessionStore) circuitPath(project, agent string) string {
	return filepath.Join(s.baseDir, "circuit", fmt.Sprintf("%s.%s.json", project, agent))
}

// LoadSession loads a session from disk.
// Returns nil if no session exists.
func (s *SessionStore) LoadSession(project, agent string) (*Session, error) {
	path := s.sessionPath(project, agent)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read session: %w", err)
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("failed to parse session: %w", err)
	}
	return &session, nil
}

// SaveSession saves a session to disk.
func (s *SessionStore) SaveSession(session *Session) error {
	path := s.sessionPath(session.Project, session.Agent)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	session.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write session: %w", err)
	}
	return nil
}

// DeleteSession removes a session from disk.
func (s *SessionStore) DeleteSession(project, agent string) error {
	path := s.sessionPath(project, agent)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

// LoadCircuitState loads circuit breaker state from disk.
// Returns nil if no state exists.
func (s *SessionStore) LoadCircuitState(project, agent string) (*CircuitState, error) {
	path := s.circuitPath(project, agent)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read circuit state: %w", err)
	}

	var state CircuitState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse circuit state: %w", err)
	}
	return &state, nil
}

// SaveCircuitState saves circuit breaker state to disk.
func (s *SessionStore) SaveCircuitState(state *CircuitState) error {
	path := s.circuitPath(state.Project, state.Agent)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create circuit directory: %w", err)
	}

	state.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal circuit state: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write circuit state: %w", err)
	}
	return nil
}

// DeleteCircuitState removes circuit breaker state from disk.
func (s *SessionStore) DeleteCircuitState(project, agent string) error {
	path := s.circuitPath(project, agent)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete circuit state: %w", err)
	}
	return nil
}

// NewSession creates a new session.
func NewSession(project, agent, prompt string) *Session {
	now := time.Now()
	return &Session{
		Project:       project,
		Agent:         agent,
		StartedAt:     now,
		UpdatedAt:     now,
		Status:        StatusPending,
		InitialPrompt: prompt,
	}
}

// Update updates the session with loop results.
func (sess *Session) Update(status *Status, loopErr error) {
	sess.LoopsCompleted++
	sess.UpdatedAt = time.Now()

	if loopErr != nil {
		sess.LastError = loopErr.Error()
	} else {
		sess.LastError = ""
	}

	if status != nil {
		sess.Status = status.Status
		sess.TotalTasksCompleted += status.TasksCompleted
		sess.TotalFilesModified += status.FilesModified
		if !status.HasProgress() {
			sess.NoProgressCount++
		} else {
			sess.NoProgressCount = 0
		}
	} else {
		sess.NoProgressCount++
	}
}

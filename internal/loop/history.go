package loop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// MaxHistoryEntries is the maximum number of history entries to keep.
	MaxHistoryEntries = 50
)

// SessionHistoryEntry represents a session state transition.
type SessionHistoryEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Event     string    `json:"event"` // created, updated, expired, deleted
	LoopCount int       `json:"loop_count"`
	Status    string    `json:"status,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// SessionHistory tracks session lifecycle events.
type SessionHistory struct {
	Project string                `json:"project"`
	Agent   string                `json:"agent"`
	Entries []SessionHistoryEntry `json:"entries"`
}

// CircuitHistoryEntry represents a circuit breaker state change.
type CircuitHistoryEntry struct {
	Timestamp       time.Time `json:"timestamp"`
	FromState       string    `json:"from_state"` // closed, tripped
	ToState         string    `json:"to_state"`
	Reason          string    `json:"reason,omitempty"`
	NoProgressCount int       `json:"no_progress_count"`
	SameErrorCount  int       `json:"same_error_count,omitempty"`
	TestLoopCount   int       `json:"test_loop_count,omitempty"`
	CompletionCount int       `json:"completion_count,omitempty"`
}

// CircuitHistory tracks circuit breaker state changes.
type CircuitHistory struct {
	Project string                `json:"project"`
	Agent   string                `json:"agent"`
	Entries []CircuitHistoryEntry `json:"entries"`
}

// HistoryStore manages history persistence.
type HistoryStore struct {
	baseDir string
}

// NewHistoryStore creates a new history store at the given base directory.
func NewHistoryStore(baseDir string) *HistoryStore {
	return &HistoryStore{baseDir: baseDir}
}

// DefaultHistoryStore returns a history store using the default clawker directory.
func DefaultHistoryStore() (*HistoryStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	baseDir := filepath.Join(home, ".local", "clawker", "loop", "history")
	return NewHistoryStore(baseDir), nil
}

// sessionHistoryPath returns the path for a session history file.
func (h *HistoryStore) sessionHistoryPath(project, agent string) string {
	return filepath.Join(h.baseDir, fmt.Sprintf("%s.%s.session.json", project, agent))
}

// circuitHistoryPath returns the path for a circuit history file.
func (h *HistoryStore) circuitHistoryPath(project, agent string) string {
	return filepath.Join(h.baseDir, fmt.Sprintf("%s.%s.circuit.json", project, agent))
}

// LoadSessionHistory loads session history from disk.
// Returns an empty history if no file exists.
func (h *HistoryStore) LoadSessionHistory(project, agent string) (*SessionHistory, error) {
	path := h.sessionHistoryPath(project, agent)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &SessionHistory{Project: project, Agent: agent, Entries: nil}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read session history: %w", err)
	}

	var history SessionHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("failed to parse session history: %w", err)
	}
	return &history, nil
}

// SaveSessionHistory saves session history to disk.
func (h *HistoryStore) SaveSessionHistory(history *SessionHistory) error {
	path := h.sessionHistoryPath(history.Project, history.Agent)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create history directory: %w", err)
	}

	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session history: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write session history: %w", err)
	}
	return nil
}

// AddSessionEntry adds an entry to session history, trimming to MaxHistoryEntries.
func (h *HistoryStore) AddSessionEntry(project, agent, event, status, errorMsg string, loopCount int) error {
	history, err := h.LoadSessionHistory(project, agent)
	if err != nil {
		return err
	}

	entry := SessionHistoryEntry{
		Timestamp: time.Now(),
		Event:     event,
		LoopCount: loopCount,
		Status:    status,
		Error:     errorMsg,
	}

	history.Entries = append(history.Entries, entry)

	// Trim to max entries
	if len(history.Entries) > MaxHistoryEntries {
		history.Entries = history.Entries[len(history.Entries)-MaxHistoryEntries:]
	}

	return h.SaveSessionHistory(history)
}

// LoadCircuitHistory loads circuit history from disk.
// Returns an empty history if no file exists.
func (h *HistoryStore) LoadCircuitHistory(project, agent string) (*CircuitHistory, error) {
	path := h.circuitHistoryPath(project, agent)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &CircuitHistory{Project: project, Agent: agent, Entries: nil}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read circuit history: %w", err)
	}

	var history CircuitHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("failed to parse circuit history: %w", err)
	}
	return &history, nil
}

// SaveCircuitHistory saves circuit history to disk.
func (h *HistoryStore) SaveCircuitHistory(history *CircuitHistory) error {
	path := h.circuitHistoryPath(history.Project, history.Agent)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create history directory: %w", err)
	}

	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal circuit history: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write circuit history: %w", err)
	}
	return nil
}

// AddCircuitEntry adds an entry to circuit history, trimming to MaxHistoryEntries.
func (h *HistoryStore) AddCircuitEntry(project, agent, fromState, toState, reason string, noProgressCount, sameErrorCount, testLoopCount, completionCount int) error {
	history, err := h.LoadCircuitHistory(project, agent)
	if err != nil {
		return err
	}

	entry := CircuitHistoryEntry{
		Timestamp:       time.Now(),
		FromState:       fromState,
		ToState:         toState,
		Reason:          reason,
		NoProgressCount: noProgressCount,
		SameErrorCount:  sameErrorCount,
		TestLoopCount:   testLoopCount,
		CompletionCount: completionCount,
	}

	history.Entries = append(history.Entries, entry)

	// Trim to max entries
	if len(history.Entries) > MaxHistoryEntries {
		history.Entries = history.Entries[len(history.Entries)-MaxHistoryEntries:]
	}

	return h.SaveCircuitHistory(history)
}

// DeleteSessionHistory removes session history from disk.
func (h *HistoryStore) DeleteSessionHistory(project, agent string) error {
	path := h.sessionHistoryPath(project, agent)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete session history: %w", err)
	}
	return nil
}

// DeleteCircuitHistory removes circuit history from disk.
func (h *HistoryStore) DeleteCircuitHistory(project, agent string) error {
	path := h.circuitHistoryPath(project, agent)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete circuit history: %w", err)
	}
	return nil
}

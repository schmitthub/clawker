// Package hostproxy provides a host-side HTTP server that containers can call
// to perform actions on the host, such as opening URLs in the browser.
package hostproxy

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// SessionIDLength is the number of random bytes used for session IDs.
// 16 bytes = 32 hex characters, providing 128 bits of entropy.
const SessionIDLength = 16

// Session is the base type for all proxy sessions.
// It stores common metadata and provides thread-safe access to session data.
type Session struct {
	ID        string
	Type      string    // e.g., "callback", "webhook", "message"
	CreatedAt time.Time
	ExpiresAt time.Time
	Metadata  map[string]any // Channel-specific data
	mu        sync.RWMutex
}

// GetMetadata safely retrieves a metadata value by key.
func (s *Session) GetMetadata(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.Metadata[key]
	return val, ok
}

// SetMetadata safely sets a metadata value.
func (s *Session) SetMetadata(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Metadata == nil {
		s.Metadata = make(map[string]any)
	}
	s.Metadata[key] = value
}

// IsExpired returns true if the session has passed its expiration time.
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// SessionStore manages sessions across all channels.
// It provides thread-safe create, get, delete, and cleanup operations.
type SessionStore struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	stopCh   chan struct{}
	stopped  bool
}

// NewSessionStore creates a new session store and starts the background
// cleanup goroutine.
func NewSessionStore() *SessionStore {
	store := &SessionStore{
		sessions: make(map[string]*Session),
		stopCh:   make(chan struct{}),
	}
	go store.cleanupLoop()
	return store
}

// Create creates a new session with the given type, TTL, and metadata.
// Returns the created session with a unique cryptographically random ID.
func (s *SessionStore) Create(sessionType string, ttl time.Duration, metadata map[string]any) (*Session, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	session := &Session{
		ID:        id,
		Type:      sessionType,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		Metadata:  metadata,
	}
	if session.Metadata == nil {
		session.Metadata = make(map[string]any)
	}

	s.mu.Lock()
	s.sessions[id] = session
	s.mu.Unlock()

	return session, nil
}

// Get retrieves a session by ID. Returns nil if not found or expired.
func (s *SessionStore) Get(id string) *Session {
	s.mu.RLock()
	session, ok := s.sessions[id]
	s.mu.RUnlock()

	if !ok {
		return nil
	}

	if session.IsExpired() {
		// Clean up expired session
		s.Delete(id)
		return nil
	}

	return session
}

// Delete removes a session by ID.
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// Count returns the number of active sessions.
func (s *SessionStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// Stop stops the background cleanup goroutine.
func (s *SessionStore) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()

	close(s.stopCh)
}

// cleanupLoop periodically removes expired sessions.
func (s *SessionStore) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}

// cleanup removes all expired sessions.
func (s *SessionStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, session := range s.sessions {
		if now.After(session.ExpiresAt) {
			delete(s.sessions, id)
		}
	}
}

// generateSessionID generates a cryptographically secure random session ID.
func generateSessionID() (string, error) {
	bytes := make([]byte, SessionIDLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

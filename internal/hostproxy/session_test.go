package hostproxy

import (
	"testing"
	"time"
)

func TestGenerateSessionID(t *testing.T) {
	id1, err := generateSessionID()
	if err != nil {
		t.Fatalf("generateSessionID() error = %v", err)
	}

	if len(id1) != SessionIDLength*2 { // hex encoding doubles the length
		t.Errorf("expected ID length %d, got %d", SessionIDLength*2, len(id1))
	}

	// Generate another and ensure they're different
	id2, err := generateSessionID()
	if err != nil {
		t.Fatalf("generateSessionID() error = %v", err)
	}

	if id1 == id2 {
		t.Error("expected unique session IDs, got duplicates")
	}
}

func TestSessionStore_Create(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	metadata := map[string]any{"key": "value"}
	session, err := store.Create("callback", 5*time.Minute, metadata)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if session.ID == "" {
		t.Error("expected session ID to be set")
	}

	if session.Type != "callback" {
		t.Errorf("expected type 'callback', got %q", session.Type)
	}

	if session.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}

	if session.ExpiresAt.Before(session.CreatedAt) {
		t.Error("expected ExpiresAt to be after CreatedAt")
	}

	val, ok := session.GetMetadata("key")
	if !ok || val != "value" {
		t.Errorf("expected metadata key='value', got %v", val)
	}
}

func TestSessionStore_CreateWithNilMetadata(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.Create("callback", 5*time.Minute, nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Should be able to set metadata even when created with nil
	session.SetMetadata("key", "value")
	val, ok := session.GetMetadata("key")
	if !ok || val != "value" {
		t.Errorf("expected metadata key='value', got %v", val)
	}
}

func TestSessionStore_Get(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.Create("callback", 5*time.Minute, nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	retrieved := store.Get(session.ID)
	if retrieved == nil {
		t.Fatal("expected to retrieve session, got nil")
	}

	if retrieved.ID != session.ID {
		t.Errorf("expected ID %q, got %q", session.ID, retrieved.ID)
	}
}

func TestSessionStore_GetNotFound(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	retrieved := store.Get("nonexistent")
	if retrieved != nil {
		t.Error("expected nil for nonexistent session")
	}
}

func TestSessionStore_GetExpired(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	// Create session with very short TTL
	session, err := store.Create("callback", 1*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	retrieved := store.Get(session.ID)
	if retrieved != nil {
		t.Error("expected nil for expired session")
	}
}

func TestSessionStore_Delete(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	session, err := store.Create("callback", 5*time.Minute, nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	store.Delete(session.ID)

	retrieved := store.Get(session.ID)
	if retrieved != nil {
		t.Error("expected nil after delete")
	}
}

func TestSessionStore_Count(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	if store.Count() != 0 {
		t.Errorf("expected count 0, got %d", store.Count())
	}

	_, err := store.Create("callback", 5*time.Minute, nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if store.Count() != 1 {
		t.Errorf("expected count 1, got %d", store.Count())
	}

	_, err = store.Create("callback", 5*time.Minute, nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if store.Count() != 2 {
		t.Errorf("expected count 2, got %d", store.Count())
	}
}

func TestSession_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{
			name:      "not expired",
			expiresAt: time.Now().Add(5 * time.Minute),
			want:      false,
		},
		{
			name:      "expired",
			expiresAt: time.Now().Add(-1 * time.Minute),
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Session{
				ExpiresAt: tt.expiresAt,
			}
			if got := s.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSession_GetSetMetadata(t *testing.T) {
	s := &Session{
		Metadata: make(map[string]any),
	}

	// Get nonexistent key
	_, ok := s.GetMetadata("missing")
	if ok {
		t.Error("expected ok=false for missing key")
	}

	// Set and get
	s.SetMetadata("key", "value")
	val, ok := s.GetMetadata("key")
	if !ok {
		t.Error("expected ok=true after setting key")
	}
	if val != "value" {
		t.Errorf("expected 'value', got %v", val)
	}

	// Overwrite
	s.SetMetadata("key", "updated")
	val, _ = s.GetMetadata("key")
	if val != "updated" {
		t.Errorf("expected 'updated', got %v", val)
	}
}

func TestSessionStore_Cleanup(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	// Create an expired session directly
	store.mu.Lock()
	store.sessions["expired"] = &Session{
		ID:        "expired",
		Type:      "test",
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	}
	store.sessions["valid"] = &Session{
		ID:        "valid",
		Type:      "test",
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	store.mu.Unlock()

	if store.Count() != 2 {
		t.Errorf("expected count 2, got %d", store.Count())
	}

	// Run cleanup
	store.cleanup()

	if store.Count() != 1 {
		t.Errorf("expected count 1 after cleanup, got %d", store.Count())
	}

	if store.Get("expired") != nil {
		t.Error("expected expired session to be removed")
	}

	if store.Get("valid") == nil {
		t.Error("expected valid session to still exist")
	}
}

func TestSessionStore_StopIsIdempotent(t *testing.T) {
	store := NewSessionStore()

	// Should not panic when called multiple times
	store.Stop()
	store.Stop()
	store.Stop()
}

func TestSession_CaptureOnce_Concurrent(t *testing.T) {
	session := &Session{
		Metadata: make(map[string]any),
	}

	const numGoroutines = 100
	results := make(chan bool, numGoroutines)

	// Start multiple goroutines trying to capture simultaneously
	for i := 0; i < numGoroutines; i++ {
		go func() {
			results <- session.CaptureOnce("received")
		}()
	}

	// Count how many succeeded
	successCount := 0
	for i := 0; i < numGoroutines; i++ {
		if <-results {
			successCount++
		}
	}

	// Only ONE should succeed
	if successCount != 1 {
		t.Errorf("expected exactly 1 successful capture, got %d", successCount)
	}

	// Verify the flag is set
	val, ok := session.GetMetadata("received")
	if !ok || val != true {
		t.Error("expected received=true in metadata")
	}
}

func TestSessionStore_ConcurrentAccess(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	const numGoroutines = 50
	done := make(chan struct{}, numGoroutines*3)

	// Start goroutines that create sessions
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			_, err := store.Create("test", 5*time.Minute, map[string]any{"id": id})
			if err != nil {
				t.Errorf("Create failed: %v", err)
			}
		}(i)
	}

	// Start goroutines that read sessions
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			// Just access the store, may or may not find sessions
			_ = store.Count()
		}()
	}

	// Start goroutines that trigger cleanup
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			store.cleanup()
		}()
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines*3; i++ {
		<-done
	}

	// Store should still be functional
	_, err := store.Create("final", 5*time.Minute, nil)
	if err != nil {
		t.Errorf("Store not functional after concurrent access: %v", err)
	}
}

func TestSessionStore_OnDeleteCallbackFired(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	callbackCalled := false
	var deletedSession *Session

	store.SetOnDelete(func(session *Session) {
		callbackCalled = true
		deletedSession = session
	})

	session, _ := store.Create("test", 5*time.Minute, map[string]any{"key": "value"})
	sessionID := session.ID

	// Delete the session
	store.Delete(sessionID)

	if !callbackCalled {
		t.Error("expected onDelete callback to be called")
	}

	if deletedSession == nil || deletedSession.ID != sessionID {
		t.Error("expected callback to receive the deleted session")
	}
}

func TestSessionStore_OnDeleteCallbackFiredOnCleanup(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()

	var deletedSessions []*Session

	store.SetOnDelete(func(session *Session) {
		deletedSessions = append(deletedSessions, session)
	})

	// Create an expired session directly
	store.mu.Lock()
	store.sessions["expired1"] = &Session{
		ID:        "expired1",
		Type:      "test",
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	}
	store.sessions["expired2"] = &Session{
		ID:        "expired2",
		Type:      "test",
		ExpiresAt: time.Now().Add(-1 * time.Minute),
	}
	store.mu.Unlock()

	// Run cleanup
	store.cleanup()

	// Both expired sessions should trigger callbacks
	if len(deletedSessions) != 2 {
		t.Errorf("expected 2 callbacks, got %d", len(deletedSessions))
	}
}

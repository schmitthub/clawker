package hostproxy

import (
	"fmt"
	"net/http"
	"time"
)

// CallbackSessionType is the session type identifier for callback sessions.
const CallbackSessionType = "callback"

// DefaultCallbackTTL is the default time-to-live for callback sessions.
const DefaultCallbackTTL = 5 * time.Minute

// Callback metadata keys
const (
	metadataPort     = "port"
	metadataPath     = "path"
	metadataReceived = "received"
	metadataData     = "data"
)

// CallbackData contains the captured OAuth callback request data.
type CallbackData struct {
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Query      string            `json:"query"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	ReceivedAt time.Time         `json:"received_at"`
}

// CallbackChannel handles OAuth callback routing.
// It manages the registration of callback expectations and captures
// incoming callbacks from the browser.
type CallbackChannel struct {
	store *SessionStore
}

// NewCallbackChannel creates a new callback channel using the provided session store.
func NewCallbackChannel(store *SessionStore) *CallbackChannel {
	return &CallbackChannel{
		store: store,
	}
}

// Register creates a new callback session expecting a callback on the specified
// port and path. Returns the session for use in URL rewriting.
func (c *CallbackChannel) Register(port int, path string, ttl time.Duration) (*Session, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %d", port)
	}
	if path == "" {
		path = "/"
	}

	metadata := map[string]any{
		metadataPort:     port,
		metadataPath:     path,
		metadataReceived: false,
	}

	return c.store.Create(CallbackSessionType, ttl, metadata)
}

// Capture stores the incoming callback request data for the specified session.
// Only the first callback for each session is captured (single-use).
// Returns an error if the session is not found or already received a callback.
func (c *CallbackChannel) Capture(sessionID string, r *http.Request) error {
	session := c.store.Get(sessionID)
	if session == nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	if session.Type != CallbackSessionType {
		return fmt.Errorf("invalid session type: %s", session.Type)
	}

	// Check if already received
	received, _ := session.GetMetadata(metadataReceived)
	if received == true {
		return fmt.Errorf("callback already received for session: %s", sessionID)
	}

	// Capture relevant headers (skip sensitive ones)
	headers := make(map[string]string)
	for key, values := range r.Header {
		// Skip potentially sensitive or noisy headers
		switch key {
		case "Cookie", "Authorization", "X-Forwarded-For", "X-Real-Ip":
			continue
		}
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	// Read body if present (limited size for safety)
	var body string
	if r.Body != nil {
		// Limit body read to 64KB for safety
		bodyBytes := make([]byte, 64*1024)
		n, _ := r.Body.Read(bodyBytes)
		if n > 0 {
			body = string(bodyBytes[:n])
		}
	}

	data := &CallbackData{
		Method:     r.Method,
		Path:       r.URL.Path,
		Query:      r.URL.RawQuery,
		Headers:    headers,
		Body:       body,
		ReceivedAt: time.Now(),
	}

	session.SetMetadata(metadataReceived, true)
	session.SetMetadata(metadataData, data)

	return nil
}

// GetData retrieves the captured callback data for the specified session.
// Returns nil if no callback has been received yet.
func (c *CallbackChannel) GetData(sessionID string) (*CallbackData, bool) {
	session := c.store.Get(sessionID)
	if session == nil {
		return nil, false
	}

	if session.Type != CallbackSessionType {
		return nil, false
	}

	received, _ := session.GetMetadata(metadataReceived)
	if received != true {
		return nil, false
	}

	data, ok := session.GetMetadata(metadataData)
	if !ok {
		return nil, false
	}

	callbackData, ok := data.(*CallbackData)
	return callbackData, ok
}

// GetPort returns the target port for the specified session.
func (c *CallbackChannel) GetPort(sessionID string) (int, bool) {
	session := c.store.Get(sessionID)
	if session == nil {
		return 0, false
	}

	port, ok := session.GetMetadata(metadataPort)
	if !ok {
		return 0, false
	}

	portInt, ok := port.(int)
	return portInt, ok
}

// GetPath returns the expected callback path for the specified session.
func (c *CallbackChannel) GetPath(sessionID string) (string, bool) {
	session := c.store.Get(sessionID)
	if session == nil {
		return "", false
	}

	path, ok := session.GetMetadata(metadataPath)
	if !ok {
		return "", false
	}

	pathStr, ok := path.(string)
	return pathStr, ok
}

// Delete removes a callback session.
func (c *CallbackChannel) Delete(sessionID string) {
	c.store.Delete(sessionID)
}

// IsReceived checks if a callback has been received for the session.
func (c *CallbackChannel) IsReceived(sessionID string) bool {
	session := c.store.Get(sessionID)
	if session == nil {
		return false
	}

	received, _ := session.GetMetadata(metadataReceived)
	return received == true
}

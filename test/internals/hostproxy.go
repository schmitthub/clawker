package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// MockHostProxy provides a mock implementation of the clawker host proxy
// for testing container scripts that communicate with the host.
type MockHostProxy struct {
	Server      *httptest.Server
	mu          sync.Mutex
	OpenedURLs  []string                 // URLs received at /open/url
	Callbacks   map[string]*CallbackData // Registered callback sessions
	GitCreds    []GitCredRequest         // Git credential requests
	SSHRequests [][]byte                 // SSH agent requests
	healthOK    bool                     // Health check response
	t           *testing.T
}

// CallbackData holds data for an OAuth callback session.
type CallbackData struct {
	SessionID     string
	OriginalPort  string
	CallbackPath  string
	CapturedPath  string // Path captured from OAuth redirect
	CapturedQuery string // Query captured from OAuth redirect
	Ready         bool   // Whether callback data is ready
}

// GitCredRequest represents a git credential request.
type GitCredRequest struct {
	Action   string // "get", "store", or "erase"
	Host     string
	Protocol string
	Username string
}

// NewMockHostProxy creates a new MockHostProxy and starts the server.
// The server is automatically stopped when the test completes.
func NewMockHostProxy(t *testing.T) *MockHostProxy {
	t.Helper()

	m := &MockHostProxy{
		Callbacks: make(map[string]*CallbackData),
		healthOK:  true,
		t:         t,
	}

	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/health", m.handleHealth)

	// URL open endpoint
	mux.HandleFunc("/open/url", m.handleOpenURL)

	// Callback registration
	mux.HandleFunc("/callback/register", m.handleCallbackRegister)

	// Callback data polling
	mux.HandleFunc("/callback/", m.handleCallbackData)

	// OAuth callback receiver (proxied from browser)
	mux.HandleFunc("/cb/", m.handleOAuthCallback)

	// Git credential forwarding
	mux.HandleFunc("/git/credential", m.handleGitCredential)

	// SSH agent forwarding
	mux.HandleFunc("/ssh/agent", m.handleSSHAgent)

	m.Server = httptest.NewServer(mux)
	t.Cleanup(func() {
		m.Server.Close()
	})

	return m
}

// URL returns the mock proxy's URL.
func (m *MockHostProxy) URL() string {
	return m.Server.URL
}

// SetHealthOK controls the health check response.
func (m *MockHostProxy) SetHealthOK(ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthOK = ok
}

// GetOpenedURLs returns a copy of the opened URLs.
func (m *MockHostProxy) GetOpenedURLs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.OpenedURLs))
	copy(result, m.OpenedURLs)
	return result
}

// GetGitCreds returns a copy of the git credential requests.
func (m *MockHostProxy) GetGitCreds() []GitCredRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]GitCredRequest, len(m.GitCreds))
	copy(result, m.GitCreds)
	return result
}

// SetCallbackReady simulates an OAuth callback being received.
func (m *MockHostProxy) SetCallbackReady(sessionID, path, query string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cb, ok := m.Callbacks[sessionID]; ok {
		cb.CapturedPath = path
		cb.CapturedQuery = query
		cb.Ready = true
	}
}

// handleHealth handles /health requests.
func (m *MockHostProxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	ok := m.healthOK
	m.mu.Unlock()

	if ok {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"unhealthy"}`))
	}
}

// handleOpenURL handles /open/url requests.
func (m *MockHostProxy) handleOpenURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.OpenedURLs = append(m.OpenedURLs, req.URL)
	m.mu.Unlock()

	m.t.Logf("MockHostProxy: opened URL %s", req.URL)
	w.WriteHeader(http.StatusOK)
}

// handleCallbackRegister handles /callback/register requests.
func (m *MockHostProxy) handleCallbackRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var req struct {
		OriginalPort string `json:"original_port"`
		CallbackPath string `json:"callback_path"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Generate session ID
	sessionID := generateSessionID()

	m.mu.Lock()
	m.Callbacks[sessionID] = &CallbackData{
		SessionID:    sessionID,
		OriginalPort: req.OriginalPort,
		CallbackPath: req.CallbackPath,
	}
	m.mu.Unlock()

	m.t.Logf("MockHostProxy: registered callback session %s for port %s", sessionID, req.OriginalPort)

	resp := struct {
		SessionID string `json:"session_id"`
	}{SessionID: sessionID}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleCallbackData handles /callback/{session}/data requests.
func (m *MockHostProxy) handleCallbackData(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from path: /callback/{session}/data or /callback/{session}
	path := r.URL.Path
	if len(path) < len("/callback/") {
		http.NotFound(w, r)
		return
	}

	remaining := path[len("/callback/"):]
	sessionID := remaining
	if idx := len(remaining) - len("/data"); idx > 0 && remaining[idx:] == "/data" {
		sessionID = remaining[:idx]
	}

	// Handle DELETE for session cleanup
	if r.Method == http.MethodDelete {
		m.mu.Lock()
		delete(m.Callbacks, sessionID)
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}

	m.mu.Lock()
	cb, ok := m.Callbacks[sessionID]
	m.mu.Unlock()

	if !ok {
		http.NotFound(w, r)
		return
	}

	if !cb.Ready {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Return in the format expected by callback-forwarder.sh
	// The script checks for .received == true before processing callback data
	resp := struct {
		Received bool `json:"received"`
		Callback struct {
			Method string `json:"method"`
			Path   string `json:"path"`
			Query  string `json:"query"`
			Body   string `json:"body"`
		} `json:"callback"`
	}{}
	resp.Received = true
	resp.Callback.Method = "GET"
	resp.Callback.Path = cb.CapturedPath
	resp.Callback.Query = cb.CapturedQuery
	resp.Callback.Body = ""

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleOAuthCallback handles /cb/{session}/{path...} requests (from browser).
func (m *MockHostProxy) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from path: /cb/{session}/{path...}
	path := r.URL.Path
	if len(path) < len("/cb/") {
		http.NotFound(w, r)
		return
	}

	remaining := path[len("/cb/"):]
	parts := splitFirst(remaining, "/")
	sessionID := parts[0]
	callbackPath := "/"
	if len(parts) > 1 {
		callbackPath = "/" + parts[1]
	}

	m.mu.Lock()
	cb, ok := m.Callbacks[sessionID]
	if ok {
		cb.CapturedPath = callbackPath
		cb.CapturedQuery = r.URL.RawQuery
		cb.Ready = true
	}
	m.mu.Unlock()

	if !ok {
		http.NotFound(w, r)
		return
	}

	m.t.Logf("MockHostProxy: captured OAuth callback for session %s: %s?%s", sessionID, callbackPath, r.URL.RawQuery)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Callback received. You can close this window."))
}

// handleGitCredential handles /git/credential requests.
func (m *MockHostProxy) handleGitCredential(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	var req struct {
		Action   string `json:"action"`
		Host     string `json:"host"`
		Protocol string `json:"protocol"`
		Username string `json:"username,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.GitCreds = append(m.GitCreds, GitCredRequest{
		Action:   req.Action,
		Host:     req.Host,
		Protocol: req.Protocol,
		Username: req.Username,
	})
	m.mu.Unlock()

	m.t.Logf("MockHostProxy: git credential %s for %s://%s", req.Action, req.Protocol, req.Host)

	// For "get" operations, return mock credentials in format expected by git-credential-clawker.sh
	if req.Action == "get" {
		resp := struct {
			Success  bool   `json:"success"`
			Username string `json:"username"`
			Password string `json:"password"`
		}{
			Success:  true,
			Username: "mock-user",
			Password: "mock-token",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// For store/erase, return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Success bool `json:"success"`
	}{Success: true})
}

// handleSSHAgent handles /ssh/agent requests.
func (m *MockHostProxy) handleSSHAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.SSHRequests = append(m.SSHRequests, body)
	m.mu.Unlock()

	m.t.Logf("MockHostProxy: received SSH agent request (%d bytes)", len(body))

	// Return empty response (no keys)
	w.WriteHeader(http.StatusOK)
}

// Helper functions

var sessionCounter int
var sessionMu sync.Mutex

func generateSessionID() string {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	sessionCounter++
	return fmt.Sprintf("test-session-%d", sessionCounter)
}

func splitFirst(s, sep string) []string {
	return strings.SplitN(s, sep, 2)
}

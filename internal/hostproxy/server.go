// Package hostproxy provides a host-side HTTP server that containers can call
// to perform actions on the host, such as opening URLs in the browser.
package hostproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/schmitthub/clawker/pkg/logger"
)

// DefaultPort is the default port for the host proxy server.
const DefaultPort = 18374

// maxRequestBodySize limits request body size to prevent DoS via memory exhaustion.
const maxRequestBodySize = 1 << 20 // 1MB

// Server is an HTTP server that handles requests from containers to perform
// host-side actions.
type Server struct {
	port            int
	listener        net.Listener
	server          *http.Server
	mu              sync.RWMutex
	running         bool
	sessionStore    *SessionStore
	callbackChannel *CallbackChannel
}

// NewServer creates a new host proxy server on the specified port.
func NewServer(port int) *Server {
	sessionStore := NewSessionStore()
	return &Server{
		port:            port,
		sessionStore:    sessionStore,
		callbackChannel: NewCallbackChannel(sessionStore),
	}
}

// Start starts the HTTP server in a goroutine.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /open/url", s.handleOpenURL)
	mux.HandleFunc("GET /health", s.handleHealth)

	// Callback channel endpoints for OAuth flow
	mux.HandleFunc("POST /callback/register", s.handleCallbackRegister)
	mux.HandleFunc("GET /callback/{session}/data", s.handleCallbackGetData)
	mux.HandleFunc("DELETE /callback/{session}", s.handleCallbackDelete)
	mux.HandleFunc("GET /cb/{session}/{path...}", s.handleCallbackCapture)

	// Bind to localhost only for security
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.listener = listener
	s.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	s.running = true

	go func() {
		logger.Debug().Str("addr", addr).Msg("host proxy server starting")
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			logger.Error().Err(err).Msg("host proxy server error")
		}
	}()

	logger.Info().Str("addr", addr).Msg("host proxy server started")
	return nil
}

// Stop gracefully shuts down the server and cleans up resources.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.server == nil {
		// Still clean up session store even if server wasn't running
		if s.sessionStore != nil {
			s.sessionStore.Stop()
		}
		return nil
	}

	s.running = false

	// Stop the session store cleanup goroutine
	if s.sessionStore != nil {
		s.sessionStore.Stop()
	}

	return s.server.Shutdown(ctx)
}

// IsRunning returns whether the server is currently running.
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// Port returns the port the server is configured to use.
func (s *Server) Port() int {
	return s.port
}

// openURLRequest is the JSON request body for the /open/url endpoint.
type openURLRequest struct {
	URL string `json:"url"`
}

// openURLResponse is the JSON response body for the /open/url endpoint.
type openURLResponse struct {
	Success bool   `json:"success"`
	URL     string `json:"url,omitempty"`
	Error   string `json:"error,omitempty"`
}

// handleOpenURL handles POST /open/url requests to open a URL in the host browser.
func (s *Server) handleOpenURL(w http.ResponseWriter, r *http.Request) {
	// Limit request body size to prevent DoS
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req openURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, openURLResponse{
			Success: false,
			Error:   "invalid JSON request body",
		})
		return
	}

	if req.URL == "" {
		s.writeJSON(w, http.StatusBadRequest, openURLResponse{
			Success: false,
			Error:   "url field is required",
		})
		return
	}

	// Validate URL scheme to prevent opening dangerous protocols
	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, openURLResponse{
			Success: false,
			Error:   "invalid URL format",
		})
		return
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		s.writeJSON(w, http.StatusBadRequest, openURLResponse{
			Success: false,
			Error:   "only http and https URLs are allowed",
		})
		return
	}

	logger.Debug().Str("url", req.URL).Msg("opening URL in browser")

	if err := openBrowser(req.URL); err != nil {
		logger.Error().Err(err).Str("url", req.URL).Msg("failed to open URL in browser")
		s.writeJSON(w, http.StatusInternalServerError, openURLResponse{
			Success: false,
			URL:     req.URL,
			Error:   err.Error(),
		})
		return
	}

	s.writeJSON(w, http.StatusOK, openURLResponse{
		Success: true,
		URL:     req.URL,
	})
}

// healthResponse is the JSON response body for the /health endpoint.
type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// handleHealth handles GET /health requests for health checking.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, healthResponse{
		Status:  "ok",
		Service: "clawker-host-proxy",
	})
}

// writeJSON writes a JSON response with the given status code.
func (s *Server) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error().Err(err).Msg("failed to encode JSON response")
	}
}

// --- Callback channel types and handlers ---

// callbackRegisterRequest is the JSON request body for POST /callback/register.
type callbackRegisterRequest struct {
	Port           int `json:"port"`
	Path           string `json:"path,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// callbackRegisterResponse is the JSON response body for POST /callback/register.
type callbackRegisterResponse struct {
	Success           bool   `json:"success"`
	SessionID         string `json:"session_id,omitempty"`
	ProxyCallbackBase string `json:"proxy_callback_base,omitempty"`
	ExpiresAt         string `json:"expires_at,omitempty"`
	Error             string `json:"error,omitempty"`
}

// handleCallbackRegister handles POST /callback/register requests.
// It creates a new callback session for OAuth flow interception.
func (s *Server) handleCallbackRegister(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req callbackRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, callbackRegisterResponse{
			Success: false,
			Error:   "invalid JSON request body",
		})
		return
	}

	if req.Port <= 0 || req.Port > 65535 {
		s.writeJSON(w, http.StatusBadRequest, callbackRegisterResponse{
			Success: false,
			Error:   "port must be between 1 and 65535",
		})
		return
	}

	ttl := DefaultCallbackTTL
	if req.TimeoutSeconds > 0 {
		ttl = min(time.Duration(req.TimeoutSeconds)*time.Second, 30*time.Minute)
	}

	path := req.Path
	if path == "" {
		path = "/callback"
	}

	session, err := s.callbackChannel.Register(req.Port, path, ttl)
	if err != nil {
		logger.Error().Err(err).Msg("failed to register callback session")
		s.writeJSON(w, http.StatusInternalServerError, callbackRegisterResponse{
			Success: false,
			Error:   "failed to create session",
		})
		return
	}

	proxyBase := fmt.Sprintf("http://localhost:%d/cb/%s", s.port, session.ID)

	logger.Debug().
		Str("session_id", session.ID).
		Int("port", req.Port).
		Str("path", path).
		Msg("registered callback session")

	s.writeJSON(w, http.StatusOK, callbackRegisterResponse{
		Success:           true,
		SessionID:         session.ID,
		ProxyCallbackBase: proxyBase,
		ExpiresAt:         session.ExpiresAt.Format(time.RFC3339),
	})
}

// callbackDataResponse is the JSON response body for GET /callback/{session}/data.
type callbackDataResponse struct {
	Received bool          `json:"received"`
	Callback *CallbackData `json:"callback,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// handleCallbackGetData handles GET /callback/{session}/data requests.
// Containers poll this endpoint to retrieve captured OAuth callback data.
func (s *Server) handleCallbackGetData(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session")
	if sessionID == "" {
		s.writeJSON(w, http.StatusBadRequest, callbackDataResponse{
			Received: false,
			Error:    "session ID required",
		})
		return
	}

	data, received := s.callbackChannel.GetData(sessionID)
	if !received {
		// Check if session exists but no callback yet
		if s.sessionStore.Get(sessionID) != nil {
			s.writeJSON(w, http.StatusOK, callbackDataResponse{
				Received: false,
			})
			return
		}
		// Session not found
		s.writeJSON(w, http.StatusNotFound, callbackDataResponse{
			Received: false,
			Error:    "session not found or expired",
		})
		return
	}

	s.writeJSON(w, http.StatusOK, callbackDataResponse{
		Received: true,
		Callback: data,
	})
}

// callbackDeleteResponse is the JSON response body for DELETE /callback/{session}.
type callbackDeleteResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// handleCallbackDelete handles DELETE /callback/{session} requests.
// Containers call this to clean up a session after processing the callback.
func (s *Server) handleCallbackDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session")
	if sessionID == "" {
		s.writeJSON(w, http.StatusBadRequest, callbackDeleteResponse{
			Success: false,
			Error:   "session ID required",
		})
		return
	}

	s.callbackChannel.Delete(sessionID)

	s.writeJSON(w, http.StatusOK, callbackDeleteResponse{
		Success: true,
	})
}

// handleCallbackCapture handles GET /cb/{session}/{path...} requests.
// This is the catch-all endpoint that receives OAuth callbacks from the browser.
func (s *Server) handleCallbackCapture(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session")
	if sessionID == "" {
		http.Error(w, "Invalid callback URL", http.StatusBadRequest)
		return
	}

	err := s.callbackChannel.Capture(sessionID, r)
	if err != nil {
		logger.Error().Err(err).Str("session_id", sessionID).Msg("failed to capture callback")

		// Check what kind of error
		if s.sessionStore.Get(sessionID) == nil {
			http.Error(w, "Session not found or expired", http.StatusNotFound)
			return
		}

		// Session exists but callback already captured (single-use)
		// Still return success to the browser since the OAuth flow succeeded
	}

	// Return a user-friendly HTML page indicating success
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <title>Authentication Complete</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            height: 100vh;
            margin: 0;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
        }
        .container {
            text-align: center;
            background: white;
            padding: 40px 60px;
            border-radius: 12px;
            box-shadow: 0 4px 20px rgba(0,0,0,0.15);
        }
        .checkmark {
            font-size: 64px;
            margin-bottom: 16px;
        }
        h1 { color: #333; margin: 0 0 8px 0; }
        p { color: #666; margin: 0; }
    </style>
</head>
<body>
    <div class="container">
        <div class="checkmark">✓</div>
        <h1>Authentication Complete</h1>
        <p>You can close this tab and return to Claude Code.</p>
    </div>
</body>
</html>`))
}

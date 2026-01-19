// Package hostproxy provides a host-side HTTP server that containers can call
// to perform actions on the host, such as opening URLs in the browser.
package hostproxy

import (
	"context"
	"encoding/json"
	"errors"
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

// dynamicListener tracks a temporary HTTP listener for OAuth callbacks.
type dynamicListener struct {
	port      int
	sessionID string
	listener  net.Listener
	server    *http.Server
}

// Server is an HTTP server that handles requests from containers to perform
// host-side actions.
type Server struct {
	port             int
	listener         net.Listener
	server           *http.Server
	mu               sync.RWMutex
	running          bool
	sessionStore     *SessionStore
	callbackChannel  *CallbackChannel
	dynamicListeners map[int]*dynamicListener // port -> listener
	portToSession    map[int]string           // port -> sessionID for lookups
}

// NewServer creates a new host proxy server on the specified port.
func NewServer(port int) *Server {
	sessionStore := NewSessionStore()
	s := &Server{
		port:             port,
		sessionStore:     sessionStore,
		callbackChannel:  NewCallbackChannel(sessionStore),
		dynamicListeners: make(map[int]*dynamicListener),
		portToSession:    make(map[int]string),
	}

	// Set up cleanup callback for when sessions are deleted
	sessionStore.SetOnDelete(func(session *Session) {
		if session.Type != CallbackSessionType {
			return
		}
		// Get the port from session metadata
		portVal, ok := session.GetMetadata(metadataPort)
		if !ok {
			return
		}
		port, ok := portVal.(int)
		if !ok {
			return
		}
		s.closeDynamicListener(port)
	})

	return s
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

	// Git credential forwarding endpoint
	mux.HandleFunc("POST /git/credential", s.handleGitCredential)

	// SSH agent forwarding endpoint
	mux.HandleFunc("POST /ssh/agent", s.handleSSHAgent)

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

	if !s.running || s.server == nil {
		// Still clean up session store even if server wasn't running
		if s.sessionStore != nil {
			s.sessionStore.Stop()
		}
		s.mu.Unlock()
		return nil
	}

	s.running = false

	// Close all dynamic listeners
	for port := range s.dynamicListeners {
		s.closeDynamicListenerLocked(port)
	}

	// Stop the session store cleanup goroutine
	if s.sessionStore != nil {
		s.sessionStore.Stop()
	}

	server := s.server
	s.mu.Unlock()

	return server.Shutdown(ctx)
}

// startDynamicListener starts a temporary HTTP listener on the specified port
// to capture OAuth callbacks.
func (s *Server) startDynamicListener(port int, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if a listener already exists on this port
	if _, exists := s.dynamicListeners[port]; exists {
		return fmt.Errorf("listener already exists on port %d", port)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	// Create a handler that captures all requests on this port
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handleDynamicCallback(port, w, r)
	})

	server := &http.Server{
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	dl := &dynamicListener{
		port:      port,
		sessionID: sessionID,
		listener:  listener,
		server:    server,
	}

	s.dynamicListeners[port] = dl
	s.portToSession[port] = sessionID

	go func() {
		logger.Debug().Int("port", port).Str("session_id", sessionID).Msg("dynamic listener starting")
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Int("port", port).Msg("dynamic listener error")
		}
	}()

	logger.Info().Int("port", port).Str("session_id", sessionID).Msg("dynamic listener started")
	return nil
}

// closeDynamicListener closes a dynamic listener on the specified port.
func (s *Server) closeDynamicListener(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeDynamicListenerLocked(port)
}

// closeDynamicListenerLocked closes a dynamic listener (must hold s.mu).
func (s *Server) closeDynamicListenerLocked(port int) {
	dl, exists := s.dynamicListeners[port]
	if !exists {
		return
	}

	logger.Debug().Int("port", port).Msg("closing dynamic listener")

	// Shutdown the server gracefully with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := dl.server.Shutdown(ctx); err != nil {
		// Timeout during cleanup is expected - just force close
		logger.Debug().Int("port", port).Msg("force closing dynamic listener")
		dl.listener.Close()
	}

	delete(s.dynamicListeners, port)
	delete(s.portToSession, port)
}

// handleDynamicCallback handles incoming requests on a dynamic listener port.
func (s *Server) handleDynamicCallback(port int, w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	sessionID, exists := s.portToSession[port]
	s.mu.RUnlock()

	if !exists {
		logger.Error().Int("port", port).Msg("no session found for dynamic listener port")
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	logger.Debug().
		Int("port", port).
		Str("session_id", sessionID).
		Str("path", r.URL.Path).
		Str("query", r.URL.RawQuery).
		Msg("received callback on dynamic listener")

	err := s.callbackChannel.Capture(sessionID, r)
	if err != nil {
		logger.Error().Err(err).Str("session_id", sessionID).Msg("failed to capture callback")

		if errors.Is(err, ErrCallbackAlreadyReceived) {
			s.writeCallbackSuccessPage(w)
			return
		}

		s.writeCallbackErrorPage(w, "An error occurred. Please try again.")
		return
	}

	s.writeCallbackSuccessPage(w)
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
	Port           int    `json:"port"`
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
// It creates a new callback session and starts a dynamic listener on the
// specified port to capture OAuth callbacks.
func (s *Server) handleCallbackRegister(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req callbackRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Debug().Err(err).Msg("failed to decode callback register request")
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

	// Start a dynamic listener on the callback port
	// This allows the host to capture OAuth callbacks on the same port
	// that Claude Code expects, without rewriting the redirect_uri
	if err := s.startDynamicListener(req.Port, session.ID); err != nil {
		logger.Error().Err(err).Int("port", req.Port).Msg("failed to start dynamic listener")
		// Clean up the session since we couldn't start the listener
		s.callbackChannel.Delete(session.ID)
		s.writeJSON(w, http.StatusInternalServerError, callbackRegisterResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to start listener on port %d: %v", req.Port, err),
		})
		return
	}

	logger.Debug().
		Str("session_id", session.ID).
		Int("port", req.Port).
		Str("path", path).
		Msg("registered callback session with dynamic listener")

	s.writeJSON(w, http.StatusOK, callbackRegisterResponse{
		Success:   true,
		SessionID: session.ID,
		ExpiresAt: session.ExpiresAt.Format(time.RFC3339),
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

		// Distinguish between different error types
		if errors.Is(err, ErrCallbackAlreadyReceived) {
			// OAuth worked, this is just a duplicate request - show success
			s.writeCallbackSuccessPage(w)
			return
		}

		// Check if session doesn't exist
		if s.sessionStore.Get(sessionID) == nil {
			http.Error(w, "Session not found or expired", http.StatusNotFound)
			return
		}

		// Some other error - show error page
		s.writeCallbackErrorPage(w, "An error occurred. Please try again.")
		return
	}

	// Return a user-friendly HTML page indicating success
	s.writeCallbackSuccessPage(w)
}

// writeCallbackSuccessPage writes an HTML success page for OAuth callbacks.
func (s *Server) writeCallbackSuccessPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <title>Authentication Complete</title>
    <style>
        :root {
            color-scheme: dark;
            --bg: #0b0c0f;
            --bg-2: #0f1116;
            --card: #12141b;
            --ink: #f2f2f2;
            --muted: #9aa0a6;
            --accent: #f2a973;
            --accent-strong: #f6b880;
            --ring: rgba(242, 169, 115, 0.28);
            --shadow: 0 22px 60px rgba(0, 0, 0, 0.55);
            --border: rgba(255, 255, 255, 0.08);
        }

        body {
            font-family: "Söhne", "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            height: 100vh;
            margin: 0;
            background:
                radial-gradient(900px 500px at 15% 0%, rgba(242, 169, 115, 0.12), transparent 60%),
                radial-gradient(900px 600px at 85% 10%, rgba(255, 255, 255, 0.06), transparent 65%),
                linear-gradient(180deg, var(--bg-2) 0%, var(--bg) 100%);
            color: var(--ink);
        }

        .container {
            text-align: center;
            background: var(--card);
            padding: 40px 56px;
            border-radius: 16px;
            box-shadow: var(--shadow);
            border: 1px solid var(--border);
            min-width: 320px;
        }

        .checkmark {
            font-size: 52px;
            margin-bottom: 18px;
            width: 72px;
            height: 72px;
            line-height: 72px;
            border-radius: 50%;
            display: inline-flex;
            align-items: center;
            justify-content: center;
            color: #0b0c0f;
            background: linear-gradient(135deg, var(--accent) 0%, var(--accent-strong) 100%);
            box-shadow: 0 10px 24px rgba(242, 169, 115, 0.35);
        }

        .product {
            text-transform: uppercase;
            letter-spacing: 0.24em;
            font-size: 11px;
            color: var(--accent);
            margin: 0 0 8px 0;
            font-weight: 600;
        }

        h1 {
            color: var(--ink);
            margin: 0 0 10px 0;
            font-size: 24px;
            letter-spacing: -0.01em;
            font-weight: 600;
        }

        p {
            color: var(--muted);
            margin: 0;
            font-size: 14.5px;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="checkmark">✓</div>
        <div class="product">clawker</div>
        <h1>Authentication Complete</h1>
        <p>You can close this tab and return to Claude Code.</p>
    </div>
</body>
</html>
`))
}

// writeCallbackErrorPage writes an HTML error page for OAuth callbacks.
func (s *Server) writeCallbackErrorPage(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
    <title>Authentication Error</title>
    <style>
        :root {
            color-scheme: dark;
            --bg: #0b0c0f;
            --bg-2: #0f1116;
            --card: #12141b;
            --ink: #f2f2f2;
            --muted: #9aa0a6;
            --accent: #f2a973;
            --accent-strong: #f6b880;
            --ring: rgba(242, 169, 115, 0.28);
            --shadow: 0 22px 60px rgba(0, 0, 0, 0.55);
            --border: rgba(255, 255, 255, 0.08);
        }

        body {
            font-family: "Söhne", "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            display: flex;
            justify-content: center;
            align-items: center;
            height: 100vh;
            margin: 0;
            background:
                radial-gradient(900px 500px at 15% 0%, rgba(242, 169, 115, 0.12), transparent 60%),
                radial-gradient(900px 600px at 85% 10%, rgba(255, 255, 255, 0.06), transparent 65%),
                linear-gradient(180deg, var(--bg-2) 0%, var(--bg) 100%);
            color: var(--ink);
        }

        .container {
            text-align: center;
            background: var(--card);
            padding: 40px 56px;
            border-radius: 16px;
            box-shadow: var(--shadow);
            border: 1px solid var(--border);
            min-width: 320px;
        }

        .error-icon {
            font-size: 52px;
            margin-bottom: 18px;
            width: 72px;
            height: 72px;
            line-height: 72px;
            border-radius: 50%;
            display: inline-flex;
            align-items: center;
            justify-content: center;
            color: #0b0c0f;
            background: linear-gradient(135deg, var(--accent) 0%, var(--accent-strong) 100%);
            box-shadow: 0 10px 24px rgba(242, 169, 115, 0.35);
        }

        .product {
            text-transform: uppercase;
            letter-spacing: 0.24em;
            font-size: 11px;
            color: var(--accent);
            margin: 0 0 8px 0;
            font-weight: 600;
        }

        h1 {
            color: var(--ink);
            margin: 0 0 10px 0;
            font-size: 24px;
            letter-spacing: -0.01em;
            font-weight: 600;
        }

        p {
            color: var(--muted);
            margin: 0;
            font-size: 14.5px;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="error-icon">✗</div>
        <div class="product">clawker</div>
        <h1>Authentication Error</h1>
        <p>` + message + `</p>
    </div>
</body>
</html>`))
}

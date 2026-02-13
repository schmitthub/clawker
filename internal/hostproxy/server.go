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

	"github.com/schmitthub/clawker/internal/logger"
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
	listeners        []net.Listener // IPv4 and optionally IPv6 listeners
	servers          []*http.Server // One server per listener
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
// It listens on both IPv4 (127.0.0.1) and IPv6 ([::1]) loopback addresses
// to support containers that resolve host.docker.internal to either protocol.
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

	// Callback channel endpoints for OAuth flow
	mux.HandleFunc("POST /callback/register", s.handleCallbackRegister)
	mux.HandleFunc("GET /callback/{session}/data", s.handleCallbackGetData)
	mux.HandleFunc("DELETE /callback/{session}", s.handleCallbackDelete)
	mux.HandleFunc("GET /cb/{session}/{path...}", s.handleCallbackCapture)

	// Bind to localhost only for security - both IPv4 and IPv6
	// This is necessary because Docker Desktop's host.docker.internal can
	// resolve to either IPv4 or IPv6 depending on the system configuration.
	addresses := []string{
		fmt.Sprintf("127.0.0.1:%d", s.port), // IPv4 loopback
		fmt.Sprintf("[::1]:%d", s.port),     // IPv6 loopback
	}

	var listeners []net.Listener
	var servers []*http.Server
	var listenedAddrs []string

	for _, addr := range addresses {
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			// IPv6 may not be available on all systems - log and continue
			logger.Debug().Str("addr", addr).Err(err).Msg("failed to listen (may be expected if protocol not available)")
			continue
		}

		server := &http.Server{
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  60 * time.Second,
		}

		listeners = append(listeners, listener)
		servers = append(servers, server)
		listenedAddrs = append(listenedAddrs, addr)
	}

	if len(listeners) == 0 {
		return fmt.Errorf("failed to listen on any address (tried %v)", addresses)
	}

	s.listeners = listeners
	s.servers = servers
	s.running = true

	// Start a goroutine for each listener
	for i, listener := range listeners {
		server := servers[i]
		addr := listenedAddrs[i]
		go func(l net.Listener, srv *http.Server, a string) {
			logger.Debug().Str("addr", a).Msg("host proxy server starting")
			if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
				logger.Error().Err(err).Str("addr", a).Msg("host proxy server error")
			}
		}(listener, server, addr)
	}

	logger.Info().Strs("addrs", listenedAddrs).Msg("host proxy server started")
	return nil
}

// Stop gracefully shuts down the server and cleans up resources.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()

	if !s.running || len(s.servers) == 0 {
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

	servers := s.servers
	s.mu.Unlock()

	// Shutdown all servers
	var errs []error
	for _, server := range servers {
		if err := server.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
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
		// ErrCallbackAlreadyReceived is expected (browsers often send duplicate requests)
		// Handle it silently by showing success page
		if errors.Is(err, ErrCallbackAlreadyReceived) {
			s.writeCallbackSuccessPage(w)
			return
		}

		// Log actual errors
		logger.Error().Err(err).Str("session_id", sessionID).Msg("failed to capture callback")
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
		// ErrCallbackAlreadyReceived is expected (browsers often send duplicate requests)
		// Handle it silently by showing success page
		if errors.Is(err, ErrCallbackAlreadyReceived) {
			s.writeCallbackSuccessPage(w)
			return
		}

		// Log actual errors
		logger.Error().Err(err).Str("session_id", sessionID).Msg("failed to capture callback")

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
	if _, err := w.Write([]byte(callbackPage("Authentication Complete", callbackSuccessBody))); err != nil {
		logger.Debug().Err(err).Msg("failed to write callback success page")
	}
}

// writeCallbackErrorPage writes an HTML error page for OAuth callbacks.
func (s *Server) writeCallbackErrorPage(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	if _, err := w.Write([]byte(callbackPage("Authentication Error", callbackErrorBody(message)))); err != nil {
		logger.Debug().Err(err).Msg("failed to write callback error page")
	}
}

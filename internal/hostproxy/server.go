// Package hostproxy provides a host-side HTTP server that containers can call
// to perform actions on the host, such as opening URLs in the browser.
package hostproxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
)

// maxRequestBodySize limits request body size to prevent DoS via memory exhaustion.
const maxRequestBodySize = 1 << 20 // 1MB

// errEgressRulesUnavailable is the client-facing error returned when the egress
// rules file is present but corrupt/unreadable — an infrastructure fault that
// fails closed rather than a per-request policy decision.
const errEgressRulesUnavailable = "egress rules unavailable"

// dynamicListener tracks a temporary HTTP listener for OAuth callbacks.
type dynamicListener struct {
	port      int
	sessionID string
	listeners []net.Listener
	servers   []*http.Server
}

// Server is an HTTP server that handles requests from containers to perform
// host-side actions.
type Server struct {
	port             int
	log              *logger.Logger
	rulesFilePath    string             // egress rules file path; empty = skip check (firewall disabled)
	tlsConfig        *tls.Config        // mTLS server config; Start refuses to serve without it
	browserFunc      func(string) error // opens URL in host browser; defaults to openBrowser
	listeners        []net.Listener     // IPv4 and optionally IPv6 listeners
	servers          []*http.Server     // One server per listener
	mu               sync.RWMutex
	running          bool
	sessionStore     *SessionStore
	callbackChannel  *CallbackChannel
	dynamicListeners map[int]*dynamicListener // port -> listener
	portToSession    map[int]string           // port -> sessionID for lookups
}

// NewServer creates a new host proxy server on the specified port.
// tlsConfig is the mTLS server config (ServerTLSConfig); Start fails
// without it.
func NewServer(port int, log *logger.Logger, rulesFilePath string, tlsConfig *tls.Config) *Server {
	sessionStore := NewSessionStore()
	s := &Server{
		port:             port,
		log:              log,
		rulesFilePath:    rulesFilePath,
		tlsConfig:        tlsConfig,
		browserFunc:      openBrowser,
		sessionStore:     sessionStore,
		callbackChannel:  NewCallbackChannel(sessionStore, log),
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
	if s.tlsConfig == nil {
		return errors.New("host proxy: no TLS config; the host proxy serves mTLS only")
	}

	// /health answers any client the TLS layer admitted (the CLI checks it
	// with its own cert). Host actions answer only the CP.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	cp := func(h http.HandlerFunc) http.HandlerFunc { return requireControlPlane(s.log, h) }
	mux.HandleFunc("POST /open/url", cp(s.handleOpenURL))
	mux.HandleFunc("POST /git/credential", cp(s.handleGitCredential))
	mux.HandleFunc("POST /callback/register", cp(s.handleCallbackRegister))
	mux.HandleFunc("GET /callback/{session}/data", cp(s.handleCallbackGetData))
	mux.HandleFunc("DELETE /callback/{session}", cp(s.handleCallbackDelete))

	// Bind to localhost only for security - both IPv4 and IPv6
	// This is necessary because Docker Desktop's host.docker.internal can
	// resolve to either IPv4 or IPv6 depending on the system configuration.
	addresses := []string{
		fmt.Sprintf(consts.Localhost+":%d", s.port), // IPv4 loopback
		fmt.Sprintf("[::1]:%d", s.port),             // IPv6 loopback
	}

	var listeners []net.Listener
	var servers []*http.Server
	var listenedAddrs []string

	for _, addr := range addresses {
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			// IPv6 may not be available on all systems - log and continue
			s.log.Debug().Str("addr", addr).Err(err).Msg("failed to listen (may be expected if protocol not available)")
			continue
		}

		server := &http.Server{
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  60 * time.Second,
		}

		listeners = append(listeners, tls.NewListener(listener, s.tlsConfig))
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
			s.log.Debug().Str("addr", a).Msg("host proxy server starting")
			if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
				s.log.Error().Err(err).Str("addr", a).Msg("host proxy server error")
			}
		}(listener, server, addr)
	}

	s.log.Debug().Strs("addrs", listenedAddrs).Msg("host proxy server started")
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

// startDynamicListener starts temporary HTTP listeners on loopback addresses
// to capture OAuth callbacks.
func (s *Server) startDynamicListener(port int, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if a listener already exists on this port
	if _, exists := s.dynamicListeners[port]; exists {
		return fmt.Errorf("listener already exists on port %d", port)
	}

	addresses := []string{
		fmt.Sprintf(consts.Localhost+":%d", port),
		fmt.Sprintf("[::1]:%d", port),
	}

	// Create a handler that captures all requests on this port
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handleDynamicCallback(port, w, r)
	})

	var listeners []net.Listener
	var servers []*http.Server
	var listenedAddrs []string
	for _, addr := range addresses {
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			s.log.Debug().Str("addr", addr).Int("port", port).Err(err).Msg("failed to listen for dynamic callback (may be expected if protocol not available)")
			continue
		}

		server := &http.Server{
			Handler:      handler,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		}

		listeners = append(listeners, listener)
		servers = append(servers, server)
		listenedAddrs = append(listenedAddrs, addr)
	}

	if len(listeners) == 0 {
		return fmt.Errorf("failed to listen on any dynamic callback address for port %d (tried %v)", port, addresses)
	}

	dl := &dynamicListener{
		port:      port,
		sessionID: sessionID,
		listeners: listeners,
		servers:   servers,
	}

	s.dynamicListeners[port] = dl
	s.portToSession[port] = sessionID

	for i, listener := range listeners {
		server := servers[i]
		addr := listenedAddrs[i]
		go func(l net.Listener, srv *http.Server, a string) {
			s.log.Debug().Int("port", port).Str("session_id", sessionID).Str("addr", a).Msg("dynamic listener starting")
			if err := srv.Serve(l); err != nil && err != http.ErrServerClosed {
				s.log.Error().Err(err).Int("port", port).Str("addr", a).Msg("dynamic listener error")
			}
		}(listener, server, addr)
	}

	s.log.Debug().Int("port", port).Str("session_id", sessionID).Strs("addrs", listenedAddrs).Msg("dynamic listener started")
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

	s.log.Debug().Int("port", port).Msg("closing dynamic listener")

	// Shutdown the servers gracefully with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	for i, server := range dl.servers {
		if err := server.Shutdown(ctx); err != nil {
			// Timeout during cleanup is expected - just force close
			s.log.Debug().Int("port", port).Int("listener_index", i).Msg("force closing dynamic listener")
			if i < len(dl.listeners) {
				if closeErr := dl.listeners[i].Close(); closeErr != nil {
					s.log.Debug().Err(closeErr).Int("port", port).Int("listener_index", i).Msg("dynamic listener close returned error")
				}
			}
		}
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
		s.log.Error().Int("port", port).Msg("no session found for dynamic listener port")
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	s.log.Debug().
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
		s.log.Error().Err(err).Str("session_id", sessionID).Msg("failed to capture callback")
		s.writeCallbackErrorPage(w, "An error occurred. Please try again.")
		return
	}

	s.writeCallbackSuccessPage(w)
	// One-shot: the first callback is captured, so release the host port.
	// The session keeps the data for the agent to poll. Close off the
	// handler goroutine: Shutdown waits for in-flight handlers.
	go s.closeDynamicListener(port)
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

	if status, msg := s.checkEgress(req.URL); status != http.StatusOK {
		s.writeJSON(w, status, openURLResponse{Success: false, URL: req.URL, Error: msg})
		return
	}

	s.log.Debug().Str("url", req.URL).Msg("opening URL in browser")

	if openErr := s.openInBrowser(req.URL); openErr != nil {
		s.log.Error().Err(openErr).Str("url", req.URL).Msg("failed to open URL in browser")
		s.writeJSON(w, http.StatusInternalServerError, openURLResponse{
			Success: false,
			URL:     req.URL,
			Error:   openErr.Error(),
		})
		return
	}

	s.writeJSON(w, http.StatusOK, openURLResponse{
		Success: true,
		URL:     req.URL,
	})
}

// checkEgress applies the egress rules to a URL the host browser will
// open. It returns [http.StatusOK] when allowed (or the firewall is off),
// otherwise the status and message to answer with.
func (s *Server) checkEgress(rawURL string) (int, string) {
	if s.rulesFilePath == "" {
		return http.StatusOK, ""
	}
	err := CheckURLAgainstEgressRules(rawURL, s.rulesFilePath)
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, errEgressRulesInvalid):
		// Present-but-corrupt rules file (e.g. tampered after the daemon
		// started). Not a policy decision — log loudly with the real cause
		// and fail closed, mirroring Envoy rejecting an invalid config
		// rather than the soft per-request deny below.
		s.log.Error().Err(err).Str("url", rawURL).Msg("egress rules file invalid; denying all URL opens until fixed")
		return http.StatusInternalServerError, errEgressRulesUnavailable
	default:
		s.log.Warn().Err(err).Str("url", rawURL).Msg("blocked by egress rules")
		return http.StatusForbidden, "blocked by egress policy"
	}
}

// openInBrowser opens a URL with the configured browser function.
func (s *Server) openInBrowser(rawURL string) error {
	if s.browserFunc == nil {
		return openBrowser(rawURL)
	}
	return s.browserFunc(rawURL)
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
		s.log.Error().Err(err).Msg("failed to encode JSON response")
	}
}

// --- Callback channel types and handlers ---

// callbackRegisterRequest is the JSON request body for POST /callback/register.
type callbackRegisterRequest struct {
	AuthURL    string `json:"authUrl"`
	TTLSeconds int    `json:"ttlSeconds,omitempty"`
}

// callbackRegisterResponse is the JSON response body for POST /callback/register.
type callbackRegisterResponse struct {
	Success   bool   `json:"success"`
	SessionID string `json:"sessionId,omitempty"`
	Port      int    `json:"port,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	Error     string `json:"error,omitempty"`
}

// registerFailure is a failed register answer.
func registerFailure(msg string) callbackRegisterResponse {
	return callbackRegisterResponse{Success: false, SessionID: "", Port: 0, ExpiresAt: "", Error: msg}
}

// minCallbackPort keeps the callback listener off privileged ports.
const minCallbackPort = 1024

// callbackTarget extracts the localhost callback port and path from the
// auth URL's redirect_uri (or callback) parameter. Only an http redirect
// to localhost or 127.0.0.1 on an unprivileged port is accepted.
func callbackTarget(authURL string) (int, string, error) {
	parsed, err := url.Parse(authURL)
	if err != nil {
		return 0, "", fmt.Errorf("invalid auth URL: %w", err)
	}
	q := parsed.Query()
	raw := q.Get("redirect_uri")
	if raw == "" {
		raw = q.Get("callback")
	}
	if raw == "" {
		return 0, "", errors.New("auth URL has no redirect_uri")
	}
	redirect, err := url.Parse(raw)
	if err != nil {
		return 0, "", fmt.Errorf("invalid redirect_uri: %w", err)
	}
	host := redirect.Hostname()
	if redirect.Scheme != "http" || redirect.User != nil || (host != "localhost" && host != consts.Localhost) {
		return 0, "", errors.New("redirect_uri must be http://localhost:<port> or http://127.0.0.1:<port>")
	}
	port, err := strconv.Atoi(redirect.Port())
	if err != nil || port < minCallbackPort || port > 65535 {
		return 0, "", fmt.Errorf("redirect_uri port must be between %d and 65535", minCallbackPort)
	}
	path := redirect.Path
	if path == "" {
		path = "/callback"
	}
	return port, path, nil
}

// handleCallbackRegister handles POST /callback/register: the one-shot
// OAuth flow. It egress-checks the auth URL, takes the callback port and
// path from its localhost redirect, binds that port, and opens the auth
// URL in the host browser. The listener closes after the first callback.
func (s *Server) handleCallbackRegister(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req callbackRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, registerFailure("invalid JSON request body"))
		return
	}

	port, path, err := callbackTarget(req.AuthURL)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, registerFailure(err.Error()))
		return
	}
	if status, msg := s.checkEgress(req.AuthURL); status != http.StatusOK {
		s.writeJSON(w, status, registerFailure(msg))
		return
	}

	ttl := DefaultCallbackTTL
	if req.TTLSeconds > 0 {
		ttl = min(time.Duration(req.TTLSeconds)*time.Second, DefaultCallbackTTL)
	}

	session, err := s.callbackChannel.Register(port, path, ttl)
	if err != nil {
		s.log.Error().Err(err).Msg("failed to register callback session")
		s.writeJSON(w, http.StatusInternalServerError, registerFailure("failed to create session"))
		return
	}
	if listenErr := s.startDynamicListener(port, session.ID); listenErr != nil {
		s.log.Error().Err(listenErr).Int("port", port).Msg("failed to start dynamic listener")
		s.callbackChannel.Delete(session.ID)
		msg := fmt.Sprintf("failed to start listener on port %d", port)
		s.writeJSON(w, http.StatusInternalServerError, registerFailure(msg))
		return
	}
	if openErr := s.openInBrowser(req.AuthURL); openErr != nil {
		s.log.Error().Err(openErr).Msg("failed to open auth URL in browser")
		s.callbackChannel.Delete(session.ID)
		s.writeJSON(w, http.StatusInternalServerError, registerFailure("failed to open browser"))
		return
	}

	s.log.Info().
		Str("event", "host_proxy_callback_registered").
		Str("session_id", session.ID).
		Int("port", port).
		Str("path", path).
		Msg("callback session registered and auth URL opened")

	s.writeJSON(w, http.StatusOK, callbackRegisterResponse{
		Success:   true,
		SessionID: session.ID,
		Port:      port,
		ExpiresAt: session.ExpiresAt.Format(time.RFC3339),
		Error:     "",
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

// writeCallbackSuccessPage writes an HTML success page for OAuth callbacks.
func (s *Server) writeCallbackSuccessPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(callbackPage("Authentication Complete", callbackSuccessBody))); err != nil {
		s.log.Debug().Err(err).Msg("failed to write callback success page")
	}
}

// writeCallbackErrorPage writes an HTML error page for OAuth callbacks.
func (s *Server) writeCallbackErrorPage(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	if _, err := w.Write([]byte(callbackPage("Authentication Error", callbackErrorBody(message)))); err != nil {
		s.log.Debug().Err(err).Msg("failed to write callback error page")
	}
}

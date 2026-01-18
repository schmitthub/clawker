// Package hostproxy provides a host-side HTTP server that containers can call
// to perform actions on the host, such as opening URLs in the browser.
package hostproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/schmitthub/clawker/pkg/logger"
)

// DefaultPort is the default port for the host proxy server.
const DefaultPort = 18374

// Server is an HTTP server that handles requests from containers to perform
// host-side actions.
type Server struct {
	port     int
	listener net.Listener
	server   *http.Server
	mu       sync.RWMutex
	running  bool
}

// NewServer creates a new host proxy server on the specified port.
func NewServer(port int) *Server {
	return &Server{
		port: port,
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

	// Bind to localhost only for security
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.listener = listener
	s.server = &http.Server{Handler: mux}
	s.running = true

	go func() {
		logger.Debug().Str("addr", addr).Msg("host proxy server starting")
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("host proxy server error")
		}
	}()

	logger.Info().Str("addr", addr).Msg("host proxy server started")
	return nil
}

// Stop gracefully shuts down the server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.server == nil {
		return nil
	}

	s.running = false
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
	Status string `json:"status"`
}

// handleHealth handles GET /health requests for health checking.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, healthResponse{
		Status: "ok",
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

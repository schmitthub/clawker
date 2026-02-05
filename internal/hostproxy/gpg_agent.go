package hostproxy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"

	"github.com/schmitthub/clawker/internal/logger"
)

// maxGPGAgentMessageSize limits GPG agent message size (64KB should be plenty)
const maxGPGAgentMessageSize = 64 * 1024

// gpgAgentRequest is the JSON request body for POST /gpg/agent.
type gpgAgentRequest struct {
	// Data is the base64-encoded GPG agent protocol message
	Data string `json:"data"`
}

// gpgAgentResponse is the JSON response body for POST /gpg/agent.
type gpgAgentResponse struct {
	Success bool   `json:"success"`
	Data    string `json:"data,omitempty"` // base64-encoded response
	Error   string `json:"error,omitempty"`
}

// getGPGExtraSocket returns the path to the GPG agent's extra socket.
// The extra socket is designed for restricted remote access.
func getGPGExtraSocket() string {
	cmd := exec.Command("gpgconf", "--list-dir", "agent-extra-socket")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// handleGPGAgent handles POST /gpg/agent requests.
// It acts as a bridge between the container's gpg-agent-proxy and the host's GPG agent.
// The GPG agent uses the Assuan protocol which is line-based, but we forward raw bytes
// to handle both the initial handshake and subsequent commands.
func (s *Server) handleGPGAgent(w http.ResponseWriter, r *http.Request) {
	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req gpgAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, gpgAgentResponse{
			Success: false,
			Error:   "invalid JSON request body",
		})
		return
	}

	// Validate request
	if req.Data == "" {
		s.writeJSON(w, http.StatusBadRequest, gpgAgentResponse{
			Success: false,
			Error:   "data field is required",
		})
		return
	}

	// Decode base64 data
	msgData, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, gpgAgentResponse{
			Success: false,
			Error:   "invalid base64 data",
		})
		return
	}

	// Validate message size
	if len(msgData) > maxGPGAgentMessageSize {
		s.writeJSON(w, http.StatusBadRequest, gpgAgentResponse{
			Success: false,
			Error:   "message too large",
		})
		return
	}

	logger.Debug().
		Int("msg_size", len(msgData)).
		Msg("GPG agent request")

	// Get GPG extra socket path
	gpgSocket := getGPGExtraSocket()
	if gpgSocket == "" {
		logger.Debug().Msg("GPG extra socket not available")
		s.writeJSON(w, http.StatusServiceUnavailable, gpgAgentResponse{
			Success: false,
			Error:   "GPG agent not available (gpgconf failed or extra socket not found)",
		})
		return
	}

	// Connect to GPG agent
	conn, err := net.Dial("unix", gpgSocket)
	if err != nil {
		logger.Debug().Err(err).Str("socket", gpgSocket).Msg("failed to connect to GPG agent")
		s.writeJSON(w, http.StatusServiceUnavailable, gpgAgentResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to connect to GPG agent: %v", err),
		})
		return
	}
	defer conn.Close()

	// Send message to agent
	if _, err := conn.Write(msgData); err != nil {
		logger.Debug().Err(err).Msg("failed to write to GPG agent")
		s.writeJSON(w, http.StatusInternalServerError, gpgAgentResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to send to GPG agent: %v", err),
		})
		return
	}

	// Read response from GPG agent
	// The Assuan protocol is line-based, responses end with OK, ERR, or other status lines
	// We read until we get a complete response (ending with OK or ERR line)
	responseBuf := make([]byte, maxGPGAgentMessageSize)
	n, err := conn.Read(responseBuf)
	if err != nil && err != io.EOF {
		logger.Debug().Err(err).Msg("failed to read GPG agent response")
		s.writeJSON(w, http.StatusInternalServerError, gpgAgentResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to read from GPG agent: %v", err),
		})
		return
	}

	logger.Debug().
		Int("response_size", n).
		Msg("GPG agent response")

	// Encode response as base64
	s.writeJSON(w, http.StatusOK, gpgAgentResponse{
		Success: true,
		Data:    base64.StdEncoding.EncodeToString(responseBuf[:n]),
	})
}

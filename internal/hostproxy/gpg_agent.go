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
//
// NOTE: A similar function exists in internal/workspace/gpg.go (GetGPGExtraSocketPath).
// The duplication is intentional: hostproxy is a server-side package that handles
// forwarding requests from containers, while workspace provides container configuration.
// Each package needs the socket path for different purposes. The hostproxy version
// returns an error for better diagnostics, while workspace logs and returns empty string.
func getGPGExtraSocket() (string, error) {
	cmd := exec.Command("gpgconf", "--list-dir", "agent-extra-socket")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gpgconf failed: %w", err)
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", fmt.Errorf("gpgconf returned empty socket path")
	}
	return path, nil
}

// readAssuanResponse reads a complete Assuan protocol response from the connection.
// The response is complete when a line starts with "OK" or "ERR".
// Returns the complete response bytes or an error.
func readAssuanResponse(conn net.Conn) ([]byte, error) {
	var response []byte
	buf := make([]byte, 4096)

	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err == io.EOF && len(response) > 0 {
				// EOF with data means response is complete
				return response, nil
			}
			return nil, err
		}

		response = append(response, buf[:n]...)

		// Check if response is complete (ends with OK or ERR line)
		if isAssuanResponseComplete(response) {
			return response, nil
		}

		// Safety check: don't read more than maxGPGAgentMessageSize
		if len(response) > maxGPGAgentMessageSize {
			return nil, fmt.Errorf("response exceeds maximum size (%d bytes)", maxGPGAgentMessageSize)
		}
	}
}

// isAssuanResponseComplete checks if an Assuan response buffer contains a complete response.
// A response is complete when the last line starts with "OK" or "ERR".
func isAssuanResponseComplete(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	// Find the last newline to get the last complete line
	// Assuan responses end with newline
	if data[len(data)-1] != '\n' {
		return false
	}

	// Find the start of the last line
	lastLineStart := len(data) - 1
	for lastLineStart > 0 && data[lastLineStart-1] != '\n' {
		lastLineStart--
	}

	lastLine := data[lastLineStart : len(data)-1] // Exclude trailing newline

	// Check if last line starts with OK or ERR
	return len(lastLine) >= 2 && (string(lastLine[:2]) == "OK" || (len(lastLine) >= 3 && string(lastLine[:3]) == "ERR"))
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
	gpgSocket, err := getGPGExtraSocket()
	if err != nil {
		logger.Debug().Err(err).Msg("GPG extra socket not available")
		s.writeJSON(w, http.StatusServiceUnavailable, gpgAgentResponse{
			Success: false,
			Error:   fmt.Sprintf("GPG agent not available: %v", err),
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

	// Read response from GPG agent using Assuan-aware reading.
	// The Assuan protocol is line-based with responses ending in status lines:
	// - "OK" or "OK <message>" indicates success
	// - "ERR <code> <message>" indicates error
	// We read until we encounter a complete response ending with OK/ERR.
	response, err := readAssuanResponse(conn)
	if err != nil {
		logger.Debug().Err(err).Msg("failed to read GPG agent response")
		s.writeJSON(w, http.StatusInternalServerError, gpgAgentResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to read from GPG agent: %v", err),
		})
		return
	}

	logger.Debug().
		Int("response_size", len(response)).
		Msg("GPG agent response")

	// Encode response as base64
	s.writeJSON(w, http.StatusOK, gpgAgentResponse{
		Success: true,
		Data:    base64.StdEncoding.EncodeToString(response),
	})
}

package hostproxy

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"

	"github.com/schmitthub/clawker/pkg/logger"
)

// maxSSHAgentMessageSize limits SSH agent message size (64KB should be plenty)
const maxSSHAgentMessageSize = 64 * 1024

// sshAgentRequest is the JSON request body for POST /ssh/agent.
type sshAgentRequest struct {
	// Data is the base64-encoded SSH agent protocol message
	Data string `json:"data"`
}

// sshAgentResponse is the JSON response body for POST /ssh/agent.
type sshAgentResponse struct {
	Success bool   `json:"success"`
	Data    string `json:"data,omitempty"` // base64-encoded response
	Error   string `json:"error,omitempty"`
}

// handleSSHAgent handles POST /ssh/agent requests.
// It acts as a bridge between the container's ssh-agent-proxy and the host's SSH agent.
func (s *Server) handleSSHAgent(w http.ResponseWriter, r *http.Request) {
	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req sshAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, sshAgentResponse{
			Success: false,
			Error:   "invalid JSON request body",
		})
		return
	}

	// Validate request
	if req.Data == "" {
		s.writeJSON(w, http.StatusBadRequest, sshAgentResponse{
			Success: false,
			Error:   "data field is required",
		})
		return
	}

	// Decode base64 data
	msgData, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, sshAgentResponse{
			Success: false,
			Error:   "invalid base64 data",
		})
		return
	}

	// Validate message size
	if len(msgData) > maxSSHAgentMessageSize {
		s.writeJSON(w, http.StatusBadRequest, sshAgentResponse{
			Success: false,
			Error:   "message too large",
		})
		return
	}

	logger.Debug().
		Int("msg_size", len(msgData)).
		Msg("SSH agent request")

	// Get SSH_AUTH_SOCK from environment
	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	if sshAuthSock == "" {
		logger.Debug().Msg("SSH_AUTH_SOCK not set")
		s.writeJSON(w, http.StatusServiceUnavailable, sshAgentResponse{
			Success: false,
			Error:   "SSH agent not available (SSH_AUTH_SOCK not set)",
		})
		return
	}

	// Connect to SSH agent
	conn, err := net.Dial("unix", sshAuthSock)
	if err != nil {
		logger.Debug().Err(err).Str("socket", sshAuthSock).Msg("failed to connect to SSH agent")
		s.writeJSON(w, http.StatusServiceUnavailable, sshAgentResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to connect to SSH agent: %v", err),
		})
		return
	}
	defer conn.Close()

	// Send message to agent
	if _, err := conn.Write(msgData); err != nil {
		logger.Debug().Err(err).Msg("failed to write to SSH agent")
		s.writeJSON(w, http.StatusInternalServerError, sshAgentResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to send to SSH agent: %v", err),
		})
		return
	}

	// Read response length (4 bytes, big-endian)
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		logger.Debug().Err(err).Msg("failed to read SSH agent response length")
		s.writeJSON(w, http.StatusInternalServerError, sshAgentResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to read from SSH agent: %v", err),
		})
		return
	}

	responseLen := binary.BigEndian.Uint32(lenBuf)
	if responseLen > maxSSHAgentMessageSize {
		logger.Debug().Uint32("response_len", responseLen).Msg("SSH agent response too large")
		s.writeJSON(w, http.StatusInternalServerError, sshAgentResponse{
			Success: false,
			Error:   "SSH agent response too large",
		})
		return
	}

	// Read response body
	responseBuf := make([]byte, 4+responseLen)
	copy(responseBuf[:4], lenBuf)
	if _, err := io.ReadFull(conn, responseBuf[4:]); err != nil {
		logger.Debug().Err(err).Msg("failed to read SSH agent response body")
		s.writeJSON(w, http.StatusInternalServerError, sshAgentResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to read from SSH agent: %v", err),
		})
		return
	}

	logger.Debug().
		Int("response_size", len(responseBuf)).
		Msg("SSH agent response")

	// Encode response as base64
	s.writeJSON(w, http.StatusOK, sshAgentResponse{
		Success: true,
		Data:    base64.StdEncoding.EncodeToString(responseBuf),
	})
}

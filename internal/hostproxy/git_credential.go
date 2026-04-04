package hostproxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"

	"github.com/schmitthub/clawker/internal/logger"
)

// gitCredentialRequest is the JSON request body for POST /git/credential.
type gitCredentialRequest struct {
	Action   string `json:"action"`   // "get", "store", or "erase"
	Protocol string `json:"protocol"` // "https" typically
	Host     string `json:"host"`     // e.g., "github.com"
	Path     string `json:"path,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"` // Only for store/erase
}

// gitCredentialResponse is the JSON response body for POST /git/credential.
type gitCredentialResponse struct {
	Success  bool   `json:"success"`
	Protocol string `json:"protocol,omitempty"`
	Host     string `json:"host,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleGitCredential handles POST /git/credential requests.
// It acts as a bridge between the container's git-credential-clawker helper
// and the host's git credential system.
func (s *Server) handleGitCredential(w http.ResponseWriter, r *http.Request) {
	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)

	var req gitCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, gitCredentialResponse{
			Success: false,
			Error:   "invalid JSON request body",
		})
		return
	}

	// Validate action
	switch req.Action {
	case "get", "store", "erase":
		// Valid actions
	default:
		s.writeJSON(w, http.StatusBadRequest, gitCredentialResponse{
			Success: false,
			Error:   "action must be 'get', 'store', or 'erase'",
		})
		return
	}

	// Validate required fields
	if req.Protocol == "" {
		s.writeJSON(w, http.StatusBadRequest, gitCredentialResponse{
			Success: false,
			Error:   "protocol is required",
		})
		return
	}
	if req.Host == "" {
		s.writeJSON(w, http.StatusBadRequest, gitCredentialResponse{
			Success: false,
			Error:   "host is required",
		})
		return
	}

	// Reject fields containing newlines or null bytes — these allow injection
	// of arbitrary key=value pairs into the git credential protocol format.
	for _, field := range []string{req.Protocol, req.Host, req.Path, req.Username, req.Password} {
		if containsCredentialInjectionChars(field) {
			s.log.Warn().
				Str("action", req.Action).
				Str("host", req.Host).
				Msg("rejected git credential request: fields contain injection characters")
			s.writeJSON(w, http.StatusBadRequest, gitCredentialResponse{
				Success: false,
				Error:   "credential fields must not contain newlines or null bytes",
			})
			return
		}
	}

	// Log request (without password)
	s.log.Debug().
		Str("action", req.Action).
		Str("protocol", req.Protocol).
		Str("host", req.Host).
		Str("username", req.Username).
		Msg("git credential request")

	// Convert to git credential protocol format
	input := formatGitCredentialInput(req)

	// Map user-friendly action names to git credential subcommands.
	// Git uses different terminology: "fill" retrieves, "approve" stores, "reject" erases.
	var gitAction string
	switch req.Action {
	case "get":
		gitAction = "fill"
	case "store":
		gitAction = "approve"
	case "erase":
		gitAction = "reject"
	}

	// Execute git credential on host
	cmd := exec.Command("git", "credential", gitAction)
	cmd.Stdin = strings.NewReader(input)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		s.log.Debug().
			Str("action", req.Action).
			Str("host", req.Host).
			Str("stderr", errMsg).
			Msg("git credential command failed")
		s.writeJSON(w, http.StatusOK, gitCredentialResponse{
			Success: false,
			Error:   "credential helper failed: " + errMsg,
		})
		return
	}

	// For get action, parse the output and return credentials
	if req.Action == "get" {
		creds := parseGitCredentialOutput(stdout.String(), s.log)
		// Log success without password
		s.log.Debug().
			Str("host", req.Host).
			Str("username", creds.Username).
			Bool("has_password", creds.Password != "").
			Msg("git credential retrieved")

		s.writeJSON(w, http.StatusOK, gitCredentialResponse{
			Success:  true,
			Protocol: creds.Protocol,
			Host:     creds.Host,
			Username: creds.Username,
			Password: creds.Password,
		})
		return
	}

	// For store/erase, just return success
	s.writeJSON(w, http.StatusOK, gitCredentialResponse{
		Success: true,
	})
}

// containsCredentialInjectionChars returns true if s contains characters that
// could inject additional key=value pairs into the git credential protocol.
func containsCredentialInjectionChars(s string) bool {
	return strings.ContainsAny(s, "\n\r\x00")
}

// credentialFieldSanitizer strips newline and null bytes from git credential
// field values. This is defense-in-depth — the handler rejects such requests
// before reaching this point.
var credentialFieldSanitizer = strings.NewReplacer("\n", "", "\r", "", "\x00", "")

// formatGitCredentialInput formats a request as git credential protocol input.
// Format is: key=value\n pairs ending with a blank line.
// All field values are sanitized to prevent newline injection attacks.
func formatGitCredentialInput(req gitCredentialRequest) string {
	var sb strings.Builder

	sb.WriteString("protocol=")
	sb.WriteString(credentialFieldSanitizer.Replace(req.Protocol))
	sb.WriteString("\n")

	sb.WriteString("host=")
	sb.WriteString(credentialFieldSanitizer.Replace(req.Host))
	sb.WriteString("\n")

	path := credentialFieldSanitizer.Replace(req.Path)
	if path != "" {
		sb.WriteString("path=")
		sb.WriteString(path)
		sb.WriteString("\n")
	}

	username := credentialFieldSanitizer.Replace(req.Username)
	if username != "" {
		sb.WriteString("username=")
		sb.WriteString(username)
		sb.WriteString("\n")
	}

	password := credentialFieldSanitizer.Replace(req.Password)
	if password != "" {
		sb.WriteString("password=")
		sb.WriteString(password)
		sb.WriteString("\n")
	}

	// Terminate with blank line
	sb.WriteString("\n")

	return sb.String()
}

// gitCredentials holds parsed git credential output.
type gitCredentials struct {
	Protocol string
	Host     string
	Username string
	Password string
}

// parseGitCredentialOutput parses git credential protocol output.
func parseGitCredentialOutput(output string, log *logger.Logger) gitCredentials {
	var creds gitCredentials
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value := parts[1]

		switch key {
		case "protocol":
			creds.Protocol = value
		case "host":
			creds.Host = value
		case "username":
			creds.Username = value
		case "password":
			creds.Password = value
		}
	}

	if err := scanner.Err(); err != nil {
		log.Debug().Err(err).Msg("error scanning git credential output")
	}

	return creds
}

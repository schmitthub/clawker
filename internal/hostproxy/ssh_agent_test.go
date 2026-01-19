package hostproxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleSSHAgent(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantError  string
	}{
		{
			name:       "invalid JSON",
			body:       "not json",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid JSON request body",
		},
		{
			name:       "missing data field",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "data field is required",
		},
		{
			name:       "empty data field",
			body:       `{"data": ""}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "data field is required",
		},
		{
			name:       "invalid base64",
			body:       `{"data": "not-valid-base64!!!"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid base64 data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/ssh/agent", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			s.handleSSHAgent(w, req)

			resp := w.Result()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}

			var result sshAgentResponse
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if result.Success {
				t.Error("expected success to be false")
			}

			if result.Error != tt.wantError {
				t.Errorf("expected error %q, got %q", tt.wantError, result.Error)
			}
		})
	}
}

func TestHandleSSHAgentMessageTooLarge(t *testing.T) {
	s := &Server{}

	// Create data larger than maxSSHAgentMessageSize (64KB)
	largeData := make([]byte, maxSSHAgentMessageSize+1)
	for i := range largeData {
		largeData[i] = 'a'
	}
	encoded := base64.StdEncoding.EncodeToString(largeData)

	body := `{"data": "` + encoded + `"}`
	req := httptest.NewRequest(http.MethodPost, "/ssh/agent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleSSHAgent(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for oversized message, got %d", resp.StatusCode)
	}

	var result sshAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.Success {
		t.Error("expected success to be false")
	}

	if result.Error != "message too large" {
		t.Errorf("expected error %q, got %q", "message too large", result.Error)
	}
}

func TestHandleSSHAgentNoSSHAuthSock(t *testing.T) {
	// Temporarily unset SSH_AUTH_SOCK if it's set
	t.Setenv("SSH_AUTH_SOCK", "")

	s := &Server{}

	// Valid small message (just needs to pass validation to hit SSH_AUTH_SOCK check)
	validData := base64.StdEncoding.EncodeToString([]byte("test"))
	body := `{"data": "` + validData + `"}`
	req := httptest.NewRequest(http.MethodPost, "/ssh/agent", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleSSHAgent(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 when SSH_AUTH_SOCK not set, got %d", resp.StatusCode)
	}

	var result sshAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.Success {
		t.Error("expected success to be false")
	}

	if result.Error != "SSH agent not available (SSH_AUTH_SOCK not set)" {
		t.Errorf("expected error about SSH_AUTH_SOCK not set, got %q", result.Error)
	}
}

func TestHandleSSHAgentBodySizeLimit(t *testing.T) {
	s := &Server{}

	// Create a body larger than maxRequestBodySize (1MB)
	largeBody := make([]byte, maxRequestBodySize+1)
	for i := range largeBody {
		largeBody[i] = 'a'
	}

	req := httptest.NewRequest(http.MethodPost, "/ssh/agent", bytes.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleSSHAgent(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for oversized body, got %d", resp.StatusCode)
	}

	var result sshAgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.Success {
		t.Error("expected success to be false")
	}
}

package hostproxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleGPGAgent_InvalidJSON(t *testing.T) {
	server := NewServer(0)
	req := httptest.NewRequest("POST", "/gpg/agent", bytes.NewReader([]byte("invalid json")))
	w := httptest.NewRecorder()

	server.handleGPGAgent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp gpgAgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success = false")
	}
	if resp.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestHandleGPGAgent_EmptyData(t *testing.T) {
	server := NewServer(0)

	reqBody := gpgAgentRequest{Data: ""}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/gpg/agent", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handleGPGAgent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp gpgAgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success = false")
	}
	if resp.Error != "data field is required" {
		t.Errorf("expected error 'data field is required', got %q", resp.Error)
	}
}

func TestHandleGPGAgent_InvalidBase64(t *testing.T) {
	server := NewServer(0)

	reqBody := gpgAgentRequest{Data: "not-valid-base64!!!"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/gpg/agent", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handleGPGAgent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp gpgAgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success = false")
	}
	if resp.Error != "invalid base64 data" {
		t.Errorf("expected error 'invalid base64 data', got %q", resp.Error)
	}
}

func TestHandleGPGAgent_MessageTooLarge(t *testing.T) {
	server := NewServer(0)

	// Create a message larger than maxGPGAgentMessageSize
	largeData := make([]byte, maxGPGAgentMessageSize+1)
	reqBody := gpgAgentRequest{Data: base64.StdEncoding.EncodeToString(largeData)}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/gpg/agent", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handleGPGAgent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp gpgAgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("expected Success = false")
	}
	if resp.Error != "message too large" {
		t.Errorf("expected error 'message too large', got %q", resp.Error)
	}
}

func TestHandleGPGAgent_NoGPGSocket(t *testing.T) {
	server := NewServer(0)

	// Send a valid request but with no GPG socket available
	// This will fail because gpgconf either doesn't exist or returns no socket
	testData := []byte("OPTION ttyname=/dev/pts/0\n")
	reqBody := gpgAgentRequest{Data: base64.StdEncoding.EncodeToString(testData)}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/gpg/agent", bytes.NewReader(body))
	w := httptest.NewRecorder()

	server.handleGPGAgent(w, req)

	// Should return service unavailable if no GPG socket
	// or success if GPG is available (depends on system)
	var resp gpgAgentResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// The response depends on whether GPG is available on the system
	// We just verify the response is valid JSON with expected fields
	t.Logf("GPG agent response: success=%v, error=%q", resp.Success, resp.Error)
}

func TestGetGPGExtraSocket(t *testing.T) {
	// Test the internal helper function
	socket, err := getGPGExtraSocket()
	t.Logf("getGPGExtraSocket() = %q, err = %v", socket, err)

	// We can't assert a specific value as it depends on the system
	// The function now returns an error instead of empty string on failure
}

func TestIsAssuanResponseComplete(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{"empty", []byte{}, false},
		{"no newline", []byte("OK"), false},
		{"OK response", []byte("OK\n"), true},
		{"OK with message", []byte("OK Pleased to meet you\n"), true},
		{"ERR response", []byte("ERR 67108922 agent not running\n"), true},
		{"incomplete", []byte("D data\n"), false},
		{"multi-line ending OK", []byte("D data\nOK\n"), true},
		{"multi-line ending ERR", []byte("D data\nERR 123 error\n"), true},
		{"partial OK", []byte("O"), false},
		{"partial line no newline", []byte("D data\nOK"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAssuanResponseComplete(tt.data)
			if result != tt.expected {
				t.Errorf("isAssuanResponseComplete(%q) = %v, want %v", tt.data, result, tt.expected)
			}
		})
	}
}

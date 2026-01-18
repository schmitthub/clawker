package hostproxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerHealthEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	s.handleHealth(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var result healthResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", result.Status)
	}

	if result.Service != "clawker-host-proxy" {
		t.Errorf("expected service 'clawker-host-proxy', got %q", result.Service)
	}
}

func TestServerOpenURLEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantError  string
	}{
		{
			name:       "invalid json",
			body:       "not json",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid JSON request body",
		},
		{
			name:       "empty url",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "url field is required",
		},
		{
			name:       "empty url string",
			body:       `{"url": ""}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "url field is required",
		},
		{
			name:       "file scheme rejected",
			body:       `{"url": "file:///etc/passwd"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "only http and https URLs are allowed",
		},
		{
			name:       "javascript scheme rejected",
			body:       `{"url": "javascript:alert(1)"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "only http and https URLs are allowed",
		},
		{
			name:       "data scheme rejected",
			body:       `{"url": "data:text/html,<script>alert(1)</script>"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "only http and https URLs are allowed",
		},
		{
			name:       "relative url rejected",
			body:       `{"url": "/etc/passwd"}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "only http and https URLs are allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{}
			req := httptest.NewRequest(http.MethodPost, "/open/url", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			s.handleOpenURL(w, req)

			resp := w.Result()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}

			var result openURLResponse
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

func TestServerPort(t *testing.T) {
	s := NewServer(12345)
	if s.Port() != 12345 {
		t.Errorf("expected port 12345, got %d", s.Port())
	}
}

func TestServerIsRunning(t *testing.T) {
	s := NewServer(0)
	if s.IsRunning() {
		t.Error("expected server to not be running initially")
	}
}

func TestServerOpenURLBodySizeLimit(t *testing.T) {
	s := &Server{}

	// Create a body larger than maxRequestBodySize (1MB)
	largeBody := make([]byte, maxRequestBodySize+1)
	for i := range largeBody {
		largeBody[i] = 'a'
	}

	req := httptest.NewRequest(http.MethodPost, "/open/url", bytes.NewReader(largeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleOpenURL(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for oversized body, got %d", resp.StatusCode)
	}

	var result openURLResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.Success {
		t.Error("expected success to be false")
	}

	// The error message should indicate invalid JSON (because the body was truncated)
	if result.Error != "invalid JSON request body" {
		t.Errorf("expected error 'invalid JSON request body', got %q", result.Error)
	}
}

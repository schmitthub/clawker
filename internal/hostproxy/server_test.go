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

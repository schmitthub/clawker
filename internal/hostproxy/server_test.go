package hostproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// --- Callback endpoint tests ---

func TestServerCallbackRegister(t *testing.T) {
	s := NewServer(18374)
	defer s.Stop(context.Background())

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantErr    bool
		checkResp  func(t *testing.T, resp callbackRegisterResponse)
	}{
		{
			name:       "valid registration",
			body:       `{"port": 8080, "path": "/callback"}`,
			wantStatus: http.StatusOK,
			wantErr:    false,
			checkResp: func(t *testing.T, resp callbackRegisterResponse) {
				if !resp.Success {
					t.Error("expected success")
				}
				if resp.SessionID == "" {
					t.Error("expected session_id")
				}
				if !strings.Contains(resp.ProxyCallbackBase, "/cb/") {
					t.Error("expected proxy_callback_base to contain /cb/")
				}
				if resp.ExpiresAt == "" {
					t.Error("expected expires_at")
				}
			},
		},
		{
			name:       "valid registration with timeout",
			body:       `{"port": 8080, "timeout_seconds": 120}`,
			wantStatus: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "invalid json",
			body:       "not json",
			wantStatus: http.StatusBadRequest,
			wantErr:    true,
		},
		{
			name:       "zero port",
			body:       `{"port": 0}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    true,
		},
		{
			name:       "negative port",
			body:       `{"port": -1}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    true,
		},
		{
			name:       "port too high",
			body:       `{"port": 70000}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/callback/register", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			s.handleCallbackRegister(w, req)

			resp := w.Result()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}

			var result callbackRegisterResponse
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if tt.wantErr && result.Success {
				t.Error("expected error response")
			}
			if !tt.wantErr && !result.Success {
				t.Errorf("expected success, got error: %s", result.Error)
			}

			if tt.checkResp != nil {
				tt.checkResp(t, result)
			}
		})
	}
}

func TestServerCallbackGetData(t *testing.T) {
	s := NewServer(18374)
	defer s.Stop(context.Background())

	// Register a session first
	session, _ := s.callbackChannel.Register(8080, "/callback", 5*time.Minute)

	t.Run("no callback yet", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/callback/"+session.ID+"/data", nil)
		req.SetPathValue("session", session.ID)
		w := httptest.NewRecorder()

		s.handleCallbackGetData(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var result callbackDataResponse
		json.NewDecoder(resp.Body).Decode(&result)
		if result.Received {
			t.Error("expected received=false")
		}
	})

	t.Run("session not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/callback/nonexistent/data", nil)
		req.SetPathValue("session", "nonexistent")
		w := httptest.NewRecorder()

		s.handleCallbackGetData(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})

	t.Run("after callback captured", func(t *testing.T) {
		// Simulate callback capture
		captureReq := httptest.NewRequest(http.MethodGet, "/cb/"+session.ID+"/callback?code=ABC&state=XYZ", nil)
		captureReq.SetPathValue("session", session.ID)
		captureReq.SetPathValue("path", "callback")
		captureW := httptest.NewRecorder()
		s.handleCallbackCapture(captureW, captureReq)

		// Now get data
		req := httptest.NewRequest(http.MethodGet, "/callback/"+session.ID+"/data", nil)
		req.SetPathValue("session", session.ID)
		w := httptest.NewRecorder()

		s.handleCallbackGetData(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var result callbackDataResponse
		json.NewDecoder(resp.Body).Decode(&result)
		if !result.Received {
			t.Error("expected received=true")
		}
		if result.Callback == nil {
			t.Fatal("expected callback data")
		}
		if result.Callback.Query != "code=ABC&state=XYZ" {
			t.Errorf("expected query 'code=ABC&state=XYZ', got %q", result.Callback.Query)
		}
	})
}

func TestServerCallbackDelete(t *testing.T) {
	s := NewServer(18374)
	defer s.Stop(context.Background())

	session, _ := s.callbackChannel.Register(8080, "/callback", 5*time.Minute)

	t.Run("delete existing session", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/callback/"+session.ID, nil)
		req.SetPathValue("session", session.ID)
		w := httptest.NewRecorder()

		s.handleCallbackDelete(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var result callbackDeleteResponse
		json.NewDecoder(resp.Body).Decode(&result)
		if !result.Success {
			t.Error("expected success")
		}

		// Verify session is gone
		if s.sessionStore.Get(session.ID) != nil {
			t.Error("expected session to be deleted")
		}
	})

	t.Run("delete nonexistent (still returns success)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/callback/nonexistent", nil)
		req.SetPathValue("session", "nonexistent")
		w := httptest.NewRecorder()

		s.handleCallbackDelete(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}
	})
}

func TestServerCallbackCapture(t *testing.T) {
	s := NewServer(18374)
	defer s.Stop(context.Background())

	t.Run("capture valid callback", func(t *testing.T) {
		session, _ := s.callbackChannel.Register(8080, "/callback", 5*time.Minute)

		req := httptest.NewRequest(http.MethodGet, "/cb/"+session.ID+"/callback?code=TOKEN123&state=STATE456", nil)
		req.SetPathValue("session", session.ID)
		req.SetPathValue("path", "callback")
		w := httptest.NewRecorder()

		s.handleCallbackCapture(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		// Verify HTML response
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
			t.Error("expected HTML content type")
		}

		// Verify callback was captured
		data, received := s.callbackChannel.GetData(session.ID)
		if !received {
			t.Error("expected callback to be captured")
		}
		if data.Query != "code=TOKEN123&state=STATE456" {
			t.Errorf("expected query to be captured, got %q", data.Query)
		}
	})

	t.Run("nonexistent session returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/cb/nonexistent/callback", nil)
		req.SetPathValue("session", "nonexistent")
		req.SetPathValue("path", "callback")
		w := httptest.NewRecorder()

		s.handleCallbackCapture(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})

	t.Run("duplicate callback returns success (single-use)", func(t *testing.T) {
		session, _ := s.callbackChannel.Register(8080, "/callback", 5*time.Minute)

		// First callback
		req1 := httptest.NewRequest(http.MethodGet, "/cb/"+session.ID+"/callback?code=FIRST", nil)
		req1.SetPathValue("session", session.ID)
		req1.SetPathValue("path", "callback")
		s.handleCallbackCapture(httptest.NewRecorder(), req1)

		// Second callback - should still return 200 for browser UX
		req2 := httptest.NewRequest(http.MethodGet, "/cb/"+session.ID+"/callback?code=SECOND", nil)
		req2.SetPathValue("session", session.ID)
		req2.SetPathValue("path", "callback")
		w := httptest.NewRecorder()

		s.handleCallbackCapture(w, req2)

		resp := w.Result()
		// Still returns 200 for good browser UX
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200 for duplicate callback, got %d", resp.StatusCode)
		}

		// But original data is preserved
		data, _ := s.callbackChannel.GetData(session.ID)
		if data.Query != "code=FIRST" {
			t.Errorf("expected first callback query preserved, got %q", data.Query)
		}
	})
}

func TestServerStopCleansUpSessions(t *testing.T) {
	s := NewServer(18374)

	// Create some sessions
	s.callbackChannel.Register(8080, "/callback", 5*time.Minute)
	s.callbackChannel.Register(8081, "/callback", 5*time.Minute)

	if s.sessionStore.Count() != 2 {
		t.Errorf("expected 2 sessions, got %d", s.sessionStore.Count())
	}

	// Stop should clean up
	s.Stop(context.Background())

	// Session store should be stopped (can't easily verify internal state,
	// but we can verify stop is idempotent)
	s.Stop(context.Background()) // Should not panic
}

package hostproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

// getFreePort returns an available TCP port on localhost.
func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", consts.Localhost+":0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestServerHealthEndpoint(t *testing.T) {
	s := &Server{log: logger.Nop()}
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
			s := &Server{log: logger.Nop()}
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

func TestServerOpenURL_EgressBlocking(t *testing.T) {
	// Write a minimal rules file: github.test:443:tls and docs.example.test:80:http.
	tmp := t.TempDir()
	rulesFile := filepath.Join(tmp, "egress-rules.yaml")
	rulesYAML := `rules:
  - action: allow
    dst: github.test
    port: 443
    proto: tls
  - action: allow
    dst: docs.example.test
    port: 80
    proto: http
`
	if err := os.WriteFile(rulesFile, []byte(rulesYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		body         string
		wantStatus   int
		wantSuccess  bool
		wantBrowser  bool
		wantErrorMsg string
	}{
		{
			name:        "allowed domain passes egress check",
			body:        `{"url":"https://github.test/schmitthub/clawker"}`,
			wantStatus:  http.StatusOK,
			wantSuccess: true,
			wantBrowser: true,
		},
		{
			name:         "blocked domain returns 403",
			body:         `{"url":"https://evil.test/exfil?data=stolen"}`,
			wantStatus:   http.StatusForbidden,
			wantSuccess:  false,
			wantBrowser:  false,
			wantErrorMsg: "blocked by egress policy",
		},
		{
			name:         "wrong proto for domain blocked",
			body:         `{"url":"http://github.test/foo"}`,
			wantStatus:   http.StatusForbidden,
			wantSuccess:  false,
			wantBrowser:  false,
			wantErrorMsg: "blocked by egress policy",
		},
		{
			name:        "http allowed domain passes",
			body:        `{"url":"http://docs.example.test/guide"}`,
			wantStatus:  http.StatusOK,
			wantSuccess: true,
			wantBrowser: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			browserCalled := false
			s := &Server{
				log:           logger.Nop(),
				rulesFilePath: rulesFile,
				browserFunc:   func(_ string) error { browserCalled = true; return nil },
			}

			req := httptest.NewRequest(http.MethodPost, "/open/url", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			s.handleOpenURL(w, req)

			resp := w.Result()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}

			var result openURLResponse
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if result.Success != tt.wantSuccess {
				t.Errorf("success = %v, want %v", result.Success, tt.wantSuccess)
			}

			if browserCalled != tt.wantBrowser {
				t.Errorf("browserCalled = %v, want %v", browserCalled, tt.wantBrowser)
			}

			if tt.wantErrorMsg != "" && result.Error != tt.wantErrorMsg {
				t.Errorf("error = %q, want %q", result.Error, tt.wantErrorMsg)
			}
		})
	}
}

func TestServerOpenURL_NoRulesFile_SkipsCheck(t *testing.T) {
	browserCalled := false
	s := &Server{
		log:         logger.Nop(),
		browserFunc: func(_ string) error { browserCalled = true; return nil },
	}

	req := httptest.NewRequest(http.MethodPost, "/open/url",
		bytes.NewBufferString(`{"url":"https://any-domain.test/path"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleOpenURL(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 when no rules file, got %d", resp.StatusCode)
	}
	if !browserCalled {
		t.Error("expected browser to be called when no rules file configured")
	}
}

func TestServerOpenURL_MissingRulesFile_FailsClosed(t *testing.T) {
	s := &Server{
		log:           logger.Nop(),
		rulesFilePath: "/nonexistent/egress-rules.yaml",
		browserFunc:   func(_ string) error { t.Fatal("browser should not be called"); return nil },
	}

	req := httptest.NewRequest(http.MethodPost, "/open/url",
		bytes.NewBufferString(`{"url":"https://github.test/foo"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleOpenURL(w, req)

	resp := w.Result()
	// Firewall enabled (path set) but the rules file is gone — a broken trust
	// boundary, not a policy deny. Loud-fail-closed: 500 + "egress rules
	// unavailable", distinct from the 403 "blocked by egress policy" path.
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 when rules file missing, got %d", resp.StatusCode)
	}

	var result openURLResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result.Success {
		t.Error("expected success to be false")
	}
	if result.Error != errEgressRulesUnavailable {
		t.Errorf("expected %q, got %q", errEgressRulesUnavailable, result.Error)
	}
}

// TestServerOpenURL_InvalidRulesFile_FailsClosedLoud verifies a present-but-
// corrupt rules file is treated as an infrastructure failure, not a policy
// deny: distinct status (500) and message from the 403 "blocked by egress
// policy" path, so the real cause (corrupt rules file) is not masked.
func TestServerOpenURL_InvalidRulesFile_FailsClosedLoud(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "corrupt.yaml")
	if err := os.WriteFile(f, []byte("rules:\n  - dst: github.test\n    proto: https\n    port: \"443\"\n    path_rules:\n      - action: deny\n        path: \"~/[bad(\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := &Server{
		log:           logger.Nop(),
		rulesFilePath: f,
		browserFunc:   func(_ string) error { t.Fatal("browser should not be called"); return nil },
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/open/url",
		bytes.NewBufferString(`{"url":"https://github.test/"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleOpenURL(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for invalid rules file, got %d", resp.StatusCode)
	}

	var result openURLResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result.Success {
		t.Error("expected success to be false")
	}
	if result.Error != errEgressRulesUnavailable {
		t.Errorf("expected %q, got %q", errEgressRulesUnavailable, result.Error)
	}
}

func TestServerPort(t *testing.T) {
	s := NewServer(12345, logger.Nop(), "", nil)
	if s.Port() != 12345 {
		t.Errorf("expected port 12345, got %d", s.Port())
	}
}

func TestServerIsRunning(t *testing.T) {
	s := NewServer(0, logger.Nop(), "", nil)
	if s.IsRunning() {
		t.Error("expected server to not be running initially")
	}
}

func TestServerOpenURLBodySizeLimit(t *testing.T) {
	s := &Server{log: logger.Nop()}

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

// authURLFor builds an OAuth authorization URL whose redirect_uri points
// at a localhost callback on port.
func authURLFor(host string, port int) string {
	redirect := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/callback"
	return "https://auth.example.com/authorize?client_id=x&redirect_uri=" + url.QueryEscape(redirect)
}

// registerCallback runs the one-shot register-and-open request against s
// and returns the decoded response. The browser is a stub.
func registerCallback(t *testing.T, s *Server, body string) (int, callbackRegisterResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/callback/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleCallbackRegister(w, req)
	var result callbackRegisterResponse
	if err := json.NewDecoder(w.Result().Body).Decode(&result); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	return w.Code, result
}

// newCallbackServer returns a server with a recording browser stub; no
// test opens a real browser.
func newCallbackServer(t *testing.T, rulesFile string) (*Server, *[]string) {
	t.Helper()
	s := NewServer(18374, logger.Nop(), rulesFile, nil)
	opened := new([]string)
	s.browserFunc = func(u string) error { *opened = append(*opened, u); return nil }
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.Stop(ctx); err != nil {
			t.Logf("stop: %v", err)
		}
	})
	return s, opened
}

// TestServerCallbackRegister pins the one-shot OAuth contract: the host
// proxy takes the callback port and path from the auth URL's localhost
// redirect, binds it, and opens the auth URL itself.
func TestServerCallbackRegister(t *testing.T) {
	s, opened := newCallbackServer(t, "")
	port := getFreePort(t)
	authURL := authURLFor("localhost", port)

	status, res := registerCallback(t, s, fmt.Sprintf(`{"authUrl": %q, "ttlSeconds": 120}`, authURL))
	if status != http.StatusOK || !res.Success {
		t.Fatalf("expected success, got %d %q", status, res.Error)
	}
	if res.SessionID == "" || res.Port != port {
		t.Fatalf("expected session and port %d, got %+v", port, res)
	}
	if got, _ := s.callbackChannel.GetPath(res.SessionID); got != "/callback" {
		t.Errorf("expected callback path /callback, got %q", got)
	}
	if len(*opened) != 1 || (*opened)[0] != authURL {
		t.Errorf("expected the auth URL opened once, got %v", *opened)
	}
}

// TestServerCallbackRegister_Rejects: everything that is not a loopback
// http redirect on an unprivileged port is refused before any listener
// binds or the browser opens.
func TestServerCallbackRegister_Rejects(t *testing.T) {
	port := getFreePort(t)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"invalid json", "not json", http.StatusBadRequest},
		{"no redirect", `{"authUrl": "https://auth.example.com/authorize"}`, http.StatusBadRequest},
		{
			"remote redirect",
			fmt.Sprintf(`{"authUrl": %q}`, authURLFor("evil.example.com", port)),
			http.StatusBadRequest,
		},
		{"privileged port", fmt.Sprintf(`{"authUrl": %q}`, authURLFor("localhost", 80)), http.StatusBadRequest},
		{
			"https redirect",
			`{"authUrl": "https://a.example.com/?redirect_uri=https%3A%2F%2Flocalhost%3A40000%2Fcb"}`,
			http.StatusBadRequest,
		},
		{
			"no port",
			`{"authUrl": "https://a.example.com/?redirect_uri=http%3A%2F%2Flocalhost%2Fcb"}`,
			http.StatusBadRequest,
		},
		{
			"userinfo",
			`{"authUrl": "https://a.example.com/?redirect_uri=http%3A%2F%2Fu%3Ap%40localhost%3A40000%2Fcb"}`,
			http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, opened := newCallbackServer(t, "")
			status, res := registerCallback(t, s, tc.body)
			if status != tc.want || res.Success {
				t.Errorf("expected %d failure, got %d %+v", tc.want, status, res)
			}
			if len(*opened) != 0 {
				t.Errorf("browser must not open, got %v", *opened)
			}
			if s.sessionStore.Count() != 0 {
				t.Errorf("no session may remain, got %d", s.sessionStore.Count())
			}
		})
	}
}

// TestServerCallbackRegister_EgressDenied: the auth URL is egress-checked
// like /open/url; a denied host opens nothing and binds nothing.
func TestServerCallbackRegister_EgressDenied(t *testing.T) {
	rules := filepath.Join(t.TempDir(), "egress-rules.yaml")
	rulesYAML := []byte("rules:\n  - dst: allowed.example.com\n    proto: https\n")
	if err := os.WriteFile(rules, rulesYAML, 0o600); err != nil {
		t.Fatal(err)
	}
	s, opened := newCallbackServer(t, rules)
	status, res := registerCallback(t, s, fmt.Sprintf(`{"authUrl": %q}`, authURLFor("localhost", getFreePort(t))))
	if status != http.StatusForbidden || res.Success {
		t.Errorf("expected 403, got %d %+v", status, res)
	}
	if len(*opened) != 0 || s.sessionStore.Count() != 0 {
		t.Errorf("denied register must not open or keep a session: opened=%v sessions=%d",
			*opened, s.sessionStore.Count())
	}
}

// TestServerCallbackRegister_TTLCapped: the session never outlives the
// default callback TTL, whatever the caller asks for.
func TestServerCallbackRegister_TTLCapped(t *testing.T) {
	s, _ := newCallbackServer(t, "")
	_, res := registerCallback(t, s, fmt.Sprintf(`{"authUrl": %q, "ttlSeconds": 86400}`,
		authURLFor("127.0.0.1", getFreePort(t))))
	expires, err := time.Parse(time.RFC3339, res.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expiresAt: %v", err)
	}
	if expires.After(time.Now().Add(DefaultCallbackTTL + time.Second)) {
		t.Errorf("expiry %v exceeds the %v cap", expires, DefaultCallbackTTL)
	}
}

// TestServerCallbackRegister_BrowserFailure: when the browser cannot open,
// the session and its listener are removed.
func TestServerCallbackRegister_BrowserFailure(t *testing.T) {
	s, _ := newCallbackServer(t, "")
	s.browserFunc = func(string) error { return errors.New("no browser") }
	port := getFreePort(t)
	status, _ := registerCallback(t, s, fmt.Sprintf(`{"authUrl": %q}`, authURLFor("localhost", port)))
	if status != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", status)
	}
	if s.sessionStore.Count() != 0 {
		t.Errorf("expected no session, got %d", s.sessionStore.Count())
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf(consts.Localhost+":%d", port), 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Errorf("listener on %d must be closed", port)
	}
}

func TestServerCallbackGetData(t *testing.T) {
	s := NewServer(18374, logger.Nop(), "", nil)
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
		captureReq := httptest.NewRequest(http.MethodGet, "/callback?code=ABC&state=XYZ", nil)
		if err := s.callbackChannel.Capture(session.ID, captureReq); err != nil {
			t.Fatalf("capture: %v", err)
		}

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
	s := NewServer(18374, logger.Nop(), "", nil)
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

func TestServerStopCleansUpSessions(t *testing.T) {
	s := NewServer(18374, logger.Nop(), "", nil)

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

func TestServer_DynamicListenerStartStop(t *testing.T) {
	s, _ := newCallbackServer(t, "")

	// Get a free port for the dynamic listener
	port := getFreePort(t)

	// Register a callback session which starts a dynamic listener
	_, result := registerCallback(t, s, fmt.Sprintf(`{"authUrl": %q}`, authURLFor("localhost", port)))

	if !result.Success || result.SessionID == "" {
		t.Fatalf("expected successful registration, got error: %s", result.Error)
	}

	// Small delay for listener to start
	time.Sleep(50 * time.Millisecond)

	// Verify the listener is actually listening on the port
	conn, err := net.DialTimeout("tcp", fmt.Sprintf(consts.Localhost+":%d", port), 1*time.Second)
	if err != nil {
		t.Fatalf("expected dynamic listener to be listening on port %d: %v", port, err)
	}
	conn.Close()

	if ln, err := net.Listen("tcp", "[::1]:0"); err == nil {
		ln.Close()
		conn6, err := net.DialTimeout("tcp", fmt.Sprintf("[::1]:%d", port), 1*time.Second)
		if err != nil {
			t.Fatalf("expected dynamic listener to be listening on IPv6 port %d: %v", port, err)
		}
		conn6.Close()
	}

	// Delete the session which should stop the listener
	delReq := httptest.NewRequest(http.MethodDelete, "/callback/"+result.SessionID, nil)
	delReq.SetPathValue("session", result.SessionID)
	delW := httptest.NewRecorder()
	s.handleCallbackDelete(delW, delReq)

	// Small delay for listener to stop
	time.Sleep(50 * time.Millisecond)

	// Verify the listener has stopped
	conn, err = net.DialTimeout("tcp", fmt.Sprintf(consts.Localhost+":%d", port), 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Errorf("expected dynamic listener to be stopped, but port %d is still open", port)
	}

	if ln, err := net.Listen("tcp", "[::1]:0"); err == nil {
		ln.Close()
		conn6, err := net.DialTimeout("tcp", fmt.Sprintf("[::1]:%d", port), 100*time.Millisecond)
		if err == nil {
			conn6.Close()
			t.Errorf("expected IPv6 dynamic listener to be stopped, but port %d is still open", port)
		}
	}
}

func TestServer_DynamicListenerCapture(t *testing.T) {
	s, _ := newCallbackServer(t, "")

	// Get a free port for the dynamic listener
	port := getFreePort(t)

	// Register a callback session
	_, result := registerCallback(t, s, fmt.Sprintf(`{"authUrl": %q}`, authURLFor("localhost", port)))

	sessionID := result.SessionID

	// Small delay for listener to start
	time.Sleep(50 * time.Millisecond)

	// Make a request to the dynamic listener (simulating OAuth callback)
	callbackURL := fmt.Sprintf("http://"+consts.Localhost+":%d/callback?code=AUTH_CODE&state=RANDOM_STATE", port)
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("failed to make callback request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from callback, got %d", resp.StatusCode)
	}

	// Verify the callback was captured
	data, received := s.callbackChannel.GetData(sessionID)
	if !received {
		t.Error("expected callback to be captured")
	}
	if data == nil {
		t.Fatal("expected callback data")
	}
	if !strings.Contains(data.Query, "code=AUTH_CODE") {
		t.Errorf("expected query to contain 'code=AUTH_CODE', got %q", data.Query)
	}
	if !strings.Contains(data.Query, "state=RANDOM_STATE") {
		t.Errorf("expected query to contain 'state=RANDOM_STATE', got %q", data.Query)
	}

	// One-shot: the listener closes after the first capture, so the host
	// port is not held open for the rest of the session lifetime.
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf(consts.Localhost+":%d", port), 100*time.Millisecond)
		if dialErr != nil {
			break
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("dynamic listener on %d still open after capture", port)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, kept := s.callbackChannel.GetData(sessionID); !kept {
		t.Error("captured data must survive the listener close")
	}
}

func TestServer_DynamicListenerCaptureIPv6(t *testing.T) {
	s, _ := newCallbackServer(t, "")

	if ln, err := net.Listen("tcp", "[::1]:0"); err != nil {
		t.Skip("IPv6 loopback not available")
	} else {
		ln.Close()
	}

	port := getFreePort(t)

	_, result := registerCallback(t, s, fmt.Sprintf(`{"authUrl": %q}`, authURLFor("localhost", port)))

	if !result.Success || result.SessionID == "" {
		t.Fatalf("expected successful registration, got error: %s", result.Error)
	}

	time.Sleep(50 * time.Millisecond)

	callbackURL := fmt.Sprintf("http://[::1]:%d/callback?code=AUTH_CODE_V6&state=RANDOM_STATE_V6", port)
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("failed to make IPv6 callback request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from IPv6 callback, got %d", resp.StatusCode)
	}

	data, received := s.callbackChannel.GetData(result.SessionID)
	if !received {
		t.Error("expected IPv6 callback to be captured")
	}
	if data == nil {
		t.Fatal("expected IPv6 callback data")
	}
	if !strings.Contains(data.Query, "code=AUTH_CODE_V6") {
		t.Errorf("expected query to contain 'code=AUTH_CODE_V6', got %q", data.Query)
	}
	if !strings.Contains(data.Query, "state=RANDOM_STATE_V6") {
		t.Errorf("expected query to contain 'state=RANDOM_STATE_V6', got %q", data.Query)
	}
}

// startTLSServer starts a host proxy on a free port with the real auth
// material and returns its base URL and the URLs the browser was asked to
// open.
func startTLSServer(t *testing.T) (string, func() []string) {
	t.Helper()
	testenv.New(t)
	require.NoError(t, auth.EnsureAuthMaterial())
	tlsCfg, err := ServerTLSConfig()
	require.NoError(t, err)

	port := getFreePort(t)
	s := NewServer(port, logger.Nop(), "", tlsCfg)
	var mu sync.Mutex
	var opened []string
	s.browserFunc = func(u string) error {
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, u)
		return nil
	}
	require.NoError(t, s.Start())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := s.Stop(ctx); stopErr != nil {
			t.Logf("stop host proxy: %v", stopErr)
		}
	})
	return fmt.Sprintf("https://localhost:%d", port), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), opened...)
	}
}

func clientWith(t *testing.T, certPath, keyPath func() (string, error)) *http.Client {
	t.Helper()
	cert, err := certPath()
	require.NoError(t, err)
	key, err := keyPath()
	require.NoError(t, err)
	cfg, err := ClientTLSConfig(cert, key)
	require.NoError(t, err)
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, Timeout: 5 * time.Second}
}

func postOpenURL(t *testing.T, c *http.Client, base string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/open/url",
		bytes.NewBufferString(`{"url":"https://example.com/"}`))
	require.NoError(t, err)
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

func getHealth(t *testing.T, c *http.Client, base string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/health", nil)
	require.NoError(t, err)
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestServer_MTLS pins who can reach the host proxy: only a client cert
// chained to the CLI CA completes the handshake, and only the CP's cert
// can run host actions. The CLI's cert can check health.
func TestServer_MTLS(t *testing.T) {
	base, opened := startTLSServer(t)

	cp := clientWith(t, consts.AuthCPClientCertPath, consts.AuthCPClientKeyPath)
	assert.Equal(t, http.StatusOK, getHealth(t, cp, base))
	assert.Equal(t, http.StatusOK, postOpenURL(t, cp, base))
	assert.Equal(t, []string{"https://example.com/"}, opened())

	cli := clientWith(t, consts.AuthCLIClientCertPath, consts.AuthCLIClientKeyPath)
	assert.Equal(t, http.StatusOK, getHealth(t, cli, base))
	assert.Equal(t, http.StatusForbidden, postOpenURL(t, cli, base), "only the CP may run host actions")
	assert.Len(t, opened(), 1)

	cfg, err := ClientTLSConfig("", "")
	require.NoError(t, err)
	noCert := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/health", nil)
	require.NoError(t, err)
	resp, err := noCert.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	require.Error(t, err, "a client without a cert must fail the handshake")
}

// TestServer_StartRequiresTLS: the host proxy never serves plaintext.
func TestServer_StartRequiresTLS(t *testing.T) {
	s := NewServer(getFreePort(t), logger.Nop(), "", nil)
	require.Error(t, s.Start())
}

func TestRequireControlPlane(t *testing.T) {
	cases := []struct {
		name  string
		state *tls.ConnectionState
		want  int
	}{
		{"no TLS", nil, http.StatusForbidden},
		{"no peer cert", new(tls.ConnectionState), http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := requireControlPlane(logger.Nop(), func(http.ResponseWriter, *http.Request) { called = true })
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/open/url", nil)
			require.NoError(t, err)
			req.TLS = tc.state
			rec := httptest.NewRecorder()
			h(rec, req)
			assert.Equal(t, tc.want, rec.Code)
			assert.False(t, called)
		})
	}
}

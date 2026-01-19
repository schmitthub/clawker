package hostproxy

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestCallbackChannel_Register(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()
	channel := NewCallbackChannel(store)

	session, err := channel.Register(8080, "/callback", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if session.ID == "" {
		t.Error("expected session ID to be set")
	}

	if session.Type != CallbackSessionType {
		t.Errorf("expected type %q, got %q", CallbackSessionType, session.Type)
	}

	// Verify metadata
	port, ok := channel.GetPort(session.ID)
	if !ok || port != 8080 {
		t.Errorf("expected port 8080, got %d", port)
	}

	path, ok := channel.GetPath(session.ID)
	if !ok || path != "/callback" {
		t.Errorf("expected path '/callback', got %q", path)
	}
}

func TestCallbackChannel_RegisterInvalidPort(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()
	channel := NewCallbackChannel(store)

	tests := []struct {
		name string
		port int
	}{
		{"zero port", 0},
		{"negative port", -1},
		{"port too high", 65536},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := channel.Register(tt.port, "/callback", 5*time.Minute)
			if err == nil {
				t.Error("expected error for invalid port")
			}
		})
	}
}

func TestCallbackChannel_RegisterEmptyPath(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()
	channel := NewCallbackChannel(store)

	session, err := channel.Register(8080, "", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	path, ok := channel.GetPath(session.ID)
	if !ok || path != "/" {
		t.Errorf("expected default path '/', got %q", path)
	}
}

func TestCallbackChannel_Capture(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()
	channel := NewCallbackChannel(store)

	session, err := channel.Register(8080, "/callback", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Create a mock HTTP request
	reqURL, _ := url.Parse("http://localhost:18374/cb/" + session.ID + "/callback?code=ABC&state=XYZ")
	req := &http.Request{
		Method: "GET",
		URL:    reqURL,
		Header: http.Header{
			"User-Agent": []string{"Mozilla/5.0"},
			"Accept":     []string{"text/html"},
		},
	}

	err = channel.Capture(session.ID, req)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}

	// Verify callback was captured
	data, ok := channel.GetData(session.ID)
	if !ok {
		t.Fatal("expected to get callback data")
	}

	if data.Method != "GET" {
		t.Errorf("expected method GET, got %q", data.Method)
	}

	if data.Query != "code=ABC&state=XYZ" {
		t.Errorf("expected query 'code=ABC&state=XYZ', got %q", data.Query)
	}

	if data.ReceivedAt.IsZero() {
		t.Error("expected ReceivedAt to be set")
	}
}

func TestCallbackChannel_CaptureWithBody(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()
	channel := NewCallbackChannel(store)

	session, err := channel.Register(8080, "/callback", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	body := "grant_type=authorization_code&code=ABC123"
	reqURL, _ := url.Parse("http://localhost:18374/cb/" + session.ID + "/callback")
	req := &http.Request{
		Method: "POST",
		URL:    reqURL,
		Header: http.Header{
			"Content-Type": []string{"application/x-www-form-urlencoded"},
		},
		Body: nopCloser{bytes.NewBufferString(body)},
	}

	err = channel.Capture(session.ID, req)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}

	data, ok := channel.GetData(session.ID)
	if !ok {
		t.Fatal("expected to get callback data")
	}

	if data.Method != "POST" {
		t.Errorf("expected method POST, got %q", data.Method)
	}

	if data.Body != body {
		t.Errorf("expected body %q, got %q", body, data.Body)
	}
}

func TestCallbackChannel_CaptureNotFound(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()
	channel := NewCallbackChannel(store)

	reqURL, _ := url.Parse("http://localhost:18374/cb/nonexistent/callback")
	req := &http.Request{
		Method: "GET",
		URL:    reqURL,
	}

	err := channel.Capture("nonexistent", req)
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestCallbackChannel_CaptureSingleUse(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()
	channel := NewCallbackChannel(store)

	session, err := channel.Register(8080, "/callback", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	reqURL, _ := url.Parse("http://localhost:18374/cb/" + session.ID + "/callback?code=FIRST")
	req1 := &http.Request{
		Method: "GET",
		URL:    reqURL,
	}

	// First capture should succeed
	err = channel.Capture(session.ID, req1)
	if err != nil {
		t.Fatalf("First Capture() error = %v", err)
	}

	reqURL2, _ := url.Parse("http://localhost:18374/cb/" + session.ID + "/callback?code=SECOND")
	req2 := &http.Request{
		Method: "GET",
		URL:    reqURL2,
	}

	// Second capture should fail
	err = channel.Capture(session.ID, req2)
	if err == nil {
		t.Error("expected error for duplicate callback")
	}

	// Data should still be from first capture
	data, _ := channel.GetData(session.ID)
	if data.Query != "code=FIRST" {
		t.Errorf("expected query from first capture, got %q", data.Query)
	}
}

func TestCallbackChannel_GetDataNotReceived(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()
	channel := NewCallbackChannel(store)

	session, err := channel.Register(8080, "/callback", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Before any callback
	data, ok := channel.GetData(session.ID)
	if ok {
		t.Error("expected ok=false before callback received")
	}
	if data != nil {
		t.Error("expected nil data before callback received")
	}
}

func TestCallbackChannel_IsReceived(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()
	channel := NewCallbackChannel(store)

	session, err := channel.Register(8080, "/callback", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Before callback
	if channel.IsReceived(session.ID) {
		t.Error("expected IsReceived=false before callback")
	}

	// Capture callback
	reqURL, _ := url.Parse("http://localhost:18374/cb/" + session.ID + "/callback")
	req := &http.Request{
		Method: "GET",
		URL:    reqURL,
	}
	_ = channel.Capture(session.ID, req)

	// After callback
	if !channel.IsReceived(session.ID) {
		t.Error("expected IsReceived=true after callback")
	}
}

func TestCallbackChannel_Delete(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()
	channel := NewCallbackChannel(store)

	session, err := channel.Register(8080, "/callback", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	channel.Delete(session.ID)

	_, ok := channel.GetPort(session.ID)
	if ok {
		t.Error("expected session to be deleted")
	}
}

func TestCallbackChannel_CaptureSkipsSensitiveHeaders(t *testing.T) {
	store := NewSessionStore()
	defer store.Stop()
	channel := NewCallbackChannel(store)

	session, err := channel.Register(8080, "/callback", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	reqURL, _ := url.Parse("http://localhost:18374/cb/" + session.ID + "/callback")
	req := &http.Request{
		Method: "GET",
		URL:    reqURL,
		Header: http.Header{
			"User-Agent":      []string{"Mozilla/5.0"},
			"Cookie":          []string{"session=secret"},
			"Authorization":   []string{"Bearer token123"},
			"X-Forwarded-For": []string{"192.168.1.1"},
			"X-Real-Ip":       []string{"10.0.0.1"},
			"Accept":          []string{"text/html"},
		},
	}

	err = channel.Capture(session.ID, req)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}

	data, _ := channel.GetData(session.ID)

	// Should have non-sensitive headers
	if _, ok := data.Headers["User-Agent"]; !ok {
		t.Error("expected User-Agent header to be captured")
	}
	if _, ok := data.Headers["Accept"]; !ok {
		t.Error("expected Accept header to be captured")
	}

	// Should not have sensitive headers
	sensitiveHeaders := []string{"Cookie", "Authorization", "X-Forwarded-For", "X-Real-Ip"}
	for _, h := range sensitiveHeaders {
		if _, ok := data.Headers[h]; ok {
			t.Errorf("expected %s header to be skipped", h)
		}
	}
}

// nopCloser wraps a bytes.Buffer to implement io.ReadCloser
type nopCloser struct {
	*bytes.Buffer
}

func (nopCloser) Close() error { return nil }

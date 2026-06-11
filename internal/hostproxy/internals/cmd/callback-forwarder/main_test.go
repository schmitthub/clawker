package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBuildLocalCallbackURL(t *testing.T) {
	data := &CallbackData{Path: "/callback", Query: "code=abc&state=xyz"}

	if got := buildLocalCallbackURL("localhost", 43123, data); got != "http://localhost:43123/callback?code=abc&state=xyz" {
		t.Fatalf("unexpected localhost URL: %s", got)
	}

	if got := buildLocalCallbackURL("127.0.0.1", 43123, data); got != "http://127.0.0.1:43123/callback?code=abc&state=xyz" {
		t.Fatalf("unexpected IPv4 URL: %s", got)
	}

	if got := buildLocalCallbackURL("::1", 43123, data); got != "http://[::1]:43123/callback?code=abc&state=xyz" {
		t.Fatalf("unexpected IPv6 URL: %s", got)
	}
}

func TestForwardCallbackFallsBackToIPv4(t *testing.T) {
	port := freeTCPPort(t, "127.0.0.1:0")

	received := make(chan *http.Request, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		received <- r
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen tcp4: %v", err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: mux}
	defer srv.Close()
	go srv.Serve(ln)

	client := &http.Client{Timeout: 2 * time.Second}
	data := &CallbackData{Method: http.MethodGet, Path: "/callback", Query: "code=v4", Headers: map[string]string{"X-Test": "yes"}}

	if err := forwardCallback(client, port, data); err != nil {
		t.Fatalf("forwardCallback failed: %v", err)
	}

	select {
	case req := <-received:
		if req.URL.RawQuery != "code=v4" {
			t.Fatalf("unexpected query: %s", req.URL.RawQuery)
		}
		if req.Header.Get("X-Test") != "yes" {
			t.Fatalf("missing forwarded header")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive forwarded callback")
	}
}

func TestForwardCallbackFallsBackToIPv6(t *testing.T) {
	lnProbe, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 loopback not available")
	}
	port := lnProbe.Addr().(*net.TCPAddr).Port
	lnProbe.Close()

	received := make(chan *http.Request, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		received <- r
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp6", fmt.Sprintf("[::1]:%d", port))
	if err != nil {
		t.Fatalf("listen tcp6: %v", err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: mux}
	defer srv.Close()
	go srv.Serve(ln)

	client := &http.Client{Timeout: 2 * time.Second}
	data := &CallbackData{Method: http.MethodGet, Path: "/callback", Query: "code=v6"}

	if err := forwardCallback(client, port, data); err != nil {
		t.Fatalf("forwardCallback failed: %v", err)
	}

	select {
	case req := <-received:
		if req.URL.RawQuery != "code=v6" {
			t.Fatalf("unexpected query: %s", req.URL.RawQuery)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive forwarded callback over IPv6")
	}
}

func TestForwardCallbackAggregatesErrors(t *testing.T) {
	client := &http.Client{Timeout: 200 * time.Millisecond}
	data := &CallbackData{Method: http.MethodGet, Path: "/callback"}

	err := forwardCallback(client, freeTCPPort(t, "127.0.0.1:0"), data)
	if err == nil {
		t.Fatal("expected forwardCallback to fail")
	}
	msg := err.Error()
	for _, want := range []string{"localhost:", "127.0.0.1:", "::1:"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to mention %q, got %q", want, msg)
		}
	}
}

func freeTCPPort(t *testing.T, addr string) int {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("free port listen failed: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

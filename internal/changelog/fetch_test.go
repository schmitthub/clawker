package changelog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetch_Success(t *testing.T) {
	const body = "# Changelog\n\n## [1.0.0] - 2026-01-01\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	raw, err := Fetch(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(raw) != body {
		t.Errorf("Fetch returned %q, want %q", raw, body)
	}
}

func TestFetch_Non200IsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected an error on a 404 response")
	}
}

func TestFetch_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the request runs
	if _, err := Fetch(ctx, srv.Client(), srv.URL); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

func TestFetch_NilClientUsesDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// A nil client must not panic — Fetch supplies its own short-timeout client.
	raw, err := Fetch(context.Background(), nil, srv.URL)
	if err != nil {
		t.Fatalf("Fetch with nil client: %v", err)
	}
	if string(raw) != "ok" {
		t.Errorf("Fetch returned %q, want %q", raw, "ok")
	}
}

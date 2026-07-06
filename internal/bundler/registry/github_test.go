package registry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/internal/bundler/registry"
)

// ghStubRoundTripper services a single GitHub latest-release lookup with
// either a canned response or an injected error.
type ghStubRoundTripper struct {
	body   []byte
	status int
	err    error

	gotURL    string
	gotAccept string
}

func (s *ghStubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s.gotURL = req.URL.String()
	s.gotAccept = req.Header.Get("Accept")
	if s.err != nil {
		return nil, s.err
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(s.body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func ghRelease(t *testing.T, tag string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"tag_name": tag})
	if err != nil {
		t.Fatalf("marshal release: %v", err)
	}
	return body
}

func TestGitHubReleaseClient_LatestVersion_StripsTagPrefix(t *testing.T) {
	var rt ghStubRoundTripper
	rt.body = ghRelease(t, "rust-v0.50.0")
	client := registry.NewGitHubReleaseClient(registry.WithGitHubHTTPClient(&http.Client{Transport: &rt}))

	got, err := client.LatestVersion(context.Background(), "openai/codex", "rust-v")
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if got != "0.50.0" {
		t.Fatalf("version = %q, want %q", got, "0.50.0")
	}
	if !strings.Contains(rt.gotURL, "/repos/openai/codex/releases/latest") {
		t.Fatalf("request URL = %q, want repos/openai/codex/releases/latest", rt.gotURL)
	}
	if rt.gotAccept != "application/vnd.github+json" {
		t.Fatalf("Accept = %q, want application/vnd.github+json", rt.gotAccept)
	}
}

func TestGitHubReleaseClient_LatestVersion_NoPrefix(t *testing.T) {
	var rt ghStubRoundTripper
	rt.body = ghRelease(t, "1.2.3")
	client := registry.NewGitHubReleaseClient(registry.WithGitHubHTTPClient(&http.Client{Transport: &rt}))

	got, err := client.LatestVersion(context.Background(), "acme/tool", "")
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if got != "1.2.3" {
		t.Fatalf("version = %q, want %q", got, "1.2.3")
	}
}

func TestGitHubReleaseClient_LatestVersion_PrefixMismatch(t *testing.T) {
	var rt ghStubRoundTripper
	rt.body = ghRelease(t, "v0.50.0")
	client := registry.NewGitHubReleaseClient(registry.WithGitHubHTTPClient(&http.Client{Transport: &rt}))

	_, err := client.LatestVersion(context.Background(), "openai/codex", "rust-v")
	if err == nil {
		t.Fatal("want error for tag missing the configured prefix, got nil")
	}
}

func TestGitHubReleaseClient_LatestVersion_HTTPError(t *testing.T) {
	var rt ghStubRoundTripper
	rt.status = http.StatusNotFound
	rt.body = []byte(`{"message":"Not Found"}`)
	client := registry.NewGitHubReleaseClient(registry.WithGitHubHTTPClient(&http.Client{Transport: &rt}))

	_, err := client.LatestVersion(context.Background(), "acme/missing", "")
	var regErr *registry.RegistryError
	if !errors.As(err, &regErr) {
		t.Fatalf("err = %v, want *registry.RegistryError", err)
	}
	if regErr.StatusCode != http.StatusNotFound {
		t.Fatalf("StatusCode = %d, want 404", regErr.StatusCode)
	}
}

func TestGitHubReleaseClient_LatestVersion_NetworkError(t *testing.T) {
	var rt ghStubRoundTripper
	rt.err = errors.New("connection refused")
	client := registry.NewGitHubReleaseClient(registry.WithGitHubHTTPClient(&http.Client{Transport: &rt}))

	_, err := client.LatestVersion(context.Background(), "acme/tool", "")
	var netErr *registry.NetworkError
	if !errors.As(err, &netErr) {
		t.Fatalf("err = %v, want *registry.NetworkError", err)
	}
}

func TestGitHubReleaseClient_LatestVersion_MissingTag(t *testing.T) {
	var rt ghStubRoundTripper
	rt.body = []byte(`{"name":"release without tag_name"}`)
	client := registry.NewGitHubReleaseClient(registry.WithGitHubHTTPClient(&http.Client{Transport: &rt}))

	_, err := client.LatestVersion(context.Background(), "acme/tool", "")
	if err == nil {
		t.Fatal("want error for response missing tag_name, got nil")
	}
}

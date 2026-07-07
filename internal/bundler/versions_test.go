package bundler

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
	"github.com/schmitthub/clawker/internal/harness"
)

// stubRoundTripper services a single npm `@anthropic-ai/claude-code` lookup
// with either a canned response or an injected error. The build command's
// httpstub_test.go has its own copy scoped to that package; this one stays
// in the bundler tests so version resolution is testable
// without dragging in the command-layer test helpers.
type stubRoundTripper struct {
	body []byte
	err  error
}

func (s stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	if !strings.Contains(req.URL.Path, ClaudeCodePackage) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(s.body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

func newStubClient(rt http.RoundTripper) *http.Client {
	return &http.Client{Transport: rt}
}

// TestResolveVersions_PartialPicksHighestPatch pins the partial-match contract:
// "2.1" must resolve to the highest 2.1.x release (2.1.11) — not 2.1.0, and not
// the lexically-largest string ("2.1.9"). resolvePattern routes a partial
// through semver.NewConstraint (tilde-equivalent >=2.1.0 <2.2.0) and keeps the
// semver-max. A regression to NewVersion(pattern) (which would coerce "2.1" to
// 2.1.0) or to string sorting is caught here, as is leaking 2.2.0 out of range.
func TestResolveVersions_PartialPicksHighestPatch(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"name":      ClaudeCodePackage,
		"dist-tags": map[string]string{"latest": "2.2.0"},
		"versions": map[string]any{
			"2.0.5":  map[string]any{"version": "2.0.5"}, // below range
			"2.1.0":  map[string]any{"version": "2.1.0"}, // range floor — must NOT win
			"2.1.2":  map[string]any{"version": "2.1.2"},
			"2.1.9":  map[string]any{"version": "2.1.9"},  // lexical max of the 2.1.x set
			"2.1.11": map[string]any{"version": "2.1.11"}, // semver max — the expected winner
			"2.2.0":  map[string]any{"version": "2.2.0"},  // above range — must be excluded
		},
	})
	client := newStubClient(stubRoundTripper{body: body})
	mgr := NewVersionsManagerWithFetcher(registry.NewNPMClient(registry.WithHTTPClient(client)), DefaultVariantConfig())

	vf, err := mgr.ResolveVersions(context.Background(), []string{"2.1"}, ResolveOptions{Output: io.Discard})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := (*vf)["2.1.11"]; !ok {
		t.Fatalf("want resolved key 2.1.11, got %v", vf.Keys())
	}
	if len(*vf) != 1 {
		t.Fatalf("want exactly one resolved version (2.1.11), got %v", vf.Keys())
	}
}

// ResolveHarnessVersion github-release path: the manifest's tag prefix is
// stripped from the latest release tag; a resolution failure degrades to the
// DefaultHarnessVersion literal plus the error (build proceeds floating).
func TestResolveHarnessVersion_GitHubRelease(t *testing.T) {
	var stub ghAnyStub
	stub.body = []byte(`{"tag_name": "rust-v0.50.0"}`)
	client := newStubClient(stub)
	var b harness.Bundle
	b.Name = "codex"
	b.Manifest.Version = harness.VersionSpec{
		Resolver:  harness.ResolverGitHubRelease,
		Package:   "openai/codex",
		TagPrefix: "rust-v",
	}

	got, err := ResolveHarnessVersion(context.Background(), client, &b)
	if err != nil {
		t.Fatalf("ResolveHarnessVersion: %v", err)
	}
	if got != "0.50.0" {
		t.Fatalf("version = %q, want %q", got, "0.50.0")
	}
}

func TestResolveHarnessVersion_GitHubRelease_FailureDegradesToLatest(t *testing.T) {
	var stub ghAnyStub
	stub.err = errors.New("connection refused")
	client := newStubClient(stub)
	var b harness.Bundle
	b.Name = "codex"
	b.Manifest.Version = harness.VersionSpec{
		Resolver:  harness.ResolverGitHubRelease,
		Package:   "openai/codex",
		TagPrefix: "",
	}

	got, err := ResolveHarnessVersion(context.Background(), client, &b)
	if err == nil {
		t.Fatal("want resolution error, got nil")
	}
	if got != DefaultHarnessVersion {
		t.Fatalf("version = %q, want DefaultHarnessVersion %q", got, DefaultHarnessVersion)
	}
}

func TestResolveHarnessVersion_GitHubRelease_MissingPackage(t *testing.T) {
	var b harness.Bundle
	b.Name = "codex"
	b.Manifest.Version = harness.VersionSpec{
		Resolver:  harness.ResolverGitHubRelease,
		Package:   "",
		TagPrefix: "",
	}

	got, err := ResolveHarnessVersion(context.Background(), http.DefaultClient, &b)
	if err == nil {
		t.Fatal("want error for missing package, got nil")
	}
	if got != DefaultHarnessVersion {
		t.Fatalf("version = %q, want DefaultHarnessVersion %q", got, DefaultHarnessVersion)
	}
}

// ghAnyStub answers any request with the canned body/error (no URL routing —
// ResolveHarnessVersion owns the URL; registry/github_test.go pins it).
type ghAnyStub struct {
	body []byte
	err  error
}

func (s ghAnyStub) RoundTrip(*http.Request) (*http.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(s.body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

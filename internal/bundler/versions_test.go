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
)

// stubRoundTripper services a single npm `@anthropic-ai/claude-code` lookup
// with either a canned response or an injected error. The build command's
// httpstub_test.go has its own copy scoped to that package; this one stays
// in the bundler tests so ResolveLatestClaudeCodeVersion is testable
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

func TestResolveLatestClaudeCodeVersion_Success(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"name": ClaudeCodePackage,
		"dist-tags": map[string]string{
			"latest": "2.99.99",
		},
		"versions": map[string]any{
			"2.99.99": map[string]any{"version": "2.99.99"},
		},
	})
	client := newStubClient(stubRoundTripper{body: body})

	got, err := ResolveLatestClaudeCodeVersion(context.Background(), client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2.99.99" {
		t.Fatalf("got %q, want %q", got, "2.99.99")
	}
}

func TestResolveLatestClaudeCodeVersion_NetworkError(t *testing.T) {
	netErr := errors.New("dial tcp: connection refused")
	client := newStubClient(stubRoundTripper{err: netErr})

	got, err := ResolveLatestClaudeCodeVersion(context.Background(), client)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got != DefaultClaudeCodeVersion {
		t.Fatalf("on failure want fallback %q, got %q", DefaultClaudeCodeVersion, got)
	}
	// Underlying *NetworkError must remain unwrappable so callers can
	// distinguish offline from registry errors.
	var nerr *NetworkError
	if !errors.As(err, &nerr) {
		t.Fatalf("expected NetworkError, got %T: %v", err, err)
	}
}

func TestResolveLatestClaudeCodeVersion_MissingLatestDistTag(t *testing.T) {
	// Registry returns a well-formed payload that omits the "latest"
	// dist-tag — resolvePattern fails per-pattern, ResolveVersions returns
	// ErrNoVersions, and the wrapper hands back the default literal.
	body, _ := json.Marshal(map[string]any{
		"name":      ClaudeCodePackage,
		"dist-tags": map[string]string{},
		"versions":  map[string]any{},
	})
	client := newStubClient(stubRoundTripper{body: body})

	got, err := ResolveLatestClaudeCodeVersion(context.Background(), client)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got != DefaultClaudeCodeVersion {
		t.Fatalf("on empty resolution want fallback %q, got %q", DefaultClaudeCodeVersion, got)
	}
	if !errors.Is(err, registry.ErrNoVersions) {
		t.Fatalf("expected ErrNoVersions, got %v", err)
	}
}

func TestResolveLatestClaudeCodeVersion_NilClient(t *testing.T) {
	// nil http.Client must not panic — it falls back to http.DefaultClient.
	// We don't actually fire a request: a context that's already canceled
	// short-circuits before any DNS lookup.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := ResolveLatestClaudeCodeVersion(ctx, nil)
	if err == nil {
		t.Fatal("expected error (canceled context), got nil")
	}
	if got != DefaultClaudeCodeVersion {
		t.Fatalf("on failure want fallback %q, got %q", DefaultClaudeCodeVersion, got)
	}
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

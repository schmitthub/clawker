package build

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// stubRoundTripper is a minimal http.RoundTripper that returns a single
// canned npm registry response for the @anthropic-ai/claude-code package.
// Tests inject it into Factory.HttpClient so the build command's Claude
// Code version resolution (bundler.ResolveLatestClaudeCodeVersion) stays
// hermetic — no live network calls during unit tests. Mirrors the gh-CLI
// httpmock pattern (Registry implements RoundTripper), but scoped tight:
// one stub for the one HTTP call the build command makes.
type stubRoundTripper struct {
	// version is the concrete version this stub will report for the
	// "latest" dist-tag. Surfaces in the rendered Dockerfile's ARG
	// CLAUDE_CODE_VERSION default; tests asserting on a specific value
	// match against this string.
	version string
}

func (s stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.Contains(req.URL.Path, "@anthropic-ai/claude-code") {
		// Unexpected request — return 404 to fail loudly rather than
		// silently returning canned data for the wrong endpoint.
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     make(http.Header),
		}, nil
	}
	body, _ := json.Marshal(map[string]any{
		"name": "@anthropic-ai/claude-code",
		"dist-tags": map[string]string{
			"latest": s.version,
		},
		"versions": map[string]any{
			s.version: map[string]any{"version": s.version},
		},
	})
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

// stubHTTPClient returns an *http.Client whose Transport is the
// stubRoundTripper. Pass via Factory.HttpClient to keep tests off the
// live npm registry.
func stubHTTPClient(version string) *http.Client {
	return &http.Client{Transport: stubRoundTripper{version: version}}
}

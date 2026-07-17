package build

import (
	"net/http"

	"github.com/schmitthub/clawker/internal/httpmock"
)

// stubHTTPClient returns an *http.Client backed by an internal/httpmock registry
// that serves a single canned npm response for @anthropic-ai/claude-code, keeping
// the build command's harness version resolution
// (bundler.ResolveHarnessVersion) hermetic — no live npm registry call
// during unit tests. Passed via Factory.HttpClient. The (error) return matches
// the Factory's HttpClient noun signature; this stub never fails.
func stubHTTPClient(version string) (*http.Client, error) {
	reg := &httpmock.Registry{}
	reg.Register(
		httpmock.REST(http.MethodGet, "@anthropic-ai/claude-code"),
		httpmock.JSONResponse(map[string]any{
			"name":      "@anthropic-ai/claude-code",
			"dist-tags": map[string]string{"latest": version},
			"versions":  map[string]any{version: map[string]any{"version": version}},
		}),
	)
	return reg.Client(), nil
}

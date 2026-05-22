package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultNPMRegistry = "https://registry.npmjs.org"
	defaultTimeout     = 30 * time.Second
)

// NPMClient fetches version information from the npm registry.
// Implements the Fetcher interface.
type NPMClient struct {
	httpClient *http.Client
	baseURL    string
	timeout    time.Duration
}

// Option configures an NPMClient.
type Option func(*NPMClient)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(n *NPMClient) {
		n.httpClient = c
	}
}

// WithBaseURL sets a custom registry URL.
func WithBaseURL(url string) Option {
	return func(n *NPMClient) {
		n.baseURL = url
	}
}

// WithTimeout sets the request timeout.
func WithTimeout(d time.Duration) Option {
	return func(n *NPMClient) {
		n.timeout = d
	}
}

// NewNPMClient creates a new npm registry client.
func NewNPMClient(opts ...Option) *NPMClient {
	c := &NPMClient{
		httpClient: &http.Client{},
		baseURL:    defaultNPMRegistry,
		timeout:    defaultTimeout,
	}

	for _, opt := range opts {
		opt(c)
	}

	// Apply timeout to client if not custom
	if c.httpClient.Timeout == 0 {
		c.httpClient.Timeout = c.timeout
	}

	return c
}

// FetchVersions retrieves all published versions of a package.
func (c *NPMClient) FetchVersions(ctx context.Context, pkg string) ([]string, error) {
	info, err := c.fetchPackageInfo(ctx, pkg)
	if err != nil {
		return nil, err
	}

	versions := make([]string, 0, len(info.Versions))
	for v := range info.Versions {
		versions = append(versions, v)
	}

	return versions, nil
}

// FetchDistTags retrieves dist-tags for a package.
func (c *NPMClient) FetchDistTags(ctx context.Context, pkg string) (DistTags, error) {
	info, err := c.fetchPackageInfo(ctx, pkg)
	if err != nil {
		return nil, err
	}

	return info.DistTags, nil
}

// fetchPackageInfo fetches the full package metadata from npm.
func (c *NPMClient) fetchPackageInfo(ctx context.Context, pkg string) (*NPMPackageInfo, error) {
	url := fmt.Sprintf("%s/%s", c.baseURL, pkg)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &NetworkError{
			URL:     url,
			Message: "failed to create request",
			Err:     err,
		}
	}

	// Abbreviated metadata: drops per-version README/scripts/peerDeps blobs.
	// Full metadata for @anthropic-ai/claude-code (148+ versions) blows past
	// the 1 MiB read cap below, causing silent truncation → JSON parse error.
	// Abbreviated keeps dist-tags + version keys (all this client needs).
	req.Header.Set("Accept", "application/vnd.npm.install-v1+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &NetworkError{
			URL:     url,
			Message: "request failed",
			Err:     err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, &RegistryError{
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    string(body),
		}
	}

	// Buffer the body so a decode failure can surface the first bytes as a
	// diagnostic snippet. Capped at 16 MiB so a misbehaving mirror can't
	// blow memory while still leaving room for legitimate npm metadata —
	// abbreviated format keeps popular packages well under 1 MiB, but
	// long-lived packages with hundreds of versions can still grow large.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, &NetworkError{
			URL:     url,
			Message: "failed to read response",
			Err:     err,
		}
	}

	var info NPMPackageInfo
	if err := json.Unmarshal(body, &info); err != nil {
		snippet := string(body)
		if len(snippet) > 256 {
			snippet = snippet[:256]
		}
		return nil, &ParseError{
			URL:     url,
			Snippet: snippet,
			Err:     err,
		}
	}

	return &info, nil
}

// Ensure NPMClient implements Fetcher.
var _ Fetcher = (*NPMClient)(nil)

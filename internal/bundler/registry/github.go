package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultGitHubAPI = "https://api.github.com"

	// githubErrorBodyCap bounds how much of a non-200 body is surfaced in
	// the RegistryError message.
	githubErrorBodyCap = 1024
	// githubReleaseBodyCap bounds the release-metadata read; the payload is
	// small, the cap guards a misbehaving mirror.
	githubReleaseBodyCap = 1 << 20
	// githubParseSnippetCap bounds the body snippet carried by a ParseError.
	githubParseSnippetCap = 256
)

// GitHubReleaseClient resolves the latest release version of a GitHub
// repository via the releases API. It serves harness bundles whose upstream
// versions live in GitHub release tags rather than an npm registry (e.g. a
// standalone-binary CLI whose installer downloads release assets).
type GitHubReleaseClient struct {
	httpClient *http.Client
	baseURL    string
	timeout    time.Duration
}

// GitHubOption configures a GitHubReleaseClient.
type GitHubOption func(*GitHubReleaseClient)

// WithGitHubHTTPClient sets a custom HTTP client.
func WithGitHubHTTPClient(c *http.Client) GitHubOption {
	return func(g *GitHubReleaseClient) {
		g.httpClient = c
	}
}

// WithGitHubBaseURL sets a custom API base URL.
func WithGitHubBaseURL(url string) GitHubOption {
	return func(g *GitHubReleaseClient) {
		g.baseURL = url
	}
}

// NewGitHubReleaseClient creates a new GitHub releases client.
func NewGitHubReleaseClient(opts ...GitHubOption) *GitHubReleaseClient {
	c := &GitHubReleaseClient{
		httpClient: &http.Client{},
		baseURL:    defaultGitHubAPI,
		timeout:    defaultTimeout,
	}

	for _, opt := range opts {
		opt(c)
	}

	if c.httpClient.Timeout == 0 {
		c.httpClient.Timeout = c.timeout
	}

	return c
}

// LatestVersion resolves the repository's latest release tag to a bare
// version string. repo is "owner/name". A non-empty tagPrefix must prefix
// the release tag and is stripped from the result (e.g. tag "rust-v0.50.0"
// with prefix "rust-v" resolves to "0.50.0"); a tag missing the prefix is
// an error rather than a guess — the caller's install path constructs asset
// URLs from the stripped version, so a mismatched tag scheme must surface
// at resolution, not as a download failure.
func (c *GitHubReleaseClient) LatestVersion(ctx context.Context, repo, tagPrefix string) (string, error) {
	tag, err := c.fetchLatestReleaseTag(ctx, repo)
	if err != nil {
		return "", err
	}
	if tagPrefix == "" {
		return tag, nil
	}
	trimmed := strings.TrimPrefix(tag, tagPrefix)
	if trimmed == tag {
		return "", fmt.Errorf(
			"github release tag %q for %q does not start with configured tag prefix %q",
			tag, repo, tagPrefix,
		)
	}
	return trimmed, nil
}

// fetchLatestReleaseTag fetches the latest-release metadata and returns its
// tag name.
func (c *GitHubReleaseClient) fetchLatestReleaseTag(ctx context.Context, repo string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", c.baseURL, repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", &NetworkError{
			URL:     url,
			Message: "failed to create request",
			Err:     err,
		}
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", &NetworkError{
			URL:     url,
			Message: "request failed",
			Err:     err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, githubErrorBodyCap))
		msg := string(body)
		if readErr != nil {
			msg = fmt.Sprintf("(error body unreadable: %v)", readErr)
		}
		return "", &RegistryError{
			Package:    repo,
			StatusCode: resp.StatusCode,
			Message:    msg,
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, githubReleaseBodyCap))
	if err != nil {
		return "", &NetworkError{
			URL:     url,
			Message: "failed to read response",
			Err:     err,
		}
	}

	return parseReleaseTag(body, url, repo)
}

// parseReleaseTag extracts tag_name from release metadata. Decoded as a
// plain map: the GitHub API's snake_case field name lives in one lookup key
// rather than a struct tag.
func parseReleaseTag(body []byte, url, repo string) (string, error) {
	var release map[string]any
	if err := json.Unmarshal(body, &release); err != nil {
		snippet := string(body)
		if len(snippet) > githubParseSnippetCap {
			snippet = snippet[:githubParseSnippetCap]
		}
		return "", &ParseError{
			URL:     url,
			Snippet: snippet,
			Err:     err,
		}
	}
	tag, ok := release["tag_name"].(string)
	if !ok || tag == "" {
		return "", fmt.Errorf("github release for %q has no tag_name", repo)
	}
	return tag, nil
}

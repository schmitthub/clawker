// fetch.go is the changelog's network concern, kept separate from the pure
// parser/transformer in changelog.go (which must never import net/http). It
// mirrors internal/update's HTTP discipline: a context-aware request with a
// short client timeout, a non-200 treated as an error, and the raw bytes
// returned for Parse to consume.
package changelog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// fetchTimeout bounds the CHANGELOG.md request independently of the caller's
// context, matching internal/update's httpTimeout.
const fetchTimeout = 5 * time.Second

// Fetch GETs the raw CHANGELOG.md bytes from url. The request is context-aware
// (cancel ctx to abort), and a non-200 response is an error. The supplied
// client may be nil, in which case a client with fetchTimeout is used. The
// returned bytes are raw CHANGELOG.md content for Parse — Fetch does no
// parsing.
func Fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching changelog: %s returned %d", url, resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading changelog response: %w", err)
	}
	return raw, nil
}

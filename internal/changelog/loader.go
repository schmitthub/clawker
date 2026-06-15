// loader.go ties fetch + cache + TTL + parse together so callers get parsed
// entries with graceful degradation. It is the I/O orchestration layer over the
// pure parser (changelog.go) and the network fetch (fetch.go): the only place in
// the package that touches the filesystem (the cache file) and the state store
// (the fetch-timestamp TTL gate).
//
// It imports internal/state but NOT internal/config — the cache path is passed
// in as a plain string so the package stays config-free (no import cycle, since
// state does not import changelog).
package changelog

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/schmitthub/clawker/internal/state"
)

// DefaultTTL is how long a fetched changelog cache is considered fresh before
// the loader re-fetches. It matches internal/update's 24h check cadence.
const DefaultTTL = 24 * time.Hour

// cacheFilePerm is the mode for the written cache file (owner read/write).
const cacheFilePerm = 0o600

// Loader fetches the curated CHANGELOG.md over the network, caches the raw
// bytes on disk, gates re-fetches on a TTL recorded in the CLI state store, and
// parses the result into entries. It degrades gracefully: a fetch failure falls
// back to the on-disk cache, and any unrecoverable error is returned for the
// caller to treat as "no changelog to show".
type Loader struct {
	client    *http.Client
	url       string
	cachePath string
	st        *state.State
	ttl       time.Duration
	// now is the clock, injected for tests; defaults to time.Now. It is an
	// unexported field set in NewLoader, not a parameter on any exported
	// signature, so production callers never see a test seam.
	now func() time.Time
}

// NewLoader constructs a Loader. client and st may be nil; a nil client makes
// Fetch use its own short-timeout client, and a nil st disables the TTL gate
// (every Load fetches). url is the raw CHANGELOG.md URL (changelog.ChangelogURL
// in production); cachePath is the on-disk cache file (under the state dir).
func NewLoader(client *http.Client, url, cachePath string, st *state.State, ttl time.Duration) *Loader {
	return &Loader{
		client:    client,
		url:       url,
		cachePath: cachePath,
		st:        st,
		ttl:       ttl,
		now:       time.Now,
	}
}

// Load returns the parsed changelog entries (newest-first). When forceRefresh is
// true, or the cache is stale (now - last fetch > ttl) or absent, it fetches
// from the network; on success it writes the cache and records the fetch
// timestamp, then parses the fetched bytes. On a fetch failure it falls back to
// the on-disk cache if present, otherwise returns the fetch error. When the
// cache is fresh, it reads and parses the cache without touching the network.
//
// The context controls the HTTP request lifetime. Callers treat any returned
// error as "no changelog to show" — the loader never panics.
func (l *Loader) Load(ctx context.Context, forceRefresh bool) ([]Entry, error) {
	if forceRefresh || l.stale() {
		raw, err := Fetch(ctx, l.client, l.url)
		if err != nil {
			// Network failed — fall back to the cache if we have one.
			if cached, cacheErr := os.ReadFile(l.cachePath); cacheErr == nil {
				return Parse(cached)
			}
			return nil, err
		}
		l.persist(raw)
		return Parse(raw)
	}

	// Cache is fresh — read and parse it. If it's somehow missing, fetch.
	cached, err := os.ReadFile(l.cachePath)
	if err != nil {
		raw, fetchErr := Fetch(ctx, l.client, l.url)
		if fetchErr != nil {
			return nil, fetchErr
		}
		l.persist(raw)
		return Parse(raw)
	}
	return Parse(cached)
}

// persist best-effort writes the fetched bytes to the cache file and records
// the fetch timestamp for the TTL gate. Both writes are intentionally
// fire-and-forget: the freshly fetched entries are already valid to return, so
// a cache-write or state-write failure only costs the next run a re-fetch — it
// must not fail the current Load. Errors are deliberately not surfaced here.
func (l *Loader) persist(raw []byte) {
	// Cache-write failure → next run re-fetches; not actionable here.
	_ = os.WriteFile(l.cachePath, raw, cacheFilePerm)
	if l.st != nil {
		// State-write failure → next run sees a stale (zero/old) timestamp and
		// re-fetches; not actionable here.
		_ = l.st.RecordChangelogFetch(l.now())
	}
}

// stale reports whether the cache is past its TTL (or there is no recorded
// fetch). A nil state store or a zero TTL means "always stale" (always fetch).
func (l *Loader) stale() bool {
	if l.st == nil {
		return true
	}
	last := l.st.ChangelogFetchedAt()
	if last.IsZero() {
		return true
	}
	return l.now().Sub(last) > l.ttl
}

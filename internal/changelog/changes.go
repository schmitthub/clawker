// REVIEW: why does this even need to be its own file instead of just being in changelog.go
// changes.go is the package's single I/O entry point: GET the curated
// CHANGELOG.md, parse it, diff it against the show-once cursor in CLI state, and
// (when asked) advance that cursor. There is no on-disk cache and no TTL — the
// curated changelog is small, best-effort, and the CLI runs on the host where
// it is always online, so each call fetches fresh. Callers treat any error as
// "no changelog to show".
package changelog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/schmitthub/clawker/internal/state"
)

// fetchTimeout bounds the CHANGELOG.md request independently of the caller's
// context, matching internal/update's httpTimeout.
const fetchTimeout = 5 * time.Second

// CheckForChanges owns the show-once changelog cursor end to end. It reads the
// cursor from CLI state and:
//
//   - First run (no cursor, or an unparseable one): seeds the cursor at current
//     and returns nil — there is NO catch-up backfill across a changelog-blind
//     upgrade; the cursor IS "last seen" from here on.
//   - Otherwise: GETs the curated CHANGELOG.md (ChangelogURL), parses it, and
//     returns the entries gained in (cursor, current] — newest first.
//
// The request is context-aware (cancel ctx to abort) and bounded by
// fetchTimeout; a non-200 is an error.
//
// When persist is true the cursor is advanced to current; the caller passes
// false on a suppressed run (non-TTY / CI / opt-out) so the teaser retries on
// the next interactive run. A nil st (state store unavailable) is a silent
// no-op. The cursor write is best-effort: a write failure is returned for the
// caller to log, but any gained entries are still returned so the teaser can
// render.
func CheckForChanges(ctx context.Context, st *state.State, current *semver.Version, persist bool) ([]Entry, error) {
	if st == nil {
		return nil, nil
	}

	cursor, err := semver.NewVersion(st.LastSeenChangelog())
	if err != nil {
		// First run of a changelog-aware binary: the cursor is empty (or an
		// unparseable leftover). Seed it at current and show nothing — the cursor
		// becomes the lower bound for the next run.
		if persist {
			if err := st.SetLastSeenChangelog(current.Original()); err != nil {
				return nil, fmt.Errorf("seeding changelog cursor: %w", err)
			}
		}
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ChangelogURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := (&http.Client{Timeout: fetchTimeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching changelog: %s returned %d", ChangelogURL, resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading changelog response: %w", err)
	}

	entries, err := parse(string(raw))
	if err != nil {
		return nil, err
	}
	gained := between(entries, cursor, current)

	if persist {
		if err := st.SetLastSeenChangelog(current.Original()); err != nil {
			return gained, fmt.Errorf("advancing changelog cursor: %w", err)
		}
	}
	return gained, nil
}

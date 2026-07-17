package bundle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// ReceiptFile is the fetch-receipt filename written inside a cache entry's
// content root. Dot-prefixed, so component enumeration and the cache scan both
// skip it. Exported for bundletest, which plants cache entries by hand.
const ReceiptFile = ".fetch.yaml"

// fetchReceipt is the record of one cache entry's last fetch. The cache is
// value-keyed — the entry's directory name is the digest of the declared
// source — so the receipt is NEVER consulted to decide what resolves. It
// exists for display (`bundle list` naming an undeclared entry's source) and
// for update-compare (has the tracked ref moved since this fetch).
type fetchReceipt struct {
	// Canonical is the declared source value this entry was fetched from.
	Canonical string `yaml:"canonical"`
	// SHA is the commit the fetch resolved to.
	SHA string `yaml:"sha"`
	// FetchedAt is when the fetch committed.
	FetchedAt time.Time `yaml:"fetched_at"`
	// Version is the display version: the manifest version, else the resolved
	// commit.
	Version string `yaml:"version"`
}

// readReceipt loads a cache entry's fetch receipt. It reports absence without
// error — a hand-placed entry carries none.
func readReceipt(entryDir string) (fetchReceipt, bool, error) {
	raw, err := os.ReadFile(filepath.Join(entryDir, ReceiptFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fetchReceipt{}, false, nil
		}
		return fetchReceipt{}, false, fmt.Errorf("read %s: %w", ReceiptFile, err)
	}
	var r fetchReceipt
	if unmarshalErr := yaml.Unmarshal(raw, &r); unmarshalErr != nil {
		return fetchReceipt{}, false, fmt.Errorf("parse %s in %s: %w", ReceiptFile, entryDir, unmarshalErr)
	}
	return r, true, nil
}

// writeReceipt persists a fetch receipt into dir.
func writeReceipt(dir string, r fetchReceipt) error {
	raw, err := yaml.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", ReceiptFile, err)
	}
	if writeErr := os.WriteFile(filepath.Join(dir, ReceiptFile), raw, 0o600); writeErr != nil {
		return fmt.Errorf("write %s: %w", ReceiptFile, writeErr)
	}
	return nil
}

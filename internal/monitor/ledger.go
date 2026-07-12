package monitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/config"
)

// UnitsLedgerFile is the name of the seeded-unit ledger under the monitor
// subdirectory. The ledger records every monitoring unit ever seeded onto the
// host stack across all projects, so `monitor up` can regenerate the collector
// config over the all-ever-seeded union rather than just the current project's
// projection — a foreign project's routings survive this project's up. It is
// engine-owned derived state (not a config layer, not a lockfile); a
// `monitor down --volumes` deletes it when the seeded REST state it tracks is
// wiped.
const UnitsLedgerFile = "units-ledger.yaml"

// SeededUnit is one entry in the units ledger: the identity, provenance, and a
// content snapshot of a monitoring unit that has been seeded onto the host
// stack. The Manifest snapshot is what lets collector-config regeneration range
// over the union without re-reading a foreign project's on-disk artifact tree —
// only the currently-resolvable projection contributes bootstrap artifacts, but
// every ever-seeded unit contributes collector routing from its snapshot.
type SeededUnit struct {
	Name        string                        `yaml:"name"`
	Source      string                        `yaml:"source"`
	ProjectRoot string                        `yaml:"project_root"`
	ContentHash string                        `yaml:"content_hash"`
	Manifest    config.MonitoringUnitManifest `yaml:"manifest"`
	SeededAt    time.Time                     `yaml:"seeded_at"`
}

// ledgerFile is the on-disk shape of the units ledger.
type ledgerFile struct {
	Units map[string]SeededUnit `yaml:"units"`
}

// Ledger is the loaded units ledger, keyed by selection spelling (bare name for
// a floor/loose unit, dotted namespace.bundle.component address for a bundled
// one).
type Ledger struct {
	units map[string]SeededUnit
}

// SeedCollisionError reports a C5 monitoring collision: a bare-named loose
// unit already seeded by one project is being re-seeded with different content
// from another project. The seed is refused — a silent (or warned-but-taken)
// overwrite would let one project's stack artifacts clobber another's.
type SeedCollisionError struct {
	Name     string
	PrevRoot string
	NewRoot  string
}

func (e *SeedCollisionError) Error() string {
	return fmt.Sprintf(
		"monitoring extension %q from %s collides with the same-named extension already seeded from %s — rename one of the extensions, or reset the seeded stack with 'clawker monitor down --volumes'",
		e.Name,
		e.NewRoot,
		e.PrevRoot,
	)
}

// NewLedger returns an empty in-memory ledger.
func NewLedger() *Ledger {
	return &Ledger{units: map[string]SeededUnit{}}
}

// LoadLedger reads the units ledger from the monitor subdirectory. A missing
// ledger is an empty ledger, not an error — the first `monitor up` on a host
// starts from nothing.
func LoadLedger(monitorDir string) (*Ledger, error) {
	raw, err := os.ReadFile(filepath.Join(monitorDir, UnitsLedgerFile))
	if err != nil {
		if os.IsNotExist(err) {
			return &Ledger{units: map[string]SeededUnit{}}, nil
		}
		return nil, fmt.Errorf("monitor: read units ledger: %w", err)
	}
	var lf ledgerFile
	if unmarshalErr := yaml.Unmarshal(raw, &lf); unmarshalErr != nil {
		return nil, fmt.Errorf("monitor: parse units ledger: %w", unmarshalErr)
	}
	if lf.Units == nil {
		lf.Units = map[string]SeededUnit{}
	}
	return &Ledger{units: lf.Units}, nil
}

// Save writes the ledger to the monitor subdirectory via a temp-file rename so a
// partial write can never leave a corrupt ledger.
func (l *Ledger) Save(monitorDir string) error {
	raw, err := yaml.Marshal(ledgerFile{Units: l.units})
	if err != nil {
		return fmt.Errorf("monitor: encode units ledger: %w", err)
	}
	if mkErr := os.MkdirAll(monitorDir, bootstrapDirMode); mkErr != nil {
		return fmt.Errorf("monitor: ensure monitor dir: %w", mkErr)
	}
	final := filepath.Join(monitorDir, UnitsLedgerFile)
	tmp, err := os.CreateTemp(monitorDir, UnitsLedgerFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("monitor: stage units ledger: %w", err)
	}
	tmpName := tmp.Name()
	if _, writeErr := tmp.Write(raw); writeErr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("monitor: write units ledger: %w", writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("monitor: close units ledger: %w", closeErr)
	}
	if renameErr := os.Rename(tmpName, final); renameErr != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("monitor: commit units ledger: %w", renameErr)
	}
	return nil
}

// Union returns every seeded unit in the ledger, sorted by name — the
// all-ever-seeded set that drives collector-config regeneration.
func (l *Ledger) Union() []SeededUnit {
	out := make([]SeededUnit, 0, len(l.units))
	for _, u := range l.units {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Merge folds the current project's resolved projection into the ledger.
// Merge semantics: an identical-content re-seed (same content hash) is a
// no-op; a different-content re-seed from the SAME project root is an in-place
// update (a project editing its own loose unit); a different-content re-seed
// of a bare-named unit from a DIFFERENT project root is a C5 collision — the
// seed is REFUSED with a *SeedCollisionError and the ledger is left
// unmodified. Qualified (bundled) units are collision-proof by construction,
// so they never collide.
func (l *Ledger) Merge(units []ResolvedUnit, now time.Time) error {
	for _, u := range units {
		prev, seen := l.units[u.Name]
		if seen && prev.ContentHash == u.ContentHash {
			continue
		}
		if seen && !u.Qualified && prev.ProjectRoot != u.ProjectRoot {
			return &SeedCollisionError{
				Name:     u.Name,
				PrevRoot: prev.ProjectRoot,
				NewRoot:  u.ProjectRoot,
			}
		}
	}
	for _, u := range units {
		if prev, seen := l.units[u.Name]; seen && prev.ContentHash == u.ContentHash {
			continue
		}
		l.units[u.Name] = SeededUnit{
			Name:        u.Name,
			Source:      u.Source,
			ProjectRoot: u.ProjectRoot,
			ContentHash: u.ContentHash,
			Manifest:    u.Unit.Manifest,
			SeededAt:    now,
		}
	}
	return nil
}

// Advisory-lock acquisition bounds for the units ledger: give up after
// ledgerLockTimeout, retrying every ledgerLockRetryInterval.
const (
	ledgerLockTimeout       = 10 * time.Second
	ledgerLockRetryInterval = 100 * time.Millisecond
)

// SeedLedger folds units into the persisted ledger as one flock-guarded
// load-merge-save critical section. `monitor up` persists through this — not
// through an in-memory Ledger held across the compose bring-up — so two
// concurrent ups from different projects cannot lost-update each other's seeds
// out of the union. A C5 collision from this authoritative merge (possible
// when a concurrent up seeded between the caller's pre-render check and here)
// refuses the seed and surfaces as an error.
func SeedLedger(ctx context.Context, monitorDir string, units []ResolvedUnit, now time.Time) error {
	fl := flock.New(filepath.Join(monitorDir, UnitsLedgerFile) + ".lock")
	lockCtx, cancel := context.WithTimeout(ctx, ledgerLockTimeout)
	defer cancel()
	locked, err := fl.TryLockContext(lockCtx, ledgerLockRetryInterval)
	if err != nil {
		return fmt.Errorf("monitor: lock units ledger: %w", err)
	}
	if !locked {
		return fmt.Errorf("monitor: timed out locking units ledger in %s", monitorDir)
	}
	// Unlock error is unactionable in deferred cleanup: the flock is released by
	// the OS on process exit regardless, and the write outcome is already decided.
	defer func() { _ = fl.Unlock() }() //nolint:errcheck // see comment above

	ledger, err := LoadLedger(monitorDir)
	if err != nil {
		return err
	}
	if mergeErr := ledger.Merge(units, now); mergeErr != nil {
		return mergeErr
	}
	return ledger.Save(monitorDir)
}

// hashUnit computes a stable content hash over a monitoring unit's manifest and
// every artifact file, so a re-seed of identical content is detectably a no-op
// and an edited loose unit is detectably changed.
func hashUnit(u *MonitoringUnit) (string, error) {
	h := sha256.New()
	manifestYAML, err := yaml.Marshal(u.Manifest)
	if err != nil {
		return "", fmt.Errorf("monitor: hash unit %q manifest: %w", u.Name, err)
	}
	if _, writeErr := fmt.Fprintf(h, "manifest\x00%s\x00", manifestYAML); writeErr != nil {
		return "", fmt.Errorf("monitor: hash unit %q: %w", u.Name, writeErr)
	}
	// WalkArtifacts yields files in a deterministic (fs.WalkDir lexical) order,
	// so the digest is stable across runs for identical content.
	walkErr := u.WalkArtifacts(func(relPath string, content []byte) error {
		if _, headerErr := fmt.Fprintf(h, "artifact\x00%s\x00%d\x00", relPath, len(content)); headerErr != nil {
			return fmt.Errorf("hash header: %w", headerErr)
		}
		if _, contentErr := h.Write(content); contentErr != nil {
			return fmt.Errorf("hash content: %w", contentErr)
		}
		return nil
	})
	if walkErr != nil {
		return "", fmt.Errorf("monitor: hash unit %q artifacts: %w", u.Name, walkErr)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

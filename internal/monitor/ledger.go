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
	Name string `yaml:"name"`
	// Source is the human provenance clause; SourceKey is the machine
	// ownership identity (see ResolvedUnit.SourceKey). An empty SourceKey
	// (hand-edited or corrupt ledger — nothing ever wrote the file without
	// one) matches no real source, so such entries follow the plain
	// foreign-source rules.
	Source      string                        `yaml:"source"`
	SourceKey   string                        `yaml:"source_key,omitempty"`
	ProjectRoot string                        `yaml:"project_root"`
	ContentHash string                        `yaml:"content_hash"`
	Manifest    config.MonitoringUnitManifest `yaml:"manifest"`
	// ClusterObjects snapshots the unit's cluster-scoped OpenSearch object
	// claims so ValidateSeededSet can refuse a cross-project name reuse with
	// different content (a silent last-write-wins PUT otherwise).
	ClusterObjects []ClusterObject `yaml:"cluster_objects,omitempty"`
	SeededAt       time.Time       `yaml:"seeded_at"`
}

// ledgerFile is the on-disk shape of the units ledger.
type ledgerFile struct {
	Units map[string]SeededUnit `yaml:"units"`
}

// Ledger is the loaded units ledger, keyed by unit identity: the bare name for
// a floor/loose unit (one entry per name — a bare name is one cluster-wide
// namespace), the dotted namespace.bundle.component address plus source key
// for a bundled one (sibling entries per pinned value). See ledgerKey.
type Ledger struct {
	units map[string]SeededUnit
}

// SeedCollisionError reports a monitoring seed collision: a bare-named unit
// already seeded from one content source is being re-seeded with different
// content from a DIFFERENT source by a different project. The seed is refused —
// a silent (or warned-but-taken) overwrite would let one project's stack
// artifacts clobber another's. Host-global sources (the embedded floor, the
// shared user dir) are one source everywhere, so they update in place and never
// reach this error; a collision therefore always involves at least one loose
// extension directory the user can rename or remove.
type SeedCollisionError struct {
	Name       string
	PrevSource string // human provenance clause of the seeded entry
	NewSource  string // human provenance clause of the refused seed
	PrevRoot   string
	NewRoot    string
}

func (e *SeedCollisionError) Error() string {
	return fmt.Sprintf(
		"monitoring extension %q from %s has different content than the same-named extension already seeded from %s (project %q) — "+
			"a bare extension name is one cluster-wide namespace, so the stack can carry only one content per name; "+
			"rename or remove the loose extension directory on one side, or reset the seeded stack with "+
			"'clawker monitor down --volumes' (this deletes indexed telemetry)",
		e.Name,
		e.NewSource,
		e.PrevSource,
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

// Union returns every seeded unit in the ledger, sorted by name (source key
// breaks the tie between sibling pins of one address) — the all-ever-seeded
// set that drives collector-config regeneration.
func (l *Ledger) Union() []SeededUnit {
	out := make([]SeededUnit, 0, len(l.units))
	for _, u := range l.units {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].SourceKey < out[j].SourceKey
	})
	return out
}

// ledgerKeySep joins a qualified address and its source key into a ledger map
// key. Addresses never contain '@' (segment charset is the shared name rule),
// so keys cannot alias.
const ledgerKeySep = "@"

// ledgerKey is a unit's identity in the ledger map. A bare unit is keyed by
// its name alone — the bare name is one cluster-wide namespace, so one entry
// carries it. A qualified unit is keyed by address + source key, so two
// projects pinning one address to different values seed sibling entries — one
// project's pin never overwrites another's (the bundle model's value-keyed
// coexistence promise).
func ledgerKey(u ResolvedUnit) string {
	if u.Qualified {
		return u.Name + ledgerKeySep + u.SourceKey
	}
	return u.Name
}

// Merge folds the current project's resolved projection into the ledger.
//
// Bare units (one ledger entry per name): an identical-content re-seed is a
// no-op; a different-content re-seed from the same content source (a floor
// unit after a CLI upgrade, an edited shared user dir, a project's own loose
// dir) or from the same project root (a project shadowing its earlier
// selection with a loose override) is an in-place update; a different-content
// re-seed from a DIFFERENT source by a DIFFERENT project is a seed collision —
// the whole batch is REFUSED with a *SeedCollisionError and the ledger is left
// unmodified.
//
// Qualified units (one ledger entry per address + source key): a re-seed of
// the same declared value updates in place; a different value seeds a sibling
// entry, after retiring any stale entry the SAME project seeded for that
// address under a previous pin (so an upgrade with unchanged index names never
// fights its own ghost). Genuine cross-pin conflicts are resource-level and
// surface in ValidateSeededSet, not here.
//
// A source_key-less entry (hand-edited or corrupt — no version ever wrote the
// ledger without source keys) gets no special treatment: the empty key matches
// no real source, so the project-root comparison governs.
func (l *Ledger) Merge(units []ResolvedUnit, now time.Time) error {
	// Collision scan first: the batch applies atomically or not at all.
	for _, u := range units {
		prev, seen := l.units[u.Name]
		if !seen || u.Qualified || prev.ContentHash == u.ContentHash {
			continue
		}
		if prev.SourceKey == u.SourceKey || prev.ProjectRoot == u.ProjectRoot {
			continue
		}
		return &SeedCollisionError{
			Name:       u.Name,
			PrevSource: prev.Source,
			NewSource:  u.Source,
			PrevRoot:   prev.ProjectRoot,
			NewRoot:    u.ProjectRoot,
		}
	}
	for _, u := range units {
		if u.Qualified {
			l.applyQualified(u, now)
			continue
		}
		l.applyBare(u, now)
	}
	return nil
}

// applyBare upserts a bare unit at its name key.
func (l *Ledger) applyBare(u ResolvedUnit, now time.Time) {
	prev, seen := l.units[u.Name]
	// Identical content already seeded from another source (e.g. a loose copy
	// of the floor unit): the existing entry already carries the routing —
	// keep its provenance rather than flip ownership. A source_key-less entry
	// with identical content is overwritten: the content matches, so stamping
	// the resolved identity cannot clobber anything.
	if seen && prev.ContentHash == u.ContentHash && prev.SourceKey != "" && prev.SourceKey != u.SourceKey {
		return
	}
	l.units[u.Name] = newSeededUnit(u, now)
}

// applyQualified upserts a qualified unit at its address+source key, retiring
// the seeding project's OWN stale pins of the same address (same project root,
// different declared value). Entries from other roots are never touched —
// including identical-content ones: two projects whose declared values fetch
// the same bytes each keep their own entry, so neither project's later re-pin
// can unroute the other (deduping them would make one entry the other
// project's silent proxy).
func (l *Ledger) applyQualified(u ResolvedUnit, now time.Time) {
	key := ledgerKey(u)
	for k, prev := range l.units {
		if k == key || prev.Name != u.Name {
			continue
		}
		if prev.ProjectRoot == u.ProjectRoot {
			delete(l.units, k)
		}
	}
	l.units[key] = newSeededUnit(u, now)
}

// newSeededUnit snapshots a resolved unit into its ledger entry.
func newSeededUnit(u ResolvedUnit, now time.Time) SeededUnit {
	return SeededUnit{
		Name:           u.Name,
		Source:         u.Source,
		SourceKey:      u.SourceKey,
		ProjectRoot:    u.ProjectRoot,
		ContentHash:    u.ContentHash,
		Manifest:       u.Unit.Manifest,
		ClusterObjects: u.ClusterObjects,
		SeededAt:       now,
	}
}

// Advisory-lock acquisition bounds for the units ledger: give up after
// ledgerLockTimeout, retrying every ledgerLockRetryInterval.
const (
	ledgerLockTimeout       = 10 * time.Second
	ledgerLockRetryInterval = 100 * time.Millisecond
)

// SeedLedger folds units into the persisted ledger as one flock-guarded
// load-merge-validate-save critical section. `monitor up` persists through
// this — not through an in-memory Ledger held across the compose bring-up — so
// two concurrent ups from different projects cannot lost-update each other's
// seeds out of the union. A seed collision or a resource conflict from this
// authoritative merge (possible when a concurrent up seeded between the
// caller's pre-render check and here) refuses the seed and surfaces as an
// error rather than persisting a conflicting union.
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
	// Re-validate the post-merge union under the lock: a concurrent up may have
	// seeded a conflicting resource between the caller's pre-render check and
	// this authoritative merge. A conflicting union must never persist.
	if validateErr := ValidateSeededSet(ledger.Union()); validateErr != nil {
		return validateErr
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
